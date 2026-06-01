//go:build !windows

package sevenzip

import "os/exec"

// hideConsoleWindow is a no-op on non-Windows (no console window to suppress).
func hideConsoleWindow(cmd *exec.Cmd) {}
