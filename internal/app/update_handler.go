package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/kurogames"
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
		return fmt.Errorf("provider %s does not support updates (M3.A: only kurogames)", p.ID())
	}

	// 1st game-running guard — resolve exe name via core.ExeNamer interface
	// (NOT core.GameDescriptor.ExeName — GameDescriptor has no such field;
	// per-provider exe metadata lives in `gameMeta` and is exposed via
	// the ExeNamer optional interface, same pattern as asset_handler.go:101).
	if exeName, ok := gameExeName(p, gid); ok {
		if kurogames.IsProcessRunning(exeName) {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil // error surfaces via snapshot LastError; RPC returns nil per spec §1.2.2
		}
	}

	// CheckForUpdate / re-use existing AvailablePredl
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		return fmt.Errorf("update already in flight for %s", gid)
	}
	state.mu.Unlock()

	// CheckForUpdate (kind-specific entry; for simplicity, reuse same path
	// and override Plan.Kind on return)
	plan, err := upd.CheckForUpdate(context.Background(), gid)
	if err != nil {
		a.setLastError(gid, asUpdateError(err))
		return nil
	}
	plan.Kind = kind

	// Cross-volume + space precheck (spec §5.2)
	tempDir := a.kurogamesTempDir(gid)
	gameDir := a.gameInstallDir(gid, p)
	if err := a.preflightChecks(tempDir, gameDir, plan.TotalBytes); err != nil {
		a.setLastError(gid, asUpdateError(err))
		return nil
	}

	// Set InFlight under mu
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.InFlight = &InFlightOp{
		Plan:    plan,
		Phase:   core.PhaseDownload,
		Total:   plan.TotalBytes,
		cancel:  cancel,
	}
	state.LastError = nil
	state.mu.Unlock()

	a.updateRegistry.EmitTerminal(gid)

	// Launch worker goroutine
	go a.runUpdateWorker(ctx, gid, upd, plan)
	return nil
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
	state.mu.RUnlock()
	cancelFn()
	return nil
}

// ApplyPredownload triggers the apply phase using a previously-completed
// predownload (spec §2.5). Same game-running guard as StartUpdate.
func (a *App) ApplyPredownload(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	predl := state.PredlReady
	state.mu.RUnlock()
	if predl == nil {
		return fmt.Errorf("no PredlReady for %s", gid)
	}
	// PredlReady plan with Kind=Update + ETag preserved → drives apply-only path.
	// runUpdateWorker's logic on PlanUpdate handles apply normally; download
	// phase will skip all entries (already present in temp).
	predlCopy := *predl
	predlCopy.Kind = core.PlanUpdate

	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s no Updater", p.ID())
	}

	// 1st game-running guard — see StartUpdate flow for ExeNamer rationale
	if exeName, ok := gameExeName(p, gid); ok {
		if kurogames.IsProcessRunning(exeName) {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil
		}
	}

	// Set InFlight for ApplyPredownload (Phase: Apply at start since download done)
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("operation in flight for %s", gid)
	}
	state.InFlight = &InFlightOp{
		Plan:   predlCopy,
		Phase:  core.PhaseApply,
		Total:  int64(len(predlCopy.Files)),
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runUpdateWorker(ctx, gid, upd, predlCopy)
	return nil
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
		tempDir := a.kurogamesTempDir(gid)
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

	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	if state.InFlight != nil {
		state.mu.RUnlock()
		return fmt.Errorf("operation already in flight for %s", gid)
	}
	state.mu.RUnlock()

	// 1st game-running guard — same as StartUpdate
	if exeName, ok := gameExeName(p, gid); ok {
		if kurogames.IsProcessRunning(exeName) {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil
		}
	}

	// Re-fetch manifest so plan reflects current server state; downloader's
	// resume logic uses progress.json's recorded mtime+size to skip files
	// already on disk in temp.
	plan, err := upd.CheckForUpdate(context.Background(), gid)
	if err != nil {
		a.setLastError(gid, asUpdateError(err))
		return nil
	}

	// Discover sidecar dir for this gid+version, examine recovery state.
	tempRoot := a.kurogamesTempDir(gid)
	gameIDFlat := strings.ReplaceAll(string(gid), "/", "-")
	sidecarDir := filepath.Join(tempRoot, gameIDFlat, plan.Version)
	rec := kurogames.ScanRecovery(sidecarDir)

	// ETag drift check (spec §2.3): read sidecar's recorded ETag and compare
	// to fresh manifest's ETag. Mismatch → drop sidecar + surface manifest_changed.
	sidecarETag := readSidecarETag(sidecarDir)
	if sidecarETag != "" && sidecarETag != plan.ManifestETag {
		_ = removeAll(sidecarDir)
		a.setLastError(gid, &core.UpdateError{
			Code:      "manifest_changed",
			Retryable: true,
			Params:    map[string]string{"old_etag": sidecarETag, "new_etag": plan.ManifestETag},
		})
		return nil
	}

	// Kind dispatch: predl-resume preserves PlanPredownload semantics
	// (RunUpdate skips apply phase + renames to predl_ready.json on completion).
	if rec.WasPredl {
		plan.Kind = core.PlanPredownload
	}

	// Set InFlight; initial Phase reflects sidecar (RecoveryPhaseApplyResume → PhaseApply,
	// else PhaseDownload).
	ctx, cancel := context.WithCancel(context.Background())
	initialPhase := core.PhaseDownload
	if rec.Phase == kurogames.RecoveryPhaseApplyResume {
		initialPhase = core.PhaseApply
	}
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("operation already in flight for %s", gid)
	}
	state.InFlight = &InFlightOp{
		Plan:   plan,
		Phase:  initialPhase,
		Total:  plan.TotalBytes,
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runUpdateWorker(ctx, gid, upd, plan)
	return nil
}

// readSidecarETag returns the ETag persisted in progress.json or
// predl_ready.json (same shape) inside dir. Returns "" if neither exists
// or both are unreadable. apply.wal also records ETag in its first line
// (per Task 9's WAL format) — read that as fallback.
func readSidecarETag(dir string) string {
	if pf, err := kurogames.LoadProgress(dir); err == nil {
		return pf.ETag
	}
	// apply.wal has its own ETag; fall back to its parser
	if etag := kurogames.ReadWALETag(filepath.Join(dir, "apply.wal")); etag != "" {
		return etag
	}
	// predl_ready.json — use loadProgressFile via kurogames helper
	predlPath := filepath.Join(dir, "predl_ready.json")
	if pf, err := kurogames.LoadProgressFromPath(predlPath); err == nil {
		return pf.ETag
	}
	return ""
}

// UpdateStatusAll returns per-game state snapshots.
func (a *App) UpdateStatusAll() map[string]GameUpdateSnapshot {
	return a.updateRegistry.SnapshotAll()
}

// --- helpers ---

func (a *App) setLastError(gid core.GameID, err *core.UpdateError) {
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.LastError = err
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
}

func (a *App) kurogamesTempDir(gid core.GameID) string {
	td := a.settings.Backends.Kurogames.TempDir
	if td == "" {
		return filepath.Join(osTempDir(), "launcher-collection")
	}
	return td
}

func (a *App) gameInstallDir(gid core.GameID, p core.Provider) string {
	installs, err := p.DetectInstall(context.Background())
	if err != nil {
		return ""
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return ig.InstallPath
		}
	}
	return ""
}

func (a *App) preflightChecks(tempDir, gameDir string, totalBytes int64) error {
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
	// Disk space precheck — implementation uses windows.GetDiskFreeSpaceEx
	// or syscall equivalent. Stub for non-Windows tests.
	if !platformHasFreeSpace(tempDir, totalBytes+(256<<20)) {
		return &core.UpdateError{
			Code:      "disk_full",
			Retryable: false,
			Params:    map[string]string{"need": fmt.Sprint(totalBytes), "have": "<computed>"},
		}
	}
	return nil
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

// gameExeName resolves the .exe filename for gid via the core.ExeNamer
// optional interface. Returns ("", false) if the provider does not implement
// ExeNamer or gid is unknown to the provider. Same shape as the lookup in
// internal/app/asset_handler.go:101.
func gameExeName(p core.Provider, gid core.GameID) (string, bool) {
	en, ok := p.(core.ExeNamer)
	if !ok {
		return "", false
	}
	return en.ExeName(gid)
}

func removeAll(path string) error {
	return osRemoveAll(path) // wraps os.RemoveAll for testability
}
