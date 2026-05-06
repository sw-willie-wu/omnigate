package kurogames

import (
	"encoding/json"
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
	pf, err := core.LoadProgressFromPath(filepath.Join(p.dir(), "progress.json"))
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
