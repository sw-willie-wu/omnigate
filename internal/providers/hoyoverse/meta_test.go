package hoyoverse

import (
	"reflect"
	"testing"

	"omnigate/internal/core"
)

// TestGameMetaAudioAndSophon pins the per-game constants the Sophon audio
// path depends on. The Star Rail values were read from the real 4.6.0
// Sophon manifests (2026-09-28): every audio category lists its files under
// StarRail_Data/Persistent/Audio/AudioPackage/Windows/<Folder>/.
func TestGameMetaAudioAndSophon(t *testing.T) {
	hsr := findByID(core.GameID("hoyoverse/starrail"))
	if hsr == nil {
		t.Fatal("starrail not registered")
	}
	if !hsr.UsesSophon {
		t.Errorf("starrail UsesSophon = false, want true (HSR moved to Sophon at 4.6)")
	}
	if hsr.PlatApp != "" {
		t.Errorf("starrail PlatApp = %q, want \"\" (server ignores plat_app)", hsr.PlatApp)
	}
	if hsr.AudioAssetsRel != "StarRail_Data/Persistent/Audio/AudioPackage/Windows" {
		t.Errorf("starrail AudioAssetsRel = %q", hsr.AudioAssetsRel)
	}
	wantHSR := map[string]string{"zh-cn": "Chinese(PRC)", "en-us": "English", "ja-jp": "Japanese", "ko-kr": "Korean"}
	if !reflect.DeepEqual(hsr.AudioFolders, wantHSR) {
		t.Errorf("starrail AudioFolders = %v, want %v", hsr.AudioFolders, wantHSR)
	}
	if hsr.AudioRecordRel != "StarRail_Data/Persistent/AudioLaucherRecord.txt" {
		t.Errorf("starrail AudioRecordRel = %q", hsr.AudioRecordRel)
	}

	gi := findByID(core.GameID("hoyoverse/genshin"))
	if gi == nil {
		t.Fatal("genshin not registered")
	}
	if gi.AudioAssetsRel != "GenshinImpact_Data/StreamingAssets/AudioAssets" {
		t.Errorf("genshin AudioAssetsRel = %q", gi.AudioAssetsRel)
	}
	wantGI := map[string]string{"zh-cn": "Chinese", "en-us": "English(US)", "ja-jp": "Japanese", "ko-kr": "Korean"}
	if !reflect.DeepEqual(gi.AudioFolders, wantGI) {
		t.Errorf("genshin AudioFolders = %v, want %v", gi.AudioFolders, wantGI)
	}
	if gi.AudioRecordRel != "" {
		t.Errorf("genshin AudioRecordRel = %q, want \"\"", gi.AudioRecordRel)
	}
	// The package-level legacy table must stay in lockstep with the meta
	// table so Genshin's Sophon plan is unchanged by the per-game lookup.
	if !reflect.DeepEqual(audioLangToFolder, wantGI) {
		t.Errorf("audioLangToFolder = %v, want %v", audioLangToFolder, wantGI)
	}

	zzz := findByID(core.GameID("hoyoverse/zzz"))
	if zzz == nil {
		t.Fatal("zzz not registered")
	}
	if zzz.UsesSophon {
		t.Errorf("zzz UsesSophon = true, want false (still legacy)")
	}
	if zzz.AudioAssetsRel != "" || len(zzz.AudioFolders) != 0 || zzz.AudioRecordRel != "" {
		t.Errorf("zzz audio meta should be empty: %q %v %q", zzz.AudioAssetsRel, zzz.AudioFolders, zzz.AudioRecordRel)
	}
}
