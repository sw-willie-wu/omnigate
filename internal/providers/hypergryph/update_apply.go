package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"omnigate/internal/core"
)

// applyWAL is the on-disk shape of apply.wal. Embeds the manifest snapshot so
// recovery doesn't need to re-fetch from network.
type applyWAL struct {
	GameID   string   `json:"game_id"`
	Version  string   `json:"version"`
	ETag     string   `json:"etag"`
	WasPredl bool     `json:"was_predl"`
	Pending  []string `json:"pending"`
	Done     []string `json:"done"`
}

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

func (a *applier) runApply(ctx context.Context) error {
	a.logger.Debug("runApply: enter", "game", a.plan.GameID, "files", len(a.plan.Files), "version", a.plan.Version, "game_dir", a.gameDir)

	if err := validateSameVolume(a.tempRoot, a.gameDir); err != nil {
		a.logger.Warn("runApply: validateSameVolume failed", "game", a.plan.GameID, "err", err)
		return err
	}

	lockDir := a.progress.dir()
	if err := a.lock.Acquire(lockDir); err != nil {
		a.logger.Warn("runApply: applyLock acquire failed", "game", a.plan.GameID, "lock_dir", lockDir, "err", err)
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "lock_held", "reason": err.Error()},
		}
	}
	defer a.lock.Release()

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
		return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"reason": err.Error()}}
	}
	if err := fsyncFile(walPath); err != nil {
		a.logger.Warn("apply.wal fsync failed; proceeding", "err", err)
	}

	_ = os.Remove(filepath.Join(a.progress.dir(), "progress.json"))

	var done atomic.Int64
	for _, f := range a.plan.Files {
		src := filepath.Join(a.progress.dir(), f.Path)
		dst := filepath.Join(a.gameDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": f.Path, "reason": err.Error()}}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": f.Path, "reason": err.Error()}}
		}
		wal.Done = append(wal.Done, f.Path)
		wal.Pending = removeString(wal.Pending, f.Path)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if a.onEvent != nil {
			a.onEvent(core.UpdateEvent{Phase: core.PhaseApply, Current: done.Load(), Total: int64(len(a.plan.Files)), CurrentFile: f.Path})
		}
	}

	// Persist new version to config.ini (AES re-encrypt) so subsequent
	// CheckVersion sees Current = Latest. Unconditional (mirrors kuro): even a
	// 0-file apply must update the version or Refresh re-flags AvailableUpdate
	// and BottomBar bounces back to [更新遊戲] (spec §6/B3). Non-fatal: files
	// are already in place.
	a.logger.Debug("runApply: writing config.ini version", "game_dir", a.gameDir, "new_version", a.plan.Version)
	if err := writeLocalVersion(a.gameDir, a.plan.Version); err != nil {
		a.logger.Warn("runApply: config.ini version writeback failed (apply otherwise succeeded)", "err", err, "game_dir", a.gameDir)
	} else {
		a.logger.Info("runApply: config.ini version written", "game_dir", a.gameDir, "version", a.plan.Version)
	}

	if err := os.Remove(walPath); err != nil {
		a.logger.Warn("remove apply.wal", "err", err)
	}

	_ = a.lock.Release()
	if err := os.RemoveAll(a.progress.dir()); err != nil {
		a.logger.Warn("cleanup version dir post-apply", "dir", a.progress.dir(), "err", err)
	}
	return nil
}

// resumeApply replays apply.wal: re-applies any Pending entries a prior crash
// didn't finish. Uses the WAL's manifest snapshot so no network call.
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

// validateSameVolume returns *core.UpdateError{cross_volume_midrun} when tempDir
// and gameDir resolve to different VolumeName values.
func validateSameVolume(tempDir, gameDir string) error {
	tempVol := filepath.VolumeName(tempDir)
	gameVol := filepath.VolumeName(gameDir)
	if tempVol != gameVol {
		return &core.UpdateError{
			Code:      "cross_volume_midrun",
			Retryable: false,
			Params:    map[string]string{"temp_vol": tempVol, "game_vol": gameVol},
		}
	}
	return nil
}

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
