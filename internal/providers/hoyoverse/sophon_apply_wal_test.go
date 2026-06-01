package hoyoverse

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

func TestSophonWAL_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{
		GameID:      string(gid),
		TargetTag:   "6.6.0",
		BuildID:     "buildA",
		SourceTag:   "6.5.0",
		Flavor:      flavorSophonPatch.String(),
		BranchKind:  "main",
		StagingRoot: filepath.Join(dir, "staging", "main", "buildA"),
		Records: []sophonApplyRecord{
			{Kind: "hdiff_patch", Category: "game", Path: "a.pak", State: "pending", PatchName: "p1", OriginalFileMD5: "old"},
			{Kind: "chunk_assemble", Category: "game", Path: "b.pak", State: "pending", AssembleSources: []walChunkSource{
				{Kind: "cdn", ChunkName: "c1", URLPrefix: "https://cdn", CompressedSz: 10, UseCompress: true, DecompSize: 20, FileOffset: 0, ExpectMD5: "m1"},
			}},
			{Kind: "delete", Category: "game", Path: "old.pak", State: "pending", ExpectMD5: "dm"},
		},
	}
	if err := writeSophonApplyWAL(dir, wal); err != nil {
		t.Fatal(err)
	}
	got, err := readSophonApplyWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("readSophonApplyWAL returned nil")
	}
	if got.TargetTag != "6.6.0" || got.Flavor != "sophon_patch" || len(got.Records) != 3 {
		t.Errorf("header/records mismatch: %+v", got)
	}
	if got.Records[1].AssembleSources[0].ChunkName != "c1" {
		t.Errorf("assemble source not round-tripped: %+v", got.Records[1])
	}
}

func TestSophonWAL_ReadMissing(t *testing.T) {
	got, err := readSophonApplyWAL(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil err for ENOENT, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil WAL, got %+v", got)
	}
}

func TestSophonWAL_FirstPending(t *testing.T) {
	wal := &sophonApplyWAL{Records: []sophonApplyRecord{
		{Path: "a", State: "done"},
		{Path: "b", State: "done"},
		{Path: "c", State: "pending"},
		{Path: "d", State: "pending"},
	}}
	if idx := wal.firstPending(); idx != 2 {
		t.Errorf("firstPending = %d, want 2", idx)
	}
	allDone := &sophonApplyWAL{Records: []sophonApplyRecord{{State: "done"}}}
	if idx := allDone.firstPending(); idx != -1 {
		t.Errorf("firstPending (all done) = %d, want -1", idx)
	}
}

func TestWalChunkSourceConversions(t *testing.T) {
	cs := sophon.ChunkSource{
		Kind: sophon.SourceLocal, Asset: "a.pak", ChunkName: "c1", URLPrefix: "https://cdn",
		CompressedSz: 10, UseCompress: true, OldFile: "old.pak", OldOffset: 64,
		DecompSize: 20, FileOffset: 128, ExpectMD5: "m1",
	}
	w := toWalChunkSource(cs)
	if w.Kind != "local" || w.ChunkName != "c1" || w.URLPrefix != "https://cdn" || w.OldFile != "old.pak" || w.OldOffset != 64 || w.FileOffset != 128 {
		t.Errorf("toWalChunkSource lossy: %+v", w)
	}
	back := fromWalChunkSource(w)
	// Asset is not persisted in WAL (plan-time-only, §A.2); verify round-trip of all persisted fields
	cs.Asset = ""
	if back != cs {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, cs)
	}
}

func TestWalFlusher_CountCadence(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid), TargetTag: "6.6.0"}
	for i := 0; i < 60; i++ {
		wal.Records = append(wal.Records, sophonApplyRecord{Path: "f", State: "pending"})
	}
	frozen := time.Unix(0, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return frozen })

	// 49 transitions: below the 50-count threshold, no flush (no clock advance).
	for i := 0; i < 49; i++ {
		wal.Records[i].State = "done"
		if err := fl.maybeFlush(); err != nil {
			t.Fatal(err)
		}
	}
	if readWALOrNil(t, dir) != nil {
		t.Fatal("flushed before reaching 50-count threshold")
	}
	if fl.flushes != 0 {
		t.Fatalf("flushes = %d before threshold, want 0", fl.flushes)
	}
	// 50th transition crosses the count threshold → flush.
	wal.Records[49].State = "done"
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	got := readWALOrNil(t, dir)
	if got == nil {
		t.Fatal("expected flush at 50-count threshold")
	}
	if fl.flushes != 1 {
		t.Fatalf("flushes = %d at threshold, want 1", fl.flushes)
	}
}

func TestWalFlusher_TimeCadence(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid)}
	wal.Records = []sophonApplyRecord{{Path: "f", State: "pending"}}
	now := time.Unix(100, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return now })

	wal.Records[0].State = "done"
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) != nil {
		t.Fatal("flushed before 5s elapsed and below count threshold")
	}
	// Advance the injected clock past 5s → time cadence triggers.
	now = time.Unix(106, 0)
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) == nil {
		t.Fatal("expected flush after 5s elapsed")
	}
	if fl.flushes != 1 {
		t.Fatalf("flushes = %d after time cadence, want 1", fl.flushes)
	}
}

func TestWalFlusher_FlushOnClose(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid)}
	wal.Records = []sophonApplyRecord{{Path: "f", State: "pending"}}
	frozen := time.Unix(0, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return frozen })
	wal.Records[0].State = "done"
	_ = fl.maybeFlush() // below thresholds, no write
	if readWALOrNil(t, dir) != nil {
		t.Fatal("unexpected flush before close")
	}
	if err := fl.Close(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) == nil {
		t.Fatal("Close did not flush")
	}
}

func readWALOrNil(t *testing.T, dir string) *sophonApplyWAL {
	t.Helper()
	w, err := readSophonApplyWAL(dir)
	if err != nil {
		t.Fatalf("readSophonApplyWAL: %v", err)
	}
	return w
}
