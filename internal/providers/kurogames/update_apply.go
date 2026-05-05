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

	"launcher-collection-tmp/internal/core"
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
}

// runApply executes the apply phase: writes WAL, atomic-renames each file,
// appends to WAL Done list, deletes WAL on success. ctx.Done() inside the
// loop is treated as no-op per spec §2.6 (apply is atomic-batch).
func (a *applier) runApply(ctx context.Context) error {
	// Cross-volume re-check (spec §5.6): apply phase must be on same volume
	// as it was at preflight. Extracted to a helper for unit testability
	// (TestApply_VolumeChangedBetweenPhases — spec §7.2).
	if err := validateSameVolume(a.tempRoot, a.gameDir); err != nil {
		return err
	}

	// Acquire applyLock (3rd guard; spec §2.7)
	if err := a.lock.Acquire(a.gameDir); err != nil {
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

	// Initialize WAL with all pending paths
	pending := make([]string, len(a.plan.Files))
	for i, f := range a.plan.Files {
		pending[i] = f.Path
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

	// Apply each file; cancel.Done() is no-op (spec §2.6)
	var done atomic.Int64
	for _, f := range a.plan.Files {
		src := filepath.Join(a.progress.dir(), f.Path)
		dst := filepath.Join(a.gameDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{
				Code:      "apply_partial",
				Retryable: true,
				Params:    map[string]string{"path": f.Path, "reason": err.Error()},
			}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{
				Code:      "apply_partial",
				Retryable: true,
				Params:    map[string]string{"path": f.Path, "reason": err.Error()},
			}
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
				Total:       int64(len(a.plan.Files)),
				CurrentFile: f.Path,
			})
		}
	}

	// All applied; remove WAL
	if err := os.Remove(walPath); err != nil {
		a.logger.Warn("remove apply.wal", "err", err)
	}
	return nil
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
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel}}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel, "reason": err.Error()}}
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

var _ = errors.Is // silence unused import if errors not actually used
