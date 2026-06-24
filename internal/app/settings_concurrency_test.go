package app

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"omnigate/internal/core"
)

// newConcurrencyTestApp builds a minimal but fully-wired App: real providers,
// a temp DB store (so UpdateSettings' saveSettingsToDB write works), and an
// update registry (so UpdateStatusAll works).
func newConcurrencyTestApp(t *testing.T) *App {
	t.Helper()
	a := &App{
		store:  openState(t),
		detect: map[core.BackendID]detectEntry{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	a.ctx = context.Background()
	if err := a.constructProviders(); err != nil {
		t.Fatalf("constructProviders: %v", err)
	}
	a.updateRegistry = NewUpdateStateRegistry(func(string, ...any) {}, realClock{})
	t.Cleanup(func() { a.updateRegistry.emitter.Stop() })
	return a
}

func TestSettingsMu_NoDeadlockUnderConcurrency(t *testing.T) {
	a := newConcurrencyTestApp(t)

	// iters is a deadlock CANARY, not a throughput benchmark: a lock-ordering
	// deadlock hangs the first time the bad interleaving occurs, which under
	// continuous reader/writer contention happens within a handful of rounds.
	// Each iteration does real disk I/O though — the writer's UpdateSettings runs
	// saveSettingsToDB (a SQLite fsync) + constructProviders (filesystem
	// DetectInstall ×3 backends) under the write lock, and readers do many
	// os.Stat calls. At 300 iters the test became dominated by disk speed and
	// blew past the deadline on slow/contended CI runners (linear in iters, not a
	// hang — verified). Keep iters modest so wall time reflects the lock
	// discipline, not the runner's disk.
	const iters = 60

	var wg sync.WaitGroup
	// Readers hammer every guarded read path.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = a.GetSettings()
				_, _ = a.ListGames()
				_ = a.ListBackends()
				_ = a.UpdateStatusAll()
				_ = a.knownBackendIDs()
			}
		}()
	}
	// Writer reconstructs providers/settings repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s := a.GetSettings()
			_ = a.UpdateSettings(s)
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	// A healthy run returns the moment wg.Wait() completes, so a generous ceiling
	// costs passing runs nothing; it only fires on a true deadlock (hangs
	// forever). On timeout, dump every goroutine's stack so CI shows exactly
	// where things parked — a bare "deadlock" message gives nothing to debug.
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("deadlock: concurrent settings access did not complete within 60s\n"+
			"=== all goroutine stacks ===\n%s", buf[:n])
	}
}
