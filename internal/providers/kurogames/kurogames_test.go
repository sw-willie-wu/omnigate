package kurogames

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"omnigate/internal/core"
)

func TestSetResolvedPaths_DetectInstallReturnsExisting(t *testing.T) {
	dir := t.TempDir() // exists
	gid := core.GameID("kurogames/wutheringwaves")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{
		gid:              dir,
		"kurogames/fake": filepath.Join(dir, "does-not-exist"),
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
	gid := core.GameID("kurogames/wutheringwaves")
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

// TestRunUpdate_AdoptsPredlStaged is Task 9 test 3 (spec §2.5 R3-B1): a
// Predownload RunUpdate stages files and renames progress.json to
// predl_ready.json; a SUBSEQUENT same-version Update RunUpdate must adopt
// those staged bytes via ConsumePredlStaged rather than re-downloading —
// asserted here by an httptest download counter that must stay at 2 (the
// initial predl download) across the apply run.
func TestRunUpdate_AdoptsPredlStaged(t *testing.T) {
	body1 := "content-of-x-file"
	body2 := "content-of-y-file"
	hash1 := md5hex(body1)
	hash2 := md5hex(body2)

	var downloadCount atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/files/x", func(w http.ResponseWriter, _ *http.Request) {
		downloadCount.Add(1)
		w.Write([]byte(body1))
	})
	mux.HandleFunc("/files/y", func(w http.ResponseWriter, _ *http.Request) {
		downloadCount.Add(1)
		w.Write([]byte(body2))
	})
	// RunUpdate re-verifies the manifest ETag at entry (spec §2.8) by
	// fetching index.json fresh. Serve the SAME ETag as the plan carries so
	// that re-check is a no-op (equal → no manifest_changed) rather than
	// hitting the real prod CDN from a test.
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"e1"`)
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origURL := indexJSONURL
	indexJSONURL = func() string { return srv.URL + "/index.json" }
	defer func() { indexJSONURL = origURL }()

	// RunUpdate's entry guard calls the real isProcessRunning (spec §2.7) —
	// must be hermetic to the actual game being open on the dev/CI machine
	// (same pattern as TestRunUpdate_ProcessGuardBlocksDuringPatchPhase).
	origIsProcessRunning := isProcessRunning
	isProcessRunning = func(string) bool { return false }
	t.Cleanup(func() { isProcessRunning = origIsProcessRunning })

	tmp := t.TempDir()
	gameDir := t.TempDir()
	gid := core.GameID("kurogames/wutheringwaves")

	p := New(Settings{TempDir: tmp}, testLogger())
	p.SetResolvedPaths(map[core.GameID]string{gid: gameDir})

	files := []core.FileTask{
		{Path: "x.dll", Hash: hash1, Size: int64(len(body1)), URL: srv.URL + "/files/x"},
		{Path: "y.dll", Hash: hash2, Size: int64(len(body2)), URL: srv.URL + "/files/y"},
	}
	predlPlan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanPredownload,
		ManifestETag: `"e1"`,
		Version:      "3.5.0",
		Files:        files,
		TotalBytes:   int64(len(body1) + len(body2)),
	}

	if err := p.RunUpdate(context.Background(), predlPlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}
	if got := downloadCount.Load(); got != 2 {
		t.Fatalf("after predl, download count = %d, want 2", got)
	}

	updatePlan := predlPlan
	updatePlan.Kind = core.PlanUpdate

	if err := p.RunUpdate(context.Background(), updatePlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("apply RunUpdate: %v", err)
	}
	if got := downloadCount.Load(); got != 2 {
		t.Fatalf("after apply, download count = %d, want still 2 (staged bytes must be adopted, not re-downloaded)", got)
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(gameDir, f.Path)); err != nil {
			t.Errorf("file missing in gameDir: %s — %v", f.Path, err)
		}
	}
}

// TestRunUpdate_ConsumeBeforeInit_PreservesParts is review fix-round-1 F3:
// pins Consume-before-Init ordering via an observable side effect that a
// progress.json-content-only check would miss — removeStaleParts() (invoked
// by progress.Init's "no matching progress.json" branch) deletes EVERY
// *.part file under the version dir, unscoped to whatever ConsumePredlStaged
// is about to restore. A predl that completed successfully leaves ONLY
// predl_ready.json in the version dir (RenameToPredlReady moved
// progress.json away), so the SAME-version adopting RunUpdate's
// Init(newETag) would — if it ran BEFORE Consume — find no progress.json,
// take the "no match" branch, and wipe any stray .part sitting in that dir
// before Consume ever runs. Consume running first means progress.json
// already exists (stamped with the new ETag) by the time Init runs, so Init
// takes the "same ETag → preserve" branch and never calls
// removeStaleParts. A fake "x.pak.part" dropped into the version dir before
// the adopting run must therefore SURVIVE (a "Consume after Init" mutation
// must fail this test).
//
// gameDir is deliberately made UNCREATABLE (an ancestor path component is a
// plain file) so the apply phase's os.MkdirAll(dst's parent) fails and
// RunUpdate returns an error BEFORE reaching its post-apply SUCCESS cleanup
// (update_apply.go: os.RemoveAll(a.progress.dir()) on the happy path) —
// that unconditional whole-version-dir wipe would otherwise erase
// x.pak.part regardless of Consume/Init ordering and mask the very bug this
// test exists to catch. Init/Consume both already ran (at the top of
// RunUpdate, before download/apply) by the time apply fails.
func TestRunUpdate_ConsumeBeforeInit_PreservesParts(t *testing.T) {
	body := "predl-file-content"
	hash := md5hex(body)

	mux := http.NewServeMux()
	mux.HandleFunc("/files/a", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"e1"`)
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origURL := indexJSONURL
	indexJSONURL = func() string { return srv.URL + "/index.json" }
	defer func() { indexJSONURL = origURL }()

	// RunUpdate's entry guard calls the real isProcessRunning (spec §2.7) —
	// must be hermetic to the actual game being open on the dev/CI machine
	// (same pattern as TestRunUpdate_ProcessGuardBlocksDuringPatchPhase).
	origIsProcessRunning := isProcessRunning
	isProcessRunning = func(string) bool { return false }
	t.Cleanup(func() { isProcessRunning = origIsProcessRunning })

	tmp := t.TempDir()
	gid := core.GameID("kurogames/wutheringwaves")
	version := "3.5.0"

	p := New(Settings{TempDir: tmp}, testLogger())
	// blockerFile is a FILE (not a dir) at the path gameDir needs as an
	// ancestor, so the apply phase's os.MkdirAll(filepath.Dir(dst), ...)
	// fails deterministically ("not a directory") instead of silently
	// auto-creating gameDir, which os.MkdirAll would otherwise do for a
	// merely-missing (but creatable) directory.
	blockerFile := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gameDir := filepath.Join(blockerFile, "sub")
	p.SetResolvedPaths(map[core.GameID]string{gid: gameDir})

	predlPlan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanPredownload,
		ManifestETag: `"e1"`,
		Version:      version,
		Files:        []core.FileTask{{Path: "a.dll", Hash: hash, Size: int64(len(body)), URL: srv.URL + "/files/a"}},
		TotalBytes:   int64(len(body)),
	}
	// Predl download never touches gameDir, so this succeeds even though
	// gameDir doesn't exist.
	if err := p.RunUpdate(context.Background(), predlPlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}

	// Drop a fake, unrelated chunked-resume partial into the version dir —
	// simulating a leftover .part that must NOT be swept just because this
	// version's RunUpdate is also adopting a staged predl.
	versionDir := filepath.Join(tmp, "kurogames-wutheringwaves", version)
	partPath := filepath.Join(versionDir, "x.pak.part")
	if err := os.WriteFile(partPath, []byte("stray-chunk-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	updatePlan := predlPlan
	updatePlan.Kind = core.PlanUpdate
	// Apply is EXPECTED to fail (gameDir doesn't exist) — that's the point:
	// it keeps the success-path version-dir cleanup from running.
	if err := p.RunUpdate(context.Background(), updatePlan, func(core.UpdateEvent) {}); err == nil {
		t.Fatal("expected apply to fail against a nonexistent gameDir (test setup invariant)")
	}

	if _, err := os.Stat(partPath); err != nil {
		t.Fatalf("x.pak.part did not survive Init/Consume — Consume must run BEFORE Init (a reordering lets removeStaleParts sweep it): %v", err)
	}
}
