package app

import (
	"log/slog"
	"sync"
	"time"

	"omnigate/internal/store"
)

// playState persists per-game last-launch timestamps in omnigate.db (the
// playstate table). It is a *persistent* user-state store, not a temp sidecar.
//
// LOCKING: mu is always the INNERMOST lock. gameRowLocked calls Get while
// holding settingsMu (order settingsMu → mu, fine). NEVER acquire settingsMu
// while holding mu.
type playState struct {
	mu    sync.Mutex
	store store.StateStore
	last  map[string]time.Time
}

// loadPlayState reads the playstate table into a playState. A nil store (DB-open
// failure) or a read error yields an empty, in-memory-only store — last-played
// is best-effort, never fatal.
func loadPlayState(st store.StateStore) *playState {
	ps := &playState{store: st, last: map[string]time.Time{}}
	if st == nil {
		return ps
	}
	m, err := st.AllPlaystate()
	if err != nil {
		slog.Default().Warn("playstate load failed; starting empty", "err", err)
		return ps
	}
	for game, unix := range m {
		ps.last[game] = time.Unix(unix, 0)
	}
	return ps
}

// Get returns the recorded last-played time, or the zero time if none.
func (ps *playState) Get(gameID string) time.Time {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.last[gameID]
}

// Record stamps gameID with the current time and persists. Persist failure is
// logged, not returned: a failed write must not block launch.
func (ps *playState) Record(gameID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.last[gameID] = time.Now()
	if err := ps.saveLocked(gameID); err != nil {
		slog.Default().Warn("playstate save failed", "err", err, "game", gameID)
	}
}

// saveLocked upserts one game's timestamp. Caller must hold ps.mu. No-op when
// the store is nil (degraded mode).
func (ps *playState) saveLocked(gameID string) error {
	if ps.store == nil {
		return nil
	}
	return ps.store.SetPlaystate(gameID, ps.last[gameID].Unix())
}
