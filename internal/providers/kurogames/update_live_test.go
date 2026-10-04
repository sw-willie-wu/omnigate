//go:build live

package kurogames

import (
	"context"
	"os"
	"testing"

	"omnigate/internal/core"
)

// TestLiveCheckForUpdate builds a v3 update plan against the real server and
// the real (READ-ONLY) install named by OMNIGATE_WUWA_DIR. Hashes the whole
// install (~86 GB) — slow; an up-to-date install must yield an empty plan.
func TestLiveCheckForUpdate(t *testing.T) {
	dir := os.Getenv("OMNIGATE_WUWA_DIR")
	if dir == "" {
		t.Skip("OMNIGATE_WUWA_DIR not set")
	}
	p := New(Settings{TempDir: t.TempDir()}, nil)
	p.SetResolvedPaths(map[core.GameID]string{"kurogames/wutheringwaves": dir})
	plan, err := p.CheckForUpdateWithProgress(context.Background(), "kurogames/wutheringwaves", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("version=%s files=%d groups=%d bytes=%d", plan.Version, len(plan.Files), len(plan.PatchGroups), plan.TotalBytes)
	if len(plan.Files) != 0 || len(plan.PatchGroups) != 0 {
		t.Fatalf("expected empty plan on an up-to-date install")
	}
}
