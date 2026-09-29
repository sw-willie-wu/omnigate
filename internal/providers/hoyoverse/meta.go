package hoyoverse

import "omnigate/internal/core"

const (
	BackendID  core.BackendID = "hoyoverse"
	LauncherID                = "VYTpXlbWo8"
	APIBase                   = "https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api"
	UserAgent                 = "omnigate/0.4 (+https://github.com/willie/omnigate)"

	// sophonChunkAPIBase is the getBuild / getPatchBuild host for the Sophon
	// chunk protocol (§2.1 / §A.4). Distinct from APIBase (getGameBranches).
	sophonChunkAPIBase = "https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api"
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
	// chunk-level delta delivery protocol: Genshin 6.0+ (2026-05) and
	// Star Rail 4.6+ (2026-09). For these games:
	//   - CheckVersion uses /getGameBranches.main.tag for the real latest
	//     (the legacy /getGamePackages endpoint is frozen at the last
	//     pre-migration manifest and reports stale data — Genshin 5.5.0,
	//     Star Rail 4.4.0).
	//   - CheckForUpdate / CheckForPredownload / RunUpdate route to the
	//     Sophon decision tree (update_sophon_*.go).
	// ZZZ stays on the legacy getGamePackages flow until HoYoverse
	// migrates it too.
	UsesSophon bool

	// PlatApp is the Sophon getBuild/getPatchBuild plat_app query param
	// (§2.1). Genshin global = "ddxf6vlr1reo". Star Rail leaves it "":
	// the live API returns a byte-identical getBuild body for "", the
	// Genshin value and garbage (verified 2026-09-28), so the parameter is
	// ignored server-side. ZZZ leaves it "" (legacy).
	PlatApp string

	// AudioAssetsRel is the voice-pack root relative to gameDir; one
	// subfolder per installed language. "" disables audio detection
	// (DetectInstalledLanguages returns an empty slice).
	AudioAssetsRel string
	// AudioFolders maps a Sophon matching_field ("zh-cn", "en-us", "ja-jp",
	// "ko-kr") to the on-disk subfolder name under AudioAssetsRel. Folder
	// names differ per game (Genshin "English(US)" vs Star Rail "English").
	AudioFolders map[string]string
	// OnDemandBlacklistRel is the game-maintained list of assets it downloads
	// on demand (JSON Lines, {"fileName":"<gameDir-relative, '/'-separated>"}).
	// When set, the Sophon planner skips a listed asset that is absent locally
	// (and its companion "<base>_<md5>.hash" marker) instead of fetching it —
	// mirroring HoYoPlay, which never fills in on-demand content on update.
	// "" = no such mechanism (Genshin, ZZZ).
	OnDemandBlacklistRel string
	// AudioRecordRel is an optional launcher-written install record
	// (relative to gameDir) listing the voice packs the launcher installed.
	// When present and parseable it takes precedence over the folder scan,
	// because folder presence cannot distinguish launcher-installed packs
	// from partial in-game on-demand downloads (Star Rail).
	AudioRecordRel string
}

var games = []gameMeta{
	{
		ID: "hoyoverse/genshin", APIGameID: "gopR6Cufr3", Biz: "hk4e_global",
		FolderName: "Genshin Impact game", ExeName: "GenshinImpact.exe",
		Display:        core.LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"},
		UsesSophon:     true,
		PlatApp:        "ddxf6vlr1reo",
		AudioAssetsRel: "GenshinImpact_Data/StreamingAssets/AudioAssets",
		AudioFolders:   map[string]string{"zh-cn": "Chinese", "en-us": "English(US)", "ja-jp": "Japanese", "ko-kr": "Korean"},
	},
	{
		ID: "hoyoverse/starrail", APIGameID: "4ziysqXOQ8", Biz: "hkrpg_global",
		FolderName: "Star Rail Games", ExeName: "StarRail.exe",
		Display: core.LocalizedString{"zh-TW": "崩壞：星穹鐵道", "en": "Honkai: Star Rail"},
		// Sophon since 4.6.0 (2026-09): /getGamePackages froze at 4.4.0.
		UsesSophon: true,
		PlatApp:    "",
		// Folder names read from the real 4.6.0 audio manifests
		// (zh-cn/en-us/ja-jp/ko-kr → 346 files each under this root).
		AudioAssetsRel: "StarRail_Data/Persistent/Audio/AudioPackage/Windows",
		AudioFolders:   map[string]string{"zh-cn": "Chinese(PRC)", "en-us": "English", "ja-jp": "Japanese", "ko-kr": "Korean"},
		// HoYoPlay's voice-pack install record (sic: "Laucher"). Folder
		// presence alone is misleading for Star Rail because the game also
		// drops partial on-demand audio into the same root.
		AudioRecordRel: "StarRail_Data/Persistent/AudioLaucherRecord.txt",
		// Written by the game itself (JSON Lines): on-demand assets it has
		// chosen not to download (cutscene .usm, unused voice .pck).
		OnDemandBlacklistRel: "StarRail_Data/Persistent/DownloadBlacklist.json",
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
