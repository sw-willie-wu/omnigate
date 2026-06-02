package hypergryph

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// buildEndfieldTestServer serves get_latest (with pkg.file_path → this server),
// /files/game_files (AES-encrypted JSON-lines), and /files/<path> payloads.
func buildEndfieldTestServer(t *testing.T, latestVersion string, files map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/game/get_latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"action":1,"version":%q,"request_version":"","pkg":{"file_path":%q,"game_files_md5":""}}`,
			latestVersion, srv.URL+"/files")
	})
	// game_files manifest (encrypted JSON-lines).
	var sb strings.Builder
	for p, b := range files {
		h := md5.Sum(b)
		fmt.Fprintf(&sb, "{\"path\":%q,\"md5\":%q,\"size\":%d}\n", p, hex.EncodeToString(h[:]), len(b))
	}
	enc, err := encryptAESCBC([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/files/game_files", func(w http.ResponseWriter, r *http.Request) {
		w.Write(enc)
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/files/")
		b, ok := files[rel]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write(b)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegration_CheckForUpdate_FiltersChangedFiles(t *testing.T) {
	game := t.TempDir()
	// config.ini at old version 1.2.5
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\n"))
	os.WriteFile(filepath.Join(game, "config.ini"), ct, 0o644)
	// "a.bin" already present + correct → should be filtered out.
	aData := []byte("aaa")
	os.MkdirAll(filepath.Join(game, "data"), 0o755)
	os.WriteFile(filepath.Join(game, "data/a.bin"), aData, 0o644)

	files := map[string][]byte{"data/a.bin": aData, "data/b.bin": []byte("bbbb")}
	srv := buildEndfieldTestServer(t, "1.2.6", files)
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	p := New(Settings{}, testLogger())
	// install path resolution is via DetectInstall; for this test we drive the
	// internal checkForUpdate against the known install path directly.
	plan, err := p.checkForUpdateAt(context.Background(), "hypergryph/endfield", game, nil)
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if plan.Version != "1.2.6" {
		t.Errorf("plan.Version = %q", plan.Version)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "data/b.bin" {
		t.Fatalf("expected only data/b.bin to need update, got %+v", plan.Files)
	}
}

// TestIntegration_RunUpdate_EndToEnd drives the FULL shipped wiring against
// httptest: DetectInstall → CheckForUpdateWithProgress → RunUpdate (download +
// apply + config.ini version writeback). Spec §9.2.
func TestIntegration_RunUpdate_EndToEnd(t *testing.T) {
	root := t.TempDir()
	gameDir := filepath.Join(root, "games", "EndField Game") // matches meta.go FolderName
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\n"))
	os.WriteFile(filepath.Join(gameDir, "config.ini"), ct, 0o644)
	os.WriteFile(filepath.Join(gameDir, "Endfield.exe"), []byte("stub"), 0o644) // DetectInstall requires the exe

	files := map[string][]byte{"data/b.bin": []byte("bbbb")}
	srv := buildEndfieldTestServer(t, "1.2.6", files)
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	// TempDir under root → same volume as gameDir (preflight passes).
	p := New(Settings{TempDir: filepath.Join(root, "_temp")}, testLogger())
	p.SetResolvedPaths(map[core.GameID]string{"hypergryph/endfield": gameDir})
	plan, err := p.CheckForUpdateWithProgress(context.Background(), "hypergryph/endfield", nil)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("expected 1 file, got %+v", plan.Files)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if b, rerr := os.ReadFile(filepath.Join(gameDir, "data/b.bin")); rerr != nil || string(b) != "bbbb" {
		t.Errorf("applied file = %q err=%v", b, rerr)
	}
	if v, _ := readLocalVersion(gameDir); v != "1.2.6" {
		t.Errorf("config.ini version after apply = %q, want 1.2.6", v)
	}
}

// TestCheckForUpdate_DegradeNoLocalVersion covers spec §3.1 degrade branch
// (curVer=="" → action==1 means update, action!=1 means up-to-date).
func TestCheckForUpdate_DegradeNoLocalVersion(t *testing.T) {
	game := t.TempDir() // no config.ini → curVer==""
	files := map[string][]byte{"data/b.bin": []byte("bbbb")}
	srv := buildEndfieldTestServer(t, "1.2.6", files) // action==1
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")
	p := New(Settings{}, testLogger())
	plan, err := p.checkForUpdateAt(context.Background(), "hypergryph/endfield", game, nil)
	if err != nil {
		t.Fatalf("check (action==1): %v", err)
	}
	if len(plan.Files) != 1 {
		t.Errorf("degrade + action==1 should yield an update, got %+v", plan.Files)
	}

	// action!=1 + no local version → up-to-date (empty plan, no manifest fetch).
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"action":0,"version":"1.2.6","request_version":"","pkg":{"file_path":"http://unused/files"}}`)
	}))
	defer srv2.Close()
	SetAPIBaseURL(srv2.URL)
	plan2, err := p.checkForUpdateAt(context.Background(), "hypergryph/endfield", game, nil)
	if err != nil {
		t.Fatalf("check (action!=1): %v", err)
	}
	if len(plan2.Files) != 0 {
		t.Errorf("degrade + action!=1 should be up-to-date, got %+v", plan2.Files)
	}
}
