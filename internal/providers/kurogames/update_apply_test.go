package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestApplier_applyErr_classifiesPermission(t *testing.T) {
	a := &applier{plan: &core.UpdatePlan{GameID: "kurogames/wutheringwaves"}}

	perm := a.applyErr("client/foo.pak", os.ErrPermission)
	ue, ok := perm.(*core.UpdateError)
	if !ok || ue.Code != "permission_denied" {
		t.Fatalf("permission error: got %#v; want code permission_denied", perm)
	}
	if ue.Params["game"] != "kurogames/wutheringwaves" {
		t.Fatalf("permission_denied params missing game: %#v", ue.Params)
	}

	other := a.applyErr("client/foo.pak", errors.New("disk gone"))
	ue2, ok := other.(*core.UpdateError)
	if !ok || ue2.Code != "apply_partial" {
		t.Fatalf("non-permission error: got %#v; want code apply_partial", other)
	}
	if ue2.Params["path"] != "client/foo.pak" {
		t.Fatalf("apply_partial should carry path: %#v", ue2.Params)
	}
}

// TestApply_HappyPath: apply phase end-to-end with no recovery state.
// Note: validateSameVolume runs on both tempDir and gameDir (both = t.TempDir()).
// On Windows this verifies same-volume success; on Linux/macOS the volume check
// is a no-op there; the real cross-volume assertion runs in
// TestApply_VolumeChangedBetweenPhases.
func TestApply_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ps.dir(), "Engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.dll"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "Engine", "b.dll"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID:       "kurogames/wutheringwaves",
		Version:      "3.4.0",
		ManifestETag: `"abc"`,
		Files: []core.FileTask{
			{Path: "a.dll", Size: 3},
			{Path: "Engine/b.dll", Size: 3},
		},
	}
	a := &applier{
		logger:   slog.Default(),
		tempRoot: tmp,
		gameDir:  gameDir,
		progress: ps,
		plan:     plan,
		lock:     newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(gameDir, "a.dll")); err != nil {
		t.Errorf("a.dll missing in gameDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "Engine", "b.dll")); err != nil {
		t.Errorf("Engine/b.dll missing in gameDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ps.dir(), "apply.wal")); err == nil {
		t.Errorf("apply.wal should be deleted on success")
	}
}

// TestApply_VolumeChangedBetweenPhases — spec §7.2 mandate.
func TestApply_VolumeChangedBetweenPhases(t *testing.T) {
	if filepath.VolumeName(`C:\foo`) == "" {
		t.Skip("filepath.VolumeName behaves only on Windows hosts")
	}
	if err := validateSameVolume(`C:\temp\omnigate`, `C:\Program Files\Wuthering Waves`); err != nil {
		t.Errorf("same volume returned err: %v", err)
	}
	err := validateSameVolume(`C:\temp\omnigate`, `D:\Games\Wuthering Waves`)
	if err == nil {
		t.Fatal("different volume: expected cross_volume_midrun error, got nil")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok {
		t.Fatalf("err = %T, want *core.UpdateError", err)
	}
	if ue.Code != "cross_volume_midrun" {
		t.Errorf("Code = %q, want cross_volume_midrun", ue.Code)
	}
	if ue.Params["temp_vol"] != `C:` || ue.Params["game_vol"] != `D:` {
		t.Errorf("Params = %v, want temp_vol=C: game_vol=D:", ue.Params)
	}
}

func TestApply_WALReplay(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.dll", "b.dll"} {
		if err := os.WriteFile(filepath.Join(ps.dir(), name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gameDir, "a.dll"), []byte("a.dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ps.dir(), "a.dll")); err != nil {
		t.Fatal(err)
	}
	wal := applyWAL{
		GameID:  "kurogames/wutheringwaves",
		Version: "3.4.0",
		ETag:    `"abc"`,
		Pending: []string{"b.dll"},
		Done:    []string{"a.dll"},
	}
	walPath := filepath.Join(ps.dir(), "apply.wal")
	body, _ := json.Marshal(&wal)
	if err := os.WriteFile(walPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := resumeApply(context.Background(), walPath, gameDir, newApplyLock(), nil, slog.Default()); err != nil {
		t.Fatalf("resumeApply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "b.dll")); err != nil {
		t.Errorf("b.dll missing post-resume: %v", err)
	}
	if _, err := os.Stat(walPath); err == nil {
		t.Errorf("apply.wal should be deleted post-resume")
	}
}

func TestApply_LockHeld(t *testing.T) {
	gameDir := t.TempDir()
	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// applyLock now lives under tempRoot/<gameID>/<version>/ (per
	// runApply's lockDir = a.progress.dir()) so non-admin processes can lock
	// when gameDir is under Program Files.
	preLock := newApplyLock()
	if err := preLock.Acquire(ps.dir()); err != nil {
		t.Fatalf("preLock: %v", err)
	}
	defer preLock.Release()

	plan := &core.UpdatePlan{Files: []core.FileTask{}}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
	}
	err := a.runApply(context.Background())
	if err == nil {
		t.Fatal("expected lock_held error")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok {
		t.Fatalf("err type = %T, want *core.UpdateError", err)
	}
	if ue.Code != "process_blocked" || ue.Params["kind"] != "lock_held" {
		t.Errorf("err = %+v, want process_blocked+lock_held", ue)
	}
}
