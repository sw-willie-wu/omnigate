# Predownload Phase 1 (WuWa + shared infra) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire predownload (預先下載) end-to-end for WuWa (kurogames) and build the shared App/frontend infrastructure so the predl button appears and works for the game the user reported.

**Architecture:** Approach A — the App owns `available_predl` as the single actionable signal. The cheap Refresh probe sets it from `vi.Predownload`, gated by a new PER-GAME capability predicate `core.PredownloadChecker.SupportsPredownload(gid)`. Clicking the button routes through `CheckForPredownload` (kuro targets `idx.Predownload`), which reuses M3.A's download/stage/apply verbatim. See spec `docs/superpowers/specs/2026-06-07-omnigate-predownload-wiring-design.md` §1/§2.1/§9.

**Tech Stack:** Go 1.26 (`internal/core`, `internal/app`, `internal/providers/kurogames`), Vue 3 + Pinia + vitest (`frontend/src`). Tests: `go test ./...`, `cd frontend && npm test`.

**Scope:** Phase 1 only (WuWa). Genshin (Phase 2) and HSR/ZZZ legacy (Phase 3) are separate plans written after Phase 1 smokes — see spec §9. Endfield deferred (spec §8).

**Toolchain note for subagent shells without Go on PATH:**
`export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. This host has `CGO_ENABLED=0` — never pass `-race` to `go test`.

---

## File Structure

- `internal/core/updater.go` — add `PredownloadChecker` interface (Task 1).
- `internal/core/errors.go` — add `ErrPredownloadUnsupported` sentinel (Task 1).
- `internal/core/predownload_test.go` — interface/sentinel test (Task 1).
- `internal/providers/kurogames/update_manifest.go` — `indexJSONURL` var seam + `pickPredownloadIndexFile` (Task 2).
- `internal/providers/kurogames/kurogames.go` — `SupportsPredownload` + `CheckForPredownload` + interface-compliance var (Task 2).
- `internal/providers/kurogames/predownload_test.go` — end-to-end predl check test (Task 2).
- `internal/app/update_handler.go` — probe sets `AvailablePredl` (Task 3); predl routing in `runStartUpdateAsync` (Task 4).
- `internal/app/app.go` — generalize phantom self-heal `kurogames.BackendID` → `p.ID()` (Task 5).
- `internal/app/check_for_update_test.go` — extend `checkUpdaterFake` + probe/routing/self-heal tests (Tasks 3-5).
- `frontend/src/components/BottomBar.vue` — unify pill/button on `available_predl` (Task 6).
- `frontend/src/__tests__/predl_button_wuwa.test.ts` — predl button + pill tests (Task 6).

---

## Task 1: core.PredownloadChecker interface + ErrPredownloadUnsupported sentinel

**Files:**
- Modify: `internal/core/updater.go` (after the `CheckForUpdateProgress` interface, ~line 45)
- Modify: `internal/core/errors.go:8-13` (add to the sentinel `var` block)
- Test: `internal/core/predownload_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/core/predownload_test.go`:

```go
package core

import (
	"context"
	"errors"
	"testing"
)

type predlCheckerStub struct{}

func (predlCheckerStub) SupportsPredownload(GameID) bool { return true }
func (predlCheckerStub) CheckForPredownload(context.Context, GameID, func(int, int)) (UpdatePlan, error) {
	return UpdatePlan{}, ErrPredownloadUnsupported
}

func TestPredownloadChecker_InterfaceAndSentinel(t *testing.T) {
	var pc PredownloadChecker = predlCheckerStub{}
	if !pc.SupportsPredownload("kurogames/wutheringwaves") {
		t.Fatal("stub should support predownload")
	}
	_, err := pc.CheckForPredownload(context.Background(), "kurogames/wutheringwaves", nil)
	if !errors.Is(err, ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestPredownloadChecker_InterfaceAndSentinel`
Expected: FAIL — compile error (`PredownloadChecker` / `ErrPredownloadUnsupported` undefined).

- [ ] **Step 3: Add the sentinel**

In `internal/core/errors.go`, add inside the existing `var ( ... )` block (after `ErrGachaURLUnavailable`):

```go
	ErrPredownloadUnsupported = errors.New("predownload not supported for this game")
```

- [ ] **Step 4: Add the interface**

In `internal/core/updater.go`, after the `CheckForUpdateProgress` interface (after line 45):

```go
// PredownloadChecker is an optional interface a Provider may implement to
// support predownloading the next game version ahead of release. The App
// type-asserts it at the Refresh probe (to light the predl button) and at
// StartPredownload (to build the predl plan).
type PredownloadChecker interface {
	// SupportsPredownload is the cheap, PER-GAME capability predicate. One
	// provider type can serve several games whose predl support lands in
	// different phases (e.g. hoyoverse: Genshin then HSR/ZZZ), so capability
	// is gated per game, NOT by Go interface satisfaction.
	SupportsPredownload(gid GameID) bool
	// CheckForPredownload builds a predl plan targeting the predownload
	// manifest (Kind=PlanPredownload, Version=<predl target>). For any gid
	// where SupportsPredownload is false, or when no predl is currently
	// published, it returns ErrPredownloadUnsupported.
	CheckForPredownload(ctx context.Context, gid GameID, onProgress func(done, total int)) (UpdatePlan, error)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestPredownloadChecker_InterfaceAndSentinel`
Expected: PASS

- [ ] **Step 6: Commit** (deferred until the task's review gate returns APPROVE)

```bash
git add internal/core/updater.go internal/core/errors.go internal/core/predownload_test.go
git commit -m "feat(predl): add core.PredownloadChecker interface + ErrPredownloadUnsupported"
```

---

## Task 2: kurogames CheckForPredownload (targets idx.Predownload)

**Files:**
- Modify: `internal/providers/kurogames/update_manifest.go:44-46` (convert `indexJSONURL` func→var seam) + add `pickPredownloadIndexFile`
- Modify: `internal/providers/kurogames/kurogames.go` (add methods near `CheckForUpdateWithProgress`; add interface-compliance var at the block near line 409-416)
- Test: `internal/providers/kurogames/predownload_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/providers/kurogames/predownload_test.go`:

```go
package kurogames

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestCheckForPredownload_TargetsPredownloadManifest(t *testing.T) {
	const gid = core.GameID("kurogames/wutheringwaves")
	index := `{
	  "default":{"version":"3.3.0","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{"version":"3.3.0","indexFile":"default/indexFile.json","baseUrl":"default/zip/"}},
	  "predownload":{"version":"3.4.0","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{"version":"3.4.0","indexFile":"predl/indexFile.json","baseUrl":"predl/zip/"}},
	  "predownloadSwitch":1
	}`
	manifest := `{"resource":[{"dest":"new.pak","md5":"abc","size":1234}]}`

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"idx-etag"`)
		w.Write([]byte(strings.ReplaceAll(index, "PLACEHOLDER", srv.URL)))
	})
	mux.HandleFunc("/predl/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(manifest))
	})
	mux.HandleFunc("/default/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("predownload check must NOT fetch the default manifest")
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	orig := indexJSONURL
	indexJSONURL = func() string { return srv.URL + "/index.json" }
	defer func() { indexJSONURL = orig }()

	p := New(Settings{}, nil)
	p.httpClient = srv.Client()
	p.SetResolvedPaths(map[core.GameID]string{gid: t.TempDir()})

	plan, err := p.CheckForPredownload(context.Background(), gid, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if plan.Kind != core.PlanPredownload {
		t.Errorf("Kind = %v, want PlanPredownload", plan.Kind)
	}
	if plan.Version != "3.4.0" {
		t.Errorf("Version = %q, want 3.4.0 (predownload target)", plan.Version)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "new.pak" {
		t.Fatalf("Files = %+v, want [new.pak]", plan.Files)
	}
	if !strings.Contains(plan.Files[0].URL, "/predl/zip/new.pak") {
		t.Errorf("file URL = %q, want predl CDN path", plan.Files[0].URL)
	}
	if plan.ManifestETag != `"idx-etag"` {
		t.Errorf("ManifestETag = %q, want index.json etag", plan.ManifestETag)
	}
}

func TestCheckForPredownload_NoActivePredl(t *testing.T) {
	const gid = core.GameID("kurogames/wutheringwaves")
	index := `{"default":{"version":"3.3.0","cdnList":[{"url":"x/","P":0}],"config":{}},"predownloadSwitch":0}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()
	orig := indexJSONURL
	indexJSONURL = func() string { return srv.URL }
	defer func() { indexJSONURL = orig }()

	p := New(Settings{}, nil)
	p.httpClient = srv.Client()
	p.SetResolvedPaths(map[core.GameID]string{gid: t.TempDir()})

	_, err := p.CheckForPredownload(context.Background(), gid, nil)
	if !errors.Is(err, core.ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kurogames/ -run TestCheckForPredownload`
Expected: FAIL — `p.CheckForPredownload` undefined and `indexJSONURL` is a func (cannot assign).

- [ ] **Step 3: Convert `indexJSONURL` to a var seam + add `pickPredownloadIndexFile`**

In `internal/providers/kurogames/update_manifest.go`, replace the function (lines 42-46):

```go
// indexJSONURL is the entrypoint for kurogames update protocol — the catalog
// of CDNs + the per-version indexFile.json pointer. WuWa Global / live channel.
// A var (not func) so tests can point the manifest fetch at an httptest server.
var indexJSONURL = func() string {
	return "https://prod-alicdn-gamestarter.kurogame.com/launcher/game/G153/" + AppCred + "/index.json"
}
```

Then add, after `pickIndexFileForVersion` (after line 220):

```go
// pickPredownloadIndexFile returns the predl indexConfig matching the current
// install version (patch path), or the predl default config (full) if no
// patchConfig entry matches. Predl mirror of pickIndexFileForVersion; takes the
// predl config directly (idx.Predownload.Config).
func pickPredownloadIndexFile(predlCfg indexConfigRaw, currentVersion string) indexConfigRaw {
	if predlCfg.PatchType == "patch" && currentVersion != "" {
		for _, p := range predlCfg.PatchConfig {
			if p.Version == currentVersion {
				return p
			}
		}
	}
	return predlCfg
}
```

- [ ] **Step 4: Add `SupportsPredownload` + `CheckForPredownload` and the compliance var**

In `internal/providers/kurogames/kurogames.go`, add after `CheckForUpdateWithProgress` (after line 291):

```go
// SupportsPredownload implements core.PredownloadChecker. kurogames serves only
// WuWa; predl is supported whenever the game id is known. Whether an active
// predl is currently published is decided in CheckForPredownload.
func (p *Provider) SupportsPredownload(gid core.GameID) bool {
	return findByID(gid) != nil
}

// CheckForPredownload implements core.PredownloadChecker. Mirrors
// CheckForUpdateWithProgress but sources the manifest from idx.Predownload
// instead of idx.Default. Returns core.ErrPredownloadUnsupported when no active
// predownload is published. Download/stage/apply reuse the M3.A path: RunUpdate
// sees Kind=PlanPredownload and stops after RenameToPredlReady.
func (p *Provider) CheckForPredownload(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	installPath, err := p.gameDir(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	localVersion, _ := readLauncherDownloadConfigVersion(filepath.Join(installPath, "launcherDownloadConfig.json"))

	idx, idxETag, err := fetchIndex(ctx, p.httpClient, indexJSONURL())
	if err != nil {
		return core.UpdatePlan{}, err
	}
	if idx.Predownload == nil || idx.Predownload.Version == "" {
		return core.UpdatePlan{}, core.ErrPredownloadUnsupported
	}

	cfg := pickPredownloadIndexFile(idx.Predownload.Config, localVersion)
	cdn := pickCDN(idx.Predownload.CDNList)
	idxFile, _, err := fetchIndexFile(ctx, p.httpClient, cdn+cfg.IndexFile)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	files := filterChangedFiles(ctx, installPath, cdn, cfg.BaseURL, idxFile.Resource, p.logger, onProgress)
	if ctx.Err() != nil {
		return core.UpdatePlan{}, ctx.Err()
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}
	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanPredownload,
		ManifestETag: idxETag,
		Version:      idx.Predownload.Version,
		Files:        files,
		TotalBytes:   totalBytes,
		Reason:       core.ReasonPredownload,
	}
	p.logger.Info("CheckForPredownload complete", "game", gid, "predl_version", idx.Predownload.Version, "files", len(files), "bytes", totalBytes)
	return plan, nil
}
```

Then add to the compile-time interface block (lines 409-416), after the `core.CheckForUpdateProgress` line:

```go
	_ core.PredownloadChecker     = (*Provider)(nil) // predl: targets idx.Predownload
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/providers/kurogames/ -run TestCheckForPredownload`
Expected: PASS (both subtests). Then run the whole package to confirm no regression from the func→var change: `go test ./internal/providers/kurogames/`
Expected: PASS (existing manifest/update tests still green).

- [ ] **Step 6: Commit** (deferred until review APPROVE)

```bash
git add internal/providers/kurogames/update_manifest.go internal/providers/kurogames/kurogames.go internal/providers/kurogames/predownload_test.go
git commit -m "feat(predl): kurogames CheckForPredownload targets idx.Predownload"
```

---

## Task 3: App Refresh probe populates available_predl

**Files:**
- Modify: `internal/app/update_handler.go:534-544` (inside `CheckForUpdate`'s locked section)
- Test: `internal/app/check_for_update_test.go` (extend `checkUpdaterFake`; add 3 tests)

- [ ] **Step 1: Extend the fake + write failing tests**

In `internal/app/check_for_update_test.go`, add fields to `checkUpdaterFake` (struct at lines 14-20):

```go
	supportsPredl          bool
	checkForPredownloadPlan core.UpdatePlan
	checkForPredownloadErr error
	predlCalled            bool
	checkForUpdateCalled   bool
```

Add methods (after `RunUpdate`, line 44) so the fake satisfies `core.PredownloadChecker`:

```go
func (f *checkUpdaterFake) SupportsPredownload(_ core.GameID) bool { return f.supportsPredl }
func (f *checkUpdaterFake) CheckForPredownload(_ context.Context, _ core.GameID, _ func(int, int)) (core.UpdatePlan, error) {
	f.predlCalled = true
	return f.checkForPredownloadPlan, f.checkForPredownloadErr
}
```

Also mark `checkForUpdateCalled` in the existing `CheckForUpdate` method (lines 39-41):

```go
func (f *checkUpdaterFake) CheckForUpdate(_ context.Context, _ core.GameID) (core.UpdatePlan, error) {
	f.checkForUpdateCalled = true
	return f.checkForUpdatePlan, f.checkForUpdateErr
}
```

Add the tests:

```go
func TestCheckForUpdate_SetsAvailablePredlWhenUpToDateAndSupported(t *testing.T) {
	gid := core.GameID("kurogames/wutheringwaves")
	fake := &checkUpdaterFake{
		gid:           gid,
		supportsPredl: true,
		checkVersionResult: core.VersionInfo{
			Current: "3.3.0", Latest: "3.3.0",
			Predownload: &core.PredownloadInfo{TargetVersion: "3.4.0"},
		},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()
	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	st := a.updateRegistry.Get(gid)
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.AvailablePredl == nil || st.AvailablePredl.Version != "3.4.0" {
		t.Fatalf("AvailablePredl = %+v, want version 3.4.0", st.AvailablePredl)
	}
	if st.AvailableUpdate != nil {
		t.Errorf("AvailableUpdate must be nil when up-to-date")
	}
}

func TestCheckForUpdate_NoPredlWhenProviderUnsupportedForGame(t *testing.T) {
	gid := core.GameID("kurogames/wutheringwaves")
	fake := &checkUpdaterFake{
		gid:           gid,
		supportsPredl: false, // provider doesn't support predl for this game yet
		checkVersionResult: core.VersionInfo{
			Current: "3.3.0", Latest: "3.3.0",
			Predownload: &core.PredownloadInfo{TargetVersion: "3.4.0"},
		},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()
	_ = a.CheckForUpdate(string(gid))
	st := a.updateRegistry.Get(gid)
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.AvailablePredl != nil {
		t.Fatalf("AvailablePredl must be nil when SupportsPredownload(gid) is false")
	}
}

func TestCheckForUpdate_NoPredlWhenUpdatePending(t *testing.T) {
	gid := core.GameID("kurogames/wutheringwaves")
	fake := &checkUpdaterFake{
		gid:           gid,
		supportsPredl: true,
		checkVersionResult: core.VersionInfo{
			Current: "3.3.0", Latest: "3.4.0", // behind → update pending
			Predownload: &core.PredownloadInfo{TargetVersion: "3.5.0"},
		},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()
	_ = a.CheckForUpdate(string(gid))
	st := a.updateRegistry.Get(gid)
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.AvailablePredl != nil {
		t.Fatalf("predl and update are mutually exclusive; AvailablePredl must be nil when behind")
	}
	if st.AvailableUpdate == nil {
		t.Errorf("AvailableUpdate must be set when behind")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/ -run TestCheckForUpdate_SetsAvailablePredlWhenUpToDateAndSupported`
Expected: FAIL — `AvailablePredl` stays nil (probe doesn't set it yet). (The unsupported/pending tests already pass since AvailablePredl is never set; the Sets-test is the red one.)

- [ ] **Step 3: Implement the probe extension**

In `internal/app/update_handler.go`, inside `CheckForUpdate`, locate the locked block (lines 534-544):

```go
	state.mu.Lock()
	if vi.Latest != "" && vi.Latest != vi.Current {
		state.AvailableUpdate = &core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanUpdate,
			Version: vi.Latest,
		}
	} else {
		state.AvailableUpdate = nil
	}
	state.mu.Unlock()
```

Insert the predl block before `state.mu.Unlock()`:

```go
	// Predl availability (spec §1.1): set only when the provider supports predl
	// for THIS game (per-game capability — NOT a Go type assertion, because one
	// provider type can serve games whose predl lands in different phases), an
	// active predl is advertised, the game is up-to-date (predl/update mutually
	// exclusive), and nothing is already staged for that target.
	pc, predlCapable := p.(core.PredownloadChecker)
	if predlCapable && pc.SupportsPredownload(gid) &&
		vi.Predownload != nil && vi.Latest == vi.Current &&
		(state.PredlReady == nil || state.PredlReady.Version != vi.Predownload.TargetVersion) {
		state.AvailablePredl = &core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanPredownload,
			Version: vi.Predownload.TargetVersion,
		}
	} else {
		state.AvailablePredl = nil
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestCheckForUpdate_`
Expected: PASS (new 3 tests + existing `TestCheckForUpdate_*`).

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add internal/app/update_handler.go internal/app/check_for_update_test.go
git commit -m "feat(predl): App Refresh probe populates available_predl (per-game gate)"
```

---

## Task 4: Route StartPredownload through PredownloadChecker

**Files:**
- Modify: `internal/app/update_handler.go:115-126` (the CheckForUpdate dispatch in `runStartUpdateAsync`)
- Test: `internal/app/check_for_update_test.go` (2 tests; reuses extended `checkUpdaterFake` from Task 3)

- [ ] **Step 1: Write the failing tests**

Add to `internal/app/check_for_update_test.go`:

```go
func TestRunStartUpdateAsync_RoutesPredlThroughChecker(t *testing.T) {
	gid := core.GameID("kurogames/wutheringwaves")
	fake := &checkUpdaterFake{gid: gid, supportsPredl: true}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()
	// Call synchronously (not via the StartPredownload goroutine) to assert routing.
	a.runStartUpdateAsync(context.Background(), gid, core.PlanPredownload, fake, fake)
	if !fake.predlCalled {
		t.Fatal("expected CheckForPredownload to be called for PlanPredownload")
	}
	if fake.checkForUpdateCalled {
		t.Fatal("CheckForUpdate must NOT be called on the predl path")
	}
}

func TestRunStartUpdateAsync_PredlUnsupportedIdlesQuietly(t *testing.T) {
	gid := core.GameID("kurogames/wutheringwaves")
	fake := &checkUpdaterFake{gid: gid, supportsPredl: false}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()
	st := a.updateRegistry.Get(gid)
	st.mu.Lock()
	st.AvailablePredl = &core.UpdatePlan{Version: "x"}
	st.InFlight = &InFlightOp{Plan: core.UpdatePlan{GameID: gid, Kind: core.PlanPredownload}}
	st.mu.Unlock()
	a.runStartUpdateAsync(context.Background(), gid, core.PlanPredownload, fake, fake)
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.AvailablePredl != nil {
		t.Error("AvailablePredl should be cleared on unsupported predl")
	}
	if st.LastError != nil {
		t.Errorf("graceful idle must not set LastError, got %+v", st.LastError)
	}
	if st.InFlight != nil {
		t.Error("InFlight should be cleared")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/ -run TestRunStartUpdateAsync_`
Expected: FAIL — `RoutesPredlThroughChecker` reds because predl is not routed through `CheckForPredownload` yet (`predlCalled` false; `checkForUpdateCalled` true). (`PredlUnsupportedIdlesQuietly` may already pass pre-impl since the worker success path also clears those fields — that's fine; the combined run is red until Task 4's impl, and both go green after.)

- [ ] **Step 3: Implement the routing**

In `internal/app/update_handler.go`, replace the dispatch block (lines 115-126):

```go
	var plan core.UpdatePlan
	var err error
	if kind == core.PlanPredownload {
		pc, ok := p.(core.PredownloadChecker)
		if !ok || !pc.SupportsPredownload(gid) {
			// Capability gate (defense-in-depth): the button should never appear
			// for an unsupported game. Idle gracefully — clear AvailablePredl, no
			// LastError, never force Kind=PlanPredownload onto a non-predl plan.
			state.mu.Lock()
			state.InFlight = nil
			state.AvailablePredl = nil
			state.mu.Unlock()
			a.updateRegistry.EmitTerminal(gid)
			return
		}
		plan, err = pc.CheckForPredownload(ctx, gid, onVerifyProgress)
	} else if updProg, ok := upd.(core.CheckForUpdateProgress); ok {
		plan, err = updProg.CheckForUpdateWithProgress(ctx, gid, onVerifyProgress)
	} else {
		plan, err = upd.CheckForUpdate(ctx, gid)
	}
	if err != nil {
		if errors.Is(err, core.ErrPredownloadUnsupported) {
			// No active predl (race: pulled between probe and click). Idle quietly.
			state.mu.Lock()
			state.InFlight = nil
			state.AvailablePredl = nil
			state.mu.Unlock()
			a.updateRegistry.EmitTerminal(gid)
			return
		}
		abort(err)
		return
	}
	plan.Kind = kind
```

(`errors` is already imported in this file — line 5.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestRunStartUpdateAsync_`
Expected: PASS. Then `go test ./internal/app/` to confirm no regression.
Expected: PASS.

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add internal/app/update_handler.go internal/app/check_for_update_test.go
git commit -m "feat(predl): route StartPredownload through PredownloadChecker"
```

---

## Task 5: Generalize phantom-predl self-heal to the owning backend

**Files:**
- Modify: `internal/app/app.go:553` (`kurogames.BackendID` → `p.ID()`)
- Test: `internal/app/check_for_update_test.go` (1 test; make `checkUpdaterFake.ID()` configurable)

- [ ] **Step 1: Make the fake's backend id configurable + write the failing test**

In `internal/app/check_for_update_test.go`, add a field to `checkUpdaterFake`:

```go
	backendID core.BackendID
```

Change `ID()` (line 22) to honor it while defaulting to the existing value:

```go
func (f *checkUpdaterFake) ID() core.BackendID {
	if f.backendID != "" {
		return f.backendID
	}
	return "kurogames"
}
```

Add the test (imports `os`, `path/filepath`, `strings` — add to the file's import block):

```go
func TestRefreshVersion_PhantomPredlSelfHealUsesOwningBackend(t *testing.T) {
	gid := core.GameID("hoyoverse/genshin")
	fake := &checkUpdaterFake{
		gid:                gid,
		backendID:          "hoyoverse",
		checkVersionResult: core.VersionInfo{Current: "6.6.0", Latest: "6.6.0"},
	}
	a := newAppWithProvider(fake)
	defer a.updateRegistry.emitter.Stop()

	root := t.TempDir()
	a.settings.App.TempDir = root

	// Stage a phantom predl_ready for 6.6.0 (== installed version) under the
	// HOYOVERSE temp dir.
	st := a.updateRegistry.Get(gid)
	st.mu.Lock()
	st.PredlReady = &core.UpdatePlan{GameID: gid, Kind: core.PlanPredownload, Version: "6.6.0"}
	st.mu.Unlock()
	flat := strings.ReplaceAll(string(gid), "/", "-")
	versionDir := filepath.Join(root, "hoyoverse", flat, "6.6.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RefreshVersion(string(gid)); err != nil {
		t.Fatalf("RefreshVersion: %v", err)
	}

	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Errorf("hoyoverse predl staging dir should be removed by self-heal; stat err = %v", err)
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.PredlReady != nil {
		t.Error("PredlReady should be cleared by self-heal")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRefreshVersion_PhantomPredlSelfHealUsesOwningBackend`
Expected: FAIL — the hardcoded `kurogames.BackendID` resolves to `root` (flat), so `root/hoyoverse/<flat>/6.6.0` is never removed.

- [ ] **Step 3: Implement the fix**

In `internal/app/app.go`, in `RefreshVersion`'s self-heal block, change line 553:

```go
			tempDir := a.tempDirFor(p.ID(), gid)
```

(was `a.tempDirFor(kurogames.BackendID, gid)`. `p` is in scope from line 535. The `kurogames` import stays used elsewhere — app.go:110-111, 763 — so no import change.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestRefreshVersion_PhantomPredlSelfHealUsesOwningBackend`
Expected: PASS. Then `go test ./internal/app/` to confirm existing self-heal behavior for kurogames is unchanged.
Expected: PASS.

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add internal/app/app.go internal/app/check_for_update_test.go
git commit -m "fix(predl): generalize phantom-predl self-heal to the owning backend"
```

---

## Task 6: Frontend — unify pill + button on available_predl

**Files:**
- Modify: `frontend/src/components/BottomBar.vue:82-86` (`hasAnyPredl` computed + comment)
- Test: `frontend/src/__tests__/predl_button_wuwa.test.ts`

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/predl_button_wuwa.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import en from '../locales/en.json';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({
  Launch: vi.fn(), StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(async () => ({})), CheckForUpdate: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

function mountBar() {
  setActivePinia(createPinia());
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(BottomBar, { global: { plugins: [i18n] } });
}

function seedWuwa(snap: any) {
  const games = useGamesStore();
  games.games = [{
    id: 'kurogames/wutheringwaves', backend: 'kurogames',
    display_name: { en: 'Wuthering Waves' }, installed: true,
    has_predownload: true, current_version: '3.3.0', latest_version: '3.3.0',
  } as any];
  games.selectedID = 'kurogames/wutheringwaves';
  const updates = useUpdatesStore();
  updates.byGame['kurogames/wutheringwaves'] = snap;
}

describe('BottomBar predl button (WuWa)', () => {
  it('renders the predl button and info pill when available_predl is set', async () => {
    const wrapper = mountBar();
    seedWuwa({ available_update: null, available_predl: { version: '3.4.0', total_bytes: 1234 }, predl_ready: null, in_flight: null, last_error: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(true);
    expect(wrapper.find('.pill').classes()).toContain('info');
  });

  it('does NOT light predl from has_predownload alone (bypass removed)', async () => {
    const wrapper = mountBar();
    seedWuwa({ available_update: null, available_predl: null, predl_ready: null, in_flight: null, last_error: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(false);
    const pill = wrapper.find('.pill');
    expect(pill.classes()).toContain('ok');   // ready/green
    expect(pill.classes()).not.toContain('info');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/__tests__/predl_button_wuwa.test.ts`
Expected: FAIL — second test fails: with `has_predownload: true` the current `hasAnyPredl` returns true, so the pill has class `info` (not `ok`).

- [ ] **Step 3: Implement the change**

In `frontend/src/components/BottomBar.vue`, replace lines 82-86:

```ts
// Pill + predl button both derive from available_predl — the App's single
// actionable predl signal, populated only when the provider supports predl for
// this game (per-game capability gate). The legacy has_predownload bypass is
// dropped so the pill never lights for a game whose predl we can't start.
const hasAnyPredl = computed(() => availablePredl.value !== null);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/__tests__/predl_button_wuwa.test.ts`
Expected: PASS (both tests).

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add frontend/src/components/BottomBar.vue frontend/src/__tests__/predl_button_wuwa.test.ts
git commit -m "feat(predl): unify BottomBar pill + button on available_predl"
```

---

## Final verification (after all tasks committed)

- [ ] `go vet ./...` — clean
- [ ] `go test ./...` — all packages green (no `-race`; CGO disabled on this host)
- [ ] `cd frontend && npm test` — vitest green (existing + new predl tests)
- [ ] `cd frontend && npm run build` — clean
- [ ] `wails build` — produces `build/bin/omnigate.exe`
- [ ] **USER smoke (Phase 1):** on the real WuWa install with the currently-open predl window — Refresh → predl button appears next to the per-game gear → click → download progress → staged → 套用 → version writeback, no bounce. (Predl windows are live now per the user's report.)

---

## Self-review notes (author)

- **Spec coverage:** §1.1 probe → Task 3; §1.2 interface+sentinel → Task 1; §1.3 routing → Task 4; §1.5 unify frontend → Task 6; §2.1 kuro CheckForPredownload + pickPredownloadIndexFile param → Task 2; §4/B3 self-heal generalization → Task 5. kuro apply/stage = M3.A reuse (no task — verified generic in spec §0.2). Phase 1 of §9 fully covered.
- **Type consistency:** `SupportsPredownload(gid)` / `CheckForPredownload(ctx, gid, onProgress)` / `ErrPredownloadUnsupported` identical across Tasks 1-4. `AvailablePredl` field (`update_state.go:32`) set in Task 3, cleared in Task 4, read by frontend `available_predl` (existing).
- **Phasing safety:** Task 3's gate uses `SupportsPredownload(gid)`, so HSR/ZZZ/Genshin (no PredownloadChecker impl in Phase 1) stay dark. Task 6 drops the has_predownload bypass so no misleading pill remains for them either.
