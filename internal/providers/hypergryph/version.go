package hypergryph

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// readLocalVersion reads <installPath>/config.ini (AES-256-CBC encrypted; see
// crypto.go) and returns its `version=` value. Missing config.ini → ("", nil)
// (unknown, not an error). Mirrors Collapse ConfigTool.cs + HgGameManager.cs.
func readLocalVersion(installPath string) (string, error) {
	content, err := decryptConfigFile(filepath.Join(installPath, "config.ini"))
	if err != nil {
		return "", err
	}
	return parseConfigVersion(content), nil
}

// parseConfigVersion extracts the `version=` value from decrypted config.ini.
func parseConfigVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "version="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// fetchVersion returns Current (local config.ini) + Latest (get_latest.version).
// Latest fetch failure is non-fatal (Latest falls back to Current). If config.ini
// is absent/unreadable, Current is empty (degrade, spec §6) — never blocks.
func fetchVersion(ctx context.Context, client *http.Client, installPath string, _ core.GameID) (core.VersionInfo, error) {
	cur, err := readLocalVersion(installPath)
	if err != nil {
		return core.VersionInfo{}, err
	}
	latest := cur
	if resp, ferr := fetchGetLatest(ctx, client, ""); ferr == nil && resp.Version != "" {
		latest = resp.Version
	}
	if cur == "" {
		return core.VersionInfo{Latest: latest}, nil
	}
	return core.VersionInfo{Current: cur, Latest: latest}, nil
}
