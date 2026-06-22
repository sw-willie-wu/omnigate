package app

import (
	"strings"

	"omnigate/internal/core"
)

// equipBanners mirrors the frontend isEquip() set (gachaHighlights.ts): high-rarity
// equipment banners → weapon side.
var equipBanners = map[string]bool{
	"weapon": true, "standard_weapon": true, "weapon_exchange": true,
	"collab_weapon": true, "lightcone": true, "wengine": true,
}

// decorateGachaIcons sets HeadlineEntry.Icon for each top-two-rarity record that
// resolves to an icon. nil manager → no-op (core summary unchanged).
func (a *App) decorateGachaIcons(gid core.GameID, sum *core.GachaSummary) {
	if a.gachaIcons == nil {
		return
	}
	backend := string(gid)
	game := gid
	if i := strings.IndexByte(backend, '/'); i >= 0 {
		game = core.GameID(backend[i+1:])
		backend = backend[:i]
	}
	for i := range sum.Highlights {
		h := &sum.Highlights[i]
		equip := equipBanners[h.BannerKey]
		e, ok := a.gachaIcons.Resolve(gid, h.Name, equip)
		if !ok {
			continue
		}
		h.Icon = "/_asset/" + backend + "/gachaicon/" + string(game) + "." + e.Kind + "." + e.ID
	}
}
