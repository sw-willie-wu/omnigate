package app

import (
	"context"
	"sync"
	"time"

	"launcher-collection-tmp/internal/core"
)

// Clock abstracts time.Now / time.NewTicker for tests. Production wires
// realClock; tests inject fakeClock advancing arbitrarily.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }

// GameUpdateState is the per-game update state held by App. Mutated by
// Wails RPC handlers and RunUpdate's onEvent callback under mu.
//
// Invariant (spec §2.1): InFlight != nil iff a runUpdate goroutine has
// been spawned and its defer has not yet completed. Writers that set
// InFlight = nil must do so under mu.Lock() AFTER all worker-side cleanup.
type GameUpdateState struct {
	mu              sync.RWMutex
	AvailableUpdate *core.UpdatePlan
	AvailablePredl  *core.UpdatePlan
	InFlight        *InFlightOp
	LastError       *core.UpdateError
	PredlReady      *core.UpdatePlan
}

// InFlightOp describes the currently-running update operation for a game.
// Fields are mutated by RunUpdate's onEvent (Current/Total/Phase) and read
// by snapshot copy. cancel is unexported because only App-layer code calls it.
type InFlightOp struct {
	Plan      core.UpdatePlan
	Phase     core.Phase
	Stage     string // "" (use Phase) | "verifying" — set during runStartUpdateAsync's CheckForUpdate so UI can render "驗證本地檔案" before bytes start flowing
	Current   int64
	Total     int64
	cancel    context.CancelFunc
	StartedAt time.Time
}

// GameUpdateSnapshot is the JSON-serializable snapshot returned by
// UpdateStatusAll RPC. NO pointers into live state — pure value copy
// to prevent torn reads in the frontend.
type GameUpdateSnapshot struct {
	AvailableUpdate *core.UpdatePlan      `json:"available_update,omitempty"`
	AvailablePredl  *core.UpdatePlan      `json:"available_predl,omitempty"`
	InFlight        *InFlightSnapshot     `json:"in_flight,omitempty"`
	LastError       *core.UpdateError     `json:"last_error,omitempty"`
	PredlReady      *core.UpdatePlan      `json:"predl_ready,omitempty"`
}

type InFlightSnapshot struct {
	Kind      core.PlanKind `json:"kind"`
	Phase     core.Phase    `json:"phase"`
	Stage     string        `json:"stage,omitempty"`
	Current   int64         `json:"current"`
	Total     int64         `json:"total"`
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"started_at"`
}

// Snapshot returns a value copy of the state under RLock. Safe to send to
// frontend; no pointer aliasing.
func (s *GameUpdateState) Snapshot() GameUpdateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := GameUpdateSnapshot{
		AvailableUpdate: copyPlan(s.AvailableUpdate),
		AvailablePredl:  copyPlan(s.AvailablePredl),
		PredlReady:      copyPlan(s.PredlReady),
	}
	if s.InFlight != nil {
		out.InFlight = &InFlightSnapshot{
			Kind:      s.InFlight.Plan.Kind,
			Phase:     s.InFlight.Phase,
			Stage:     s.InFlight.Stage,
			Current:   s.InFlight.Current,
			Total:     s.InFlight.Total,
			Version:   s.InFlight.Plan.Version,
			StartedAt: s.InFlight.StartedAt,
		}
	}
	if s.LastError != nil {
		errCopy := *s.LastError
		// Copy params map to break aliasing
		if errCopy.Params != nil {
			pp := make(map[string]string, len(errCopy.Params))
			for k, v := range errCopy.Params {
				pp[k] = v
			}
			errCopy.Params = pp
		}
		out.LastError = &errCopy
	}
	return out
}

func copyPlan(p *core.UpdatePlan) *core.UpdatePlan {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Files != nil {
		cp.Files = make([]core.FileTask, len(p.Files))
		copy(cp.Files, p.Files)
	}
	return &cp
}

// UpdateStateRegistry holds per-game update state and runs a single
// ticker-drain emitter goroutine that throttles Wails event emission to
// ~8 Hz for byte-progress, with phase-transition / cancel / error / done
// events bypassing via synchronous direct emit.
type UpdateStateRegistry struct {
	mu        sync.Mutex
	games     map[core.GameID]*GameUpdateState
	emitter   *eventEmitter
}

func NewUpdateStateRegistry(emit func(name string, args ...any), clock Clock) *UpdateStateRegistry {
	r := &UpdateStateRegistry{
		games: map[core.GameID]*GameUpdateState{},
	}
	r.emitter = newEventEmitter(emit, clock)
	r.emitter.start()
	return r
}

// Get returns (or creates) the state for a game. Idempotent.
func (r *UpdateStateRegistry) Get(gid core.GameID) *GameUpdateState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.games[gid]
	if !ok {
		st = &GameUpdateState{}
		r.games[gid] = st
	}
	return st
}

// EmitChanged queues a snapshot of game's state for the next 125ms tick.
// Non-blocking; replaces any pending snapshot for the same gameID.
func (r *UpdateStateRegistry) EmitChanged(gid core.GameID) {
	st := r.Get(gid)
	r.emitter.queueProgress(string(gid), st.Snapshot())
}

// EmitTerminal emits IMMEDIATELY (bypasses throttle). Used for phase
// transitions, cancel, error, done.
func (r *UpdateStateRegistry) EmitTerminal(gid core.GameID) {
	st := r.Get(gid)
	r.emitter.emitNow(string(gid), st.Snapshot())
}

// SnapshotAll returns a value-copy map for UpdateStatusAll RPC.
func (r *UpdateStateRegistry) SnapshotAll() map[string]GameUpdateSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]GameUpdateSnapshot, len(r.games))
	for gid, st := range r.games {
		out[string(gid)] = st.Snapshot()
	}
	return out
}

// eventEmitter implements the ticker-drain throttle: 125ms ticker + 1-buf
// channel + non-blocking replace-on-full per gameID; phase transitions
// bypass via emitNow.
type eventEmitter struct {
	emit  func(name string, args ...any)
	clock Clock

	mu      sync.Mutex
	pending map[string]GameUpdateSnapshot // gameID → latest queued snapshot

	stop chan struct{}
}

func newEventEmitter(emit func(name string, args ...any), clock Clock) *eventEmitter {
	return &eventEmitter{
		emit:    emit,
		clock:   clock,
		pending: map[string]GameUpdateSnapshot{},
		stop:    make(chan struct{}),
	}
}

func (e *eventEmitter) start() {
	go e.run()
}

func (e *eventEmitter) Stop() { close(e.stop) }

func (e *eventEmitter) queueProgress(gameID string, snap GameUpdateSnapshot) {
	e.mu.Lock()
	e.pending[gameID] = snap // latest-wins
	e.mu.Unlock()
}

func (e *eventEmitter) emitNow(gameID string, snap GameUpdateSnapshot) {
	e.mu.Lock()
	delete(e.pending, gameID) // drop any pending byte-progress; this terminal supersedes
	e.mu.Unlock()
	e.emit("update:changed", gameID, snap)
}

func (e *eventEmitter) run() {
	ticker := e.clock.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			e.drain()
		}
	}
}

func (e *eventEmitter) drain() {
	e.mu.Lock()
	if len(e.pending) == 0 {
		e.mu.Unlock()
		return
	}
	pending := e.pending
	e.pending = map[string]GameUpdateSnapshot{}
	e.mu.Unlock()
	for gameID, snap := range pending {
		e.emit("update:changed", gameID, snap)
	}
}
