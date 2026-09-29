package hoyoverse

import (
	"context"
	"net/http"
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

// TestSophonDownloadClient pins spec §3.4: bulk Sophon transfers get a client
// with NO overall timeout, the zero-value Provider falls back to a package-level
// client WITHOUT writing it back into p (three HoYoverse games (genshin/starrail/zzz)
// share one *Provider and runStartUpdateAsync runs in a goroutine), and
// p.httpClient stays the seam.
func TestSophonDownloadClient(t *testing.T) {
	p := New(Settings{}, nil)
	if p.downloadClient == nil {
		t.Fatal("New(): downloadClient is nil, want downloader.NewClient()")
	}
	if p.downloadClient.Timeout != 0 {
		t.Errorf("New(): downloadClient.Timeout = %v, want 0 (an overall timeout caps the whole body read)", p.downloadClient.Timeout)
	}
	if got := p.sophonDownloadClient(); got != p.downloadClient {
		t.Errorf("sophonDownloadClient() = %p, want p.downloadClient (%p)", got, p.downloadClient)
	}

	// Zero-value Provider (constructed directly, e.g. in tests): package fallback.
	p0 := &Provider{}
	first := p0.sophonDownloadClient()
	second := p0.sophonDownloadClient()
	if first == nil || second == nil {
		t.Fatalf("&Provider{}.sophonDownloadClient() returned nil (%p, %p), want the package fallback", first, second)
	}
	if first != second {
		t.Errorf("fallback client differs between calls (%p vs %p); want the one stable package client", first, second)
	}
	if first.Timeout != 0 {
		t.Errorf("fallback client Timeout = %v, want 0", first.Timeout)
	}
	if p0.downloadClient != nil {
		t.Errorf("sophonDownloadClient() wrote the fallback back into p.downloadClient (%p); want p left untouched (data race)", p0.downloadClient)
	}

	// p.httpClient is the test seam and wins over both.
	seam := &http.Client{}
	ps := New(Settings{}, nil)
	ps.httpClient = seam
	if got := ps.sophonDownloadClient(); got != seam {
		t.Errorf("sophonDownloadClient() = %p, want the p.httpClient seam (%p)", got, seam)
	}
}
