package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func newProgressStoreForTest(t *testing.T) *progressStore {
	t.Helper()
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	ps, err := newProgressStore(tmp, gid, "5.7.0", "test-etag")
	if err != nil {
		t.Fatalf("newProgressStore: %v", err)
	}
	return ps
}

func TestProgressStore_LoadOrInit_Empty(t *testing.T) {
	ps := newProgressStoreForTest(t)
	pf := ps.snapshot()
	if pf.GameID != "hoyoverse/genshin" {
		t.Errorf("game_id = %q want hoyoverse/genshin", pf.GameID)
	}
	if pf.Version != "5.7.0" {
		t.Errorf("version = %q want 5.7.0", pf.Version)
	}
	if len(pf.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(pf.Entries))
	}
}

func TestProgressStore_MarkComplete(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	pf := ps.snapshot()
	if e, ok := pf.Entries["blob1.zip"]; !ok || e.Size != 1024 || e.Hash != "abcd" {
		t.Errorf("entry not recorded correctly: %+v", pf.Entries)
	}
}

func TestProgressStore_Persist(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	ps2, err := newProgressStore(ps.tempRoot, ps.gid, "5.7.0", "test-etag")
	if err != nil {
		t.Fatal(err)
	}
	pf := ps2.snapshot()
	if _, ok := pf.Entries["blob1.zip"]; !ok {
		t.Errorf("entry not persisted: %+v", pf.Entries)
	}
}

func TestProgressStore_AllEntriesComplete(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if ps.AllEntriesComplete([]core.FileTask{{Path: "blob1.zip", Size: 1024}}) {
		t.Error("expected false when no entries marked")
	}
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if !ps.AllEntriesComplete([]core.FileTask{{Path: "blob1.zip", Size: 1024}}) {
		t.Error("expected true when all entries marked")
	}
}

func TestProgressStore_RenameToPredlReady(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	snapshot := planSnapshot{
		SourceVersion:  "5.6.0",
		TargetVersion:  "5.7.0",
		Files:          []core.FileTask{{Path: "blob1.zip", Size: 1024, Hash: "abcd"}},
		AudioLanguages: []string{"Chinese"},
		ManifestETag:   "test-etag",
	}
	if err := ps.RenameToPredlReady(snapshot); err != nil {
		t.Fatalf("RenameToPredlReady: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(ps.versionDir(), "progress.json")); !os.IsNotExist(statErr) {
		t.Errorf("expected progress.json removed; stat err: %v", statErr)
	}
	predl, err := loadJSONSidecar[predlReadyFile](filepath.Join(ps.versionDir(), "predl_ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if predl == nil {
		t.Fatal("predl_ready.json absent")
	}
	if predl.PlanSnapshot.TargetVersion != "5.7.0" {
		t.Errorf("snapshot target_version = %q want 5.7.0", predl.PlanSnapshot.TargetVersion)
	}
}

func TestProgressStore_SetDifferenceInvalidate(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("audio_zh-cn.zip", 2048, time.Now(), "ef01"); err != nil {
		t.Fatal(err)
	}
	newFiles := []core.FileTask{
		{Path: "blob1.zip", Size: 1024, Hash: "abcd"},
		{Path: "audio_ko-kr.zip", Size: 3072, Hash: "ff22"},
	}
	if err := ps.InvalidateRemoved(newFiles); err != nil {
		t.Fatalf("InvalidateRemoved: %v", err)
	}
	pf := ps.snapshot()
	if _, ok := pf.Entries["blob1.zip"]; !ok {
		t.Errorf("blob1.zip should be retained")
	}
	if _, ok := pf.Entries["audio_zh-cn.zip"]; ok {
		t.Errorf("audio_zh-cn.zip should be invalidated")
	}
}
