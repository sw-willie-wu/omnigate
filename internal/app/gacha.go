package app

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

// gachaDBPathFor puts gacha.db beside the settings file (same dir convention as
// playstate). Mirrors playStatePathFor.
func gachaDBPathFor(settingsPath string) string {
	dir := filepath.Dir(settingsPath)
	if dir == "." || dir == "" {
		return "gacha.db"
	}
	return filepath.Join(dir, "gacha.db")
}

// gachaUIDFor resolves the gacha uid for a specific account. For switcher
// providers (WuWa): accountID=="" → the written-active account's uid; else the
// named account's uid (may be "" when unknown). For non-switcher providers it
// returns ("", false) so callers fall back to LatestUID (HoYoverse/Endfield
// unchanged). A switcher whose listing fails, or that has no matching account,
// returns ("", true) — unknown, never the LatestUID path (so we don't leak
// another account's records).
func (a *App) gachaUIDFor(gid core.GameID, accountID string) (uid string, isSwitcher bool) {
	accts, err := a.ListGameAccounts(string(gid))
	if err != nil {
		if errors.Is(err, core.ErrAccountSwitchUnsupported) {
			return "", false // non-switcher → LatestUID path
		}
		return "", true // switcher game but listing failed → unknown, never LatestUID
	}
	for _, ac := range accts {
		if accountID == "" {
			if ac.Active {
				return ac.UID, true
			}
		} else if ac.ID == accountID {
			return ac.UID, true
		}
	}
	return "", true
}

// gachaProgressPayload builds the Wails event payload for one progress tick:
// resolves the banner KEY to its localized label from the game's config (so the
// frontend localizes with its own helper), plus page/pool counters. Pure +
// unit-testable. Carries no token/URL.
func gachaProgressPayload(cfg core.GachaConfig, p core.GachaProgress) map[string]any {
	var label core.LocalizedString
	for _, b := range cfg.Banners {
		if b.Key == p.BannerKey {
			label = b.Label
			break
		}
	}
	if label == nil {
		label = core.LocalizedString{"en": p.BannerKey}
	}
	return map[string]any{
		"banner": label, "page": p.Page, "poolIndex": p.PoolIndex, "poolTotal": p.PoolTotal,
	}
}

// RefreshGacha extracts the local history URL, fetches the record API, upserts
// into the store (dedup), and returns the recomputed summary. Network-touching.
func (a *App) RefreshGacha(gameID, accountID string) (core.GachaSummary, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	gp, ok := p.(core.GachaProvider)
	if !ok {
		return core.GachaSummary{Supported: false}, nil
	}
	if a.gachaStore == nil {
		return core.GachaSummary{Supported: false}, nil
	}

	a.settingsMu.RLock()
	installDir := a.resolved[gid].Path
	a.settingsMu.RUnlock()

	game := string(gid)

	uid, isSwitcher := a.gachaUIDFor(gid, accountID)
	if isSwitcher && uid == "" {
		return core.GachaSummary{}, core.ErrGachaActiveUnknown
	}
	// Cached URL comes from the EXPECTED account's partition (switcher) or the
	// latest uid (non-switcher, unchanged).
	cacheUID := uid
	if !isSwitcher {
		cacheUID, _ = a.gachaStore.LatestUID(game)
	}
	cachedURL := ""
	if cacheUID != "" {
		cachedURL, _, _ = a.gachaStore.GetURLCache(game, cacheUID)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Stream pagination progress to the UI (banner/page/pool — never the URL).
	cfg := gp.GachaConfig(gid)
	ctx = core.WithGachaProgress(ctx, func(p core.GachaProgress) {
		a.emit("gacha:progress", gameID, gachaProgressPayload(cfg, p))
	})

	res, err := gp.FetchGacha(ctx, gid, installDir, cachedURL)
	if err != nil {
		// Do NOT log res.URL anywhere — it carries a live token.
		a.logger.Warn("RefreshGacha fetch failed", "gid", gameID, "code", core.ErrorCode(err))
		if isSwitcher && errors.Is(err, core.ErrGachaURLUnavailable) {
			return core.GachaSummary{}, core.ErrGachaURLExpired
		}
		return core.GachaSummary{}, err
	}
	if isSwitcher && res.UID != uid {
		// The convene URL in the log belongs to a different account than the
		// active one — guide the user, do not attribute pulls to the wrong uid.
		return core.GachaSummary{}, core.ErrGachaWrongAccount
	}
	if _, err := a.gachaStore.UpsertPulls(game, res.UID, res.Pulls); err != nil {
		return core.GachaSummary{}, err
	}
	if res.URL != "" {
		// Best-effort: a cache-write failure must not fail the refresh (pulls are
		// already persisted); next refresh just re-extracts the URL from the log.
		_ = a.gachaStore.PutURLCache(game, res.UID, res.URL)
	}
	all, err := a.gachaStore.AllPulls(game, res.UID)
	if err != nil {
		return core.GachaSummary{}, err
	}
	return core.ComputeSummary(res.UID, all, gp.GachaConfig(gid)), nil
}

// GetGachaSummary reads the store (no network) and computes the summary for the
// game's relevant uid: the ACTIVE account's uid for switcher providers (WuWa),
// or LatestUID for everyone else (HoYoverse/Endfield). For switcher games it
// also reads the local account state (LocalStorage.db + KRSDK cache) and may
// persist the uid cache as a side effect of ListGameAccounts. Empty active uid
// on a switcher → supported summary with ActiveUnknown=true (play-first).
func (a *App) GetGachaSummary(gameID, accountID string) (core.GachaSummary, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	gp, ok := p.(core.GachaProvider)
	if !ok || a.gachaStore == nil {
		return core.GachaSummary{Supported: false}, nil
	}
	game := string(gid)
	uid, isSwitcher := a.gachaUIDFor(gid, accountID)
	if !isSwitcher {
		uid, _ = a.gachaStore.LatestUID(game)
	}
	if uid == "" {
		empty := core.GachaSummary{Supported: true, PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
			Pity: []core.BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []core.HeadlineEntry{}}
		empty.ActiveUnknown = isSwitcher // play-first only for switcher games
		return empty, nil
	}
	all, err := a.gachaStore.AllPulls(game, uid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	return core.ComputeSummary(uid, all, gp.GachaConfig(gid)), nil
}
