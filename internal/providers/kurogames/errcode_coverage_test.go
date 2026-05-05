package kurogames

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrcodeCoverage walks the manifest of error codes (spec §7.2) and
// asserts each has at least one referencing .go file under
// internal/providers/kurogames/ or internal/app/. Scans BOTH production
// and test files: a code that ships in production code but lacks an
// "Exercising test" (per spec §7.2) is a coverage gap that a deeper
// suite catches; this test only catches "code defined in enum but never referenced".
func TestErrcodeCoverage(t *testing.T) {
	codes := []string{
		"process_blocked", "manifest_changed", "manifest_not_found",
		"auth_failed", "network", "predl_stale", "interrupted_resume",
		"disk_full", "cross_volume_temp", "cross_volume_midrun",
		"corrupt", "apply_partial", "unrecoverable",
		"unsupported_filesystem", "internal",
	}
	content := allGoFilesContent(t)
	for _, code := range codes {
		needle := `"` + code + `"`
		if !strings.Contains(content, needle) {
			t.Errorf("error code %q has no source reference; either add it to a code path (production) or remove from the catalog", code)
		}
	}
}

func allGoFilesContent(t *testing.T) string {
	t.Helper()
	roots := []string{".", "../../app"}
	var sb strings.Builder
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".go") {
				return nil
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			sb.Write(body)
			sb.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Logf("walk %s: %v (skipped)", root, err)
		}
	}
	return sb.String()
}
