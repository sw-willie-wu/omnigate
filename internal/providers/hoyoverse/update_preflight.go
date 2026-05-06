package hoyoverse

import (
	"fmt"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// freeSpaceProbe is the test seam for OS free-space queries.
type freeSpaceProbe interface {
	FreeBytes(path string) (uint64, error)
}

// CheckDiskSpace verifies (a) staging tempRoot and gameDir are on the same
// volume; (b) sufficient free bytes at tempRoot for `Σ FileTask.Size × 1.1`
// (download size; not decompressed which Stage F handles per-file).
//
// Returns *core.UpdateError on failure (code = cross_volume_setup or
// insufficient_space). Returns nil on success.
func CheckDiskSpace(plan core.UpdatePlan, tempRoot, gameDir string, probe freeSpaceProbe) error {
	if !sameVolumeWindows(tempRoot, gameDir) {
		return &core.UpdateError{
			Code: "cross_volume_setup",
			Params: map[string]string{
				"temp_root": tempRoot,
				"game_dir":  gameDir,
			},
			Retryable: false,
		}
	}
	var total int64
	for _, f := range plan.Files {
		total += f.Size
	}
	required := uint64(float64(total) * 1.1)

	free, err := probe.FreeBytes(tempRoot)
	if err != nil {
		return fmt.Errorf("free-space probe: %w", err)
	}
	if free < required {
		return &core.UpdateError{
			Code: "insufficient_space",
			Params: map[string]string{
				"required":  formatGiB(required),
				"available": formatGiB(free),
			},
			Retryable: false,
		}
	}
	return nil
}

func sameVolumeWindows(a, b string) bool {
	a = filepath.VolumeName(a)
	b = filepath.VolumeName(b)
	if a == "" && b == "" {
		return true
	}
	return strings.EqualFold(a, b)
}

func formatGiB(bytes uint64) string {
	const gib = 1024 * 1024 * 1024
	return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
}
