package hypergryph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestApply_RenamesFilesAndWritesVersion(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir() // same volume as temp on CI/dev
	// Seed an encrypted config.ini at the old version.
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\nentry=Endfield.exe\n"))
	if err := os.WriteFile(filepath.Join(game, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "data/a.bundle", Hash: "x", Size: 3}},
	}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	// Place the "downloaded" staged file under the version dir.
	if err := os.MkdirAll(filepath.Join(ps.dir(), "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "data/a.bundle"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	// File landed in game dir.
	if b, err := os.ReadFile(filepath.Join(game, "data/a.bundle")); err != nil || string(b) != "abc" {
		t.Errorf("applied file = %q err=%v", b, err)
	}
	// config.ini version written back.
	if v, _ := readLocalVersion(game); v != "1.2.6" {
		t.Errorf("config.ini version = %q, want 1.2.6", v)
	}
	// version dir cleaned up.
	if _, err := os.Stat(ps.dir()); !os.IsNotExist(err) {
		t.Errorf("version dir not cleaned up")
	}
}

func TestApply_CrossVolumeMidrunRejected(t *testing.T) {
	err := validateSameVolume(`C:\temp\omnigate`, `D:\Games\Endfield`)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_midrun" {
		t.Errorf("expected cross_volume_midrun, got %v", err)
	}
	if err := validateSameVolume(`C:\temp`, `C:\Games`); err != nil {
		t.Errorf("same volume should pass: %v", err)
	}
}

// TestApply_ZeroFilesStillWritesVersion: an already-current (0-file) apply must
// STILL write the new version back to config.ini so the next CheckVersion shows
// up-to-date and the UI doesn't bounce to [更新] (spec §5/§6/B3, kuro parity).
func TestApply_ZeroFilesStillWritesVersion(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir()
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\n"))
	if err := os.WriteFile(filepath.Join(game, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6", Files: nil}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if v, _ := readLocalVersion(game); v != "1.2.6" {
		t.Errorf("version after 0-file apply = %q, want 1.2.6", v)
	}
}

// TestApply_NoConfigIni_NonFatal: a missing config.ini (degrade, §6) must NOT
// fail the apply — the version writeback is a logged no-op.
func TestApply_NoConfigIni_NonFatal(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir() // no config.ini seeded
	plan := &core.UpdatePlan{GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6", Files: nil}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("apply must succeed even without config.ini (degrade §6): %v", err)
	}
}
