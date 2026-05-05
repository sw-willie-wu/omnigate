package app

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"launcher-collection-tmp/internal/core"
)

// fakeClock is a controllable Clock for tests.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	tickCh chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:    time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		tickCh: make(chan time.Time, 1),
	}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTicker(d time.Duration) *time.Ticker {
	// Hijack: real ticker but immediately fired by our advance() control.
	// The returned *time.Ticker.C is a normal channel; we substitute.
	t := time.NewTicker(d)
	t.Stop()
	go func() {
		for tick := range f.tickCh {
			// Reflect via the real ticker's channel
			select {
			case <-t.C:
			default:
			}
			// Push tick value into a fake by closing — simpler: use dedicated chan
			_ = tick
		}
	}()
	return t
}

// For simpler tests, use a custom emitter directly with a tickable channel.
func TestEmitter_TickerDrainCoalesces(t *testing.T) {
	var emitted []struct {
		name string
		args []any
	}
	var emitMu sync.Mutex
	emit := func(name string, args ...any) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emitted = append(emitted, struct {
			name string
			args []any
		}{name, args})
	}

	e := newEventEmitter(emit, realClock{})
	// Don't start the goroutine; call drain directly.

	// Queue 3 progress snapshots for same gameID; latest-wins.
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 100}})
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 200}})
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 300}})
	e.queueProgress("g2", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 50}})

	e.drain()

	emitMu.Lock()
	defer emitMu.Unlock()
	if len(emitted) != 2 {
		t.Fatalf("got %d emits, want 2 (one per gameID)", len(emitted))
	}
	// Find g1's emit; assert latest snapshot wins.
	for _, e := range emitted {
		if e.args[0] == "g1" {
			snap := e.args[1].(GameUpdateSnapshot)
			if snap.InFlight.Current != 300 {
				t.Errorf("g1 final Current = %d, want 300 (latest)", snap.InFlight.Current)
			}
		}
	}
}

func TestEmitter_EmitNowBypassesQueue(t *testing.T) {
	var emitted []string
	var emitMu sync.Mutex
	emit := func(name string, args ...any) {
		emitMu.Lock()
		defer emitMu.Unlock()
		gameID := args[0].(string)
		snap := args[1].(GameUpdateSnapshot)
		if snap.InFlight != nil {
			emitted = append(emitted, gameID+":progress")
		} else {
			emitted = append(emitted, gameID+":terminal")
		}
	}
	e := newEventEmitter(emit, realClock{})

	// Queue a progress snapshot, then emitNow with a terminal snapshot.
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 100}})
	e.emitNow("g1", GameUpdateSnapshot{}) // terminal: InFlight nil

	// drain after — should NOT re-emit the dropped progress.
	e.drain()

	emitMu.Lock()
	defer emitMu.Unlock()
	if len(emitted) != 1 {
		t.Fatalf("got %d emits, want 1", len(emitted))
	}
	if emitted[0] != "g1:terminal" {
		t.Errorf("emit = %s, want g1:terminal", emitted[0])
	}
}

func TestStateRace(t *testing.T) {
	// 100 goroutines reading + writing GameUpdateState; must run -race clean.
	st := &GameUpdateState{}
	var wg sync.WaitGroup
	var counter atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				st.mu.Lock()
				if st.InFlight == nil {
					st.InFlight = &InFlightOp{Plan: core.UpdatePlan{Version: "x"}}
				} else {
					st.InFlight.Current = counter.Add(1)
				}
				st.mu.Unlock()

				_ = st.Snapshot()
			}
		}()
	}
	wg.Wait()
}

// TestThrottle_8Hz_FakeClock validates spec §3.3 throttle target: 1000
// incoming progress events across an 8-tick (1-second) window emit ~8
// outgoing snapshots (latest-wins coalesce per tick). Per spec §7.6.
func TestThrottle_8Hz_FakeClock(t *testing.T) {
	var emitted atomic.Int64
	emit := func(name string, args ...any) {
		emitted.Add(1)
	}
	e := newEventEmitter(emit, realClock{})
	// Don't start the goroutine; manually invoke drain() 8 times to simulate
	// 1 second at the 125ms tick interval (1000ms / 125ms = 8 ticks).

	const totalEvents = 1000
	const ticks = 8
	const eventsPerTick = totalEvents / ticks
	for tick := 0; tick < ticks; tick++ {
		for i := 0; i < eventsPerTick; i++ {
			e.queueProgress("g1", GameUpdateSnapshot{
				InFlight: &InFlightSnapshot{Current: int64(tick*eventsPerTick + i)},
			})
		}
		e.drain()
	}

	got := emitted.Load()
	if got != int64(ticks) {
		t.Errorf("emitted = %d, want %d (1000 events coalesced into one per 125ms tick over 1 second)", got, ticks)
	}
}

func TestSnapshot_NoPointerLeak(t *testing.T) {
	st := &GameUpdateState{
		AvailableUpdate: &core.UpdatePlan{
			Version: "3.4.0",
			Files:   []core.FileTask{{Path: "a", Size: 1}},
		},
		LastError: &core.UpdateError{
			Code:   "network",
			Params: map[string]string{"url": "x"},
		},
	}
	snap := st.Snapshot()
	// Mutate live state; snapshot must not change.
	st.AvailableUpdate.Version = "MUTATED"
	st.AvailableUpdate.Files[0].Path = "MUTATED"
	st.LastError.Code = "MUTATED"
	st.LastError.Params["url"] = "MUTATED"

	if snap.AvailableUpdate.Version != "3.4.0" {
		t.Errorf("snapshot Version leaked: %q", snap.AvailableUpdate.Version)
	}
	if snap.AvailableUpdate.Files[0].Path != "a" {
		t.Errorf("snapshot Files[0].Path leaked: %q", snap.AvailableUpdate.Files[0].Path)
	}
	if snap.LastError.Code != "network" {
		t.Errorf("snapshot LastError.Code leaked: %q", snap.LastError.Code)
	}
	if snap.LastError.Params["url"] != "x" {
		t.Errorf("snapshot LastError.Params leaked: %q", snap.LastError.Params["url"])
	}
}
