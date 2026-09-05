package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"omnigate/internal/core"
	"omnigate/internal/patch/hpatchz"
)

// applyWAL is the on-disk shape of apply.wal (spec §2.2). Embeds the
// manifest snapshot so recovery doesn't need to re-fetch from network.
type applyWAL struct {
	GameID   string   `json:"game_id"`
	Version  string   `json:"version"`
	ETag     string   `json:"etag"`
	WasPredl bool     `json:"was_predl"` // for recovery message variant (spec §6.3)
	Pending  []string `json:"pending"`   // relative paths still to apply
	Done     []string `json:"done"`      // relative paths already moved
}

// applier wraps dependencies for the apply phase.
type applier struct {
	logger   *slog.Logger
	tempRoot string
	gameDir  string
	progress *progressStore
	plan     *core.UpdatePlan
	wasPredl bool
	onEvent  func(core.UpdateEvent)
	lock     applyLock

	// exeName + procRunning back the cheap re-guard immediately before each
	// patch group (spec §4-2). Injected by RunUpdate as g.ExeName /
	// isProcessRunning — the applier itself never queries the process
	// registry. Zero-value (exeName == "") disables the guard, which is why
	// RunUpdate's applier literal MUST set both; a missing injection fails
	// silently (no compile error, no test failure short of the dedicated
	// process-guard test), so treat this wiring as load-bearing.
	exeName     string
	procRunning func(string) bool
}

// errEphemeralLeak is returned by assertNotEphemeral when a relPath about to
// be renamed into gameDir belongs to an Ephemeral FileTask. Theoretically
// unreachable (the rename loop's `if f.Ephemeral { continue }` already skips
// these paths) — this is the independent defence-in-depth check per spec
// invariant 2, guarding against a future refactor that drops the loop guard.
var errEphemeralLeak = errors.New("ephemeral file must never be renamed into game dir")

// assertNotEphemeral returns errEphemeralLeak when relPath belongs to an
// Ephemeral task in plan. Called immediately before every gameDir rename.
func assertNotEphemeral(plan *core.UpdatePlan, relPath string) error {
	for _, f := range plan.Files {
		if f.Ephemeral && f.Path == relPath {
			return errEphemeralLeak
		}
	}
	return nil
}

// hasKrpdiffSuffix reports whether relPath ends in ".krpdiff"
// (case-insensitive) — used by runApply's rename loop to reject any task
// bearing this suffix, independent of its Ephemeral flag.
//
// This is the categorical choke point: buildFileAndPatchPlan's own suffix
// check (update_patchplan.go, "orphan krpdiff resource entry") only covers
// the groupInfos-classification path it owns. Two other plan-producing
// routes call filterChangedFiles directly — step 1's legacy no-groupInfos
// flow and fullFallback's whole-plan-fallback flow (update_patchplan.go)
// — and neither passes through that check, so a hypothetical *.krpdiff
// resource entry reaching either of them would be staged as an ordinary
// non-Ephemeral FileTask and sail straight past assertNotEphemeral (which
// only inspects the Ephemeral flag, not the suffix). Catching the suffix
// here, at the single point immediately before every gameDir rename,
// closes that gap regardless of which upstream path produced the plan —
// the 2026-08-20 incident class this whole defence exists for.
func hasKrpdiffSuffix(relPath string) bool {
	return strings.EqualFold(filepath.Ext(relPath), ".krpdiff")
}

// safeGameRelPath validates rel as a gameDir-relative path with no
// traversal (spec §4-3 deleteFiles guard — the mirror image of the
// 2026-08-20 incident: a corrupt/hostile DeleteFiles entry must never
// resolve outside gameDir). Rejects absolute paths, ".." components, and
// any path that escapes gameDir after filepath.Clean. Returns the absolute
// joined path on success.
func safeGameRelPath(gameDir, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty delete path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %s", rel)
	}
	cleanRel := filepath.Clean(rel)
	if cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes game dir: %s", rel)
	}
	absGameDir, err := filepath.Abs(gameDir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(absGameDir, cleanRel)
	if full != absGameDir && !strings.HasPrefix(full, absGameDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes game dir: %s", rel)
	}
	return full, nil
}

// applyErr classifies an apply-phase write failure: a permission error (e.g.
// game installed under C:\Program Files\ without admin) becomes permission_denied
// — carrying the game id so the UI can offer one-click elevation — otherwise the
// generic apply_partial. core.IsPermissionError unwraps os.LinkError/Errno.
func (a *applier) applyErr(path string, err error) error {
	if core.IsPermissionError(err) {
		return &core.UpdateError{
			Code:      "permission_denied",
			Retryable: true,
			Params:    map[string]string{"game": string(a.plan.GameID)},
		}
	}
	return &core.UpdateError{
		Code:      "apply_partial",
		Retryable: true,
		// "file" duplicates "path" — the apply_partial locale strings
		// interpolate {file}, not {path} (pre-existing mismatch fixed in
		// Task 11); "path" is kept for compat with any existing consumer.
		Params: map[string]string{"path": path, "file": path, "reason": err.Error()},
	}
}

// pathGuardErr classifies a safeGameRelPath rejection (spec §4-3 traversal
// guard) as invalid_path — deliberately distinct from apply_partial. A
// rejected path is a corrupt/hostile manifest entry, not a transient I/O
// failure: it is never retryable, and apply_partial's "close the game and
// retry" copy is actively wrong advice here.
func pathGuardErr(rel string) error {
	return &core.UpdateError{
		Code:      "invalid_path",
		Retryable: false,
		Params:    map[string]string{"file": rel},
	}
}

// runApply executes the apply phase: writes WAL, atomic-renames each file,
// appends to WAL Done list, deletes WAL on success. ctx.Done() inside the
// file-rename loop is treated as no-op per spec §2.6 (apply is
// atomic-batch; renames are cheap and there's no meaningful place to
// interrupt mid-loop). The patch phase (runPatchGroups) below is the
// intentional exception: it DOES check ctx.Err() at each group boundary
// before invoking hpatchz, since a single krpdiff apply can run long
// enough that a genuine cancel should still take effect between groups.
func (a *applier) runApply(ctx context.Context) error {
	a.logger.Debug("runApply: enter", "game", a.plan.GameID, "files", len(a.plan.Files), "version", a.plan.Version, "game_dir", a.gameDir, "temp_root", a.tempRoot)

	// Cross-volume re-check (spec §5.6): apply phase must be on same volume
	// as it was at preflight. Extracted to a helper for unit testability
	// (TestApply_VolumeChangedBetweenPhases — spec §7.2).
	if err := validateSameVolume(a.tempRoot, a.gameDir); err != nil {
		a.logger.Warn("runApply: validateSameVolume failed", "game", a.plan.GameID, "err", err)
		return err
	}

	// Acquire applyLock (3rd guard; spec §2.7).
	// Lockfile lives under tempRoot rather than gameDir so non-admin
	// processes can still coordinate even when gameDir is under Program
	// Files (which requires elevation to write). The coordination scope
	// drops from "any launcher writing this gameDir" to "any of our
	// launcher instances writing this gameDir's temp subtree" — sufficient
	// for spec §2.7 (we don't coordinate with KRLauncher anyway; different
	// lockfile names + KRLauncher uses its own apply path).
	lockDir := a.progress.dir()
	if err := a.lock.Acquire(lockDir); err != nil {
		a.logger.Warn("runApply: applyLock acquire failed", "game", a.plan.GameID, "lock_dir", lockDir, "err", err)
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params: map[string]string{
				"kind":   "lock_held",
				"reason": err.Error(),
			},
		}
	}
	defer a.lock.Release()
	a.logger.Debug("runApply: applyLock acquired", "game", a.plan.GameID, "lock_dir", lockDir)

	// Initialize WAL with all pending paths. Ephemeral tasks are excluded
	// (spec invariant 2): they feed the patch phase below, not a gameDir
	// rename, and must never appear as a WAL rename target — the
	// 2026-08-20 incident was exactly a diff file getting renamed into the
	// game dir.
	pending := make([]string, 0, len(a.plan.Files))
	renameTotal := 0
	for _, f := range a.plan.Files {
		if f.Ephemeral {
			continue
		}
		pending = append(pending, f.Path)
		renameTotal++
	}
	wal := applyWAL{
		GameID:   string(a.plan.GameID),
		Version:  a.plan.Version,
		ETag:     a.plan.ManifestETag,
		WasPredl: a.wasPredl,
		Pending:  pending,
		Done:     []string{},
	}
	walPath := filepath.Join(a.progress.dir(), "apply.wal")
	if err := writeWALAtomic(walPath, &wal); err != nil {
		return &core.UpdateError{
			Code:      "apply_partial",
			Retryable: true,
			Params:    map[string]string{"reason": err.Error()},
		}
	}
	// fsync — go's os.Rename relies on fs guarantees; explicit fsync via re-open
	if err := fsyncFile(walPath); err != nil {
		a.logger.Warn("apply.wal fsync failed; proceeding", "err", err)
	}

	// Now safe to drop progress.json (spec §5.3 transition)
	_ = os.Remove(filepath.Join(a.progress.dir(), "progress.json"))

	// Apply each file; cancel.Done() is no-op for this rename loop (spec
	// §2.6) — the patch phase below is where ctx cancellation actually
	// takes effect (at group boundaries). Ephemeral tasks (krpdiff diffs)
	// are never a gameDir rename target — they're consumed by
	// runPatchGroups below. Total counts rename targets + patch groups so
	// the progress bar spans both sub-phases.
	var done atomic.Int64
	renameEventTotal := int64(renameTotal + len(a.plan.PatchGroups))
	for _, f := range a.plan.Files {
		if f.Ephemeral {
			continue // never a gameDir rename target
		}
		if err := assertNotEphemeral(a.plan, f.Path); err != nil {
			return a.applyErr(f.Path, err)
		}
		if hasKrpdiffSuffix(f.Path) {
			return pathGuardErr(f.Path)
		}
		src := filepath.Join(a.progress.dir(), f.Path)
		dst := filepath.Join(a.gameDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return a.applyErr(f.Path, err)
		}
		if err := atomicRename(src, dst); err != nil {
			return a.applyErr(f.Path, err)
		}
		// Update WAL: move from Pending to Done
		wal.Done = append(wal.Done, f.Path)
		wal.Pending = removeString(wal.Pending, f.Path)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if a.onEvent != nil {
			a.onEvent(core.UpdateEvent{
				Phase:       core.PhaseApply,
				Current:     done.Load(),
				Total:       renameEventTotal,
				CurrentFile: f.Path,
			})
		}
	}

	// Patch phase: krpdiff-based binary diffs (spec §4). Must run before
	// deleteFiles — a group's Src may coincide with a delete target.
	if err := a.runPatchGroups(ctx, renameTotal); err != nil {
		return err
	}

	// deleteFiles (spec §4-3): applied strictly after all patch groups.
	// Missing target = no-op (idempotent across resume/retry).
	for _, rel := range a.plan.DeleteFiles {
		clean, err := safeGameRelPath(a.gameDir, rel)
		if err != nil {
			return pathGuardErr(rel)
		}
		if rmErr := os.Remove(clean); rmErr != nil && !os.IsNotExist(rmErr) {
			return a.applyErr(rel, rmErr)
		}
	}

	// Persist new version to launcherDownloadConfig.json so subsequent
	// CheckVersion sees Current = Latest. Without this, even a 0-file apply
	// (already-up-to-date) leaves the config showing the stale local version
	// → Refresh re-flags AvailableUpdate and BottomBar bounces back to
	// [更新遊戲]. Failure is non-fatal — files are already in place.
	configPath := filepath.Join(a.gameDir, "launcherDownloadConfig.json")
	a.logger.Debug("runApply: writing launcherDownloadConfig.json", "path", configPath, "new_version", a.plan.Version)
	if err := writeLauncherConfigVersion(configPath, a.plan.Version); err != nil {
		a.logger.Warn("runApply: update launcherDownloadConfig.json failed (apply otherwise succeeded)", "err", err, "path", configPath)
	} else {
		a.logger.Info("runApply: launcherDownloadConfig.json written", "path", configPath, "version", a.plan.Version)
	}

	// All applied; remove WAL
	if err := os.Remove(walPath); err != nil {
		a.logger.Warn("remove apply.wal", "err", err)
	}

	// Clean up the entire version dir so subsequent scanForRecovery doesn't
	// re-fire on orphan markers (.lc_update.lock, stray .part files, etc).
	// Release the lock first — Windows can't delete an open file. The
	// deferred lock.Release() above is a no-op after explicit release
	// (Release is idempotent: sets w.file = nil).
	_ = a.lock.Release()
	if err := os.RemoveAll(a.progress.dir()); err != nil {
		a.logger.Warn("cleanup version dir post-apply", "dir", a.progress.dir(), "err", err)
	}
	return nil
}

// writeLauncherConfigVersion reads the existing launcherDownloadConfig.json
// (if any), overwrites only the `version` field, and atomic-renames the
// updated JSON back. Preserves any other fields KRLauncher writes (we only
// know about `version` from research). Creates a minimal `{"version":...}`
// file if none exists.
func writeLauncherConfigVersion(path, newVersion string) error {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if uerr := json.Unmarshal(data, &doc); uerr != nil {
			doc = map[string]any{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	doc["version"] = newVersion
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resumeApply replays apply.wal: re-applies any Pending entries that
// the prior crash didn't finish. Uses WAL's manifest snapshot so no
// network call.
func resumeApply(ctx context.Context, walPath, gameDir string, lock applyLock, onEvent func(core.UpdateEvent), logger *slog.Logger) error {
	body, err := os.ReadFile(walPath)
	if err != nil {
		return &core.UpdateError{Code: "unrecoverable", Params: map[string]string{"reason": err.Error()}}
	}
	var wal applyWAL
	if err := json.Unmarshal(body, &wal); err != nil {
		return &core.UpdateError{Code: "unrecoverable", Params: map[string]string{"reason": err.Error()}}
	}

	if err := lock.Acquire(gameDir); err != nil {
		return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "lock_held"}}
	}
	defer lock.Release()

	tempDir := filepath.Dir(walPath)
	var done atomic.Int64
	done.Store(int64(len(wal.Done)))
	total := int64(len(wal.Done) + len(wal.Pending))

	for _, rel := range wal.Pending {
		src := filepath.Join(tempDir, rel)
		dst := filepath.Join(gameDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel, "file": rel}}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel, "file": rel, "reason": err.Error()}}
		}
		wal.Done = append(wal.Done, rel)
		wal.Pending = removeString(wal.Pending, rel)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if onEvent != nil {
			onEvent(core.UpdateEvent{Phase: core.PhaseApply, Current: done.Load(), Total: total, CurrentFile: rel})
		}
	}
	_ = os.Remove(walPath)
	return nil
}

// validateSameVolume returns *core.UpdateError{cross_volume_midrun} when
// tempDir and gameDir resolve to different VolumeName values. Extracted so
// tests can supply hand-crafted "C:\..." vs "D:\..." paths without needing
// a real multi-volume Windows host (spec §7.2 TestApply_VolumeChangedBetweenPhases).
func validateSameVolume(tempDir, gameDir string) error {
	tempVol := filepath.VolumeName(tempDir)
	gameVol := filepath.VolumeName(gameDir)
	if tempVol != gameVol {
		return &core.UpdateError{
			Code:      "cross_volume_midrun",
			Retryable: false,
			Params: map[string]string{
				"temp_vol": tempVol,
				"game_vol": gameVol,
			},
		}
	}
	return nil
}

// atomicRename does a file-level rename; on EXDEV (cross-volume) returns
// error with details (M3.A doesn't fall back to copy+delete).
func atomicRename(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if isEXDEV(err) {
		return fmt.Errorf("cross-volume rename %s → %s: %w", src, dst, err)
	}
	return err
}

func isEXDEV(err error) bool {
	// On Windows, cross-volume rename fails with ERROR_NOT_SAME_DEVICE (0x11).
	// Match by string fragment to avoid platform-specific imports here.
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "different drive") || strings.Contains(s, "not same device") || strings.Contains(s, "cross-device")
}

func writeWALAtomic(walPath string, wal *applyWAL) error {
	body, err := json.MarshalIndent(wal, "", "  ")
	if err != nil {
		return err
	}
	tmp := walPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, walPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// fsyncFile opens the file and fsyncs to disk. Best-effort: if fails, caller
// proceeds (data is still written; only durability is at risk).
func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func removeString(s []string, target string) []string {
	out := s[:0]
	for _, x := range s {
		if x != target {
			out = append(out, x)
		}
	}
	return out
}

// runPatchGroups applies each PatchGroup via hpatchz in size-ascending
// order (spec §5 disk-peak formula depends on this order; builder already
// sorts, this re-asserts against a hand-edited plan). renameDone is the
// count of gameDir renames already completed by the loop above — used only
// as the progress-event offset so the apply-phase bar spans both
// sub-phases.
//
// dir-diff semantics (spec §4 runtime correction): hpatchz's old/out
// arguments are ROOT DIRECTORIES here, not single files — the krpdiff
// embeds relative paths (e.g. Client/Content/Paks/x.pak) and hpatchz reads
// old content from under gameDir and writes new content under outRoot at
// that same relative path.
func (a *applier) runPatchGroups(ctx context.Context, renameDone int) error {
	gs := a.plan.PatchGroups

	// Assert size-ascending (builder already sorts; this guards a
	// hand-edited/corrupt plan — the disk-peak formula in spec §5 depends
	// on this order holding at apply time).
	for i := 1; i < len(gs); i++ {
		if gs[i].Dst.Size < gs[i-1].Dst.Size {
			return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": "patch groups not size-sorted"}}
		}
	}

	outRoot := filepath.Join(a.progress.dir(), "_out")

	for i, g := range gs {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Cheap re-guard (spec §4-2): exeName/procRunning are injected by
		// RunUpdate's applier literal (g.ExeName / isProcessRunning) — the
		// applier itself never queries the process registry.
		if a.exeName != "" && a.procRunning != nil && a.procRunning(a.exeName) {
			return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "process_running"}}
		}

		// Path validation before joining (free defence-in-depth now that
		// safeGameRelPath exists — spec §4-3's traversal guard applies just
		// as much to a corrupt/hostile PatchGroup as to DeleteFiles): reject
		// absolute paths, "..", and any escape after Clean. Dst.Path is
		// validated against gameDir (it's the eventual rename target);
		// DiffPath is validated against the version temp dir (where it's
		// staged and read from).
		if _, err := safeGameRelPath(a.gameDir, g.Dst.Path); err != nil {
			return pathGuardErr(g.Dst.Path)
		}
		if _, err := safeGameRelPath(a.progress.dir(), g.DiffPath); err != nil {
			return pathGuardErr(g.DiffPath)
		}

		out := filepath.Join(outRoot, filepath.FromSlash(g.Dst.Path))
		diff := filepath.Join(a.progress.dir(), g.DiffPath)

		// Crash-resume shortcut: _out already holds a verified product
		// from a prior interrupted run (bad/consumed diff notwithstanding)
		// → skip straight to rename, don't re-invoke hpatchz.
		cached := false
		if h, err := md5File(out); err == nil && h == g.Dst.Hash {
			cached = true
		}

		if !cached {
			if a.onEvent != nil {
				a.onEvent(core.UpdateEvent{
					Phase: core.PhaseApply, Stage: "patching",
					Current: int64(renameDone + i), Total: int64(renameDone + len(gs)), CurrentFile: g.Dst.Path,
				})
			}
			if err := os.MkdirAll(outRoot, 0o755); err != nil {
				return a.applyErr(g.Dst.Path, err)
			}
			if err := hpatchz.Run(ctx, a.gameDir, diff, outRoot); err != nil {
				_ = os.RemoveAll(outRoot)
				return &core.UpdateError{Code: "patch_failed", Retryable: true, Params: map[string]string{"file": g.Dst.Path, "reason": err.Error()}}
			}
			if h, err := md5File(out); err != nil || h != g.Dst.Hash {
				_ = os.Remove(out)
				return &core.UpdateError{Code: "patch_failed", Retryable: true, Params: map[string]string{"file": g.Dst.Path, "reason": "post-patch md5 mismatch"}}
			}
		}

		// MkdirAll before rename (symmetry with the plain rename loop
		// above): a group whose Dst.Path introduces a new subdirectory
		// (not just an in-place replace) would otherwise hit a latent
		// ENOENT here.
		dst := filepath.Join(a.gameDir, g.Dst.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return a.applyErr(g.Dst.Path, err)
		}
		if err := atomicRename(out, dst); err != nil {
			return a.applyErr(g.Dst.Path, err)
		}
		_ = os.Remove(diff) // spec §5 precondition (ii): release the diff's disk space immediately
		if a.onEvent != nil {
			// Stage:"patching" here too (not just the pre-hpatchz start event
			// above) — otherwise the label reverts to empty between groups
			// (T8-M7 deferred fix). This completion event fires for BOTH
			// freshly-patched AND cache-hit (crash-resume) groups since it's
			// outside the `if !cached` block above, so a fully-cached resume
			// still shows progress instead of going silent.
			a.onEvent(core.UpdateEvent{
				Phase: core.PhaseApply, Stage: "patching",
				Current: int64(renameDone + i + 1), Total: int64(renameDone + len(gs)), CurrentFile: g.Dst.Path,
			})
		}
	}
	return nil
}
