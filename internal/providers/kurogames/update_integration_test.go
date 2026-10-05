package kurogames

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"omnigate/internal/core"
)

// TestUpdate_HappyPath_E2E: drives downloader + applier through a synthetic
// CDN. Does NOT exercise CheckForUpdate's two-step manifest fetch (covered
// by update_v3_test.go / update_krpdiff_e2e_test.go). This integration test
// validates that the plan-driven download → apply pipeline reaches gameDir
// with the correct file contents.
func TestUpdate_HappyPath_E2E(t *testing.T) {
	body1 := "content-of-a"
	body2 := "content-of-b"
	hash1 := md5hex(body1) // MD5 per kurogames manifest format
	hash2 := md5hex(body2)

	mux := http.NewServeMux()
	mux.HandleFunc("/files/a", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body1)) })
	mux.HandleFunc("/files/b", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body2)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := ps.Init(`"e1"`); err != nil {
		t.Fatal(err)
	}

	plan := core.UpdatePlan{
		GameID:       "kurogames/wutheringwaves",
		Kind:         core.PlanUpdate,
		ManifestETag: `"e1"`,
		Version:      "3.4.0",
		Files: []core.FileTask{
			{Path: "a.dll", Hash: hash1, Size: int64(len(body1)), URL: srv.URL + "/files/a"},
			{Path: "Engine/b.dll", Hash: hash2, Size: int64(len(body2)), URL: srv.URL + "/files/b"},
		},
		TotalBytes: int64(len(body1) + len(body2)),
	}

	var events atomic.Int64
	onEvent := func(e core.UpdateEvent) { events.Add(1) }

	d := &downloader{
		client: srv.Client(), logger: testLogger(), progress: ps,
		plan: &plan, onEvent: onEvent, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("download: %v", err)
	}

	a := &applier{
		logger: testLogger(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: &plan, onEvent: onEvent, lock: newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Files moved to gameDir
	for _, f := range plan.Files {
		if _, err := os.Stat(filepath.Join(gameDir, f.Path)); err != nil {
			t.Errorf("file missing in gameDir: %s — %v", f.Path, err)
		}
	}
	// WAL deleted on success
	if _, err := os.Stat(filepath.Join(ps.dir(), "apply.wal")); err == nil {
		t.Errorf("apply.wal should be deleted on success")
	}
	if events.Load() == 0 {
		t.Errorf("no events emitted")
	}
}

// TestRunUpdate_ETagDriftRejects validates RunUpdate's entry re-verify
// (spec §2.1): a plan built against one v3 game index must be rejected with
// manifest_changed (retryable) when a target pack's version/indexFileMd5
// changes before RunUpdate starts.
func TestRunUpdate_ETagDriftRejects(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	common := tinyIndexFile(t, dir, map[string]string{"Client/Content/Paks/a.pak": "a"})
	srv := newV3Server(t, map[string][]byte{"common": common}, nil)
	p := newV3TestProvider(t, dir)
	plan, err := p.CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Server publishes a new common pack after the plan was built.
	c := srv.idx.ResourcePacks["common"]
	c.Version, c.IndexFileMD5 = "3.7.1", "ffffffffffffffffffffffffffffffff"
	srv.idx.ResourcePacks["common"] = c

	err = p.RunUpdate(context.Background(), plan, nil)
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "manifest_changed" || !ue.Retryable {
		t.Fatalf("err=%v, want retryable manifest_changed", err)
	}
	if ue.Params["old_etag"] != plan.ManifestETag || ue.Params["new_etag"] == plan.ManifestETag {
		t.Fatalf("params=%v", ue.Params)
	}
}

// testLogger returns a slog.Logger that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestPredl_HappyPath: predownload completes, predl_ready.json written,
// game dir untouched.
func TestPredl_HappyPath(t *testing.T) {
	body := "predl-content"
	hash := md5hex(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := ps.Init(`"e1"`); err != nil {
		t.Fatal(err)
	}
	plan := core.UpdatePlan{
		GameID:       "kurogames/wutheringwaves",
		Kind:         core.PlanPredownload,
		ManifestETag: `"e1"`,
		Version:      "3.5.0",
		Files:        []core.FileTask{{Path: "next.dll", Hash: hash, Size: int64(len(body)), URL: srv.URL}},
	}
	d := &downloader{client: srv.Client(), logger: testLogger(), progress: ps, plan: &plan, clock: fakeRetryClock{}}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ps.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(gameDir)
	if len(entries) != 0 {
		t.Errorf("game dir touched during predl: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(ps.dir(), "predl_ready.json")); err != nil {
		t.Errorf("predl_ready.json missing")
	}
}

// TestApplyPredl_HappyPath: from predl_ready.json → ApplyPredownload →
// rename to apply.wal → apply phase → completion.
func TestApplyPredl_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wutheringwaves", "3.5.0")
	if err := ps.Init(`"e1"`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "next.dll"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("next.dll", time.Now(), 4, "hash-next"); err != nil {
		t.Fatal(err)
	}
	if err := ps.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	predlPath := filepath.Join(ps.dir(), "predl_ready.json")
	walPath := filepath.Join(ps.dir(), "apply.wal")
	wal := applyWAL{
		GameID:   "kurogames/wutheringwaves",
		Version:  "3.5.0",
		ETag:     `"e1"`,
		WasPredl: true,
		Pending:  []string{"next.dll"},
	}
	body, _ := json.Marshal(&wal)
	_ = os.Remove(predlPath)
	if err := os.WriteFile(walPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := resumeApply(context.Background(), walPath, gameDir, newApplyLock(), nil, slog.Default()); err != nil {
		t.Fatalf("resumeApply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "next.dll")); err != nil {
		t.Errorf("next.dll missing in gameDir: %v", err)
	}
}
