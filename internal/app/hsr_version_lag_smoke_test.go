package app

// Live smoke for the versionNewer fix, exploiting the real HSR rollover-lag
// window (2026-09-05: local 4.5.0 applied by HoYoPlay, API main still 4.4.0).
// Run manually while the window lasts:
//
//	$env:OMNIGATE_E2E_HSR="1"; $env:CGO_ENABLED="0"
//	go test ./internal/app/ -run TestHSRVersionLagSmoke -v

import (
	"log/slog"
	"os"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse"
)

func TestHSRVersionLagSmoke(t *testing.T) {
	if os.Getenv("OMNIGATE_E2E_HSR") != "1" {
		t.Skip("set OMNIGATE_E2E_HSR=1 to run (needs network + local HSR install)")
	}
	gid := core.GameID("hoyoverse/starrail")
	p := hoyoverse.New(hoyoverse.Settings{}, slog.Default())
	// Outside the real app the gameDirFn seam is unwired and DetectInstall may
	// miss the install → currentLocal would fall back to "" (vacuous test).
	// Point it at the real install; skip if this machine doesn't have it.
	const hsrDir = `C:\Program Files\HoYoPlay\games\Star Rail Games`
	if _, err := os.Stat(hsrDir); err != nil {
		t.Skipf("HSR install not found at %s", hsrDir)
	}
	p.SetGameDirFn(func(g core.GameID) (string, error) {
		if g == gid {
			return hsrDir, nil
		}
		return "", os.ErrNotExist
	})

	vi, err := p.CheckVersion(t.Context(), gid)
	if err != nil {
		t.Fatalf("CheckVersion: %v", err)
	}
	t.Logf("live VersionInfo: current=%q latest=%q", vi.Current, vi.Latest)
	if vi.Current == "" {
		t.Skip("local HSR version not detected on this machine — cannot exercise the lag case")
	}
	if vi.Current == vi.Latest {
		t.Skipf("API already rolled to %s — the lag window closed; comparator covered by TestVersionNewer", vi.Latest)
	}

	a := newAppWithProvider(p)
	defer a.updateRegistry.emitter.Stop()
	state := a.updateRegistry.Get(gid)
	state.AvailableUpdate = &core.UpdatePlan{Version: "stale"}
	if err := a.CheckForUpdate(string(gid)); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if versionNewer(vi.Latest, vi.Current) {
		// genuinely behind — the update flag is correct; nothing lag-specific to assert
		if state.AvailableUpdate == nil {
			t.Fatal("behind server but AvailableUpdate nil")
		}
		t.Logf("local genuinely behind (%s < %s); update flag correctly set", vi.Current, vi.Latest)
		return
	}
	if state.AvailableUpdate != nil {
		t.Fatalf("AvailableUpdate = %+v — the lag window must NOT flag an update (current=%s latest=%s)",
			state.AvailableUpdate, vi.Current, vi.Latest)
	}
	t.Logf("lag window handled: current=%s > latest=%s, no update flagged, stale cleared", vi.Current, vi.Latest)
}
