package kurogames

import (
	"bytes"
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

// TestApply_EphemeralNeverRenamed pins the invariant behind the 2026-08-20
// incident: a krpdiff diff staged as Files[].Ephemeral must never end up
// renamed into gameDir, and the WAL built at apply-start must never
// reference it either (WAL Pending/Done is the union of all rename
// targets ever tracked — checked mid-run via onEvent, since apply.wal is
// deleted from disk on overall success). Also directly exercises both
// branches of assertNotEphemeral, the independent defence-in-depth check.
func TestApply_EphemeralNeverRenamed(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ps.dir(), "staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "staged", "a.krpdiff"), []byte("bogus-diff-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.dll"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID:       "kurogames/wutheringwaves",
		Version:      "3.4.0",
		ManifestETag: `"etag-1"`,
		Files: []core.FileTask{
			{Path: "staged/a.krpdiff", Size: 16, Ephemeral: true},
			{Path: "a.dll", Size: 3},
		},
	}

	walPath := filepath.Join(ps.dir(), "apply.wal")
	var sawEvent bool
	var walPendingSnapshot, walDoneSnapshot []string
	onEvent := func(core.UpdateEvent) {
		sawEvent = true
		body, err := os.ReadFile(walPath)
		if err != nil {
			t.Fatalf("read apply.wal mid-run: %v", err)
		}
		var w applyWAL
		if err := json.Unmarshal(body, &w); err != nil {
			t.Fatalf("unmarshal apply.wal mid-run: %v", err)
		}
		walPendingSnapshot = append([]string{}, w.Pending...)
		walDoneSnapshot = append([]string{}, w.Done...)
	}

	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, onEvent: onEvent, lock: newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if !sawEvent {
		t.Fatal("onEvent never fired; test setup broken")
	}

	if _, err := os.Stat(filepath.Join(gameDir, "staged", "a.krpdiff")); err == nil {
		t.Errorf("ephemeral diff must never be renamed into gameDir")
	}
	if _, err := os.Stat(filepath.Join(gameDir, "a.dll")); err != nil {
		t.Errorf("a.dll missing in gameDir: %v", err)
	}
	for _, p := range append(walPendingSnapshot, walDoneSnapshot...) {
		if p == "staged/a.krpdiff" {
			t.Errorf("WAL Pending/Done referenced ephemeral path: pending=%v done=%v", walPendingSnapshot, walDoneSnapshot)
		}
	}

	// assertNotEphemeral: direct unit coverage of both branches.
	if err := assertNotEphemeral(plan, "staged/a.krpdiff"); !errors.Is(err, errEphemeralLeak) {
		t.Errorf("assertNotEphemeral(ephemeral path) = %v, want errEphemeralLeak", err)
	}
	if err := assertNotEphemeral(plan, "a.dll"); err != nil {
		t.Errorf("assertNotEphemeral(normal path) = %v, want nil", err)
	}
}

// TestApply_DeleteFiles covers spec §4-3: normal delete, missing-file
// no-op, and the path guard (absolute path / ".." traversal) rejecting
// with a structured error while leaving any out-of-gameDir file untouched.
func TestApply_DeleteFiles(t *testing.T) {
	tmp := t.TempDir()
	parent := t.TempDir()
	gameDir := filepath.Join(parent, "game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "legacy.dat"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "keep.dat"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	escapePath := filepath.Join(parent, "escape.txt")
	if err := os.WriteFile(escapePath, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	absTarget := filepath.Join(t.TempDir(), "abs.txt")
	if err := os.WriteFile(absTarget, []byte("absolute"), 0o644); err != nil {
		t.Fatal(err)
	}

	newApplier := func(version string, deleteFiles []string) *applier {
		ps := newProgressStore(tmp, "kurogames/wuwa", version)
		if err := ps.Init("etag-1"); err != nil {
			t.Fatal(err)
		}
		plan := &core.UpdatePlan{
			GameID: "kurogames/wutheringwaves", Version: version, ManifestETag: `"etag-1"`,
			Files: []core.FileTask{}, DeleteFiles: deleteFiles,
		}
		return &applier{
			logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
			progress: ps, plan: plan, lock: newApplyLock(),
		}
	}

	// Normal delete + missing-file no-op.
	a1 := newApplier("3.4.0", []string{"legacy.dat", "missing.dat"})
	if err := a1.runApply(context.Background()); err != nil {
		t.Fatalf("runApply (normal deletes): %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "legacy.dat")); err == nil {
		t.Errorf("legacy.dat should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(gameDir, "keep.dat")); err != nil {
		t.Errorf("keep.dat should still exist: %v", err)
	}

	// Traversal escape.
	a2 := newApplier("3.4.1", []string{"../escape.txt"})
	err2 := a2.runApply(context.Background())
	if err2 == nil {
		t.Fatal("expected structured error for ../escape traversal")
	}
	if _, ok := err2.(*core.UpdateError); !ok {
		t.Errorf("err type = %T, want *core.UpdateError", err2)
	}
	got, err := os.ReadFile(escapePath)
	if err != nil || string(got) != "outside" {
		t.Errorf("escape.txt should be untouched: content=%q err=%v", got, err)
	}

	// Absolute path.
	a3 := newApplier("3.4.2", []string{absTarget})
	err3 := a3.runApply(context.Background())
	if err3 == nil {
		t.Fatal("expected structured error for absolute delete path")
	}
	if _, ok := err3.(*core.UpdateError); !ok {
		t.Errorf("err type = %T, want *core.UpdateError", err3)
	}
	got3, err := os.ReadFile(absTarget)
	if err != nil || string(got3) != "absolute" {
		t.Errorf("abs.txt should be untouched: content=%q err=%v", got3, err)
	}
}

// readKrpdiffFixture loads a testdata/krpdiff/<name> fixture (Task 1.5).
func readKrpdiffFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "krpdiff", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// chunkAPath is the game-dir-relative path the "a" krpdiff fixture pair
// embeds (see testdata/krpdiff/README.md).
const chunkAPath = "Client/Content/Paks/chunk_a.pak"

// TestApply_DeleteAfterGroups proves ordering (spec §1-5 / §4): a
// PatchGroup's Src coincides with a DeleteFiles entry. If deleteFiles ran
// before the patch group, hpatchz would fail to find the old file
// (patch_failed); success here proves the src was still present when
// hpatchz ran, i.e. deleteFiles genuinely ran after.
func TestApply_DeleteAfterGroups(t *testing.T) {
	oldBytes := readKrpdiffFixture(t, "old_a.bin")
	newBytes := readKrpdiffFixture(t, "new_a.bin")
	diffBytes := readKrpdiffFixture(t, "a.krpdiff")

	tmp := t.TempDir()
	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, filepath.Dir(filepath.FromSlash(chunkAPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)), oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.krpdiff"), diffBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID: "kurogames/wutheringwaves", Version: "3.4.0", ManifestETag: `"etag-1"`,
		PatchGroups: []core.PatchGroup{
			{
				DiffPath: "a.krpdiff",
				Src:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(oldBytes), Size: int64(len(oldBytes))},
				Dst:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(newBytes), Size: int64(len(newBytes))},
			},
		},
		DeleteFiles: []string{chunkAPath},
	}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v (patch could not read src → deleteFiles ran before groups)", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, filepath.FromSlash(chunkAPath))); err == nil {
		t.Errorf("file should have been deleted after the patch group completed")
	}
}

// TestApply_PatchGroupHappyPath: real hpatchz roundtrip via the "a"
// fixture — old_a.bin, patched with a.krpdiff, must equal new_a.bin
// byte-for-byte once renamed into gameDir.
func TestApply_PatchGroupHappyPath(t *testing.T) {
	oldBytes := readKrpdiffFixture(t, "old_a.bin")
	newBytes := readKrpdiffFixture(t, "new_a.bin")
	diffBytes := readKrpdiffFixture(t, "a.krpdiff")

	tmp := t.TempDir()
	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, filepath.Dir(filepath.FromSlash(chunkAPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)), oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.krpdiff"), diffBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID: "kurogames/wutheringwaves", Version: "3.4.0", ManifestETag: `"etag-1"`,
		PatchGroups: []core.PatchGroup{
			{
				DiffPath: "a.krpdiff",
				Src:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(oldBytes), Size: int64(len(oldBytes))},
				Dst:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(newBytes), Size: int64(len(newBytes))},
			},
		},
	}
	var events []core.UpdateEvent
	onEvent := func(e core.UpdateEvent) { events = append(events, e) }
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, onEvent: onEvent, lock: newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if !bytes.Equal(got, newBytes) {
		t.Errorf("patched content mismatch: got %d bytes, want %d bytes matching new_a.bin", len(got), len(newBytes))
	}
	var sawPatching bool
	for _, e := range events {
		if e.Stage == "patching" {
			sawPatching = true
		}
	}
	if !sawPatching {
		t.Errorf("expected a Stage=%q progress event", "patching")
	}
}

// TestApply_PatchDstMismatchKeepsOld: a wrong Dst.Hash must fail the
// post-patch MD5 verification, leave the original gameDir file untouched,
// and clean up the _out product (no leftover garbage for a future resume
// to accidentally treat as cached-valid).
func TestApply_PatchDstMismatchKeepsOld(t *testing.T) {
	oldBytes := readKrpdiffFixture(t, "old_a.bin")
	diffBytes := readKrpdiffFixture(t, "a.krpdiff")

	tmp := t.TempDir()
	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, filepath.Dir(filepath.FromSlash(chunkAPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)), oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.krpdiff"), diffBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID: "kurogames/wutheringwaves", Version: "3.4.0", ManifestETag: `"etag-1"`,
		PatchGroups: []core.PatchGroup{
			{
				DiffPath: "a.krpdiff",
				Src:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(oldBytes), Size: int64(len(oldBytes))},
				Dst:      core.PatchFile{Path: chunkAPath, Hash: "deadbeefdeadbeefdeadbeefdeadbeef", Size: 999},
			},
		},
	}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
	}
	err := a.runApply(context.Background())
	if err == nil {
		t.Fatal("expected patch_failed for Dst.Hash mismatch")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok || ue.Code != "patch_failed" {
		t.Fatalf("err = %#v, want *core.UpdateError{Code: patch_failed}", err)
	}

	got, rerr := os.ReadFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)))
	if rerr != nil || !bytes.Equal(got, oldBytes) {
		t.Errorf("gameDir file should remain the original old_a.bin content; read err=%v", rerr)
	}
	outPath := filepath.Join(ps.dir(), "_out", filepath.FromSlash(chunkAPath))
	if _, serr := os.Stat(outPath); serr == nil {
		t.Errorf("_out product should have been removed after md5 mismatch")
	}
}

// TestApply_OutCacheSkipsRepatch: a pre-existing verified _out product
// (crash-resume state) must short-circuit hpatchz entirely. Proven by
// pairing it with a deliberately corrupt diff file — if hpatchz were
// actually invoked, it would fail on the bogus diff and the apply would
// error.
func TestApply_OutCacheSkipsRepatch(t *testing.T) {
	oldBytes := readKrpdiffFixture(t, "old_a.bin")
	newBytes := readKrpdiffFixture(t, "new_a.bin")

	tmp := t.TempDir()
	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, filepath.Dir(filepath.FromSlash(chunkAPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)), oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Deliberately corrupt diff — proves hpatchz is never invoked.
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.krpdiff"), []byte("NOT A REAL KRPDIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-seed a verified _out product (simulating crash-resume state).
	outDir := filepath.Join(ps.dir(), "_out", filepath.Dir(filepath.FromSlash(chunkAPath)))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "_out", filepath.FromSlash(chunkAPath)), newBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID: "kurogames/wutheringwaves", Version: "3.4.0", ManifestETag: `"etag-1"`,
		PatchGroups: []core.PatchGroup{
			{
				DiffPath: "a.krpdiff",
				Src:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(oldBytes), Size: int64(len(oldBytes))},
				Dst:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(newBytes), Size: int64(len(newBytes))},
			},
		},
	}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v (hpatchz must not have run against the bogus diff)", err)
	}
	got, err := os.ReadFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)))
	if err != nil || !bytes.Equal(got, newBytes) {
		t.Errorf("gameDir should have the cached _out product renamed in; err=%v", err)
	}
}

// TestApply_ProcessGuardBlocksPatch pins gate warning #1: the process
// guard is not decorative. A stubbed procRunning returning true must
// block the patch phase with process_blocked and leave gameDir untouched.
func TestApply_ProcessGuardBlocksPatch(t *testing.T) {
	oldBytes := readKrpdiffFixture(t, "old_a.bin")
	newBytes := readKrpdiffFixture(t, "new_a.bin")
	diffBytes := readKrpdiffFixture(t, "a.krpdiff")

	tmp := t.TempDir()
	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, filepath.Dir(filepath.FromSlash(chunkAPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)), oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.krpdiff"), diffBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID: "kurogames/wutheringwaves", Version: "3.4.0", ManifestETag: `"etag-1"`,
		PatchGroups: []core.PatchGroup{
			{
				DiffPath: "a.krpdiff",
				Src:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(oldBytes), Size: int64(len(oldBytes))},
				Dst:      core.PatchFile{Path: chunkAPath, Hash: md5hexBytes(newBytes), Size: int64(len(newBytes))},
			},
		},
	}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
		exeName:     "x.exe",
		procRunning: func(string) bool { return true },
	}
	err := a.runApply(context.Background())
	if err == nil {
		t.Fatal("expected process_blocked")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok || ue.Code != "process_blocked" {
		t.Fatalf("err = %#v, want *core.UpdateError{Code: process_blocked}", err)
	}

	got, rerr := os.ReadFile(filepath.Join(gameDir, filepath.FromSlash(chunkAPath)))
	if rerr != nil || !bytes.Equal(got, oldBytes) {
		t.Errorf("gameDir should be untouched by a blocked patch; read err=%v", rerr)
	}
}
