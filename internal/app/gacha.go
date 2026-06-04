package app

import (
	"context"
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

// RefreshGacha extracts the local history URL, fetches the record API, upserts
// into the store (dedup), and returns the recomputed summary. Network-touching.
func (a *App) RefreshGacha(gameID string) (core.GachaSummary, error) {
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
	latestUID, _ := a.gachaStore.LatestUID(game)
	cachedURL := ""
	if latestUID != "" {
		cachedURL, _, _ = a.gachaStore.GetURLCache(game, latestUID)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	res, err := gp.FetchGacha(ctx, gid, installDir, cachedURL)
	if err != nil {
		// Do NOT log res.URL anywhere — it carries a live token.
		a.logger.Warn("RefreshGacha fetch failed", "gid", gameID, "code", core.ErrorCode(err))
		return core.GachaSummary{}, err
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

// GetGachaSummary reads the store only (no network) and computes the summary for
// the latest known uid. Empty store → supported-but-empty summary.
func (a *App) GetGachaSummary(gameID string) (core.GachaSummary, error) {
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
	uid, _ := a.gachaStore.LatestUID(game)
	if uid == "" {
		return core.GachaSummary{Supported: true, PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
			Pity: []core.BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []core.HeadlineEntry{}}, nil
	}
	all, err := a.gachaStore.AllPulls(game, uid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	return core.ComputeSummary(uid, all, gp.GachaConfig(gid)), nil
}
