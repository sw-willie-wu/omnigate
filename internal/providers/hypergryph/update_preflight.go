package hypergryph

import (
	"path/filepath"
	"strconv"

	"omnigate/internal/core"
)

// preflightSameVolume returns *core.UpdateError{cross_volume_temp} when tempDir
// and gameDir are on different volumes (atomic rename across volumes fails).
// Distinct from the mid-apply validateSameVolume → cross_volume_midrun.
func preflightSameVolume(tempDir, gameDir string) error {
	if filepath.VolumeName(tempDir) != filepath.VolumeName(gameDir) {
		return &core.UpdateError{
			Code:      "cross_volume_temp",
			Retryable: false,
			Params:    map[string]string{"temp_vol": filepath.VolumeName(tempDir), "game_vol": filepath.VolumeName(gameDir)},
		}
	}
	return nil
}

// checkDiskSpace returns *core.UpdateError{disk_full} when the volume holding
// dir has less than needed free bytes. A stat failure is treated as best-effort
// pass (don't block the update on an unreadable free-space query).
func checkDiskSpace(dir string, needed int64) error {
	free, err := platformFreeDiskBytes(dir)
	if err != nil {
		return nil
	}
	if free < needed {
		return &core.UpdateError{
			Code:      "disk_full",
			Retryable: false,
			Params:    map[string]string{"need": strconv.FormatInt(needed, 10), "have": strconv.FormatInt(free, 10)},
		}
	}
	return nil
}
