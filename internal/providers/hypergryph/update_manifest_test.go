package hypergryph

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGetLatest_FixtureHasVersion(t *testing.T) {
	body, err := os.ReadFile("testdata/get_latest-sample.json")
	if err != nil {
		t.Skipf("fixture missing (Task A1 spike): %v", err)
	}
	resp, err := decodeGetLatest(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version == "" {
		t.Fatal("expected non-empty target version")
	}
}

func TestFetchGetLatest_HitsServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appcode") != gameAppCode {
			t.Errorf("missing appcode: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": 1, "version": "1.2.5",
			"pkg": map[string]any{"packs": []any{}},
		})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	resp, err := fetchGetLatest(context.Background(), srv.Client(), "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if resp.Version != "1.2.5" {
		t.Fatalf("version: got %q", resp.Version)
	}
}

func TestSanitizeURL_RedactsRandSegment(t *testing.T) {
	in := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/x.zip.001"
	want := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_<RAND>/packs/x.zip.001"
	if got := sanitizeURL(in); got != want {
		t.Fatalf("sanitizeURL:\n got %q\nwant %q", got, want)
	}
}

func TestParseGameFilesManifest(t *testing.T) {
	plain := []byte(`{"path":"Endfield.exe","md5":"aaa","size":100}
{"path":"data/x.bundle","md5":"bbb","size":200}

{"path":"config.ini","md5":"ccc","size":50}
garbage-not-json
{"path":"data/y.bundle","md5":"ddd","size":300}`)
	nodes, err := parseGameFilesManifest(plain)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// config.ini skipped; blank + garbage lines skipped.
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3: %+v", len(nodes), nodes)
	}
	if nodes[0].Path != "Endfield.exe" || nodes[0].MD5 != "aaa" || nodes[0].Size != 100 {
		t.Errorf("node0 = %+v", nodes[0])
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Path, "config.ini") {
			t.Errorf("config.ini should be skipped")
		}
	}
}

func TestFileURL(t *testing.T) {
	got := fileURL("https://cdn.example/app/1.2/files", "data/a b.bundle")
	want := "https://cdn.example/app/1.2/files/data/a%20b.bundle"
	if got != want {
		t.Errorf("fileURL = %q, want %q", got, want)
	}
	// trailing slash on base is tolerated
	if got2 := fileURL("https://cdn.example/files/", "x"); got2 != "https://cdn.example/files/x" {
		t.Errorf("fileURL trailing slash = %q", got2)
	}
}

func TestFilterChangedFiles_GameFiles(t *testing.T) {
	dir := t.TempDir()
	// present + correct md5
	good := []byte("hello")
	goodMD5 := md5hexBytes(good)
	if err := os.WriteFile(filepath.Join(dir, "good.bin"), good, 0o644); err != nil {
		t.Fatal(err)
	}
	// present but wrong size
	if err := os.WriteFile(filepath.Join(dir, "stale.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes := []manifestNode{
		{Path: "good.bin", MD5: goodMD5, Size: int64(len(good))},
		{Path: "stale.bin", MD5: "deadbeef", Size: 999},
		{Path: "missing.bin", MD5: "f00d", Size: 42},
	}
	got := filterChangedFiles(context.Background(), dir, "https://cdn/files", nodes, nil, nil)
	// good.bin dropped; stale.bin + missing.bin kept; sorted by path.
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(got), got)
	}
	if got[0].Path != "missing.bin" || got[1].Path != "stale.bin" {
		t.Errorf("unexpected order/paths: %+v", got)
	}
	if got[1].URL != "https://cdn/files/stale.bin" {
		t.Errorf("URL = %q", got[1].URL)
	}
}

// md5hexBytes is a test helper (lowercase hex MD5 of b).
func md5hexBytes(b []byte) string {
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}
