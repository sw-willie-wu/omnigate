package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"omnigate/internal/core"
)

// newConcurrencyTestApp builds a minimal but fully-wired App: real providers,
// a temp settings path (so UpdateSettings' disk write works), and an update
// registry (so UpdateStatusAll works).
func newConcurrencyTestApp(t *testing.T) *App {
	t.Helper()
	a := &App{
		settingsP: filepath.Join(t.TempDir(), "settings.toml"),
		detect:    map[core.BackendID]detectEntry{},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: concurrent settings access did not complete within 15s")
	}
}
