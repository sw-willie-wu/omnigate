package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"omnigate/internal/core"
)

// sophonProgressFile is the chunk-level download progress sidecar
// (<versionSidecarDir>/sophon_progress.json, §A.6 / §5.2). v1's progress.json
// (core.ProgressFile, per-file granularity) is untouched and still used by
// HSR/ZZZ. Keys (ChunkName / PatchName) are the full CDN filenames from the
// manifest.
type sophonProgressFile struct {
	GameID      string          `json:"game_id"`
	Version     string          `json:"version"`
	BranchKind  string          `json:"branch_kind"`  // "main" | "predl"
	BuildID     string          `json:"build_id"`
	Stage       string          `json:"stage"`        // "download" | "apply"
	ChunksDone  map[string]bool `json:"chunks_done"`  // key = ChunkName
	PatchesDone map[string]bool `json:"patches_done"` // key = PatchName
}

// sophonProgressStore guards a sophonProgressFile with mu held across the
// whole atomic write, mirroring v1 progressStore.Persist so the 4-worker
// download pool (Task 19) cannot collide on the shared *.tmp during Rename.
type sophonProgressStore struct {
	mu       sync.Mutex
	tempRoot string
	gid      core.GameID
	version  string
	pf       *sophonProgressFile
}

// newSophonProgressStore loads an existing sophon_progress.json (resume) or
// creates a fresh one. On resume it preserves ChunksDone/PatchesDone but
// refreshes the header fields to the current run's values.
func newSophonProgressStore(tempRoot string, gid core.GameID, version, branchKind, buildID string) (*sophonProgressStore, error) {
	dir := versionSidecarDir(tempRoot, gid, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pf, err := loadSophonProgress(tempRoot, gid, version)
	if err != nil {
		return nil, err
	}
	if pf == nil {
		pf = &sophonProgressFile{
			GameID:      string(gid),
			Version:     version,
			BranchKind:  branchKind,
			BuildID:     buildID,
			Stage:       "download",
			ChunksDone:  make(map[string]bool),
			PatchesDone: make(map[string]bool),
		}
	} else {
		pf.GameID = string(gid)
		pf.Version = version
		pf.BranchKind = branchKind
		pf.BuildID = buildID
		if pf.ChunksDone == nil {
			pf.ChunksDone = make(map[string]bool)
		}
		if pf.PatchesDone == nil {
			pf.PatchesDone = make(map[string]bool)
		}
	}
	st := &sophonProgressStore{tempRoot: tempRoot, gid: gid, version: version, pf: pf}
	return st, nil
}

// loadSophonProgress reads the sidecar with corrupt-file recovery (delete +
// nil), matching loadJSONSidecar semantics. Returns (nil, nil) on ENOENT.
func loadSophonProgress(tempRoot string, gid core.GameID, version string) (*sophonProgressFile, error) {
	path := filepath.Join(versionSidecarDir(tempRoot, gid, version), "sophon_progress.json")
	return loadJSONSidecar[sophonProgressFile](path)
}

func (s *sophonProgressStore) path() string {
	return filepath.Join(versionSidecarDir(s.tempRoot, s.gid, s.version), "sophon_progress.json")
}

// Save atomically writes the current progress: mu held across the whole
// tmp→write→rename (v1 progressStore.Persist idiom).
func (s *sophonProgressStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *sophonProgressStore) saveLocked() error {
	data, err := json.MarshalIndent(s.pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sophon_progress.json: %w", err)
	}
	dir := versionSidecarDir(s.tempRoot, s.gid, s.version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := s.path()
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

// MarkChunkDone records a chunk as verified-on-disk and persists.
func (s *sophonProgressStore) MarkChunkDone(chunkName string) error {
	s.mu.Lock()
	s.pf.ChunksDone[chunkName] = true
	s.mu.Unlock()
	return s.Save()
}

// MarkPatchDone records a patch blob as verified-on-disk and persists.
func (s *sophonProgressStore) MarkPatchDone(patchName string) error {
	s.mu.Lock()
	s.pf.PatchesDone[patchName] = true
	s.mu.Unlock()
	return s.Save()
}

// ChunkDone reports whether chunkName is already done (resume skip, §5.2 step 1).
func (s *sophonProgressStore) ChunkDone(chunkName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pf.ChunksDone[chunkName]
}

// PatchDone reports whether patchName is already done.
func (s *sophonProgressStore) PatchDone(patchName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pf.PatchesDone[patchName]
}

// SetStage updates the stage field ("download"|"apply") and persists.
func (s *sophonProgressStore) SetStage(stage string) error {
	s.mu.Lock()
	s.pf.Stage = stage
	s.mu.Unlock()
	return s.Save()
}
