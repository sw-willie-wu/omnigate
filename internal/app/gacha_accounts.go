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

	acc := store.GachaAccount{
		ID:    newAccountID(),
		Game:  gameID,
		HgID:  res.HgID,
		Email: res.Email,
		Label: res.Email, // default label = email; user can rename via SetGachaAccountLabel
		Token: res.Token,
	}
	if err := a.gachaStore.UpsertGachaAccount(acc); err != nil {
		return store.GachaAccount{}, err
	}
	if err := a.gachaStore.SetActiveGachaAccount(gameID, acc.ID); err != nil {
		return store.GachaAccount{}, err
	}

	// Best-effort: fetch pulls and write back the roleId uid. If this fails the
	// account is already saved; the empty uid means the board is blank until the
	// user triggers a manual refresh.
	if err := a.refreshAndWriteBackUID(ctx, gid, &acc); err != nil {
		return acc, err
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

	res, err := cp.FetchGachaWithCredential(ctx, gid, acc.Token, mapEndfieldLang(uiLang))
	if err != nil {
		return err
	}
	if _, err := a.gachaStore.UpsertPulls(acc.Game, res.UID, res.Pulls); err != nil {
		return err
	}
	acc.UID = res.UID
	return a.gachaStore.UpsertGachaAccount(*acc) // write back roleId uid
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
