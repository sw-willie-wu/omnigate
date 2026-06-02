package kurogames

import (
	"context"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestKuroLocator_JoinsFolderUnderLauncherRoot(t *testing.T) {
	read := func() (string, bool) { return `C:\Games\Wuthering Waves\uninst.exe`, true }
	got, err := kuroLocator{read: read}.LocateInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The game lives in FolderName under the launcher root — not at the root.
	want := filepath.Join(`C:\Games\Wuthering Waves`, FolderNames()["kurogames/wutheringwaves"])
	if got["kurogames/wutheringwaves"] != want {
		t.Errorf("got %q, want %q (full map %+v)", got["kurogames/wutheringwaves"], want, got)
	}
}

func TestKuroLocator_NoRecord(t *testing.T) {
	read := func() (string, bool) { return "", false }
	got, err := kuroLocator{read: read}.LocateInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil map")
	}
	if _, ok := got["kurogames/wutheringwaves"]; ok {
		t.Errorf("expected no entry when reader yields nothing: %+v", got)
	}
}

func TestKurogamesImplementsInstallLocator(t *testing.T) {
	var _ core.InstallLocator = New(Settings{}, nil)
}
