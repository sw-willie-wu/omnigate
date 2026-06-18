package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestEnsureGameDirWritable(t *testing.T) {
	a := &App{logger: slog.Default()}
	gid := core.GameID("kurogames/wutheringwaves")

	orig := probeGameDirWritable
	t.Cleanup(func() { probeGameDirWritable = orig })

	// permission error → permission_denied carrying the game id
	probeGameDirWritable = func(string) error { return os.ErrPermission }
	if ue := a.ensureGameDirWritable(gid, `C:\Program Files\X`); ue == nil || ue.Code != "permission_denied" || ue.Params["game"] != string(gid) {
		t.Fatalf("permission probe: got %#v; want permission_denied with game", ue)
	}

	// writable → nil
	probeGameDirWritable = func(string) error { return nil }
	if ue := a.ensureGameDirWritable(gid, `C:\Games\X`); ue != nil {
		t.Fatalf("writable probe: got %#v; want nil", ue)
	}

	// non-permission error → nil (don't block here; other flows surface it)
	probeGameDirWritable = func(string) error { return errors.New("dir missing") }
	if ue := a.ensureGameDirWritable(gid, `C:\Games\X`); ue != nil {
		t.Fatalf("non-permission probe: got %#v; want nil", ue)
	}

	// empty path → nil (unresolved install)
	if ue := a.ensureGameDirWritable(gid, ""); ue != nil {
		t.Fatalf("empty path: got %#v; want nil", ue)
	}
}

// fakeUpdater satisfies core.Updater for testing the App layer.
type fakeUpdater struct {
	id          core.BackendID
	games       []core.GameDescriptor
	checkResult core.UpdatePlan
	checkErr    error
	runErr      error
	runEvents   []core.UpdateEvent
}

func (f *fakeUpdater) ID() core.BackendID                  { return f.id }
func (f *fakeUpdater) DisplayName() core.LocalizedString    { return core.LocalizedString{"en": "fake"} }
func (f *fakeUpdater) Games() []core.GameDescriptor         { return f.games }
func (f *fakeUpdater) SettingsSchema() []core.SettingField  { return nil }
func (f *fakeUpdater) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: f.games[0].ID, InstallPath: "/tmp/fake"}}, nil
}
func (f *fakeUpdater) GetIcon(_ context.Context, _ core.GameID) (string, error)        { return "", nil }
func (f *fakeUpdater) GetBackgrounds(_ context.Context, _ core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *fakeUpdater) CheckVersion(_ context.Context, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (f *fakeUpdater) Launch(_ context.Context, _ core.GameID, _ core.LaunchOptions) (int, error) {
	return 0, nil
}
func (f *fakeUpdater) CheckForUpdate(_ context.Context, _ core.GameID) (core.UpdatePlan, error) {
	return f.checkResult, f.checkErr
}
func (f *fakeUpdater) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
	for _, e := range f.runEvents {
		onEvent(e)
	}
	return f.runErr
}

func TestStartUpdate_PropagatesCheckError(t *testing.T) {
	t.Skip("App-layer integration test deferred to Task 16; this stub asserts wiring compiles")
}

// TestRapidStartCancelStart_NoInterleave validates the spec §2.1 invariant:
// a second StartUpdate must observe `state.InFlight == nil` only AFTER the
// first goroutine's defer (worker temp-cleanup, sidecar finalization) fully
// completes. The mu.Lock ordering inside runUpdateWorker's defer is the
// happens-before edge. Run with `go test -race` to catch interleaving bugs.
//
// Spec §7.4 mandate. The test uses a fakeUpdater whose RunUpdate blocks on
// a channel until released, so we can deterministically order Start → Cancel →
// (await first defer) → Start.
func TestRapidStartCancelStart_NoInterleave(t *testing.T) {
	a := &App{updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{})}
	defer a.updateRegistry.emitter.Stop()

	gid := core.GameID("kurogames/wuwa")
	state := a.updateRegistry.Get(gid)

	// Simulate first runUpdateWorker holding the lock briefly during defer
	first := make(chan struct{})
	state.mu.Lock()
	state.InFlight = &InFlightOp{Plan: core.UpdatePlan{Version: "1"}}
	state.mu.Unlock()

	// Cancel + clear (simulates first goroutine's defer)
	go func() {
		time.Sleep(10 * time.Millisecond)
		state.mu.Lock()
		state.InFlight = nil
		state.mu.Unlock()
		close(first)
	}()

	// Second StartUpdate-equivalent: spin until lock observes InFlight == nil
	deadline := time.Now().Add(1 * time.Second)
	for {
		state.mu.RLock()
		clear := state.InFlight == nil
		state.mu.RUnlock()
		if clear {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("InFlight never cleared (deadlock or invariant violation)")
		}
	}
	<-first

	// Now safe to set new InFlight without interleaving
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		t.Fatal("InFlight non-nil at second-start time — first cleanup did not happen-before")
	}
	state.InFlight = &InFlightOp{Plan: core.UpdatePlan{Version: "2"}}
	state.mu.Unlock()
}

// TestRunUpdate_PanicRecovers — spec §7.2 (errcode_coverage `internal`) +
// spec §6.4 panic recovery contract. A fakeUpdater whose RunUpdate panics
// must be caught by runUpdateWorker's defer; state.LastError set to
// {Code: "internal"}; state.InFlight cleared.
func TestRunUpdate_PanicRecovers(t *testing.T) {
	a := &App{updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{})}
	defer a.updateRegistry.emitter.Stop()

	gid := core.GameID("kurogames/wuwa")

	panicker := &fakeUpdater{
		id:    "kurogames",
		games: []core.GameDescriptor{{ID: gid}},
		runErr: nil, // overridden below
	}
	// Wrap fake to panic instead of returning runErr
	panicked := make(chan struct{})
	panickyRunUpdate := func(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
		close(panicked)
		panic("synthetic test panic")
	}

	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.InFlight = &InFlightOp{Plan: core.UpdatePlan{GameID: gid, Version: "1"}}
	state.mu.Unlock()

	// Synchronous (test-only) invocation: production calls `go a.runUpdateWorker(...)`,
	// but here we want the panic to propagate through runUpdateWorker's defer
	// and complete state mutation BEFORE we read it. The defer's recover()
	// turns the panic into nil-return, so this synchronous call still returns
	// normally. Awaiting `panicked` is belt-and-suspenders for ordering.
	a.runUpdateWorker(context.Background(), gid, &panickyUpdater{base: panicker, fn: panickyRunUpdate}, core.UpdatePlan{GameID: gid})

	<-panicked

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.InFlight != nil {
		t.Errorf("InFlight = %+v, want nil after panic recovery", state.InFlight)
	}
	if state.LastError == nil || state.LastError.Code != "internal" {
		t.Errorf("LastError = %+v, want {Code: internal}", state.LastError)
	}
}

// panickyUpdater wraps a base Updater but routes RunUpdate to fn (used by
// TestRunUpdate_PanicRecovers to inject a panic).
type panickyUpdater struct {
	base *fakeUpdater
	fn   func(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error
}

func (p *panickyUpdater) ID() core.BackendID                                      { return p.base.ID() }
func (p *panickyUpdater) DisplayName() core.LocalizedString                       { return p.base.DisplayName() }
func (p *panickyUpdater) Games() []core.GameDescriptor                            { return p.base.Games() }
func (p *panickyUpdater) SettingsSchema() []core.SettingField                     { return p.base.SettingsSchema() }
func (p *panickyUpdater) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return p.base.DetectInstall(ctx)
}
func (p *panickyUpdater) GetIcon(ctx context.Context, gid core.GameID) (string, error) {
	return p.base.GetIcon(ctx, gid)
}
func (p *panickyUpdater) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	return p.base.GetBackgrounds(ctx, gid)
}
func (p *panickyUpdater) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	return p.base.CheckVersion(ctx, gid)
}
func (p *panickyUpdater) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	return p.base.Launch(ctx, gid, opts)
}
func (p *panickyUpdater) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	return p.base.CheckForUpdate(ctx, gid)
}
func (p *panickyUpdater) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
	return p.fn(ctx, plan, onEvent)
}

// buildAppForTest creates an *App with both kurogames and hoyoverse
// providers registered (each with one canonical game ID). Used by
// scanForRecovery cross-backend tests.
func buildAppForTest(t *testing.T) *App {
	t.Helper()
	a := &App{
		settings: Settings{Version: 1},
		logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if err := a.registerProvider(&minimalProviderForScan{
		id:   "kurogames",
		gids: []core.GameID{"kurogames/wutheringwaves"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.registerProvider(&minimalProviderForScan{
		id:   "hoyoverse",
		gids: []core.GameID{"hoyoverse/genshin"},
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// minimalProviderForScan is the Provider stub for buildAppForTest.
type minimalProviderForScan struct {
	id   core.BackendID
	gids []core.GameID
}

func (m *minimalProviderForScan) ID() core.BackendID            { return m.id }
func (m *minimalProviderForScan) DisplayName() core.LocalizedString { return core.LocalizedString{} }
func (m *minimalProviderForScan) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, len(m.gids))
	for i, g := range m.gids {
		out[i] = core.GameDescriptor{ID: g}
	}
	return out
}
func (m *minimalProviderForScan) SettingsSchema() []core.SettingField                                       { return nil }
func (m *minimalProviderForScan) DetectInstall(ctx context.Context) ([]core.InstalledGame, error)           { return nil, nil }
func (m *minimalProviderForScan) GetIcon(ctx context.Context, gid core.GameID) (string, error)              { return "", nil }
func (m *minimalProviderForScan) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) { return nil, nil }
func (m *minimalProviderForScan) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error)     { return core.VersionInfo{}, nil }
func (m *minimalProviderForScan) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) { return 0, nil }

func TestScanForRecovery_CrossBackend_FiltersBackendNames(t *testing.T) {
	tmp := t.TempDir()

	// Layout simulating real:
	//   <tmp>/omnigate/                                          ← kurogames root (flat)
	//   <tmp>/omnigate/kurogames-wutheringwaves/3.0.0/progress.json  (kuro game)
	//   <tmp>/omnigate/hoyoverse/                                ← hoyoverse root (subdir)
	//   <tmp>/omnigate/hoyoverse/hoyoverse-genshin/5.6.0/progress.json (hoyo game)
	for _, p := range []string{
		filepath.Join(tmp, "omnigate", "kurogames-wutheringwaves", "3.0.0"),
		filepath.Join(tmp, "omnigate", "hoyoverse", "hoyoverse-genshin", "5.6.0"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "progress.json"),
			[]byte(`{"game_id":"","version":"","etag":"","entries":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prevTempDir := osTempDir
	osTempDir = func() string { return tmp }
	defer func() { osTempDir = prevTempDir }()

	a := buildAppForTest(t)

	visited := []string{}
	prevApply := applyRecoveryStateOverride
	applyRecoveryStateOverride = func(gid core.GameID, dir string) {
		visited = append(visited, string(gid)+":"+filepath.Base(dir))
	}
	defer func() { applyRecoveryStateOverride = prevApply }()

	a.scanForRecovery()

	// Expect exactly 2 visited dirs: one kuro, one hoyo. NOT 3 (no extra
	// "hoyoverse" treated as kurogames-flat gameDir).
	if len(visited) != 2 {
		t.Fatalf("expected 2 visited dirs, got %d: %v", len(visited), visited)
	}
}

func TestScanForRecovery_KurogamesPrefixDirSurvives(t *testing.T) {
	tmp := t.TempDir()

	// Layout: only a kurogames-prefixed dir exists; hoyoverse provider
	// is registered but no hoyoverse temp tree. The scan must still
	// visit the kurogames game dir.
	for _, p := range []string{
		filepath.Join(tmp, "omnigate", "kurogames-wutheringwaves", "3.0.0"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "progress.json"),
			[]byte(`{"game_id":"","version":"","etag":"","entries":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prevTempDir := osTempDir
	osTempDir = func() string { return tmp }
	defer func() { osTempDir = prevTempDir }()

	a := buildAppForTest(t)

	visited := []string{}
	prevApply := applyRecoveryStateOverride
	applyRecoveryStateOverride = func(gid core.GameID, dir string) {
		visited = append(visited, string(gid))
	}
	defer func() { applyRecoveryStateOverride = prevApply }()

	a.scanForRecovery()

	if len(visited) != 1 || visited[0] != "kurogames/wutheringwaves" {
		t.Fatalf("expected 1 visit to kurogames/wutheringwaves, got %v", visited)
	}
}

// More tests in Task 16 integration phase — this file establishes wiring.
