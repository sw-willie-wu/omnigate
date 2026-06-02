package kurogames

import (
	"context"
	"testing"

	"omnigate/internal/core"
)

func TestKuroLocator_DerivesInstallDir(t *testing.T) {
	read := func() (string, bool) { return `C:\Games\Wuthering Waves\uninst.exe`, true }
	got, err := kuroLocator{read: read}.LocateInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["kurogames/wutheringwaves"] != `C:\Games\Wuthering Waves` {
		t.Errorf("got %+v", got)
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
