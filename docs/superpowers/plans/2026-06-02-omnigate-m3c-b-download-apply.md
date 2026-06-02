# M3.C Phase B — Endfield Update Download + Apply Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add in-app update download + atomic apply to the `hypergryph` (Endfield) provider so an out-of-date install can be brought to the latest launchable version from within Omnigate, mirroring M3.A `kurogames`.

**Architecture:** Option A — **file-level incremental** (spec §0.4). `CheckForUpdate` fetches the new version's `game_files` manifest (`{pkg.file_path}/game_files`, AES-decrypted → JSON-lines `{path,md5,size}`), MD5-compares against the local install, and builds a plan of only the changed/missing files. `RunUpdate` downloads each file individually from `{pkg.file_path}/<path>` (4-worker pool, MD5 verify), WAL-guarded atomic-renames each into the game dir, then re-encrypts `config.ini` with the new version. **No zip-extract, no HDiffPatch, no MultiVolumeStream.** The App-layer RPC, Pinia store, and BottomBar/SidebarRow/bell UI are reused unchanged.

**Tech Stack:** Go 1.26 (`CGO_ENABLED=0`, no `-race` — see `feedback_no_cgo_race`), `golang.org/x/sys/windows`, Vue 3 + Vitest (frontend). Toolchain: `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`.

**Spec:** `docs/superpowers/specs/2026-06-02-omnigate-m3c-endfield-update-design.md` (§1, §3–§10).

**Mirror sources (read before each mirror task):**
- `internal/providers/kurogames/update_download.go` — download worker pool + retry + progress (verbatim drop-in).
- `internal/providers/kurogames/update_apply.go` — WAL + validateSameVolume + atomicRename + cleanup (mirror; swap version writeback).
- `internal/providers/kurogames/update_progress.go` — progressStore (verbatim drop-in minus predl).
- `internal/providers/kurogames/apply_lock*.go` / `process_check_*.go` — lock + process check (verbatim drop-in).
- `internal/providers/kurogames/update_manifest.go` — `filterChangedFiles` + `fileURL` patterns (adapt for game_files).
- `internal/providers/kurogames/kurogames.go` — Updater integration (CheckForUpdate/RunUpdate/IsGameRunning + interface assertions).

**Phase A already shipped (do NOT rewrite):** `crypto.go` (AES decrypt + pkcs7Unpad), `version.go` (`readLocalVersion`/`parseConfigVersion`/`fetchVersion`), `update_manifest.go` (`getLatestResponse`, `fetchGetLatest`, `getLatestURL`, `sanitizeURL`, `SetAPIBaseURL`, `randSegRe`), `meta.go` (`BackendID="hypergryph"`, `Endfield.exe`, `FolderName`), `hypergryph.go` (Provider with `CheckVersion`/`Launch`/etc.).

---

## File Structure (Phase B)

| File | New/Mod | Responsibility | Mirror |
|---|---|---|---|
| `internal/app/settings.go` | mod | `HypergryphSettings.TempDir` field + `LoadSettings` projection | kuro `KurogamesSettings` |
| `internal/app/app.go` | mod | pass `TempDir` to `hypergryph.New`; `tempDirFor` `case hypergryph.BackendID` | — |
| `internal/providers/hypergryph/hypergryph.go` | mod | `Settings.TempDir` + `clock`; download-grade http client; `CheckForUpdate`/`CheckForUpdateWithProgress`/`RunUpdate`/`IsGameRunning` + interface assertions | kuro `kurogames.go` |
| `internal/providers/hypergryph/crypto.go` | mod | add `encryptAESCBC` + `pkcs7Pad` (for config.ini re-encrypt writeback) | — |
| `internal/providers/hypergryph/version.go` | mod | add `writeLocalVersion` (re-encrypt config.ini) + `setConfigVersion` | — |
| `internal/providers/hypergryph/update_manifest.go` | mod | add `Pkg.FilePath`/`GameFilesMD5`; `manifestNode`; `gameFilesURL`; `fetchGameFilesManifest`; `parseGameFilesManifest`; `fileURL`; `filterChangedFiles` | kuro `update_manifest.go` |
| `internal/providers/hypergryph/update_progress.go` | new | `progressStore` | kuro (drop-in, no predl) |
| `internal/providers/hypergryph/apply_lock.go` + `_windows.go` + `_other.go` | new | LockFileEx apply lock / noop | kuro (drop-in) |
| `internal/providers/hypergryph/process_check_windows.go` + `_other.go` | new | `Endfield.exe` detection | kuro (drop-in) |
| `internal/providers/hypergryph/update_preflight.go` + `disk_space_windows.go` + `_other.go` | new | disk-space (`disk_full`) + same-volume (`cross_volume_temp`) preflight | — (M3.C improvement) |
| `internal/providers/hypergryph/update_download.go` | new | 4-worker per-file download + MD5 + retry + progress | kuro (drop-in) |
| `internal/providers/hypergryph/update_apply.go` | new | WAL + validateSameVolume + atomic rename + config.ini AES writeback + cleanup | kuro (mirror) |
| `internal/providers/hypergryph/errcode_coverage_test.go` | new | every emitted §7 code referenced | kuro |
| `frontend/src/**` (Vitest) | mod | assert predl button hidden for Hypergryph | — |

**Task order is strict-serial.** B2 (manifest) and B3–B6 (progress/lock/process/download) are independent of each other but B7 (apply) needs B3, and B8 (provider) needs B2+B6+B7. Do them in order.

**TDD note for drop-in mirror tasks (B3/B4/B5/B6):** these create a verbatim copy of a kuro file, so their tests are **mirror-verification smoke checks** (assert the copied behavior works in the new package), not strict red→green — the "implementation" is a known-good copy. The novel tasks (B2 manifest, B7 apply+crypto+preflight, B8 provider) follow strict failing-test-first TDD.

---

## Task B1: Settings.TempDir plumbing

**Files:**
- Modify: `internal/app/settings.go:40` (HypergryphSettings) + `:129-131` (LoadSettings projection)
- Modify: `internal/app/app.go:109-112` (constructProviders) + `:457-468` (tempDirFor switch)
- Modify: `internal/providers/hypergryph/hypergryph.go:13-15` (Settings struct)
- Test: `internal/app/settings_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/app/settings_test.go`:

```go
func TestSettings_HypergryphTempDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	s := defaultSettings() // NOTE: unexported (settings.go:61); NOT DefaultSettings
	s.Backends.Hypergryph.Path = `C:\Games\GRYPHLINK`
	s.Backends.Hypergryph.TempDir = `D:\omnigate-temp`
	if err := SaveSettings(path, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Backends.Hypergryph.TempDir != `D:\omnigate-temp` {
		t.Errorf("TempDir = %q, want D:\\omnigate-temp", loaded.Backends.Hypergryph.TempDir)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="/c/Program Files/Go/bin:$PATH"; cd /c/Users/willie/Repos/omnigate; CGO_ENABLED=0 go test ./internal/app/ -run TestSettings_HypergryphTempDirRoundTrip -v`
Expected: FAIL — `loaded.Backends.Hypergryph.TempDir` undefined (field doesn't exist) → compile error.

- [ ] **Step 3: Add the TempDir field**

In `internal/app/settings.go`, replace line 40:

```go
type HypergryphSettings struct {
	Path    string `toml:"path"`
	TempDir string `toml:"temp_dir,omitempty"` // empty → runtime default os.TempDir()/omnigate/hypergryph
}
```

- [ ] **Step 4: Add the LoadSettings projection**

In `internal/app/settings.go`, the hypergryph projection block (around line 129) currently projects only `Path`. Replace it with:

```go
	if raw.Backends.Hypergryph.Path != "" {
		out.Backends.Hypergryph.Path = raw.Backends.Hypergryph.Path
	}
	if raw.Backends.Hypergryph.TempDir != "" {
		out.Backends.Hypergryph.TempDir = raw.Backends.Hypergryph.TempDir
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/app/ -run TestSettings_HypergryphTempDirRoundTrip -v`
Expected: PASS.

- [ ] **Step 6: Add the provider Settings field**

In `internal/providers/hypergryph/hypergryph.go`, replace the Settings struct (lines 13-15):

```go
type Settings struct {
	Path    string
	TempDir string // optional override; empty → app layer's hypergryph temp default
}
```

- [ ] **Step 7: Wire constructProviders + tempDirFor**

In `internal/app/app.go`, replace the `gryph := hypergryph.New(...)` call (lines 109-112):

```go
	gryph := hypergryph.New(
		hypergryph.Settings{
			Path:    a.settings.Backends.Hypergryph.Path,
			TempDir: a.settings.Backends.Hypergryph.TempDir,
		},
		a.logger.With("backend", "hypergryph"),
	)
```

Then add a `case` to the `tempDirFor` switch (after the `hoyoverse.BackendID` case, before the closing `}` at line 468). `osTempDir()` is the existing app-layer test seam already used by the other cases (`app.go:462,467,471`) — use it, not `os.TempDir()`:

```go
	case hypergryph.BackendID:
		if td := a.settings.Backends.Hypergryph.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "omnigate", "hypergryph")
```

- [ ] **Step 8: Build + run app tests**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./internal/app/ ./internal/providers/hypergryph/...`
Expected: PASS (build clean; existing tests green).

- [ ] **Step 9: Commit**

```bash
git add internal/app/settings.go internal/app/app.go internal/providers/hypergryph/hypergryph.go internal/app/settings_test.go
git commit -m "feat(m3c-b): HypergryphSettings.TempDir plumbing"
```

---

## Task B2: game_files manifest fetch + parse + filterChangedFiles

**Files:**
- Modify: `internal/providers/hypergryph/update_manifest.go`
- Test: `internal/providers/hypergryph/update_manifest_test.go`

This task adds: the `pkg.file_path`/`game_files_md5` response fields, the `game_files` manifest fetch + AES-decrypt + JSON-lines parse, the per-file URL builder, and `filterChangedFiles` (MD5 compare vs the local install — mirrors kuro `update_manifest.go:255` but over `manifestNode` instead of `manifestFileRaw`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/hypergryph/update_manifest_test.go`:

```go
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
```

Add imports to the test file if missing: `"context"`, `"crypto/md5"`, `"encoding/hex"`, `"os"`, `"path/filepath"`, `"strings"`, `"testing"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestParseGameFilesManifest|TestFileURL|TestFilterChangedFiles_GameFiles' -v`
Expected: FAIL — `parseGameFilesManifest`, `fileURL`, `manifestNode`, `filterChangedFiles` undefined (compile error).

- [ ] **Step 3: Extend the response struct with pkg.file_path**

In `internal/providers/hypergryph/update_manifest.go`, replace the `Pkg` field of `getLatestResponse` (lines 61-68) with:

```go
	Pkg struct {
		Packs []struct {
			URL         string `json:"url"`
			MD5         string `json:"md5"`
			PackageSize string `json:"package_size"`
		} `json:"packs"`
		TotalSize    string `json:"total_size"`
		FilePath     string `json:"file_path"`      // per-file CDN base — Option A consumes this
		GameFilesMD5 string `json:"game_files_md5"` // aggregate MD5 (informational)
	} `json:"pkg"`
```

- [ ] **Step 4: Add manifestNode + game_files fetch/parse + fileURL + filterChangedFiles**

Append to `internal/providers/hypergryph/update_manifest.go` (and add imports `"bufio"`, `"bytes"`, `"crypto/md5"`, `"encoding/hex"`, `"log/slog"`, `"os"`, `"path/filepath"`, `"sort"`, `"strings"`, `"sync"` to the file's import block):

```go
// manifestNode is one line of the decrypted game_files manifest
// (Collapse HgManifestNode). Path uses forward slashes.
type manifestNode struct {
	Path string `json:"path"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}

// gameFilesURL is the per-file CDN manifest endpoint: <pkg.file_path>/game_files.
func gameFilesURL(filePath string) string {
	return strings.TrimRight(filePath, "/") + "/game_files"
}

// fileURL builds a per-file download URL: <pkg.file_path>/<relPath> with spaces
// percent-encoded. relPath keeps forward slashes (URL path), so use string concat
// rather than filepath.Join (which would backslash on Windows).
func fileURL(filePath, relPath string) string {
	return strings.TrimRight(filePath, "/") + "/" + strings.ReplaceAll(relPath, " ", "%20")
}

// fetchGameFilesManifest GETs {filePath}/game_files, AES-decrypts it (same key/IV
// as config.ini), and parses the JSON-lines manifest. Status → core.UpdateError.
func fetchGameFilesManifest(ctx context.Context, client *http.Client, filePath string) ([]manifestNode, error) {
	urlStr := gameFilesURL(filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "omnigate/0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": err.Error()}}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &core.UpdateError{Code: "auth_failed", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode/100 == 5:
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": err.Error()}}
	}
	plain, derr := decryptAESCBC(body)
	if derr != nil {
		return nil, &core.UpdateError{Code: "corrupt", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": "game_files decrypt failed"}}
	}
	return parseGameFilesManifest(plain)
}

// parseGameFilesManifest parses decrypted JSON-lines into manifestNode slice.
// Blank lines + unparseable lines are skipped (mirrors HgGameRepairer). The
// config.ini entry is skipped — its version is written separately (spec §5/§6).
func parseGameFilesManifest(plain []byte) ([]manifestNode, error) {
	var out []manifestNode
	sc := bufio.NewScanner(bytes.NewReader(plain))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // tolerate long lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var n manifestNode
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			continue
		}
		if n.Path == "" || strings.EqualFold(n.Path, "config.ini") {
			continue
		}
		out = append(out, n)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("game_files scan: %w", err)
	}
	return out, nil
}

// verifyWorkers controls parallelism in filterChangedFiles (mirror kuro).
const verifyWorkers = 4

// filterChangedFiles drops manifest nodes whose on-disk file matches size+MD5.
// Mirrors kuro update_manifest.go filterChangedFiles, over manifestNode. Output
// is sorted by Path. onProgress(done, total) fires after each file (may be nil).
// ctx is checked per file so cancel mid-verify takes effect at the next boundary.
func filterChangedFiles(ctx context.Context, installDir, filePath string, nodes []manifestNode, logger *slog.Logger, onProgress func(done, total int)) []core.FileTask {
	total := len(nodes)
	if total == 0 {
		return nil
	}
	results := make([]*core.FileTask, total)
	jobs := make(chan int, total)
	for i := range nodes {
		jobs <- i
	}
	close(jobs)

	var (
		progressMu sync.Mutex
		done       int
	)
	emit := func() {
		progressMu.Lock()
		done++
		d := done
		progressMu.Unlock()
		if onProgress != nil {
			onProgress(d, total)
		}
	}

	mk := func(n manifestNode) *core.FileTask {
		return &core.FileTask{Path: n.Path, Hash: n.MD5, Size: n.Size, URL: fileURL(filePath, n.Path)}
	}

	var wg sync.WaitGroup
	wg.Add(verifyWorkers)
	for w := 0; w < verifyWorkers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				n := nodes[i]
				full := filepath.Join(installDir, filepath.FromSlash(n.Path))
				fi, err := os.Stat(full)
				if err != nil || fi.IsDir() || fi.Size() != n.Size {
					results[i] = mk(n)
					emit()
					continue
				}
				h, herr := md5File(full)
				if herr != nil {
					if logger != nil {
						logger.Debug("md5 check failed; will re-download", "path", n.Path, "err", herr)
					}
					results[i] = mk(n)
					emit()
					continue
				}
				if h != n.MD5 {
					results[i] = mk(n)
				}
				emit()
			}
		}()
	}
	wg.Wait()

	out := make([]core.FileTask, 0, total)
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// md5File returns the lowercase-hex MD5 of a file's contents.
func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestParseGameFilesManifest|TestFileURL|TestFilterChangedFiles_GameFiles' -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Whole-package build + vet**

Run: `CGO_ENABLED=0 go vet ./internal/providers/hypergryph/ && CGO_ENABLED=0 go test ./internal/providers/hypergryph/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/hypergryph/update_manifest.go internal/providers/hypergryph/update_manifest_test.go
git commit -m "feat(m3c-b): game_files manifest fetch/parse + filterChangedFiles"
```

---

## Task B3: progressStore (drop-in mirror)

**Files:**
- Create: `internal/providers/hypergryph/update_progress.go`
- Test: `internal/providers/hypergryph/update_progress_test.go`

- [ ] **Step 1: Create update_progress.go**

Create `internal/providers/hypergryph/update_progress.go` as a copy of `internal/providers/kurogames/update_progress.go` with: (a) `package kurogames` → `package hypergryph`; (b) **remove** the `RenameToPredlReady` method (predl deferred, spec §3.4). Full content:

```go
package hypergryph

import (
	"encoding/json"
	"fmt"
	"omnigate/internal/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type progressStore struct {
	// mu serializes concurrent MarkComplete calls from the download worker pool.
	mu       sync.Mutex
	tempRoot string
	gameID   string
	version  string
}

func newProgressStore(tempRoot, gameID, version string) *progressStore {
	return &progressStore{tempRoot: tempRoot, gameID: gameID, version: version}
}

func (p *progressStore) dir() string {
	flat := strings.ReplaceAll(p.gameID, "/", "-")
	return filepath.Join(p.tempRoot, flat, p.version)
}

// Init creates the progress dir and writes a fresh progress.json.
func (p *progressStore) Init(etag string) error {
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		return fmt.Errorf("mkdir progress: %w", err)
	}
	pf := core.ProgressFile{
		GameID:  p.gameID,
		Version: p.version,
		ETag:    etag,
		Entries: map[string]core.ProgressEntry{},
	}
	return p.writeAtomic("progress.json", &pf)
}

// MarkComplete records that <relPath> finished download + verify. Holds p.mu so
// concurrent download workers serialize their load-modify-write of progress.json.
func (p *progressStore) MarkComplete(relPath string, mtime time.Time, size int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pf, err := core.LoadProgressFromPath(filepath.Join(p.dir(), "progress.json"))
	if err != nil {
		return err
	}
	pf.Entries[relPath] = core.ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond)}
	return p.writeAtomic("progress.json", pf)
}

func (p *progressStore) writeAtomic(name string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	finalPath := filepath.Join(p.dir(), name)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
```

- [ ] **Step 2: Write the test**

Create `internal/providers/hypergryph/update_progress_test.go`:

```go
package hypergryph

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestProgressStore_InitAndMarkComplete(t *testing.T) {
	root := t.TempDir()
	ps := newProgressStore(root, "hypergryph/endfield", "1.2.6")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := ps.MarkComplete("data/a.bundle", time.Now(), 123); err != nil {
		t.Fatalf("mark: %v", err)
	}
	pf, err := core.LoadProgressFromPath(filepath.Join(ps.dir(), "progress.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e, ok := pf.Entries["data/a.bundle"]
	if !ok || e.Size != 123 {
		t.Errorf("entry = %+v, ok=%v", e, ok)
	}
	if pf.ETag != "etag-1" {
		t.Errorf("etag = %q", pf.ETag)
	}
}
```

- [ ] **Step 3: Run test**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestProgressStore -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/providers/hypergryph/update_progress.go internal/providers/hypergryph/update_progress_test.go
git commit -m "feat(m3c-b): progressStore sidecar I/O (mirror kurogames)"
```

---

## Task B4: apply lock (drop-in mirror)

**Files:**
- Create: `internal/providers/hypergryph/apply_lock.go`, `apply_lock_windows.go`, `apply_lock_other.go`
- Test: `internal/providers/hypergryph/apply_lock_windows_test.go`

- [ ] **Step 1: Create the three lock files**

`internal/providers/hypergryph/apply_lock.go` (copy of kuro's; package rename only):

```go
package hypergryph

// applyLock guards apply phase against concurrent game launches.
type applyLock interface {
	Acquire(gameDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
```

`internal/providers/hypergryph/apply_lock_windows.go` (copy of kuro's; package rename + `lockFileName` value `.omnigate_hg_update.lock`):

```go
//go:build windows

package hypergryph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const lockFileName = ".omnigate_hg_update.lock"

func platformApplyLock() applyLock {
	return &windowsApplyLock{}
}

type windowsApplyLock struct {
	file *os.File
}

func (w *windowsApplyLock) Acquire(gameDir string) error {
	if w.file != nil {
		return errors.New("applyLock already acquired")
	}
	lockPath := filepath.Join(gameDir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return fmt.Errorf("acquire apply lock: %w", err)
	}
	w.file = f
	return nil
}

func (w *windowsApplyLock) Release() error {
	if w.file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(w.file.Fd()), 0, 1, 0, &overlapped)
	err := w.file.Close()
	w.file = nil
	return err
}
```

`internal/providers/hypergryph/apply_lock_other.go` (copy of kuro's; package rename only):

```go
//go:build !windows

package hypergryph

import "errors"

func platformApplyLock() applyLock {
	return &stubApplyLock{}
}

type stubApplyLock struct {
	held bool
}

var stubLockSentinel = errors.New("applyLock stub: already held")

func (s *stubApplyLock) Acquire(gameDir string) error {
	if s.held {
		return stubLockSentinel
	}
	s.held = true
	return nil
}

func (s *stubApplyLock) Release() error {
	s.held = false
	return nil
}
```

- [ ] **Step 2: Write the test**

Create `internal/providers/hypergryph/apply_lock_windows_test.go`:

```go
//go:build windows

package hypergryph

import "testing"

func TestApplyLock_AcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Second exclusive acquire from a fresh lock on the same dir must fail.
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err == nil {
		t.Errorf("expected second acquire to fail")
		_ = l2.Release()
	}
	if err := l.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
```

- [ ] **Step 3: Run test**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestApplyLock -v`
Expected: PASS (Windows host).

- [ ] **Step 4: Commit**

```bash
git add internal/providers/hypergryph/apply_lock*.go
git commit -m "feat(m3c-b): apply lock (LockFileEx, mirror kurogames)"
```

---

## Task B5: process check (drop-in mirror)

**Files:**
- Create: `internal/providers/hypergryph/process_check_windows.go`, `process_check_other.go`
- Test: `internal/providers/hypergryph/process_check_test.go`

- [ ] **Step 1: Create the two process-check files**

`internal/providers/hypergryph/process_check_windows.go` (copy of kuro's; package rename only):

```go
//go:build windows

package hypergryph

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformIsProcessRunning(exeName string) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	target := strings.ToLower(exeName)
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(exe, target) {
			return true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return false
}
```

`internal/providers/hypergryph/process_check_other.go` (copy of kuro's; package rename only):

```go
//go:build !windows

package hypergryph

func platformIsProcessRunning(exeName string) bool {
	return false // stub: tests run on non-Windows; production is Windows-only
}
```

- [ ] **Step 2: Write the test**

Create `internal/providers/hypergryph/process_check_test.go`:

```go
package hypergryph

import "testing"

// TestPlatformIsProcessRunning_NotRunning asserts a clearly-absent exe returns
// false on every platform (Windows snapshot walk + non-Windows stub).
func TestPlatformIsProcessRunning_NotRunning(t *testing.T) {
	if platformIsProcessRunning("definitely-not-a-real-process-xyz.exe") {
		t.Errorf("expected false for a non-existent process")
	}
}
```

- [ ] **Step 3: Run test**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestPlatformIsProcessRunning -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/providers/hypergryph/process_check_windows.go internal/providers/hypergryph/process_check_other.go internal/providers/hypergryph/process_check_test.go
git commit -m "feat(m3c-b): Endfield.exe process check (mirror kurogames)"
```

---

## Task B6: download phase (drop-in mirror)

**Files:**
- Create: `internal/providers/hypergryph/update_download.go`
- Test: `internal/providers/hypergryph/update_download_test.go`

The downloader is byte-for-byte the kurogames `update_download.go` with `package kurogames` → `package hypergryph` (it already references `sanitizeURL` and `progressStore`, both of which exist in hypergryph). It is **per-file single-GET** (no byte-range resume — spec §4).

- [ ] **Step 1: Create update_download.go**

Create `internal/providers/hypergryph/update_download.go` as a copy of `internal/providers/kurogames/update_download.go` changing ONLY the package line to `package hypergryph`. (The file's `singleDownload` comment references "kurogames manifest hash algorithm"; leave it — MD5 is the same for Endfield. The `TODO(M3.A.v2): chunkInfos` comment is harmless.) The full content is:

```go
package hypergryph

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"omnigate/internal/core"
)

const (
	downloadWorkers = 4

	netRetries  = 3
	hashRetries = 2
)

var netBackoff = []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}

type downloader struct {
	client    *http.Client
	logger    *slog.Logger
	tempRoot  string
	progress  *progressStore
	plan      *core.UpdatePlan
	onEvent   func(core.UpdateEvent)
	bytesDone atomic.Int64
	clock     RetryClock
}

// RetryClock abstracts time for the download-retry backoff seam.
type RetryClock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
	Sleep(d time.Duration)
}

type realRetryClock struct{}

func (realRetryClock) Now() time.Time                         { return time.Now() }
func (realRetryClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (realRetryClock) Sleep(d time.Duration)                  { time.Sleep(d) }

func (d *downloader) runDownload(ctx context.Context) error {
	progress, _ := core.LoadProgress(d.progress.dir())
	type job struct {
		index int
		file  core.FileTask
	}

	if progress != nil {
		for _, f := range d.plan.Files {
			if e, ok := progress.Entries[f.Path]; ok {
				if e.Size == f.Size {
					d.bytesDone.Add(f.Size)
				}
			}
		}
	}

	jobCh := make(chan job, len(d.plan.Files))
	errCh := make(chan error, downloadWorkers)

	for w := 0; w < downloadWorkers; w++ {
		go func() {
			for j := range jobCh {
				if err := d.processFile(ctx, j.file); err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}

	for i, f := range d.plan.Files {
		select {
		case <-ctx.Done():
			close(jobCh)
			return ctx.Err()
		case jobCh <- job{index: i, file: f}:
		}
	}
	close(jobCh)

	var firstErr error
	for w := 0; w < downloadWorkers; w++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (d *downloader) processFile(ctx context.Context, f core.FileTask) error {
	finalPath := filepath.Join(d.progress.dir(), f.Path)

	progress, _ := core.LoadProgress(d.progress.dir())
	if progress != nil {
		if e, ok := progress.Entries[f.Path]; ok {
			if e.Size == f.Size {
				if fi, err := os.Stat(finalPath); err == nil && fi.Size() == f.Size && fi.ModTime().Truncate(time.Millisecond).Equal(e.MTime) {
					d.emitProgress(f.Path)
					return nil
				}
			}
		}
	}

	partPath := finalPath + ".part"
	_ = os.Remove(partPath)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(finalPath), err)
	}

	for attempt := 0; attempt <= netRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt > 0 {
			d.clock.Sleep(netBackoff[attempt-1])
		}
		if err := d.downloadAndVerify(ctx, f, partPath); err != nil {
			var ue *core.UpdateError
			if errors.As(err, &ue) {
				return ue
			}
			d.logger.Debug("download attempt failed", "path", f.Path, "attempt", attempt+1, "err", err)
			if attempt == netRetries {
				return &core.UpdateError{
					Code:      "network",
					Retryable: true,
					Params: map[string]string{
						"url":    sanitizeURL(f.URL),
						"reason": err.Error(),
					},
				}
			}
			continue
		}
		if err := os.Rename(partPath, finalPath); err != nil {
			return fmt.Errorf("rename %s: %w", finalPath, err)
		}
		fi, err := os.Stat(finalPath)
		if err != nil {
			return fmt.Errorf("stat post-rename %s: %w", finalPath, err)
		}
		if err := d.progress.MarkComplete(f.Path, fi.ModTime(), fi.Size()); err != nil {
			return fmt.Errorf("progress.MarkComplete: %w", err)
		}
		d.emitProgress(f.Path)
		return nil
	}
	return &core.UpdateError{Code: "internal", Retryable: true} // unreachable
}

func (d *downloader) downloadAndVerify(ctx context.Context, f core.FileTask, partPath string) error {
	for hashAttempt := 0; hashAttempt <= hashRetries; hashAttempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		gotHash, err := d.singleDownload(ctx, f.URL, partPath, f.Size)
		if err != nil {
			return err
		}
		if gotHash == f.Hash {
			return nil
		}
		d.logger.Debug("hash mismatch", "path", f.Path, "got", gotHash, "want", f.Hash, "attempt", hashAttempt+1)
		_ = os.Remove(partPath)
		if hashAttempt == hashRetries {
			return &core.UpdateError{
				Code:      "corrupt",
				Retryable: true,
				Params: map[string]string{
					"url":  sanitizeURL(f.URL),
					"path": f.Path,
				},
			}
		}
	}
	return &core.UpdateError{Code: "internal"} // unreachable
}

func (d *downloader) singleDownload(ctx context.Context, urlStr, partPath string, expectedSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	f, err := os.Create(partPath)
	if err != nil {
		return "", fmt.Errorf("create part: %w", err)
	}
	defer f.Close()

	h := md5.New()
	written := int64(0)
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			h.Write(buf[:n])
			written += int64(n)
			d.bytesDone.Add(int64(n))
			d.emitProgress("")
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if expectedSize > 0 && written != expectedSize {
		return "", fmt.Errorf("size mismatch: got %d, want %d", written, expectedSize)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (d *downloader) emitProgress(currentFile string) {
	if d.onEvent == nil {
		return
	}
	d.onEvent(core.UpdateEvent{
		Phase:       core.PhaseDownload,
		Current:     d.bytesDone.Load(),
		Total:       d.plan.TotalBytes,
		CurrentFile: currentFile,
	})
}
```

> **Delta vs kuro:** added an explicit 404 → `manifest_not_found` branch inside `singleDownload` (the per-file 404 / stranded-CDN signal, spec §4 + R1). Everything else is identical.

- [ ] **Step 2: Write the test**

Create `internal/providers/hypergryph/update_download_test.go`:

```go
package hypergryph

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

type instantClock struct{}

func (instantClock) Now() time.Time                         { return time.Now() }
func (instantClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (instantClock) Sleep(d time.Duration)                  {}

func md5hex(b []byte) string { h := md5.Sum(b); return hex.EncodeToString(h[:]) }

func newDownloaderFor(t *testing.T, srv *httptest.Server, plan *core.UpdatePlan) (*downloader, *progressStore) {
	t.Helper()
	root := t.TempDir()
	ps := newProgressStore(root, "hypergryph/endfield", plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatalf("progress init: %v", err)
	}
	d := &downloader{
		client:   srv.Client(),
		logger:   testLogger(),
		tempRoot: root,
		progress: ps,
		plan:     plan,
		clock:    instantClock{},
	}
	return d, ps
}

func TestDownload_SuccessAndMD5(t *testing.T) {
	payload := []byte("endfield-file-contents")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "data/a.bundle", Hash: md5hex(payload), Size: int64(len(payload)), URL: srv.URL + "/a"}},
		TotalBytes: int64(len(payload)),
	}
	d, ps := newDownloaderFor(t, srv, plan)
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.dir(), "data/a.bundle"))
	if err != nil || string(got) != string(payload) {
		t.Errorf("downloaded file mismatch: %q err=%v", got, err)
	}
}

func TestDownload_CorruptAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("wrong-bytes"))
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "a.bin", Hash: md5hex([]byte("expected")), Size: int64(len("wrong-bytes")), URL: srv.URL + "/a"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	err := d.runDownload(context.Background())
	var ue *core.UpdateError
	if err == nil || !asUpdateError(err, &ue) || ue.Code != "corrupt" {
		t.Fatalf("expected corrupt, got %v", err)
	}
}

func TestDownload_404NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "a.bin", Hash: "x", Size: 5, URL: srv.URL + "/a"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	err := d.runDownload(context.Background())
	var ue *core.UpdateError
	if err == nil || !asUpdateError(err, &ue) || ue.Code != "manifest_not_found" {
		t.Fatalf("expected manifest_not_found, got %v", err)
	}
}

func TestDownload_CancelMidway(t *testing.T) {
	// Handler writes one byte, cancels the ctx, then blocks until the request
	// ctx is done — so the download is interrupted mid-stream and the next
	// processFile attempt-loop iteration returns ctx.Err().
	cancelCh := make(chan func(), 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{0})
		if f := <-cancelCh; f != nil {
			f()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "big.bin", Hash: "x", Size: 1 << 20, URL: srv.URL + "/big"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	ctx, cancel := context.WithCancel(context.Background())
	cancelCh <- cancel
	err := d.runDownload(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
```

Add these helpers (if not already present in the package's test files) to `update_download_test.go`:

```go
import "errors"

func asUpdateError(err error, target **core.UpdateError) bool { return errors.As(err, target) }
```

And a shared `testLogger` helper — add to `update_download_test.go` if the package has none yet:

```go
import "log/slog"

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

(add `"io"` import). If a `testLogger` already exists elsewhere in the package's test files, omit this and reuse it.

- [ ] **Step 3: Run tests**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestDownload -v`
Expected: PASS (4 tests: success, corrupt, 404, cancel).

- [ ] **Step 4: Commit**

```bash
git add internal/providers/hypergryph/update_download.go internal/providers/hypergryph/update_download_test.go
git commit -m "feat(m3c-b): 4-worker per-file download + MD5 verify (mirror kurogames)"
```

---

## Task B7: preflight + apply phase + version writeback

**Files:**
- Create: `internal/providers/hypergryph/update_preflight.go`, `disk_space_windows.go`, `disk_space_other.go`
- Create: `internal/providers/hypergryph/update_apply.go`
- Modify: `internal/providers/hypergryph/crypto.go` (add encrypt)
- Modify: `internal/providers/hypergryph/version.go` (add `writeLocalVersion` + `setConfigVersion`)
- Test: `internal/providers/hypergryph/update_apply_test.go`, `crypto_test.go`, `update_preflight_test.go`

### B7a — crypto encrypt + config.ini writeback

- [ ] **Step 1: Write the failing crypto round-trip test**

Create `internal/providers/hypergryph/crypto_test.go`:

```go
package hypergryph

import "testing"

func TestAESRoundTrip(t *testing.T) {
	plain := []byte("[Game]\nversion=1.2.5\nentry=Endfield.exe\n")
	ct, err := encryptAESCBC(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ct) == 0 || len(ct)%16 != 0 {
		t.Fatalf("ciphertext length %d not a block multiple", len(ct))
	}
	got, err := decryptAESCBC(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("round-trip mismatch: %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestAESRoundTrip -v`
Expected: FAIL — `encryptAESCBC` undefined.

- [ ] **Step 3: Add encrypt + pkcs7Pad to crypto.go**

Append to `internal/providers/hypergryph/crypto.go`:

```go
// encryptAESCBC encrypts plaintext with the Endfield key/IV (AES-256-CBC + PKCS7).
// Inverse of decryptAESCBC — used for config.ini version writeback (spec §5/§6).
func encryptAESCBC(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	padded := pkcs7Pad(plaintext, bs)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, endfieldAESIV).CryptBlocks(out, padded)
	return out, nil
}

// pkcs7Pad appends PKCS7 padding to a multiple of blockSize.
func pkcs7Pad(b []byte, blockSize int) []byte {
	pad := blockSize - (len(b) % blockSize)
	out := make([]byte, len(b)+pad)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestAESRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Write the failing version-writeback test**

Append to `internal/providers/hypergryph/version_test.go`:

```go
func TestWriteLocalVersion_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := "[Game]\nversion=1.2.5\nentry=Endfield.exe\nchannel=6\n"
	ct, err := encryptAESCBC([]byte(orig))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalVersion(dir, "1.2.6"); err != nil {
		t.Fatalf("writeLocalVersion: %v", err)
	}
	got, err := readLocalVersion(dir)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != "1.2.6" {
		t.Errorf("version after writeback = %q, want 1.2.6", got)
	}
	// other keys preserved
	content, _ := decryptConfigFile(filepath.Join(dir, "config.ini"))
	if !strings.Contains(content, "entry=Endfield.exe") || !strings.Contains(content, "channel=6") {
		t.Errorf("other config keys lost: %q", content)
	}
}

func TestSetConfigVersion(t *testing.T) {
	in := "[Game]\nversion=1.0.0\nentry=x\n"
	out := setConfigVersion(in, "2.0.0")
	if !strings.Contains(out, "version=2.0.0") || strings.Contains(out, "1.0.0") {
		t.Errorf("setConfigVersion = %q", out)
	}
	// no version line → appended
	out2 := setConfigVersion("[Game]\nentry=x\n", "3.0.0")
	if !strings.Contains(out2, "version=3.0.0") {
		t.Errorf("setConfigVersion append = %q", out2)
	}
}
```

`version_test.go` already imports `"os"`, `"path/filepath"`, `"testing"` but **NOT `"strings"`** — add `"strings"` to its import block (the new tests use `strings.Contains`).

- [ ] **Step 6: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestWriteLocalVersion|TestSetConfigVersion' -v`
Expected: FAIL — `writeLocalVersion`, `setConfigVersion` undefined.

- [ ] **Step 7: Add writeLocalVersion + setConfigVersion to version.go**

Append to `internal/providers/hypergryph/version.go` (add imports `"os"`, `"fmt"` to the file; `"path/filepath"` + `"strings"` already present):

```go
// writeLocalVersion re-encrypts <installPath>/config.ini with the `version=`
// line replaced by newVersion. Decrypt → setConfigVersion → re-encrypt → atomic
// write. Spec §5/§6 (omnigate-original; the reference copies a server-supplied
// config.ini.new instead). Caller treats failure as non-fatal (the apply already
// succeeded) but should log it.
func writeLocalVersion(installPath, newVersion string) error {
	configPath := filepath.Join(installPath, "config.ini")
	content, err := decryptConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("decrypt config.ini: %w", err)
	}
	if content == "" {
		// No existing config.ini to update — nothing to write back (degrade, §6).
		return fmt.Errorf("config.ini missing or empty; cannot write version")
	}
	updated := setConfigVersion(content, newVersion)
	ct, err := encryptAESCBC([]byte(updated))
	if err != nil {
		return fmt.Errorf("encrypt config.ini: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, ct, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// setConfigVersion replaces the `version=` line in decrypted config.ini text,
// preserving all other lines. If no version line exists, one is appended.
func setConfigVersion(content, newVersion string) string {
	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "version=") {
			lines[i] = "version=" + newVersion
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "version="+newVersion)
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 8: Run crypto + version tests**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestAESRoundTrip|TestWriteLocalVersion|TestSetConfigVersion' -v`
Expected: PASS.

### B7b — preflight (disk space + same volume)

- [ ] **Step 9: Create the preflight files**

`internal/providers/hypergryph/update_preflight.go`:

```go
package hypergryph

import (
	"path/filepath"
	"strconv"

	"omnigate/internal/core"
)

// preflightSameVolume returns *core.UpdateError{cross_volume_temp} when tempDir
// and gameDir are on different volumes (atomic rename across volumes fails).
// Distinct from the mid-apply validateSameVolume → cross_volume_midrun.
func preflightSameVolume(tempDir, gameDir string) error {
	if filepath.VolumeName(tempDir) != filepath.VolumeName(gameDir) {
		return &core.UpdateError{
			Code:      "cross_volume_temp",
			Retryable: false,
			Params:    map[string]string{"temp_vol": filepath.VolumeName(tempDir), "game_vol": filepath.VolumeName(gameDir)},
		}
	}
	return nil
}

// checkDiskSpace returns *core.UpdateError{disk_full} when the volume holding
// dir has less than needed free bytes. A stat failure is treated as best-effort
// pass (don't block the update on an unreadable free-space query).
func checkDiskSpace(dir string, needed int64) error {
	free, err := platformFreeDiskBytes(dir)
	if err != nil {
		return nil
	}
	if free < needed {
		return &core.UpdateError{
			Code:      "disk_full",
			Retryable: false,
			Params:    map[string]string{"need_bytes": strconv.FormatInt(needed, 10), "free_bytes": strconv.FormatInt(free, 10)},
		}
	}
	return nil
}
```

`internal/providers/hypergryph/disk_space_windows.go`:

```go
//go:build windows

package hypergryph

import "golang.org/x/sys/windows"

// platformFreeDiskBytes returns the free bytes available to the caller on the
// volume containing dir (GetDiskFreeSpaceEx lpFreeBytesAvailableToCaller).
func platformFreeDiskBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeAvail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(freeAvail), nil
}
```

`internal/providers/hypergryph/disk_space_other.go`:

```go
//go:build !windows

package hypergryph

// platformFreeDiskBytes stub for non-Windows test runners: report a huge value
// so checkDiskSpace passes (production is Windows-only).
func platformFreeDiskBytes(dir string) (int64, error) {
	return 1 << 62, nil
}
```

- [ ] **Step 10: Write the preflight test**

Create `internal/providers/hypergryph/update_preflight_test.go`:

```go
package hypergryph

import (
	"errors"
	"math"
	"testing"

	"omnigate/internal/core"
)

func TestPreflightSameVolume(t *testing.T) {
	if err := preflightSameVolume(`C:\temp`, `C:\Games\X`); err != nil {
		t.Errorf("same volume should pass: %v", err)
	}
	err := preflightSameVolume(`C:\temp`, `D:\Games\X`)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_temp" {
		t.Errorf("cross volume should be cross_volume_temp, got %v", err)
	}
}

func TestCheckDiskSpace_Full(t *testing.T) {
	dir := t.TempDir()
	err := checkDiskSpace(dir, math.MaxInt64)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "disk_full" {
		t.Errorf("expected disk_full for impossible size, got %v", err)
	}
	if err := checkDiskSpace(dir, 1); err != nil {
		t.Errorf("1 byte should fit: %v", err)
	}
}
```

- [ ] **Step 11: Run preflight tests**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestPreflightSameVolume|TestCheckDiskSpace' -v`
Expected: PASS.

### B7c — apply phase

- [ ] **Step 12: Create update_apply.go**

Create `internal/providers/hypergryph/update_apply.go` as a mirror of kuro's `update_apply.go` with these deltas: (a) `package hypergryph`; (b) version writeback calls `writeLocalVersion(a.gameDir, a.plan.Version)` instead of `writeLauncherConfigVersion`; (c) drop the `writeLauncherConfigVersion` function. Full content:

```go
package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"omnigate/internal/core"
)

// applyWAL is the on-disk shape of apply.wal. Embeds the manifest snapshot so
// recovery doesn't need to re-fetch from network.
type applyWAL struct {
	GameID   string   `json:"game_id"`
	Version  string   `json:"version"`
	ETag     string   `json:"etag"`
	WasPredl bool     `json:"was_predl"`
	Pending  []string `json:"pending"`
	Done     []string `json:"done"`
}

type applier struct {
	logger   *slog.Logger
	tempRoot string
	gameDir  string
	progress *progressStore
	plan     *core.UpdatePlan
	wasPredl bool
	onEvent  func(core.UpdateEvent)
	lock     applyLock
}

func (a *applier) runApply(ctx context.Context) error {
	a.logger.Debug("runApply: enter", "game", a.plan.GameID, "files", len(a.plan.Files), "version", a.plan.Version, "game_dir", a.gameDir)

	if err := validateSameVolume(a.tempRoot, a.gameDir); err != nil {
		a.logger.Warn("runApply: validateSameVolume failed", "game", a.plan.GameID, "err", err)
		return err
	}

	lockDir := a.progress.dir()
	if err := a.lock.Acquire(lockDir); err != nil {
		a.logger.Warn("runApply: applyLock acquire failed", "game", a.plan.GameID, "lock_dir", lockDir, "err", err)
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "lock_held", "reason": err.Error()},
		}
	}
	defer a.lock.Release()

	pending := make([]string, len(a.plan.Files))
	for i, f := range a.plan.Files {
		pending[i] = f.Path
	}
	wal := applyWAL{
		GameID:   string(a.plan.GameID),
		Version:  a.plan.Version,
		ETag:     a.plan.ManifestETag,
		WasPredl: a.wasPredl,
		Pending:  pending,
		Done:     []string{},
	}
	walPath := filepath.Join(a.progress.dir(), "apply.wal")
	if err := writeWALAtomic(walPath, &wal); err != nil {
		return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"reason": err.Error()}}
	}
	if err := fsyncFile(walPath); err != nil {
		a.logger.Warn("apply.wal fsync failed; proceeding", "err", err)
	}

	_ = os.Remove(filepath.Join(a.progress.dir(), "progress.json"))

	var done atomic.Int64
	for _, f := range a.plan.Files {
		src := filepath.Join(a.progress.dir(), f.Path)
		dst := filepath.Join(a.gameDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": f.Path, "reason": err.Error()}}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": f.Path, "reason": err.Error()}}
		}
		wal.Done = append(wal.Done, f.Path)
		wal.Pending = removeString(wal.Pending, f.Path)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if a.onEvent != nil {
			a.onEvent(core.UpdateEvent{Phase: core.PhaseApply, Current: done.Load(), Total: int64(len(a.plan.Files)), CurrentFile: f.Path})
		}
	}

	// Persist new version to config.ini (AES re-encrypt) so subsequent
	// CheckVersion sees Current = Latest. Unconditional (mirrors kuro): even a
	// 0-file apply must update the version or Refresh re-flags AvailableUpdate
	// and BottomBar bounces back to [更新遊戲] (spec §6/B3). Non-fatal: files
	// are already in place.
	a.logger.Debug("runApply: writing config.ini version", "game_dir", a.gameDir, "new_version", a.plan.Version)
	if err := writeLocalVersion(a.gameDir, a.plan.Version); err != nil {
		a.logger.Warn("runApply: config.ini version writeback failed (apply otherwise succeeded)", "err", err, "game_dir", a.gameDir)
	} else {
		a.logger.Info("runApply: config.ini version written", "game_dir", a.gameDir, "version", a.plan.Version)
	}

	if err := os.Remove(walPath); err != nil {
		a.logger.Warn("remove apply.wal", "err", err)
	}

	_ = a.lock.Release()
	if err := os.RemoveAll(a.progress.dir()); err != nil {
		a.logger.Warn("cleanup version dir post-apply", "dir", a.progress.dir(), "err", err)
	}
	return nil
}

// resumeApply replays apply.wal: re-applies any Pending entries a prior crash
// didn't finish. Uses the WAL's manifest snapshot so no network call.
func resumeApply(ctx context.Context, walPath, gameDir string, lock applyLock, onEvent func(core.UpdateEvent), logger *slog.Logger) error {
	body, err := os.ReadFile(walPath)
	if err != nil {
		return &core.UpdateError{Code: "unrecoverable", Params: map[string]string{"reason": err.Error()}}
	}
	var wal applyWAL
	if err := json.Unmarshal(body, &wal); err != nil {
		return &core.UpdateError{Code: "unrecoverable", Params: map[string]string{"reason": err.Error()}}
	}

	if err := lock.Acquire(gameDir); err != nil {
		return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "lock_held"}}
	}
	defer lock.Release()

	tempDir := filepath.Dir(walPath)
	var done atomic.Int64
	done.Store(int64(len(wal.Done)))
	total := int64(len(wal.Done) + len(wal.Pending))

	for _, rel := range wal.Pending {
		src := filepath.Join(tempDir, rel)
		dst := filepath.Join(gameDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel}}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{Code: "apply_partial", Retryable: true, Params: map[string]string{"path": rel, "reason": err.Error()}}
		}
		wal.Done = append(wal.Done, rel)
		wal.Pending = removeString(wal.Pending, rel)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if onEvent != nil {
			onEvent(core.UpdateEvent{Phase: core.PhaseApply, Current: done.Load(), Total: total, CurrentFile: rel})
		}
	}
	_ = os.Remove(walPath)
	return nil
}

// validateSameVolume returns *core.UpdateError{cross_volume_midrun} when tempDir
// and gameDir resolve to different VolumeName values.
func validateSameVolume(tempDir, gameDir string) error {
	tempVol := filepath.VolumeName(tempDir)
	gameVol := filepath.VolumeName(gameDir)
	if tempVol != gameVol {
		return &core.UpdateError{
			Code:      "cross_volume_midrun",
			Retryable: false,
			Params:    map[string]string{"temp_vol": tempVol, "game_vol": gameVol},
		}
	}
	return nil
}

func atomicRename(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if isEXDEV(err) {
		return fmt.Errorf("cross-volume rename %s → %s: %w", src, dst, err)
	}
	return err
}

func isEXDEV(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "different drive") || strings.Contains(s, "not same device") || strings.Contains(s, "cross-device")
}

func writeWALAtomic(walPath string, wal *applyWAL) error {
	body, err := json.MarshalIndent(wal, "", "  ")
	if err != nil {
		return err
	}
	tmp := walPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, walPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func removeString(s []string, target string) []string {
	out := s[:0]
	for _, x := range s {
		if x != target {
			out = append(out, x)
		}
	}
	return out
}
```

> **Note (parity):** `resumeApply` is carried verbatim from kuro for parity but, like kuro, is **not wired into `RunUpdate`** — crash recovery re-runs `RunUpdate` (the App `update_handler.go` recovery path), whose download phase skips already-complete files via `progress.json` and whose apply re-renames. `resumeApply` is therefore unexercised in both providers; it is kept so the package matches kuro and to support a future direct-resume wiring. No test is required for it.

- [ ] **Step 13: Write the apply test**

Create `internal/providers/hypergryph/update_apply_test.go`:

```go
package hypergryph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestApply_RenamesFilesAndWritesVersion(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir() // same volume as temp on CI/dev
	// Seed an encrypted config.ini at the old version.
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\nentry=Endfield.exe\n"))
	if err := os.WriteFile(filepath.Join(game, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "data/a.bundle", Hash: "x", Size: 3}},
	}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	// Place the "downloaded" staged file under the version dir.
	if err := os.MkdirAll(filepath.Join(ps.dir(), "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "data/a.bundle"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	// File landed in game dir.
	if b, err := os.ReadFile(filepath.Join(game, "data/a.bundle")); err != nil || string(b) != "abc" {
		t.Errorf("applied file = %q err=%v", b, err)
	}
	// config.ini version written back.
	if v, _ := readLocalVersion(game); v != "1.2.6" {
		t.Errorf("config.ini version = %q, want 1.2.6", v)
	}
	// version dir cleaned up.
	if _, err := os.Stat(ps.dir()); !os.IsNotExist(err) {
		t.Errorf("version dir not cleaned up")
	}
}

func TestApply_CrossVolumeMidrunRejected(t *testing.T) {
	err := validateSameVolume(`C:\temp\omnigate`, `D:\Games\Endfield`)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_midrun" {
		t.Errorf("expected cross_volume_midrun, got %v", err)
	}
	if err := validateSameVolume(`C:\temp`, `C:\Games`); err != nil {
		t.Errorf("same volume should pass: %v", err)
	}
}

// TestApply_ZeroFilesStillWritesVersion: an already-current (0-file) apply must
// STILL write the new version back to config.ini so the next CheckVersion shows
// up-to-date and the UI doesn't bounce to [更新] (spec §5/§6/B3, kuro parity).
func TestApply_ZeroFilesStillWritesVersion(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir()
	ct, _ := encryptAESCBC([]byte("[Game]\nversion=1.2.5\n"))
	if err := os.WriteFile(filepath.Join(game, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6", Files: nil}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if v, _ := readLocalVersion(game); v != "1.2.6" {
		t.Errorf("version after 0-file apply = %q, want 1.2.6", v)
	}
}

// TestApply_NoConfigIni_NonFatal: a missing config.ini (degrade, §6) must NOT
// fail the apply — the version writeback is a logged no-op.
func TestApply_NoConfigIni_NonFatal(t *testing.T) {
	temp := t.TempDir()
	game := t.TempDir() // no config.ini seeded
	plan := &core.UpdatePlan{GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6", Files: nil}
	ps := newProgressStore(temp, string(plan.GameID), plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatal(err)
	}
	a := &applier{logger: testLogger(), tempRoot: temp, gameDir: game, progress: ps, plan: plan, lock: newApplyLock()}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("apply must succeed even without config.ini (degrade §6): %v", err)
	}
}
```

- [ ] **Step 14: Run all B7 tests + whole package**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/...`
Expected: PASS (all package tests green).

- [ ] **Step 15: Commit**

```bash
git add internal/providers/hypergryph/crypto.go internal/providers/hypergryph/crypto_test.go internal/providers/hypergryph/version.go internal/providers/hypergryph/version_test.go internal/providers/hypergryph/update_preflight.go internal/providers/hypergryph/disk_space_windows.go internal/providers/hypergryph/disk_space_other.go internal/providers/hypergryph/update_preflight_test.go internal/providers/hypergryph/update_apply.go internal/providers/hypergryph/update_apply_test.go
git commit -m "feat(m3c-b): preflight + WAL apply + config.ini AES version writeback"
```

---

## Task B8: Provider integration (hypergryph.go)

**Files:**
- Modify: `internal/providers/hypergryph/hypergryph.go`
- Test: `internal/providers/hypergryph/update_integration_test.go` (extend)

Adds `CheckForUpdate` / `CheckForUpdateWithProgress` / `RunUpdate` / `IsGameRunning` + interface assertions, mirroring kuro `kurogames.go` but using the Option-A flow (config.ini local version, game_files manifest, per-file plan). Also adds a `clock` field and bumps the http client timeout for downloads.

- [ ] **Step 1: Write the failing integration test (httptest end-to-end)**

Replace the contents of `internal/providers/hypergryph/update_integration_test.go` with:

```go
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

	p := New(Settings{Path: game}, testLogger())
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
	p := New(Settings{Path: root, TempDir: filepath.Join(root, "_temp")}, testLogger())
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
	p := New(Settings{Path: game}, testLogger())
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
```

> The first test (`checkForUpdateAt`) drives the check logic against a known install path; the end-to-end test drives the full `DetectInstall → CheckForUpdateWithProgress → RunUpdate` wiring (spec §9.2). Step 3 introduces `checkForUpdateAt` and makes `CheckForUpdateWithProgress` a thin wrapper over it.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestIntegration_CheckForUpdate -v`
Expected: FAIL — `checkForUpdateAt`, `New(...).checkForUpdateAt` undefined (and `New` signature/fields unchanged).

- [ ] **Step 3: Rewrite the Provider for updates**

In `internal/providers/hypergryph/hypergryph.go`: (a) update imports to add `"os"` and `"path/filepath"`; (b) add `clock` to the struct + a download-grade client; (c) add the update methods + interface assertions. Replace the struct + `New` (lines 17-28) with:

```go
type Provider struct {
	settings Settings
	logger   *slog.Logger
	client   *http.Client
	clock    RetryClock
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		settings: settings,
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Minute}, // download-grade; get_latest also fine
		clock:    realRetryClock{},
	}
}
```

Then append the update methods + assertions to the end of `hypergryph.go`:

```go
// IsGameRunning implements core.ProcessChecker (1st-point game-running guard).
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
	exe, ok := p.ExeName(gid)
	if !ok {
		return false, nil
	}
	return platformIsProcessRunning(exe), nil
}

// CheckForUpdate is a thin wrapper passing a nil verify-progress callback.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	return p.CheckForUpdateWithProgress(ctx, gid, nil)
}

// CheckForUpdateWithProgress resolves the install path then runs the Option-A
// flow. onProgress fires during the local per-file MD5 verify.
func (p *Provider) CheckForUpdateWithProgress(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	installPath, err := p.installPathFor(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	return p.checkForUpdateAt(ctx, gid, installPath, onProgress)
}

// checkForUpdateAt is the core CheckForUpdate logic against a known install path
// (extracted for testability). Spec §3.1 decision tree.
func (p *Provider) checkForUpdateAt(ctx context.Context, gid core.GameID, installPath string, onProgress func(done, total int)) (core.UpdatePlan, error) {
	// 1. Local version (config.ini AES; "" if unreadable — degrade §6).
	curVer, _ := readLocalVersion(installPath)

	// 2. get_latest.
	rsp, err := fetchGetLatest(ctx, p.client, curVer)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// 3. Staleness — version compare authoritative; action==1 only on degrade.
	switch {
	case curVer != "" && curVer == rsp.Version:
		// up-to-date → empty plan.
		return core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: rsp.Version, ManifestETag: rsp.Version, Reason: core.ReasonVersionChanged}, nil
	case curVer == "" && rsp.Action != 1:
		// degrade + no update signal → up-to-date.
		return core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: rsp.Version, ManifestETag: rsp.Version, Reason: core.ReasonVersionChanged}, nil
	}

	// 4. Fetch + parse game_files manifest from the per-file CDN.
	if rsp.Pkg.FilePath == "" {
		return core.UpdatePlan{}, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"reason": "no pkg.file_path"}}
	}
	nodes, err := fetchGameFilesManifest(ctx, p.client, rsp.Pkg.FilePath)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// 5. Filter to changed files (heavy local MD5 verify; onProgress per file).
	files := filterChangedFiles(ctx, installPath, rsp.Pkg.FilePath, nodes, p.logger, onProgress)
	if ctx.Err() != nil {
		return core.UpdatePlan{}, ctx.Err()
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}

	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanUpdate,
		ManifestETag: rsp.Version,
		Version:      rsp.Version,
		Files:        files,
		TotalBytes:   totalBytes,
		Reason:       core.ReasonVersionChanged,
	}
	p.logger.Info("hypergryph CheckForUpdate complete", "game", gid, "local_version", curVer, "target_version", rsp.Version, "files_to_update", len(files), "bytes", totalBytes)
	return plan, nil
}

// RunUpdate executes a previously-checked plan: re-verify version → preflight →
// process guard → download → apply. Spec §3.2.
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("RunUpdate panic", "game", plan.GameID, "panic", r)
			err = &core.UpdateError{Code: "internal", Retryable: true, Params: map[string]string{"detail": fmt.Sprint(r)}}
		}
	}()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	g := findByID(plan.GameID)
	if g == nil {
		return fmt.Errorf("%w: %s", core.ErrUnknownGame, plan.GameID)
	}
	installPath, err := p.installPathFor(ctx, plan.GameID)
	if err != nil {
		return err
	}

	// Process guard.
	if platformIsProcessRunning(g.ExeName) {
		return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "process_running", "game": string(plan.GameID)}}
	}

	// Re-verify version (manifest_changed).
	if cur, ferr := fetchGetLatest(ctx, p.client, ""); ferr == nil && cur.Version != "" && cur.Version != plan.ManifestETag {
		return &core.UpdateError{Code: "manifest_changed", Retryable: true, Params: map[string]string{"old": plan.ManifestETag, "new": cur.Version}}
	}

	// Resolve temp root (matches app.tempDirFor hypergryph default).
	tempDir := p.settings.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "omnigate", "hypergryph")
	}

	// Preflight: same-volume + disk space.
	if perr := preflightSameVolume(tempDir, installPath); perr != nil {
		return perr
	}
	if perr := checkDiskSpace(tempDir, plan.TotalBytes); perr != nil {
		return perr
	}

	progress := newProgressStore(tempDir, string(plan.GameID), plan.Version)
	if err := progress.Init(plan.ManifestETag); err != nil {
		return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
	}

	d := &downloader{client: p.client, logger: p.logger, tempRoot: tempDir, progress: progress, plan: &plan, onEvent: onEvent, clock: p.clock}
	if err := d.runDownload(ctx); err != nil {
		return err
	}

	a := &applier{logger: p.logger, tempRoot: tempDir, gameDir: installPath, progress: progress, plan: &plan, wasPredl: false, onEvent: onEvent, lock: newApplyLock()}
	return a.runApply(ctx)
}

// installPathFor resolves the install path for gid via DetectInstall.
func (p *Provider) installPathFor(ctx context.Context, gid core.GameID) (string, error) {
	if findByID(gid) == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return "", err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return ig.InstallPath, nil
		}
	}
	return "", fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}
```

Finally, extend the interface-assertion block at the end of `hypergryph.go`:

```go
var (
	_ core.Provider               = (*Provider)(nil)
	_ core.PathProvider           = (*Provider)(nil)
	_ core.ExeNamer               = (*Provider)(nil)
	_ core.Updater                = (*Provider)(nil)
	_ core.CheckForUpdateProgress = (*Provider)(nil)
	_ core.ProcessChecker         = (*Provider)(nil)
)
```

(Remove the old 3-line `var (...)` assertion block to avoid a duplicate.)

- [ ] **Step 4: Run the integration test + whole package**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run 'TestIntegration|TestCheckForUpdate_Degrade' -v && CGO_ENABLED=0 go test ./internal/providers/hypergryph/...`
Expected: PASS (FiltersChangedFiles, RunUpdate_EndToEnd, Degrade + whole package).

- [ ] **Step 5: Whole-repo build**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./internal/providers/hypergryph/`
Expected: clean (the App layer now sees hypergryph implement core.Updater/ProcessChecker — already type-asserted at RPC entry, no app change needed beyond B1).

- [ ] **Step 6: Commit**

```bash
git add internal/providers/hypergryph/hypergryph.go internal/providers/hypergryph/update_integration_test.go
git commit -m "feat(m3c-b): provider Updater/ProcessChecker integration (Option A flow)"
```

---

## Task B9: errcode coverage + crash-recovery wiring

**Files:**
- Create: `internal/providers/hypergryph/errcode_coverage_test.go`
- Verify (read-only): `internal/app/update_handler.go` scanForRecovery `knownBackendIDs`

- [ ] **Step 1: Create errcode_coverage_test.go**

Create `internal/providers/hypergryph/errcode_coverage_test.go` (mirror kuro; the M3.C catalog = the codes the provider actually emits per spec §7):

```go
package hypergryph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrcodeCoverage asserts each error code the provider emits (spec §7) has
// a referencing .go file under this package or internal/app/. Mirrors kuro.
func TestErrcodeCoverage(t *testing.T) {
	codes := []string{
		"manifest_changed", "manifest_not_found", "network", "auth_failed",
		"corrupt", "disk_full", "cross_volume_temp", "cross_volume_midrun",
		"process_blocked", "apply_partial", "unrecoverable", "internal",
	}
	content := allGoFilesContent(t)
	for _, code := range codes {
		if !strings.Contains(content, `"`+code+`"`) {
			t.Errorf("error code %q has no source reference", code)
		}
	}
}

func allGoFilesContent(t *testing.T) string {
	t.Helper()
	roots := []string{".", "../../app"}
	var sb strings.Builder
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
				return nil
			}
			if body, rerr := os.ReadFile(path); rerr == nil {
				sb.Write(body)
				sb.WriteByte('\n')
			}
			return nil
		})
	}
	return sb.String()
}
```

> Catalog excludes `interrupted_resume*` (App-derived, not provider-emitted — spec §7) and `unsupported_filesystem` (not emitted by the Option-A path). Every listed code is emitted by B2/B6/B7/B8 production code, so the test passes without relying on test-file-only references.

- [ ] **Step 2: Run + verify each code is really emitted (not just catalog-referenced)**

Run: `CGO_ENABLED=0 go test ./internal/providers/hypergryph/ -run TestErrcodeCoverage -v`
Expected: PASS.

Then verify each code appears in a NON-test `.go` file:

Run: `cd /c/Users/willie/Repos/omnigate; for c in manifest_changed manifest_not_found network auth_failed corrupt disk_full cross_volume_temp cross_volume_midrun process_blocked apply_partial unrecoverable internal; do n=$(grep -rl "\"$c\"" internal/providers/hypergryph/*.go | grep -v _test.go | wc -l); echo "$c: $n"; done`
Expected: every code ≥ 1.

- [ ] **Step 3: Verify the recovery scanner covers hypergryph (read-only)**

Run: `grep -n "knownBackendIDs\|registerProvider\|scanForRecovery" internal/app/update_handler.go internal/app/app.go | head`
Expected: `knownBackendIDs()` (`update_handler.go:~685`) is built **dynamically from the registered providers** (`a.providers`), and `constructProviders` already calls `registerProvider(gryph)` (`app.go:113`). So hypergryph is covered automatically — **no edit needed**. Just confirm the registration is present; the WAL/progress sidecars hypergryph writes under `tempDirFor("hypergryph", gid)` are what the scanner reads, and the recovery bell-drawer flow is otherwise App-layer + unchanged.

- [ ] **Step 4: Commit**

```bash
git add internal/providers/hypergryph/errcode_coverage_test.go
git commit -m "test(m3c-b): errcode coverage (provider-emitted codes only)"
```

---

## Task B10: Frontend — predl-hidden assertion + i18n parity

**Files:**
- Verify: `frontend/src/components/BottomBar.vue` predl gating fields
- Test: `frontend/src/__tests__/` (new or extend an existing Vitest spec)

- [ ] **Step 1: Confirm the predl gating fields (read-only)**

Run: `grep -n "available_predl\|availablePredl\|has_predownload\|hasPredownload\|AvailablePredl" frontend/src/components/BottomBar.vue frontend/src/stores/*.ts | head -30`
Expected: the predl button visibility derives from a snapshot field like `available_predl` and/or `has_predownload`. Note the exact field names + the `v-if`/computed that gates the predl button — the test asserts the button is absent when both are falsy.

- [ ] **Step 2: Write the failing Vitest**

Create `frontend/src/__tests__/predl_hidden_hypergryph.test.ts` (adapt the import paths + the snapshot shape to what Step 1 found):

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import BottomBar from '../components/BottomBar.vue'
import en from '../locales/en.json'

// A Hypergryph game snapshot with an available update but NO predownload.
function hypergryphState() {
  return {
    backend: 'hypergryph',
    game_id: 'hypergryph/endfield',
    available_update: { version: '1.2.6' },
    available_predl: null, // hypergryph never sets this
    has_predownload: false, // hypergryph does not implement predlExposer
    in_flight: null,
    last_error: null,
  }
}

describe('BottomBar predl suppression for Hypergryph', () => {
  it('does not render the predownload button', () => {
    setActivePinia(createPinia())
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
    const wrapper = mount(BottomBar, {
      global: { plugins: [i18n] },
      props: { state: hypergryphState() }, // adapt to BottomBar's real props/store wiring
    })
    // Adapt the selector to BottomBar's predl button (data-testid recommended).
    expect(wrapper.find('[data-testid="predl-button"]').exists()).toBe(false)
  })
})
```

> **Implementer note:** BottomBar likely reads from the updates Pinia store rather than a `state` prop. In that case, seed the store (e.g. `useUpdatesStore().byGame['hypergryph/endfield'] = hypergryphState()`) instead of passing a prop, and mount with no props. If the predl button has no stable selector, add `data-testid="predl-button"` to the button element in `BottomBar.vue` (a harmless, test-only attribute) as part of this task. Match the existing M3.A Vitest patterns in `frontend/src/__tests__/`.

- [ ] **Step 3: Run to verify it fails (if selector/seed needs adjusting) then passes**

Run: `cd frontend && npx vitest run src/__tests__/predl_hidden_hypergryph.test.ts`
Expected: After adapting the seed/selector to the real BottomBar wiring, PASS (button absent).

- [ ] **Step 4: i18n parity — confirm no new keys needed**

Run: `cd frontend && npx vitest run` (the full suite, incl. `i18n_parity.test.ts`)
Expected: PASS. M3.C adds NO new i18n keys (spec §7/§8 — all error codes reuse the existing `update.errors.*` set). If `i18n_parity.test.ts` fails for a missing key, that means a code path emits an unlisted code — fix by reusing an existing code, do NOT add a key.

- [ ] **Step 5: Frontend build**

Run: `cd frontend && npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/__tests__/predl_hidden_hypergryph.test.ts frontend/src/components/BottomBar.vue
git commit -m "test(m3c-b): assert predl button hidden for Hypergryph"
```

---

## Task B11 (USER): full smoke + ship

**This task requires a real Endfield install and the user — subagents cannot click the GUI or hit the live CDN.**

- [ ] **Step 1: Whole-repo green**

Run:
```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"; cd /c/Users/willie/Repos/omnigate
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
cd frontend && npm run build && npx vitest run
```
Expected: all green.

- [ ] **Step 2: Build the production binary**

Run: `cd /c/Users/willie/Repos/omnigate && wails build`
Expected: `build/bin/omnigate.exe` produced.

- [ ] **Step 3: USER smoke checklist (real Endfield install, spec §9.3)**

1. Launcher shows `就緒 · v<X.Y>` for Endfield at the latest version → BottomBar shows [開始遊戲].
2. Fake-stale: re-encrypt `config.ini` with an older `version=` (use a throwaway Go snippet calling `encryptAESCBC`, or the smoke helper) → Refresh → [更新遊戲] appears.
3. Click [更新遊戲] → "驗證本地檔案 X / Y" (per-file MD5 verify) progress visible.
4. Download progress visible (bytes + current file).
5. Cancel × mid-download → ctx canceled, state returns to idle, no partial apply.
6. Real run to completion: per-file download + atomic apply + config.ini version writeback → sidebar returns to `就緒 · v<latest>`, no bounce back to [更新遊戲].
7. i18n zh-TW ↔ en toggle, no missing-key fallback.
8. Crash-recovery: kill mid-download, relaunch → bell drawer prompts resume → OK runs full resume to completion.
9. **R1 confirm:** the per-file CDN served every `game_files` path for the real delta (no mid-run 404 / `manifest_not_found`). If 404s occur, capture which paths → decide `protocol_unsupported` fallback vs investigation.
10. Program-Files admin-writeback check: if Endfield is under `C:\Program Files`, confirm config.ini writeback behavior (admin needed; clear error if non-elevated).

- [ ] **Step 4: Tag + merge (after smoke passes)**

```bash
cd /c/Users/willie/Repos/omnigate
git tag v0.5.0-m3c
git checkout main
git merge --no-ff m3c/spec
```
(no `Co-Authored-By` trailer — `feedback_commits`). Then mark M3.C SHIPPED in memory (`project_status.md` + `project_m3c_endfield_update.md`).

---

## Self-Review (writing-plans checklist)

**Spec coverage:**
- §0.4 file-level incremental → B2 (game_files manifest + filterChangedFiles), B6 (per-file download), B7 (atomic rename, no zip/hdiff). ✅
- §1.1 file responsibilities → B2–B8 cover update_manifest/crypto/download/apply/progress/lock/process_check. ✅
- §1.6 App-layer TempDir plumbing → B1. ✅
- §3.1 CheckForUpdate decision tree (version-compare staleness, degrade) → B8 `checkForUpdateAt`. ✅
- §3.2 RunUpdate (re-verify → preflight → process → download → apply) → B8 `RunUpdate`. ✅
- §3.4 predl suppression (zero-action) → B8 (implements neither AvailablePredl nor predlExposer) + B10 (Vitest). ✅
- §5 apply + config.ini AES writeback (unconditional, incl. 0-file apply) → B7 (`TestApply_ZeroFilesStillWritesVersion`). ✅
- §6 version writeback + degrade → B7 (`writeLocalVersion` errors non-fatally — `TestApply_NoConfigIni_NonFatal`) + B8 degrade branch (`TestCheckForUpdate_DegradeNoLocalVersion`). ✅
- §7 error catalog (incl. `auth_failed`; preflight `disk_full`/`cross_volume_temp` now really emitted; `interrupted_resume*` App-derived) → B7/B9. ✅
- §9 tests: manifest decrypt + filterChangedFiles (B2), crypto round-trip (B7a), per-file download incl. 404 + **cancel** (B6), apply + writeback + 0-file + no-config (B7), preflight (B7b), **end-to-end `RunUpdate` httptest** with `pkg.file_path` → test server (B8 `TestIntegration_RunUpdate_EndToEnd`), degrade branch (B8). ✅

**Placeholder scan:** no `TODO`/`TBD`/"implement later" in shipped code. The two `data-testid`/store-seed notes in B10 are genuine adapt-to-real-wiring instructions (the frontend's exact BottomBar prop/store shape is confirmed in B10 Step 1 before writing the test), not placeholders. ✅

**Type consistency:** `manifestNode{Path,MD5,Size}`, `fileURL(filePath, relPath)`, `filterChangedFiles(ctx, installDir, filePath, nodes, logger, onProgress)`, `writeLocalVersion(installPath, newVersion)`, `setConfigVersion(content, newVersion)`, `encryptAESCBC`/`decryptAESCBC`, `preflightSameVolume`/`checkDiskSpace`/`platformFreeDiskBytes`, `downloader`/`applier` structs, `progressStore`, `RetryClock` — all defined once and referenced consistently. `getLatestResponse.Pkg.FilePath` added in B2, consumed in B8. `core.FileTask{Path,Hash,Size,URL}`, `core.UpdatePlan{GameID,Kind,ManifestETag,Version,Files,TotalBytes,Reason}`, `core.UpdateEvent{Phase,Current,Total,CurrentFile}` match the kuro usage verified against the existing source. ✅

**Known cross-task dependency:** B6/B7/B8 all reference `testLogger()` — defined once in B6's `update_download_test.go`. If subagents execute out of order, ensure `testLogger` exists before B7/B8 tests run (it's in the package test scope).
