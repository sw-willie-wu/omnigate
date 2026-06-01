//go:build windows

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var osTempDir = func() string { return os.TempDir() }
var osRemoveAll = func(path string) error { return os.RemoveAll(path) }
var osReadDir = func(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

func platformHasFreeSpace(dir string, need int64) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW := kernel32.NewProc("GetDiskFreeSpaceExW")
	dirPtr, _ := syscall.UTF16PtrFromString(dir)
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	r1, _, _ := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return true // err — assume OK to avoid blocking
	}
	return int64(freeBytesAvailable) >= need
}

// platformFilesystemName resolves "NTFS" / "FAT32" / "exFAT" for the volume
// containing dir. Returns ("", false) on lookup failure (caller treats as
// "skip the FS check"). Per spec §1.3 + §6.1 unsupported_filesystem.
func platformFilesystemName(dir string) (string, bool) {
	root := filepath.VolumeName(dir) + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", false
	}
	var (
		volNameBuf   [windows.MAX_PATH + 1]uint16
		serial       uint32
		maxComponent uint32
		fsFlags      uint32
		fsNameBuf    [windows.MAX_PATH + 1]uint16
	)
	if err := windows.GetVolumeInformation(
		rootPtr,
		&volNameBuf[0], uint32(len(volNameBuf)),
		&serial, &maxComponent, &fsFlags,
		&fsNameBuf[0], uint32(len(fsNameBuf)),
	); err != nil {
		return "", false
	}
	return windows.UTF16ToString(fsNameBuf[:]), true
}
