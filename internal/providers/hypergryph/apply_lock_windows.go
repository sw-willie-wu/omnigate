//go:build windows

package hypergryph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const lockFileName = ".omnigate_hg_update.lock"

func platformApplyLock() applyLock {
	return &windowsApplyLock{}
}

type windowsApplyLock struct {
	file *os.File
}

func (w *windowsApplyLock) Acquire(gameDir string) error {
	if w.file != nil {
		return errors.New("applyLock already acquired")
	}
	lockPath := filepath.Join(gameDir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return fmt.Errorf("acquire apply lock: %w", err)
	}
	w.file = f
	return nil
}

func (w *windowsApplyLock) Release() error {
	if w.file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(w.file.Fd()), 0, 1, 0, &overlapped)
	err := w.file.Close()
	w.file = nil
	return err
}
