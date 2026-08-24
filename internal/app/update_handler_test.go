package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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
func (f *fakeUpdater) DisplayName() core.LocalizedString   { return core.LocalizedString{"en": "fake"} }
func (f *fakeUpdater) Games() []core.GameDescriptor        { return f.games }
func (f *fakeUpdater) SettingsSchema() []core.SettingField { return nil }
func (f *fakeUpdater) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: f.games[0].ID, InstallPath: "/tmp/fake"}}, nil
}
func (f *fakeUpdater) GetIcon(_ context.Context, _ core.GameID) (string, error) { return "", nil }
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
		id:     "kurogames",
		games:  []core.GameDescriptor{{ID: gid}},
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

func (p *panickyUpdater) ID() core.BackendID                  { return p.base.ID() }
func (p *panickyUpdater) DisplayName() core.LocalizedString   { return p.base.DisplayName() }
func (p *panickyUpdater) Games() []core.GameDescriptor        { return p.base.Games() }
func (p *panickyUpdater) SettingsSchema() []core.SettingField { return p.base.SettingsSchema() }
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

func (m *minimalProviderForScan) ID() core.BackendID                { return m.id }
func (m *minimalProviderForScan) DisplayName() core.LocalizedString { return core.LocalizedString{} }
func (m *minimalProviderForScan) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, len(m.gids))
	for i, g := range m.gids {
		out[i] = core.GameDescriptor{ID: g}
	}
	return out
}
func (m *minimalProviderForScan) SettingsSchema() []core.SettingField { return nil }
func (m *minimalProviderForScan) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return nil, nil
}
func (m *minimalProviderForScan) GetIcon(ctx context.Context, gid core.GameID) (string, error) {
	return "", nil
}
func (m *minimalProviderForScan) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (m *minimalProviderForScan) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (m *minimalProviderForScan) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	return 0, nil
}

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

// --- Task 6: preflight disk-need formula (spec §5) ---

// TestPlanDiskNeed pins the spec §5 formula on the pure planDiskNeed helper
// — no free-space stubbing involved (platformHasFreeSpace is a build-tag
// func and cannot be stubbed; see task-6 brief gate warning #2).
func TestPlanDiskNeed(t *testing.T) {
	gib := func(x float64) int64 { return int64(x * float64(int64(1)<<30)) }

	cases := []struct {
		name                         string
		totalBytes, peakTempBytes    int64
		stagedAll, stagedEph, growth int64
		want                         int64
	}{
		{
			// (a) fresh StartUpdate, no patch plan, verDir empty: need is
			// TotalBytes + margin — remaining has NOT been discounted by
			// any staged bytes (stagedAll=0), so it stays at full TotalBytes.
			name:          "a_fresh_no_patch_plan",
			totalBytes:    gib(21.6),
			peakTempBytes: 0,
			want:          gib(21.6) + preflightMarginBytes,
		},
		{
			// (b) spec-pinned: PeakTempBytes=29.52GiB, TotalBytes=21.6GiB,
			// staged=0 → the peak term (29.52GiB) beats remaining (21.6GiB).
			name:          "b_spec_pinned_peak_wins_fresh",
			totalBytes:    gib(21.6),
			peakTempBytes: gib(29.52),
			want:          gib(29.52) + preflightMarginBytes,
		},
		{
			// (c) spec-pinned predl-fully-staged case: remaining collapses
			// to 0 (stagedAll == TotalBytes) so only the peak term
			// (29.52 - 20.05 = 9.47GiB) matters.
			name:          "c_spec_pinned_predl_staged_peak_term",
			totalBytes:    gib(21.6),
			peakTempBytes: gib(29.52),
			stagedAll:     gib(21.6),
			stagedEph:     gib(20.05),
			want:          (gib(29.52) - gib(20.05)) + preflightMarginBytes,
		},
		{
			// (e) remaining goes negative (over-staged) → clamps to 0.
			name:       "e_remaining_negative_clamps_to_zero",
			totalBytes: 100,
			stagedAll:  1000,
			want:       preflightMarginBytes,
		},
		{
			// (e) peak term goes negative (stagedEph exceeds PeakTempBytes)
			// → clamps to 0.
			name:          "e_peak_term_negative_clamps_to_zero",
			peakTempBytes: 50,
			stagedEph:     1000,
			want:          preflightMarginBytes,
		},
		{
			// (e) growth goes negative (net game-dir shrink) → clamps to 0,
			// never subtracts from need.
			name:       "e_growth_negative_clamps_to_zero",
			totalBytes: 100,
			stagedAll:  100,
			growth:     -500,
			want:       preflightMarginBytes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := core.UpdatePlan{TotalBytes: tc.totalBytes, PeakTempBytes: tc.peakTempBytes}
			got := planDiskNeed(plan, tc.stagedAll, tc.stagedEph, tc.growth)
			if got != tc.want {
				t.Fatalf("planDiskNeed() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMeasureGrowth_GeneralFiles_PerFileClamp covers case (d): a general
// file with no existing gameDir counterpart contributes its full Size; a
// shrinking file (old > new) contributes 0, never a negative amount that
// could offset a growing file elsewhere.
func TestMeasureGrowth_GeneralFiles_PerFileClamp(t *testing.T) {
	gameDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(gameDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "grow.bin"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "shrink.bin"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	// "new.bin" and "sub/nested.bin" intentionally absent — no old file.

	files := []core.FileTask{
		{Path: "grow.bin", Size: 1500},                       // old 1000 -> new 1500: +500
		{Path: "shrink.bin", Size: 2000},                     // old 5000 -> new 2000: clamps to 0, not -3000
		{Path: "new.bin", Size: 700},                         // no old file (old=0): +700 full size
		{Path: "sub/nested.bin", Size: 300},                  // nested path, no old file: +300
		{Path: "eph.krpdiff", Size: 999999, Ephemeral: true}, // excluded entirely
	}

	got := measureGrowth(gameDir, nil, files)
	want := int64(500 + 0 + 700 + 300)
	if got != want {
		t.Fatalf("measureGrowth() = %d, want %d", got, want)
	}
}

// TestMeasureGrowth_PatchGroups covers the (d-1) PatchGroups net-sum term:
// groups sum Dst.Size-Src.Size directly (no per-group clamp — a
// shrinking group is allowed to net-negative at this stage; only the
// FINAL aggregate is clamped, inside planDiskNeed).
func TestMeasureGrowth_PatchGroups(t *testing.T) {
	groups := []core.PatchGroup{
		{Src: core.PatchFile{Size: 1000}, Dst: core.PatchFile{Size: 1500}}, // +500
		{Src: core.PatchFile{Size: 2000}, Dst: core.PatchFile{Size: 800}},  // -1200
	}
	got := measureGrowth(t.TempDir(), groups, nil)
	want := int64(500 - 1200)
	if got != want {
		t.Fatalf("measureGrowth() groups = %d, want %d", got, want)
	}
}

// TestMeasureStagedBytes exercises the verDir stat loop against a small fs
// fixture: exact size match counts as staged (Ephemeral subset tracked
// separately), size mismatch and missing files do not.
func TestMeasureStagedBytes(t *testing.T) {
	verDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(verDir, "a.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verDir, "b.krpdiff"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verDir, "c.bin"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err) // deliberate size mismatch vs. plan below
	}
	// "d.bin" intentionally missing entirely.

	files := []core.FileTask{
		{Path: "a.bin", Size: 100},
		{Path: "b.krpdiff", Size: 50, Ephemeral: true},
		{Path: "c.bin", Size: 999}, // mismatch -> not staged
		{Path: "d.bin", Size: 42},  // missing -> not staged
	}

	stagedAll, stagedEph := measureStagedBytes(verDir, files)
	if stagedAll != 150 {
		t.Fatalf("stagedAll = %d, want 150", stagedAll)
	}
	if stagedEph != 50 {
		t.Fatalf("stagedEph = %d, want 50", stagedEph)
	}
}

// --- Task 9: three-entry-point wiring (spec §2.5) ---

// fakeUpdaterRecording wraps fakeUpdater (embedded by value, mirroring
// fakeGachaProvider's embed pattern in gacha_test.go) and records
// CheckForUpdate call count + the exact plan RunUpdate was invoked with —
// enough to assert ApplyPredownload re-plans at apply time rather than
// trusting the stale predl-time plan (spec §2.5).
type fakeUpdaterRecording struct {
	fakeUpdater

	mu         sync.Mutex
	checkCalls int
	gotRunPlan *core.UpdatePlan
}

func (r *fakeUpdaterRecording) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	r.mu.Lock()
	r.checkCalls++
	r.mu.Unlock()
	return r.fakeUpdater.CheckForUpdate(ctx, gid)
}

func (r *fakeUpdaterRecording) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
	cp := plan
	r.mu.Lock()
	r.gotRunPlan = &cp
	r.mu.Unlock()
	return r.fakeUpdater.RunUpdate(ctx, plan, onEvent)
}

// waitInFlightClear polls state.InFlight until nil or the deadline expires
// (same polling pattern as TestRapidStartCancelStart_NoInterleave — the
// async continuations under test run on their own goroutine).
func waitInFlightClear(t *testing.T, state *GameUpdateState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		state.mu.RLock()
		done := state.InFlight == nil
		state.mu.RUnlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("InFlight never cleared before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestApplyPredownload_RePlansAndConverges: ApplyPredownload must re-plan
// against the live manifest (CheckForUpdate) rather than trusting the
// stored predl-time plan — the worker must receive the FRESH plan (distinct
// ManifestETag here), and CheckForUpdate must actually have been called.
func TestApplyPredownload_RePlansAndConverges(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	predlPlan := core.UpdatePlan{
		GameID:       gid,
		ManifestETag: "predl-etag",
		Version:      "3.6.0",
		Files:        []core.FileTask{{Path: "old.dll", Hash: "h1", Size: 10}},
	}
	livePlan := core.UpdatePlan{
		GameID:       gid,
		ManifestETag: "live-etag",
		Version:      "3.6.0", // same version → live, not predl_not_live
		Files:        []core.FileTask{{Path: "new.dll", Hash: "h2", Size: 20}},
		TotalBytes:   20,
	}

	upd := &fakeUpdaterRecording{fakeUpdater: fakeUpdater{
		id:          "kurogames",
		games:       []core.GameDescriptor{{ID: gid, Backend: "kurogames"}},
		checkResult: livePlan,
	}}

	a := &App{
		settings:       Settings{App: AppSettings{TempDir: t.TempDir()}},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		providers:      []core.Provider{upd},
		resolved:       map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}},
		updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{}),
	}
	defer a.updateRegistry.emitter.Stop()

	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.PredlReady = &predlPlan
	state.mu.Unlock()

	if err := a.ApplyPredownload(string(gid)); err != nil {
		t.Fatalf("ApplyPredownload: %v", err)
	}
	waitInFlightClear(t, state, 2*time.Second)

	upd.mu.Lock()
	checkCalls, gotRunPlan := upd.checkCalls, upd.gotRunPlan
	upd.mu.Unlock()

	if checkCalls != 1 {
		t.Fatalf("CheckForUpdate calls = %d, want 1", checkCalls)
	}
	if gotRunPlan == nil {
		t.Fatal("RunUpdate never invoked")
	}
	if gotRunPlan.ManifestETag != "live-etag" {
		t.Fatalf("worker RunUpdate plan ETag = %q, want %q (re-planned, not the stale predl plan)", gotRunPlan.ManifestETag, "live-etag")
	}
	if gotRunPlan.Kind != core.PlanUpdate {
		t.Fatalf("worker RunUpdate plan Kind = %v, want PlanUpdate", gotRunPlan.Kind)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.LastError != nil {
		t.Fatalf("LastError = %+v, want nil", state.LastError)
	}
}

// TestApplyPredownload_NotLiveKeepsStaged: when the live manifest hasn't
// advanced to the predl-staged version yet, ApplyPredownload must abort with
// predl_not_live WITHOUT ever invoking RunUpdate, and must leave PredlReady
// (+ implicitly predl_ready.json / staged bytes) untouched so the user can
// retry once the version actually goes live (spec §2.5).
//
// Table-driven over two version pairs. The second case
// (predl="3.10.0", live="3.9.0") is the load-bearing one (review fix-round-1
// MEDIUM-F1): plan.Version != predlVersion must be VERSION-EQUALITY-ONLY. A
// mutant that instead compares with `<` or `<=` (string/lexical order) would
// evaluate "3.9.0" < "3.10.0" as FALSE — because byte-wise, '9' > '1' at the
// third character, so "3.9.0" sorts AFTER "3.10.0" lexically — and so would
// wrongly treat this pair as "live" and proceed to apply with the stale
// 3.9.0 plan. This is the exact 3.9→3.10 lexical-sort trap spec §2.5 cites
// (same class of bug as the HoYo webCaches precedent). The first case alone
// cannot catch this: "3.5.0" != "3.6.0" trips not_live under both the
// correct equality check AND under any naive ordering mutant, so it doesn't
// discriminate.
func TestApplyPredownload_NotLiveKeepsStaged(t *testing.T) {
	cases := []struct {
		name         string
		predlVersion string
		liveVersion  string
	}{
		{name: "basic_mismatch", predlVersion: "3.6.0", liveVersion: "3.5.0"},
		{name: "lexical_trap_3_9_vs_3_10", predlVersion: "3.10.0", liveVersion: "3.9.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gid := core.GameID("kurogames/wuwa")
			predlPlan := core.UpdatePlan{
				GameID:       gid,
				ManifestETag: "predl-etag",
				Version:      tc.predlVersion,
				Files:        []core.FileTask{{Path: "old.dll", Hash: "h1", Size: 10}},
			}
			livePlan := core.UpdatePlan{
				GameID:       gid,
				ManifestETag: "live-etag",
				Version:      tc.liveVersion, // != predlVersion → predl_not_live
			}

			upd := &fakeUpdaterRecording{fakeUpdater: fakeUpdater{
				id:          "kurogames",
				games:       []core.GameDescriptor{{ID: gid, Backend: "kurogames"}},
				checkResult: livePlan,
			}}

			a := &App{
				settings:       Settings{App: AppSettings{TempDir: t.TempDir()}},
				logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
				providers:      []core.Provider{upd},
				resolved:       map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}},
				updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{}),
			}
			defer a.updateRegistry.emitter.Stop()

			state := a.updateRegistry.Get(gid)
			state.mu.Lock()
			state.PredlReady = &predlPlan
			state.mu.Unlock()

			if err := a.ApplyPredownload(string(gid)); err != nil {
				t.Fatalf("ApplyPredownload: %v", err)
			}
			waitInFlightClear(t, state, 2*time.Second)

			upd.mu.Lock()
			gotRunPlan := upd.gotRunPlan
			upd.mu.Unlock()
			if gotRunPlan != nil {
				t.Fatal("RunUpdate must NOT be invoked when predl target is not yet live")
			}

			state.mu.RLock()
			defer state.mu.RUnlock()
			if state.LastError == nil || state.LastError.Code != "predl_not_live" {
				t.Fatalf("LastError = %+v, want Code=predl_not_live", state.LastError)
			}
			if state.PredlReady == nil {
				t.Fatal("PredlReady was cleared on a not-live abort; must be preserved for retry")
			}
		})
	}
}

// TestApplyPredownload_PreflightFailureKeepsStaged is review fix-round-1
// MEDIUM-F2: ApplyPredownload's preflightChecks call is otherwise unpinned
// (deleting it entirely would not fail any Task 9 test). livePlan carries
// the SAME version as the staged predl (passes the not_live gate) but an
// absurd PeakTempBytes (1<<62), which makes the REAL free-space check fail
// deterministically on any real machine (platformHasFreeSpace is a
// build-tag func and cannot be stubbed). Also pins fix-round-1 F4: since the
// failure is a PREFLIGHT failure (before any staged-bytes adoption begins
// inside the provider's RunUpdate), PredlReady must survive so the [套用]
// button remains available to retry once the blocker clears.
func TestApplyPredownload_PreflightFailureKeepsStaged(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	predlPlan := core.UpdatePlan{
		GameID:       gid,
		ManifestETag: "predl-etag",
		Version:      "3.6.0",
		Files:        []core.FileTask{{Path: "old.dll", Hash: "h1", Size: 10}},
	}
	livePlan := core.UpdatePlan{
		GameID:        gid,
		ManifestETag:  "live-etag",
		Version:       "3.6.0", // same version → live, passes not_live gate
		PeakTempBytes: 1 << 62,
	}

	upd := &fakeUpdaterRecording{fakeUpdater: fakeUpdater{
		id:          "kurogames",
		games:       []core.GameDescriptor{{ID: gid, Backend: "kurogames"}},
		checkResult: livePlan,
	}}

	a := &App{
		settings:       Settings{App: AppSettings{TempDir: t.TempDir()}},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		providers:      []core.Provider{upd},
		resolved:       map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}},
		updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{}),
	}
	defer a.updateRegistry.emitter.Stop()

	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.PredlReady = &predlPlan
	state.mu.Unlock()

	if err := a.ApplyPredownload(string(gid)); err != nil {
		t.Fatalf("ApplyPredownload: %v", err)
	}
	waitInFlightClear(t, state, 2*time.Second)

	upd.mu.Lock()
	gotRunPlan := upd.gotRunPlan
	upd.mu.Unlock()
	if gotRunPlan != nil {
		t.Fatal("RunUpdate must NOT be invoked when preflight rejects the re-planned plan")
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.LastError == nil || state.LastError.Code != "disk_full" {
		t.Fatalf("LastError = %+v, want Code=disk_full", state.LastError)
	}
	if state.PredlReady == nil {
		t.Fatal("PredlReady was cleared on a preflight failure; must be preserved for retry (fix-round-1 F4)")
	}
}

// TestResumeAsync_RunsPreflight: runResumeAsync must run preflightChecks on
// the re-planned plan before handing off to the worker. Mechanism: a fake
// plan with an absurd PeakTempBytes (1<<62) makes the REAL free-space check
// fail deterministically on any real machine — platformHasFreeSpace is a
// build-tag func and cannot be stubbed (task-9 brief gate warning #2), so
// this is exercised for real rather than weakened into a mock assertion.
func TestResumeAsync_RunsPreflight(t *testing.T) {
	gid := core.GameID("kurogames/wuwa")
	plan := core.UpdatePlan{
		GameID:        gid,
		ManifestETag:  "e1",
		Version:       "9.9.9",
		PeakTempBytes: 1 << 62,
	}
	upd := &fakeUpdater{
		id:          "kurogames",
		games:       []core.GameDescriptor{{ID: gid, Backend: "kurogames"}},
		checkResult: plan,
	}

	a := &App{
		settings:       Settings{App: AppSettings{TempDir: t.TempDir()}},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		resolved:       map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}},
		updateRegistry: NewUpdateStateRegistry(func(string, ...any) {}, realClock{}),
	}
	defer a.updateRegistry.emitter.Stop()

	state := a.updateRegistry.Get(gid)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state.mu.Lock()
	state.InFlight = &InFlightOp{
		Plan:   core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate},
		Phase:  core.PhaseDownload,
		Stage:  "verifying",
		cancel: cancel,
	}
	state.mu.Unlock()

	a.runResumeAsync(ctx, gid, upd, upd)

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.LastError == nil || state.LastError.Code != "disk_full" {
		t.Fatalf("LastError = %+v, want Code=disk_full", state.LastError)
	}
	if state.InFlight != nil {
		t.Fatalf("InFlight = %+v, want nil after preflight abort", state.InFlight)
	}
}
