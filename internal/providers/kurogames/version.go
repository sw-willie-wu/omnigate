package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

// fetchVersion returns version info for one game. Reads the launcher's
// canonical config file at <installPath>/launcherDownloadConfig.json.
func fetchVersion(_ context.Context, installPath string, _ core.GameID) (core.VersionInfo, error) {
	configPath := filepath.Join(installPath, "launcherDownloadConfig.json")
	v, err := readLauncherDownloadConfigVersion(configPath)
	if err != nil {
		return core.VersionInfo{}, err
	}
	if v == "" {
		return core.VersionInfo{}, nil
	}
	return core.VersionInfo{
		Current: v,
		Latest:  v, // M2: no remote-version check; show installed version as both
	}, nil
}

func readLauncherDownloadConfigVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("kurogames: parse launcherDownloadConfig.json: %w", err)
	}
	return doc.Version, nil
}
