//go:build live

package kurogames

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveReadInstallState(t *testing.T) {
	dir := os.Getenv("OMNIGATE_WUWA_DIR")
	if dir == "" {
		t.Skip("OMNIGATE_WUWA_DIR not set")
	}
	s, err := readInstallState(filepath.Join(dir, installStateFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed=%v pending=%v common=%s legacy=%v", s.installedKnown(), s.pendingKnown(), s.packVersion("common"), s.Legacy)
	if len(s.installedKnown()) == 0 {
		t.Fatal("expected at least one installed bundle")
	}
}
