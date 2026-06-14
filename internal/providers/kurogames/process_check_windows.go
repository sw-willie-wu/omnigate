//go:build windows

package kurogames

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
	target := strings.ToLower(exeName)
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(exe, target) {
			return true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return false
}

// anyProcessRunning reports whether ANY of the named exes is running.
func anyProcessRunning(names []string) bool {
	for _, n := range names {
		if platformIsProcessRunning(n) {
			return true
		}
	}
	return false
}
