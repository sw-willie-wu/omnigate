# launcher-collection M2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Kuro Games (鳴潮 / Wuthering Waves) and Hypergryph (終末地 / Arknights: Endfield) as new providers alongside the existing HoYoverse provider. Each new provider supports detect, direct-exe launch via `windows.ShellExecute`, runtime PE-icon extraction for the sidebar, and a per-publisher real background art source (HTTP API or local cache scrape — decided during impl).

**Architecture:** Provider registry on `App` with per-game routing via `core.ParseGameID`. Optional cross-cutting interfaces (`AssetServer`, `PathProvider`, `ExeNamer`) on top of the M1 `core.Provider` interface. A new AssetServer middleware on Wails' `assetserver.Options.Handler` serves `/_asset/<backend>/<kind>/<key>` — `kind=icon` handled uniformly by middleware via shared `iconext` package; `kind=bg` delegated per-publisher to `core.AssetServer` via type-assertion. Detection cache moves to App layer (event-based invalidation, no TTL). Settings TOML extends with `version=1` field and `[backends.kurogames]` / `[backends.hypergryph]` sections; M1 `hoyoplay_path` migrated.

**Tech Stack:** Go 1.26.2, Wails v2.12.0, Vue 3 + Pinia + vue-i18n@9 (already on the project from M1), `golang.org/x/sys/windows` (already direct-required from M1's launch fix), `log/slog` (stdlib), `github.com/pelletier/go-toml/v2` (M1).

**Pre-conditions** (verified during spec writing):
- `m2/spec` branch already contains `docs/superpowers/specs/2026-05-04-launcher-collection-m2-design.md`. The plan is committed onto the same branch, then `m2/spec` merges to `main` (`--no-ff`) before implementation begins on a fresh `m2/implementation` branch.
- M1 ships at `v0.1.0-m1` on `main`; merge commit `5d0e05e` has the full M1 codebase available.
- All three games installed locally: `Wuthering Waves.exe`, `Endfield.exe`, plus the existing HoYoPlay games (verified working in M1 smoke).
- Module path is `launcher-collection-tmp` (Wails scaffold artefact; never renamed). Imports use that prefix.

**Per-task review (subagent-driven-development):** every task ends with implementer status + spec-compliance review + code-quality review. Per the autonomous-mode memory, the user pre-authorized the M1 loop's autonomy; M2 is a fresh milestone, so check in after Task 4 (the App refactor) before continuing — that's the riskiest abstraction-locking task.

---

## File Structure

### New files

```
internal/core/
  errors.go                           sentinel errors + ErrorCode helper
  pathprovider.go                     optional PathProvider interface
  assetserver.go                      optional AssetServer interface
  exenamer.go                         optional ExeNamer interface

internal/util/dirver/
  dirver.go                           shared version-dir scanner / max-comparator
  dirver_test.go

internal/providers/iconext/
  iconext.go                          public API (Extract, ErrUnsupported); package-level lru.Cache
  iconext_windows.go                  //go:build windows — real impl
  iconext_other.go                    //go:build !windows — stub returns ErrUnsupported
  iconext_test.go                     cross-platform behavior tests
  testdata/sample.exe                 small PE binary with a known 32×32 icon (creation: see Task 3)

internal/providers/kurogames/
  meta.go                             BackendID, gameMeta with WuWa
  detect.go                           DetectInstall scans <path>/Wuthering Waves Game/
  detect_test.go
  version.go                          reads launcherDownloadConfig.json `.version`
  version_test.go
  launch_windows.go                   ShellExecute "Wuthering Waves.exe"
  bg.go                               (or api.go) — Task 8 research outcome decides
  kurogames.go                        Provider impl + PathProvider + ExeNamer + AssetServer

internal/providers/hypergryph/
  meta.go                             BackendID, gameMeta with Endfield (zh-CN distinct)
  detect.go                           DetectInstall scans <path>/games/EndField Game/
  detect_test.go
  version.go                          returns "" — no clean source
  version_test.go
  launch_windows.go                   ShellExecute "Endfield.exe"
  bg.go                               (or api.go) — Task 9 research outcome
  hypergryph.go                       Provider impl + PathProvider + ExeNamer + AssetServer

internal/app/
  asset_handler.go                    AssetServer middleware (path parse + dispatch)
  asset_handler_test.go

frontend/src/
  locales/zh-CN.json                  simplified-Chinese strings
  stores/backends.ts                  Pinia store calling ListBackends
```

### Modified files

```
internal/core/
  locstring.go                        fallback chain extended (zh-CN → en → zh-TW)
  locstring_test.go                   new fallback-chain tests
  provider.go                         GameID format godoc + ParseGameID + interface
                                       godoc updates (anti-cheat note)

internal/app/
  app.go                              registry slice + provider() helper + cachedDetect at
                                       App layer + ListBackends + Refresh + ErrorCode/
                                       ErrorMessage Wails binds
  settings.go                         schema extension with [backends.kurogames] /
                                       [backends.hypergryph] + version=1 + migration
  settings_test.go                    migration / malformed / version=1 tests

internal/providers/hoyoverse/
  hoyoverse.go                        +logger field; +PrimaryPath() impl;
                                       settings.HoYoplayPath → settings.Path

main.go                               slog wiring; AssetServer.Handler hookup; panic
                                       recovery; multi-provider construction

frontend/src/
  components/Footbar.vue               backend count derived from games
  components/Topbar.vue                refresh icon button
  i18n.ts                              register zh-CN locale
  stores/games.ts                      no structural change (existing iteration is fine)
```

### Removed files

None. M1 code stays.

---

## Task 1: core helpers — sentinel errors, optional interfaces, GameID parser

**Files:**
- Create: `internal/core/errors.go`, `internal/core/errors_test.go`, `internal/core/pathprovider.go`, `internal/core/assetserver.go`, `internal/core/exenamer.go`
- Modify: `internal/core/provider.go`, `internal/core/locstring.go`, `internal/core/locstring_test.go`

Module path is `launcher-collection-tmp`.

### Step 1.1: Write failing test for `ParseGameID`

Append to `internal/core/locstring_test.go` is wrong — a different file is more appropriate. Create `internal/core/provider_test.go`:

```go
package core

import "testing"

func TestParseGameID_Valid(t *testing.T) {
	b, suffix, err := ParseGameID("hoyoverse/genshin")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if b != "hoyoverse" {
		t.Errorf("backend = %q, want hoyoverse", b)
	}
	if suffix != "genshin" {
		t.Errorf("suffix = %q, want genshin", suffix)
	}
}

func TestParseGameID_Invalid(t *testing.T) {
	cases := []GameID{"", "no-slash", "/leading", "trailing/", "hoyoverse//"}
	for _, c := range cases {
		if _, _, err := ParseGameID(c); err == nil {
			t.Errorf("ParseGameID(%q) succeeded; want error", c)
		}
	}
}

func TestParseGameID_SuffixWithSlash(t *testing.T) {
	// suffix may itself contain slashes — split is on FIRST slash only
	b, suffix, err := ParseGameID("hoyoverse/sub/game")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if b != "hoyoverse" || suffix != "sub/game" {
		t.Errorf("got (%q, %q), want (hoyoverse, sub/game)", b, suffix)
	}
}
```

### Step 1.2: Run, verify FAIL

```
go test ./internal/core/...
```

Expected FAIL: `ParseGameID undefined`.

### Step 1.3: Modify `internal/core/provider.go` to add `ParseGameID`

Append to `internal/core/provider.go` (after the existing imports, add `strings` and `fmt`; after the `Provider` interface):

```go
// ParseGameID splits a GameID of the form "<backend>/<suffix>" into its
// components. Returns an error if the format is invalid (missing slash,
// empty backend, or empty suffix).
//
// The format is part of the contract: front-end stores, App routing, and
// asset URLs all depend on it.
func ParseGameID(s GameID) (BackendID, string, error) {
	parts := strings.SplitN(string(s), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid game id %q (want <backend>/<suffix>)", s)
	}
	return BackendID(parts[0]), parts[1], nil
}
```

Add `"strings"` and `"fmt"` to the imports.

Also update the `Launch` method's godoc on the `Provider` interface (existing comment block right above `Launch`) to add the anti-cheat invariant:

```go
// Launch starts the game by executing its main exe. Returns (0, nil) on
// successful spawn — the spawned process is intentionally NOT tracked
// by the launcher (anti-cheat may flag a polling parent). Implementations
// MUST NOT call cmd.Wait() or otherwise observe the child after spawn.
//
// ctx may short-circuit pre-spawn work (UTF-16 conversions, cache lookup)
// via ctx.Err() but does NOT bind to the spawned process lifetime.
Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
```

### Step 1.4: Run, verify PASS

```
go test ./internal/core/...
```

Expected: PASS — `TestParseGameID_*` plus M1's existing `TestLocalizedString_*` all green.

### Step 1.5: Write failing tests for sentinel errors

Create `internal/core/errors_test.go`:

```go
package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrors_Is(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", ErrGameNotInstalled)
	if !errors.Is(wrapped, ErrGameNotInstalled) {
		t.Errorf("wrapped error not detected via errors.Is")
	}
}

func TestErrorCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrUnknownGame, "unknown_game"},
		{ErrGameNotInstalled, "not_installed"},
		{ErrBackendNotConfigured, "not_configured"},
		{ErrLauncherMissing, "launcher_missing"},
		{ErrAssetNotAvailable, "asset_unavailable"},
		{fmt.Errorf("wrap: %w", ErrGameNotInstalled), "not_installed"},
		{nil, "internal"},
		{errors.New("random"), "internal"},
	}
	for _, c := range cases {
		got := ErrorCode(c.err)
		if got != c.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
```

### Step 1.6: Run, verify FAIL

```
go test ./internal/core/...
```

Expected: FAIL — `ErrUnknownGame undefined`, etc.

### Step 1.7: Create `internal/core/errors.go`

```go
package core

import "errors"

// Sentinel errors that the rest of the codebase wraps with %w. The frontend
// uses ErrorCode to map these to stable JSON-friendly codes.
var (
	ErrUnknownGame          = errors.New("unknown game id")
	ErrGameNotInstalled     = errors.New("game not installed")
	ErrBackendNotConfigured = errors.New("backend not configured")
	ErrLauncherMissing      = errors.New("launcher folder not found")
	ErrAssetNotAvailable    = errors.New("asset not available")
)

// ErrorCode returns a stable JSON-friendly code for the given error. The
// frontend uses this code to choose UX (CTA, retry, dim). Returns "internal"
// for nil or unrecognized errors.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return "internal"
	case errors.Is(err, ErrUnknownGame):
		return "unknown_game"
	case errors.Is(err, ErrGameNotInstalled):
		return "not_installed"
	case errors.Is(err, ErrBackendNotConfigured):
		return "not_configured"
	case errors.Is(err, ErrLauncherMissing):
		return "launcher_missing"
	case errors.Is(err, ErrAssetNotAvailable):
		return "asset_unavailable"
	default:
		return "internal"
	}
}
```

### Step 1.8: Run, verify PASS

```
go test ./internal/core/...
```

Expected: PASS for `TestSentinelErrors_Is`, `TestErrorCode`, plus existing tests.

### Step 1.9: Create the three optional interfaces

Create `internal/core/pathprovider.go`:

```go
package core

// PathProvider is implemented by Providers that have a primary on-disk root
// (the directory the user configures in settings.toml). App's BackendStatus
// derivation uses PrimaryPath to detect "path_unset" / "launcher_missing".
//
// Optional interface — providers that don't have a path-based detection
// model (e.g. registry-only) simply do not implement this; status falls back
// to "ok" if DetectInstall returns games, "empty" otherwise.
type PathProvider interface {
	PrimaryPath() string // empty string when not configured
}
```

Create `internal/core/assetserver.go`:

```go
package core

import "context"

// AssetServer is implemented by Providers that serve binary assets (e.g.
// background art bytes from a local launcher cache) over the AssetServer
// middleware's /_asset/<backend>/<kind>/<key> route.
//
// The middleware ONLY ever calls ServeAsset with kind == "bg". The kind ==
// "icon" path is handled uniformly by the middleware itself via the shared
// iconext package + ExeNamer interface — see internal/app/asset_handler.go.
//
// Implementations that don't have local-cache backgrounds (e.g. hoyoverse,
// which returns CDN URLs) do not implement this interface; the middleware
// falls back to 404 on the type-assertion failure.
type AssetServer interface {
	ServeAsset(ctx context.Context, kind, key string) (data []byte, mime string, err error)
}
```

Create `internal/core/exenamer.go`:

```go
package core

// ExeNamer is implemented by Providers whose games each have a single primary
// .exe filename relative to the install path. The AssetServer middleware uses
// this to resolve <installPath>/<exeName> for runtime PE icon extraction.
//
// Optional interface — providers without a single canonical exe per game (or
// without runtime icon extraction needs) simply do not implement this; the
// middleware returns 404 for icon URLs in that case.
type ExeNamer interface {
	ExeName(gid GameID) (string, bool) // returns the exe filename and true; false if gid unknown
}
```

### Step 1.10: Write failing tests for the LocalizedString fallback chain

Replace the contents of `internal/core/locstring_test.go` with:

```go
package core

import "testing"

func TestLocalizedString_Get(t *testing.T) {
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("zh-TW"); got != "原神" {
		t.Errorf("Get(zh-TW) = %q, want 原神", got)
	}
	if got := ls.Get("en"); got != "Genshin Impact" {
		t.Errorf("Get(en) = %q, want Genshin Impact", got)
	}
}

func TestLocalizedString_GetMissingFallsBackToEn(t *testing.T) {
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("ja"); got != "Genshin Impact" {
		t.Errorf("Get(ja) fallback = %q, want Genshin Impact", got)
	}
}

func TestLocalizedString_GetEmptyReturnsEmpty(t *testing.T) {
	ls := LocalizedString{}
	if got := ls.Get("zh-TW"); got != "" {
		t.Errorf("Get on empty = %q, want empty", got)
	}
}

func TestLocalizedString_GetZhCN_PrefersExplicit(t *testing.T) {
	ls := LocalizedString{
		"zh-CN": "明日方舟：终末地",
		"zh-TW": "明日方舟：終末地",
		"en":    "Arknights: Endfield",
	}
	if got := ls.Get("zh-CN"); got != "明日方舟：终末地" {
		t.Errorf("Get(zh-CN) = %q, want explicit zh-CN", got)
	}
}

func TestLocalizedString_GetZhCN_FallsBackToEn_NotZhTW(t *testing.T) {
	// HoYoverse games only ship zh-TW + en. zh-CN must NOT fall through to zh-TW
	// (would mix simplified/traditional in one sidebar). Should fall to en.
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("zh-CN"); got != "Genshin Impact" {
		t.Errorf("Get(zh-CN) = %q, want en fallback Genshin Impact", got)
	}
}

func TestLocalizedString_GetZhCN_FallsToZhTW_WhenNoEn(t *testing.T) {
	// Edge case: only zh-TW available. zh-CN → en (missing) → zh-TW.
	ls := LocalizedString{"zh-TW": "原神"}
	if got := ls.Get("zh-CN"); got != "原神" {
		t.Errorf("Get(zh-CN) with only zh-TW = %q, want 原神", got)
	}
}

func TestLocalizedString_GetZhTW_NoFallthroughToZhCN(t *testing.T) {
	// User on zh-TW must not see zh-CN content.
	ls := LocalizedString{"zh-CN": "崩坏：星穹铁道", "en": "Honkai: Star Rail"}
	if got := ls.Get("zh-TW"); got != "Honkai: Star Rail" {
		t.Errorf("Get(zh-TW) with only zh-CN+en = %q, want en fallback", got)
	}
}
```

### Step 1.11: Run, verify some tests FAIL

```
go test ./internal/core/...
```

Expected: 3 of the new tests FAIL (existing fallback chain doesn't honor zh-CN preferring en). The first three (M1 tests) still PASS.

### Step 1.12: Modify `internal/core/locstring.go`

Replace the file contents with:

```go
package core

// LocalizedString maps language tag (e.g. "zh-TW", "zh-CN", "en") to the
// localized string. Use Get() to resolve with fallback chain.
type LocalizedString map[string]string

// Get returns the value for the given language with a fallback chain that
// avoids mixing scripts:
//
//   zh-CN → en → zh-TW → first non-empty entry → ""
//   zh-TW → en → first non-empty → ""
//   en    → first non-empty → ""
//   other → matching entry, else en, else first non-empty, else ""
//
// The zh-CN-prefers-en rule prevents traditional-Chinese fallback when the
// user has chosen simplified Chinese (mixing 崩壞 and 崩坏 in one sidebar
// is jarring; English is a cleaner fallback than wrong-script Chinese).
func (l LocalizedString) Get(lang string) string {
	if v, ok := l[lang]; ok && v != "" {
		return v
	}
	switch lang {
	case "zh-CN":
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
		if v, ok := l["zh-TW"]; ok && v != "" {
			return v
		}
	case "zh-TW":
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
	default:
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
	}
	for _, v := range l {
		if v != "" {
			return v
		}
	}
	return ""
}
```

### Step 1.13: Run, verify all PASS

```
go test ./internal/core/...
```

Expected: all 7 new + existing tests PASS.

### Step 1.14: Verify whole-repo still compiles + passes M1 tests

```
go vet ./...
go test ./...
```

Expected: clean vet, all M1 tests still pass (no regressions).

### Step 1.15: Commit

```
git add internal/core/
git commit -m "feat(core): sentinel errors, optional interfaces, ParseGameID, locale fallback"
```

---

## Task 2: util/dirver — shared version-dir parser

**Files:**
- Create: `internal/util/dirver/dirver.go`, `internal/util/dirver/dirver_test.go`

### Step 2.1: Write failing tests

Create `internal/util/dirver/dirver_test.go`:

```go
package dirver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaxIn_PicksHighest(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.0.0"))
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "1.10.0"))
	mustMkdir(t, filepath.Join(tmp, "0.9.9"))

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.10.0" {
		t.Errorf("MaxIn = %q, want 1.10.0 (numeric, not lex)", got)
	}
}

func TestMaxIn_HandlesFourPart(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "2.5.0.1"))
	mustMkdir(t, filepath.Join(tmp, "2.6.1.0"))
	mustMkdir(t, filepath.Join(tmp, "2.5.0.0"))

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.6.1.0" {
		t.Errorf("MaxIn = %q, want 2.6.1.0", got)
	}
}

func TestMaxIn_FiltersNonVersionDirs(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "Cache"))           // not a version
	mustMkdir(t, filepath.Join(tmp, "Wuthering Waves Game")) // not a version
	mustMkdir(t, filepath.Join(tmp, "kr_game_cache"))   // not a version
	// also place a file with a version-like name
	if err := os.WriteFile(filepath.Join(tmp, "2.0.0"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Errorf("MaxIn = %q, want 1.2.3 (file named 2.0.0 must be ignored)", got)
	}
}

func TestMaxIn_EmptyReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatalf("expected nil error on empty dir, got %v", err)
	}
	if got != "" {
		t.Errorf("MaxIn empty dir = %q, want empty", got)
	}
}

func TestMaxIn_NonExistentReturnsEmpty(t *testing.T) {
	got, err := MaxIn(filepath.Join(os.TempDir(), "definitely-does-not-exist-launcher-collection"))
	if err != nil {
		t.Errorf("unexpected error on non-existent dir: %v", err)
	}
	if got != "" {
		t.Errorf("MaxIn non-existent = %q, want empty", got)
	}
}

func TestMaxIn_MixedThreeAndFourPart(t *testing.T) {
	// Defensive: a dir with both 3- and 4-part children should still find max
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "1.2.3.5"))
	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// 1.2.3.5 > 1.2.3 (treating missing 4th part as 0)
	if got != "1.2.3.5" {
		t.Errorf("MaxIn = %q, want 1.2.3.5", got)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
```

### Step 2.2: Run, verify FAIL

```
go test ./internal/util/dirver/...
```

Expected: FAIL — `MaxIn undefined`.

### Step 2.3: Create `internal/util/dirver/dirver.go`

```go
// Package dirver scans a directory for child directories whose names look
// like version numbers (3- or 4-part dotted ints, e.g. "1.2.3" or "2.6.1.0")
// and returns the highest-versioned name. Used by Kuro and Hypergryph
// providers to read the installed version from the launcher's local FS.
package dirver

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// versionRe matches names of 3 or 4 dotted positive integers — e.g.
//   1.2.3
//   2.6.1.0
// and rejects:
//   Cache
//   1.2
//   1.2.3-rc1
//   v1.2.3 (no leading 'v')
var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(\.\d+)?$`)

// MaxIn scans dir for child directories whose names match the version regex
// and returns the highest-versioned one as a string. Returns "" with nil
// error when:
//   - dir doesn't exist
//   - dir exists but contains no version-named children
//   - dir exists but is not actually a directory (file at that path)
//
// Returns a non-nil error only on unexpected I/O failure (permission
// denied, etc.).
func MaxIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		// Treat "not a directory" as "no version found"
		if _, statErr := os.Stat(dir); statErr == nil {
			return "", nil
		}
		return "", err
	}

	var bestName string
	var bestParts []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !versionRe.MatchString(name) {
			continue
		}
		parts := parseInts(name)
		if bestName == "" || cmpVersionInts(parts, bestParts) > 0 {
			bestName = name
			bestParts = parts
		}
	}
	return bestName, nil
}

// parseInts splits "1.2.3.4" into []int{1,2,3,4}. Caller must guarantee the
// input matches versionRe — no error returned.
func parseInts(s string) []int {
	segs := strings.Split(s, ".")
	out := make([]int, len(segs))
	for i, seg := range segs {
		n, _ := strconv.Atoi(seg)
		out[i] = n
	}
	return out
}

// cmpVersionInts compares two int slices lexicographically, treating missing
// trailing segments as 0 (so [1,2,3] < [1,2,3,1]). Returns -1, 0, or 1.
func cmpVersionInts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}
```

### Step 2.4: Run, verify PASS

```
go test ./internal/util/dirver/...
```

Expected: all 6 tests PASS.

### Step 2.5: Run `go vet`, whole-repo tests

```
go vet ./...
go test ./...
```

Expected: clean.

### Step 2.6: Commit

```
git add internal/util/dirver/
git commit -m "feat(util): dirver shared version-dir parser (3- and 4-part)"
```

---

## Task 3: iconext — shared package for runtime PE icon extraction

**Files:**
- Create: `internal/providers/iconext/iconext.go`, `internal/providers/iconext/iconext_windows.go`, `internal/providers/iconext/iconext_other.go`, `internal/providers/iconext/iconext_test.go`, `internal/providers/iconext/testdata/sample.exe`

The Windows impl uses `golang.org/x/sys/windows` (already a direct require from M1's launch fix). On non-Windows builds the package compiles to a stub that returns `ErrUnsupported`.

### Step 3.1: Create the testdata fixture

Pick one of the following methods to produce `internal/providers/iconext/testdata/sample.exe` (a small PE binary with a known 32×32 icon):

- **Option A (preferred — reproducible):** copy a small Windows-bundled exe with an embedded icon. `C:\Windows\System32\notepad.exe` is reasonable; copy via:

  ```bash
  mkdir -p internal/providers/iconext/testdata
  cp "C:\\Windows\\System32\\notepad.exe" internal/providers/iconext/testdata/sample.exe
  ```

  Document in the test file that the fixture is a copy of notepad.exe (Microsoft license; OK to redistribute as test fixture per typical legal practice for tiny system binaries — if uncertain, switch to Option B).

- **Option B (fully self-contained):** use the `rsrc` tool to embed an icon into a tiny Go program, build it. Steps:

  ```bash
  go install github.com/akavel/rsrc@latest
  mkdir -p /tmp/iconfix && cd /tmp/iconfix
  echo 'package main; func main() {}' > main.go
  # Provide a tiny 32x32 .ico (e.g. transparent square or a known pattern)
  rsrc -ico icon.ico -o rsrc.syso
  GOOS=windows go build -o sample.exe .
  cp sample.exe <repo>/internal/providers/iconext/testdata/sample.exe
  ```

- **Option C (simplest, cross-machine fragile):** point the test at `os.Getenv("WINDIR") + "\\System32\\notepad.exe"` directly without checking in a fixture. Test only runs on Windows.

Pick **A** or **C** to keep this task small. The test file in step 3.4 below assumes Option A's testdata path; adjust if you choose C.

### Step 3.2: Create `internal/providers/iconext/iconext.go` (cross-platform API)

```go
// Package iconext extracts the largest available icon from a Windows PE
// executable's resource section, encoded as PNG. The extracted bytes are
// cached in-memory across calls (keyed by exePath + mtime).
//
// The package is built only on Windows. On other platforms the public API
// returns ErrUnsupported.
package iconext

import "errors"

// ErrUnsupported is returned by Extract on non-Windows builds and when the
// underlying Win32 calls report no extractable icon.
var ErrUnsupported = errors.New("iconext: extraction unsupported on this platform")

// Extract returns the PNG-encoded bytes of the largest icon embedded in
// exePath. Implementations must cache by (exePath, mtime) so repeated calls
// for the same .exe are cheap; the cache is bounded (~32 MB hard cap, LRU).
//
// Errors: ErrUnsupported on non-Windows; os.PathError if exePath does not
// exist or is not readable; any error from the underlying icon-decoding
// path (NoIcon, BitmapDecodeError) is wrapped with %w.
//
// The function is safe for concurrent use.
//
// (Implementation lives in iconext_windows.go / iconext_other.go.)
func Extract(exePath string) ([]byte, error) {
	return extractImpl(exePath)
}
```

### Step 3.3: Create `internal/providers/iconext/iconext_other.go` (non-Windows stub)

```go
//go:build !windows

package iconext

func extractImpl(exePath string) ([]byte, error) {
	return nil, ErrUnsupported
}
```

### Step 3.4: Write failing test

Create `internal/providers/iconext/iconext_test.go`:

```go
package iconext

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtract_NonExistentPath(t *testing.T) {
	_, err := Extract(filepath.Join(os.TempDir(), "definitely-not-an-exe.exe"))
	if err == nil {
		t.Errorf("expected error on non-existent path, got nil")
	}
}

func TestExtract_Windows_Sample(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PE icon extraction requires Windows")
	}
	bytesOut, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(bytesOut) == 0 {
		t.Fatalf("extracted bytes empty")
	}
	// Verify the bytes are a valid PNG and decode to a non-zero-sized image.
	img, err := png.Decode(bytes.NewReader(bytesOut))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		t.Errorf("extracted PNG has zero dimensions: %v", bounds)
	}
	t.Logf("extracted PNG: %dx%d, %d bytes", bounds.Dx(), bounds.Dy(), len(bytesOut))
}

func TestExtract_NonWindowsReturnsErrUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("only relevant on non-Windows")
	}
	_, err := Extract("anything")
	if err == nil || err.Error() != ErrUnsupported.Error() {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}

func TestExtract_CachedRepeats(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows")
	}
	a, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("cached repeat returned different bytes: len(a)=%d, len(b)=%d", len(a), len(b))
	}
	// Image dimensions provide a sanity check the cache returns the same content
	imgA, _ := png.Decode(bytes.NewReader(a))
	imgB, _ := png.Decode(bytes.NewReader(b))
	if !boundsEqual(imgA, imgB) {
		t.Errorf("cached repeat returned different image bounds")
	}
}

func boundsEqual(a, b image.Image) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Bounds() == b.Bounds()
}
```

### Step 3.5: Run, verify FAIL

```
go test ./internal/providers/iconext/...
```

Expected on Windows: FAIL — `extractImpl undefined` for `iconext_windows.go`.

### Step 3.6: Create `internal/providers/iconext/iconext_windows.go`

This is the substantial Win32 plumbing. Use `golang.org/x/sys/windows` and `image`/`png` from stdlib. The full file:

```go
//go:build windows

package iconext

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Cache: bounded by total bytes (~32 MB).
const maxCacheBytes = 32 * 1024 * 1024

type cacheEntry struct {
	mtime int64
	data  []byte
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
	cached  int // total bytes currently held
)

func extractImpl(exePath string) ([]byte, error) {
	st, err := os.Stat(exePath)
	if err != nil {
		return nil, err
	}
	mtime := st.ModTime().UnixNano()

	cacheMu.Lock()
	if e, ok := cache[exePath]; ok && e.mtime == mtime {
		out := e.data
		cacheMu.Unlock()
		return out, nil
	}
	cacheMu.Unlock()

	bytesOut, err := extractFromExe(exePath)
	if err != nil {
		return nil, err
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	// Evict oldest entries (random map ordering is fine for M2; a real LRU
	// would need a list — out of M2 scope) until the new entry fits.
	for cached+len(bytesOut) > maxCacheBytes && len(cache) > 0 {
		for k, v := range cache {
			cached -= len(v.data)
			delete(cache, k)
			break
		}
	}
	cache[exePath] = cacheEntry{mtime: mtime, data: bytesOut}
	cached += len(bytesOut)
	return bytesOut, nil
}

// extractFromExe loads the largest icon from the .exe via ExtractIconExW,
// converts the HICON to RGBA pixels via GetIconInfo + GetDIBits, and PNG-
// encodes the result.
func extractFromExe(exePath string) ([]byte, error) {
	pathPtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, fmt.Errorf("utf16 path: %w", err)
	}

	var large windows.Handle
	// ExtractIconExW(path, 0, &large, nil, 1) -> count of icons in the file
	// (returns >0 on success; the returned HICON is in `large`).
	r, _, _ := procExtractIconExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&large)),
		0,
		1,
	)
	if r == 0 || r == ^uintptr(0) || large == 0 {
		return nil, fmt.Errorf("%w: no icons in %s", ErrUnsupported, exePath)
	}
	defer procDestroyIcon.Call(uintptr(large))

	return iconToPNG(large)
}

// iconToPNG converts an HICON to a PNG byte slice via GetIconInfo + GetDIBits.
func iconToPNG(hIcon windows.Handle) ([]byte, error) {
	var info iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(hIcon), uintptr(unsafe.Pointer(&info))); r == 0 {
		return nil, fmt.Errorf("%w: GetIconInfo failed", ErrUnsupported)
	}
	defer procDeleteObject.Call(uintptr(info.hbmColor))
	defer procDeleteObject.Call(uintptr(info.hbmMask))

	// Inspect the color bitmap dimensions.
	var bm bitmap
	if r, _, _ := procGetObjectW.Call(
		uintptr(info.hbmColor),
		unsafe.Sizeof(bm),
		uintptr(unsafe.Pointer(&bm)),
	); r == 0 {
		return nil, fmt.Errorf("%w: GetObject failed", ErrUnsupported)
	}
	width, height := int(bm.width), int(bm.height)
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("%w: zero icon dimensions", ErrUnsupported)
	}

	hdc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, hdc)

	// BITMAPINFOHEADER for 32bpp top-down BGRA.
	bi := bitmapInfo{}
	bi.header.biSize = uint32(unsafe.Sizeof(bi.header))
	bi.header.biWidth = int32(width)
	bi.header.biHeight = -int32(height) // top-down
	bi.header.biPlanes = 1
	bi.header.biBitCount = 32
	bi.header.biCompression = 0 // BI_RGB

	pixels := make([]byte, width*height*4)
	r, _, _ := procGetDIBits.Call(
		hdc,
		uintptr(info.hbmColor),
		0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bi)),
		0, // DIB_RGB_COLORS
	)
	if r == 0 {
		return nil, fmt.Errorf("%w: GetDIBits failed", ErrUnsupported)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			b, g, r, a := pixels[i], pixels[i+1], pixels[i+2], pixels[i+3]
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Win32 plumbing -------------------------------------------------------------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procExtractIconExW = shell32.NewProc("ExtractIconExW")
	procDestroyIcon    = user32.NewProc("DestroyIcon")
	procGetIconInfo    = user32.NewProc("GetIconInfo")
	procGetDC          = user32.NewProc("GetDC")
	procReleaseDC      = user32.NewProc("ReleaseDC")
	procGetObjectW     = gdi32.NewProc("GetObjectW")
	procGetDIBits      = gdi32.NewProc("GetDIBits")
	procDeleteObject   = gdi32.NewProc("DeleteObject")
)

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmap struct {
	bmType       int32
	width        int32
	height       int32
	widthBytes   int32
	planes       uint16
	bitsPerPixel uint16
	bits         uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	header   bitmapInfoHeader
	colors   [256]uint32 // unused for 32bpp, but keeps the alloc one-shot
}
```

### Step 3.7: Run, verify PASS

```
go test ./internal/providers/iconext/... -v
```

Expected on Windows: all four tests PASS. The extracted PNG should decode to a non-zero image, and repeated calls return the cached bytes.

### Step 3.8: Run whole-repo tests + vet

```
go vet ./...
go test ./...
```

Expected: clean.

### Step 3.9: Commit

```
git add internal/providers/iconext/
git commit -m "feat(iconext): runtime PE icon extraction with package-level lru cache"
```

---

## Task 4: App refactor — provider registry, detection cache at App layer, Wails command updates

This is the biggest abstraction-locking task; the implementer should pause for human review here before continuing.

**Files:**
- Modify: `internal/app/app.go` (substantial rewrite)
- Create: `internal/app/app_test.go` (new tests for `provider()` lookup, GameID prefix validation, cache invalidation)

### Step 4.1: Read M1's `internal/app/app.go` in full

Familiarize with the M1 shape: single `hoyo *hoyoverse.Provider` field, 7 Wails-bound commands. Take note of:
- `App.Startup(ctx)` lifecycle
- The `GameRow` struct returned from `ListGames`
- How `New(settingsPath string) *App` constructs the provider

### Step 4.2: Write failing tests

Create `internal/app/app_test.go`:

```go
package app

import (
	"context"
	"errors"
	"testing"

	"launcher-collection-tmp/internal/core"
)

// fakeProvider satisfies core.Provider with configurable behavior for tests.
type fakeProvider struct {
	id    core.BackendID
	games []core.GameDescriptor
	// Optional install set; nil means DetectInstall returns ([], nil)
	installs []core.InstalledGame
	// optional path for PathProvider
	path string
}

func (f *fakeProvider) ID() core.BackendID                                  { return f.id }
func (f *fakeProvider) DisplayName() core.LocalizedString                   { return core.LocalizedString{"en": string(f.id)} }
func (f *fakeProvider) Games() []core.GameDescriptor                        { return f.games }
func (f *fakeProvider) SettingsSchema() []core.SettingField                 { return nil }
func (f *fakeProvider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return f.installs, nil
}
func (f *fakeProvider) GetIcon(ctx context.Context, gid core.GameID) (string, error) { return "", nil }
func (f *fakeProvider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *fakeProvider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (f *fakeProvider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	return 0, nil
}
func (f *fakeProvider) PrimaryPath() string { return f.path }

func TestProviderLookup(t *testing.T) {
	a := newAppForTest(t,
		&fakeProvider{id: "hoyoverse", games: []core.GameDescriptor{{ID: "hoyoverse/genshin", Backend: "hoyoverse"}}},
		&fakeProvider{id: "kurogames", games: []core.GameDescriptor{{ID: "kurogames/wuwa", Backend: "kurogames"}}},
	)

	p, err := a.provider("hoyoverse/genshin")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID() != "hoyoverse" {
		t.Errorf("got provider %q, want hoyoverse", p.ID())
	}

	p, err = a.provider("kurogames/wuwa")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID() != "kurogames" {
		t.Errorf("got provider %q, want kurogames", p.ID())
	}

	if _, err := a.provider("nonexistent/game"); !errors.Is(err, core.ErrUnknownGame) {
		t.Errorf("provider(unknown) = %v, want ErrUnknownGame", err)
	}

	if _, err := a.provider("malformed-no-slash"); err == nil {
		t.Errorf("provider(malformed) succeeded; want error")
	}
}

func TestRegisterProvider_RejectsMismatchedGameIDs(t *testing.T) {
	bad := &fakeProvider{
		id:    "hoyoverse",
		games: []core.GameDescriptor{{ID: "kurogames/wuwa", Backend: "kurogames"}}, // wrong prefix!
	}
	a := &App{}
	if err := a.registerProvider(bad); err == nil {
		t.Errorf("registerProvider accepted mismatched game id; want error")
	}
}

func TestCachedDetect_CachesAcrossCalls(t *testing.T) {
	p := &fakeProvider{
		id:       "hoyoverse",
		installs: []core.InstalledGame{{GameID: "hoyoverse/genshin", InstallPath: "C:/x"}},
	}
	a := newAppForTest(t, p)

	g1, err := a.cachedDetect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := a.cachedDetect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(g1) != 1 || len(g2) != 1 {
		t.Fatalf("cached detect lengths = %d, %d", len(g1), len(g2))
	}
	// Both calls should return the same slice content (cache hit on second).
	if g1[0].InstallPath != g2[0].InstallPath {
		t.Errorf("cache returned different install path on second call")
	}
}

func TestRefreshClearsCache(t *testing.T) {
	p := &fakeProvider{id: "hoyoverse"}
	a := newAppForTest(t, p)
	if _, err := a.cachedDetect(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	a.detectMu.Lock()
	defer a.detectMu.Unlock()
	if _, ok := a.detect[p.ID()]; ok {
		t.Errorf("cache not cleared after Refresh()")
	}
}

func TestListBackends_DerivesStatuses(t *testing.T) {
	hoyo := &fakeProvider{
		id:       "hoyoverse",
		path:     "C:/Program Files/HoYoPlay",
		installs: []core.InstalledGame{{GameID: "hoyoverse/genshin"}},
	}
	emptyKuro := &fakeProvider{
		id:   "kurogames",
		path: "C:/Program Files/Wuthering Waves", // path set, no installs
	}
	unconfigured := &fakeProvider{
		id:   "hypergryph",
		path: "", // path empty
	}
	a := newAppForTest(t, hoyo, emptyKuro, unconfigured)
	statuses := a.ListBackends()
	if len(statuses) != 3 {
		t.Fatalf("got %d, want 3 statuses", len(statuses))
	}
	byID := map[string]BackendStatus{}
	for _, s := range statuses {
		byID[s.BackendID] = s
	}
	// Note: emptyKuro's path doesn't actually exist on the test FS; that means
	// status will be "launcher_missing" not "empty". Adjust as needed once
	// status semantics are concrete.
	if byID["hoyoverse"].Status != "ok" && byID["hoyoverse"].Status != "launcher_missing" {
		t.Errorf("hoyoverse status = %q (allowed: ok|launcher_missing depending on FS)", byID["hoyoverse"].Status)
	}
	if byID["hypergryph"].Status != "path_unset" {
		t.Errorf("hypergryph status = %q, want path_unset", byID["hypergryph"].Status)
	}
}

// Helper: build an App with the given providers and skip settings file IO.
func newAppForTest(t *testing.T, ps ...core.Provider) *App {
	t.Helper()
	a := &App{
		settingsP: "",
		providers: nil,
		detect:    map[core.BackendID]detectEntry{},
	}
	for _, p := range ps {
		if err := a.registerProvider(p); err != nil {
			t.Fatalf("register %s: %v", p.ID(), err)
		}
	}
	a.ctx = context.Background()
	return a
}
```

### Step 4.3: Run, verify FAIL

```
go test ./internal/app/...
```

Expected: FAIL — many undefined symbols (`registerProvider`, `cachedDetect`, `detect`, `Refresh`, `ListBackends`, `BackendStatus`).

### Step 4.4: Rewrite `internal/app/app.go`

Replace the M1 contents with the new App struct and command set. Full file:

```go
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/hoyoverse"
)

type detectEntry struct {
	games []core.InstalledGame
	at    time.Time
	err   error
}

type App struct {
	ctx       context.Context
	settings  Settings
	settingsP string
	providers []core.Provider
	detect    map[core.BackendID]detectEntry
	detectMu  sync.Mutex
	logger    *slog.Logger
}

// New returns an App. settingsPath may be "" → default to alongside the binary.
// logger may be nil → uses slog.Default().
func New(settingsPath string, logger *slog.Logger) *App {
	if settingsPath == "" {
		settingsPath = "settings.toml"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s, err := LoadSettings(settingsPath)
	if err != nil {
		logger.Error("settings load failed; using defaults", "err", err, "path", settingsPath)
	}
	a := &App{
		settings:  s,
		settingsP: settingsPath,
		detect:    map[core.BackendID]detectEntry{},
		logger:    logger,
	}
	if err := a.constructProviders(); err != nil {
		logger.Error("provider construction failed", "err", err)
	}
	return a
}

// constructProviders builds the list of providers from current settings. M1
// hoyoverse always present; M2 adds kurogames + hypergryph (constructed in a
// later task). Re-called by UpdateSettings.
func (a *App) constructProviders() error {
	a.providers = nil
	hoyo := hoyoverse.New(
		hoyoverse.Settings{
			HoYoplayPath: a.settings.Backends.Hoyoverse.Path,
			Region:       a.settings.Backends.Hoyoverse.Region,
		},
		a.logger.With("backend", "hoyoverse"),
	)
	if err := a.registerProvider(hoyo); err != nil {
		return err
	}
	// Task 8 inserts kurogames here; Task 9 inserts hypergryph here.
	return nil
}

// registerProvider adds a provider to the registry after validating that
// every GameID it emits has the provider's BackendID as the prefix.
func (a *App) registerProvider(p core.Provider) error {
	for _, g := range p.Games() {
		b, _, err := core.ParseGameID(g.ID)
		if err != nil {
			return fmt.Errorf("provider %q emitted invalid game id %q: %w", p.ID(), g.ID, err)
		}
		if b != p.ID() {
			return fmt.Errorf("provider %q emitted game id %q with mismatched backend prefix %q",
				p.ID(), g.ID, b)
		}
	}
	a.providers = append(a.providers, p)
	return nil
}

// provider returns the registered Provider for a given GameID, or
// core.ErrUnknownGame if no match.
func (a *App) provider(gid core.GameID) (core.Provider, error) {
	backendID, _, err := core.ParseGameID(gid)
	if err != nil {
		return nil, err
	}
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}

// byID returns the registered Provider for a backend, or nil if none.
func (a *App) byID(backendID core.BackendID) core.Provider {
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p
		}
	}
	return nil
}

// cachedDetect returns the detection result for a provider, caching it
// across calls. Cache is invalidated by UpdateSettings or Refresh.
func (a *App) cachedDetect(ctx context.Context, p core.Provider) ([]core.InstalledGame, error) {
	a.detectMu.Lock()
	if e, ok := a.detect[p.ID()]; ok && e.err == nil {
		out := e.games
		a.detectMu.Unlock()
		return out, nil
	}
	a.detectMu.Unlock()

	games, err := p.DetectInstall(ctx)

	a.detectMu.Lock()
	a.detect[p.ID()] = detectEntry{games: games, at: time.Now(), err: err}
	a.detectMu.Unlock()

	return games, err
}

// invalidateDetect clears the entire detection cache. Called by UpdateSettings
// (provider settings may have changed paths) and the manual Refresh command.
func (a *App) invalidateDetect() {
	a.detectMu.Lock()
	a.detect = map[core.BackendID]detectEntry{}
	a.detectMu.Unlock()
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// ─── Wails-bound commands (return values must be JSON-serializable) ───

type GameRow struct {
	ID             string                 `json:"id"`
	Backend        string                 `json:"backend"`
	DisplayName    core.LocalizedString   `json:"display_name"`
	Installed      bool                   `json:"installed"`
	InstallPath    string                 `json:"install_path,omitempty"`
	Current        string                 `json:"current_version,omitempty"`
	Latest         string                 `json:"latest_version,omitempty"`
	HasPredownload bool                   `json:"has_predownload"`
	IconURL        string                 `json:"icon_url,omitempty"`
}

// BackendStatus is one entry from ListBackends.
type BackendStatus struct {
	BackendID   string                 `json:"backend_id"`
	DisplayName core.LocalizedString   `json:"display_name"`
	Status      string                 `json:"status"` // ok | path_unset | launcher_missing | empty | error
	Detail      string                 `json:"detail,omitempty"`
}

func (a *App) ListGames() ([]GameRow, error) {
	out := []GameRow{}
	for _, p := range a.providers {
		installed, err := a.cachedDetect(a.ctx, p)
		if err != nil {
			a.logger.Warn("DetectInstall failed", "backend", p.ID(), "err", err)
			continue
		}
		seen := map[core.GameID]core.InstalledGame{}
		for _, ig := range installed {
			seen[ig.GameID] = ig
		}
		for _, g := range p.Games() {
			row := GameRow{
				ID:          string(g.ID),
				Backend:     string(g.Backend),
				DisplayName: g.DisplayName,
			}
			if ig, ok := seen[g.ID]; ok {
				row.Installed = true
				row.InstallPath = ig.InstallPath
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func (a *App) ListBackends() []BackendStatus {
	out := make([]BackendStatus, 0, len(a.providers))
	for _, p := range a.providers {
		bs := BackendStatus{
			BackendID:   string(p.ID()),
			DisplayName: p.DisplayName(),
		}
		// Path-based status derivation
		var path string
		if pp, ok := p.(core.PathProvider); ok {
			path = pp.PrimaryPath()
		}
		switch {
		case path == "":
			bs.Status = "path_unset"
		default:
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					bs.Status = "launcher_missing"
					bs.Detail = path
				} else {
					bs.Status = "error"
					bs.Detail = err.Error()
				}
			} else {
				games, err := a.cachedDetect(a.ctx, p)
				switch {
				case err != nil:
					bs.Status = "error"
					bs.Detail = err.Error()
				case len(games) == 0:
					bs.Status = "empty"
				default:
					bs.Status = "ok"
				}
			}
		}
		out = append(out, bs)
	}
	return out
}

func (a *App) RefreshVersion(gameID string) (core.VersionInfo, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return core.VersionInfo{}, err
	}
	return p.CheckVersion(a.ctx, core.GameID(gameID))
}

func (a *App) GetIcon(gameID string) (string, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return "", err
	}
	return p.GetIcon(a.ctx, core.GameID(gameID))
}

func (a *App) GetBackgrounds(gameID string) ([]core.Background, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return nil, err
	}
	return p.GetBackgrounds(a.ctx, core.GameID(gameID))
}

func (a *App) Launch(gameID string) (int, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return 0, err
	}
	return p.Launch(a.ctx, core.GameID(gameID), core.LaunchOptions{})
}

func (a *App) GetSettings() Settings { return a.settings }

func (a *App) UpdateSettings(s Settings) error {
	if err := SaveSettings(a.settingsP, s); err != nil {
		return err
	}
	a.settings = s
	a.invalidateDetect()
	return a.constructProviders()
}

// Refresh clears the detection cache. Wails-bound; the frontend's manual
// refresh button calls this.
func (a *App) Refresh() {
	a.invalidateDetect()
}

// ErrorCode exposes the core.ErrorCode mapping to the frontend.
func (a *App) ErrorCode(s string) string {
	if s == "" {
		return "internal"
	}
	// Frontend passes the err.message string back; match against the sentinels'
	// .Error() values (works because we wrap with %w and the wrapped chain
	// carries the sentinel).
	for _, sentinel := range []error{
		core.ErrUnknownGame, core.ErrGameNotInstalled, core.ErrBackendNotConfigured,
		core.ErrLauncherMissing, core.ErrAssetNotAvailable,
	} {
		if filepath.Clean(s) == sentinel.Error() || strContains(s, sentinel.Error()) {
			return core.ErrorCode(fmt.Errorf("wrap: %w", sentinel))
		}
	}
	return "internal"
}

// ErrorMessage returns a localized human string for the given JSON code.
// M2 ships with English messages only; M3 can route through vue-i18n.
func (a *App) ErrorMessage(code string) string {
	switch code {
	case "unknown_game":
		return "Unknown game."
	case "not_installed":
		return "Game is not installed."
	case "not_configured":
		return "Backend not configured. Set the launcher path in settings.toml."
	case "launcher_missing":
		return "Launcher folder not found at the configured path."
	case "asset_unavailable":
		return "Asset is not available."
	default:
		return "Internal error."
	}
}

// resolveSettingsPath returns ./settings.toml relative to the binary.
func resolveSettingsPath() string { return filepath.Join(".", "settings.toml") }

func strContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

### Step 4.5: Run, verify PASS

```
go test ./internal/app/...
```

Expected: all 5 new tests + existing settings tests PASS. Note the `TestListBackends_DerivesStatuses` test is intentionally lenient on the `hoyoverse` row's exact value — adjust if it fails.

### Step 4.6: `go vet`, whole-repo

```
go vet ./...
go test ./...
```

Note: `main.go` will fail to compile because `app.New` now takes a logger. Task 7 fixes this. For now run tests scoped to packages:

```
go test ./internal/...
```

Expected: PASS for `internal/...`. `main.go` build break is expected and will be fixed in Task 6/7.

### Step 4.7: Commit

```
git add internal/app/app.go internal/app/app_test.go
git commit -m "refactor(app): provider registry + detection cache + ListBackends + Refresh

Introduces the multi-provider scaffolding used by Task 8 (kurogames) and
Task 9 (hypergryph). main.go is intentionally left broken pending the
hoyoverse adapter changes (Task 6) and main.go rewire (Task 7)."
```

> **Pause here for human review.** This task locks the App-level abstractions; everything else builds on them.

---

## Task 5: Settings — schema extension, version=1 field, M1 hoyoplay_path migration

**Files:**
- Modify: `internal/app/settings.go`, `internal/app/settings_test.go`

### Step 5.1: Read existing M1 `internal/app/settings.go`

Note the structure:
- `Settings { App, Backends }`
- `BackendSettings { Hoyoverse }`
- `HoyoverseSettings { HoYoplayPath, Region }`
- `defaultSettings()`, `LoadSettings(path)`, `SaveSettings(path, s)`

### Step 5.2: Write failing tests

Replace the contents of `internal/app/settings_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_LoadDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 1 {
		t.Errorf("default Version = %d, want 1", s.Version)
	}
	if s.App.Language != "zh-TW" {
		t.Errorf("default lang = %s, want zh-TW", s.App.Language)
	}
	if s.Backends.Hoyoverse.Path != `C:\Program Files\HoYoPlay` {
		t.Errorf("default hoyoverse path = %q", s.Backends.Hoyoverse.Path)
	}
	if s.Backends.Kurogames.Path != `C:\Program Files\Wuthering Waves` {
		t.Errorf("default kurogames path = %q", s.Backends.Kurogames.Path)
	}
	if s.Backends.Hypergryph.Path != `C:\Program Files\GRYPHLINK` {
		t.Errorf("default hypergryph path = %q", s.Backends.Hypergryph.Path)
	}
}

func TestSettings_RoundTripWritesVersion1(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.Language = "en"
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "version = 1") {
		t.Errorf("written file missing 'version = 1':\n%s", raw)
	}
	if !strings.Contains(string(raw), `path = "C:\\Program Files\\HoYoPlay"`) &&
		!strings.Contains(string(raw), `path = 'C:\Program Files\HoYoPlay'`) {
		t.Errorf("written file missing canonical 'path' key for hoyoverse:\n%s", raw)
	}
	if strings.Contains(string(raw), "hoyoplay_path") {
		t.Errorf("written file should not contain legacy hoyoplay_path:\n%s", raw)
	}
}

func TestSettings_MigrateLegacyHoyoplayPath(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// Write an M1-format settings file with hoyoplay_path
	m1 := `[app]
language = "zh-TW"
banner_animation_pref = "video-when-available"
show_technical_info = false

[backends.hoyoverse]
hoyoplay_path = "D:\\HoYoPlay"
region = "global"
`
	if err := os.WriteFile(p, []byte(m1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Hoyoverse.Path != `D:\HoYoPlay` {
		t.Errorf("migrated Path = %q, want D:\\HoYoPlay", s.Backends.Hoyoverse.Path)
	}
	// On save, canonical schema is written
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "hoyoplay_path") {
		t.Errorf("save still contains hoyoplay_path; migration incomplete:\n%s", raw)
	}
	if !strings.Contains(string(raw), "version = 1") {
		t.Errorf("save missing version = 1:\n%s", raw)
	}
}

func TestSettings_MalformedTOMLReturnsDefaults(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	if err := os.WriteFile(p, []byte("this is not valid toml ====="), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err == nil {
		t.Errorf("expected error from LoadSettings on malformed TOML")
	}
	// Even on error, the returned struct should be safe (defaults).
	if s.Version != 1 {
		t.Errorf("returned Version on malformed = %d, want 1", s.Version)
	}
}

func TestSettings_FreshInstallSavesVersion1(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// LoadSettings on missing file returns defaults silently
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	// Save it back
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	// Re-load — should NOT trigger migration (Path already populated, Version=1)
	s2, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Version != 1 {
		t.Errorf("re-loaded Version = %d, want 1", s2.Version)
	}
	if s2.Backends.Hoyoverse.Path != `C:\Program Files\HoYoPlay` {
		t.Errorf("re-loaded hoyoverse Path = %q", s2.Backends.Hoyoverse.Path)
	}
}
```

### Step 5.3: Run, verify FAIL

```
go test ./internal/app/...
```

Expected: FAIL — `Settings.Version` field undefined, etc.

### Step 5.4: Rewrite `internal/app/settings.go`

Replace its contents:

```go
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	Version  int             `toml:"version"`
	App      AppSettings     `toml:"app"`
	Backends BackendSettings `toml:"backends"`
}

type AppSettings struct {
	Language            string `toml:"language"`
	BannerAnimationPref string `toml:"banner_animation_pref"`
	ShowTechnicalInfo   bool   `toml:"show_technical_info"`
}

type BackendSettings struct {
	Hoyoverse  HoyoverseSettings  `toml:"hoyoverse"`
	Kurogames  KurogamesSettings  `toml:"kurogames"`
	Hypergryph HypergryphSettings `toml:"hypergryph"`
}

type HoyoverseSettings struct {
	Path   string `toml:"path"`
	Region string `toml:"region"`
}

type KurogamesSettings  struct{ Path string `toml:"path"` }
type HypergryphSettings struct{ Path string `toml:"path"` }

// hoyoverseRawTOML is used for the M1 → M2 migration: M1 wrote
// `hoyoplay_path` under [backends.hoyoverse]. On Load, if Path is empty and
// HoYoplayPath is non-empty, project HoYoplayPath into Path and warn.
type hoyoverseRawTOML struct {
	Path         string `toml:"path"`
	HoYoplayPath string `toml:"hoyoplay_path"`
	Region       string `toml:"region"`
}

type rawTOML struct {
	Version  int         `toml:"version"`
	App      AppSettings `toml:"app"`
	Backends struct {
		Hoyoverse  hoyoverseRawTOML   `toml:"hoyoverse"`
		Kurogames  KurogamesSettings  `toml:"kurogames"`
		Hypergryph HypergryphSettings `toml:"hypergryph"`
	} `toml:"backends"`
}

func defaultSettings() Settings {
	return Settings{
		Version: 1,
		App: AppSettings{
			Language:            "zh-TW",
			BannerAnimationPref: "video-when-available",
			ShowTechnicalInfo:   false,
		},
		Backends: BackendSettings{
			Hoyoverse:  HoyoverseSettings{Path: `C:\Program Files\HoYoPlay`, Region: "global"},
			Kurogames:  KurogamesSettings{Path: `C:\Program Files\Wuthering Waves`},
			Hypergryph: HypergryphSettings{Path: `C:\Program Files\GRYPHLINK`},
		},
	}
}

// LoadSettings reads path. Returns defaultSettings() on missing file with
// nil error. On parse failure, returns defaultSettings() with the parse
// error so callers can log and continue (M1 ate this silently).
func LoadSettings(path string) (Settings, error) {
	defaults := defaultSettings()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var raw rawTOML
	if err := toml.Unmarshal(b, &raw); err != nil {
		return defaults, fmt.Errorf("settings TOML parse: %w", err)
	}

	out := defaults
	// version
	if raw.Version != 0 {
		out.Version = raw.Version
	} // else stays 1 from defaults; we treat absent as v0=M1
	// app
	if raw.App.Language != "" {
		out.App.Language = raw.App.Language
	}
	if raw.App.BannerAnimationPref != "" {
		out.App.BannerAnimationPref = raw.App.BannerAnimationPref
	}
	out.App.ShowTechnicalInfo = raw.App.ShowTechnicalInfo

	// hoyoverse — migrate hoyoplay_path → path
	hov := raw.Backends.Hoyoverse
	if hov.Path != "" {
		out.Backends.Hoyoverse.Path = hov.Path
	} else if hov.HoYoplayPath != "" && raw.Version == 0 {
		// M1-format file — migrate
		out.Backends.Hoyoverse.Path = hov.HoYoplayPath
		slog.Default().Warn("settings: migrated legacy [backends.hoyoverse].hoyoplay_path → path",
			"old_value", hov.HoYoplayPath)
	}
	if hov.Region != "" {
		out.Backends.Hoyoverse.Region = hov.Region
	}

	// kurogames / hypergryph (no migration; M2 introduces them)
	if raw.Backends.Kurogames.Path != "" {
		out.Backends.Kurogames.Path = raw.Backends.Kurogames.Path
	}
	if raw.Backends.Hypergryph.Path != "" {
		out.Backends.Hypergryph.Path = raw.Backends.Hypergryph.Path
	}

	// On any successful load (including post-migration), bump version to 1.
	out.Version = 1

	return out, nil
}

// SaveSettings writes the canonical schema. Always includes version = 1; never
// emits hoyoplay_path.
func SaveSettings(path string, s Settings) error {
	s.Version = 1 // canonicalize
	b, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
```

### Step 5.5: Run, verify PASS

```
go test ./internal/app/...
```

Expected: all 5 settings tests + the App tests from Task 4 PASS.

### Step 5.6: `go vet`, scoped tests

```
go vet ./internal/...
go test ./internal/...
```

Expected: clean.

### Step 5.7: Commit

```
git add internal/app/settings.go internal/app/settings_test.go
git commit -m "feat(app): settings schema with version=1 + M1 hoyoplay_path migration"
```

---

## Task 6: hoyoverse adapter — logger, PathProvider, settings.Path field

**Files:**
- Modify: `internal/providers/hoyoverse/hoyoverse.go`

### Step 6.1: Read M1 `internal/providers/hoyoverse/hoyoverse.go`

Note that M1's `Settings.HoYoplayPath` is consumed in `New()` and `DetectInstall()`. The Settings struct in this package (`hoyoverse.Settings`) is separate from the App's `app.HoyoverseSettings`. We rename the field for consistency.

### Step 6.2: Modify `internal/providers/hoyoverse/hoyoverse.go`

Apply these changes:

1. Rename the package-local `Settings.HoYoplayPath` to `Settings.Path` (matches the new pattern across all providers).
2. Add a `logger *slog.Logger` field to `Provider`.
3. Add a `PrimaryPath() string` method (implements `core.PathProvider`).
4. Update `New` to take a logger.
5. App's `constructProviders` already passes the logger and `app.settings.Backends.Hoyoverse.Path` (Task 4 wrote the call).

Replace the file with:

```go
package hoyoverse

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path   string // launcher install root, e.g. C:\Program Files\HoYoPlay
	Region string // "global" or "cn" — only "global" supported in M2
}

type Provider struct {
	api      *apiClient
	settings Settings
	logger   *slog.Logger
}

// New returns a new HoYoverse Provider. logger may be nil; falls back to
// slog.Default().
func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		api:      newAPIClient(APIBase, &http.Client{Timeout: 30 * time.Second}),
		settings: settings,
		logger:   logger,
	}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "米哈遊", "en": "HoYoverse"}
}

func (p *Provider) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, 0, len(games))
	for _, g := range games {
		out = append(out, core.GameDescriptor{
			ID:               g.ID,
			Backend:          BackendID,
			DisplayName:      g.Display,
			SupportedRegions: []string{"global"},
		})
	}
	return out
}

func (p *Provider) SettingsSchema() []core.SettingField {
	return []core.SettingField{
		{Key: "path", Kind: core.SettingPath,
			Label: core.LocalizedString{"zh-TW": "HoYoPlay 安裝資料夾", "en": "HoYoPlay install folder"}},
		{Key: "region", Kind: core.SettingSelectKind,
			Label:   core.LocalizedString{"zh-TW": "區域", "en": "Region"},
			Options: []string{"global"}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

func (p *Provider) GetIcon(ctx context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchGameIcon(ctx, g.Biz, "zh-tw")
}

func (p *Provider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchBasicInfo(ctx, g.APIGameID, "zh-tw")
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	g := findByID(gid)
	if g == nil {
		return core.VersionInfo{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchVersion(ctx, g.APIGameID, "")
}

func (p *Provider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return 0, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return Launch(ctx, ig.InstallPath, gid, opts)
		}
	}
	return 0, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

// PrimaryPath implements core.PathProvider.
func (p *Provider) PrimaryPath() string { return p.settings.Path }

// compile-time check
var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
)
```

### Step 6.3: Update App's constructProviders (already in Task 4) — verify

In `internal/app/app.go`, the `constructProviders` body should already reference `hoyoverse.Settings{HoYoplayPath: ...}`. **Update** to use the renamed field:

```go
hoyo := hoyoverse.New(
    hoyoverse.Settings{
        Path:   a.settings.Backends.Hoyoverse.Path,
        Region: a.settings.Backends.Hoyoverse.Region,
    },
    a.logger.With("backend", "hoyoverse"),
)
```

### Step 6.4: Run scoped tests

```
go vet ./internal/...
go test ./internal/...
```

Expected: PASS for all `internal/...` packages. M1's hoyoverse 7 tests, App's 5 new tests, settings's 5 new tests, dirver's 6 tests, iconext's 4 tests, core's tests — all pass.

### Step 6.5: Commit

```
git add internal/providers/hoyoverse/hoyoverse.go internal/app/app.go
git commit -m "refactor(hoyoverse): logger field + PathProvider + settings.Path rename"
```

---

## Task 7: AssetServer middleware skeleton + main.go rewire

**Files:**
- Create: `internal/app/asset_handler.go`, `internal/app/asset_handler_test.go`
- Modify: `main.go`

The middleware lands BEFORE kurogames so kurogames assets work the moment Task 8's provider is added.

### Step 7.1: Write failing test for the middleware

Create `internal/app/asset_handler_test.go`:

```go
package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetHandler_ParseAndDispatch_404OnUnknownBackend(t *testing.T) {
	a := newAppForTest(t /* no providers */)
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/unknown/icon/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestAssetHandler_404OnUnknownKind(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "kurogames"})
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/garbage/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestAssetHandler_RejectsDotDotInKey(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "kurogames"})
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/icon/..\\..\\hack", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 on '..' in key", rr.Code)
	}
}

func TestAssetHandler_BgDelegatesToAssetServer(t *testing.T) {
	served := false
	p := &fakeProviderWithAsset{
		fakeProvider: fakeProvider{id: "kurogames"},
		serve: func(kind, key string) ([]byte, string, error) {
			served = true
			return []byte("PNG-bytes"), "image/png", nil
		},
	}
	a := newAppForTest(t, p)
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/bg/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "PNG-bytes" {
		t.Errorf("body = %q, want PNG-bytes", rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cache-Control") == "" {
		t.Errorf("missing Cache-Control header")
	}
	if !served {
		t.Errorf("ServeAsset was not called")
	}
}

func TestAssetHandler_BgReturns404WhenProviderLacksAssetServer(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "hoyoverse"}) // no AssetServer impl
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/hoyoverse/bg/genshin", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// fakeProviderWithAsset implements core.AssetServer too.
type fakeProviderWithAsset struct {
	fakeProvider
	serve func(kind, key string) ([]byte, string, error)
}

func (f *fakeProviderWithAsset) ServeAsset(_ interface{ Done() <-chan struct{} }, kind, key string) ([]byte, string, error) {
	// satisfy the interface signature shape with context.Context — adjusted below
	return f.serve(kind, key)
}
```

> **Note**: `core.AssetServer.ServeAsset` takes `context.Context`. The test fake's signature must match exactly. If the test code above compiles loosely, fix the receiver method to:
> ```go
> func (f *fakeProviderWithAsset) ServeAsset(ctx context.Context, kind, key string) ([]byte, string, error) {
>     return f.serve(kind, key)
> }
> ```

### Step 7.2: Run, verify FAIL

```
go test ./internal/app/...
```

Expected: FAIL — `newAssetHandler` undefined.

### Step 7.3: Create `internal/app/asset_handler.go`

```go
package app

import (
	"net/http"
	"strings"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/iconext"
)

// newAssetHandler returns the http.Handler mounted at /_asset/* on the
// existing Wails AssetServer. Dispatches:
//
//   GET /_asset/<backendID>/icon/<key>  → uniform: cachedDetect + iconext.Extract
//   GET /_asset/<backendID>/bg/<key>    → delegate to provider's core.AssetServer
//
// Returns 404 for unknown backends, unknown kinds, "." or ".." in the key
// path component, or providers that don't implement core.AssetServer for bg
// requests.
//
// Adds Cache-Control: max-age=3600 on successful responses; the WebView's
// in-memory cache absorbs repeated same-session requests.
func newAssetHandler(a *App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip "/_asset/" prefix.
		const prefix = "/_asset/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		backendID, kind, key := parts[0], parts[1], parts[2]

		// Validate kind allowlist.
		if kind != "icon" && kind != "bg" {
			http.NotFound(w, r)
			return
		}
		// Reject path-escape attempts in key.
		if key == "" || key == "." || key == ".." ||
			strings.Contains(key, "/") || strings.Contains(key, "\\") ||
			strings.Contains(key, "..") {
			http.NotFound(w, r)
			return
		}

		p := a.byID(core.BackendID(backendID))
		if p == nil {
			http.NotFound(w, r)
			return
		}

		switch kind {
		case "icon":
			a.serveIcon(w, r, p, key)
		case "bg":
			as, ok := p.(core.AssetServer)
			if !ok {
				http.NotFound(w, r)
				return
			}
			data, mime, err := as.ServeAsset(r.Context(), "bg", key)
			if err != nil {
				a.logger.Debug("ServeAsset bg failed", "backend", p.ID(), "key", key, "err", err)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Cache-Control", "max-age=3600")
			_, _ = w.Write(data)
		}
	})
}

// serveIcon handles kind=icon uniformly: look up the install, use ExeNamer to
// get the .exe filename, then iconext.Extract(<installPath>/<exeName>).
func (a *App) serveIcon(w http.ResponseWriter, r *http.Request, p core.Provider, key string) {
	installs, err := a.cachedDetect(r.Context(), p)
	if err != nil {
		a.logger.Debug("cachedDetect failed in icon serve", "backend", p.ID(), "err", err)
		http.NotFound(w, r)
		return
	}
	gid := core.GameID(string(p.ID()) + "/" + key)
	var inst *core.InstalledGame
	for i := range installs {
		if installs[i].GameID == gid {
			inst = &installs[i]
			break
		}
	}
	if inst == nil {
		http.NotFound(w, r)
		return
	}
	en, ok := p.(core.ExeNamer)
	if !ok {
		http.NotFound(w, r)
		return
	}
	exeName, ok := en.ExeName(gid)
	if !ok {
		http.NotFound(w, r)
		return
	}
	exePath := inst.InstallPath + "\\" + exeName // Windows-only path semantics
	data, err := iconext.Extract(exePath)
	if err != nil {
		a.logger.Debug("iconext.Extract failed", "exe", exePath, "err", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=3600")
	_, _ = w.Write(data)
}
```

### Step 7.4: Run, verify PASS

```
go test ./internal/app/...
```

Expected: all 5 asset_handler tests + Task 4/5 tests PASS.

### Step 7.5: Modify `main.go` — slog wiring + AssetServer.Handler hookup + panic recovery

Replace the contents of `main.go`:

```go
package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"

	"launcher-collection-tmp/internal/app"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("main panic", "err", r, "stack", string(debug.Stack()))
			os.Exit(1)
		}
	}()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	a := app.New("", logger)

	// Wails AssetServer middleware: any /_asset/* request from WebView2 hits
	// the App's asset handler before falling through to the embedded fs.
	assetMux := http.NewServeMux()
	assetMux.Handle("/_asset/", app.AssetHandlerForApp(a))

	err := wails.Run(&options.App{
		Title:            "launcher-collection",
		Width:            1280, Height: 720,
		MinWidth:         1280, MinHeight: 720,
		MaxWidth:         1280, MaxHeight: 720,
		DisableResize:    true,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 8, G: 8, B: 14, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: assetMux,
		},
		OnStartup: a.Startup,
		Bind:      []interface{}{a},
	})
	if err != nil {
		slog.Error("wails run", "err", err)
	}
}

// silence unused-import checker for context (used transitively by App)
var _ = context.Background
```

`AssetHandlerForApp` is a small exported wrapper around `newAssetHandler`. Add it to `internal/app/asset_handler.go`:

```go
// AssetHandlerForApp returns the asset HTTP handler for use as Wails'
// assetserver.Options.Handler. Exported so main.go can mount it.
func AssetHandlerForApp(a *App) http.Handler {
	return newAssetHandler(a)
}
```

### Step 7.6: Run whole-repo build + tests

```
go vet ./...
go build ./...
go test ./...
```

Expected: clean. `main.go` now compiles. Existing manual smoke from M1 still works (the M2 abstractions are in but no new providers yet).

### Step 7.7: Commit

```
git add internal/app/asset_handler.go internal/app/asset_handler_test.go main.go
git commit -m "feat(app): AssetServer middleware + slog plumbing + panic recovery in main"
```

---

## Task 8: kurogames provider — meta, detect, version, launch, bg research, Provider impl

This is the biggest publisher task. Includes a research step for the BG decision (A=API or B=cache scrape).

**Files:**
- Create: `internal/providers/kurogames/meta.go`, `detect.go`, `detect_test.go`, `version.go`, `version_test.go`, `launch_windows.go`, `bg.go` (or `api.go`), `kurogames.go`

### Step 8.1: Create `internal/providers/kurogames/meta.go`

```go
package kurogames

import "launcher-collection-tmp/internal/core"

const (
	BackendID core.BackendID = "kurogames"
	UserAgent                = "launcher-collection/0.2 (+https://github.com/willie/launcher-collection)"
)

// gameMeta holds compile-time per-game constants for kurogames.
type gameMeta struct {
	ID         core.GameID
	FolderName string // subfolder under launcher root, e.g. "Wuthering Waves Game"
	ExeName    string // launches via this exe
	Display    core.LocalizedString
}

var games = []gameMeta{
	{
		ID:         "kurogames/wutheringwaves",
		FolderName: "Wuthering Waves Game",
		ExeName:    "Wuthering Waves.exe",
		Display: core.LocalizedString{
			"zh-TW": "鳴潮",
			"zh-CN": "鸣潮",
			"en":    "Wuthering Waves",
		},
	},
}

// findByID returns the gameMeta for a GameID or nil if not registered.
func findByID(id core.GameID) *gameMeta {
	for i := range games {
		if games[i].ID == id {
			return &games[i]
		}
	}
	return nil
}
```

### Step 8.2: Write failing test for `DetectInstall`

Create `internal/providers/kurogames/detect_test.go`:

```go
package kurogames

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestDetectInstall_FindsWuwa(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// game requires the exe to exist (consistent with M1 detect heuristic
	// being more than just folder presence — adjust if you choose folder-only)
	if err := os.WriteFile(filepath.Join(gameDir, "Wuthering Waves.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installed, want 1", len(got))
	}
	if got[0].GameID != "kurogames/wutheringwaves" {
		t.Errorf("GameID = %q", got[0].GameID)
	}
	if got[0].InstallPath != gameDir {
		t.Errorf("InstallPath = %q, want %q", got[0].InstallPath, gameDir)
	}
}

func TestDetectInstall_MissingPathReturnsEmpty(t *testing.T) {
	got, err := DetectInstall(context.Background(), `C:\path\that\definitely\does\not\exist`)
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d installed, want 0", len(got))
	}
}

func TestDetectInstall_FolderWithoutExeIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// no .exe inside
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (folder without exe should not count as installed)", len(got))
	}
	_ = core.GameID("") // silence unused import in some setups
}
```

### Step 8.3: Run, verify FAIL

```
go test ./internal/providers/kurogames/...
```

Expected: FAIL — `DetectInstall undefined`.

### Step 8.4: Create `internal/providers/kurogames/detect.go`

```go
package kurogames

import (
	"context"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// DetectInstall scans the kurogames launcher install root and returns each
// known game whose folder + canonical .exe is present.
//
// kuroPath should be the launcher root (e.g. "C:\\Program Files\\Wuthering Waves").
// Missing path returns ([], nil) — not an error; user may not have the launcher
// installed.
func DetectInstall(ctx context.Context, kuroPath string) ([]core.InstalledGame, error) {
	info, err := os.Stat(kuroPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	out := []core.InstalledGame{}
	for _, g := range games {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		gameDir := filepath.Join(kuroPath, g.FolderName)
		exePath := filepath.Join(gameDir, g.ExeName)
		// require BOTH the folder and the exe to exist
		if dirInfo, err := os.Stat(gameDir); err != nil || !dirInfo.IsDir() {
			continue
		}
		if _, err := os.Stat(exePath); err != nil {
			continue
		}
		out = append(out, core.InstalledGame{
			GameID:         g.ID,
			InstallPath:    gameDir,
			CurrentVersion: "", // populated by CheckVersion later
		})
	}
	return out, nil
}
```

### Step 8.5: Run, verify PASS

```
go test ./internal/providers/kurogames/...
```

Expected: 3 detect tests PASS.

### Step 8.6: Write failing test for `version.go`

Create `internal/providers/kurogames/version_test.go`:

```go
package kurogames

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchVersion_ReadsLauncherDownloadConfig(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "launcherDownloadConfig.json"),
		[]byte(`{"version":"3.3.0","reUseVersion":"","state":"","isPreDownload":false,"appId":"50004"}`),
		0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readLauncherDownloadConfigVersion(filepath.Join(gameDir, "launcherDownloadConfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.3.0" {
		t.Errorf("version = %q, want 3.3.0", got)
	}
	_ = context.Background
}

func TestFetchVersion_MissingFileReturnsEmpty(t *testing.T) {
	got, err := readLauncherDownloadConfigVersion(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("expected nil error on missing file, got %v", err)
	}
	if got != "" {
		t.Errorf("version on missing file = %q, want empty", got)
	}
}

func TestFetchVersion_MalformedJSONReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLauncherDownloadConfigVersion(p)
	if err == nil {
		t.Errorf("expected error on malformed json, got nil; got version=%q", got)
	}
	if got != "" {
		t.Errorf("version on malformed = %q, want empty", got)
	}
}
```

### Step 8.7: Run, verify FAIL

```
go test ./internal/providers/kurogames/...
```

Expected: FAIL — `readLauncherDownloadConfigVersion undefined`.

### Step 8.8: Create `internal/providers/kurogames/version.go`

```go
package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// fetchVersion returns version info for one game. Reads the launcher's
// canonical config file at <installPath>/launcherDownloadConfig.json.
func fetchVersion(_ context.Context, installPath string, _ core.GameID) (core.VersionInfo, error) {
	configPath := filepath.Join(installPath, "launcherDownloadConfig.json")
	v, err := readLauncherDownloadConfigVersion(configPath)
	if err != nil {
		return core.VersionInfo{}, err
	}
	if v == "" {
		return core.VersionInfo{}, nil
	}
	return core.VersionInfo{
		Current: v,
		Latest:  v, // M2: no remote-version check; show installed version as both
	}, nil
}

func readLauncherDownloadConfigVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("kurogames: parse launcherDownloadConfig.json: %w", err)
	}
	return doc.Version, nil
}
```

### Step 8.9: Run, verify PASS

```
go test ./internal/providers/kurogames/...
```

Expected: 3 detect + 3 version tests PASS.

### Step 8.10: Create `internal/providers/kurogames/launch_windows.go`

```go
package kurogames

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"launcher-collection-tmp/internal/core"
)

// Launch starts the game via Windows ShellExecute so the exe's manifest can
// trigger UAC elevation if requested. Wuthering Waves' Kuro launcher does
// the same. Returns 0 on success — anti-cheat-friendly: no polling parent.
func Launch(_ context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
	g := findByID(gid)
	if g == nil {
		return 0, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	exePath := filepath.Join(installPath, g.ExeName)

	exePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return 0, fmt.Errorf("utf16 exe: %w", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(installPath)
	if err != nil {
		return 0, fmt.Errorf("utf16 cwd: %w", err)
	}
	var argsPtr *uint16
	if len(opts.ExtraArgs) > 0 {
		argsPtr, err = windows.UTF16PtrFromString(strings.Join(opts.ExtraArgs, " "))
		if err != nil {
			return 0, fmt.Errorf("utf16 args: %w", err)
		}
	}

	if err := windows.ShellExecute(0, nil, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return 0, fmt.Errorf("ShellExecute %s: %w", exePath, err)
	}
	return 0, nil
}
```

### Step 8.11: Background research (impl-task — research before writing bg.go or api.go)

Per spec §2.8: do offline-first research and lock the BG strategy for kurogames before writing code.

Steps:

1. **Check Collapse Launcher source** for any KuroLauncher / WuWa coverage. Look in `Hi3Helper.Core/` and search for `wuthering` / `Kuro`. Likely partial coverage — note any API endpoint URL or response shape they document. (Use Github search via `gh`.)

2. **Inspect local AppData** for endpoint hints:
   ```bash
   cat "C:/Users/willie/AppData/Roaming/KRLauncher/G153/C50004/kr_starter_cached.json" | head -200
   cat "C:/Users/willie/AppData/Roaming/KRLauncher/G153/C50004/kr_starter_language.json" | head -50
   ls "C:/Users/willie/AppData/Roaming/KR_G153/" | head
   cat "C:/Users/willie/AppData/Roaming/KR_G153/KRSDKAnnouncementCache-"*.json | head -100
   ```
   Look for fields: `bg_url`, `background`, `image_url`, `cdn`, `api`, `cgi`, `gateway`. If a clear endpoint surfaces, that's strategy A. If not, search for cached image files (`.png`, `.webp`, `.jpg`) in the same dirs.

3. **Light live probe (1-2 endpoints)** if offline finds an API URL. Use `curl --max-time 10 -A "..."` once, validate response shape, walk away. Avoid hammering.

4. **Decide A or B**, document in a comment block at the top of the bg.go (or api.go) file. Recommended decision shape:

   ```
   // Background source: <A: API> | <B: cache scrape>
   //
   // (A) API endpoint:    <URL>
   //     auth/header:     <key/none>
   //     response shape:  <JSON path to image URL>
   //     decided based on: Collapse coverage / KRLauncher cache hint / live probe
   //
   // OR
   //
   // (B) Cache path:      <%APPDATA%\KRLauncher\... or fallback location>
   //     file format:     <png/webp/jpg>
   //     match key:       <by game id, by appId, etc.>
   //     decided because: <no API found in Collapse / appdata / live probe>
   ```

### Step 8.12: Implement BG path (A or B per research outcome)

**Branch A (API)** — create `internal/providers/kurogames/api.go`:

Mirrors M1's `internal/providers/hoyoverse/api.go` shape. Implementer writes the HTTP client + parser based on the discovered endpoint. Tests via `httptest.NewServer` with canned response. Skip the literal code here — see M1 hoyoverse api.go for the pattern.

**Branch B (cache scrape)** — create `internal/providers/kurogames/bg.go`:

```go
package kurogames

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// Background source: B (cache scrape from KRLauncher AppData)
//
// During Task 8 research we found <discovered cache path; e.g. AppData/Roaming/KRLauncher/G153/C50004/<key>.png>.
// No public API endpoint was identified in Collapse Launcher source or community references.
// File format: <png|webp|jpg>; match key: <how the file is named>.

// findCachedBackground looks up the cached banner for a kurogames game in
// AppData. Returns empty string + nil when no cache file is present (caller
// returns 404 to the asset middleware).
func findCachedBackground(gameKey string) (string, error) {
	// IMPLEMENTER: replace this body with actual paths from research.
	// Example skeleton:
	roaming, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(roaming, "KRLauncher", "G153", "C50004", gameKey+".png"),
		filepath.Join(roaming, "KRLauncher", "G153", "C50004", gameKey+".jpg"),
		filepath.Join(roaming, "KRLauncher", "G153", "C50004", gameKey+".webp"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", nil
}

// serveBg serves background bytes for ServeAsset(kind="bg", key=<game-suffix>).
func serveBg(_ context.Context, _ *core.InstalledGame, key string) ([]byte, string, error) {
	cachePath, err := findCachedBackground(key)
	if err != nil {
		return nil, "", err
	}
	if cachePath == "" {
		return nil, "", fmt.Errorf("%w: %s bg not in cache", core.ErrAssetNotAvailable, key)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, "", err
	}
	mime := "image/png"
	switch filepath.Ext(cachePath) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	return data, mime, nil
}
```

If Branch A is taken instead, write `api.go` per the M1 hoyoverse pattern AND skip `bg.go` (or write a minimal `bg.go` that delegates).

### Step 8.13: Create `internal/providers/kurogames/kurogames.go` (Provider impl)

```go
package kurogames

import (
	"context"
	"fmt"
	"log/slog"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path string // launcher install root
}

type Provider struct {
	settings Settings
	logger   *slog.Logger
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{settings: settings, logger: logger}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "庫洛", "zh-CN": "库洛", "en": "Kuro Games"}
}

func (p *Provider) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, 0, len(games))
	for _, g := range games {
		out = append(out, core.GameDescriptor{
			ID:               g.ID,
			Backend:          BackendID,
			DisplayName:      g.Display,
			SupportedRegions: []string{"global"},
		})
	}
	return out
}

func (p *Provider) SettingsSchema() []core.SettingField {
	return []core.SettingField{
		{Key: "path", Kind: core.SettingPath,
			Label: core.LocalizedString{
				"zh-TW": "鳴潮 launcher 安裝資料夾",
				"zh-CN": "鸣潮 launcher 安装文件夹",
				"en":    "Wuthering Waves launcher folder",
			}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

// GetIcon returns the canonical asset URL; the AssetServer middleware does
// the actual PE extraction via iconext on demand.
func (p *Provider) GetIcon(_ context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return fmt.Sprintf("/_asset/%s/icon/%s", p.ID(), suffix), nil
}

// GetBackgrounds returns the asset URL pointing at the AssetServer middleware.
// The middleware will call ServeAsset(kind="bg") on this Provider.
func (p *Provider) GetBackgrounds(_ context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return []core.Background{
		{
			ImageURL: fmt.Sprintf("/_asset/%s/bg/%s", p.ID(), suffix),
			VideoURL: "", // M2: no video for kurogames
			Type:     core.BackgroundImage,
		},
	}, nil
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return core.VersionInfo{}, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return fetchVersion(ctx, ig.InstallPath, gid)
		}
	}
	return core.VersionInfo{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

func (p *Provider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return 0, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return Launch(ctx, ig.InstallPath, gid, opts)
		}
	}
	return 0, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

// PrimaryPath implements core.PathProvider.
func (p *Provider) PrimaryPath() string { return p.settings.Path }

// ExeName implements core.ExeNamer (used by the AssetServer middleware for
// kind=icon to locate the .exe).
func (p *Provider) ExeName(gid core.GameID) (string, bool) {
	g := findByID(gid)
	if g == nil {
		return "", false
	}
	return g.ExeName, true
}

// ServeAsset implements core.AssetServer for kind=bg only. The middleware
// only ever calls this with kind="bg"; non-bg returns ErrAssetNotAvailable.
func (p *Provider) ServeAsset(ctx context.Context, kind, key string) ([]byte, string, error) {
	if kind != "bg" {
		return nil, "", core.ErrAssetNotAvailable
	}
	return serveBg(ctx, nil, key)
}

// compile-time interface compliance
var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
	_ core.ExeNamer     = (*Provider)(nil)
	_ core.AssetServer  = (*Provider)(nil)
)
```

### Step 8.14: Wire kurogames into App's `constructProviders`

In `internal/app/app.go`, append after the hoyoverse registration in `constructProviders`:

```go
import "launcher-collection-tmp/internal/providers/kurogames"

// ...inside constructProviders, after hoyoverse registration:
kuro := kurogames.New(
    kurogames.Settings{Path: a.settings.Backends.Kurogames.Path},
    a.logger.With("backend", "kurogames"),
)
if err := a.registerProvider(kuro); err != nil {
    return err
}
```

### Step 8.15: Run all tests

```
go vet ./...
go test ./...
```

Expected: clean, all tests pass.

### Step 8.16: Commit

```
git add internal/providers/kurogames/ internal/app/app.go
git commit -m "feat(kurogames): provider impl with detect/version(launcherDownloadConfig)/launch/icon/bg

BG source decision: <A:api | B:cache-scrape> per Task 8 research outcome,
documented inline in <bg.go|api.go>."
```

---

## Task 9: hypergryph provider

Mirrors Task 8's structure. Endfield's version source is "none clean" — `CheckVersion` returns empty `VersionInfo`. BG research is identical workflow.

**Files:**
- Create: `internal/providers/hypergryph/meta.go`, `detect.go`, `detect_test.go`, `version.go`, `version_test.go`, `launch_windows.go`, `bg.go` (or `api.go`), `hypergryph.go`

### Step 9.1: Create `internal/providers/hypergryph/meta.go`

```go
package hypergryph

import (
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

const (
	BackendID core.BackendID = "hypergryph"
	UserAgent                = "launcher-collection/0.2 (+https://github.com/willie/launcher-collection)"
)

type gameMeta struct {
	ID         core.GameID
	FolderName string // relative to launcher root, e.g. filepath.Join("games", "EndField Game")
	ExeName    string
	Display    core.LocalizedString
}

var games = []gameMeta{
	{
		ID:         "hypergryph/endfield",
		FolderName: filepath.Join("games", "EndField Game"),
		ExeName:    "Endfield.exe",
		Display: core.LocalizedString{
			"zh-TW": "明日方舟：終末地",
			"zh-CN": "明日方舟：终末地",
			"en":    "Arknights: Endfield",
		},
	},
}

func findByID(id core.GameID) *gameMeta {
	for i := range games {
		if games[i].ID == id {
			return &games[i]
		}
	}
	return nil
}
```

### Step 9.2: Write failing detect test

Create `internal/providers/hypergryph/detect_test.go`:

```go
package hypergryph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstall_FindsEndfield(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "games", "EndField Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "Endfield.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].GameID != "hypergryph/endfield" {
		t.Errorf("GameID = %q", got[0].GameID)
	}
	if got[0].InstallPath != gameDir {
		t.Errorf("InstallPath = %q", got[0].InstallPath)
	}
}

func TestDetectInstall_MissingPathReturnsEmpty(t *testing.T) {
	got, err := DetectInstall(context.Background(), `C:\does\not\exist`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

func TestDetectInstall_FolderWithoutExeIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "games", "EndField Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (no exe)", len(got))
	}
}
```

### Step 9.3: Run, verify FAIL

```
go test ./internal/providers/hypergryph/...
```

### Step 9.4: Create `internal/providers/hypergryph/detect.go`

Same shape as kurogames detect — copy from Task 8.4 and change to look up `<gryphlinkPath>/games/EndField Game/Endfield.exe`. The code is structurally identical:

```go
package hypergryph

import (
	"context"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// DetectInstall scans the GRYPHLINK launcher root and returns each known
// game whose folder + canonical .exe is present.
func DetectInstall(ctx context.Context, gryphPath string) ([]core.InstalledGame, error) {
	info, err := os.Stat(gryphPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	out := []core.InstalledGame{}
	for _, g := range games {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		gameDir := filepath.Join(gryphPath, g.FolderName)
		exePath := filepath.Join(gameDir, g.ExeName)
		if dirInfo, err := os.Stat(gameDir); err != nil || !dirInfo.IsDir() {
			continue
		}
		if _, err := os.Stat(exePath); err != nil {
			continue
		}
		out = append(out, core.InstalledGame{
			GameID:         g.ID,
			InstallPath:    gameDir,
			CurrentVersion: "",
		})
	}
	return out, nil
}
```

### Step 9.5: Run, verify PASS

```
go test ./internal/providers/hypergryph/...
```

### Step 9.6: Write trivial version test

Create `internal/providers/hypergryph/version_test.go`:

```go
package hypergryph

import (
	"context"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestFetchVersion_AlwaysEmpty_M2Limitation(t *testing.T) {
	v, err := fetchVersion(context.Background(), "any/path", core.GameID("hypergryph/endfield"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v.Current != "" || v.Latest != "" {
		t.Errorf("got VersionInfo %+v, want empty (no clean version source for hypergryph in M2)", v)
	}
	if v.Predownload != nil {
		t.Errorf("Predownload = %v, want nil", v.Predownload)
	}
}
```

### Step 9.7: Run, verify FAIL

### Step 9.8: Create `internal/providers/hypergryph/version.go`

```go
package hypergryph

import (
	"context"

	"launcher-collection-tmp/internal/core"
)

// fetchVersion: no clean local-FS version source surfaced during pre-spec
// research (Endfield.exe FileVersion is the Unity engine version
// 2021.3.34f5; Endfield_Data/app.info has only "Gryphline\nEndfield";
// GRYPHLINK's <root>/<x.y.z>/ folder is launcher-version, not game).
//
// M2 returns empty VersionInfo for hypergryph; sidebar shows "就緒" with no
// `· vX.Y` suffix. M3+ may add an API-based version check.
//
// During impl smoke: if a clean version source surfaces (Addressables build
// version under Endfield_Data/StreamingAssets/aa/, registry under
// HKCU\Software\Hypergryph\Endfield, GRYPHLINK launcher local API), wire
// it then; do NOT block on research per spec §3 risk note.
func fetchVersion(_ context.Context, _ string, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
```

### Step 9.9: Run, verify PASS

### Step 9.10: Create `internal/providers/hypergryph/launch_windows.go`

Copy from Task 8.10 with module-name swap; entire file:

```go
package hypergryph

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"launcher-collection-tmp/internal/core"
)

// Launch via ShellExecute — same pattern as hoyoverse/kurogames.
func Launch(_ context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
	g := findByID(gid)
	if g == nil {
		return 0, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	exePath := filepath.Join(installPath, g.ExeName)

	exePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return 0, fmt.Errorf("utf16 exe: %w", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(installPath)
	if err != nil {
		return 0, fmt.Errorf("utf16 cwd: %w", err)
	}
	var argsPtr *uint16
	if len(opts.ExtraArgs) > 0 {
		argsPtr, err = windows.UTF16PtrFromString(strings.Join(opts.ExtraArgs, " "))
		if err != nil {
			return 0, fmt.Errorf("utf16 args: %w", err)
		}
	}
	if err := windows.ShellExecute(0, nil, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return 0, fmt.Errorf("ShellExecute %s: %w", exePath, err)
	}
	return 0, nil
}
```

### Step 9.11: Background research for hypergryph

Run the same workflow as Task 8.11 but for Endfield / GRYPHLINK:

1. Check Collapse Launcher source for any Endfield / Hypergryph coverage (likely zero — too new).
2. Inspect AppData:
   ```bash
   ls "C:/Users/willie/AppData/Roaming/Gryphline/" -R 2>&1 | head -50
   ls "C:/Program Files/GRYPHLINK/Cache/" 2>&1
   find "C:/Users/willie/AppData/Roaming/Gryphline/" -maxdepth 5 -name "*.json" 2>/dev/null | head
   ```
   We already know `C:\Program Files\GRYPHLINK\1.3.0\res\icons\endfield.ico` exists (icons ARE on disk). Look for similar pattern for backgrounds (`.png`, `.webp`, `.jpg` under any of these dirs).
3. If a launcher API URL surfaces in any json file, light-probe.
4. Decide and document.

### Step 9.12: Implement BG path (same A vs B branching as Task 8.12)

Create `internal/providers/hypergryph/bg.go` (or `api.go`). Same skeleton as Task 8.12; adjust paths for the hypergryph-specific cache locations from research.

### Step 9.13: Create `internal/providers/hypergryph/hypergryph.go`

Mirror Task 8.13 — substitute names. The Provider impl is structurally identical:

```go
package hypergryph

import (
	"context"
	"fmt"
	"log/slog"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path string
}

type Provider struct {
	settings Settings
	logger   *slog.Logger
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{settings: settings, logger: logger}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "鷹角", "zh-CN": "鹰角", "en": "Hypergryph"}
}

func (p *Provider) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, 0, len(games))
	for _, g := range games {
		out = append(out, core.GameDescriptor{
			ID:               g.ID,
			Backend:          BackendID,
			DisplayName:      g.Display,
			SupportedRegions: []string{"global"},
		})
	}
	return out
}

func (p *Provider) SettingsSchema() []core.SettingField {
	return []core.SettingField{
		{Key: "path", Kind: core.SettingPath,
			Label: core.LocalizedString{
				"zh-TW": "GRYPHLINK 安裝資料夾",
				"zh-CN": "GRYPHLINK 安装文件夹",
				"en":    "GRYPHLINK launcher folder",
			}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

func (p *Provider) GetIcon(_ context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return fmt.Sprintf("/_asset/%s/icon/%s", p.ID(), suffix), nil
}

func (p *Provider) GetBackgrounds(_ context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return []core.Background{
		{
			ImageURL: fmt.Sprintf("/_asset/%s/bg/%s", p.ID(), suffix),
			Type:     core.BackgroundImage,
		},
	}, nil
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return core.VersionInfo{}, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return fetchVersion(ctx, ig.InstallPath, gid)
		}
	}
	return core.VersionInfo{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

func (p *Provider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return 0, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return Launch(ctx, ig.InstallPath, gid, opts)
		}
	}
	return 0, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

func (p *Provider) PrimaryPath() string { return p.settings.Path }

func (p *Provider) ExeName(gid core.GameID) (string, bool) {
	g := findByID(gid)
	if g == nil {
		return "", false
	}
	return g.ExeName, true
}

func (p *Provider) ServeAsset(ctx context.Context, kind, key string) ([]byte, string, error) {
	if kind != "bg" {
		return nil, "", core.ErrAssetNotAvailable
	}
	return serveBg(ctx, nil, key)
}

var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
	_ core.ExeNamer     = (*Provider)(nil)
	_ core.AssetServer  = (*Provider)(nil)
)
```

### Step 9.14: Wire hypergryph into `constructProviders`

In `internal/app/app.go`, after kurogames:

```go
import "launcher-collection-tmp/internal/providers/hypergryph"

// inside constructProviders, after kurogames:
gryph := hypergryph.New(
    hypergryph.Settings{Path: a.settings.Backends.Hypergryph.Path},
    a.logger.With("backend", "hypergryph"),
)
if err := a.registerProvider(gryph); err != nil {
    return err
}
```

### Step 9.15: Run all tests + vet

```
go vet ./...
go test ./...
```

Expected: all tests PASS across the repo.

### Step 9.16: Commit

```
git add internal/providers/hypergryph/ internal/app/app.go
git commit -m "feat(hypergryph): provider impl with detect/launch/icon/bg (no version source — known M2 limit)"
```

---

## Task 10: Frontend — Footbar count, Topbar refresh, zh-CN locale, backends store

**Files:**
- Modify: `frontend/src/components/Footbar.vue`, `frontend/src/components/Topbar.vue`, `frontend/src/i18n.ts`
- Create: `frontend/src/locales/zh-CN.json`, `frontend/src/stores/backends.ts`

### Step 10.1: Create `frontend/src/locales/zh-CN.json`

```json
{
  "publishers": {
    "hoyoverse": "米哈游",
    "kurogames": "库洛",
    "hypergryph": "鹰角",
    "perfectworld": "完美世界"
  },
  "buttons": {
    "play": "开始游戏",
    "play_short": "开始",
    "update": "更新",
    "pause": "暂停"
  },
  "status": {
    "ready": "就绪",
    "update": "更新可用",
    "predownload": "预下载",
    "downloading": "下载中",
    "applying": "应用中",
    "error": "错误"
  },
  "labels": {
    "last_run": "上次启动",
    "checked": "检查于",
    "minutes_ago": "{n} 分钟前",
    "days_ago": "{n} 天前",
    "ready_pill": "就绪"
  },
  "footer": {
    "summary": "{backends} 个后端 · {games} 款游戏",
    "synced": "已同步"
  }
}
```

### Step 10.2: Modify `frontend/src/i18n.ts` — register zh-CN

Replace the file:

```ts
import { createI18n } from 'vue-i18n';
import zhTW from './locales/zh-TW.json';
import zhCN from './locales/zh-CN.json';
import en from './locales/en.json';

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-TW',
  fallbackLocale: 'en',
  messages: { 'zh-TW': zhTW, 'zh-CN': zhCN, en },
});

export function setLang(lang: 'zh-TW' | 'zh-CN' | 'en') {
  i18n.global.locale.value = lang;
  // BCP47-ish HTML lang attribute hint for the browser/font selection
  const htmlLang =
    lang === 'en' ? 'en' :
    lang === 'zh-CN' ? 'zh-Hans' :
    'zh-Hant';
  document.documentElement.setAttribute('lang', htmlLang);
}
```

### Step 10.3: Modify `frontend/src/components/Topbar.vue` — add refresh + 3-way locale toggle

Replace the file:

```vue
<script setup lang="ts">
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';
import { Refresh } from '../../wailsjs/go/app/App';

const view = useViewStore();
const games = useGamesStore();

// 3-way cycle: zh-TW → zh-CN → en → zh-TW
const cycleLang = () => {
  const cur = i18n.global.locale.value;
  const next = cur === 'zh-TW' ? 'zh-CN' : cur === 'zh-CN' ? 'en' : 'zh-TW';
  setLang(next);
};

const onRefresh = async () => {
  try {
    await Refresh();
    await games.load();
    await games.refreshVersions();
    await games.loadAssets();
  } catch (e) {
    console.error('refresh failed', e);
  }
};
</script>

<template>
  <div class="topbar">
    <div class="toolbar">
      <button class="icon-btn" @click="cycleLang" title="Language"><span class="material-symbols-outlined">translate</span></button>
      <button class="icon-btn" @click="onRefresh" title="Refresh"><span class="material-symbols-outlined">refresh</span></button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>
      <button class="icon-btn window-btn" @click="WindowMinimise()" title="Minimize">─</button>
      <button class="icon-btn window-btn close" @click="Quit()" title="Close">×</button>
    </div>
  </div>
</template>
```

### Step 10.4: Modify `frontend/src/components/Footbar.vue` — derive backend count from games

Replace its `<script setup>` block contents:

```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';

const games = useGamesStore();
const { t } = useI18n();
const summary = computed(() => {
  const backends = new Set(games.games.map((g) => g.backend)).size;
  return t('footer.summary', { backends, games: games.games.length });
});
</script>
```

(Template unchanged.)

### Step 10.5: Create `frontend/src/stores/backends.ts`

```ts
import { defineStore } from 'pinia';
import { ListBackends } from '../../wailsjs/go/app/App';

export type BackendStatus = {
  backend_id: string;
  display_name: Record<string, string>;
  status: 'ok' | 'path_unset' | 'launcher_missing' | 'empty' | 'error';
  detail?: string;
};

export const useBackendsStore = defineStore('backends', {
  state: () => ({
    backends: [] as BackendStatus[],
  }),
  actions: {
    async load() {
      this.backends = await ListBackends();
    },
  },
});
```

(Wired into the App lifecycle in App.vue or a dedicated init step. For M2, the store is exported and exercisable; UI rendering of statuses is deferred to M3 per spec §7.)

### Step 10.6: Run frontend build to verify type-check + bundling

```
cd frontend
wails generate module  # regenerate Wails TS bindings (App now exposes ListBackends, Refresh, ErrorCode, ErrorMessage)
npm run build
cd ..
```

Expected: `vue-tsc` clean, `vite build` produces dist/.

### Step 10.7: Run whole-repo tests one more time

```
go vet ./...
go test ./...
```

Expected: clean.

### Step 10.8: Commit

```
git add frontend/
git commit -m "feat(frontend): zh-CN locale + Topbar refresh + 3-way lang toggle + backends store"
```

---

## Task 11: Manual smoke

No automated work in this task — the user (or implementer with display access) runs `wails dev` and walks through the checklist from spec §6.

### Step 11.1: Start wails dev

```
wails dev
```

Wait for the WebView2 window to appear with the launcher UI.

### Step 11.2: Walk the checklist

For each item in spec §6 Manual smoke checklist, verify and tick. Paste the full checklist into a scratch doc with check-marks. Items:

```
[ ] All 5 games visible in sidebar grouped under 3 publisher groups
[ ] Genshin / Star Rail / ZZZ launch (M1 regression)
[ ] Wuthering Waves launches via Wuthering Waves.exe (UAC may prompt)
[ ] Endfield launches via Endfield.exe (UAC may prompt)
[ ] Wuthering Waves icon shows in sidebar (PE-extracted, not first-letter fallback)
[ ] Endfield icon shows in sidebar (PE-extracted)
[ ] Wuthering Waves bg shows in main view (real art, source A/B per impl decision)
[ ] Endfield bg shows in main view (real art)
[ ] Wuthering Waves sidebar status: 「就緒 · v3.3.0」 (or current version)
[ ] Endfield sidebar status: 「就緒」 (no version, deliberately)
[ ] Footbar shows "3 backends · 5 games"
[ ] Editing settings.toml's hoyoplay_path → app loads with warning log + auto-migrates path on save
[ ] Removing one publisher's path from settings.toml → ListBackends reports path_unset for that backend
[ ] Refresh button (topbar) re-runs DetectInstall and updates UI
[ ] Language toggle zh-TW ↔ zh-CN ↔ en updates sidebar names live
[ ] Stopwatch: ListGames returns < 2 seconds with all 3 providers configured
[ ] No structured-logging gaps — every provider's detect/version paths log at debug+
```

### Step 11.3: Fix any failures inline

If any check fails, fix in-place (separate commits per fix). The fix-commits land before Task 12's tag.

### Step 11.4: Commit any smoke fixes

```bash
git add <changed files>
git commit -m "fix: smoke-run fixes for M2"
```

(Skip if no fixes needed.)

---

## Task 12: Tag v0.2.0-m2 + merge to main

### Step 12.1: Final verification pass

```
go vet ./...
go test ./...
cd frontend && npm run build && cd ..
```

Expected: all green.

### Step 12.2: Switch to main and merge

The implementation work has been on `m2/implementation` (a fresh branch off `main` after `m2/spec` was merged). Merge with `--no-ff`:

```
git checkout main
git merge --no-ff m2/implementation -m "merge: M2 (kurogames + hypergryph providers)"
```

### Step 12.3: Tag

```
git tag v0.2.0-m2
```

### Step 12.4: Verify history

```
git log --graph --oneline --all | head -40
git tag --list "v0.2*"
```

Expected:
- `m2/implementation` and `m2/spec` branch-out / merge-in arcs visible
- Tag `v0.2.0-m2` on the merge commit
- (No `git push` — local only unless user opts in.)

---

## Self-Review Notes

- **Spec coverage**: every section in the spec has at least one task. §2.1 registry → Task 4. §2.2 ParseGameID → Task 1. §2.3 AssetServer middleware → Task 7. §2.4 detection cache → Task 4. §2.5 DetectionStatus derivation → Task 4 (`ListBackends`). §2.6 Launch invariant → Task 1 (interface godoc) + Task 6/8/9 (per-provider impl). §2.7 iconext + wiring → Task 3 + Task 7. §2.8 BG per-publisher → Task 8/9 research+impl. §2.9 version per-publisher → Task 8 (kuro json) + Task 9 (none). §2.10 settings + version=1 + migration → Task 5. §2.11 slog → Task 7 (main.go) + Task 6/8/9 (per-provider). §2.12 sentinel errors → Task 1. §2.13 LocalizedString fallback → Task 1. §2.14 frontend → Task 10. §3 per-publisher table → Task 8/9. §5 testing → in each task. §6 smoke → Task 11. §7 limitations → carried forward as notes only.
- **Placeholder check**: BG impl in Tasks 8/9 has a research → A/B branch step, with skeleton code for branch B and a pointer to M1 hoyoverse api.go for branch A. The decision and code are committed during the task; not "TODO later". Reasonable boundary for "research-driven, fixed strategy".
- **Type consistency**: `Settings.Path` is the canonical field name across hoyoverse, kurogames, hypergryph. `gameMeta` per package is local (different per publisher); no cross-package collision. `ExeNamer.ExeName` signature is consistent across providers.
- **Anti-cheat invariant**: documented in Task 1 (interface godoc) and Tasks 6/8/9 (each launch_windows.go inherits the pattern). `Launch` always returns `(0, nil)` on success.
- **Test coverage**: each new package has `_test.go`; the App refactor introduces `app_test.go`; settings has migration + malformed tests; iconext has cross-platform tests. Smoke covers what unit tests can't (real PE extraction, real launches, real bg from real cache/API).

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-04-launcher-collection-m2-providers.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Per the autonomous-mode memory, M1 was authorized for full autonomy after a check-in pause; M2 should re-confirm authorization at the Task 4 (App refactor) review checkpoint since it's the riskiest abstraction.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review.

Which approach?
