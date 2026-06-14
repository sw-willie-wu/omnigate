package hoyoverse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestPlanSnapshot_FlavorRoundTrip(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	ps, err := newProgressStore(tempRoot, gid, "4.4.0", "etag-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("game.zip", 10, time.Now(), "abc"); err != nil {
		t.Fatal(err)
	}
	snap := planSnapshot{
		SourceVersion: "4.3.0", TargetVersion: "4.4.0",
		Files:  []core.FileTask{{Path: "game.zip", Hash: "abc", Size: 10}},
		Flavor: flavorPredlPatch.String(),
	}
	if err := ps.RenameToPredlReady(snap); err != nil {
		t.Fatal(err)
	}
	prf, err := loadJSONSidecar[predlReadyFile](filepath.Join(versionSidecarDir(tempRoot, gid, "4.4.0"), "predl_ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if prf == nil {
		t.Fatal("predl_ready.json not found")
	}
	if prf.PlanSnapshot.Flavor != "predl_patch" {
		t.Errorf("Flavor = %q, want predl_patch", prf.PlanSnapshot.Flavor)
	}
}

func predlResp(currentInPatches bool) *HypGetGamePackagesResponse {
	predl := &HypGamePackagesMain{
		Major: HypPackageInfo{
			Version:  "4.4.0",
			GamePkgs: []HypPackageData{{URL: "https://cdn.example/full_4.4.0.zip", MD5: "fullmd5", Size: 100}},
		},
	}
	if currentInPatches {
		predl.Patches = []HypPackageInfo{{
			Version:  "4.3.0",
			GamePkgs: []HypPackageData{{URL: "https://cdn.example/patch_4.3.0_to_4.4.0.zip", MD5: "patchmd5", Size: 50}},
		}}
	}
	resp := &HypGetGamePackagesResponse{ManifestETag: "etag-x"}
	resp.Data.GamePackages = []HypGameEntry{{
		Main:        HypGamePackagesMain{Major: HypPackageInfo{Version: "4.3.0"}},
		PreDownload: predl,
	}}
	return resp
}

func TestBuildPredlPlan_PatchFlavor(t *testing.T) {
	gp, err := buildPredlPlan(context.Background(), predlResp(true), core.GameID("hoyoverse/starrail"), "4.3.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil {
		t.Fatal("expected a predl plan")
	}
	if gp.flavor != flavorPredlPatch {
		t.Errorf("flavor = %v, want flavorPredlPatch", gp.flavor)
	}
	if gp.UpdatePlan.Kind != core.PlanPredownload || gp.UpdatePlan.Version != "4.4.0" {
		t.Errorf("plan = {Kind:%v Version:%q}, want PlanPredownload @ 4.4.0", gp.UpdatePlan.Kind, gp.UpdatePlan.Version)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "patch_4.3.0_to_4.4.0.zip" {
		t.Fatalf("Files = %+v, want the patch zip", gp.UpdatePlan.Files)
	}
	if gp.sourceVersion != "4.3.0" {
		t.Errorf("sourceVersion = %q, want 4.3.0", gp.sourceVersion)
	}
}

func TestBuildPredlPlan_FullFlavor(t *testing.T) {
	gp, err := buildPredlPlan(context.Background(), predlResp(false), core.GameID("hoyoverse/starrail"), "4.0.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil || gp.flavor != flavorPredlFull {
		t.Fatalf("flavor = %v, want flavorPredlFull", gp.flavor)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "full_4.4.0.zip" {
		t.Fatalf("Files = %+v, want the full zip", gp.UpdatePlan.Files)
	}
}

func TestBuildPredlPlan_NoPredl(t *testing.T) {
	resp := &HypGetGamePackagesResponse{ManifestETag: "etag-x"}
	resp.Data.GamePackages = []HypGameEntry{{Main: HypGamePackagesMain{Major: HypPackageInfo{Version: "4.3.0"}}}} // PreDownload nil
	gp, err := buildPredlPlan(context.Background(), resp, core.GameID("hoyoverse/starrail"), "4.3.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 1 << 40})
	if err != nil {
		t.Fatalf("buildPredlPlan: %v", err)
	}
	if gp != nil {
		t.Fatalf("expected nil plan when no predownload published, got %+v", gp)
	}
}

func TestSupportsPredownload_IncludesLegacyAfterPhase3(t *testing.T) {
	p := New(Settings{}, nil)
	for _, gid := range []string{"hoyoverse/genshin", "hoyoverse/starrail", "hoyoverse/zzz"} {
		if !p.SupportsPredownload(core.GameID(gid)) {
			t.Errorf("%s must support predownload after Phase 3", gid)
		}
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/unknown")) {
		t.Error("unknown game must not support predownload")
	}
}

func stageLegacyPredl(t *testing.T, tempRoot string, gid core.GameID, version, flavor string) string {
	t.Helper()
	ps, err := newProgressStore(tempRoot, gid, version, "etag-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("patch.zip", 50, time.Now(), "abc"); err != nil {
		t.Fatal(err)
	}
	snap := planSnapshot{
		SourceVersion: "4.3.0", TargetVersion: version,
		Files:        []core.FileTask{{Path: "patch.zip", Hash: "abc", Size: 50}},
		ManifestETag: "etag-1", Flavor: flavor,
	}
	if err := ps.RenameToPredlReady(snap); err != nil {
		t.Fatal(err)
	}
	return versionSidecarDir(tempRoot, gid, version)
}

func TestLoadLegacyPredlConsume_RestoresAndRebuilds(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	versionDir := stageLegacyPredl(t, tempRoot, gid, "4.4.0", flavorPredlPatch.String())

	gp, err := loadLegacyPredlConsume(versionDir, "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil {
		t.Fatal("expected a consume plan")
	}
	if gp.flavor != flavorPredlPatch {
		t.Errorf("flavor = %v, want flavorPredlPatch", gp.flavor)
	}
	if gp.UpdatePlan.Kind != core.PlanUpdate {
		t.Errorf("Kind = %v, want PlanUpdate (apply)", gp.UpdatePlan.Kind)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "patch.zip" {
		t.Fatalf("Files = %+v", gp.UpdatePlan.Files)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "progress.json")); err != nil {
		t.Errorf("progress.json should be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "predl_ready.json")); !os.IsNotExist(err) {
		t.Errorf("predl_ready.json should be removed, stat err = %v", err)
	}
	restored, err := core.LoadProgressFromPath(filepath.Join(versionDir, "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Entries["patch.zip"]; !ok {
		t.Error("restored progress.json missing the staged entry")
	}
}

func TestLoadLegacyPredlConsume_VersionMismatch(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	versionDir := stageLegacyPredl(t, tempRoot, gid, "4.4.0", flavorPredlFull.String())
	gp, err := loadLegacyPredlConsume(versionDir, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if gp != nil {
		t.Error("expected nil when the staged predl targets a different version")
	}
	if _, err := os.Stat(filepath.Join(versionDir, "predl_ready.json")); err != nil {
		t.Errorf("predl_ready.json must be preserved on mismatch: %v", err)
	}
}

func TestLoadLegacyPredlConsume_NoStaged(t *testing.T) {
	gp, err := loadLegacyPredlConsume(t.TempDir(), "4.4.0")
	if err != nil || gp != nil {
		t.Fatalf("expected (nil,nil) when nothing staged; got gp=%v err=%v", gp, err)
	}
}
