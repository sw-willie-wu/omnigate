package kurogames

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"omnigate/internal/core"
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
	out := filterChangedFiles(context.Background(), tmp, "https://cdn.example/", "base/", files, nil, nil)
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

// TestFilterChangedFiles_CarriesChunks (T12): manifest chunkInfos must be
// carried into FileTask.Chunks, and the chunk layout must satisfy the
// contract the range-resume path depends on: end is INCLUSIVE, chunks tile
// the file exactly — Σ(end−start+1) == size.
func TestFilterChangedFiles_CarriesChunks(t *testing.T) {
	tmp := t.TempDir()
	files := []manifestFileRaw{
		{
			Dest: "big.pak", MD5: "abc", Size: 250,
			ChunkInfos: []chunkInfo{
				{Start: 0, End: 99, MD5: "c0"},
				{Start: 100, End: 199, MD5: "c1"},
				{Start: 200, End: 249, MD5: "c2"},
			},
		},
		{Dest: "small.dll", MD5: "def", Size: 10}, // no chunks
	}
	out := filterChangedFiles(context.Background(), tmp, "https://cdn.example/", "base/", files, nil, nil)
	if len(out) != 2 {
		t.Fatalf("got %d files, want 2", len(out))
	}
	byPath := map[string]core.FileTask{}
	for _, f := range out {
		byPath[f.Path] = f
	}

	big := byPath["big.pak"]
	if len(big.Chunks) != 3 {
		t.Fatalf("big.pak Chunks = %d, want 3", len(big.Chunks))
	}
	var sum int64
	for i, c := range big.Chunks {
		if c.End < c.Start {
			t.Errorf("chunk %d: end %d < start %d", i, c.End, c.Start)
		}
		sum += c.End - c.Start + 1
	}
	if sum != big.Size {
		t.Errorf("Σ(end−start+1) = %d, want %d (end must be inclusive and chunks must tile the file)", sum, big.Size)
	}
	if big.Chunks[0].Hash != "c0" || big.Chunks[2].Hash != "c2" {
		t.Errorf("chunk hashes not carried: %+v", big.Chunks)
	}

	if len(byPath["small.dll"].Chunks) != 0 {
		t.Errorf("small.dll should have no chunks: %+v", byPath["small.dll"].Chunks)
	}
}

// TestLocalFileMD5s (Task 4): generic parallel local-hash helper extracted
// from filterChangedFiles. Covers missing files (result ""), size pre-check
// short-circuit (result "" without hashing), and progress-callback contract
// (done strictly increasing, offset by progressBase, total pinned to the
// caller-supplied progressTotal for multi-batch merging).
func TestLocalFileMD5s(t *testing.T) {
	tmp := t.TempDir()
	aPath := filepath.Join(tmp, "a")
	if err := os.WriteFile(aPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bPath := filepath.Join(tmp, "b")
	if err := os.WriteFile(bPath, []byte("yy"), 0o644); err != nil {
		t.Fatal(err)
	}
	// c intentionally not created (missing file case).

	wantA, err := md5File(aPath)
	if err != nil {
		t.Fatal(err)
	}
	wantB, err := md5File(bPath)
	if err != nil {
		t.Fatal(err)
	}

	rels := []string{"a", "b", "c"}
	sizes := []int64{1, -1, 5}

	var (
		progressMu sync.Mutex
		progress   [][2]int
	)
	onProgress := func(done, total int) {
		progressMu.Lock()
		progress = append(progress, [2]int{done, total})
		progressMu.Unlock()
	}

	got := localFileMD5s(context.Background(), tmp, rels, sizes, 7, 10, onProgress)
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0] != wantA {
		t.Errorf("result[0] = %q, want %q (md5 of \"x\")", got[0], wantA)
	}
	if got[1] != wantB {
		t.Errorf("result[1] = %q, want %q (md5 of \"yy\")", got[1], wantB)
	}
	if got[2] != "" {
		t.Errorf("result[2] = %q, want \"\" (missing file)", got[2])
	}

	if len(progress) != 3 {
		t.Fatalf("got %d progress callbacks, want 3: %+v", len(progress), progress)
	}
	prevDone := 7 // progressBase
	for _, p := range progress {
		done, total := p[0], p[1]
		if total != 10 {
			t.Errorf("progress total = %d, want 10 (progressTotal, unchanged across callbacks)", total)
		}
		if done <= prevDone {
			t.Errorf("progress done = %d, not strictly increasing from previous %d", done, prevDone)
		}
		prevDone = done
	}
	if prevDone != 10 {
		t.Errorf("final done = %d, want 10 (progressBase 7 + 3 items)", prevDone)
	}

	// Size pre-check: mismatched size skips hashing entirely -> "".
	sizesMismatch := []int64{999, -1, 5}
	got2 := localFileMD5s(context.Background(), tmp, rels, sizesMismatch, 0, 3, nil)
	if got2[0] != "" {
		t.Errorf("size-mismatch result[0] = %q, want \"\" (999 != actual size 1)", got2[0])
	}
	if got2[1] != wantB {
		t.Errorf("size-mismatch case: result[1] = %q, want %q (size -1 always hashes)", got2[1], wantB)
	}
}

// TestParseIndexFile_PatchFields (Task 3): a real krpdiff patch indexFile
// (3.5.3->3.6.0, trimmed) must parse groupInfos/deleteFiles/applyTypes
// alongside resource, preserving the non-bijective src/dst shape of the
// multi-file group (deletes-only srcFiles, adds-only dstFiles alongside
// same-path pairs).
func TestParseIndexFile_PatchFields(t *testing.T) {
	body, err := os.ReadFile("testdata/indexfile_353_to_360_trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	var idxFile indexFileRaw
	if err := json.Unmarshal(body, &idxFile); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(idxFile.GroupInfos) != 4 {
		t.Errorf("GroupInfos = %d, want 4", len(idxFile.GroupInfos))
	}
	if len(idxFile.ApplyTypes) != 1 || idxFile.ApplyTypes[0] != "group" {
		t.Errorf("ApplyTypes = %+v, want [group]", idxFile.ApplyTypes)
	}
	if len(idxFile.DeleteFiles) != 4 {
		t.Errorf("DeleteFiles = %d, want 4", len(idxFile.DeleteFiles))
	}

	if len(idxFile.Resource) < 5 {
		t.Fatalf("Resource = %d, want >= 5", len(idxFile.Resource))
	}
	var withChunks, withoutChunks *manifestFileRaw
	for i := range idxFile.Resource {
		r := &idxFile.Resource[i]
		if !strings.HasSuffix(r.Dest, ".krpdiff") {
			continue
		}
		if r.ChunkInfos != nil && withChunks == nil {
			withChunks = r
		}
		if r.ChunkInfos == nil && withoutChunks == nil {
			withoutChunks = r
		}
	}
	if withChunks == nil {
		t.Error("expected at least one krpdiff resource entry with ChunkInfos != nil")
	}
	if withoutChunks == nil {
		t.Error("expected at least one krpdiff resource entry with ChunkInfos == nil")
	}
}
