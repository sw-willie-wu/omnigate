package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"omnigate/internal/core"
)

type planSnapshot struct {
	SourceVersion  string           `json:"source_version"`
	TargetVersion  string           `json:"target_version"`
	Files          []core.FileTask  `json:"files"`
	AudioLanguages []string         `json:"audio_languages"`
	ManifestETag   string           `json:"manifest_etag"`
}

type predlReadyFile struct {
	core.ProgressFile
	PlanSnapshot planSnapshot `json:"plan_snapshot"`
}

type progressStore struct {
	mu       sync.Mutex
	tempRoot string
	gid      core.GameID
	version  string
	pf       *core.ProgressFile
}

func newProgressStore(tempRoot string, gid core.GameID, version string, etag string) (*progressStore, error) {
	dir := versionSidecarDir(tempRoot, gid, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "progress.json")
	pf, err := loadJSONSidecar[core.ProgressFile](path)
	if err != nil {
		return nil, err
	}
	if pf == nil {
		pf = &core.ProgressFile{
			GameID:  string(gid),
			Version: version,
			ETag:    etag,
			Entries: make(map[string]core.ProgressEntry),
		}
	}
	if pf.Entries == nil {
		pf.Entries = make(map[string]core.ProgressEntry)
	}
	return &progressStore{
		tempRoot: tempRoot,
		gid:      gid,
		version:  version,
		pf:       pf,
	}, nil
}

func (ps *progressStore) versionDir() string {
	return versionSidecarDir(ps.tempRoot, ps.gid, ps.version)
}

func (ps *progressStore) snapshot() core.ProgressFile {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := *ps.pf
	out.Entries = make(map[string]core.ProgressEntry, len(ps.pf.Entries))
	for k, v := range ps.pf.Entries {
		out.Entries[k] = v
	}
	return out
}

func (ps *progressStore) MarkComplete(relPath string, size int64, mtime time.Time, hash string) error {
	ps.mu.Lock()
	ps.pf.Entries[relPath] = core.ProgressEntry{
		Size:  size,
		MTime: mtime,
		Hash:  hash,
	}
	ps.mu.Unlock()
	return ps.Persist()
}

func (ps *progressStore) Persist() error {
	ps.mu.Lock()
	data, err := json.MarshalIndent(ps.pf, "", "  ")
	ps.mu.Unlock()
	if err != nil {
		return fmt.Errorf("marshal progress.json: %w", err)
	}
	dir := ps.versionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "progress.json")
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

func (ps *progressStore) AllEntriesComplete(files []core.FileTask) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, f := range files {
		if _, ok := ps.pf.Entries[f.Path]; !ok {
			return false
		}
	}
	return true
}

func (ps *progressStore) InvalidateRemoved(newFiles []core.FileTask) error {
	wanted := make(map[string]struct{}, len(newFiles))
	for _, f := range newFiles {
		wanted[f.Path] = struct{}{}
	}
	ps.mu.Lock()
	toRemove := []string{}
	for k := range ps.pf.Entries {
		if _, ok := wanted[k]; !ok {
			toRemove = append(toRemove, k)
		}
	}
	for _, k := range toRemove {
		delete(ps.pf.Entries, k)
	}
	ps.mu.Unlock()

	dir := ps.versionDir()
	for _, k := range toRemove {
		p := filepath.Join(dir, k)
		_ = os.Remove(p)
		_ = os.Remove(p + ".part")
	}
	return ps.Persist()
}

func (ps *progressStore) RenameToPredlReady(snap planSnapshot) error {
	dir := ps.versionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	prf := predlReadyFile{
		ProgressFile: *ps.pf,
		PlanSnapshot: snap,
	}
	data, err := json.MarshalIndent(prf, "", "  ")
	if err != nil {
		return err
	}
	predlPath := filepath.Join(dir, "predl_ready.json")
	tmp := predlPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, predlPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(filepath.Join(dir, "progress.json"))
	return nil
}
