# M3.C Phase A — Endfield Protocol Spike + Version Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate the Endfield `get_latest` protocol against a real install and make the `hypergryph` provider report a real installed version + latest version (replacing the M2 empty-string stub), so the sidebar shows `就緒 · v<X.Y>`. This is the de-risking first half of M3.C; Phase B (download + apply) builds on the protocol this phase confirms.

**Architecture:** A protocol research spike resolves the three open unknowns and lands sanitized fixtures. Then a small `get_latest` HTTP client + a rewritten `version.go` populate `core.VersionInfo{Current, Latest}` from the GRYPHLINK launcher CDN. No download, no apply, no `core.Updater` — those are Phase B. The existing sidebar UI renders the version with no frontend changes.

**Tech Stack:** Go 1.26 (`net/http`, `encoding/json`, `crypto/md5`, `regexp`), Wails v2. `CGO_ENABLED=0` (no `-race`).

**Spec:** `docs/superpowers/specs/2026-06-02-omnigate-m3c-endfield-update-design.md` (§0.5, §2, §6).
**Reference (mirror source):** `internal/providers/kurogames/` (M3.A — `version.go`, `update_manifest.go`).
**Phase B plan:** `docs/superpowers/plans/2026-06-02-omnigate-m3c-b-download-apply.md` (written after Phase A smoke).

**Toolchain (subagent shells without Go on PATH):**
`export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`

---

## File Structure (Phase A)

| File | New/Mod | Responsibility |
|---|---|---|
| `docs/superpowers/research/m3c-endfield-update-protocol.md` | new | Protocol facts + TEST_ANCHOR + resolved unknowns |
| `internal/providers/hypergryph/testdata/get_latest-sample.json` | new | Sanitized live `get_latest` (no version) |
| `internal/providers/hypergryph/testdata/get_latest-uptodate-sample.json` | new | Sanitized live `get_latest` (with version) |
| `internal/providers/hypergryph/update_manifest.go` | new | `get_latest` client + response structs + `sanitizeURL` + `SetAPIBaseURL` seam |
| `internal/providers/hypergryph/crypto.go` | new | AES-256-CBC decrypt + reverse-engineered key/IV (Collapse plugin, attributed) for config.ini / game_files |
| `internal/providers/hypergryph/version.go` | **mod** | Decrypt config.ini → `version=` (local) + `Latest` from `get_latest` |
| `internal/providers/hypergryph/hypergryph.go` | **mod** | Give `CheckVersion` an `*http.Client`; no new interfaces |
| Tests | new | `update_manifest_test.go`, `version_test.go`, `m3c_protocol_doc_test.go`, `sanitize_url_fuzz_test.go`, `update_integration_test.go` |

**Out of Phase A (→ Phase B):** `Settings.TempDir`, `packsToFileTasks`/`filterChangedFiles`, `core.Updater` (`CheckForUpdate`/`RunUpdate`), `update_download.go`, `update_apply.go`, `update_progress.go`, `apply_lock*`, `process_check*`, the in-app update button.

---

## Task A1: Protocol research spike

**Files:**
- Create: `docs/superpowers/research/m3c-endfield-update-protocol.md`
- Create: `internal/providers/hypergryph/testdata/get_latest-sample.json`
- Create: `internal/providers/hypergryph/testdata/get_latest-uptodate-sample.json`

Research + documentation only — no production Go. Primary source: the public archive `daydreamer-json/ak-endfield-api-archive` (`gh api repos/daydreamer-json/ak-endfield-api-archive/contents/<path> --jq '.content' | base64 -d`). Two live GETs confirm the up-to-date shape. **The local-version-source step (Step 2) needs a real Endfield install — coordinate with the user if this subagent has no install access.**

- [ ] **Step 1: Write the known protocol facts into the research doc.**

Create `docs/superpowers/research/m3c-endfield-update-protocol.md` documenting (verified during spec writing):
- Endpoint `GET https://launcher.gryphline.com/api/game/get_latest` params `appcode, launcher_appcode, channel, sub_channel, launcher_sub_channel, version` (`version` optional).
- Constants (global/os): game `YDUTE5gscDZ229CW`; launcher `TiaytKBUIEdoEwRT`; channel `6`; sub_channel `6`; launcher_sub_channel `6`. CDN `beyond.hg-cdn.com`.
- Response shape: `action`(int), `state`(int), `launcher_action`(int), `version`(str=latest), `client_version`(str|null), `request_version`(str), `pkg{packs:[{url,md5,package_size}],total_size,...}`, `patch{...}` (ignored).
- Hash = MD5. Locked: consume `pkg.packs[]` (full), ignore `patch`.

Add a TEST_ANCHOR block with the pack-URL regex Phase B will validate:

```
<!-- TEST_ANCHOR: pack_url_regex -->
^https://beyond\.hg-cdn\.com/[A-Za-z0-9]+/[0-9.]+/update/\d+/\d+/Windows/[0-9.]+_[A-Za-z0-9]+/packs/.+\.zip\.\d{3}$
<!-- END_ANCHOR: pack_url_regex -->
```

- [ ] **Step 2: Resolve OPEN QUESTION 1 — local installed-version source.**

On a real Endfield install, investigate in order and document which works (M2 found none pre-release):
1. A launcher-written JSON under the game dir holding a version matching `rsp.version` (e.g. `1.2.5`).
2. Registry `HKCU\Software\Hypergryph\…\Endfield` (or `HKLM`).
3. A sidecar under `%LOCALAPPDATA%` / the GRYPHLINK launcher dir.

Document the chosen source + exact path/key + parse shape under `## Local version source`. **If none is reliable, document that** — Task A3 then ships the graceful-degrade path (`Current` empty; `Latest` still shown).

- [ ] **Step 3: Live GET (no version) — confirm package path is live + capture fixture.**

```bash
curl -s -A "omnigate/0.5" "https://launcher.gryphline.com/api/game/get_latest?appcode=YDUTE5gscDZ229CW&launcher_appcode=TiaytKBUIEdoEwRT&channel=6&sub_channel=6&launcher_sub_channel=6" > /tmp/gl.json
```
Confirm non-empty `.rsp`/`pkg.packs[]` (or top-level depending on observed shape — document the actual envelope). Sanitize (redact any account/device IDs; keep structure) → `testdata/get_latest-sample.json`. Document observed `action/state/launcher_action`. Empty packs ⇒ package path stranded; flag for Phase B `protocol_unsupported`.

- [ ] **Step 4: Live GET (with version=latest) — capture up-to-date discriminator.**

Re-GET with `&version=<latest from Step 3>`; observe how `action/state/launcher_action`/`pkg.packs` change. Save sanitized → `testdata/get_latest-uptodate-sample.json`. Document the up-to-date discriminator under `## Up-to-date discriminator` (Phase B's CheckForUpdate uses it).

- [ ] **Step 5: Commit.**

```bash
git add docs/superpowers/research/m3c-endfield-update-protocol.md internal/providers/hypergryph/testdata/
git commit -m "research(m3c): Endfield get_latest protocol + fixtures + local version source"
```

**Outputs to record at the top of the research doc (Phase A/B read these):**
- `LOCAL_VERSION_SOURCE = <path/registry-key | "none (degrade)">`
- `UPTODATE_DISCRIMINATOR = <e.g. pkg.packs empty>`
- `PACKAGE_PATH_LIVE = <yes/no>`
- `RESPONSE_ENVELOPE = <flat rsp{...} | nested {rsp:{...}}>` (whatever the live body shows)

---

## Task A2: `get_latest` client + response structs + `sanitizeURL`

> **Step 0 (research-derived corrections):** Open the research doc. (1) Match the struct tags below to the **actual** field names + envelope in `testdata/get_latest-sample.json`; if the live body nests under a `rsp` object, wrap `getLatestResponse` in an envelope struct and unwrap in `fetchGetLatest`. (2) Inspect a real pack URL in the fixture: confirm the `<ver>_<rand>/packs/` shape matches `randSegRe`; **if the live URLs also carry an account ID or device ID** (Endfield's `get_latest` is unauthenticated, but verify), port kurogames' `accountIDRe`/`deviceIDRe` redaction into `sanitizeURL` and extend `TestSanitizeURL_*` to assert those tokens are gone. `gameAppCode` here has the same string value as `bg.go`'s existing `endfieldGameFolder` const (the CDN game-folder == launcher appcode) — that duplication is intentional; do NOT dedupe them into one symbol (different call sites).

**Files:**
- Create: `internal/providers/hypergryph/update_manifest.go`
- Test: `internal/providers/hypergryph/update_manifest_test.go`

- [ ] **Step 1: Write the failing test.**

Create `internal/providers/hypergryph/update_manifest_test.go`:

```go
package hypergryph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
```

- [ ] **Step 2: Run it; verify it fails to compile.**

Run: `go test ./internal/providers/hypergryph/ -run 'TestParseGetLatest|TestFetchGetLatest|TestSanitizeURL'`
Expected: FAIL — `undefined: decodeGetLatest` etc.

- [ ] **Step 3: Write `update_manifest.go`.**

```go
package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"omnigate/internal/core"
)

// Endfield Global (region "os") launcher API constants. Verified against
// daydreamer-json/ak-endfield-api-archive — see the research doc.
const (
	gameAppCode        = "YDUTE5gscDZ229CW"
	launcherAppCode    = "TiaytKBUIEdoEwRT"
	apiChannel         = "6"
	apiSubChannel      = "6"
	apiLauncherSubChan = "6"
	defaultAPIBase     = "https://launcher.gryphline.com/api"
)

// apiBase is overridable in tests via SetAPIBaseURL. NOTE: this is a
// PACKAGE-LEVEL var + package-level func, intentionally NOT hoyoverse's
// method form (`func (p *Provider) SetAPIBaseURL`). The package-level form is
// chosen so package tests (update_manifest_test / version_test) can override
// without constructing a Provider. Do NOT copy hoyoverse's method signature —
// the tests below call `SetAPIBaseURL(...)` as a bare function.
var apiBase = defaultAPIBase

// SetAPIBaseURL overrides the launcher API base for integration tests. Pass ""
// to reset to production.
func SetAPIBaseURL(base string) {
	if base == "" {
		apiBase = defaultAPIBase
		return
	}
	apiBase = base
}

// randSegRe matches the per-build "<ver>_<rand>" path segment for redaction.
var randSegRe = regexp.MustCompile(`(/[0-9.]+_)[A-Za-z0-9]{8,}(/)`)

func sanitizeURL(s string) string {
	return randSegRe.ReplaceAllString(s, "${1}<RAND>${2}")
}

// getLatestResponse is the get_latest body (see research doc). Phase A uses
// only Version; Phase B adds pkg/pack consumption.
type getLatestResponse struct {
	Action         int    `json:"action"`
	State          int    `json:"state"`
	LauncherAction int    `json:"launcher_action"`
	Version        string `json:"version"`
	ClientVersion  string `json:"client_version"`
	RequestVersion string `json:"request_version"`
	Pkg            struct {
		Packs []struct {
			URL         string `json:"url"`
			MD5         string `json:"md5"`
			PackageSize string `json:"package_size"`
		} `json:"packs"`
		TotalSize string `json:"total_size"`
	} `json:"pkg"`
}

// decodeGetLatest unmarshals a get_latest body. If Task A1 found the live body
// nests under {"rsp":{...}}, add an envelope here per Step 0.
func decodeGetLatest(body []byte) (*getLatestResponse, error) {
	var out getLatestResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("get_latest parse: %w", err)
	}
	return &out, nil
}

func getLatestURL(version string) string {
	q := url.Values{}
	q.Set("appcode", gameAppCode)
	q.Set("launcher_appcode", launcherAppCode)
	q.Set("channel", apiChannel)
	q.Set("sub_channel", apiSubChannel)
	q.Set("launcher_sub_channel", apiLauncherSubChan)
	if version != "" {
		q.Set("version", version)
	}
	return apiBase + "/game/get_latest?" + q.Encode()
}

// fetchGetLatest GETs get_latest and parses it. Status → core.UpdateError
// (404 manifest_not_found; 401/403 auth_failed; 5xx network).
func fetchGetLatest(ctx context.Context, client *http.Client, version string) (*getLatestResponse, error) {
	urlStr := getLatestURL(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "omnigate/0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get_latest fetch: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &core.UpdateError{Code: "auth_failed", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode/100 == 5:
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode), "reason": "server error"}}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return decodeGetLatest(body)
}
```

- [ ] **Step 4: Run tests; verify pass.**

Run: `go test ./internal/providers/hypergryph/ -run 'TestParseGetLatest|TestFetchGetLatest|TestSanitizeURL'`
Expected: PASS (`TestParseGetLatest` Skips only if the Task A1 fixture is absent — it must be present).

- [ ] **Step 5: Commit.**

```bash
git add internal/providers/hypergryph/update_manifest.go internal/providers/hypergryph/update_manifest_test.go
git commit -m "feat(m3c-a): get_latest client + response structs + sanitizeURL + SetAPIBaseURL seam"
```

---

## Task A3: `version.go` rewrite — real local + latest version

> **RESOLVED (Task A1 breakthrough):** `LOCAL_VERSION_SOURCE = <installPath>/config.ini`, AES-256-CBC encrypted (PKCS7). Decrypt → INI text → read the `version=` line. Verified live → `version=1.2.5`. The AES key/IV are a reverse-engineered constant already public in the Collapse plugin `misaka10843/Hi3Helper.Plugin.Hypergryph` (`HgCrypto.cs`); hardcode with attribution (like kurogames `AppCred`). Staleness = local `version` != `get_latest.version`. The degrade path (spec §6) is now only a fallback for when config.ini is absent/unreadable.

**Files:**
- Create: `internal/providers/hypergryph/crypto.go` (AES-256-CBC decrypt helper + key/IV)
- Modify: `internal/providers/hypergryph/version.go` (read+decrypt config.ini → `version=`)
- Modify: `internal/providers/hypergryph/hypergryph.go` (give `CheckVersion` an `*http.Client`)
- Modify: `internal/providers/hypergryph/version_test.go` (**file already exists** — see Step 1)

- [ ] **Step 1: Replace the existing `version_test.go`.**

⚠️ `version_test.go` ALREADY EXISTS with `TestFetchVersion_AlwaysEmpty_M2Limitation`, which calls the OLD 3-arg `fetchVersion(ctx, path, gid)`. Step 3 changes `fetchVersion` to 4 args, so that test must be **removed** (its "always empty" premise is exactly what Phase A deletes) or the build breaks. **Replace the entire file contents** with:

```go
package hypergryph

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReadLocalVersion_FromConfigIni(t *testing.T) {
	dir := t.TempDir()
	writeEncryptedConfig(t, dir, "1.2.3")
	got, err := readLocalVersion(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "1.2.3" {
		t.Fatalf("got %q want 1.2.3", got)
	}
}

func TestReadLocalVersion_MissingReturnsEmpty(t *testing.T) {
	got, err := readLocalVersion(t.TempDir())
	if err != nil {
		t.Fatalf("missing should not error: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestFetchVersion_PopulatesLatestFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"action": 1, "version": "1.2.9", "pkg": map[string]any{"packs": []any{}}})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	dir := t.TempDir()
	writeEncryptedConfig(t, dir, "1.2.3")
	vi, err := fetchVersion(context.Background(), srv.Client(), dir, "endfield/global")
	if err != nil {
		t.Fatalf("fetchVersion: %v", err)
	}
	if vi.Current != "1.2.3" || vi.Latest != "1.2.9" {
		t.Fatalf("got %+v", vi)
	}
}

// TestFetchVersion_NoLocalSource_Degrades covers spec §6: when config.ini is
// absent, Current stays "" (sidebar shows 就緒 with no suffix) and Latest is
// still populated from get_latest.
func TestFetchVersion_NoLocalSource_Degrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"action": 1, "version": "1.2.9", "pkg": map[string]any{"packs": []any{}}})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	vi, err := fetchVersion(context.Background(), srv.Client(), t.TempDir(), "endfield/global")
	if err != nil {
		t.Fatalf("degrade should not error: %v", err)
	}
	if vi.Current != "" {
		t.Fatalf("degrade: Current must be empty (spec §6), got %q", vi.Current)
	}
	if vi.Latest != "1.2.9" {
		t.Fatalf("degrade: Latest should be populated, got %q", vi.Latest)
	}
}

// writeEncryptedConfig writes a real AES-256-CBC-encrypted config.ini (same
// key/IV as crypto.go) so readLocalVersion exercises the actual decrypt path —
// no committed binary fixture, fully deterministic.
func writeEncryptedConfig(t *testing.T, dir, version string) {
	t.Helper()
	plain := []byte("[Game]\nversion=" + version + "\nentry=Endfield.exe\n")
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		t.Fatal(err)
	}
	bs := block.BlockSize()
	pad := bs - len(plain)%bs
	padded := append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, endfieldAESIV).CryptBlocks(ct, padded)
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run it; verify it fails to compile.**

Run: `go test ./internal/providers/hypergryph/ -run 'TestReadLocalVersion|TestFetchVersion'`
Expected: FAIL — `undefined: endfieldAESKey`, `readLocalVersion`, etc.

- [ ] **Step 3a: Create `crypto.go`** (AES-256-CBC decrypt + the reverse-engineered key/IV, with attribution).

```go
package hypergryph

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"os"
)

// Endfield stores config.ini and the game_files manifest AES-256-CBC encrypted
// (PKCS7). The key + IV are a reverse-engineered constant ALREADY PUBLICLY
// PUBLISHED in the Collapse Launcher plugin
// "misaka10843/Hi3Helper.Plugin.Hypergryph"
// (Hi3Helper.Hypergryph.Core/Utils/HgCrypto.cs). They are hardcoded here, with
// attribution, ONLY to interoperate with the official GRYPHLINK launcher's
// local config format — mirroring how kurogames hardcodes its reverse-engineered
// AppCred. No secret is newly disclosed. If omnigate is published and this draws
// concern, switch to build-time/runtime injection.
var (
	endfieldAESKey = []byte{
		0xC0, 0xF3, 0x0E, 0x1C, 0xE7, 0x63, 0xBB, 0xC2, 0x1C, 0xC3, 0x55, 0xA3, 0x43, 0x03, 0xAC, 0x50,
		0x39, 0x94, 0x44, 0xBF, 0xF6, 0x8C, 0x4A, 0x22, 0xAF, 0x39, 0x8C, 0x0A, 0x16, 0x6E, 0xE1, 0x43,
	}
	endfieldAESIV = []byte{
		0x33, 0x46, 0x78, 0x61, 0x19, 0x27, 0x50, 0x64, 0x95, 0x01, 0x93, 0x72, 0x64, 0x60, 0x84, 0x00,
	}
)

// decryptAESCBC decrypts AES-256-CBC + PKCS7 ciphertext with the Endfield key/IV.
func decryptAESCBC(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%bs != 0 {
		return nil, fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, endfieldAESIV).CryptBlocks(out, ciphertext)
	return pkcs7Unpad(out, bs)
}

// decryptConfigFile reads + AES-decrypts a file to a UTF-8 string. Missing file
// → ("", nil). Decrypt failure → error.
func decryptConfigFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := decryptAESCBC(data)
	if err != nil {
		return "", fmt.Errorf("hypergryph: decrypt %s: %w", path, err)
	}
	return string(plain), nil
}

func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty plaintext")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > blockSize || pad > len(b) {
		return nil, fmt.Errorf("invalid pkcs7 padding %d", pad)
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid pkcs7 padding bytes")
		}
	}
	return b[:len(b)-pad], nil
}
```

- [ ] **Step 3b: Rewrite `version.go`** (decrypt config.ini → `version=`).

```go
package hypergryph

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// readLocalVersion reads <installPath>/config.ini (AES-256-CBC encrypted; see
// crypto.go) and returns its `version=` value. Missing config.ini → ("", nil)
// (unknown, not an error). Mirrors Collapse ConfigTool.cs + HgGameManager.cs.
func readLocalVersion(installPath string) (string, error) {
	content, err := decryptConfigFile(filepath.Join(installPath, "config.ini"))
	if err != nil {
		return "", err
	}
	return parseConfigVersion(content), nil
}

// parseConfigVersion extracts the `version=` value from decrypted config.ini.
func parseConfigVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "version="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// fetchVersion returns Current (local config.ini) + Latest (get_latest.version).
// Latest fetch failure is non-fatal (Latest falls back to Current). If config.ini
// is absent/unreadable, Current is empty (degrade, spec §6) — never blocks.
func fetchVersion(ctx context.Context, client *http.Client, installPath string, _ core.GameID) (core.VersionInfo, error) {
	cur, err := readLocalVersion(installPath)
	if err != nil {
		return core.VersionInfo{}, err
	}
	latest := cur
	if resp, ferr := fetchGetLatest(ctx, client, ""); ferr == nil && resp.Version != "" {
		latest = resp.Version
	}
	if cur == "" {
		return core.VersionInfo{Latest: latest}, nil
	}
	return core.VersionInfo{Current: cur, Latest: latest}, nil
}
```

- [ ] **Step 4: Update `hypergryph.go` to pass an `*http.Client` to `fetchVersion`.**

Add a client field to `Provider` (or use a package default). Minimal change — in `hypergryph.go`, add to the struct and `New`:

```go
type Provider struct {
	settings Settings
	logger   *slog.Logger
	client   *http.Client
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{settings: settings, logger: logger, client: &http.Client{Timeout: 30 * time.Second}}
}
```

Update the `CheckVersion` method body to pass `p.client`:

```go
return fetchVersion(ctx, p.client, ig.InstallPath, gid)
```

Update the import block (current imports are only `context`, `fmt`, `log/slog`, `omnigate/internal/core`) to add `net/http` and `time`:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"omnigate/internal/core"
)
```

- [ ] **Step 5: Run tests + build; verify pass.**

Run: `go test ./internal/providers/hypergryph/ && go build ./...`
Expected: PASS, build clean.

- [ ] **Step 6: Commit.**

```bash
git add internal/providers/hypergryph/version.go internal/providers/hypergryph/version_test.go internal/providers/hypergryph/hypergryph.go
git commit -m "feat(m3c-a): real Endfield version detection (local + latest via get_latest)"
```

---

## Task A4: Test seams — protocol-doc drift + sanitizeURL fuzz + integration skip

**Files:**
- Create: `internal/providers/hypergryph/m3c_protocol_doc_test.go`
- Create: `internal/providers/hypergryph/sanitize_url_fuzz_test.go`
- Create: `internal/providers/hypergryph/update_integration_test.go`

- [ ] **Step 1: Protocol-doc drift test (mirrors kurogames `m3a_protocol_doc_test.go`).**

```go
package hypergryph

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestProtocolDocPackURLRegex asserts the TEST_ANCHOR regex in the research
// doc compiles and matches a representative pack URL — detects drift between
// the documented protocol and the code's expectations.
func TestProtocolDocPackURLRegex(t *testing.T) {
	docPath := "../../../docs/superpowers/research/m3c-endfield-update-protocol.md"
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("research markdown missing: %v", err)
	}
	re := regexp.MustCompile(`(?s)<!-- TEST_ANCHOR: pack_url_regex -->\s*\n(.*?)<!-- END_ANCHOR: pack_url_regex -->`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		t.Fatal("pack_url_regex TEST_ANCHOR not found")
	}
	pat := strings.TrimSpace(m[1])
	if pat == "" {
		t.Fatal("pack_url_regex TEST_ANCHOR block is empty")
	}
	compiled, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("doc regex does not compile: %v", err)
	}
	sample := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/Beyond_Release.zip.001"
	if !compiled.MatchString(sample) {
		t.Fatalf("doc pack_url_regex does not match sample pack URL")
	}
}
```

- [ ] **Step 2: sanitizeURL fuzz (mirrors kurogames `sanitize_url_fuzz_test.go`).**

```go
package hypergryph

import (
	"strings"
	"testing"
)

func FuzzSanitizeURL(f *testing.F) {
	f.Add("https://beyond.hg-cdn.com/X/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/x.zip.001")
	f.Add("")
	f.Add("not a url")
	f.Fuzz(func(t *testing.T, s string) {
		out := sanitizeURL(s)
		// Invariant: output never longer-by-leak; redaction is idempotent.
		if sanitizeURL(out) != out {
			t.Fatalf("sanitizeURL not idempotent: %q -> %q -> %q", s, out, sanitizeURL(out))
		}
		_ = strings.TrimSpace(out)
	})
}
```

- [ ] **Step 3: Integration skip placeholder + SetAPIBaseURL seam doc.**

```go
package hypergryph

import "testing"

// Live get_latest E2E is exercised by the Phase A smoke (Task A6) against the
// real GRYPHLINK CDN. The SetAPIBaseURL seam (update_manifest.go) lets Phase B
// run download/apply against an httptest server; placeholder kept for parity.
func TestIntegration_LiveGetLatest(t *testing.T) {
	t.Skip("live E2E covered by Phase A smoke; see m3c-endfield-update-protocol.md")
}
```

- [ ] **Step 4: Run; verify pass.**

Run: `go test ./internal/providers/hypergryph/ && go test -run xxx -fuzz FuzzSanitizeURL -fuzztime 5s ./internal/providers/hypergryph/`
Expected: tests PASS; fuzz runs 5s with no crash.

- [ ] **Step 5: Commit.**

```bash
git add internal/providers/hypergryph/m3c_protocol_doc_test.go internal/providers/hypergryph/sanitize_url_fuzz_test.go internal/providers/hypergryph/update_integration_test.go
git commit -m "test(m3c-a): protocol-doc drift + sanitizeURL fuzz + integration skip"
```

---

## Task A5: Whole-repo verification

**Files:** none (verification only).

- [ ] **Step 1: Vet + full test suite.**

Run: `go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: vet clean; all packages PASS (hypergryph gains the new tests).

- [ ] **Step 2: Frontend build (no frontend changes expected this phase).**

Run: `cd frontend && npm run build`
Expected: build clean. (Version display uses the existing sidebar `VersionInfo` rendering — confirm by reading `SidebarRow.vue`; if the `· vX.Y` suffix needs the `Latest!=Current` indicator wired and it's missing, that is Phase B's update-button work, NOT Phase A.)

- [ ] **Step 3: Wails build sanity.**

Run: `wails build` (or note skip if environment lacks it)
Expected: `build/bin/omnigate.exe` produced.

No commit (verification only).

---

## Task A6 (USER): Phase A smoke + checkpoint

**This task requires the user — subagents can't drive the GUI or guarantee a real Endfield install.**

- [ ] **Step 1:** Run `wails dev` (or the built binary) with a real Endfield install configured (Settings → Hypergryph path = the GRYPHLINK/Endfield game dir).
- [ ] **Step 2:** Confirm version display. **If a local source was found:** the Endfield row shows `就緒 · v<X.Y>` with the **correct installed version** (matches what the GRYPHLINK launcher reports). **If the spike found no local source (degrade, spec §6):** the row shows `就緒` with **no** `· vX.Y` suffix, no error.
- [ ] **Step 3:** Toggle offline (or block the CDN) and confirm `CheckVersion` degrades gracefully (no crash). With a local source: shows local version (`Latest` falls back to `Current`). Without a local source: shows `就緒` with no suffix.
- [ ] **Step 4:** Confirm the live `get_latest` `version` matches the current public Endfield version (validates the protocol end-to-end).
- [ ] **Step 5 (checkpoint):** With the protocol validated on a real install, decide with the user:
  - Tag this point (e.g. `v0.5.0-m3c-a`) and/or keep on `m3c/spec`.
  - Proceed to write **Phase B** (`docs/superpowers/plans/2026-06-02-omnigate-m3c-b-download-apply.md`) using the confirmed `LOCAL_VERSION_SOURCE` / `UPTODATE_DISCRIMINATOR` / response envelope.

---

## Self-Review (Phase A)

- **Spec coverage:** §0.5 (server latest) → A2/A3; §2 protocol/constants → A1/A2; §6 version detection + degrade → A3; §0.6 research doc + TEST_ANCHOR → A1/A4. Download/apply/error-catalog/predl (§3–§5,§7) are intentionally Phase B.
- **Placeholder scan:** the only `<SPIKE: …>` placeholders are in Task A3 consts, gated explicitly by Task A1's Step 0 — they are resolved before A3 runs, by design (mirrors M3.A's "research-derived corrections").
- **Type consistency:** `getLatestResponse.Version`, `fetchGetLatest(ctx,client,version)`, `fetchVersion(ctx,client,installPath,gid)`, `readLocalVersion(installPath)`, `SetAPIBaseURL`, `versionFileName`/`localVersionField` are consistent across A2/A3/tests.
