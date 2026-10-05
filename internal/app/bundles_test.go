package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestInstallBundle_Gates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *App, f *fakeBundleProv)
		arg   string
		code  string
	}{
		{"unknown", nil, "4K", "bundle_unknown"},
		{"running", func(a *App, f *fakeBundleProv) { f.running = true }, "SD", "process_blocked"},
		{"interrupted", func(a *App, f *fakeBundleProv) {
			a.setLastError(gid, &core.UpdateError{Code: "interrupted_resume"})
		}, "SD", "bundle_busy"},
		{"no record", func(a *App, f *fakeBundleProv) { f.st.Installed = map[string]string{} }, "SD", "install_record_missing"},
		{"pending", func(a *App, f *fakeBundleProv) { f.st.Pending = []string{"SD"} }, "SD", "bundle_pending"},
		{"installed", nil, "HD", "bundle_installed"},
		{"update first", func(a *App, f *fakeBundleProv) { f.version = core.VersionInfo{Current: "3.6.1", Latest: "3.7.0"} }, "SD", "bundle_update_first"},
		{"permission", func(a *App, f *fakeBundleProv) { probeGameDirWritable = func(string) error { return os.ErrPermission } }, "SD", "permission_denied"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, f := newBundleApp(t) // HD installed @3.7.0, latest 3.7.0
			if c.setup != nil {
				c.setup(a, f)
			}
			st, err := a.InstallBundle(string(gid), c.arg)
			if err != nil || st.Error == nil || st.Error.Code != c.code {
				t.Fatalf("st.Error=%v err=%v", st.Error, err)
			}
			if snap := a.updateRegistry.Get(gid).Snapshot(); snap.InFlight != nil {
				t.Fatal("gate failure must not leave in-flight")
			}
			if c.code == "permission_denied" || c.code == "process_blocked" {
				if snap := a.updateRegistry.Get(gid).Snapshot(); snap.LastError != nil && snap.LastError.Code == c.code {
					t.Fatal("bundle-mode sync errors must not setLastError")
				}
			}
		})
	}
}

func TestInstallBundle_NonInterruptedLastErrorDoesNotBlockAndIsCleared(t *testing.T) {
	a, f := newBundleApp(t)
	f.planGate = make(chan struct{})
	defer waitIdle(t, a, gid)
	defer close(f.planGate) // deferred LIFO: close first, then wait for the worker
	a.setLastError(gid, &core.UpdateError{Code: "network"})
	st, _ := a.InstallBundle(string(gid), "SD")
	if st.Error != nil {
		t.Fatalf("unexpected gate: %v", st.Error)
	}
	snap := a.updateRegistry.Get(gid).Snapshot()
	if snap.LastError != nil || snap.InFlight == nil || snap.InFlight.Bundle != "SD" || snap.InFlight.Stage != "verifying" {
		t.Fatalf("snap=%+v", snap)
	}
}

func TestInstallBundle_DoubleClickIsBusy(t *testing.T) { // Review Focus #4
	a, f := newBundleApp(t)
	f.planGate = make(chan struct{})
	defer waitIdle(t, a, gid)
	defer close(f.planGate) // deferred LIFO: close first, then wait for the worker
	if st, _ := a.InstallBundle(string(gid), "SD"); st.Error != nil {
		t.Fatal(st.Error)
	}
	st, _ := a.InstallBundle(string(gid), "SD")
	if st.Error == nil || st.Error.Code != "bundle_busy" || st.Error.Params["reason"] == "residual" {
		t.Fatalf("second click: %v", st.Error)
	}
	if f.planCalls.Load() > 1 {
		t.Fatal("second worker started")
	}
}

func TestInstallBundle_SuccessSetsActiveKeepsAvailableUpdate(t *testing.T) {
	a, f := newBundleApp(t)
	f.plan = core.UpdatePlan{Kind: core.PlanUpdate, Version: "3.7.0"}
	sentinel := &core.UpdatePlan{Version: "9.9.9"}
	a.updateRegistry.Get(gid).mu.Lock()
	a.updateRegistry.Get(gid).AvailableUpdate = sentinel
	a.updateRegistry.Get(gid).mu.Unlock()
	_, _ = a.InstallBundle(string(gid), "SD")
	waitIdle(t, a, gid)
	if got := f.activeSnapshot(); len(got) != 1 || got[0] != "SD" {
		t.Fatalf("activeSet=%v", got)
	}
	if a.updateRegistry.Get(gid).Snapshot().AvailableUpdate == nil {
		t.Fatal("bundle install must not clear AvailableUpdate")
	}
}

func TestInstallBundle_ResidualOtherBundleIsBusy(t *testing.T) {
	a, f := newBundleApp(t)
	dir := a.sidecarVersionDir(f, gid, "3.7.0")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"game_id":"x","version":"3.7.0","etag":"t","bundle":"UHD","entries":{}}`), 0o644)
	st, _ := a.InstallBundle(string(gid), "SD")
	if st.Error == nil || st.Error.Code != "bundle_busy" || st.Error.Params["reason"] != "residual" || st.Error.Params["bundle"] != "UHD" {
		t.Fatalf("st.Error=%v", st.Error)
	}
	// 同一 bundle → 放行
	f.planGate = make(chan struct{})
	defer waitIdle(t, a, gid)
	defer close(f.planGate)
	if st, _ := a.InstallBundle(string(gid), "UHD"); st.Error != nil {
		t.Fatalf("same bundle should resume: %v", st.Error)
	}
}

func TestRemoveBundle_Gates(t *testing.T) {
	a, f := newBundleApp(t)
	f.st = core.BundleInstallState{Active: "HD", Installed: map[string]string{"HD": "3.7.0"}}
	if st, _ := a.RemoveBundle(string(gid), "HD"); st.Error == nil || st.Error.Code != "bundle_in_use" {
		t.Fatalf("%v", st.Error)
	}
	f.st.Active = "SD" // 非 active 但唯一
	if st, _ := a.RemoveBundle(string(gid), "HD"); st.Error == nil || st.Error.Code != "bundle_last" {
		t.Fatalf("%v", st.Error)
	}
	f.st = core.BundleInstallState{Active: "HD", Installed: map[string]string{"HD": "3.7.0", "SD": "3.7.0"}}
	st, _ := a.RemoveBundle(string(gid), "SD")
	if st.Error != nil || len(f.removed) != 1 {
		t.Fatalf("st=%+v removed=%v", st, f.removed)
	}
}

func TestGetBundleState_Unsupported(t *testing.T) {
	a := newAppWithPlainProvider(t) // 不實作 BundleManager 的 fake
	st, err := a.GetBundleState(string(gid))
	if err != nil || st.Supported {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestGetBundleState_CatalogStale(t *testing.T) {
	a, f := newBundleApp(t)
	f.catFound = true
	f.cat = core.BundleCatalog{FetchedAt: a.startedAt.Add(-time.Hour)}
	if st, _ := a.GetBundleState(string(gid)); !st.CatalogStale {
		t.Fatal("catalog older than startedAt must be stale")
	}
	f.cat.FetchedAt = a.startedAt.Add(time.Second)
	if st, _ := a.GetBundleState(string(gid)); st.CatalogStale {
		t.Fatal("fresh catalog must not be stale")
	}
	f.catFound = false // Review Focus #5: missing/corrupt catalog reads as not found
	if st, _ := a.GetBundleState(string(gid)); !st.CatalogStale || len(st.Options) != 0 {
		t.Fatalf("missing catalog: stale=%v options=%v", st.CatalogStale, st.Options)
	}
}

func TestDiscardInterrupted(t *testing.T) {
	t.Run("interrupted", func(t *testing.T) {
		a, f := newBundleApp(t)
		dir := a.sidecarVersionDir(f, gid, "3.7.0")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"game_id":"x","version":"3.7.0","etag":"t","bundle":"SD","entries":{}}`), 0o644)
		a.applyRecoveryState(gid, dir)
		if err := a.DiscardInterrupted(string(gid)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) || a.updateRegistry.Get(gid).Snapshot().LastError != nil {
			t.Fatal("dir or LastError not cleared")
		}
	})
	t.Run("residual without LastError", func(t *testing.T) {
		a, f := newBundleApp(t) // HD@3.7.0 → residual dir = 3.7.0
		dir := a.sidecarVersionDir(f, gid, "3.7.0")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"game_id":"x","version":"3.7.0","etag":"t","bundle":"SD","entries":{}}`), 0o644)
		a.setLastError(gid, &core.UpdateError{Code: "network"})
		if err := a.DiscardInterrupted(string(gid)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) || a.updateRegistry.Get(gid).Snapshot().LastError != nil {
			t.Fatal("residual not discarded")
		}
	})
	t.Run("nothing to discard", func(t *testing.T) {
		a, _ := newBundleApp(t)
		if err := a.DiscardInterrupted(string(gid)); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("rejects path traversal", func(t *testing.T) {
		a, _ := newBundleApp(t)
		a.setLastError(gid, &core.UpdateError{Code: "interrupted_resume", Params: map[string]string{"version": "..\\.."}})
		if err := a.DiscardInterrupted(string(gid)); err == nil {
			t.Fatal("want error for bad version")
		}
	})
}

// writeBundleSidecar leaves an interrupted download sidecar for bundle at
// version 3.7.0 and loads it into LastError like startup recovery does.
func writeBundleSidecar(t *testing.T, a *App, f *fakeBundleProv, bundle string) string {
	t.Helper()
	dir := a.sidecarVersionDir(f, gid, "3.7.0")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"game_id":"x","version":"3.7.0","etag":"t","bundle":"`+bundle+`","entries":{}}`), 0o644)
	a.applyRecoveryState(gid, dir)
	return dir
}

// Final review I-1(a): a non-admin resume of an interrupted bundle install
// must keep the bundle on permission_denied so the UI relaunches with
// --elevate-install-bundle, not --elevate-update.
func TestResumeInterrupted_BundlePermissionDeniedCarriesBundle(t *testing.T) {
	a, f := newBundleApp(t)
	writeBundleSidecar(t, a, f, "SD")
	probeGameDirWritable = func(string) error { return os.ErrPermission }
	if err := a.ResumeInterrupted(string(gid)); err != nil {
		t.Fatal(err)
	}
	le := a.updateRegistry.Get(gid).Snapshot().LastError
	if le == nil || le.Code != "permission_denied" || le.Params["bundle"] != "SD" {
		t.Fatalf("LastError = %+v", le)
	}
}

// Final review I-1(b): the elevated instance's auto InstallBundle for the
// interrupted bundle resumes it instead of failing bundle_busy.
func TestInstallBundle_SameInterruptedBundleResumes(t *testing.T) {
	a, f := newBundleApp(t)
	f.plan = core.UpdatePlan{Kind: core.PlanUpdate, Version: "3.7.0", ManifestETag: "t"}
	writeBundleSidecar(t, a, f, "SD")
	st, err := a.InstallBundle(string(gid), "SD")
	if err != nil || st.Error != nil {
		t.Fatalf("st.Error=%v err=%v", st.Error, err)
	}
	waitIdle(t, a, gid)
	if f.planCalls.Load() != 1 || f.checkForUpdateCalls() != 0 {
		t.Fatalf("planCalls=%d generic=%d", f.planCalls.Load(), f.checkForUpdateCalls())
	}
	// A different bundle is still blocked.
	a2, f2 := newBundleApp(t)
	writeBundleSidecar(t, a2, f2, "UHD")
	if st, _ := a2.InstallBundle(string(gid), "SD"); st.Error == nil || st.Error.Code != "bundle_busy" {
		t.Fatalf("other bundle: %v", st.Error)
	}
}

// Final review I-1(c): a plain update whose plan lands on a bundle's
// interrupted sidecar must not overwrite it; the interrupted prompt returns.
func TestStartUpdate_DoesNotClobberBundleSidecar(t *testing.T) {
	a, f := newBundleApp(t)
	f.updatePlan = core.UpdatePlan{Kind: core.PlanUpdate, Version: "3.7.0", ManifestETag: "other"}
	dir := writeBundleSidecar(t, a, f, "SD")
	if err := a.StartUpdate(string(gid)); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, a, gid)
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err != nil {
		t.Fatal("bundle sidecar was removed")
	}
	le := a.updateRegistry.Get(gid).Snapshot().LastError
	if le == nil || le.Code != "interrupted_resume" || le.Params["bundle"] != "SD" {
		t.Fatalf("LastError = %+v", le)
	}
	if f.runUpdateCalls() != 0 {
		t.Fatal("RunUpdate must not run over a bundle sidecar")
	}
}

// Final review I-2: removing a bundle without write access reports
// permission_denied (with the bundle and op) instead of bundle_remove_failed.
func TestRemoveBundle_PermissionDenied(t *testing.T) {
	a, f := newBundleApp(t)
	f.st = core.BundleInstallState{Active: "HD", Installed: map[string]string{"HD": "3.7.0", "SD": "3.7.0"}}
	probeGameDirWritable = func(string) error { return os.ErrPermission }
	st, _ := a.RemoveBundle(string(gid), "SD")
	if st.Error == nil || st.Error.Code != "permission_denied" || st.Error.Params["bundle"] != "SD" || st.Error.Params["op"] != "remove" {
		t.Fatalf("st.Error=%+v", st.Error)
	}
	if len(f.removed) != 0 {
		t.Fatal("provider RemoveBundle must not run")
	}
}
