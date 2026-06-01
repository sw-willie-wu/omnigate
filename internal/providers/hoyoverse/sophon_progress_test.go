package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestSophonProgress_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	st, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkChunkDone("chunk-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkChunkDone("chunk-2"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkPatchDone("patch-1"); err != nil {
		t.Fatal(err)
	}

	pf, err := loadSophonProgress(tmp, gid, "6.6.0")
	if err != nil {
		t.Fatal(err)
	}
	if pf == nil {
		t.Fatal("loadSophonProgress returned nil after writes")
	}
	if pf.GameID != string(gid) || pf.Version != "6.6.0" || pf.BranchKind != "main" || pf.BuildID != "buildA" {
		t.Errorf("header mismatch: %+v", pf)
	}
	if !pf.ChunksDone["chunk-1"] || !pf.ChunksDone["chunk-2"] {
		t.Errorf("ChunksDone = %v", pf.ChunksDone)
	}
	if !pf.PatchesDone["patch-1"] {
		t.Errorf("PatchesDone = %v", pf.PatchesDone)
	}
}

func TestSophonProgress_PartialResume(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	st, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.MarkChunkDone("done-1")

	// Re-open: existing progress must be loaded, not clobbered.
	st2, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	if !st2.ChunkDone("done-1") {
		t.Error("resume lost done-1")
	}
	if st2.ChunkDone("never") {
		t.Error("ChunkDone returned true for absent chunk")
	}
}

func TestSophonProgress_CorruptRecovery(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sophon_progress.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	pf, err := loadSophonProgress(tmp, gid, "6.6.0")
	if err != nil {
		t.Fatalf("expected nil err on corrupt, got %v", err)
	}
	if pf != nil {
		t.Errorf("expected nil pf on corrupt, got %+v", pf)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected corrupt file removed, stat err: %v", statErr)
	}
}
