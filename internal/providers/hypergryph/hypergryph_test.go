package hypergryph

import (
	"context"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestSetResolvedPaths_DetectInstallReturnsExisting(t *testing.T) {
	dir := t.TempDir() // exists
	gid := core.GameID("hypergryph/endfield")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{
		gid:             dir,
		"hypergryph/fake": filepath.Join(dir, "does-not-exist"),
	})
	got, err := p.DetectInstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GameID != gid || got[0].InstallPath != dir {
		t.Fatalf("got %+v, want only %s at %s", got, gid, dir)
	}
}

func TestGameDirFromResolved(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("hypergryph/endfield")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{gid: dir})
	got, err := p.gameDir(context.Background(), gid)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("gameDir = %q, want %q", got, dir)
	}
}
