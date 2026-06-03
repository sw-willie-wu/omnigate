package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// playState persists per-game last-launch timestamps. It is a *persistent*
// user-state file (not a temp sidecar), stored alongside settings.toml.
//
// LOCKING: mu is always the INNERMOST lock. gameRowLocked calls Get while
// holding settingsMu (order settingsMu → mu, fine). NEVER acquire settingsMu
// while holding mu.
type playState struct {
	mu   sync.Mutex
	path string
	last map[string]time.Time
}

// loadPlayState reads path into a playState. A missing or corrupt file yields
// an empty (but writable) store — last-played is best-effort, never fatal.
func loadPlayState(path string) *playState {
	ps := &playState{path: path, last: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Default().Warn("playstate read failed; starting empty", "err", err, "path", path)
		}
		return ps
	}
	if err := json.Unmarshal(b, &ps.last); err != nil {
		slog.Default().Warn("playstate parse failed; starting empty", "err", err, "path", path)
		ps.last = map[string]time.Time{}
	}
	return ps
}

// Get returns the recorded last-played time, or the zero time if none.
func (ps *playState) Get(gameID string) time.Time {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.last[gameID]
}

// Record stamps gameID with the current time and persists atomically.
// Persist failure is logged, not returned: a failed write must not block launch.
func (ps *playState) Record(gameID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.last[gameID] = time.Now()
	if err := ps.saveLocked(); err != nil {
		slog.Default().Warn("playstate save failed", "err", err, "path", ps.path)
	}
}

// saveLocked writes atomically (temp → rename). Caller must hold ps.mu.
func (ps *playState) saveLocked() error {
	b, err := json.MarshalIndent(ps.last, "", "  ")
	if err != nil {
		return err
	}
	tmp := ps.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ps.path)
}

// playStatePathFor derives the playstate.json location from the settings path
// (same directory). With the current main.go (app.New("") → "settings.toml"),
// filepath.Dir is "." → process CWD, alongside settings.toml. Known current
// behavior; revisit on settings-path change / SQLite migration.
func playStatePathFor(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "playstate.json")
}
