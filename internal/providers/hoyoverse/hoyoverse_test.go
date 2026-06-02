package hoyoverse

import (
	"context"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestSetResolvedPaths_DetectInstallReturnsExisting(t *testing.T) {
	dir := t.TempDir() // exists
	gid := core.GameID("hoyoverse/genshin")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{
		gid:                  dir,
		"hoyoverse/starrail": filepath.Join(dir, "does-not-exist"),
	})
	got, err := p.DetectInstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GameID != gid || got[0].InstallPath != dir {
		t.Fatalf("got %+v, want only %s at %s", got, gid, dir)
	}
}

func TestProvider_IsGameRunning_Stub(t *testing.T) {
	p := &Provider{}
	got, err := p.IsGameRunning(core.GameID("hoyoverse/genshin"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	_ = got
}

func TestProvider_CheckForUpdate_FullPath(t *testing.T) {
	t.Skip("integration scenario lives in Task 20 integration_test.go")
}

func TestProvider_RunUpdate_ResumeDispatchTable(t *testing.T) {
	t.Skip("dispatch validation in Task 20 integration_test.go")
}

func TestProvider_SelfHeal_ConfigWritebackFailure(t *testing.T) {
	t.Skip("self-heal scenario in Task 20 integration_test.go")
}
