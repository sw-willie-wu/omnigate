package hoyoverse

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestRunApplyPlanPatch_CleansVersionDirIncludingLock(t *testing.T) {
	// Regression: cleanup os.RemoveAll(versionDir) must succeed even though the
	// apply held apply.lock. On Windows the still-open lock handle blocks
	// deletion (unlinkat "being used by another process") unless the lock is
	// released BEFORE the cleanup (defer Release fires too late).
	tempRoot := t.TempDir()
	gameDir := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	version := "1.0.0"

	versionDir := versionSidecarDir(tempRoot, gid, version)
	stagingDir := filepath.Join(versionDir, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	emit := func(string, int, int) {}
	if err := runApplyPlanPatch(context.Background(), tempRoot, gameDir, gid, version, false, "", emit); err != nil {
		t.Fatalf("runApplyPlanPatch: %v", err)
	}
	if _, err := os.Stat(versionDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("versionDir should be fully removed after apply; stat err = %v", err)
	}
}

func TestApplyWAL_WriteAndReplay(t *testing.T) {
	versionDir := t.TempDir()
	wal := &applyWAL{Pending: []string{"a.dll", "b.dll"}, Done: []string{}, WasPredl: false}
	if err := writeApplyWAL(versionDir, wal); err != nil {
		t.Fatal(err)
	}
	got, err := readApplyWAL(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 2 || got.Pending[0] != "a.dll" {
		t.Errorf("pending mismatch: %+v", got.Pending)
	}
}

func TestApplyWAL_Corrupt_GracefulRecover(t *testing.T) {
	versionDir := t.TempDir()
	walPath := filepath.Join(versionDir, "apply.wal")
	if err := os.WriteFile(walPath, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readApplyWAL(versionDir)
	if err != nil {
		t.Fatalf("expected nil err on corrupt; got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil wal on corrupt; got %+v", got)
	}
	if _, err := os.Stat(walPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected wal removed; stat err: %v", err)
	}
}

func TestApplyAtomicRename_SameVolume(t *testing.T) {
	gameDir := t.TempDir()
	stagingDir := t.TempDir()
	rel := "subdir/file.dll"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(stagingDir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, rel), []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyOneRename(stagingDir, gameDir, rel); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(gameDir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("wrong content: %q", string(got))
	}
}

func TestProcessDeletefiles_ENOENTSkipped(t *testing.T) {
	gameDir := t.TempDir()
	stagingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stagingDir, "deletefiles.txt"), []byte("nonexistent.dll\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := processDeletefiles(stagingDir, gameDir); err != nil {
		t.Errorf("ENOENT should be silent; got %v", err)
	}
}

func TestProcessDeletefiles_RemovesFiles(t *testing.T) {
	gameDir := t.TempDir()
	stagingDir := t.TempDir()
	target := filepath.Join(gameDir, "obsolete.dll")
	if err := os.WriteFile(target, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "deletefiles.txt"), []byte("obsolete.dll\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := processDeletefiles(stagingDir, gameDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected file removed; stat err: %v", err)
	}
}

func TestConfigWritebackSuccess(t *testing.T) {
	gameDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"),
		[]byte("[General]\ngame_version=5.6.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGameVersion(gameDir, "5.7.0"); err != nil {
		t.Errorf("write: %v", err)
	}
}

func TestLastApplyTarget_PersistsConfigWritebackOK(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	lat := lastApplyTarget{
		TargetVersion:     "5.7.0",
		AudioLanguages:    []string{"Chinese"},
		CompletionTS:      time.Now().UTC(),
		ConfigWritebackOK: true,
	}
	if err := writeLastApplyTarget(tmp, gid, &lat); err != nil {
		t.Fatal(err)
	}
	got, err := loadJSONSidecar[lastApplyTarget](filepath.Join(gameSidecarDir(tmp, gid), "last_apply_target.json"))
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if !got.ConfigWritebackOK {
		t.Error("config_writeback_ok not persisted")
	}
}

func TestRemoveAll_PreservesParentLastApplyTarget(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	lat := lastApplyTarget{TargetVersion: "5.7.0"}
	if err := writeLastApplyTarget(tmp, gid, &lat); err != nil {
		t.Fatal(err)
	}
	versionDir := versionSidecarDir(tmp, gid, "5.7.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "scratch.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(versionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(gameSidecarDir(tmp, gid), "last_apply_target.json")); err != nil {
		t.Errorf("last_apply_target.json should survive RemoveAll(versionDir); %v", err)
	}
}

func TestExtractProgress_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	ep := &extractProgress{
		ManifestETag: "etag-1",
		Blobs: map[string]extractedBlob{
			"https://example.invalid/a.zip": {Extracted: true, ExtractedAt: time.Now().UTC()},
		},
	}
	if err := writeExtractProgress(tmp, ep); err != nil {
		t.Fatal(err)
	}
	got, err := readExtractProgress(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ManifestETag != "etag-1" {
		t.Errorf("read mismatch: %+v", got)
	}
}

func TestEXDEVCrossVolume_TerminalError(t *testing.T) {
	versionDir := t.TempDir()
	rel := "x.dll"
	if err := os.WriteFile(filepath.Join(versionDir, "staging-fake-"+rel), []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := osRenameForApply
	osRenameForApply = func(src, dst string) error {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: crossDeviceErrForTest}
	}
	defer func() { osRenameForApply = prev }()

	err := applyOneRename(filepath.Join(versionDir, "staging-fake-"), versionDir, rel)
	if err == nil {
		t.Fatal("expected EXDEV terminal error")
	}
	var ue *core.UpdateError
	if !asUpdateError(err, &ue) || ue.Code != "cross_volume_midrun" {
		t.Errorf("expected cross_volume_midrun; got %v", err)
	}
}
