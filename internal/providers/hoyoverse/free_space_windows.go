//go:build windows

package hoyoverse

import "golang.org/x/sys/windows"

func windowsFreeBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
