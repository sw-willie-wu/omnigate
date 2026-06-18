//go:build windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// isElevated reports whether the current process token is elevated (admin).
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// relaunchElevated re-launches this executable with args via ShellExecute's
// "runas" verb, triggering UAC. Returns errUACDeclined if the user declines.
func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	var argsPtr *uint16
	if len(args) > 0 {
		argsPtr, err = windows.UTF16PtrFromString(strings.Join(args, " "))
		if err != nil {
			return err
		}
	}
	cwdPtr, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verbPtr, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errUACDeclined
		}
		return err
	}
	return nil
}
