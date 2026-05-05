package kurogames

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"launcher-collection-tmp/internal/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)


type progressStore struct {
	// mu serializes concurrent MarkComplete calls from the download
	// worker pool (Task 8 spawns 4 workers; each load-modify-writes
	// progress.json; without this lock the last writer would clobber
	// the others' entries).
	mu       sync.Mutex
	tempRoot string
	gameID   string
	version  string
}

func newProgressStore(tempRoot, gameID, version string) *progressStore {
	return &progressStore{tempRoot: tempRoot, gameID: gameID, version: version}
}

func (p *progressStore) dir() string {
	flat := strings.ReplaceAll(p.gameID, "/", "-")
	return filepath.Join(p.tempRoot, flat, p.version)
}

// Init creates the progress dir and writes a fresh progress.json.
func (p *progressStore) Init(etag string) error {
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		return fmt.Errorf("mkdir progress: %w", err)
	}
	pf := core.ProgressFile{
		GameID:  p.gameID,
		Version: p.version,
		ETag:    etag,
		Entries: map[string]core.ProgressEntry{},
	}
	return p.writeAtomic("progress.json", &pf)
}

// MarkComplete records that <relPath> finished download + verify.
// Holds p.mu so concurrent download workers serialize their load-modify-write
// of progress.json (without the lock, last writer wins and earlier entries
// are silently dropped — verified bug found in Task 8 code review).
func (p *progressStore) MarkComplete(relPath string, mtime time.Time, size int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pf, err := loadProgressFile(filepath.Join(p.dir(), "progress.json"))
	if err != nil {
		return err
	}
	pf.Entries[relPath] = core.ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond)}
	return p.writeAtomic("progress.json", pf)
}

// RenameToPredlReady atomically renames progress.json → predl_ready.json
// (spec §2.2) at end of PlanPredownload's download phase.
func (p *progressStore) RenameToPredlReady() error {
	src := filepath.Join(p.dir(), "progress.json")
	dst := filepath.Join(p.dir(), "predl_ready.json")
	return os.Rename(src, dst)
}

func (p *progressStore) writeAtomic(name string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	finalPath := filepath.Join(p.dir(), name)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// LoadProgress parses progress.json from the given dir.
func LoadProgress(dir string) (*core.ProgressFile, error) {
	return loadProgressFile(filepath.Join(dir, "progress.json"))
}

// LoadProgressFromPath parses a sidecar ProgressFile (progress.json or
// predl_ready.json — same schema) from an explicit path. Used by App
// layer's ResumeInterrupted ETag drift check (spec §2.3).
func LoadProgressFromPath(path string) (*core.ProgressFile, error) {
	return loadProgressFile(path)
}

// ReadWALETag returns the ETag recorded in apply.wal's header line.
// Returns "" if file missing/unreadable/header malformed. Used by App
// layer's ResumeInterrupted ETag drift check (spec §2.3).
//
// WAL format (set by applier in Task 9): line 1 is JSON header
// `{"etag":"<value>","plan_files":[...]}`; subsequent lines are
// `<relpath> OK\n` per applied file.
func ReadWALETag(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Header is single line; split on first newline
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		nl = len(body)
	}
	var hdr struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body[:nl], &hdr); err != nil {
		return ""
	}
	return hdr.ETag
}

func loadProgressFile(path string) (*core.ProgressFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf core.ProgressFile
	if err := json.Unmarshal(body, &pf); err != nil {
		return nil, fmt.Errorf("progress json parse: %w", err)
	}
	return &pf, nil
}


// ScanRecovery resolves sidecar collisions per spec §6.3 + §2.3.
// WasPredl is set from apply.wal's `was_predl` header field (Task 9 writes
// it via applyWAL.WasPredl) — this distinguishes "interrupted apply that
// originated from a predl" from "interrupted apply from a fresh download",
// which spec §3.5 row 4 surfaces in the resume prompt copy.
func ScanRecovery(dir string) core.RecoveryState {
	hasProgress := fileExists(filepath.Join(dir, "progress.json"))
	hasWAL := fileExists(filepath.Join(dir, "apply.wal"))
	hasPredl := fileExists(filepath.Join(dir, "predl_ready.json"))

	switch {
	case hasWAL:
		if hasProgress {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
		}
		if hasPredl {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
		}
		walPath := filepath.Join(dir, "apply.wal")
		body, err := os.ReadFile(walPath)
		if err != nil {
			return core.RecoveryState{Phase: core.RecoveryCorrupt, Err: err}
		}
		// Parse header for was_predl flag. Fall back to RecoveryCorrupt on
		// malformed JSON — caller surfaces `unrecoverable` per spec §6.3.
		var hdr struct {
			WasPredl bool `json:"was_predl"`
		}
		if err := json.Unmarshal(body, &hdr); err != nil {
			return core.RecoveryState{Phase: core.RecoveryCorrupt, Err: err}
		}
		return core.RecoveryState{Phase: core.RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

	case hasProgress && hasPredl:
		_ = os.Remove(filepath.Join(dir, "progress.json"))
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return core.RecoveryState{Phase: core.RecoveryNone}
		}
		return core.RecoveryState{Phase: core.RecoveryPhasePredlAwaiting}

	case hasProgress:
		if _, err := loadProgressFile(filepath.Join(dir, "progress.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
			return core.RecoveryState{Phase: core.RecoveryNone}
		}
		return core.RecoveryState{Phase: core.RecoveryPhaseDownloadResume}

	case hasPredl:
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return core.RecoveryState{Phase: core.RecoveryNone}
		}
		return core.RecoveryState{Phase: core.RecoveryPhasePredlAwaiting}

	default:
		return core.RecoveryState{Phase: core.RecoveryNone}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
