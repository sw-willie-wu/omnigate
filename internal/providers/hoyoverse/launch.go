package hoyoverse

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// Launch starts the game by executing its main exe. Returns the spawned PID.
// We do not capture stdout/stderr nor inject anything into the process, to
// avoid anti-cheat false positives.
func Launch(ctx context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
	g := findByID(gid)
	if g == nil {
		return 0, fmt.Errorf("unknown game id %q", gid)
	}
	exePath := filepath.Join(installPath, g.ExeName)
	cmd := exec.CommandContext(ctx, exePath, opts.ExtraArgs...)
	cmd.Dir = installPath
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", exePath, err)
	}
	return cmd.Process.Pid, nil
}
