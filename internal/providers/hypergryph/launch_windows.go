package hypergryph

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"omnigate/internal/core"
)

// Launch via ShellExecute — same pattern as hoyoverse/kurogames.
func Launch(_ context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
	g := findByID(gid)
	if g == nil {
		return 0, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
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
	if err := windows.ShellExecute(0, nil, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return 0, fmt.Errorf("ShellExecute %s: %w", exePath, err)
	}
	return 0, nil
}
