package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/providers/hoyoverse/sophon"
)

// sophonApplyWAL is the typed-record write-ahead log for Sophon apply
// (<versionSidecarDir>/sophon_apply.wal, §A.6 / §6.1). v1's flat-list
// apply.wal is untouched (HSR/ZZZ).
type sophonApplyWAL struct {
	GameID      string              `json:"game_id"`
	TargetTag   string              `json:"target_tag"`
	BuildID     string              `json:"build_id"`
	SourceTag   string              `json:"source_tag"`   // "" for flavorSophonFull
	Flavor      string              `json:"flavor"`       // planFlavor.String()
	BranchKind  string              `json:"branch_kind"`  // "main" — predl never reaches apply
	WasPredl    bool                `json:"was_predl"`    // true if originated from predl-consume
	StagingRoot string              `json:"staging_root"` // resolved staging dir; all reads relative to it
	Records     []sophonApplyRecord `json:"records"`
}

type sophonApplyRecord struct {
	Kind            string           `json:"kind"`  // "chunk_assemble" | "hdiff_patch" | "copy_over" | "delete"
	Category        string           `json:"category"`
	Path            string           `json:"path"`  // target relative to gameDir
	State           string           `json:"state"` // "pending" | "done"
	AssetMD5        string           `json:"asset_md5,omitempty"`
	OldPath         string           `json:"old_path,omitempty"`
	PatchName       string           `json:"patch_name,omitempty"`
	PatchOff        int64            `json:"patch_off,omitempty"`
	PatchLen        int64            `json:"patch_len,omitempty"`
	ExpectMD5       string           `json:"expect_md5,omitempty"`        // delete pre-check
	AssembleSources []walChunkSource `json:"assemble_sources,omitempty"`  // chunk_assemble (incl §6.4 demotions)
	OriginalFileMD5 string           `json:"original_file_md5,omitempty"` // hdiff_patch pre-apply guard
}

type walChunkSource struct {
	Kind         string `json:"kind"` // "cdn" | "local"
	ChunkName    string `json:"chunk_name"`
	URLPrefix    string `json:"url_prefix"`
	CompressedSz int64  `json:"compressed_sz"`
	UseCompress  bool   `json:"use_compress"`
	OldFile      string `json:"old_file,omitempty"`
	OldOffset    int64  `json:"old_offset,omitempty"`
	DecompSize   int64  `json:"decomp_size"`
	FileOffset   int64  `json:"file_offset"`
	ExpectMD5    string `json:"expect_md5"`
}

const sophonApplyWALName = "sophon_apply.wal"

// toWalChunkSource converts an in-memory sophon.ChunkSource to its JSON-tagged
// WAL form. sophon.Kind uses the SourceCDN/SourceLocal string constants which
// equal "cdn"/"local" (§A.2), so Kind copies through verbatim.
func toWalChunkSource(cs sophon.ChunkSource) walChunkSource {
	return walChunkSource{
		Kind:         cs.Kind,
		ChunkName:    cs.ChunkName,
		URLPrefix:    cs.URLPrefix,
		CompressedSz: cs.CompressedSz,
		UseCompress:  cs.UseCompress,
		OldFile:      cs.OldFile,
		OldOffset:    cs.OldOffset,
		DecompSize:   cs.DecompSize,
		FileOffset:   cs.FileOffset,
		ExpectMD5:    cs.ExpectMD5,
	}
}

// fromWalChunkSource is the inverse. Asset is not persisted in the WAL (it is
// plan-time-only scoping metadata, §A.2) and stays "" on the way back.
func fromWalChunkSource(w walChunkSource) sophon.ChunkSource {
	return sophon.ChunkSource{
		Kind:         w.Kind,
		ChunkName:    w.ChunkName,
		URLPrefix:    w.URLPrefix,
		CompressedSz: w.CompressedSz,
		UseCompress:  w.UseCompress,
		OldFile:      w.OldFile,
		OldOffset:    w.OldOffset,
		DecompSize:   w.DecompSize,
		FileOffset:   w.FileOffset,
		ExpectMD5:    w.ExpectMD5,
	}
}

// firstPending returns the index of the first record whose State != "done",
// or -1 if all done (§6.2 step 2 resume point).
func (w *sophonApplyWAL) firstPending() int {
	for i := range w.Records {
		if w.Records[i].State != "done" {
			return i
		}
	}
	return -1
}

// writeSophonApplyWAL atomically writes wal to <dir>/sophon_apply.wal
// (tmp→write→rename), mirroring v1 progressStore.Persist.
func writeSophonApplyWAL(dir string, wal *sophonApplyWAL) error {
	data, err := json.MarshalIndent(wal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sophon_apply.wal: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sophonApplyWALName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readSophonApplyWAL reads <dir>/sophon_apply.wal with corrupt-file recovery
// (delete + nil) via loadJSONSidecar. (nil, nil) on ENOENT.
func readSophonApplyWAL(dir string) (*sophonApplyWAL, error) {
	return loadJSONSidecar[sophonApplyWAL](filepath.Join(dir, sophonApplyWALName))
}

// walFlusher batches WAL rewrites (§6.1): it writes to disk when ≥50 record
// state-transitions have accumulated since the last flush OR ≥5s of injected
// clock time has elapsed, whichever comes first; Close performs a final flush.
//
// Local test seam (§A.7): the flusher takes an explicit now func defaulting to
// time.Now. This is NOT a global clock interface — it is a per-flusher field
// only, so production code keeps calling time.Now and §A.7's "no clock
// interface" rule holds. Tests inject a fixed/steppable clock.
type walFlusher struct {
	dir     string
	wal     *sophonApplyWAL
	now     func() time.Time // nil → time.Now
	nSince  int              // records mutated since last disk write
	lastAt  time.Time
	flushes int              // TEST-READABLE: total disk rewrites performed (§E.2 P4)
}

const (
	walFlushEveryN = 50
	walFlushEvery  = 5 * time.Second
)

func newWalFlusher(dir string, wal *sophonApplyWAL, now func() time.Time) *walFlusher {
	if now == nil {
		now = time.Now
	}
	return &walFlusher{dir: dir, wal: wal, now: now, lastAt: now()}
}

// maybeFlush records one state transition and rewrites if nSince>=50 OR
// now()-lastAt>=5s; resets counters + increments flushes. Call once per record
// state change.
func (f *walFlusher) maybeFlush() error {
	f.nSince++
	if f.nSince >= walFlushEveryN || f.now().Sub(f.lastAt) >= walFlushEvery {
		return f.flush()
	}
	return nil
}

// flush performs an unconditional rewrite and increments flushes.
func (f *walFlusher) flush() error {
	if err := writeSophonApplyWAL(f.dir, f.wal); err != nil {
		return err
	}
	f.nSince = 0
	f.lastAt = f.now()
	f.flushes++
	return nil
}

// Close performs a final flush regardless of cadence (§6.1).
func (f *walFlusher) Close() error {
	return f.flush()
}
