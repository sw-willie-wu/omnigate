package hypergryph

import (
	"context"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestGryphLocator_JoinsFolderUnderRoot(t *testing.T) {
	got, err := gryphLocator{root: func() (string, bool) { return `D:\GL`, true }}.LocateInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`D:\GL`, "games", "EndField Game") // = hypergryph.FolderNames()["hypergryph/endfield"] joined
	if got["hypergryph/endfield"] != want {
		t.Errorf("got %+v want %q", got, want)
	}
}

func TestGryphLocator_NoRoot(t *testing.T) {
	got, _ := gryphLocator{root: func() (string, bool) { return "", false }}.LocateInstalls(context.Background())
	if len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func TestHypergryphImplementsInstallLocator(t *testing.T) { var _ core.InstallLocator = New(Settings{}, nil) }
