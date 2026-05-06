package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"omnigate/internal/core"
)

// checkUpdaterFake is a minimal Provider+Updater that lets tests inject the
// CheckForUpdate plan and the CheckVersion result independently.
type checkUpdaterFake struct {
	gid                core.GameID
	checkVersionResult core.VersionInfo
	checkVersionErr    error
	checkForUpdatePlan core.UpdatePlan
	checkForUpdateErr  error
}

func (f *checkUpdaterFake) ID() core.BackendID                  { return "kurogames" }
func (f *checkUpdaterFake) DisplayName() core.LocalizedString    { return core.LocalizedString{} }
func (f *checkUpdaterFake) Games() []core.GameDescriptor         { return []core.GameDescriptor{{ID: f.gid}} }
func (f *checkUpdaterFake) SettingsSchema() []core.SettingField  { return nil }
func (f *checkUpdaterFake) DetectInstall(_ context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: f.gid, InstallPath: ""}}, nil
}
func (f *checkUpdaterFake) GetIcon(_ context.Context, _ core.GameID) (string, error) { return "", nil }
func (f *checkUpdaterFake) GetBackgrounds(_ context.Context, _ core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *checkUpdaterFake) CheckVersion(_ context.Context, _ core.GameID) (core.VersionInfo, error) {
	return f.checkVersionResult, f.checkVersionErr
}
func (f *checkUpdaterFake) Launch(_ context.Context, _ core.GameID, _ core.LaunchOptions) (int, error) {
	return 0, nil
}
func (f *checkUpdaterFake) CheckForUpdate(_ context.Context, _ core.GameID) (core.UpdatePlan, error) {
	return f.checkForUpdatePlan, f.checkForUpdateErr
}
func (f *checkUpdaterFake) RunUpdate(_ context.Context, _ core.UpdatePlan, _ func(core.UpdateEvent)) error {
	return nil
}

// nonUpdaterFake satisfies core.Provider but NOT core.Updater (no CheckForUpdate
// or RunUpdate). Mirrors hoyoverse/hypergryph which do not implement Updater
// in M3.A.
type nonUpdaterFake struct {
	gid core.GameID
}

func (p *nonUpdaterFake) ID() core.BackendID                  { return "hoyoverse" }
func (p *nonUpdaterFake) DisplayName() core.LocalizedString    { return core.LocalizedString{} }
func (p *nonUpdaterFake) Games() []core.GameDescriptor         { return []core.GameDescriptor{{ID: p.gid}} }
func (p *nonUpdaterFake) SettingsSchema() []core.SettingField  { return nil }
func (p *nonUpdaterFake) DetectInstall(_ context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: p.gid, InstallPath: ""}}, nil
}
func (p *nonUpdaterFake) GetIcon(_ context.Context, _ core.GameID) (string, error) { return "", nil }
func (p *nonUpdaterFake) GetBackgrounds(_ context.Context, _ core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (p *nonUpdaterFake) CheckVersion(_ context.Context, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (p *nonUpdaterFake) Launch(_ context.Context, _ core.GameID, _ core.LaunchOptions) (int, error) {
	return 0, nil
}

func newAppWithProvider(p core.Provider) *App {
	a := &App{
		updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{}),
		detect:         map[core.BackendID]detectEntry{},
		logger:         slog.Default(),
	}
	a.providers = []core.Provider{p}
	return a
}

// TestCheckForUpdate_SetsAvailableWhenVersionDiffers — Provider.CheckVersion
// reports vi.Latest != vi.Current (i.e. server has a newer version than the
// local install). AvailableUpdate must be set so BottomBar's [更新 ↓] button
// (spec §3.1) appears.
//
// This is the spec §1.2.1 missing trigger path discovered during M3.A Task 18
// smoke: AvailableUpdate is "populated lazily on user click" but no RPC
// actually populated it pre-button-click. This RPC fills the gap with a
// lightweight probe (CheckVersion + fetchIndex) rather than the full
// CheckForUpdate (which does per-file MD5 over hundreds of GB-sized .pak
// files and would take 10-30s).
func TestCheckForUpdate_SetsAvailableWhenVersionDiffers(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	fake := &checkUpdaterFake{
		gid:                gid,
		checkVersionResult: core.VersionInfo{Current: "3.0.0", Latest: "3.3.0"},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()

	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}

	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.AvailableUpdate == nil {
		t.Fatal("AvailableUpdate is nil; want non-nil so [更新 ↓] button appears")
	}
	if state.AvailableUpdate.Version != "3.3.0" {
		t.Errorf("AvailableUpdate.Version = %q, want 3.3.0 (vi.Latest)", state.AvailableUpdate.Version)
	}
}

// TestCheckForUpdate_ClearsAvailableWhenVersionEquals — Provider.CheckVersion
// reports vi.Latest == vi.Current. Any stale AvailableUpdate must be cleared
// (e.g., user just finished an update via official launcher then hit Refresh
// in our launcher).
func TestCheckForUpdate_ClearsAvailableWhenVersionEquals(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	fake := &checkUpdaterFake{
		gid:                gid,
		checkVersionResult: core.VersionInfo{Current: "3.3.0", Latest: "3.3.0"},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()

	state := a.updateRegistry.Get(gid)
	state.AvailableUpdate = &core.UpdatePlan{Version: "stale"}

	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.AvailableUpdate != nil {
		t.Errorf("AvailableUpdate = %+v, want nil after version match", state.AvailableUpdate)
	}
}

// TestCheckForUpdate_SkipsWhenInFlight — must not probe (or mutate state)
// while an update is in flight. Refresh during update would race the worker
// goroutine and could clobber a fresh AvailableUpdate written by RunUpdate
// completion.
func TestCheckForUpdate_SkipsWhenInFlight(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	fake := &checkUpdaterFake{
		gid:                gid,
		checkVersionResult: core.VersionInfo{Current: "3.0.0", Latest: "3.3.0"},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()

	state := a.updateRegistry.Get(gid)
	state.InFlight = &InFlightOp{Plan: core.UpdatePlan{Version: "in-flight"}}

	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.AvailableUpdate != nil {
		t.Errorf("AvailableUpdate = %+v, want nil — must not write state during in-flight op", state.AvailableUpdate)
	}
}

// TestCheckForUpdate_NoOpForNonUpdater — providers that don't implement
// core.Updater (hoyoverse, hypergryph in M3.A) get a no-op (return nil, no
// state mutation). Frontend iterates over all installed games; non-Updater
// providers must not produce errors.
func TestCheckForUpdate_NoOpForNonUpdater(t *testing.T) {
	gid := core.GameID("hoyoverse/genshin")
	a := newAppWithProvider(&nonUpdaterFake{gid: gid})
	defer a.updateRegistry.emitter.Stop()

	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Errorf("CheckForUpdate(non-Updater) returned error: %v; want nil", err)
	}

	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.AvailableUpdate != nil {
		t.Errorf("AvailableUpdate = %+v on non-Updater provider; want nil", state.AvailableUpdate)
	}
}

// TestCheckForUpdate_SwallowsCheckVersionError — Refresh-time probes are
// best-effort. A CheckVersion error (e.g., DetectInstall transient failure)
// must not surface as LastError (that's reserved for user-initiated [更新]
// clicks). The real error will resurface when user clicks the button.
func TestCheckForUpdate_SwallowsCheckVersionError(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	fake := &checkUpdaterFake{
		gid:               gid,
		checkVersionErr:   errors.New("transient detect failure"),
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()

	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Errorf("CheckForUpdate returned error: %v; want nil (best-effort)", err)
	}

	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.LastError != nil {
		t.Errorf("LastError = %+v; Refresh probes must not surface errors", state.LastError)
	}
}
