package hypergryph

import (
	"context"
	"fmt"
	"net/http"
	"os"
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

// writeLocalVersion re-encrypts <installPath>/config.ini with the `version=`
// line replaced by newVersion. Decrypt → setConfigVersion → re-encrypt → atomic
// write. Spec §5/§6 (omnigate-original; the reference copies a server-supplied
// config.ini.new instead). Caller treats failure as non-fatal (the apply already
// succeeded) but should log it.
func writeLocalVersion(installPath, newVersion string) error {
	configPath := filepath.Join(installPath, "config.ini")
	content, err := decryptConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("decrypt config.ini: %w", err)
	}
	if content == "" {
		// No existing config.ini to update — nothing to write back (degrade, §6).
		return fmt.Errorf("config.ini missing or empty; cannot write version")
	}
	updated := setConfigVersion(content, newVersion)
	ct, err := encryptAESCBC([]byte(updated))
	if err != nil {
		return fmt.Errorf("encrypt config.ini: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, ct, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// setConfigVersion replaces the `version=` line in decrypted config.ini text,
// preserving all other lines. If no version line exists, one is appended.
func setConfigVersion(content, newVersion string) string {
	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "version=") {
			lines[i] = "version=" + newVersion
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "version="+newVersion)
	}
	return strings.Join(lines, "\n")
}
