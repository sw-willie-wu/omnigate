package kurogames

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{
			"https://prod-alicdn-gamestarter.kurogame.com/launcher/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/G153/manifest/3.4.0.json",
			"https://prod-alicdn-gamestarter.kurogame.com/launcher/<ACCOUNT_ID>/G153/manifest/3.4.0.json",
		},
		{
			"https://x.com/?token=" + strings.Repeat("a", 32),
			"https://x.com/?token=<DEVICE_ID>",
		},
		{
			"https://no-pii.example.com/foo",
			"https://no-pii.example.com/foo",
		},
	}
	for _, tc := range cases {
		got := sanitizeURL(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFetchIndex_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, _, err := fetchIndex(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "manifest_not_found") {
		t.Errorf("err = %v, want manifest_not_found", err)
	}
}

func TestFetchIndex_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, _, err := fetchIndex(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "auth_failed") {
		t.Errorf("err = %v, want auth_failed", err)
	}
}

func TestFetchIndex_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, _, err := fetchIndex(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
}

func TestFetchIndex_OK_AndETagFallback(t *testing.T) {
	body := `{"default":{"version":"3.3.0","cdnList":[{"url":"https://cdn.example/","P":0,"K1":1,"K2":1}],"config":{"version":"3.3.0","indexFile":"a/indexFile.json","indexFileMd5":"abc","baseUrl":"a/zip/","size":100,"patchType":"patch","patchConfig":[{"version":"3.2.2","indexFile":"a/3.2.2/indexFile.json","baseUrl":"a/3.2.2/resources/","size":50}]}},"predownloadSwitch":1,"keyFileCheckList":["Wuthering Waves.exe"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "Wed, 29 Apr 2026 20:10:00 GMT")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	idx, etag, err := fetchIndex(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if etag != "Wed, 29 Apr 2026 20:10:00 GMT" {
		t.Errorf("etag = %q, want Last-Modified value", etag)
	}
	if idx.Default.Version != "3.3.0" {
		t.Errorf("version = %q", idx.Default.Version)
	}
	if idx.Default.Config.PatchType != "patch" || len(idx.Default.Config.PatchConfig) != 1 {
		t.Errorf("patchConfig not parsed: %+v", idx.Default.Config)
	}
	if idx.Default.Config.PatchConfig[0].Version != "3.2.2" {
		t.Errorf("patch version: %q", idx.Default.Config.PatchConfig[0].Version)
	}
	if len(idx.KeyFileCheckList) != 1 || idx.KeyFileCheckList[0] != "Wuthering Waves.exe" {
		t.Errorf("keyFileCheckList: %+v", idx.KeyFileCheckList)
	}
}

func TestFetchIndexFile_OK_AndETag(t *testing.T) {
	body := `{"resource":[{"dest":"Wuthering Waves.exe","md5":"abc","size":1},{"dest":"big.pak","md5":"def","size":200000000,"chunkInfos":[{"start":0,"end":104857599,"md5":"chunk1"},{"start":104857600,"end":199999999,"md5":"chunk2"}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc-md5"`)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	idxFile, etag, err := fetchIndexFile(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if etag != `"abc-md5"` {
		t.Errorf("etag = %q", etag)
	}
	if len(idxFile.Resource) != 2 {
		t.Fatalf("resource count = %d, want 2", len(idxFile.Resource))
	}
	if idxFile.Resource[0].Dest != "Wuthering Waves.exe" || idxFile.Resource[0].MD5 != "abc" {
		t.Errorf("entry 0: %+v", idxFile.Resource[0])
	}
	if len(idxFile.Resource[1].ChunkInfos) != 2 {
		t.Errorf("chunkInfos: %+v", idxFile.Resource[1].ChunkInfos)
	}
}

func TestPickCDN_LowestPWins(t *testing.T) {
	list := []cdnEntry{
		{URL: "https://akamai.example/", P: 7857},
		{URL: "https://qcloud.example/", P: 0},
		{URL: "https://aws.example/", P: 0},
		{URL: "https://aliyun.example/", P: 2205},
	}
	got := pickCDN(list)
	// P=0 ties broken by URL sort: aws < qcloud
	if got != "https://aws.example/" {
		t.Errorf("pickCDN = %q, want https://aws.example/", got)
	}
	if pickCDN(nil) != "https://hw-pcdownload-qcloud.aki-game.net/" {
		t.Errorf("empty pickCDN should return safe default")
	}
}

func TestPickIndexFileForVersion(t *testing.T) {
	idx := &indexRaw{}
	idx.Default.Config = indexConfigRaw{
		Version:   "3.3.0",
		IndexFile: "full/indexFile.json",
		PatchType: "patch",
		PatchConfig: []indexConfigRaw{
			{Version: "3.2.2", IndexFile: "patch/3.2.2/indexFile.json"},
			{Version: "3.0.0", IndexFile: "patch/3.0.0/indexFile.json"},
		},
	}

	cfg, isPatch := pickIndexFileForVersion(idx, "3.2.2")
	if !isPatch || cfg.IndexFile != "patch/3.2.2/indexFile.json" {
		t.Errorf("3.2.2 install: cfg = %+v, isPatch = %v", cfg, isPatch)
	}

	cfg, isPatch = pickIndexFileForVersion(idx, "1.5.0") // not in patchConfig
	if isPatch || cfg.IndexFile != "full/indexFile.json" {
		t.Errorf("unknown version: cfg = %+v, isPatch = %v", cfg, isPatch)
	}

	cfg, isPatch = pickIndexFileForVersion(idx, "") // fresh install
	if isPatch || cfg.IndexFile != "full/indexFile.json" {
		t.Errorf("empty version: cfg = %+v, isPatch = %v", cfg, isPatch)
	}
}

func TestFileURL_BaseAndFromFolder(t *testing.T) {
	cdn := "https://cdn.example/"
	base := "launcher/.../zip/"
	entry1 := manifestFileRaw{Dest: "Engine/foo.dll"}
	if got := fileURL(cdn, base, entry1); got != "https://cdn.example/launcher/.../zip/Engine/foo.dll" {
		t.Errorf("base case: %q", got)
	}
	// Spaces in dest → %20
	entry2 := manifestFileRaw{Dest: "Wuthering Waves.exe"}
	if got := fileURL(cdn, base, entry2); got != "https://cdn.example/launcher/.../zip/Wuthering%20Waves.exe" {
		t.Errorf("spaces case: %q", got)
	}
	// fromFolder overrides base
	entry3 := manifestFileRaw{Dest: "patched.pak", FromFolder: "patches/3.2.2/resources/"}
	if got := fileURL(cdn, base, entry3); got != "https://cdn.example/patches/3.2.2/resources/patched.pak" {
		t.Errorf("fromFolder case: %q", got)
	}
}

func TestFilterChangedFiles_SkipsIdentical(t *testing.T) {
	tmp := t.TempDir()
	identical := filepath.Join(tmp, "identical.dll")
	if err := os.WriteFile(identical, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := md5File(identical)
	if err != nil {
		t.Fatal(err)
	}
	differentSize := filepath.Join(tmp, "diff.dll")
	if err := os.WriteFile(differentSize, []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []manifestFileRaw{
		{Dest: "identical.dll", MD5: hash, Size: 5},
		{Dest: "missing.dll", MD5: "x", Size: 100},
		{Dest: "diff.dll", MD5: "x", Size: 999},
	}
	out := filterChangedFiles(tmp, "https://cdn.example/", "base/", files, nil)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2 (missing + size-diff); out = %+v", len(out), out)
	}
	got := map[string]bool{}
	for _, f := range out {
		got[f.Path] = true
	}
	if got["identical.dll"] {
		t.Error("identical file should be filtered out")
	}
	if !got["missing.dll"] || !got["diff.dll"] {
		t.Errorf("missing/diff not in output: %+v", got)
	}
	// URL constructed correctly
	for _, f := range out {
		if f.URL == "" || !strings.Contains(f.URL, "https://cdn.example/base/") {
			t.Errorf("URL not constructed: %+v", f)
		}
	}
}
