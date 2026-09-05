package kurogames

import (
	"encoding/json"
	"omnigate/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgress_WriteAndLoadEntry(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-abc"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("Engine/foo.dll", mt, 12345, "abc123hash"); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ETag != "etag-abc" {
		t.Errorf("ETag = %q", loaded.ETag)
	}
	e, ok := loaded.Entries["Engine/foo.dll"]
	if !ok {
		t.Fatal("entry not loaded")
	}
	if e.Size != 12345 || !e.MTime.Equal(mt) {
		t.Errorf("entry mismatch: %+v", e)
	}
}

// TestMarkComplete_RecordsHash: MarkComplete's hash argument must round-trip
// through progress.json (spec §2.5 — correctness moves from ETag to per-file
// Hash so ConsumePredlStaged can validate staged files individually).
func TestMarkComplete_RecordsHash(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-abc"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("Engine/foo.dll", mt, 12345, "deadbeef"); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	e, ok := loaded.Entries["Engine/foo.dll"]
	if !ok {
		t.Fatal("entry not loaded")
	}
	if e.Hash != "deadbeef" {
		t.Errorf("Hash = %q, want deadbeef", e.Hash)
	}
}

func TestProgress_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(p.dir(), "progress.json")
	tmpPath := mainPath + ".tmp"
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("progress.json missing: %v", err)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Errorf(".tmp should be cleaned")
	}
}

func TestProgress_InitPreservesEntriesOnSameETag(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("a.dll", mt, 1, "hash-a"); err != nil {
		t.Fatal(err)
	}

	// Re-Init with the SAME ETag must PRESERVE entries so a re-started download
	// resumes (skips already-completed files) instead of restarting.
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Entries["a.dll"]; !ok {
		t.Error("re-Init with same ETag must preserve entries (resume); entry was wiped")
	}

	// Re-Init with a DIFFERENT ETag must WIPE (manifest changed → re-download).
	if err := p.Init("etag-2"); err != nil {
		t.Fatal(err)
	}
	loaded2, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded2.Entries) != 0 {
		t.Errorf("re-Init with different ETag must wipe entries; got %d", len(loaded2.Entries))
	}
	if loaded2.ETag != "etag-2" {
		t.Errorf("ETag = %q, want etag-2", loaded2.ETag)
	}
}

func TestProgress_RenameToPredlReady(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("a.dll", time.Now(), 1, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "progress.json")); err == nil {
		t.Errorf("progress.json should be gone")
	}
	body, _ := os.ReadFile(filepath.Join(p.dir(), "predl_ready.json"))
	var v struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("predl_ready.json malformed: %v", err)
	}
	if v.ETag != "etag-1" {
		t.Errorf("ETag = %q", v.ETag)
	}
}

// TestConsumePredlStaged_AdoptsMatching: a staged predl_ready.json for the
// CURRENT version, whose entries' Hash all match wantHash (one entry uses
// the legacy hash-less path, re-hashed from disk), is adopted wholesale:
// progress.json is restored with newETag, predl_ready.json disappears, and
// stagedEphemeralBytes sums only entries flagged ephemeral.
func TestConsumePredlStaged_AdoptsMatching(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := p.Init("old-etag"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "a.dll"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "b.pak"), []byte("bbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "c.diff"), []byte("cc"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a.dll and b.pak carry a hash recorded at download time; c.diff is a
	// legacy hash-less entry (pre-Task-7 progress.json) that must be
	// re-hashed from disk to validate.
	if err := p.MarkComplete("a.dll", time.Now(), 4, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("b.pak", time.Now(), 6, "hash-b"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("c.diff", time.Now(), 2, ""); err != nil {
		t.Fatal(err)
	}
	cHash, err := md5File(filepath.Join(p.dir(), "c.diff"))
	if err != nil {
		t.Fatal(err)
	}
	// d.orphan is staged (present in predl_ready.json) but the CURRENT
	// manifest no longer references it (absent from wantHash below) — it
	// must be dropped from the restored progress.json (MINOR-2).
	if err := os.WriteFile(filepath.Join(p.dir(), "d.orphan"), []byte("dddd"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("d.orphan", time.Now(), 4, "hash-d"); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	wantHash := map[string]string{"a.dll": "hash-a", "b.pak": "hash-b", "c.diff": cHash}
	ephemeral := map[string]bool{"c.diff": true}
	adopted, stagedBytes, err := p.ConsumePredlStaged("new-etag", wantHash, ephemeral)
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("adopted = false, want true")
	}
	if stagedBytes != 2 {
		t.Errorf("stagedEphemeralBytes = %d, want 2 (only c.diff is ephemeral)", stagedBytes)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "predl_ready.json")); err == nil {
		t.Error("predl_ready.json should be gone after adoption")
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ETag != "new-etag" {
		t.Errorf("ETag = %q, want new-etag", loaded.ETag)
	}
	for _, rel := range []string{"a.dll", "b.pak", "c.diff"} {
		if _, ok := loaded.Entries[rel]; !ok {
			t.Errorf("entry %q missing after adoption", rel)
		}
	}
	if _, ok := loaded.Entries["d.orphan"]; ok {
		t.Error("d.orphan is absent from wantHash and must not survive adoption (MINOR-2)")
	}
}

// TestConsumePredlStaged_LegacyHashlessEdgeCases pins two legacy (Hash=="")
// re-hash edge cases that a naive "legacy entries always match" mutation
// would let slip through:
//
//   - drifted: on-disk bytes no longer match wantHash → drop + remove staged file.
//   - missing: staged file absent from disk entirely (md5File errors) →
//     drop, no panic, no keep.
func TestConsumePredlStaged_LegacyHashlessEdgeCases(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := p.Init("old-etag"); err != nil {
		t.Fatal(err)
	}

	// drifted: staged content no longer matches the current manifest hash.
	if err := os.WriteFile(filepath.Join(p.dir(), "drifted.diff"), []byte("stale-on-disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("drifted.diff", time.Now(), 13, ""); err != nil {
		t.Fatal(err)
	}

	// missing: entry recorded, but the staged file was never written (or was
	// since deleted) — md5File must error, not panic, and the entry drops.
	if err := p.MarkComplete("missing.diff", time.Now(), 99, ""); err != nil {
		t.Fatal(err)
	}

	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	wantHash := map[string]string{
		"drifted.diff": "some-other-hash-entirely",
		"missing.diff": "does-not-matter",
	}
	adopted, _, err := p.ConsumePredlStaged("new-etag", wantHash, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("adopted = false, want true")
	}

	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Entries["drifted.diff"]; ok {
		t.Error("drifted.diff (legacy, on-disk mismatch) should be dropped")
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "drifted.diff")); err == nil {
		t.Error("drifted.diff staged file should be removed from disk")
	}
	if _, ok := loaded.Entries["missing.diff"]; ok {
		t.Error("missing.diff (legacy, no file on disk) should be dropped")
	}
}

// TestConsumePredlStaged_DropsDrifted: an entry whose Hash disagrees with
// wantHash must be dropped from the restored progress.json AND have its
// staged file removed from disk (so the downloader re-fetches it instead of
// silently reusing stale bytes); entries that still match are kept.
func TestConsumePredlStaged_DropsDrifted(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := p.Init("old-etag"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "good.dll"), []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "bad.pak"), []byte("stale-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("good.dll", time.Now(), 4, "hash-good"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("bad.pak", time.Now(), 11, "hash-bad-old"); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	// wantHash for bad.pak has changed since staging (manifest updated) —
	// its Hash no longer matches.
	wantHash := map[string]string{"good.dll": "hash-good", "bad.pak": "hash-bad-new"}
	adopted, _, err := p.ConsumePredlStaged("new-etag", wantHash, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("adopted = false, want true")
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Entries["good.dll"]; !ok {
		t.Error("good.dll should be kept")
	}
	if _, ok := loaded.Entries["bad.pak"]; ok {
		t.Error("bad.pak should be dropped (hash mismatch)")
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "bad.pak")); err == nil {
		t.Error("bad.pak staged file should be removed from disk")
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "good.dll")); err != nil {
		t.Error("good.dll staged file should remain on disk")
	}
}

// TestConsumePredlStaged_VersionMismatchNoop: a staged predl_ready.json for
// a DIFFERENT version than p.version must be left completely untouched —
// it belongs to a predownload for another release and stays staged until
// that version's own RunUpdate consumes it.
func TestConsumePredlStaged_VersionMismatchNoop(t *testing.T) {
	tmp := t.TempDir()
	// p targets 3.5.0, but the staged predl_ready.json under its own dir
	// carries Version "9.9.9" (e.g. a stale predownload never cleaned up,
	// or — more precisely per spec — this simulates the recorded pf.Version
	// disagreeing with p.version even though it lives in p's dir).
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "future.pak"), []byte("fff"), 0o644); err != nil {
		t.Fatal(err)
	}
	pf := core.ProgressFile{
		GameID:  "kurogames/wutheringwaves",
		Version: "9.9.9",
		ETag:    "etag-future",
		Entries: map[string]core.ProgressEntry{
			"future.pak": {Size: 3, MTime: time.Now().Truncate(time.Millisecond), Hash: "hash-future"},
		},
	}
	body, err := json.MarshalIndent(&pf, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	predlPath := filepath.Join(p.dir(), "predl_ready.json")
	if err := os.WriteFile(predlPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	adopted, stagedBytes, err := p.ConsumePredlStaged("new-etag", map[string]string{"future.pak": "hash-future"}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if adopted {
		t.Error("adopted = true, want false (version mismatch)")
	}
	if stagedBytes != 0 {
		t.Errorf("stagedEphemeralBytes = %d, want 0", stagedBytes)
	}
	if _, err := os.Stat(predlPath); err != nil {
		t.Error("predl_ready.json for another version must stay untouched")
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "progress.json")); err == nil {
		t.Error("progress.json must not be created on version mismatch")
	}
}

// TestConsumePredlStaged_ThenInitPreservesEntries: after adoption stamps
// progress.json with newETag, a subsequent Init(newETag) call (the normal
// RunUpdate startup path) must treat it as a resume — same ETag → entries
// preserved, not wiped.
func TestConsumePredlStaged_ThenInitPreservesEntries(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := p.Init("old-etag"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir(), "a.dll"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("a.dll", time.Now(), 4, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	adopted, _, err := p.ConsumePredlStaged("new-etag", map[string]string{"a.dll": "hash-a"}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("adopted = false, want true")
	}

	if err := p.Init("new-etag"); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Entries["a.dll"]; !ok {
		t.Error("Init after ConsumePredlStaged with same ETag must preserve entries")
	}
	if loaded.ETag != "new-etag" {
		t.Errorf("ETag = %q, want new-etag", loaded.ETag)
	}
}
