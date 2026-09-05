package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse"
)

// StartUpdate kicks off the update flow for a game. Performs 1st-point
// game-running guard + statfs precheck + sets InFlight + spawns
// runUpdate goroutine.
func (a *App) StartUpdate(gameID string) error {
	gid := core.GameID(gameID)
	return a.startUpdateFlow(gid, core.PlanUpdate)
}

// StartPredownload kicks off predownload (download phase only).
func (a *App) StartPredownload(gameID string) error {
	gid := core.GameID(gameID)
	return a.startUpdateFlow(gid, core.PlanPredownload)
}

func (a *App) startUpdateFlow(gid core.GameID, kind core.PlanKind) error {
	// Find provider, type-assert Updater
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s does not support updates", p.ID())
	}

	// 1st game-running guard — resolve game-running status via core.ProcessChecker interface
	// (optional capability: not all providers implement it).
	if pc, ok := p.(core.ProcessChecker); ok {
		if running, _ := pc.IsGameRunning(gid); running {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil // error surfaces via snapshot LastError; RPC returns nil per spec §1.2.2
		}
	}

	// Preflight: fail fast (before any download) if the game dir is not writable
	// — e.g. installed under Program Files and we are not elevated.
	if ue := a.ensureGameDirWritable(gid, a.gameInstallDir(gid, p)); ue != nil {
		a.setLastError(gid, ue)
		return nil
	}

	state := a.updateRegistry.Get(gid)
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("update already in flight for %s", gid)
	}
	// Set "verifying" InFlight immediately so the BottomBar reflects the
	// click. Without this, the user sees no feedback until upd.CheckForUpdate
	// returns — and for large installs that means filterChangedFiles doing
	// sequential MD5 over hundreds of GB-class .pak files (minutes, not
	// seconds). Plan/TotalBytes start zero; runStartUpdateAsync rewrites
	// them once CheckForUpdate returns.
	state.InFlight = &InFlightOp{
		Plan:   core.UpdatePlan{GameID: gid, Kind: kind},
		Phase:  core.PhaseDownload,
		Stage:  "verifying",
		Total:  0,
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runStartUpdateAsync(ctx, gid, kind, p, upd)
	return nil
}

// runStartUpdateAsync is the off-thread continuation of startUpdateFlow.
// Performs the heavy CheckForUpdate (per-file MD5) + preflightChecks, then
// hands off to runUpdateWorker. On any error before runUpdateWorker takes
// over, it must clear InFlight and set LastError (which runUpdateWorker
// would otherwise do via its own deferred path).
func (a *App) runStartUpdateAsync(ctx context.Context, gid core.GameID, kind core.PlanKind, p core.Provider, upd core.Updater) {
	state := a.updateRegistry.Get(gid)

	abort := func(err error) {
		a.logger.Warn("runStartUpdateAsync abort", "game", gid, "err", err)
		state.mu.Lock()
		state.InFlight = nil
		if !errors.Is(err, context.Canceled) {
			state.LastError = asUpdateError(err)
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)
	}

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
		// CheckForPredownload isn't part of checkForUpdateVerifying's
		// CheckForUpdate-only scope, but it drives the same "驗證本地檔案
		// X / Y" BottomBar label during the predl probe, so it shares the
		// same verify-progress callback via verifyProgressFn.
		plan, err = pc.CheckForPredownload(ctx, gid, a.verifyProgressFn(gid))
	} else {
		plan, err = a.checkForUpdateVerifying(ctx, gid, upd)
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
	a.logger.Debug("runStartUpdateAsync: CheckForUpdate done", "game", gid, "files", len(plan.Files), "bytes", plan.TotalBytes, "version", plan.Version)

	tempDir := a.tempDirFor(p.ID(), gid)
	gameDir := a.gameInstallDir(gid, p)
	a.logger.Debug("runStartUpdateAsync: preflightChecks", "game", gid, "temp_dir", tempDir, "game_dir", gameDir)
	if err := a.preflightChecks(tempDir, gameDir, plan); err != nil {
		abort(err)
		return
	}
	a.logger.Debug("runStartUpdateAsync: preflightChecks done; entering runUpdateWorker", "game", gid)

	// Verify phase done — rewrite InFlight with the real plan and switch
	// out of the "verifying" stage so BottomBar can render "下載中 X%".
	state.mu.Lock()
	if state.InFlight == nil {
		state.mu.Unlock()
		return
	}
	state.InFlight.Plan = plan
	state.InFlight.Stage = ""
	state.InFlight.Current = 0
	state.InFlight.Total = plan.TotalBytes
	state.mu.Unlock()
	a.updateRegistry.EmitChanged(gid)

	a.runUpdateWorker(ctx, gid, upd, plan)
}

// verifyProgressFn returns a progress callback that writes (done, total)
// into gid's InFlightOp — but only while it is in the "verifying" stage, so
// a stale callback from a superseded probe can't clobber a later phase's
// Current/Total — throttled via the registry's emitter to avoid 195+
// events/sec saturating the bridge. Shared by checkForUpdateVerifying
// (CheckForUpdate side) and startUpdateFlow's predl probe
// (CheckForPredownload side): both are "驗證本地檔案 X / Y" style manifest
// probes that drive the same BottomBar label.
func (a *App) verifyProgressFn(gid core.GameID) func(done, total int) {
	state := a.updateRegistry.Get(gid)
	return func(done, total int) {
		state.mu.Lock()
		if state.InFlight != nil && state.InFlight.Stage == "verifying" {
			state.InFlight.Current = int64(done)
			state.InFlight.Total = int64(total)
		}
		state.mu.Unlock()
		a.updateRegistry.EmitChanged(gid)
	}
}

// checkForUpdateVerifying calls upd.CheckForUpdate — preferring the
// CheckForUpdateWithProgress variant when the provider implements it — and
// wires its per-file verify progress into gid's InFlightOp so BottomBar can
// render "驗證本地檔案 X / Y" while the potentially-minutes-long per-file MD5
// pass runs. Shared by runStartUpdateAsync's non-predl path, runResumeAsync,
// and runApplyPredlAsync — the three entry points that all re-plan against
// the SAME live-manifest builder at their respective start (spec §2.5's
// three-entry-point symmetry). Extracted so that symmetry can never drift
// via copy-paste divergence between the three call sites.
func (a *App) checkForUpdateVerifying(ctx context.Context, gid core.GameID, upd core.Updater) (core.UpdatePlan, error) {
	if updProg, ok := upd.(core.CheckForUpdateProgress); ok {
		return updProg.CheckForUpdateWithProgress(ctx, gid, a.verifyProgressFn(gid))
	}
	return upd.CheckForUpdate(ctx, gid)
}

func (a *App) runUpdateWorker(ctx context.Context, gid core.GameID, upd core.Updater, plan core.UpdatePlan) {
	state := a.updateRegistry.Get(gid)
	defer func() {
		// Outer panic recovery — RunUpdate also has its own; this is belt-and-suspenders
		if r := recover(); r != nil {
			state.mu.Lock()
			state.LastError = &core.UpdateError{
				Code:      "internal",
				Retryable: true,
				Params:    map[string]string{"detail": fmt.Sprint(r)},
			}
			state.InFlight = nil
			state.mu.Unlock()
			a.updateRegistry.EmitTerminal(gid)
		}
	}()

	onEvent := func(e core.UpdateEvent) {
		state.mu.Lock()
		if state.InFlight != nil {
			state.InFlight.Phase = e.Phase
			state.InFlight.Current = e.Current
			state.InFlight.Total = e.Total
			// Fine-grained stage (extract/patch/verify/apply) drives the UI label;
			// "" falls back to the Phase-based label (e.g. download progress).
			state.InFlight.Stage = e.Stage
		}
		state.mu.Unlock()
		a.updateRegistry.EmitChanged(gid)
	}

	err := upd.RunUpdate(ctx, plan, onEvent)

	// Terminal: clear InFlight, set LastError if non-cancel.
	// Use errors.Is (NOT ==) because cancel-induced panics get wrapped into
	// *core.UpdateError by RunUpdate's defer; equality check would miss those.
	state.mu.Lock()
	state.InFlight = nil
	if err != nil && !errors.Is(err, context.Canceled) {
		state.LastError = asUpdateError(err)
	} else if err == nil {
		// Success: clear AvailableUpdate or set PredlReady
		if plan.Kind == core.PlanUpdate {
			state.AvailableUpdate = nil
		} else if plan.Kind == core.PlanPredownload {
			state.PredlReady = &plan
			state.AvailablePredl = nil
		}
	}
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
}

// CancelInFlight cancels the active op for gameID.
func (a *App) CancelInFlight(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	if state.InFlight == nil || state.InFlight.Phase == core.PhaseApply {
		state.mu.RUnlock()
		return nil // no-op; UI shouldn't allow cancel during apply (spec §2.6)
	}
	cancelFn := state.InFlight.cancel
	stage := state.InFlight.Stage
	state.mu.RUnlock()
	a.logger.Info("CancelInFlight invoked", "game", gid, "stage", stage)
	cancelFn()
	return nil
}

// ApplyPredownload triggers the apply phase using a previously-completed
// predownload (spec §2.5). Same game-running guard as StartUpdate.
//
// The stored PredlReady plan is NOT applied directly — its content (Files,
// PatchGroups, ETag, ...) is display-only and may be stale (built before
// go-live, or rebuilt with missing fields after a restart — spec §2.5 notes
// this reconstruction is harmless BECAUSE apply always re-plans). Actual
// apply-time planning happens off-thread in runApplyPredlAsync, which
// re-runs the SAME live-manifest builder StartUpdate uses.
func (a *App) ApplyPredownload(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	predl := state.PredlReady
	state.mu.RUnlock()
	if predl == nil {
		return fmt.Errorf("no PredlReady for %s", gid)
	}

	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s no Updater", p.ID())
	}

	// 1st game-running guard — see StartUpdate flow for ProcessChecker rationale
	if pc, ok := p.(core.ProcessChecker); ok {
		if running, _ := pc.IsGameRunning(gid); running {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil
		}
	}

	// Preflight: fail fast if the game dir is not writable (Program Files w/o admin).
	if ue := a.ensureGameDirWritable(gid, a.gameInstallDir(gid, p)); ue != nil {
		a.setLastError(gid, ue)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("operation in flight for %s", gid)
	}
	// InFlight starts in "verifying" stage, same shape as startUpdateFlow: the
	// apply-time re-plan below is a minutes-level per-file MD5 pass and must
	// NOT run synchronously on this RPC handler goroutine (spec §2.5).
	state.InFlight = &InFlightOp{
		Plan:   *predl,
		Phase:  core.PhaseDownload,
		Stage:  "verifying",
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runApplyPredlAsync(ctx, gid, upd, p, predl.Version)
	return nil
}

// runApplyPredlAsync is the off-thread continuation of ApplyPredownload. It
// re-plans against the live manifest via checkForUpdateVerifying — the SAME
// builder StartUpdate uses (CheckForUpdate side), deliberately NOT
// CheckForPredownload: once a version has gone live, idx.Predownload has
// disappeared from the manifest and that path would return
// core.ErrPredownloadUnsupported (spec §2.5).
func (a *App) runApplyPredlAsync(ctx context.Context, gid core.GameID, upd core.Updater, p core.Provider, predlVersion string) {
	state := a.updateRegistry.Get(gid)

	abort := func(err error) {
		a.logger.Warn("runApplyPredlAsync abort", "game", gid, "err", err)
		state.mu.Lock()
		state.InFlight = nil
		if !errors.Is(err, context.Canceled) {
			state.LastError = asUpdateError(err)
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)
	}

	plan, err := a.checkForUpdateVerifying(ctx, gid, upd)
	if err != nil {
		abort(err)
		return
	}

	// predl_not_live: compared by VERSION EQUALITY ONLY — never size or
	// lexical ordering (spec §2.5 cites the 3.9→3.10 lexical-sort trap, same
	// class of bug as the HoYo webCaches precedent). This check MUST run
	// BEFORE any staged-bytes restoration/deletion — that happens inside the
	// provider's RunUpdate — so a not-live abort leaves PredlReady and
	// predl_ready.json untouched: the user can retry once the version
	// actually goes live.
	if plan.Version != predlVersion {
		abort(&core.UpdateError{
			Code:      "predl_not_live",
			Retryable: true,
			Params:    map[string]string{"predl_version": predlVersion, "live_version": plan.Version},
		})
		return
	}
	plan.Kind = core.PlanUpdate

	// Live: proceed to preflight BEFORE touching PredlReady. A preflight
	// failure here (e.g. disk_full) means nothing has been adopted or
	// consumed yet — keeping PredlReady intact leaves the [套用] button
	// available to retry once the blocker clears, instead of downgrading
	// straight to "resume only". This is strictly safer than clearing the
	// flag right after the not-live check while still satisfying spec §2.5's
	// "旗標已消失" wording, which describes failures AFTER staged-bytes
	// adoption has begun (inside the provider's RunUpdate) — preflight runs
	// before that, at the App layer.
	tempDir := a.tempDirFor(p.ID(), gid)
	gameDir := a.gameInstallDir(gid, p)
	if err := a.preflightChecks(tempDir, gameDir, plan); err != nil {
		abort(err)
		return
	}

	// Preflight passed: from this point on the predl plan is permanently
	// superseded by this re-plan, even if the apply itself fails below —
	// the PredlReady flag disappears; recovery from any failure past this
	// point is via ResumeInterrupted, not "predl ready" again. This is
	// documented, expected behavior, not a bug. Cleared in the SAME lock as
	// the InFlight swap below (rather than the InFlight==nil early-return
	// guard racing a separate PredlReady write).
	state.mu.Lock()
	if state.InFlight == nil {
		state.mu.Unlock()
		return
	}
	state.PredlReady = nil
	state.InFlight.Plan = plan
	state.InFlight.Stage = ""
	state.InFlight.Current = 0
	state.InFlight.Total = plan.TotalBytes
	state.mu.Unlock()
	a.updateRegistry.EmitChanged(gid)

	a.runUpdateWorker(ctx, gid, upd, plan)
}

// RemovePredownload deletes predl_ready.json + temp files for gameID's
// PredlReady version only. Capturing predlVersion BEFORE clearing the
// PredlReady pointer is required: deleting `<gameID-flat>/` (without the
// version segment) would nuke any concurrent in-flight progress sidecar
// for an unrelated version of the same game.
func (a *App) RemovePredownload(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	var predlVersion string
	if state.PredlReady != nil {
		predlVersion = state.PredlReady.Version
	}
	state.PredlReady = nil
	state.mu.Unlock()

	if predlVersion != "" {
		backendID, _, _ := core.ParseGameID(gid)
		tempDir := a.tempDirFor(backendID, gid)
		gameIDFlat := strings.ReplaceAll(string(gid), "/", "-")
		versionDir := filepath.Join(tempDir, gameIDFlat, predlVersion)
		// Best-effort cleanup; ignore errors
		_ = removeAll(versionDir)
	}
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

// DismissError clears state.LastError.
func (a *App) DismissError(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

// ResumeInterrupted re-enters an interrupted update pipeline using an
// existing sidecar (progress.json or apply.wal). Re-fetches the manifest
// to validate the ETag still matches; on drift, drops the sidecar and
// surfaces `manifest_changed` (spec §2.3 row "Only progress.json").
//
// Spec §2.3: download-resume re-fetches manifest header for ETag compare;
// apply-resume re-hashes only WAL-listed files. Both paths converge on
// runUpdateWorker, which delegates to upd.RunUpdate — its downloader
// resumes from progress.json by file mtime+size equality (spec §5.1)
// and its applier replays apply.wal entries marked `OK` (spec §5.3).
func (a *App) ResumeInterrupted(gameID string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s does not support updates", p.ID())
	}

	// 1st game-running guard — same as StartUpdate
	if pc, ok := p.(core.ProcessChecker); ok {
		if running, _ := pc.IsGameRunning(gid); running {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil
		}
	}

	// Preflight: fail fast if the game dir is not writable (Program Files w/o admin).
	if ue := a.ensureGameDirWritable(gid, a.gameInstallDir(gid, p)); ue != nil {
		a.setLastError(gid, ue)
		return nil
	}

	state := a.updateRegistry.Get(gid)
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("operation already in flight for %s", gid)
	}
	// Set "verifying" InFlight + clear LastError immediately so the bell
	// drops the notification (frontend's pending list filters by
	// last_error.code) and the user gets visible feedback while CheckForUpdate
	// re-fetches the manifest + re-MD5s local files (potentially minutes).
	// Same pattern as startUpdateFlow → runStartUpdateAsync.
	state.InFlight = &InFlightOp{
		Plan:   core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate},
		Phase:  core.PhaseDownload,
		Stage:  "verifying",
		Total:  0,
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runResumeAsync(ctx, gid, p, upd)
	return nil
}

// runResumeAsync is the off-thread continuation of ResumeInterrupted —
// same shape as runStartUpdateAsync. Re-fetches manifest, validates ETag,
// then dispatches to runUpdateWorker with the appropriate Kind / initial
// Phase based on the recovered sidecar.
func (a *App) runResumeAsync(ctx context.Context, gid core.GameID, p core.Provider, upd core.Updater) {
	state := a.updateRegistry.Get(gid)
	abort := func(err error) {
		a.logger.Warn("runResumeAsync abort", "game", gid, "err", err)
		state.mu.Lock()
		state.InFlight = nil
		if !errors.Is(err, context.Canceled) {
			state.LastError = asUpdateError(err)
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)
	}

	plan, err := a.checkForUpdateVerifying(ctx, gid, upd)
	if err != nil {
		abort(err)
		return
	}

	tempRoot := a.tempDirFor(p.ID(), gid)
	gameIDFlat := strings.ReplaceAll(string(gid), "/", "-")
	sidecarDir := filepath.Join(tempRoot, gameIDFlat, plan.Version)
	rec := core.ScanRecovery(sidecarDir)

	sidecarETag := readSidecarETag(sidecarDir)
	if sidecarETag != "" && sidecarETag != plan.ManifestETag {
		_ = removeAll(sidecarDir)
		abort(&core.UpdateError{
			Code:      "manifest_changed",
			Retryable: true,
			Params:    map[string]string{"old_etag": sidecarETag, "new_etag": plan.ManifestETag},
		})
		return
	}

	if rec.WasPredl {
		plan.Kind = core.PlanPredownload
	}

	// Preflight (spec §2.5 — new wiring for the resume entry point): the
	// re-planned plan reflects only what's still outstanding (completed
	// groups no longer counted), so PeakTempBytes/TotalBytes here are
	// already the shrunk values; preflightChecks additionally discounts
	// whatever is already staged on disk in the version temp dir.
	gameDir := a.gameInstallDir(gid, p)
	if err := a.preflightChecks(tempRoot, gameDir, plan); err != nil {
		abort(err)
		return
	}

	initialPhase := core.PhaseDownload
	if rec.Phase == core.RecoveryPhaseApplyResume {
		initialPhase = core.PhaseApply
	}
	// Verify done — rewrite the in-flight state with the real plan + phase
	// (preserving the cancel closure captured from ResumeInterrupted's ctx).
	state.mu.Lock()
	if state.InFlight == nil {
		state.mu.Unlock()
		return // cancelled between verify finishing and rewrite
	}
	state.InFlight.Plan = plan
	state.InFlight.Phase = initialPhase
	state.InFlight.Stage = ""
	state.InFlight.Current = 0
	state.InFlight.Total = plan.TotalBytes
	state.mu.Unlock()
	a.updateRegistry.EmitChanged(gid)

	a.runUpdateWorker(ctx, gid, upd, plan)
}

// readSidecarETag returns the ETag persisted in progress.json or
// predl_ready.json (same shape) inside dir. Returns "" if neither exists
// or both are unreadable. apply.wal also records ETag in its first line
// (per Task 9's WAL format) — read that as fallback.
func readSidecarETag(dir string) string {
	if pf, err := core.LoadProgress(dir); err == nil {
		return pf.ETag
	}
	// apply.wal has its own ETag; fall back to its parser
	if etag := core.ReadWALETag(filepath.Join(dir, "apply.wal")); etag != "" {
		return etag
	}
	// predl_ready.json — use loadProgressFile via core helper
	predlPath := filepath.Join(dir, "predl_ready.json")
	if pf, err := core.LoadProgressFromPath(predlPath); err == nil {
		return pf.ETag
	}
	return ""
}

// versionNewer reports whether a is a strictly newer version than b, comparing
// dot-separated numeric segments (missing segments count as 0, so "1.2" equals
// "1.2.0"; "2.100" beats "2.99" — numeric, never lexical). If either side has
// a non-numeric segment or is empty, it falls back to `a != b` (the historical
// behavior), so exotic version strings still surface an update rather than
// silently hiding one.
func versionNewer(a, b string) bool {
	as, aok := versionSegments(a)
	bs, bok := versionSegments(b)
	if !aok || !bok {
		return a != b
	}
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// updateAvailable reports whether the API's main version is genuinely newer
// than the local install (strictly-newer, not inequality — see versionNewer).
// The ONLY home of this predicate: CheckForUpdate and RefreshVersion both call
// it, so the sidebar chip and the [更新] button can never disagree on logic.
func updateAvailable(vi core.VersionInfo) bool {
	return vi.Latest != "" && versionNewer(vi.Latest, vi.Current)
}

func versionSegments(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// CheckForUpdate probes the manifest and populates state.AvailableUpdate when
// the server-reported version differs from the locally-installed version.
// Frontend calls this from Topbar.onRefresh for each installed game so the
// BottomBar [更新 ↓] button (spec §3.1) can appear without requiring a click
// on a button that doesn't exist yet.
//
// Spec §1.2.1 says "AvailableUpdate is populated lazily on user click and the
// result cached on the snapshot" — but no RPC was wired to do the populating
// before the click. This RPC closes that gap (discovered during M3.A Task 18
// smoke). Best-effort by design: errors are swallowed so a transient network
// blip on Refresh doesn't surface a toast; the real error path is via
// StartUpdate when the user clicks [更新].
func (a *App) CheckForUpdate(gameID string) error {
	gid := core.GameID(gameID)
	a.logger.Debug("CheckForUpdate RPC called", "game", gid)
	p, err := a.provider(gid)
	if err != nil {
		a.logger.Debug("CheckForUpdate skip: unknown game", "game", gid, "err", err)
		return nil
	}
	if _, ok := p.(core.Updater); !ok {
		a.logger.Debug("CheckForUpdate skip: provider not Updater", "game", gid, "provider", p.ID())
		return nil
	}

	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	inFlight := state.InFlight != nil
	state.mu.RUnlock()
	if inFlight {
		a.logger.Debug("CheckForUpdate skip: in-flight", "game", gid)
		return nil
	}

	// Lightweight probe: rely on p.CheckVersion (which now fetches index.json
	// for the real Latest) rather than upd.CheckForUpdate's full two-step
	// manifest + per-file MD5 computation (which can take 10-30s on disks
	// with hundreds of GB-sized .pak files). The full plan is fetched lazily
	// when user clicks [更新] (StartUpdate → upd.CheckForUpdate).
	probeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	vi, err := p.CheckVersion(probeCtx, gid)
	if err != nil {
		a.logger.Warn("CheckForUpdate probe failed", "game", gid, "err", err)
		return nil
	}
	a.logger.Debug("CheckForUpdate result", "game", gid, "latest", vi.Latest, "current", vi.Current)

	state.mu.Lock()
	// Strictly-newer comparison, not inequality: during a version rollover the
	// API's main version can lag a local install that already applied the
	// predownload (HSR 2026-09-05: local 4.5.0 vs API 4.4.0), and `!=` would
	// offer an "update" to the OLDER version.
	updateAvail := updateAvailable(vi)
	if updateAvail {
		state.AvailableUpdate = &core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanUpdate,
			Version: vi.Latest,
		}
	} else {
		state.AvailableUpdate = nil
	}
	// Predl availability (spec §1.1): set only when the provider supports predl
	// for THIS game (per-game capability — NOT a Go type assertion, because one
	// provider type can serve games whose predl lands in different phases), an
	// active predl is advertised, the game is up-to-date (predl/update mutually
	// exclusive), and nothing is already staged for that target.
	pc, predlCapable := p.(core.PredownloadChecker)
	if predlCapable && pc.SupportsPredownload(gid) &&
		vi.Predownload != nil && vi.Predownload.TargetVersion != "" && !updateAvail &&
		versionNewer(vi.Predownload.TargetVersion, vi.Current) &&
		(state.PredlReady == nil || state.PredlReady.Version != vi.Predownload.TargetVersion) {
		state.AvailablePredl = &core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanPredownload,
			Version: vi.Predownload.TargetVersion,
		}
	} else {
		state.AvailablePredl = nil
	}
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

// predlExposer is the optional capability interface for providers that expose
// predownload availability and last-apply-target data. Implemented by the
// hoyoverse Provider (M3.B).
type predlExposer interface {
	GetPredownloadAvailable(core.GameID) bool
	GetLastApplyTarget(core.GameID) *hoyoverse.LastApplyTarget
}

// UpdateStatusAll returns per-game state snapshots.
func (a *App) UpdateStatusAll() map[string]GameUpdateSnapshot {
	snaps := a.updateRegistry.SnapshotAll()

	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	// Overlay provider-sourced fields that are not tracked in GameUpdateState.
	for _, p := range provs {
		if pe, ok := p.(predlExposer); ok {
			for _, g := range p.Games() {
				gid := g.ID
				snap := snaps[string(gid)]
				snap.PredownloadAvailable = pe.GetPredownloadAvailable(gid)
				if lat := pe.GetLastApplyTarget(gid); lat != nil {
					snap.LastApplyTarget = &LastApplyTargetSnapshot{
						TargetVersion:     lat.TargetVersion,
						ConfigWritebackOK: lat.ConfigWritebackOK,
					}
				}
				snaps[string(gid)] = snap
			}
		}
	}

	return snaps
}

// --- helpers ---

func (a *App) setLastError(gid core.GameID, err *core.UpdateError) {
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.LastError = err
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
}

func (a *App) gameInstallDir(gid core.GameID, p core.Provider) string {
	_ = p // path now comes from a.resolved, not provider re-detection
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.resolved[gid].Path
}

// probeGameDirWritable creates and removes a probe file in dir to test write
// access. Package var so tests can substitute it (mirrors the osReadDir/osRemoveAll
// seams in the update_handler_{windows,other}.go files).
var probeGameDirWritable = func(dir string) error {
	probe := filepath.Join(dir, ".omnigate_wtest")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}

// ensureGameDirWritable returns *core.UpdateError{permission_denied} when the
// game directory cannot be written (e.g. Program Files without admin), else nil.
// Non-permission probe errors (missing dir, etc.) are ignored here — they surface
// through the normal CheckForUpdate/apply paths. gameDir "" (unresolved) → nil.
func (a *App) ensureGameDirWritable(gid core.GameID, gameDir string) *core.UpdateError {
	if gameDir == "" {
		return nil
	}
	if err := probeGameDirWritable(gameDir); err != nil {
		if core.IsPermissionError(err) {
			return &core.UpdateError{
				Code:      "permission_denied",
				Retryable: true,
				Params:    map[string]string{"game": string(gid)},
			}
		}
		a.logger.Debug("ensureGameDirWritable: non-permission probe error (ignored)", "game", gid, "dir", gameDir, "err", err)
	}
	return nil
}

// preflightMarginBytes is a fixed safety margin added on top of the computed
// disk need, absorbing small estimation error (filesystem cluster overhead,
// concurrent writes elsewhere, etc). NOT big enough to absorb GiB-level
// patch-phase growth — that must be computed explicitly (see planDiskNeed).
const preflightMarginBytes = 256 << 20

// preflightChecks is the sole disk/filesystem preflight entry point (spec
// §2.5 plan gate B2: all three call sites — runStartUpdateAsync here, plus
// ApplyPredownload/runResumeAsync — must go through this one func with no
// external scalar parameters, so patch-phase accounting can never drift
// between entries).
//
// Timing note (spec §5): this runs at the App layer, BEFORE the provider's
// RunUpdate has a chance to call ConsumePredlStaged — progress.json has not
// been restored yet, so "already staged" is determined here by directly
// stat-ing the version temp dir. A size match is treated as staged for
// budgeting purposes; a drifted file (same size, different content) is
// over-counted as staged (under-counting remaining need), but that's safe
// because the provider's own hash verification is what actually gates
// correctness — this check is a budget estimate, not a correctness check.
func (a *App) preflightChecks(tempDir, gameDir string, plan core.UpdatePlan) error {
	// Spec §1.3: TempDir MUST be on NTFS (or any FS with sub-second mtime
	// resolution). FAT32/exFAT have 2s resolution which breaks the resume
	// exact-equality check (spec §5.1). os.MkdirAll the dir first so
	// GetVolumeInformation has a target.
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return &core.UpdateError{
			Code:      "internal",
			Retryable: false,
			Params:    map[string]string{"detail": "create temp dir: " + err.Error()},
		}
	}
	if fsName, ok := platformFilesystemName(tempDir); ok {
		if !isSupportedFilesystem(fsName) {
			return &core.UpdateError{
				Code:      "unsupported_filesystem",
				Retryable: false,
				Params:    map[string]string{"temp_dir": tempDir, "fs": fsName},
			}
		}
	}

	tempVol := filepath.VolumeName(tempDir)
	gameVol := filepath.VolumeName(gameDir)
	if tempVol != gameVol && tempVol != "" && gameVol != "" {
		return &core.UpdateError{
			Code:      "cross_volume_temp",
			Retryable: false,
			Params:    map[string]string{"temp_vol": tempVol, "game_vol": gameVol},
		}
	}

	verDir := filepath.Join(tempDir, strings.ReplaceAll(string(plan.GameID), "/", "-"), plan.Version)
	stagedAll, stagedEph := measureStagedBytes(verDir, plan.Files)
	growth := measureGrowth(gameDir, plan.PatchGroups, plan.Files)
	need := planDiskNeed(plan, stagedAll, stagedEph, growth)

	// Disk space precheck — implementation uses windows.GetDiskFreeSpaceEx
	// or syscall equivalent. Stub for non-Windows tests.
	if !platformHasFreeSpace(tempDir, need) {
		return &core.UpdateError{
			Code:      "disk_full",
			Retryable: false,
			Params:    map[string]string{"need": fmt.Sprint(need), "have": "<computed>"},
		}
	}
	return nil
}

// measureStagedBytes stats each plan file inside verDir (the version temp
// dir) and reports total bytes already staged there (size-match heuristic —
// see preflightChecks doc comment for the drift caveat) plus the Ephemeral
// (patch-diff) subset of that total.
func measureStagedBytes(verDir string, files []core.FileTask) (stagedAll, stagedEph int64) {
	for _, f := range files {
		if fi, err := os.Stat(filepath.Join(verDir, filepath.FromSlash(f.Path))); err == nil && fi.Size() == f.Size {
			stagedAll += f.Size
			if f.Ephemeral {
				stagedEph += f.Size
			}
		}
	}
	return stagedAll, stagedEph
}

// measureGrowth computes the net game-dir byte growth the apply phase will
// cause (spec §5): PatchGroups' dst-src net sum, plus, per general
// (non-Ephemeral) file, its size delta over any existing gameDir file —
// clamped to >=0 PER FILE so a shrinking file can never offset a growing
// one. The aggregate result may still be negative (a patch-heavy plan can
// net-shrink the game dir); planDiskNeed clamps the aggregate to 0.
func measureGrowth(gameDir string, groups []core.PatchGroup, files []core.FileTask) int64 {
	var growth int64
	for _, g := range groups {
		growth += g.Dst.Size - g.Src.Size
	}
	for _, f := range files {
		if f.Ephemeral {
			continue
		}
		var old int64
		if fi, err := os.Stat(filepath.Join(gameDir, filepath.FromSlash(f.Path))); err == nil {
			old = fi.Size()
		}
		if d := f.Size - old; d > 0 {
			growth += d
		}
	}
	return growth
}

// planDiskNeed computes the disk headroom preflight must confirm is
// available, per spec §5's pinned formula:
//
//	need = max(TotalBytes - stagedAll, PeakTempBytes - stagedEph) + growth + margin
//
// (each of the two max operands, and growth, clamped to >=0 individually).
// stagedAll/stagedEph/growth are pre-measured by the caller (measureStagedBytes/
// measureGrowth) so this stays pure and unit-testable without stubbing the
// build-tag platformHasFreeSpace.
func planDiskNeed(plan core.UpdatePlan, stagedAll, stagedEph, growth int64) int64 {
	remaining := plan.TotalBytes - stagedAll
	if remaining < 0 {
		remaining = 0
	}
	peakTerm := plan.PeakTempBytes - stagedEph
	if peakTerm < 0 {
		peakTerm = 0
	}
	need := remaining
	if peakTerm > need {
		need = peakTerm
	}
	if growth < 0 {
		growth = 0
	}
	return need + growth + preflightMarginBytes
}

// isSupportedFilesystem returns true for filesystems with sub-second mtime
// resolution (NTFS, ReFS). FAT32/exFAT have 2s resolution which breaks
// progress.json's mtime+size exact-equality resume check (spec §5.1, §1.3).
func isSupportedFilesystem(name string) bool {
	switch strings.ToUpper(name) {
	case "NTFS", "REFS":
		return true
	default:
		return false
	}
}

func asUpdateError(err error) *core.UpdateError {
	if ue, ok := err.(*core.UpdateError); ok {
		return ue
	}
	return &core.UpdateError{
		Code:      "internal",
		Retryable: true,
		Params:    map[string]string{"detail": err.Error()},
	}
}

func removeAll(path string) error {
	return osRemoveAll(path) // wraps os.RemoveAll for testability
}

// knownBackendIDs returns a set of registered backend IDs as plain strings.
// Used by scanForRecovery to skip <TEMP>/omnigate/<otherBackend>/ subdirs
// during the kurogames flat-root walk: kurogames root <TEMP>/omnigate/
// happens to be a parent of hoyoverse's <TEMP>/omnigate/hoyoverse/ subdir,
// so a naive walker would treat "hoyoverse" as a candidate game directory.
// Cross-backend gid collision is structurally impossible by ParseGameID
// strengthening (Task 3); this filter eliminates noise.
func (a *App) knownBackendIDs() map[string]struct{} {
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	out := make(map[string]struct{}, len(provs))
	for _, p := range provs {
		out[string(p.ID())] = struct{}{}
	}
	return out
}

// scanForRecovery walks every registered backend's per-backend temp root,
// scanning each <root>/<gameIDFlat>/<version>/ for sidecars and seeding
// per-game state (interrupted_resume / predl_ready) via applyRecoveryState.
//
// Tree shape per backend: <tempDirFor(backend, "")>/<gameIDFlat>/<version>/
// where <gameIDFlat> = strings.Replace(string(gid), "/", "-", 1).
//
// kurogames flat root <TEMP>/omnigate/ may contain sibling backend
// subdirs (e.g. <TEMP>/omnigate/hoyoverse/); knownBackendIDs filter
// skips them to suppress noise.
func (a *App) scanForRecovery() {
	skipNames := a.knownBackendIDs()
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	for _, p := range provs {
		root := a.tempDirFor(p.ID(), "")
		a.scanForRecoveryRoot(p.ID(), root, skipNames)
	}
}

func (a *App) scanForRecoveryRoot(backend core.BackendID, root string, skipNames map[string]struct{}) {
	a.logger.Debug("scanForRecovery: enter", "backend", backend, "root", root)
	gameDirs, err := osReadDir(root)
	if err != nil {
		a.logger.Debug("scanForRecovery: no temp dir (first-run normal)", "backend", backend, "err", err)
		return
	}
	for _, gameDir := range gameDirs {
		if !gameDir.IsDir() {
			continue
		}
		gameIDFlat := gameDir.Name()
		// Skip sibling backends' subdirs (kurogames flat root case only).
		if _, isBackendName := skipNames[gameIDFlat]; isBackendName {
			continue
		}
		gid := core.GameID(strings.Replace(gameIDFlat, "-", "/", 1))
		p, err := a.provider(gid)
		if err != nil {
			a.logger.Debug("scanForRecovery: skip unknown game dir", "backend", backend, "dir", gameIDFlat, "err", err)
			continue
		}
		// Defense-in-depth: the resolved provider must own this root.
		if p.ID() != backend {
			a.logger.Debug("scanForRecovery: cross-backend dir; skipping", "backend", backend, "gid", gid, "owner", p.ID())
			continue
		}
		gameDirPath := filepath.Join(root, gameIDFlat)
		versionDirs, err := osReadDir(gameDirPath)
		if err != nil {
			continue
		}
		for _, vDir := range versionDirs {
			if !vDir.IsDir() {
				continue
			}
			// Skip cross-version sidecar dirs (e.g. ".sophon/"); they are not
			// version dirs and must not be passed to ScanRecovery (spec §1).
			if strings.HasPrefix(vDir.Name(), ".") {
				continue
			}
			sidecarDir := filepath.Join(gameDirPath, vDir.Name())
			a.logger.Debug("scanForRecovery: scanning sidecar dir", "backend", backend, "game", gid, "dir", sidecarDir)
			a.applyRecoveryState(gid, sidecarDir)
		}
	}
}

// applyRecoveryStateOverride is a test seam used by scanForRecovery tests
// to assert which sidecar dirs the walker visits without exercising the
// full RecoveryPhase routing logic. Production code never sets this.
var applyRecoveryStateOverride func(gid core.GameID, sidecarDir string)

func (a *App) applyRecoveryState(gid core.GameID, sidecarDir string) {
	if applyRecoveryStateOverride != nil {
		applyRecoveryStateOverride(gid, sidecarDir)
		return
	}
	rec := core.ScanRecovery(sidecarDir)
	a.logger.Debug("applyRecoveryState: ScanRecovery result", "game", gid, "dir", sidecarDir, "phase", rec.Phase, "wasPredl", rec.WasPredl)
	state := a.updateRegistry.Get(gid)
	switch rec.Phase {
	case core.RecoveryPhaseDownloadResume:
		state.mu.Lock()
		state.LastError = &core.UpdateError{
			Code:      "interrupted_resume",
			Retryable: true,
			Params: map[string]string{
				"phase":    "download",
				"wasPredl": fmt.Sprintf("%t", rec.WasPredl),
			},
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)

	case core.RecoveryPhaseApplyResume:
		state.mu.Lock()
		state.LastError = &core.UpdateError{
			Code:      "interrupted_resume",
			Retryable: true,
			Params: map[string]string{
				"phase":    "apply",
				"wasPredl": fmt.Sprintf("%t", rec.WasPredl),
			},
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)

	case core.RecoveryPhasePredlAwaiting:
		// Spec §2.3 row "Only predl_ready.json": parse → set state.PredlReady, no prompt.
		predlPath := filepath.Join(sidecarDir, "predl_ready.json")
		pf, err := core.LoadProgressFromPath(predlPath)
		if err != nil {
			return // ScanRecovery already deletes corrupt predl_ready.json
		}
		// Reconstruct minimal UpdatePlan from ProgressFile. Apply phase only
		// needs Path (URL/Hash already used during predownload's verify step).
		files := make([]core.FileTask, 0, len(pf.Entries))
		var totalBytes int64
		for relPath, entry := range pf.Entries {
			files = append(files, core.FileTask{
				Path: relPath,
				Hash: entry.Hash,
				Size: entry.Size,
			})
			totalBytes += entry.Size
		}
		state.mu.Lock()
		state.PredlReady = &core.UpdatePlan{
			GameID:       gid,
			Kind:         core.PlanPredownload,
			ManifestETag: pf.ETag,
			Version:      pf.Version,
			Files:        files,
			TotalBytes:   totalBytes,
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)

	case core.RecoveryCorrupt:
		// Spec §2.3 row "Corrupt apply.wal": LastError = unrecoverable.
		state.mu.Lock()
		state.LastError = &core.UpdateError{
			Code:      "unrecoverable",
			Retryable: false,
			Params:    map[string]string{"reason": "corrupt apply.wal sidecar"},
		}
		state.mu.Unlock()
		a.updateRegistry.EmitTerminal(gid)

	case core.RecoveryNone:
		// nothing to do
	}
}
