//go:build windows

package hpatchz

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW (0x08000000) runs the console child without allocating a
// console window — without it, each hpatchz.exe invocation flashes a terminal,
// which is very visible during a patch apply that runs hpatchz hundreds of times.
const createNoWindow = 0x08000000

// hideConsoleWindow suppresses the console window for the hpatchz child process.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
