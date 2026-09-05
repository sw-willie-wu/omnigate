package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"omnigate/internal/core"
)

// Task 10: offline end-to-end suite exercising the WHOLE kurogames update
// pipeline (Provider.CheckForUpdateWithProgress → Provider.RunUpdate)
// against an httptest CDN + the real testdata/krpdiff fixtures (Task 1.5),
// including a real hpatchz.exe subprocess invocation. See spec §7 and
// .superpowers/sdd/2026-08-24-wuwa-krpdiff-patch-update/task-10-brief.md
// for the case rationale.
//
// Shared helpers below build a synthetic two-step kurogames manifest
// (index.json → indexFile.json) and a small httptest-backed CDN file host,
// mirroring the real protocol closely enough that CheckForUpdateWithProgress
// / CheckForPredownload's actual parsing + buildFileAndPatchPlan
// classification run unmodified, then RunUpdate drives the real
// download/apply/patch code paths.

const (
	e2eGID           core.GameID = "kurogames/wutheringwaves"
	e2eLocalVersion              = "3.4.0" // launcherDownloadConfig.json's pre-update value
	e2eTargetVersion             = "3.5.0" // idx.Default.Version (live/update target)
	e2ePredlVersion              = "3.5.0" // idx.Predownload.Version (predl target)

	// chunkBPath mirrors chunkAPath (defined in update_apply_test.go) — the
	// game-dir-relative path the "b" krpdiff fixture pair embeds (see
	// testdata/krpdiff/README.md).
	chunkBPath = "Client/Content/Paks/chunk_b.pak"
)

// e2eFileServer is a tiny content-addressed CDN stand-in: files are keyed by
// their manifest `dest` (relative path, forward slashes) and served under a
// mounted URL prefix. hits() lets tests assert which route (patch vs full)
// actually served a given file — the crux of the fallback-routing cases.
type e2eFileServer struct {
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
}

func newE2EFileServer() *e2eFileServer {
	return &e2eFileServer{files: map[string][]byte{}, hits: map[string]int{}}
}

func (fs *e2eFileServer) set(dest string, content []byte) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.files[dest] = content
}

func (fs *e2eFileServer) hitCount(dest string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.hits[dest]
}

func (fs *e2eFileServer) mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		fs.mu.Lock()
		fs.hits[rel]++
		b, ok := fs.files[rel]
		fs.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
}

// e2eMutableJSON lets a test swap an httptest handler's JSON body between two
// RunUpdate/CheckForUpdate calls (case 3's "manifest corrected, re-run"
// step) without re-registering the mux pattern (http.ServeMux panics on a
// duplicate pattern).
type e2eMutableJSON struct {
	mu   sync.Mutex
	body []byte
}

func (m *e2eMutableJSON) set(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	m.mu.Lock()
	m.body = b
	m.mu.Unlock()
}

func (m *e2eMutableJSON) mount(mux *http.ServeMux, path string) {
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		b := m.body
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
}

// mountJSON registers a static (never-mutated) JSON body at path.
func mountJSON(mux *http.ServeMux, path string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

// patchRoute is one patchConfig entry in the synthetic index.json.
type patchRoute struct {
	fromVersion  string
	indexFileURL string
	baseURL      string
}

// e2eIndexOpts configures buildE2EIndexJSON's index.json body — a stand-in
// for the real kurogames launcher index.json two-tier (default +
// predownload) manifest.
type e2eIndexOpts struct {
	cdnURL string

	defaultVersion      string // idx.Default.Version
	defaultIndexFileURL string // idx.Default.Config.IndexFile (FULL/fresh-install manifest)
	defaultBaseURL      string // idx.Default.Config.BaseURL (FULL download base)
	defaultPatch        *patchRoute

	predlVersion      string // "" = no predownload section published
	predlIndexFileURL string
	predlBaseURL      string
	predlPatch        *patchRoute
}

func buildE2EIndexJSON(o e2eIndexOpts) []byte {
	defaultCfg := map[string]any{
		"version":   o.defaultVersion,
		"indexFile": o.defaultIndexFileURL,
		"baseUrl":   o.defaultBaseURL,
	}
	if o.defaultPatch != nil {
		defaultCfg["patchType"] = "patch"
		defaultCfg["patchConfig"] = []map[string]any{{
			"version":   o.defaultPatch.fromVersion,
			"indexFile": o.defaultPatch.indexFileURL,
			"baseUrl":   o.defaultPatch.baseURL,
			"patchType": "patch",
		}}
	}
	doc := map[string]any{
		"default": map[string]any{
			"version": o.defaultVersion,
			"cdnList": []map[string]any{{"url": o.cdnURL, "P": 0}},
			"config":  defaultCfg,
		},
	}
	if o.predlVersion != "" {
		predlCfg := map[string]any{
			"version":   o.predlVersion,
			"indexFile": o.predlIndexFileURL,
			"baseUrl":   o.predlBaseURL,
		}
		if o.predlPatch != nil {
			predlCfg["patchType"] = "patch"
			predlCfg["patchConfig"] = []map[string]any{{
				"version":   o.predlPatch.fromVersion,
				"indexFile": o.predlPatch.indexFileURL,
				"baseUrl":   o.predlPatch.baseURL,
				"patchType": "patch",
			}}
		}
		doc["predownload"] = map[string]any{
			"version": o.predlVersion,
			"cdnList": []map[string]any{{"url": o.cdnURL, "P": 0}},
			"config":  predlCfg,
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return body
}

func mountIndexJSON(mux *http.ServeMux, body []byte) {
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"idx-e2e"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

// mountEmptyFullManifest wires a defensive, always-valid but empty full
// (fresh-install) indexFile.json at the given path — none of this suite's 8
// cases actually needs the whole-plan fallback (fetchFull) to fire, but
// every synthetic index.json's Default.Config.IndexFile must resolve to
// *something* in case a latent bug causes an unexpected fallback; hitting
// this handler makes that bug loudly visible (empty resource list → the
// test's own assertions fail) instead of a nil-pointer panic.
func mountEmptyFullManifest(mux *http.ServeMux, path string) {
	mountJSON(mux, path, &indexFileRaw{})
}

// e2eEnv bundles one test case's httptest CDN: two file routes (patch-files
// for the patch manifest's own CDN/baseURL, full-files for the
// fresh-install/fallback CDN/baseURL — matching update_patchplan.go's
// cdn/baseURL vs fullCDN/fullBaseURL distinction) plus the shared mux/server.
type e2eEnv struct {
	mux     *http.ServeMux
	srv     *httptest.Server
	patchFS *e2eFileServer
	fullFS  *e2eFileServer
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	mux := http.NewServeMux()
	patchFS := newE2EFileServer()
	fullFS := newE2EFileServer()
	patchFS.mount(mux, "/patch-files/")
	fullFS.mount(mux, "/full-files/")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	origIdxURL := indexJSONURL
	t.Cleanup(func() { indexJSONURL = origIdxURL })
	indexJSONURL = func() string { return srv.URL + "/index.json" }

	origProcRunning := isProcessRunning
	t.Cleanup(func() { isProcessRunning = origProcRunning })
	isProcessRunning = func(string) bool { return false }

	return &e2eEnv{mux: mux, srv: srv, patchFS: patchFS, fullFS: fullFS}
}

// newE2EProvider builds a Provider wired to gameDir for e2eGID, with tempRoot
// tempDir (Settings.TempDir fallback — same pattern as
// TestRunUpdate_AdoptsPredlStaged).
func newE2EProvider(tempDir, gameDir string) *Provider {
	p := New(Settings{TempDir: tempDir}, testLogger())
	p.SetResolvedPaths(map[core.GameID]string{e2eGID: gameDir})
	return p
}

func writeLauncherVersion(t *testing.T, gameDir, version string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"version": version})
	if err := os.WriteFile(filepath.Join(gameDir, "launcherDownloadConfig.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedGameFile(t *testing.T, gameDir, relSlash string, content []byte) {
	t.Helper()
	full := filepath.Join(gameDir, filepath.FromSlash(relSlash))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// noKrpdiffLeaked walks gameDir and fails if any *.krpdiff file is found —
// Ephemeral diffs must never survive into the game dir (spec invariant 2).
func noKrpdiffLeaked(t *testing.T, gameDir string) {
	t.Helper()
	_ = filepath.WalkDir(gameDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr
		}
		if strings.HasSuffix(d.Name(), ".krpdiff") {
			t.Errorf("krpdiff diff leaked into gameDir: %s", path)
		}
		return nil
	})
}

func readGameFile(t *testing.T, gameDir, relSlash string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(gameDir, filepath.FromSlash(relSlash)))
	if err != nil {
		t.Fatalf("read gameDir/%s: %v", relSlash, err)
	}
	return b
}

// --- fixture bundle (a/b krpdiff pairs, Task 1.5) ---

type krpdiffPair struct {
	old, new_, diff []byte
}

func loadKrpdiffPair(t *testing.T, letter string) krpdiffPair {
	t.Helper()
	return krpdiffPair{
		old:  readKrpdiffFixture(t, "old_"+letter+".bin"),
		new_: readKrpdiffFixture(t, "new_"+letter+".bin"),
		diff: readKrpdiffFixture(t, letter+".krpdiff"),
	}
}

// ===========================================================================
// Case 1: TestE2E_PatchHappyPath
// ===========================================================================
//
// 2 patch groups (a, b) + 1 regular resource file + deleteFiles, driven
// through the full CheckForUpdateWithProgress → RunUpdate pipeline. Verifies
// final gameDir state (patched content, deleted file gone, no krpdiff
// leakage), launcherDownloadConfig.json version bump, and version temp dir
// cleanup. Also incidentally proves the embedded hpatchz supports the
// zstd+fadler64 dir-diff format (real hpatchz.exe subprocess, real
// fixtures) since both groups patch successfully.
func TestE2E_PatchHappyPath(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	b := loadKrpdiffPair(t, "b")
	regularOld := []byte("OLD_REGULAR_CONTENT")
	regularNew := []byte("NEW_REGULAR_CONTENT_LONGER_THAN_OLD")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	seedGameFile(t, gameDir, chunkBPath, b.old)
	seedGameFile(t, gameDir, "regular.dat", regularOld)
	seedGameFile(t, gameDir, "toDelete.dat", []byte("delete-me"))
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("a.krpdiff", a.diff)
	env.patchFS.set("b.krpdiff", b.diff)
	env.patchFS.set("regular.dat", regularNew)

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "a.krpdiff", MD5: md5hexBytes(a.diff), Size: int64(len(a.diff))},
			{Dest: "b.krpdiff", MD5: md5hexBytes(b.diff), Size: int64(len(b.diff))},
			{Dest: "regular.dat", MD5: md5hexBytes(regularNew), Size: int64(len(regularNew))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "a.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.new_), Size: int64(len(a.new_))}},
			},
			{
				Dest:     "b.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.old), Size: int64(len(b.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.new_), Size: int64(len(b.new_))}},
			},
		},
		DeleteFiles: []string{"toDelete.dat"},
	}
	mountJSON(env.mux, "/patch/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if plan.Version != e2eTargetVersion {
		t.Errorf("plan.Version = %q, want %q", plan.Version, e2eTargetVersion)
	}
	if len(plan.PatchGroups) != 2 {
		t.Fatalf("plan.PatchGroups = %+v, want 2 groups", plan.PatchGroups)
	}
	if len(plan.DeleteFiles) != 1 || plan.DeleteFiles[0] != "toDelete.dat" {
		t.Errorf("plan.DeleteFiles = %v, want [toDelete.dat]", plan.DeleteFiles)
	}
	// Mirror-face of case 2's fallback-URL assertion: a general resource
	// file (not a group's dst) must be URL'd from the PATCH base
	// (cdn/baseURL), never the FULL base fallback route.
	var sawRegularTask bool
	for _, f := range plan.Files {
		if f.Path == "regular.dat" {
			sawRegularTask = true
			if !strings.HasPrefix(f.URL, env.srv.URL+"/patch-files/") {
				t.Errorf("regular.dat URL = %q, want PATCH base prefix %q (general resource files use the patch cdn/baseURL, not the full one)", f.URL, env.srv.URL+"/patch-files/")
			}
		}
	}
	if !sawRegularTask {
		t.Fatalf("plan.Files missing regular.dat task: %+v", plan.Files)
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak content mismatch after patch")
	}
	if got := readGameFile(t, gameDir, chunkBPath); string(got) != string(b.new_) {
		t.Errorf("chunk_b.pak content mismatch after patch")
	}
	if got := readGameFile(t, gameDir, "regular.dat"); string(got) != string(regularNew) {
		t.Errorf("regular.dat content mismatch after download")
	}
	if _, err := os.Stat(filepath.Join(gameDir, "toDelete.dat")); err == nil {
		t.Errorf("toDelete.dat should have been removed by deleteFiles")
	}
	noKrpdiffLeaked(t, gameDir)

	var cfg struct {
		Version string `json:"version"`
	}
	cfgBytes, err := os.ReadFile(filepath.Join(gameDir, "launcherDownloadConfig.json"))
	if err != nil {
		t.Fatalf("read launcherDownloadConfig.json: %v", err)
	}
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("parse launcherDownloadConfig.json: %v", err)
	}
	if cfg.Version != e2eTargetVersion {
		t.Errorf("launcherDownloadConfig.json version = %q, want %q", cfg.Version, e2eTargetVersion)
	}

	versionDir := filepath.Join(tempDir, "kurogames-wutheringwaves", e2eTargetVersion)
	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Errorf("version temp dir should be cleaned up post-apply, got err=%v", err)
	}
}

// ===========================================================================
// Case 2: TestE2E_SrcMismatchFallsBack
// ===========================================================================
//
// Group B's local file is corrupted (neither src nor dst content) before the
// check runs — its dst must fall back to a full download served from the
// FULL base URL (fullCDN/fullBaseURL, per update_patchplan.go's C1 fix), NOT
// the patch base. Group A proceeds through the normal patch path. Both must
// converge to the correct final content.
func TestE2E_SrcMismatchFallsBack(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	b := loadKrpdiffPair(t, "b")

	// Same size as old_b.bin, different bytes — forces the hash-based
	// classification (not the cheap size pre-check) to land on the "neither
	// src nor dst" default branch.
	corruptB := append([]byte(nil), b.old...)
	corruptB[0] ^= 0xFF

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	seedGameFile(t, gameDir, chunkBPath, corruptB)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("a.krpdiff", a.diff)
	env.fullFS.set(chunkBPath, b.new_)

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "a.krpdiff", MD5: md5hexBytes(a.diff), Size: int64(len(a.diff))},
			{Dest: "b.krpdiff", MD5: md5hexBytes(b.diff), Size: int64(len(b.diff))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "a.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.new_), Size: int64(len(a.new_))}},
			},
			{
				Dest:     "b.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.old), Size: int64(len(b.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.new_), Size: int64(len(b.new_))}},
			},
		},
	}
	mountJSON(env.mux, "/patch/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if len(plan.PatchGroups) != 1 || plan.PatchGroups[0].DiffPath != "a.krpdiff" {
		t.Fatalf("plan.PatchGroups = %+v, want exactly group A", plan.PatchGroups)
	}
	var sawFullFallbackTask bool
	for _, f := range plan.Files {
		if f.Path == chunkBPath {
			sawFullFallbackTask = true
			if !strings.HasPrefix(f.URL, env.srv.URL+"/full-files/") {
				t.Errorf("chunk_b.pak fallback URL = %q, want prefix %q (FULL base, not patch base)", f.URL, env.srv.URL+"/full-files/")
			}
		}
	}
	if !sawFullFallbackTask {
		t.Fatalf("plan.Files missing chunk_b.pak full-download fallback task: %+v", plan.Files)
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak (patched) content mismatch")
	}
	if got := readGameFile(t, gameDir, chunkBPath); string(got) != string(b.new_) {
		t.Errorf("chunk_b.pak (full download) content mismatch")
	}
	if got := env.fullFS.hitCount(chunkBPath); got != 1 {
		t.Errorf("full-route request count for chunk_b.pak = %d, want 1", got)
	}
	if got := env.patchFS.hitCount("b.krpdiff"); got != 0 {
		t.Errorf("patch route should never be hit for group B's diff (it fell back to full download), got %d hits", got)
	}
	noKrpdiffLeaked(t, gameDir)
}

// ===========================================================================
// Case 3: TestE2E_BadDstHashKeepsOldFile
// ===========================================================================
//
// The manifest's dst.md5 is deliberately wrong (simulating a corrupt/drifted
// manifest entry). Plan-build-time note: the SAME wrong md5 also drives the
// dst-first classification in buildFileAndPatchPlan (local file's real hash
// == src.MD5, and != the wrong dst.MD5) — so the local file is STILL
// correctly classified into the PatchGroup path (h == src.MD5 branch), not
// diverted to a full-download fallback. This test therefore necessarily
// exercises the patch branch (not a coincidence of case construction): the
// hpatchz apply happens for real, and it's the POST-patch md5 verification
// (against the wrong g.Dst.Hash) that fails.
//
// After the failed run, gameDir's original file must be untouched (the
// mismatch is caught before rename). Re-running with a corrected manifest
// must converge.
func TestE2E_BadDstHashKeepsOldFile(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	wrongHash := strings.Repeat("f", 32) // syntactically valid MD5-shaped hex, deliberately wrong

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)
	env.patchFS.set("a.krpdiff", a.diff)

	mkIdxFile := func(dstHash string) *indexFileRaw {
		return &indexFileRaw{
			ApplyTypes: []string{"group"},
			Resource: []manifestFileRaw{
				{Dest: "a.krpdiff", MD5: md5hexBytes(a.diff), Size: int64(len(a.diff))},
			},
			GroupInfos: []groupInfoRaw{
				{
					Dest:     "a.krpdiff",
					SrcFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))}},
					DstFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: dstHash, Size: int64(len(a.new_))}},
				},
			},
		}
	}

	mb := &e2eMutableJSON{}
	mb.set(mkIdxFile(wrongHash))
	mb.mount(env.mux, "/patch/indexFile.json")
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress (run 1): %v", err)
	}
	if len(plan.PatchGroups) != 1 {
		t.Fatalf("plan.PatchGroups = %+v, want exactly 1 group (dst-first classification must still route to PatchGroup)", plan.PatchGroups)
	}
	if plan.PatchGroups[0].Dst.Hash != wrongHash {
		t.Fatalf("PatchGroups[0].Dst.Hash = %q, want the wrong manifest hash %q", plan.PatchGroups[0].Dst.Hash, wrongHash)
	}

	err = p.RunUpdate(ctx, plan, func(core.UpdateEvent) {})
	if err == nil {
		t.Fatalf("RunUpdate (run 1) succeeded, want patch_failed (post-patch md5 mismatch)")
	}
	var uerr *core.UpdateError
	if !errors.As(err, &uerr) || uerr.Code != "patch_failed" {
		t.Errorf("run 1 error = %v, want a *core.UpdateError with Code=patch_failed", err)
	}
	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.old) {
		t.Errorf("gameDir file must be untouched after a failed patch; got content differs from old_a.bin")
	}
	if got := env.patchFS.hitCount("a.krpdiff"); got != 1 {
		t.Fatalf("post-run-1 a.krpdiff download hits = %d, want 1", got)
	}

	// Correct the manifest and re-run — must converge.
	mb.set(mkIdxFile(md5hexBytes(a.new_)))

	plan2, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress (run 2): %v", err)
	}
	if err := p.RunUpdate(ctx, plan2, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate (run 2, corrected manifest): %v", err)
	}
	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak content mismatch after convergent re-run")
	}
	// Pins the current warm-resume semantics rather than asserting an
	// aspirational "no re-download": run 1's runApply removes
	// progress.json unconditionally near the top of the apply phase —
	// BEFORE the patch phase (and thus before it can fail) — so by the
	// time run 1 returns its error, progress.json is already gone. Run 2's
	// progress.Init() therefore finds no progress.json, takes the "no
	// match" branch, and starts from a fresh empty progress ledger; a.krpdiff
	// has no recorded entry to short-circuit isComplete(), so the download
	// phase re-fetches it from the CDN even though the correct bytes were
	// already sitting on disk from run 1. Hits go 1 (run 1) → 2 (run 2).
	if got := env.patchFS.hitCount("a.krpdiff"); got != 2 {
		t.Errorf("post-run-2 a.krpdiff download hits = %d, want 2 (re-downloaded: progress.json was wiped by run 1's apply-phase failure before the patch phase ran)", got)
	}
	noKrpdiffLeaked(t, gameDir)
}

// ===========================================================================
// Case 4: TestE2E_KillBetweenPatchAndRename
// ===========================================================================
//
// Simulates a crash between hpatchz producing _out/<dst> and the subsequent
// rename: pre-seed a CORRECT _out product on disk, and have the CDN serve
// BAD diff bytes whose manifest md5/size matches those bad bytes exactly (so
// download+verify succeeds transporting them — the corruption is only
// "revealed" if hpatchz actually tries to apply them, which would either
// error out or produce content failing the post-patch md5 check). RunUpdate
// succeeding therefore proves the cached-_out shortcut fired and hpatchz
// was never invoked. Deliberately NOT timing-based (no goroutine kill mid-run
// — flaky); the proof is structural (bad bytes could not have produced a
// verifying result any other way).
func TestE2E_KillBetweenPatchAndRename(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	badDiff := []byte("THIS IS NOT A REAL HDIFF PATCH FILE — BOGUS BYTES THAT WOULD FAIL HPATCHZ IF EVER RUN")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)
	env.patchFS.set("a.krpdiff", badDiff)

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "a.krpdiff", MD5: md5hexBytes(badDiff), Size: int64(len(badDiff))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "a.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.new_), Size: int64(len(a.new_))}},
			},
		},
	}
	mountJSON(env.mux, "/patch/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	// Pre-seed the CORRECT _out product at the path runPatchGroups will
	// check for a cached, already-verified product — before RunUpdate ever
	// runs. Path layout: <tempRoot>/kurogames-wutheringwaves/<version>/_out/<dst>.
	outPath := filepath.Join(tempDir, "kurogames-wutheringwaves", e2eTargetVersion, "_out", filepath.FromSlash(chunkAPath))
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outPath, a.new_, 0o644); err != nil {
		t.Fatal(err)
	}

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if plan.Version != e2eTargetVersion {
		t.Fatalf("plan.Version = %q, want %q (pre-seeded _out path depends on this)", plan.Version, e2eTargetVersion)
	}
	if len(plan.PatchGroups) != 1 {
		t.Fatalf("plan.PatchGroups = %+v, want exactly 1 group", plan.PatchGroups)
	}

	// The test's structural argument ("hpatchz never ran") only holds if
	// the bad diff genuinely went through download+verify first — i.e. the
	// bytes on disk really are the corrupt ones (matching their own wrong
	// manifest hash), not some accidental pass-through. Stage the download
	// phase directly (same construction pattern as the package's other
	// downloader-level tests) BEFORE calling RunUpdate, so this can be
	// asserted independently of the apply phase that follows.
	preStage := newProgressStore(tempDir, string(e2eGID), plan.Version)
	if err := preStage.Init(plan.ManifestETag); err != nil {
		t.Fatalf("pre-stage progress.Init: %v", err)
	}
	d := &downloader{
		client: env.srv.Client(), logger: testLogger(), tempRoot: tempDir,
		progress: preStage, plan: &plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(ctx); err != nil {
		t.Fatalf("pre-stage download: %v", err)
	}
	if got := env.patchFS.hitCount("a.krpdiff"); got != 1 {
		t.Fatalf("a.krpdiff download hit count = %d, want 1 (download+verify of the bad-but-hash-matching diff must have happened)", got)
	}
	stagedDiff, err := os.ReadFile(filepath.Join(preStage.dir(), "a.krpdiff"))
	if err != nil {
		t.Fatalf("bad diff must have landed in the version dir after download: %v", err)
	}
	if string(stagedDiff) != string(badDiff) {
		t.Errorf("staged diff bytes mismatch — want the bad bytes actually served by the CDN")
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate failed — bad diff bytes must not have been applied by hpatchz if the cached _out shortcut worked: %v", err)
	}
	// The pre-staged download above already consumed the one legitimate
	// CDN request; RunUpdate's own download phase must find the identical
	// bytes already complete (same progress dir/ETag) and NOT re-fetch.
	if got := env.patchFS.hitCount("a.krpdiff"); got != 1 {
		t.Errorf("a.krpdiff download hit count after RunUpdate = %d, want still 1 (no re-download)", got)
	}

	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak content mismatch — expected the pre-seeded cached product")
	}
	noKrpdiffLeaked(t, gameDir)
}

// ===========================================================================
// Case 5: TestE2E_MultiGroupFullDownload
// ===========================================================================
//
// A non-1:1 (multi-file) group is never patch-classified — its dstFiles
// unconditionally become full-download FileTasks via the FULL cdn/baseURL,
// regardless of local content. This test uses one group whose srcFiles/
// dstFiles each list both chunk_a.pak and chunk_b.pak (a combined
// multi-file diff in the real protocol), which must fully decompose into 2
// plain downloads with zero PatchGroups.
func TestE2E_MultiGroupFullDownload(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	b := loadKrpdiffPair(t, "b")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	seedGameFile(t, gameDir, chunkBPath, b.old)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.fullFS.set(chunkAPath, a.new_)
	env.fullFS.set(chunkBPath, b.new_)

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			// combined.krpdiff is this group's own Dest — never fetched
			// because a multi-file group always falls back to full
			// download (no diff download needed at all).
			{Dest: "combined.krpdiff", MD5: "unused", Size: 1},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest: "combined.krpdiff",
				SrcFiles: []manifestFileRaw{
					{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))},
					{Dest: chunkBPath, MD5: md5hexBytes(b.old), Size: int64(len(b.old))},
				},
				DstFiles: []manifestFileRaw{
					{Dest: chunkAPath, MD5: md5hexBytes(a.new_), Size: int64(len(a.new_))},
					{Dest: chunkBPath, MD5: md5hexBytes(b.new_), Size: int64(len(b.new_))},
				},
			},
		},
	}
	mountJSON(env.mux, "/patch/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if len(plan.PatchGroups) != 0 {
		t.Fatalf("plan.PatchGroups = %+v, want 0 (multi-file group always falls back to full download)", plan.PatchGroups)
	}
	if len(plan.Files) != 2 {
		t.Fatalf("plan.Files = %+v, want 2 full-download tasks", plan.Files)
	}
	for _, f := range plan.Files {
		if !strings.HasPrefix(f.URL, env.srv.URL+"/full-files/") {
			t.Errorf("file %s URL = %q, want FULL base prefix %q", f.Path, f.URL, env.srv.URL+"/full-files/")
		}
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak content mismatch")
	}
	if got := readGameFile(t, gameDir, chunkBPath); string(got) != string(b.new_) {
		t.Errorf("chunk_b.pak content mismatch")
	}
	if got := env.fullFS.hitCount(chunkAPath); got != 1 {
		t.Errorf("full-route hits for chunk_a.pak = %d, want 1", got)
	}
	if got := env.fullFS.hitCount(chunkBPath); got != 1 {
		t.Errorf("full-route hits for chunk_b.pak = %d, want 1", got)
	}
	noKrpdiffLeaked(t, gameDir)
}

// ===========================================================================
// Case 6: TestE2E_PredlThenApply
// ===========================================================================
//
// The e2e (real patch) version of Task 9's TestRunUpdate_AdoptsPredlStaged:
// CheckForPredownload (full manifest fetch, real patch groups) → RunUpdate
// stages (incl. the Ephemeral krpdiff diffs) → RenameToPredlReady → the SAME
// plan, Kind flipped to PlanUpdate, is re-run through RunUpdate — this must
// adopt every staged byte (zero re-download, asserted via the CDN hit
// counters) and then run the real patch phase to completion. Per the task
// brief, driving the "predl → apply" transition at the provider layer
// (reusing the plan) rather than via a second CheckForUpdateWithProgress
// call mirrors Task 9 test 3 and is the accepted shortcut for this case.
func TestE2E_PredlThenApply(t *testing.T) {
	a := loadKrpdiffPair(t, "a")
	b := loadKrpdiffPair(t, "b")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, chunkAPath, a.old)
	seedGameFile(t, gameDir, chunkBPath, b.old)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("a.krpdiff", a.diff)
	env.patchFS.set("b.krpdiff", b.diff)

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "a.krpdiff", MD5: md5hexBytes(a.diff), Size: int64(len(a.diff))},
			{Dest: "b.krpdiff", MD5: md5hexBytes(b.diff), Size: int64(len(b.diff))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "a.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.old), Size: int64(len(a.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkAPath, MD5: md5hexBytes(a.new_), Size: int64(len(a.new_))}},
			},
			{
				Dest:     "b.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.old), Size: int64(len(b.old))}},
				DstFiles: []manifestFileRaw{{Dest: chunkBPath, MD5: md5hexBytes(b.new_), Size: int64(len(b.new_))}},
			},
		},
	}
	mountJSON(env.mux, "/predl/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/predl-full/indexFile.json")
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eLocalVersion, // live version hasn't moved yet — this is a predownload
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		predlVersion:        e2ePredlVersion,
		predlIndexFileURL:   "/predl-full/indexFile.json",
		predlBaseURL:        "/full-files/",
		predlPatch:          &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/predl/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	predlPlan, err := p.CheckForPredownload(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if predlPlan.Kind != core.PlanPredownload {
		t.Fatalf("predlPlan.Kind = %v, want PlanPredownload", predlPlan.Kind)
	}
	if len(predlPlan.PatchGroups) != 2 {
		t.Fatalf("predlPlan.PatchGroups = %+v, want 2 real patch groups", predlPlan.PatchGroups)
	}

	if err := p.RunUpdate(ctx, predlPlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}
	hitsAAfterPredl := env.patchFS.hitCount("a.krpdiff")
	hitsBAfterPredl := env.patchFS.hitCount("b.krpdiff")
	if hitsAAfterPredl != 1 || hitsBAfterPredl != 1 {
		t.Fatalf("post-predl diff download counts = a:%d b:%d, want 1/1", hitsAAfterPredl, hitsBAfterPredl)
	}
	// gameDir must be untouched by a predownload. A top-level entry COUNT
	// has zero discrimination here — both chunk_a.pak and chunk_b.pak live
	// under the shared Client/ subtree, so even an (erroneous) in-place
	// apply during predl would leave the count unchanged. Assert content
	// directly instead: both chunk files must still read as their OLD
	// (pre-update) bytes, and the staged predl must have landed as
	// predl_ready.json (not progress.json) in the version dir.
	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.old) {
		t.Errorf("chunk_a.pak must still be the OLD content after a predownload (got patched/new content)")
	}
	if got := readGameFile(t, gameDir, chunkBPath); string(got) != string(b.old) {
		t.Errorf("chunk_b.pak must still be the OLD content after a predownload (got patched/new content)")
	}
	predlReadyPath := filepath.Join(tempDir, "kurogames-wutheringwaves", e2ePredlVersion, "predl_ready.json")
	if _, err := os.Stat(predlReadyPath); err != nil {
		t.Errorf("predl_ready.json missing after predl RunUpdate: %v", err)
	}

	updatePlan := predlPlan
	updatePlan.Kind = core.PlanUpdate

	if err := p.RunUpdate(ctx, updatePlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("apply RunUpdate: %v", err)
	}

	if got := env.patchFS.hitCount("a.krpdiff"); got != hitsAAfterPredl {
		t.Errorf("a.krpdiff re-downloaded during apply (staged bytes must be adopted): hits went from %d to %d", hitsAAfterPredl, got)
	}
	if got := env.patchFS.hitCount("b.krpdiff"); got != hitsBAfterPredl {
		t.Errorf("b.krpdiff re-downloaded during apply (staged bytes must be adopted): hits went from %d to %d", hitsBAfterPredl, got)
	}

	if got := readGameFile(t, gameDir, chunkAPath); string(got) != string(a.new_) {
		t.Errorf("chunk_a.pak content mismatch after predl-then-apply patch")
	}
	if got := readGameFile(t, gameDir, chunkBPath); string(got) != string(b.new_) {
		t.Errorf("chunk_b.pak content mismatch after predl-then-apply patch")
	}
	noKrpdiffLeaked(t, gameDir)
}

// ===========================================================================
// Case 7: TestE2E_PredlManifestDrift
// ===========================================================================
//
// Offline simulation of go-live ETag/content drift: after a predl stages 2
// plain files, one file's content changes on the "CDN" and its manifest md5
// is updated to match (the other file is untouched). The adopting
// RunUpdate must re-download ONLY the drifted file (ConsumePredlStaged's
// per-file Hash comparison, spec §2.5) and adopt the other from staged
// bytes.
func TestE2E_PredlManifestDrift(t *testing.T) {
	oldX := []byte("OLD_X_CONTENT_V0")
	v1X := []byte("X_CONTENT_V1")
	v2X := []byte("X_CONTENT_V2_DRIFTED")
	oldY := []byte("OLD_Y_CONTENT_V0")
	v1Y := []byte("Y_CONTENT_V1")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, "x.dll", oldX)
	seedGameFile(t, gameDir, "y.dll", oldY)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("x.dll", v1X)
	env.patchFS.set("y.dll", v1Y)

	idxFile := &indexFileRaw{
		Resource: []manifestFileRaw{
			{Dest: "x.dll", MD5: md5hexBytes(v1X), Size: int64(len(v1X))},
			{Dest: "y.dll", MD5: md5hexBytes(v1Y), Size: int64(len(v1Y))},
		},
	}
	mountJSON(env.mux, "/predl/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eLocalVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		predlVersion:        e2ePredlVersion,
		predlIndexFileURL:   "/predl/indexFile.json",
		predlBaseURL:        "/patch-files/",
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	predlPlan, err := p.CheckForPredownload(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if len(predlPlan.Files) != 2 {
		t.Fatalf("predlPlan.Files = %+v, want 2", predlPlan.Files)
	}

	if err := p.RunUpdate(ctx, predlPlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}
	if got := env.patchFS.hitCount("x.dll"); got != 1 {
		t.Fatalf("post-predl x.dll hits = %d, want 1", got)
	}
	if got := env.patchFS.hitCount("y.dll"); got != 1 {
		t.Fatalf("post-predl y.dll hits = %d, want 1", got)
	}

	// Simulate manifest drift: x.dll's content (and hash) changed on the
	// CDN after the predl staged the old bytes; y.dll is untouched.
	env.patchFS.set("x.dll", v2X)

	updatePlan := predlPlan
	updatePlan.Kind = core.PlanUpdate
	filesCopy := make([]core.FileTask, len(predlPlan.Files))
	copy(filesCopy, predlPlan.Files)
	for i := range filesCopy {
		if filesCopy[i].Path == "x.dll" {
			filesCopy[i].Hash = md5hexBytes(v2X)
			filesCopy[i].Size = int64(len(v2X))
		}
	}
	updatePlan.Files = filesCopy

	if err := p.RunUpdate(ctx, updatePlan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("apply RunUpdate: %v", err)
	}

	if got := env.patchFS.hitCount("x.dll"); got != 2 {
		t.Errorf("x.dll hits after drift+apply = %d, want 2 (predl + re-download)", got)
	}
	if got := env.patchFS.hitCount("y.dll"); got != 1 {
		t.Errorf("y.dll hits after drift+apply = %d, want 1 (adopted from staged bytes, not re-downloaded)", got)
	}
	if got := readGameFile(t, gameDir, "x.dll"); string(got) != string(v2X) {
		t.Errorf("x.dll content = %q, want drifted v2 content %q", got, v2X)
	}
	if got := readGameFile(t, gameDir, "y.dll"); string(got) != string(v1Y) {
		t.Errorf("y.dll content = %q, want staged v1 content %q", got, v1Y)
	}
}

// ===========================================================================
// Case 8: TestE2E_LegacyManifestUnchanged
// ===========================================================================
//
// Regression guard: an indexFile with no groupInfos/applyTypes must behave
// identically to the pre-krpdiff flow — filterChangedFiles directly, zero
// PatchGroups, zero DeleteFiles — driven through the full
// CheckForUpdateWithProgress → RunUpdate pipeline (unlike
// TestUpdate_HappyPath_E2E, which hand-builds the plan and skips the
// two-step manifest fetch).
func TestE2E_LegacyManifestUnchanged(t *testing.T) {
	oldA := []byte("OLD_LEGACY_A_CONTENT")
	newA := []byte("NEW_LEGACY_A_CONTENT_LONGER")
	oldB := []byte("OLD_LEGACY_B_CONTENT")
	newB := []byte("NEW_LEGACY_B_CONTENT_LONGER")
	// alreadyC is the definitive legacy-behavior leg (MINOR-1): a file whose
	// local content already matches the manifest md5 must be filtered out
	// of the plan entirely (filterChangedFiles' whole purpose), and its CDN
	// route must never be hit.
	alreadyC := []byte("ALREADY_UP_TO_DATE_C_CONTENT")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, "legacy_a.dat", oldA)
	seedGameFile(t, gameDir, "legacy_b.dat", oldB)
	seedGameFile(t, gameDir, "legacy_c.dat", alreadyC)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("legacy_a.dat", newA)
	env.patchFS.set("legacy_b.dat", newB)
	env.patchFS.set("legacy_c.dat", alreadyC)

	idxFile := &indexFileRaw{
		Resource: []manifestFileRaw{
			{Dest: "legacy_a.dat", MD5: md5hexBytes(newA), Size: int64(len(newA))},
			{Dest: "legacy_b.dat", MD5: md5hexBytes(newB), Size: int64(len(newB))},
			{Dest: "legacy_c.dat", MD5: md5hexBytes(alreadyC), Size: int64(len(alreadyC))},
		},
	}
	mountJSON(env.mux, "/legacy/indexFile.json", idxFile)
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/legacy/indexFile.json",
		defaultBaseURL:      "/patch-files/",
		defaultPatch:        nil, // legacy: no patchConfig at all
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if len(plan.PatchGroups) != 0 {
		t.Errorf("plan.PatchGroups = %+v, want 0 (legacy manifest)", plan.PatchGroups)
	}
	if len(plan.DeleteFiles) != 0 {
		t.Errorf("plan.DeleteFiles = %v, want empty (legacy manifest)", plan.DeleteFiles)
	}
	if len(plan.Files) != 2 {
		t.Fatalf("plan.Files = %+v, want 2 (legacy_c.dat already matches, must be filtered out)", plan.Files)
	}
	for _, f := range plan.Files {
		if f.Path == "legacy_c.dat" {
			t.Errorf("legacy_c.dat (already up to date) must not appear in plan.Files: %+v", plan.Files)
		}
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	if got := readGameFile(t, gameDir, "legacy_a.dat"); string(got) != string(newA) {
		t.Errorf("legacy_a.dat content mismatch")
	}
	if got := readGameFile(t, gameDir, "legacy_b.dat"); string(got) != string(newB) {
		t.Errorf("legacy_b.dat content mismatch")
	}
	if got := readGameFile(t, gameDir, "legacy_c.dat"); string(got) != string(alreadyC) {
		t.Errorf("legacy_c.dat content mismatch (must remain untouched)")
	}
	if got := env.patchFS.hitCount("legacy_c.dat"); got != 0 {
		t.Errorf("legacy_c.dat CDN route hits = %d, want 0 (already up to date, never downloaded)", got)
	}
}

// TestE2E_LegacyPatchConfigPresentButNoGroupInfos is MINOR-1's variant leg:
// index.json's default.config DOES have a matching patchType="patch"
// patchConfig entry (so pickIndexFileForVersion picks the patch route), but
// the indexFile.json actually fetched from that route carries no
// groupInfos/applyTypes at all — a real-world shape for "a patch update
// with zero binary diffs, just plain file replacements". The
// legacy-vs-patch-aware routing decision in CheckForUpdateWithProgress must
// be driven by the FETCHED idxFile's own fields (len(GroupInfos) > 0 ||
// len(ApplyTypes) > 0), never by whether the outer patchConfig was picked —
// this pins that against a future regression that gates on cfg.PatchType
// instead.
func TestE2E_LegacyPatchConfigPresentButNoGroupInfos(t *testing.T) {
	oldA := []byte("OLD_PLAIN_A_CONTENT")
	newA := []byte("NEW_PLAIN_A_CONTENT_LONGER")

	env := newE2EEnv(t)
	tempDir := t.TempDir()
	gameDir := t.TempDir()

	seedGameFile(t, gameDir, "plain_a.dat", oldA)
	writeLauncherVersion(t, gameDir, e2eLocalVersion)

	env.patchFS.set("plain_a.dat", newA)

	idxFile := &indexFileRaw{
		Resource: []manifestFileRaw{
			{Dest: "plain_a.dat", MD5: md5hexBytes(newA), Size: int64(len(newA))},
		},
		// No GroupInfos, no ApplyTypes — the crux of this variant.
	}
	mountJSON(env.mux, "/patch/indexFile.json", idxFile)
	mountEmptyFullManifest(env.mux, "/full/indexFile.json")
	mountIndexJSON(env.mux, buildE2EIndexJSON(e2eIndexOpts{
		cdnURL:              env.srv.URL,
		defaultVersion:      e2eTargetVersion,
		defaultIndexFileURL: "/full/indexFile.json",
		defaultBaseURL:      "/full-files/",
		defaultPatch:        &patchRoute{fromVersion: e2eLocalVersion, indexFileURL: "/patch/indexFile.json", baseURL: "/patch-files/"},
	}))

	p := newE2EProvider(tempDir, gameDir)
	ctx := context.Background()

	plan, err := p.CheckForUpdateWithProgress(ctx, e2eGID, nil)
	if err != nil {
		t.Fatalf("CheckForUpdateWithProgress: %v", err)
	}
	if len(plan.PatchGroups) != 0 {
		t.Errorf("plan.PatchGroups = %+v, want 0 (patchConfig picked, but fetched indexFile has no groupInfos/applyTypes — must still be legacy flow)", plan.PatchGroups)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "plain_a.dat" {
		t.Fatalf("plan.Files = %+v, want exactly [plain_a.dat]", plan.Files)
	}

	if err := p.RunUpdate(ctx, plan, func(core.UpdateEvent) {}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if got := readGameFile(t, gameDir, "plain_a.dat"); string(got) != string(newA) {
		t.Errorf("plain_a.dat content mismatch")
	}
}
