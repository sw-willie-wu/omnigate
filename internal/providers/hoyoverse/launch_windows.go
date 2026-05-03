package hoyoverse

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"launcher-collection-tmp/internal/core"
)

// Launch starts the game via Windows ShellExecute so the exe's manifest can
// trigger UAC elevation when needed (HoYoverse anti-cheat drivers require
// admin). exec.Command / CreateProcess does NOT honor the manifest's
// requestedExecutionLevel, hence the explicit Win32 call.
//
// We do not capture stdout/stderr nor track the spawned process — anti-cheat
// can flag a polling parent. PID is returned as 0 by design.
func Launch(_ context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
	g := findByID(gid)
	if g == nil {
		return 0, fmt.Errorf("unknown game id %q", gid)
	}
	exePath := filepath.Join(installPath, g.ExeName)

	exePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return 0, fmt.Errorf("utf16 exe: %w", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(installPath)
	if err != nil {
		return 0, fmt.Errorf("utf16 cwd: %w", err)
	}
	var argsPtr *uint16
	if len(opts.ExtraArgs) > 0 {
		argsPtr, err = windows.UTF16PtrFromString(strings.Join(opts.ExtraArgs, " "))
		if err != nil {
			return 0, fmt.Errorf("utf16 args: %w", err)
		}
	}

	// verb=nil → default ("open"), so the manifest's requestedExecutionLevel
	// drives whether UAC is shown. SW_NORMAL (1) = show window normally.
	if err := windows.ShellExecute(0, nil, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return 0, fmt.Errorf("ShellExecute %s: %w", exePath, err)
	}
	return 0, nil
}
