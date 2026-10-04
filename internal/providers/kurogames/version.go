package kurogames

import (
	"context"
	"path/filepath"

	"omnigate/internal/core"
)

// fetchVersion returns the local version for one game from the install
// record (<installPath>/launcherDownloadConfig.json, v2 or v3 shape). The
// game version is the common pack's version (min over installed bundles).
// No usable record → empty VersionInfo (not installed via a known launcher).
func fetchVersion(_ context.Context, installPath string, _ core.GameID) (core.VersionInfo, error) {
	st, err := readInstallState(filepath.Join(installPath, installStateFile))
	if err != nil {
		return core.VersionInfo{}, err
	}
	v := st.packVersion("common")
	return core.VersionInfo{Current: v, Latest: v}, nil
}
