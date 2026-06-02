//go:build windows

package hypergryph

import "golang.org/x/sys/windows"

// platformFreeDiskBytes returns the free bytes available to the caller on the
// volume containing dir (GetDiskFreeSpaceEx lpFreeBytesAvailableToCaller).
func platformFreeDiskBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeAvail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(freeAvail), nil
}
