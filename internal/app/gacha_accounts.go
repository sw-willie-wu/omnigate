package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// gachaCtx returns a background context with a 120-second timeout, mirroring
// the idiom used in RefreshGacha. Caller must defer cancel().
func (a *App) gachaCtx() (context.Context, context.CancelFunc) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, 120*time.Second)
}

func newAccountID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ga_" + hex.EncodeToString(b)
}

// GameAccountKind reports how the frontend should source account UI for a game:
//   - "switcher"   — provider implements AccountSwitcher (WuWa)
//   - "credential" — provider implements GachaLoginProvider (Endfield)
//   - "none"       — no account concept
func (a *App) GameAccountKind(gameID string) string {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return "none"
	}
	if _, ok := p.(core.AccountSwitcher); ok {
		return "switcher"
	}
	if _, ok := p.(core.GachaLoginProvider); ok {
		return "credential"
	}
	return "none"
}

// ListGachaAccounts returns the stored credential accounts for a game.
func (a *App) ListGachaAccounts(gameID string) ([]store.GachaAccount, error) {
	if a.gachaStore == nil {
		return nil, nil
	}
	return a.gachaStore.ListGachaAccounts(gameID)
}

// AddGachaAccountByLogin logs in via email+password (password is never stored),
// persists the resulting token as a new GachaAccount, sets it active, then
// immediately refreshes and writes the roleId (res.UID) back onto the account.
// Returns the fully-populated account. If the write-back refresh fails, the
// account is still saved (UID="") so the user can retry via RefreshGacha.
func (a *App) AddGachaAccountByLogin(gameID, email, password string) (store.GachaAccount, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return store.GachaAccount{}, err
	}
	lp, ok := p.(core.GachaLoginProvider)
	if !ok {
		return store.GachaAccount{}, core.ErrGachaCredentialRequired
	}
	if a.gachaStore == nil {
		return store.GachaAccount{}, core.ErrGachaCredentialRequired
	}
	ctx, cancel := a.gachaCtx()
	defer cancel()

	res, err := lp.LoginByEmailPassword(ctx, email, password)
	if err != nil {
		// Log only the error code — never log email, password, or token.
		a.logger.Warn("gacha login failed", "gid", gameID, "code", core.ErrorCode(err))
		return store.GachaAccount{}, err
	}

	hgID, resolvedEmail, label := a.resolveCredentialIdentity(ctx, gid, res.Token, res.HgID, res.Email)
	acc, err := a.upsertDedupedCredentialAccount(gameID, hgID, resolvedEmail, label, res.Token)
	if err != nil {
		return store.GachaAccount{}, err
	}

	// Return immediately after login — do NOT block on the (potentially slow, ~80s)
	// record fetch. The account is saved with uid=""; the frontend closes the login
	// modal and the gacha board drives the refresh-with-progress for the new account
	// (which writes back the roleId uid). See GachaBoard.loadForSelection.
	return acc, nil
}

// resolveCredentialIdentity fetches passport identity for a durable token (best-effort).
// label = nickName > realEmail. On FetchUserInfo failure, fall back to fb* and label=fbEmail;
// log only the error CODE (never err.Error()/token).
func (a *App) resolveCredentialIdentity(ctx context.Context, gid core.GameID, token, fbHgID, fbEmail string) (hgID, email, label string) {
	hgID, email = fbHgID, fbEmail
	label = email
	p, _ := a.provider(gid)
	if uip, ok := p.(core.GachaUserInfoProvider); ok {
		if info, err := uip.FetchUserInfo(ctx, token); err == nil {
			if info.HgID != "" {
				hgID = info.HgID
			}
			if info.RealEmail != "" {
				email = info.RealEmail
			}
			if info.NickName != "" {
				label = info.NickName
			} else {
				label = email
			}
		} else {
			a.logger.Warn("gacha user/info failed", "gid", gid, "code", core.ErrorCode(err))
		}
	}
	return hgID, email, label
}

// upsertDedupedCredentialAccount upserts a credential account, deduping by HgID. label is the
// fresh API value (overwritten each login). An existing row's CustomLabel (user alias) is
// PRESERVED onto the returned acc so the subsequent refresh write-back doesn't null it.
func (a *App) upsertDedupedCredentialAccount(gameID, hgID, email, label, token string) (store.GachaAccount, error) {
	id := newAccountID()
	custom := ""
	if existing, err := a.gachaStore.ListGachaAccounts(gameID); err == nil {
		for _, x := range existing {
			if hgID != "" && x.HgID == hgID {
				id = x.ID
				custom = x.CustomLabel
				break
			}
		}
	}
	acc := store.GachaAccount{ID: id, Game: gameID, HgID: hgID, Email: email, Label: label, CustomLabel: custom, Token: token}
	if err := a.gachaStore.UpsertGachaAccount(acc); err != nil {
		return store.GachaAccount{}, err
	}
	if err := a.gachaStore.SetActiveGachaAccount(gameID, acc.ID); err != nil {
		return store.GachaAccount{}, err
	}
	return acc, nil
}

// refreshAndWriteBackUID calls FetchGachaWithCredential with the account's token,
// persists the returned pulls, and writes the resulting roleId (res.UID) back
// onto the account row. ctx is caller-supplied so both AddGachaAccountByLogin
// (plain timeout ctx) and future RefreshGacha integration (progress-wired ctx)
// can share this path.
func (a *App) refreshAndWriteBackUID(ctx context.Context, gid core.GameID, acc *store.GachaAccount) error {
	p, _ := a.provider(gid)
	cp, ok := p.(core.GachaCredentialProvider)
	if !ok {
		return core.ErrGachaCredentialRequired
	}

	a.settingsMu.RLock()
	uiLang := a.settings.App.Language
	a.settingsMu.RUnlock()

	// Incremental sync: tell the provider which pulls we already have so it stops
	// paginating once it reaches them (only the first sync fetches the full history).
	// Empty until the account's uid is known (a brand-new account fetches in full).
	known := map[string]bool{}
	if acc.UID != "" {
		existing, _ := a.gachaStore.AllPulls(acc.Game, acc.UID)
		// Collect PerPool banner keys (e.g. Endfield 特許尋訪). A stored PerPool pull
		// with an empty PoolID predates per-pool capture; force ONE full re-fetch so
		// UpsertPulls can backfill the poolId. Scoped to PerPool pulls only: non-PerPool
		// pools (standard/beginner/joint) may never return a poolId, so their empty rows
		// must not wedge us into perpetual full re-fetches.
		perPool := map[string]bool{}
		if gp, ok := p.(core.GachaProvider); ok {
			for _, b := range gp.GachaConfig(gid).Banners {
				if b.PerPool {
					perPool[b.Key] = true
				}
			}
		}
		needsBackfill := false
		for _, pull := range existing {
			known[pull.ID] = true
			if perPool[pull.BannerKey] && pull.PoolID == "" {
				needsBackfill = true
			}
		}
		if needsBackfill {
			known = nil // force full re-fetch to backfill poolId on PerPool pulls
		}
	}

	res, err := cp.FetchGachaWithCredential(ctx, gid, acc.Token, mapEndfieldLang(uiLang), known)
	if err != nil {
		return err
	}
	if _, err := a.gachaStore.UpsertPulls(acc.Game, res.UID, res.Pulls); err != nil {
		return err
	}
	acc.UID = res.UID
	return a.gachaStore.UpsertGachaAccount(*acc) // write back roleId uid
}

// resolveGachaAccount picks the account for a credential game: the named one, else
// the active one, else the first. Returns ErrGachaCredentialRequired when none exists.
func (a *App) resolveGachaAccount(game, accountID string) (store.GachaAccount, error) {
	accts, err := a.gachaStore.ListGachaAccounts(game)
	if err != nil {
		return store.GachaAccount{}, err
	}
	if len(accts) == 0 {
		return store.GachaAccount{}, core.ErrGachaCredentialRequired
	}
	if accountID != "" {
		for _, x := range accts {
			if x.ID == accountID {
				return x, nil
			}
		}
	}
	for _, x := range accts {
		if x.Active {
			return x, nil
		}
	}
	return accts[0], nil
}

// emptyCredentialSummary returns the zeroed-but-supported summary used when a
// credential account exists but has not yet been refreshed (UID==""). Mirrors the
// uid=="" empty shape in GetGachaSummary, with ActiveUnknown=false (credential
// games are not play-first switchers).
func (a *App) emptyCredentialSummary() core.GachaSummary {
	return core.GachaSummary{
		Supported: true, PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
		Pity: []core.BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []core.HeadlineEntry{},
	}
}

// SelectGachaAccount marks an account as the active default for a game.
func (a *App) SelectGachaAccount(gameID, accountID string) error {
	if a.gachaStore == nil {
		return nil
	}
	return a.gachaStore.SetActiveGachaAccount(gameID, accountID)
}

// SetGachaAccountLabel renames an account (the default label is the login email).
func (a *App) SetGachaAccountLabel(gameID, accountID, label string) error {
	if a.gachaStore == nil {
		return nil
	}
	return a.gachaStore.SetGachaAccountLabel(accountID, label)
}

// DeleteGachaAccount removes an account row. Pull records stored under the
// account's uid partition are left intact.
func (a *App) DeleteGachaAccount(gameID, accountID string) error {
	if a.gachaStore == nil {
		return nil
	}
	return a.gachaStore.DeleteGachaAccount(accountID)
}
