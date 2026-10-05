package kurogames

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func newTestApplier(t *testing.T, gameDir string, plan core.UpdatePlan) *applier {
	t.Helper()
	tmp := t.TempDir()
	ps := newProgressStore(tmp, string(plan.GameID), plan.Version)
	if err := ps.Init("tok"); err != nil {
		t.Fatal(err)
	}
	return &applier{logger: slog.Default(), tempRoot: tmp, gameDir: gameDir, progress: ps, plan: &plan, lock: newApplyLock(), procRunning: func(string) bool { return false }}
}

func TestSetBundle_PersistsIntoProgress(t *testing.T) {
	ps := newProgressStore(t.TempDir(), string(gidWuwa), "3.7.0")
	if err := ps.Init("tok"); err != nil {
		t.Fatal(err)
	}
	if err := ps.SetBundle("SD"); err != nil {
		t.Fatal(err)
	}
	pf, err := core.LoadProgress(ps.dir())
	if err != nil || pf.Bundle != "SD" {
		t.Fatalf("pf=%+v err=%v", pf, err)
	}
	if rs := core.ScanRecovery(ps.dir()); rs.Bundle != "SD" {
		t.Fatalf("recovery bundle=%q", rs.Bundle)
	}
}

func TestApply_BundleInstallRegistersBundle(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	a := newTestApplier(t, dir, core.UpdatePlan{GameID: gidWuwa, Kind: core.PlanUpdate, Version: "3.7.0", Bundle: "SD"})
	if err := a.runApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := readInstallState(filepath.Join(dir, installStateFile))
	if s.Bundles["SD"].Version != "3.7.0" || s.Bundles["HD"].Version != "3.7.0" {
		t.Fatalf("state=%+v", s)
	}
	if _, err := os.Stat(a.progress.dir()); !os.IsNotExist(err) {
		t.Fatal("version dir must be removed after apply")
	}
}

func TestApply_BundleWritebackFailureIsApplyPartialAndCleansSidecar(t *testing.T) {
	dir := t.TempDir()
	// make the write-back fail: a directory sits at the install record path
	_ = os.MkdirAll(filepath.Join(dir, installStateFile), 0o755)
	a := newTestApplier(t, dir, core.UpdatePlan{GameID: gidWuwa, Kind: core.PlanUpdate, Version: "3.7.0", Bundle: "SD"})
	err := a.runApply(context.Background())
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "apply_partial" || !ue.Retryable || ue.Params["bundle"] != "SD" {
		t.Fatalf("err=%v", err)
	}
	if _, serr := os.Stat(filepath.Join(a.progress.dir(), "apply.wal")); !os.IsNotExist(serr) {
		t.Fatal("apply.wal must not survive (spec 3.2)")
	}
}

func TestApply_WALCarriesBundle(t *testing.T) {
	// a file task whose src is missing makes atomicRename fail, leaving the WAL
	dir := t.TempDir()
	a := newTestApplier(t, dir, core.UpdatePlan{GameID: gidWuwa, Kind: core.PlanUpdate, Version: "3.7.0", Bundle: "UHD",
		Files: []core.FileTask{{Path: "Client/Content/UHD/missing.pak"}}})
	_ = a.runApply(context.Background())
	if rs := core.ScanRecovery(a.progress.dir()); rs.Phase != core.RecoveryPhaseApplyResume || rs.Bundle != "UHD" {
		t.Fatalf("rs=%+v", rs)
	}
}
