package hoyoverse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func loadSampleManifest(t *testing.T) *HypGetGamePackagesResponse {
	t.Helper()
	var rawBody []byte
	var err error
	for _, path := range []string{
		"testdata/manifest-sample.json",
		"./testdata/manifest-sample.json",
		"internal/providers/hoyoverse/testdata/manifest-sample.json",
	} {
		rawBody, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("could not read testdata: %v", err)
	}
	// Extract the "data" field from the envelope
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}
	resp, err := parseGamePackagesResponse(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	resp.ManifestETag = "test-etag-1234"
	return resp
}

// stubFreeSpace lets preflight tests inject a known free-space value.
type stubFreeSpace struct{ bytes uint64 }

func (s *stubFreeSpace) FreeBytes(path string) (uint64, error) { return s.bytes, nil }

func TestBuildPlan_FlavorNone_NoVersionChange_NoAudioDrift(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{
		TargetVersion:  "5.7.0",
		AudioLanguages: []string{"Chinese"},
	}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}

	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	gp, predlAvail, err := buildPlan(context.Background(), resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorNone {
		t.Errorf("flavor = %v want flavorNone", gp.flavor)
	}
	_ = predlAvail
}

func TestBuildPlan_FlavorNone_FreshInstall_NoBaseline(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Korean"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	gp, _, err := buildPlan(context.Background(), resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorNone {
		t.Errorf("flavor = %v want flavorNone (fresh install, no baseline)", gp.flavor)
	}
}

func TestBuildPlan_FlavorPatch_VersionMatchesPatchEntry(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	gp, _, err := buildPlan(context.Background(), resp, gid, "5.6.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorPatch {
		t.Errorf("flavor = %v want flavorPatch", gp.flavor)
	}
	if gp.UpdatePlan.Kind != core.PlanUpdate {
		t.Errorf("Kind = %v want PlanUpdate (hoyoverse uses PlanUpdate for all flavors)", gp.UpdatePlan.Kind)
	}
	if gp.UpdatePlan.Reason != core.ReasonVersionChanged {
		t.Errorf("reason = %v want ReasonVersionChanged", gp.UpdatePlan.Reason)
	}
	if len(gp.UpdatePlan.Files) == 0 {
		t.Error("expected non-empty Files")
	}
}

func TestBuildPlan_FlavorFull_NoMatchingPatch(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	gp, _, err := buildPlan(context.Background(), resp, gid, "3.0.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorFull {
		t.Errorf("flavor = %v want flavorFull", gp.flavor)
	}
}

func TestBuildPlan_FlavorAudioOnly(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{
		TargetVersion:  "5.7.0",
		AudioLanguages: []string{"Chinese"},
	}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}

	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	for _, lang := range []string{"Chinese", "English(US)"} {
		if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, lang), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resp := loadSampleManifest(t)
	gp, _, err := buildPlan(context.Background(), resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorAudioOnly {
		t.Errorf("flavor = %v want flavorAudioOnly", gp.flavor)
	}
	if gp.UpdatePlan.Reason != core.ReasonAudioPackAdded {
		t.Errorf("reason = %v want ReasonAudioPackAdded", gp.UpdatePlan.Reason)
	}
	if len(gp.UpdatePlan.Files) == 0 {
		t.Error("expected non-empty Files (English(US) audio_pkg should be selected)")
	}
}

func TestBuildPlan_PredownloadAvailable(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{TargetVersion: "5.7.0", AudioLanguages: []string{"Chinese"}}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	gp, predlAvail, err := buildPlan(context.Background(), resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if gp.flavor != flavorNone {
		t.Errorf("flavor = %v want flavorNone (currentVer == mainMajor)", gp.flavor)
	}
	if !predlAvail {
		t.Error("expected predlAvail=true (pre_download in manifest)")
	}
}
