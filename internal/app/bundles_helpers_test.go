package app

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"omnigate/internal/core"
)

const gid = core.GameID("kurogames/wutheringwaves")

type fakeBundleProv struct {
	mu          sync.Mutex
	id          core.BackendID
	running     bool
	version     core.VersionInfo
	updatePlan  core.UpdatePlan // generic CheckForUpdate result
	checkCalls  atomic.Int32
	runCalls    atomic.Int32
	runErr      error
	cat         core.BundleCatalog
	st          core.BundleInstallState
	catFound    bool
	plan        core.UpdatePlan // BuildBundleInstallPlan result (Bundle set by the call)
	planErr     error
	planCalls   atomic.Int32
	planGate    chan struct{} // non-nil → BuildBundleInstallPlan blocks until closed or ctx done
	removed     []string
	activeSet   []string
	opts        map[string]bool
	noBundleMgr bool
}

// core.Provider
func (f *fakeBundleProv) ID() core.BackendID { return f.id }
func (f *fakeBundleProv) DisplayName() core.LocalizedString {
	return core.LocalizedString{"en": "fake"}
}
func (f *fakeBundleProv) Games() []core.GameDescriptor        { return []core.GameDescriptor{{ID: gid}} }
func (f *fakeBundleProv) SettingsSchema() []core.SettingField { return nil }
func (f *fakeBundleProv) DetectInstall(context.Context) ([]core.InstalledGame, error) {
	return nil, nil
}
func (f *fakeBundleProv) GetIcon(context.Context, core.GameID) (string, error) { return "", nil }
func (f *fakeBundleProv) GetBackgrounds(context.Context, core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *fakeBundleProv) CheckVersion(context.Context, core.GameID) (core.VersionInfo, error) {
	return f.version, nil
}
func (f *fakeBundleProv) Launch(context.Context, core.GameID, core.LaunchOptions) (int, error) {
	return 0, nil
}

// core.ProcessChecker
func (f *fakeBundleProv) IsGameRunning(core.GameID) (bool, error) { return f.running, nil }

// core.Updater
func (f *fakeBundleProv) CheckForUpdate(context.Context, core.GameID) (core.UpdatePlan, error) {
	f.checkCalls.Add(1)
	return f.updatePlan, nil
}
func (f *fakeBundleProv) RunUpdate(context.Context, core.UpdatePlan, func(core.UpdateEvent)) error {
	f.runCalls.Add(1)
	return f.runErr
}

// core.PredownloadChecker (always unsupported, like kurogames after this change)
func (f *fakeBundleProv) SupportsPredownload(core.GameID) bool { return false }
func (f *fakeBundleProv) CheckForPredownload(context.Context, core.GameID, func(int, int)) (core.UpdatePlan, error) {
	return core.UpdatePlan{}, core.ErrPredownloadUnsupported
}

func (f *fakeBundleProv) checkForUpdateCalls() int32 { return f.checkCalls.Load() }
func (f *fakeBundleProv) runUpdateCalls() int32      { return f.runCalls.Load() }

// core.BundleManager
func (f *fakeBundleProv) BundleState(context.Context, core.GameID) (core.BundleCatalog, core.BundleInstallState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := map[string]string{}
	for k, v := range f.st.Installed {
		inst[k] = v
	}
	st := f.st
	st.Installed = inst
	return f.cat, st, f.catFound, nil
}
func (f *fakeBundleProv) SetActiveBundle(_ context.Context, _ core.GameID, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeSet = append(f.activeSet, n)
	f.st.Active = n
	return nil
}
func (f *fakeBundleProv) LaunchOptions(context.Context, core.GameID) (map[string]bool, error) {
	return f.opts, nil
}
func (f *fakeBundleProv) SetLaunchOption(_ context.Context, _ core.GameID, c string, v bool) error {
	f.opts[c] = v
	return nil
}
func (f *fakeBundleProv) BuildBundleInstallPlan(ctx context.Context, g core.GameID, n string, _ func(int, int)) (core.UpdatePlan, error) {
	f.planCalls.Add(1)
	if f.planGate != nil {
		select {
		case <-f.planGate:
		case <-ctx.Done():
			return core.UpdatePlan{}, ctx.Err()
		}
	}
	if f.planErr != nil {
		return core.UpdatePlan{}, f.planErr
	}
	p := f.plan
	p.GameID, p.Kind, p.Bundle = g, core.PlanUpdate, n
	return p, nil
}
func (f *fakeBundleProv) RemoveBundle(_ context.Context, _ core.GameID, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, n)
	delete(f.st.Installed, n)
	return nil
}
func (f *fakeBundleProv) activeSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.activeSet...)
}

// plainProv embeds only the core.Provider interface value, so its method set
// excludes BundleManager/Updater — the "unsupported game" case.
type plainProv struct{ core.Provider }

// newBundleApp builds an isolated App: temp root and game dir under t.TempDir(),
// HD installed @3.7.0, latest 3.7.0, catalog absent.
func newBundleApp(t *testing.T) (*App, *fakeBundleProv) {
	t.Helper()
	f := &fakeBundleProv{
		id:         "kurogames",
		version:    core.VersionInfo{Current: "3.7.0", Latest: "3.7.0"},
		updatePlan: core.UpdatePlan{Kind: core.PlanUpdate, Version: "3.7.0"},
		plan:       core.UpdatePlan{Version: "3.7.0", ManifestETag: "t"},
		st:         core.BundleInstallState{Active: "HD", Installed: map[string]string{"HD": "3.7.0"}},
		opts:       map[string]bool{},
	}
	reg := NewUpdateStateRegistry(func(string, ...any) {}, realClock{})
	t.Cleanup(reg.emitter.Stop)
	a := &App{
		updateRegistry: reg,
		detect:         map[core.BackendID]detectEntry{},
		resolved:       map[core.GameID]resolvedEntry{gid: {Path: t.TempDir()}},
		logger:         slog.Default(),
		startedAt:      time.Now(),
	}
	a.settings = defaultSettings()
	a.settings.App.TempDir = t.TempDir() // HARD requirement: never touch the real %TEMP%\omnigate
	a.providers = []core.Provider{f}
	origProbe := probeGameDirWritable
	probeGameDirWritable = func(string) error { return nil }
	t.Cleanup(func() { probeGameDirWritable = origProbe })
	return a, f
}

func newAppWithPlainProvider(t *testing.T) *App {
	a, f := newBundleApp(t)
	a.providers = []core.Provider{plainProv{f}}
	return a
}

func waitIdle(t *testing.T, a *App, g core.GameID) {
	t.Helper()
	waitInFlightClear(t, a.updateRegistry.Get(g), 2*time.Second)
}
