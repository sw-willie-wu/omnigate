//go:build windows

package sevenzip

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW (0x08000000) runs the console child without allocating a
// console window — without it, the 7zr.exe invocation flashes a terminal.
const createNoWindow = 0x08000000

// hideConsoleWindow suppresses the console window for the 7zr child process.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
