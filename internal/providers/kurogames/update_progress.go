package kurogames

import (
	"encoding/json"
	"fmt"
	"omnigate/internal/core"
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

// Init creates the progress dir and writes progress.json.
//
// Resume-aware: if an existing progress.json carries the SAME ETag, its Entries
// are preserved so a re-started or resumed download skips already-completed
// files (this is what makes ResumeInterrupted's documented "resume from
// progress.json" and a re-clicked predownload actually resume rather than
// re-download). A missing/corrupt file, or a changed ETag (the manifest changed
// → staged bytes are stale), falls back to a fresh empty progress.json AND
// deletes every staged .part under the version dir — chunked-resume .part
// files now persist across sessions, and stale ones must not be resumed
// against a new manifest's chunk table.
func (p *progressStore) Init(etag string) error {
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		return fmt.Errorf("mkdir progress: %w", err)
	}
	if existing, err := core.LoadProgressFromPath(filepath.Join(p.dir(), "progress.json")); err == nil && existing != nil && existing.ETag == etag {
		return nil // keep prior Entries → resume
	}
	p.removeStaleParts()
	pf := core.ProgressFile{
		GameID:  p.gameID,
		Version: p.version,
		ETag:    etag,
		Entries: map[string]core.ProgressEntry{},
	}
	return p.writeAtomic("progress.json", &pf)
}

// removeStaleParts deletes every *.part under the version dir. Must walk
// recursively: .part files live at the file's final relative path (e.g.
// Client/Content/Paks/x.pak.part) — a top-level glob would miss exactly the
// big paks chunked resume exists for. Best-effort: a locked/undeletable
// .part only costs disk until the next manifest change.
func (p *progressStore) removeStaleParts() {
	_ = filepath.WalkDir(p.dir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr — best-effort sweep
		}
		if strings.HasSuffix(d.Name(), ".part") {
			_ = os.Remove(path)
		}
		return nil
	})
}

// MarkComplete records that <relPath> finished download + verify.
// Holds p.mu so concurrent download workers serialize their load-modify-write
// of progress.json (without the lock, last writer wins and earlier entries
// are silently dropped — verified bug found in Task 8 code review).
//
// hash is the manifest-provided (already-verified) per-file hash — download
// verification proved the file matches it, so callers must pass that value
// rather than re-hashing here (spec §2.5). It lets ConsumePredlStaged later
// validate individual staged files without re-checking the whole ETag.
func (p *progressStore) MarkComplete(relPath string, mtime time.Time, size int64, hash string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pf, err := core.LoadProgressFromPath(filepath.Join(p.dir(), "progress.json"))
	if err != nil {
		return err
	}
	pf.Entries[relPath] = core.ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond), Hash: hash}
	return p.writeAtomic("progress.json", pf)
}

// RenameToPredlReady atomically renames progress.json → predl_ready.json
// (spec §2.2) at end of PlanPredownload's download phase.
func (p *progressStore) RenameToPredlReady() error {
	src := filepath.Join(p.dir(), "progress.json")
	dst := filepath.Join(p.dir(), "predl_ready.json")
	return os.Rename(src, dst)
}

// ConsumePredlStaged detects a staged predl_ready.json for p.version, restores
// it as progress.json stamped with newETag (correctness moves from ETag to
// per-file Hash — spec §2.5), and deletes predl_ready.json. Entries whose Hash
// mismatches wantHash[rel] (or, for legacy hash-less entries, whose on-disk
// re-hash mismatches) are dropped → re-downloaded. Returns (adopted, staged
// Ephemeral bytes on disk, error). (false, 0, nil) when nothing staged or
// version mismatch (predl for another version stays untouched).
func (p *progressStore) ConsumePredlStaged(newETag string, wantHash map[string]string, ephemeral map[string]bool) (bool, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	predlPath := filepath.Join(p.dir(), "predl_ready.json")
	pf, err := core.LoadProgressFromPath(predlPath)
	if err != nil || pf == nil {
		return false, 0, nil //nolint:nilerr — no staged predl is not an error
	}
	if pf.Version != p.version {
		return false, 0, nil // predl for another version — leave untouched
	}

	var stagedEphemeralBytes int64
	for rel, e := range pf.Entries {
		want, ok := wantHash[rel]
		if !ok {
			// New manifest no longer needs this file.
			delete(pf.Entries, rel)
			continue
		}

		matched := false
		if e.Hash != "" {
			matched = e.Hash == want
		} else {
			// Legacy hash-less entry (staged before Task 7): re-hash from disk.
			onDisk, hashErr := md5File(filepath.Join(p.dir(), rel))
			matched = hashErr == nil && onDisk == want
		}

		if !matched {
			delete(pf.Entries, rel)
			_ = os.Remove(filepath.Join(p.dir(), rel))
			continue
		}

		if ephemeral[rel] {
			stagedEphemeralBytes += e.Size
		}
	}

	pf.ETag = newETag
	if err := p.writeAtomic("progress.json", pf); err != nil {
		return false, 0, err
	}
	_ = os.Remove(predlPath)
	return true, stagedEphemeralBytes, nil
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
