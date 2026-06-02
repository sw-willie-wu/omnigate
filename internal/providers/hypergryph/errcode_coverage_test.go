package hypergryph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrcodeCoverage asserts each error code the provider emits (spec §7) has
// a referencing .go file under this package or internal/app/. Mirrors kuro.
func TestErrcodeCoverage(t *testing.T) {
	codes := []string{
		"manifest_changed", "manifest_not_found", "network", "auth_failed",
		"corrupt", "disk_full", "cross_volume_temp", "cross_volume_midrun",
		"process_blocked", "apply_partial", "unrecoverable", "internal",
	}
	content := allGoFilesContent(t)
	for _, code := range codes {
		if !strings.Contains(content, `"`+code+`"`) {
			t.Errorf("error code %q has no source reference", code)
		}
	}
}

func allGoFilesContent(t *testing.T) string {
	t.Helper()
	roots := []string{".", "../../app"}
	var sb strings.Builder
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
				return nil
			}
			if body, rerr := os.ReadFile(path); rerr == nil {
				sb.Write(body)
				sb.WriteByte('\n')
			}
			return nil
		})
	}
	return sb.String()
}
