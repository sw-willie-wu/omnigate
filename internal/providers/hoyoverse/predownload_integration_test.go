//go:build integration

package hoyoverse

import (
	"context"
	"testing"

	"omnigate/internal/core"
)

func TestCheckVersion_SophonSurfacesPredownload(t *testing.T) {
	// branches_with_predl.json: main.tag=6.6.0, pre_download.tag=6.7.0.
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
	p, _, _ := newSophonProvider(t, fs, "6.6.0") // installed == main.tag → up-to-date
	vi, err := p.CheckVersion(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckVersion: %v", err)
	}
	if vi.Latest != "6.6.0" || vi.Current != "6.6.0" {
		t.Errorf("Latest/Current = %q/%q, want 6.6.0/6.6.0", vi.Latest, vi.Current)
	}
	if vi.Predownload == nil || vi.Predownload.TargetVersion != "6.7.0" {
		t.Fatalf("vi.Predownload = %+v, want TargetVersion 6.7.0", vi.Predownload)
	}
}

func TestCheckForPredownload_SophonBuildsPredlPlanWhenUpToDate(t *testing.T) {
	// main.tag=6.6.0, installed=6.6.0 → main is IDLE. pre_download.tag=6.7.0,
	// diff_tags=["6.5.0"] (excludes 6.6.0) → predl BUILD flavor (reuses build_small.json).
	fs := newFakeSophonServer(t, "branches_idle_predl_build.json", "build_small.json", "patch_small.json")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
	seedAppliedManifest(t, tempRoot, "6.6.0") // oldMainManifest != nil → enables predl build flavor
	seedOldFiles(t, gameDir)

	// Plain CheckForUpdate hits the idle short-circuit → predl NOT built (documents B1).
	if _, err := p.CheckForUpdate(context.Background(), genshinGID); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatal("precondition: idle CheckForUpdate must not build the predl plan (B1 gap being fixed)")
	}

	// CheckForPredownload builds it directly even though up-to-date.
	plan, err := p.CheckForPredownload(context.Background(), genshinGID, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if plan.Kind != core.PlanPredownload || plan.Version != "6.7.0" {
		t.Fatalf("plan = {Kind:%v Version:%q}, want PlanPredownload @ 6.7.0", plan.Kind, plan.Version)
	}
	gp := p.manifestCache.get(genshinGID)
	if gp == nil || gp.predlPlan == nil {
		t.Fatal("predlPlan must be populated in manifestCache after CheckForPredownload")
	}
	if gp.predlPlan.TargetVersion != "6.7.0" {
		t.Errorf("predlPlan.TargetVersion = %q, want 6.7.0", gp.predlPlan.TargetVersion)
	}
}
