package app

import (
	"context"
	"io"
	"log/slog"
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

	var wg sync.WaitGroup
	// Readers hammer every guarded read path.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
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
		for i := 0; i < 300; i++ {
			s := a.GetSettings()
			_ = a.UpdateSettings(s)
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	// A healthy run returns the moment wg.Wait() completes (~10s locally), so a
	// generous ceiling costs passing runs nothing; it only guards against a true
	// deadlock (which hangs forever). 15s was too tight for slow/contended CI
	// runners where this workload legitimately exceeds it — use 60s.
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("deadlock: concurrent settings access did not complete within 60s")
	}
}
