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
