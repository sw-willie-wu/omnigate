//go:build !windows

package hypergryph

// platformFreeDiskBytes stub for non-Windows test runners: report a huge value
// so checkDiskSpace passes (production is Windows-only).
func platformFreeDiskBytes(dir string) (int64, error) {
	return 1 << 62, nil
}
