//go:build windows

package hoyoverse

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformIsProcessRunning enumerates Windows processes via
// CreateToolhelp32Snapshot + Process32First/Next, comparing the executable
// basename case-insensitively to `exeName`. Mirrors
// internal/providers/kurogames/process_check_windows.go pattern verbatim.
//
// Returns true if any matching process is found; false on no match OR
// any enumeration error (errors are swallowed — used as a best-effort
// gate, not a security check).
func platformIsProcessRunning(exeName string) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(exe, exeName) {
			return true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return false
}
