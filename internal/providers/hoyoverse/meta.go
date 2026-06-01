package hoyoverse

import "omnigate/internal/core"

const (
	BackendID  core.BackendID = "hoyoverse"
	LauncherID                = "VYTpXlbWo8"
	APIBase                   = "https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api"
	UserAgent                 = "omnigate/0.1 (+https://github.com/willie/omnigate)"
)

// gameMeta holds compile-time constants per supported game.
type gameMeta struct {
	ID         core.GameID
	APIGameID  string // HoYoverse API "game_id" param value
	Biz        string // e.g. "hk4e_global"
	FolderName string // subfolder under HoYoPlay/games/
	ExeName    string // launches via this exe
	Display    core.LocalizedString
	// UsesSophon flags games that HoYoverse migrated to the Sophon
	// chunk-level delta delivery protocol (Genshin 6.0+). For these games:
	//   - CheckVersion uses /getGameBranches.main.tag for the real latest
	//     (the legacy /getGamePackages endpoint is frozen at the last 5.x
	//     manifest and reports stale data).
	//   - CheckForUpdate returns a friendly sophon_not_supported error;
	//     M3.B v1's zip + hdiff + hpatchz pipeline cannot apply Sophon
	//     deltas. M3.B v2 (separate session) implements the Sophon path.
	// HSR/ZZZ stay on the legacy getGamePackages flow until HoYoverse
	// migrates them too.
	UsesSophon bool
}

var games = []gameMeta{
	{
		ID: "hoyoverse/genshin", APIGameID: "gopR6Cufr3", Biz: "hk4e_global",
		FolderName: "Genshin Impact game", ExeName: "GenshinImpact.exe",
		Display:    core.LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"},
		UsesSophon: true,
	},
	{
		ID: "hoyoverse/starrail", APIGameID: "4ziysqXOQ8", Biz: "hkrpg_global",
		FolderName: "Star Rail Games", ExeName: "StarRail.exe",
		Display: core.LocalizedString{"zh-TW": "崩壞：星穹鐵道", "en": "Honkai: Star Rail"},
	},
	{
		ID: "hoyoverse/zzz", APIGameID: "U5hbdsT9W7", Biz: "nap_global",
		FolderName: "ZenlessZoneZero Game", ExeName: "ZenlessZoneZero.exe",
		Display: core.LocalizedString{"zh-TW": "絕區零", "en": "Zenless Zone Zero"},
	},
}

// findByID returns the gameMeta for a GameID or nil if not registered.
func findByID(id core.GameID) *gameMeta {
	for i := range games {
		if games[i].ID == id {
			return &games[i]
		}
	}
	return nil
}
