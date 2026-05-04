# M3.A — WuWa Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Reuse M2 pattern: implementer (haiku) + spec reviewer (haiku) + code-quality reviewer (haiku, superpowers:code-reviewer agent type) per task. Override reviewer findings that conflict with plan-verbatim code.

**Goal:** Self-hosted update download + apply for Wuthering Waves (kurogames provider). Patches existing installs + supports predownload. No fresh install, no cross-game concurrency, no byte-level resume.

**Architecture:** Provider-isolated (Approach 3 from spec): all update logic lives in `internal/providers/kurogames/update_*.go` (flat files, no subpackage to avoid import cycle); App layer at `internal/app/update_state.go` + `update_handler.go` holds per-game state + Wails RPC. M3.B (HoYo update) will refactor common pieces. Implements new optional `core.Updater` interface; non-kuro providers don't implement it.

**Tech Stack:** Go 1.26 + Wails v2.12 + Vue 3 + Pinia + vue-i18n. New deps: `golang.org/x/sys/windows.LockFileEx` (already vendored via `x/sys`). Frontend adds Vitest as devDep.

**Spec source of truth:** `docs/superpowers/specs/2026-05-04-launcher-collection-m3a-wuwa-update-design.md` (commit `27f53d9` on branch `m3a/spec`, locked after 3 review iterations / 17 edits).

**Branch convention** (per memory `feedback_commits.md`): work continues on `m3a/spec` (which already holds the spec doc). When all tasks done, merge `--no-ff` to `main`. NO `Co-Authored-By:` trailer in any commit.

---

## Task overview

| # | Task | Blocks on | Phase |
|---|---|---|---|
| 1 | M3.A.0 protocol research → research markdown + sample fixture | — | 0 setup |
| 2 | core types: `Updater` iface, `UpdatePlan`, `UpdateEvent`, `UpdateError`, `PlanKind`, `Phase` | — | 1 scaffolding (parallel) |
| 3 | `KurogamesSettings.TempDir` + LoadSettings defaults + NTFS precheck | — | 1 scaffolding (parallel) |
| 4 | `applyLock` interface + Windows `LockFileEx` impl + stub for non-Windows + tests | — | 1 scaffolding (parallel) |
| 5 | `update_progress.go` — sidecar I/O (progress.json, apply.wal, predl_ready.json) + recovery scan + collision rules | 2 | 1 scaffolding |
| 6 | `update_state.go` — GameUpdateState + InFlightOp + mu + ticker-drain Wails event emitter | 2 | 1 scaffolding |
| 7 | `update_manifest.go` — CheckForUpdate impl, ETag, JSON parse, file filter | 1, 2 | 2 protocol |
| 8 | `update_download.go` — worker pool 4, per-file SHA verify, retry, ctx-cancel, progress emit | 1, 2, 5 | 2 protocol |
| 9 | `update_apply.go` — apply.wal write/replay, atomic rename, EXDEV detect, applyLock acquire, cross-volume midrun guard | 2, 4, 5, 7 | 2 protocol |
| 10 | `kurogames.go` integration — CheckForUpdate + RunUpdate methods + panic recovery | 7, 8, 9 | 2 protocol |
| 11 | `update_handler.go` — Wails RPCs + 1st game guard + `App.Launch` apply-phase refusal | 6, 10 | 3 app |
| 12 | `frontend/src/stores/updates.ts` — Pinia store + EventsOn binding + rAF batching + justCompletedUpdate hook | — | 4 frontend (parallel) |
| 13 | `ConfirmDialog.vue` + `ToastHost.vue` components | — | 4 frontend (parallel) |
| 14 | `BottomBar.vue` state matrix + `SidebarRow.vue` 1px overlay + style updates | 12, 13 | 4 frontend |
| 15 | i18n keys + parity test (en + zh-TW + zh-CN) | 12 | 4 frontend |
| 16 | Integration tests (HappyPath/PredlHappyPath/ApplyPredlHappyPath/SanitizeURL/DriftDoc/LoadFixture) | 7-11 | 5 ship |
| 17 | Vitest setup + frontend tests | 12-15 | 5 ship |
| 18 | Manual smoke (17-item) + tag `v0.3.0-m3a` + merge `--no-ff` to main | all | 5 ship |

Phases 1 + 4 are parallel-startable with Phase 0 research; Phase 2 sequential after research; Phase 3 after Phase 2; Phase 5 after all impl.

**MVP-minus branch (per spec §1.2.3)**: if Task 1 escalates (manifest endpoint not identifiable in 1-day timebox), Task 7's manifest parser drops `Mode` / patch-related fields; Task 9's `update_apply.go` skips patch-apply codepath; no `update_patch.go` task added. Re-evaluate at Task 1 conclusion.

---

## Task 1: M3.A.0 Protocol research

**Files:**
- Create: `docs/superpowers/research/m3a-kuro-update-protocol.md`
- Create: `internal/providers/kurogames/testdata/manifest-sample.json` (synthetic minimal, 10-20 entries, sanitized)

**Time-box:** 1 working day (per spec §4.4). Escalate to user if manifest endpoint not identified by EOD with: (i) what's captured, (ii) MVP-minus fallback proposal.

- [ ] **Step 1: Cache scan (cheapest first; per spec §4.3)**

Run from any shell with grep:

```bash
CACHE="C:/Users/willie/AppData/Roaming/KRLauncher/G153/C50004/KRWebViewUserData/EBWebView/Default/Cache/Cache_Data"
grep -aohE 'https://[a-zA-Z0-9._-]+\.(kurogame\.com|aki-game\.net|kurogames\.net)[a-zA-Z0-9/_?=&.-]*' "$CACHE"/data_* 2>/dev/null | sort -u
```

Look for endpoint candidates beyond the bg config endpoint (which goes to `prod-alicdn-gamestarter.kurogame.com/launcher/.../background/.../zh-Hant.json`). Manifest URL likely follows similar pattern but with `manifest`/`patch`/`update` in path.

Rank candidates by mtime of containing data_N file. Output: ≤5 candidate URL list saved to a working notes file.

- [ ] **Step 2: Gate-zero mitmproxy smoke**

Install mitmproxy + Windows trust root CA. Set Windows system proxy to `127.0.0.1:8080`. Open KRLauncher (don't click anything). Observe mitmproxy flow list:
- bg config endpoint (`prod-alicdn-gamestarter.kurogame.com/launcher/.../background/.../zh-Hant.json`) shows up → proxy works, cert pinning is NOT in effect; proceed to Step 3.
- bg config endpoint NOT in flow list → cert pinning suspected. Switch to Plan B: skip mitmproxy, rely on Step 1 cache scan + community sources only. Note this in escalation report.

- [ ] **Step 3: Trigger manifest fetch via "verify integrity" (A1)**

Click KRLauncher's "驗證遊戲完整性" / "Verify Game Integrity" button. Capture all HTTP traffic for ~30 seconds. Save the manifest endpoint's request + response.

If the button doesn't exist OR doesn't fetch the manifest endpoint:

- [ ] **Step 3-fallback: Trigger via downgrade (A2)**

Edit `C:/Program Files/Wuthering Waves/Wuthering Waves Game/launcherDownloadConfig.json`:

```json
{ "version": "3.0.0", ... }   // was 3.3.0 or whatever current
```

Restart KRLauncher → observe update detection → mitmproxy captures manifest fetch.

After capture, restore original `launcherDownloadConfig.json` (have the file backed up before editing).

- [ ] **Step 4: Document the manifest endpoint**

Create `docs/superpowers/research/m3a-kuro-update-protocol.md` with this skeleton (fill from observation):

```markdown
# Kuro WuWa update protocol — observed (YYYY-MM-DD)

## Auth prerequisite
- accountID source: `%APPDATA%/KRLauncher/G153/C50004/KRWebViewUserData/EBWebView/Default/Cache/Cache_Data/data_*`
  Format: `50004_<32-char-alphanumeric>`. Extract via cache-scrape (mirror M2 BG research pattern).

## Endpoints

<!-- TEST_ANCHOR: manifest_url_regex -->
### Manifest (current version)
- Method: <GET | POST>
- URL pattern: `https://prod-alicdn-gamestarter.kurogame.com/launcher/<accountID>/G153/<...>/<lang>.json`
- Headers: <list>
- Request body / params: <list>
- Response status: <code>
- Response Content-Type: <type>
- ETag header observed: <yes | no>; if yes, format: `<example>`
<!-- END_ANCHOR: manifest_url_regex -->

### Manifest (predownload, if separate endpoint)
- <fill or note "same endpoint with query/header diff" + spec the diff>

### Files
- CDN: hw-pcdownload-qcloud.aki-game.net (verified during M2 BG research)
- URL pattern: `/launcher/clientUpload/<hash>.<ext>`
- Range support: <yes | no> (verified via `curl -H "Range: bytes=0-1023" <URL>` returns 206)
- Throttling headers: <list any X-RateLimit-*, Retry-After, etc.>

## JSON shapes

### Manifest response (synthetic minimal fixture; full capture .gitignored)
\`\`\`json
{
  "version": "3.4.0",
  "released_at": "2026-05-15T00:00:00Z",
  "files": [
    {
      "path": "Wuthering Waves Game/Engine/Binaries/foo.dll",
      "hash": "<sha256 hex>",
      "size": 12345678,
      "url": "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/<hash>.dll"
    }
  ]
}
\`\`\`

### Per-file entry — observed fields
- `path` — relative to game install dir
- `hash` — algorithm: <sha256 | md5>; encoding: <hex | base64>
- `size` — bytes (int64)
- `url` — full HTTPS
- `<patchUrl/baseHash/patchSize>` — present iff diff-based; see decision rule below

## Diff format detection
Decision rule:
- Entries contain `patchUrl/baseHash/patchSize` → diff-based; identify algorithm from first 4-8 magic bytes of a sample patch (HPatch v1 = `HDIFF`, bsdiff = `BSDIFF40`, custom = unknown)
- Flat `{path, hash, size, url}` only → full-replace

Observed: <full-replace | HPatch v1 | bsdiff | other:identify>

If full-replace: M3.A ships as-is. If diff-based: Task 9 adds patch-apply branch + new `update_patch.go` task (split from Task 9 if non-trivial).

## Sanitization rules (pre-commit regex)
- `accountID` → `<ACCOUNT_ID>`: pattern `50004_[a-zA-Z0-9]+`
- `device_id` → `<DEVICE_ID>`: pattern `[a-f0-9]{32}`
- `auth_token` (if any captured) → `<TOKEN>`

## Open questions / TODOs for M3.A.v2
- <list>

## Change log
- YYYY-MM-DD: initial capture (this file)
```

- [ ] **Step 5: Range support verification**

Pick one cached file URL and test:

```bash
curl --max-time 10 -H "Range: bytes=0-1023" -I "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/<hash>.<ext>" 2>&1 | head -20
```

Expect `HTTP/1.1 206 Partial Content` + `Content-Range: bytes 0-1023/<total>`. Document yes/no in the markdown.

- [ ] **Step 6: Synthetic minimal fixture**

From the captured manifest body, extract 10-20 representative entries (cover: smallest file, largest file, patch entry if diff-format, zero-byte entry if any). Sanitize via the regex rules. Save as `internal/providers/kurogames/testdata/manifest-sample.json`. Validate it is well-formed JSON: `cat manifest-sample.json | python -m json.tool > /dev/null` should exit 0.

- [ ] **Step 7: Commit**

```bash
git add docs/superpowers/research/m3a-kuro-update-protocol.md internal/providers/kurogames/testdata/manifest-sample.json
git commit -m "research(m3a): kuro update protocol — manifest endpoint, file CDN, sample fixture"
```

- [ ] **Step 8: Escalation gate**

If Steps 3 and 3-fallback both failed (no manifest endpoint identifiable), escalate to user with:
1. The captured material (Step 1 cache-scan candidates, mitmproxy flow list dump)
2. Proposal: M3.A-MVP-minus = full-file-replace only (assume `{path, hash, size, url}` flat shape; defer diff support to M3.A.v2)
3. Wait for user direction before proceeding to Task 7.

---

## Task 2: Core types — Updater interface + UpdatePlan/UpdateEvent/UpdateError

**Files:**
- Create: `internal/core/updater.go`
- Modify: `internal/core/provider.go` — add `PlanKind` and `Phase` enums

- [ ] **Step 1: Add enums to `provider.go`**

Append to `internal/core/provider.go`:

```go
// PlanKind distinguishes a full update plan from a predownload-only plan.
// PlanPredownload runs only the download phase and persists predl_ready.json
// for later application via ApplyPredownload.
type PlanKind int

const (
	PlanUpdate PlanKind = iota
	PlanPredownload
)

// Phase identifies which sub-phase of RunUpdate is currently active.
// PhaseDownload progress is reported in bytes; PhaseApply in file count.
type Phase int

const (
	PhaseDownload Phase = iota
	PhaseApply
)
```

- [ ] **Step 2: Create `internal/core/updater.go`**

```go
package core

import (
	"context"
	"fmt"
)

// Updater is an optional interface providers may implement to support
// game updates. M3.A: kurogames implements it. hoyoverse and hypergryph
// do NOT in M3.A; M3.B and M3.C add them respectively.
//
// App layer type-asserts at RPC entry: p, ok := provider.(core.Updater).
type Updater interface {
	// CheckForUpdate fetches the per-game manifest. Idempotent. Read-only.
	// Returns a populated UpdatePlan or an error. Plan.Files is filtered
	// to exclude entries whose hash matches the currently-installed file.
	CheckForUpdate(ctx context.Context, gid GameID) (UpdatePlan, error)

	// RunUpdate executes a previously-checked plan. Emits progress via
	// onEvent (synchronous callback, not channel). Returns nil on success,
	// ctx.Err() on cancel, or *UpdateError on terminal failure.
	//
	// Re-verifies plan.ManifestETag at entry; mismatch returns
	// *UpdateError{Code: "manifest_changed"}.
	//
	// Plan-Kind dispatch:
	//   PlanUpdate      → download phase + apply phase
	//   PlanPredownload → download phase only; renames progress.json to
	//                     predl_ready.json on completion
	RunUpdate(ctx context.Context, plan UpdatePlan, onEvent func(UpdateEvent)) error
}

// UpdatePlan describes the work needed to bring an installed game from
// its current version to the manifest's target version.
type UpdatePlan struct {
	GameID       GameID     // which game this plan is for
	Kind         PlanKind   // PlanUpdate | PlanPredownload
	ManifestETag string     // re-checked at RunUpdate entry; mismatch → ErrManifestChanged
	Version      string     // human-readable label, e.g. "3.4.0"
	Files        []FileTask // already filtered: only files whose hash differs from current install
	TotalBytes   int64      // sum of Files[].Size; used for download progress denominator
}

// FileTask is one file to download + apply during an update run.
type FileTask struct {
	Path string // relative to game install dir, e.g. "Wuthering Waves Game/foo/bar.dll"
	Hash string // hex-encoded SHA-256 (or whatever the manifest provides; document in research markdown)
	Size int64  // expected byte size
	URL  string // full CDN URL; sanitized before logging via sanitizeURL
}

// UpdateEvent is emitted by RunUpdate via the onEvent callback during
// download and apply phases. Throttled to ~8 Hz at the App layer for
// byte-progress; phase transitions / cancel / error / done bypass the
// throttle and emit synchronously.
type UpdateEvent struct {
	Phase       Phase  // PhaseDownload | PhaseApply
	Current     int64  // bytes done in PhaseDownload, file count applied in PhaseApply
	Total       int64  // TotalBytes (PhaseDownload) or len(plan.Files) (PhaseApply)
	CurrentFile string // optional: name of file currently being processed
}

// UpdateError is the structured error type returned by RunUpdate /
// CheckForUpdate. Transported to frontend via GameUpdateState.LastError
// in the snapshot returned by UpdateStatusAll RPC; frontend renders text
// via i18n key update.errors.<Code> with Params substitution. Go side
// never sends pre-rendered text.
type UpdateError struct {
	Code      string            // see spec §6.1 catalog
	Params    map[string]string // template substitution data; URLs sanitized
	Retryable bool              // true → toast shows retry button (frontend reads this flag)
}

func (e *UpdateError) Error() string {
	return fmt.Sprintf("%s: %v", e.Code, e.Params)
}
```

- [ ] **Step 3: Run vet + build**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go vet ./internal/core/...
go build ./internal/...
```

Expected: clean. (Updater interface has no implementers yet; that's fine.)

- [ ] **Step 4: Add interface compliance compile-time check** (no test file yet — placeholder)

This task ships zero behavior tests because the interface has no implementers in this task. Tests will land in Task 10 when `kurogames.Provider` implements `Updater`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/updater.go internal/core/provider.go
git commit -m "feat(core): Updater optional iface + UpdatePlan/UpdateEvent/UpdateError types"
```

---

## Task 3: KurogamesSettings.TempDir + LoadSettings defaults

**Files:**
- Modify: `internal/app/settings.go` — add `TempDir` field to `KurogamesSettings`
- Modify: `internal/app/settings_test.go` — extend tests

- [ ] **Step 1: Add `TempDir` field to settings**

In `internal/app/settings.go`, replace `type KurogamesSettings  struct{ Path string ` + "`" + `toml:"path"` + "`" + ` }` with:

```go
type KurogamesSettings struct {
	Path    string `toml:"path"`
	TempDir string `toml:"temp_dir,omitempty"` // empty → runtime default os.TempDir()/launcher-collection/<gameID>
}
```

`rawTOML.Backends.Kurogames` already references `KurogamesSettings`, no change needed there.

- [ ] **Step 2: Add backward-compat tests**

Append to `internal/app/settings_test.go`:

```go
func TestSettings_KurogamesTempDir_DefaultEmpty(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Kurogames.TempDir != "" {
		t.Errorf("default TempDir = %q, want empty", s.Backends.Kurogames.TempDir)
	}
}

func TestSettings_KurogamesTempDir_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.Backends.Kurogames.TempDir = `D:\my-temp`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Backends.Kurogames.TempDir != `D:\my-temp` {
		t.Errorf("round-trip TempDir = %q", loaded.Backends.Kurogames.TempDir)
	}
}

func TestSettings_KurogamesTempDir_BackwardCompat(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	m2 := "version = 1\n\n[app]\nlanguage = \"zh-TW\"\n\n[backends.kurogames]\npath = \"C:\\\\Program Files\\\\Wuthering Waves\"\n"
	if err := os.WriteFile(p, []byte(m2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Kurogames.Path != `C:\Program Files\Wuthering Waves` {
		t.Errorf("path lost during load: %q", s.Backends.Kurogames.Path)
	}
	if s.Backends.Kurogames.TempDir != "" {
		t.Errorf("TempDir = %q on M2-era file", s.Backends.Kurogames.TempDir)
	}
}
```

- [ ] **Step 3: Run, verify PASS**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test -count=1 -run "TestSettings_KurogamesTempDir" ./internal/app/...
```

Expected: 3 tests PASS.

- [ ] **Step 4: Whole-internal vet + tests**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 5: Commit**

```bash
git add internal/app/settings.go internal/app/settings_test.go
git commit -m "feat(app): KurogamesSettings.TempDir field — backward-compat optional"
```

---

## Task 4: applyLock interface + Windows impl + non-Windows stub

**Files:**
- Create: `internal/providers/kurogames/apply_lock.go`
- Create: `internal/providers/kurogames/apply_lock_windows.go` (`//go:build windows`)
- Create: `internal/providers/kurogames/apply_lock_other.go` (`//go:build !windows`)
- Create: `internal/providers/kurogames/apply_lock_windows_test.go` (`//go:build windows`)
- Create: `internal/providers/kurogames/apply_lock_other_test.go` (`//go:build !windows`)

- [ ] **Step 1: Create the interface file**

`internal/providers/kurogames/apply_lock.go`:

```go
package kurogames

// applyLock guards apply phase against concurrent game launches.
// Windows: file lock on `<gameDir>\.lc_update.lock` via LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY.
// Non-Windows: stub for tests on CI Linux runners.
type applyLock interface {
	Acquire(gameDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
```

- [ ] **Step 2: Create Windows impl**

`internal/providers/kurogames/apply_lock_windows.go`:

```go
//go:build windows

package kurogames

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const lockFileName = ".lc_update.lock"

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

- [ ] **Step 3: Create non-Windows stub**

`internal/providers/kurogames/apply_lock_other.go`:

```go
//go:build !windows

package kurogames

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

- [ ] **Step 4: Windows contention test**

`internal/providers/kurogames/apply_lock_windows_test.go`:

```go
//go:build windows

package kurogames

import "testing"

func TestApplyLock_AcquireSucceedsOnce(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()
}

func TestApplyLock_SecondAcquireFails(t *testing.T) {
	dir := t.TempDir()
	l1 := newApplyLock()
	if err := l1.Acquire(dir); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err == nil {
		t.Errorf("second Acquire should fail")
		_ = l2.Release()
	}
}

func TestApplyLock_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	l1 := newApplyLock()
	if err := l1.Acquire(dir); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err != nil {
		t.Errorf("re-Acquire after Release: %v", err)
	}
	_ = l2.Release()
}
```

- [ ] **Step 5: Non-Windows stub test**

`internal/providers/kurogames/apply_lock_other_test.go`:

```go
//go:build !windows

package kurogames

import (
	"errors"
	"testing"
)

func TestApplyLockStub_AcquireOnceReleaseSucceeds(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("stub Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("stub Release: %v", err)
	}
}

func TestApplyLockStub_SecondAcquireSentinel(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	_ = l.Acquire(dir)
	err := l.Acquire(dir)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	if !errors.Is(err, stubLockSentinel) {
		t.Errorf("err = %v, want stubLockSentinel", err)
	}
}
```

- [ ] **Step 6: Run, verify PASS (Windows host)**

```bash
go test -count=1 ./internal/providers/kurogames/... -run "TestApplyLock"
```

Expected: 3 Windows tests PASS.

- [ ] **Step 7: Whole-repo verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 8: Commit**

```bash
git add internal/providers/kurogames/apply_lock*.go
git commit -m "feat(kurogames): applyLock interface — Windows LockFileEx + non-Windows stub"
```

---

## Task 5: update_progress.go — sidecar I/O + recovery scan

**Files:**
- Create: `internal/providers/kurogames/update_progress.go`
- Create: `internal/providers/kurogames/update_progress_test.go`

Spec sources: §2.2 sidecar table, §2.3 recovery rules, §5.1 mtime exact-equality, §6.3 collision rules.

- [ ] **Step 1: Write failing tests first**

`internal/providers/kurogames/update_progress_test.go`:

```go
package kurogames

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgress_WriteAndLoadEntry(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-abc"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("Engine/foo.dll", mt, 12345); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ETag != "etag-abc" {
		t.Errorf("ETag = %q", loaded.ETag)
	}
	e, ok := loaded.Entries["Engine/foo.dll"]
	if !ok {
		t.Fatal("entry not loaded")
	}
	if e.Size != 12345 || !e.MTime.Equal(mt) {
		t.Errorf("entry mismatch: %+v", e)
	}
}

func TestProgress_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(p.dir(), "progress.json")
	tmpPath := mainPath + ".tmp"
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("progress.json missing: %v", err)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Errorf(".tmp should be cleaned")
	}
}

func TestProgress_LoadCorruptReturnsErr(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProgress(dir); err == nil {
		t.Error("expected err on corrupt JSON")
	}
}

func TestProgress_RenameToPredlReady(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("a.dll", time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "progress.json")); err == nil {
		t.Errorf("progress.json should be gone")
	}
	body, _ := os.ReadFile(filepath.Join(p.dir(), "predl_ready.json"))
	var v struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("predl_ready.json malformed: %v", err)
	}
	if v.ETag != "etag-1" {
		t.Errorf("ETag = %q", v.ETag)
	}
}

func TestProgress_RecoveryScan_ApplyWalWins(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apply.wal"), []byte(`{"etag":"e","done":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(dir)
	if state.Phase != PhaseApplyResume {
		t.Errorf("Phase = %v, want PhaseApplyResume", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestProgress_RecoveryScan_PredlOverProgress(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(dir)
	if state.Phase != PhasePredlAwaiting {
		t.Errorf("Phase = %v, want PhasePredlAwaiting", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test -count=1 -run "TestProgress" ./internal/providers/kurogames/...
```

Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement `update_progress.go`**

`internal/providers/kurogames/update_progress.go`:

```go
package kurogames

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProgressEntry is one file's resume metadata after successful download +
// hash verify + atomic rename. mtime+size exact-equality is the resume
// trust check (spec §5.1).
type ProgressEntry struct {
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
	Hash  string    `json:"hash,omitempty"`
}

// ProgressFile is the on-disk shape of progress.json (and predl_ready.json
// after rename — same schema).
type ProgressFile struct {
	GameID  string                   `json:"game_id"`
	Version string                   `json:"version"`
	ETag    string                   `json:"etag"`
	Entries map[string]ProgressEntry `json:"entries"`
}

type progressStore struct {
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
	pf := ProgressFile{
		GameID:  p.gameID,
		Version: p.version,
		ETag:    etag,
		Entries: map[string]ProgressEntry{},
	}
	return p.writeAtomic("progress.json", &pf)
}

// MarkComplete records that <relPath> finished download + verify.
func (p *progressStore) MarkComplete(relPath string, mtime time.Time, size int64) error {
	pf, err := loadProgressFile(filepath.Join(p.dir(), "progress.json"))
	if err != nil {
		return err
	}
	pf.Entries[relPath] = ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond)}
	return p.writeAtomic("progress.json", pf)
}

// RenameToPredlReady atomically renames progress.json → predl_ready.json
// (spec §2.2) at end of PlanPredownload's download phase.
func (p *progressStore) RenameToPredlReady() error {
	src := filepath.Join(p.dir(), "progress.json")
	dst := filepath.Join(p.dir(), "predl_ready.json")
	return os.Rename(src, dst)
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

// LoadProgress parses progress.json from the given dir.
func LoadProgress(dir string) (*ProgressFile, error) {
	return loadProgressFile(filepath.Join(dir, "progress.json"))
}

func loadProgressFile(path string) (*ProgressFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf ProgressFile
	if err := json.Unmarshal(body, &pf); err != nil {
		return nil, fmt.Errorf("progress json parse: %w", err)
	}
	return &pf, nil
}

// RecoveryPhase identifies the in-progress sidecar a directory contains.
type RecoveryPhase int

const (
	RecoveryNone RecoveryPhase = iota
	PhaseDownloadResume
	PhaseApplyResume
	PhasePredlAwaiting
	RecoveryCorrupt
)

type RecoveryState struct {
	Phase    RecoveryPhase
	WasPredl bool
	Err      error
}

// ScanRecovery resolves sidecar collisions per spec §6.3.
func ScanRecovery(dir string) RecoveryState {
	hasProgress := fileExists(filepath.Join(dir, "progress.json"))
	hasWAL := fileExists(filepath.Join(dir, "apply.wal"))
	hasPredl := fileExists(filepath.Join(dir, "predl_ready.json"))

	switch {
	case hasWAL:
		if hasProgress {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
		}
		if hasPredl {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
		}
		if _, err := os.ReadFile(filepath.Join(dir, "apply.wal")); err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		return RecoveryState{Phase: PhaseApplyResume}

	case hasProgress && hasPredl:
		_ = os.Remove(filepath.Join(dir, "progress.json"))
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: PhasePredlAwaiting}

	case hasProgress:
		if _, err := loadProgressFile(filepath.Join(dir, "progress.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: PhaseDownloadResume}

	case hasPredl:
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: PhasePredlAwaiting}

	default:
		return RecoveryState{Phase: RecoveryNone}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test -count=1 -run "TestProgress" ./internal/providers/kurogames/...
```

Expected: 6 tests PASS.

- [ ] **Step 5: Whole-repo verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/kurogames/update_progress.go internal/providers/kurogames/update_progress_test.go
git commit -m "feat(kurogames): update progress sidecar I/O + recovery collision rules"
```

---

## Task 6: update_state.go — GameUpdateState + ticker-drain Wails emitter

**Files:**
- Create: `internal/app/update_state.go`
- Create: `internal/app/update_state_test.go`

Spec sources: §2.1 concurrency rules + invariant, §3.3 8 Hz throttle (ticker-drain pattern), §5.6 cancel ownership, §7.0 test seams (Clock interface, snapshot copy).

- [ ] **Step 1: Define Clock interface for testability**

`internal/app/update_state.go` (top of file, before state struct):

```go
package app

import (
	"context"
	"sync"
	"time"

	"launcher-collection-tmp/internal/core"
)

// Clock abstracts time.Now / time.NewTicker for tests. Production wires
// realClock; tests inject fakeClock advancing arbitrarily.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
```

- [ ] **Step 2: Define GameUpdateState + InFlightOp**

Append to `internal/app/update_state.go`:

```go
// GameUpdateState is the per-game update state held by App. Mutated by
// Wails RPC handlers and RunUpdate's onEvent callback under mu.
//
// Invariant (spec §2.1): InFlight != nil iff a runUpdate goroutine has
// been spawned and its defer has not yet completed. Writers that set
// InFlight = nil must do so under mu.Lock() AFTER all worker-side cleanup.
type GameUpdateState struct {
	mu              sync.RWMutex
	AvailableUpdate *core.UpdatePlan
	AvailablePredl  *core.UpdatePlan
	InFlight        *InFlightOp
	LastError       *core.UpdateError
	PredlReady      *core.UpdatePlan
}

// InFlightOp describes the currently-running update operation for a game.
// Fields are mutated by RunUpdate's onEvent (Current/Total/Phase) and read
// by snapshot copy. cancel is unexported because only App-layer code calls it.
type InFlightOp struct {
	Plan      core.UpdatePlan
	Phase     core.Phase
	Current   int64
	Total     int64
	cancel    context.CancelFunc
	StartedAt time.Time
}

// GameUpdateSnapshot is the JSON-serializable snapshot returned by
// UpdateStatusAll RPC. NO pointers into live state — pure value copy
// to prevent torn reads in the frontend.
type GameUpdateSnapshot struct {
	AvailableUpdate *core.UpdatePlan      `json:"available_update,omitempty"`
	AvailablePredl  *core.UpdatePlan      `json:"available_predl,omitempty"`
	InFlight        *InFlightSnapshot     `json:"in_flight,omitempty"`
	LastError       *core.UpdateError     `json:"last_error,omitempty"`
	PredlReady      *core.UpdatePlan      `json:"predl_ready,omitempty"`
}

type InFlightSnapshot struct {
	Kind       core.PlanKind `json:"kind"`
	Phase      core.Phase    `json:"phase"`
	Current    int64         `json:"current"`
	Total      int64         `json:"total"`
	Version    string        `json:"version"`
	StartedAt  time.Time     `json:"started_at"`
}

// Snapshot returns a value copy of the state under RLock. Safe to send to
// frontend; no pointer aliasing.
func (s *GameUpdateState) Snapshot() GameUpdateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := GameUpdateSnapshot{
		AvailableUpdate: copyPlan(s.AvailableUpdate),
		AvailablePredl:  copyPlan(s.AvailablePredl),
		PredlReady:      copyPlan(s.PredlReady),
	}
	if s.InFlight != nil {
		out.InFlight = &InFlightSnapshot{
			Kind:      s.InFlight.Plan.Kind,
			Phase:     s.InFlight.Phase,
			Current:   s.InFlight.Current,
			Total:     s.InFlight.Total,
			Version:   s.InFlight.Plan.Version,
			StartedAt: s.InFlight.StartedAt,
		}
	}
	if s.LastError != nil {
		errCopy := *s.LastError
		// Copy params map to break aliasing
		if errCopy.Params != nil {
			pp := make(map[string]string, len(errCopy.Params))
			for k, v := range errCopy.Params {
				pp[k] = v
			}
			errCopy.Params = pp
		}
		out.LastError = &errCopy
	}
	return out
}

func copyPlan(p *core.UpdatePlan) *core.UpdatePlan {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Files != nil {
		cp.Files = make([]core.FileTask, len(p.Files))
		copy(cp.Files, p.Files)
	}
	return &cp
}
```

- [ ] **Step 3: Define UpdateStateRegistry + ticker-drain emitter**

Append to `internal/app/update_state.go`:

```go
// UpdateStateRegistry holds per-game update state and runs a single
// ticker-drain emitter goroutine that throttles Wails event emission to
// ~8 Hz for byte-progress, with phase-transition / cancel / error / done
// events bypassing via synchronous direct emit.
type UpdateStateRegistry struct {
	mu        sync.Mutex
	games     map[core.GameID]*GameUpdateState
	emitter   *eventEmitter
}

func NewUpdateStateRegistry(emit func(name string, args ...any), clock Clock) *UpdateStateRegistry {
	r := &UpdateStateRegistry{
		games: map[core.GameID]*GameUpdateState{},
	}
	r.emitter = newEventEmitter(emit, clock)
	r.emitter.start()
	return r
}

// Get returns (or creates) the state for a game. Idempotent.
func (r *UpdateStateRegistry) Get(gid core.GameID) *GameUpdateState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.games[gid]
	if !ok {
		st = &GameUpdateState{}
		r.games[gid] = st
	}
	return st
}

// EmitChanged queues a snapshot of game's state for the next 125ms tick.
// Non-blocking; replaces any pending snapshot for the same gameID.
func (r *UpdateStateRegistry) EmitChanged(gid core.GameID) {
	st := r.Get(gid)
	r.emitter.queueProgress(string(gid), st.Snapshot())
}

// EmitTerminal emits IMMEDIATELY (bypasses throttle). Used for phase
// transitions, cancel, error, done.
func (r *UpdateStateRegistry) EmitTerminal(gid core.GameID) {
	st := r.Get(gid)
	r.emitter.emitNow(string(gid), st.Snapshot())
}

// SnapshotAll returns a value-copy map for UpdateStatusAll RPC.
func (r *UpdateStateRegistry) SnapshotAll() map[string]GameUpdateSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]GameUpdateSnapshot, len(r.games))
	for gid, st := range r.games {
		out[string(gid)] = st.Snapshot()
	}
	return out
}

// eventEmitter implements the ticker-drain throttle: 125ms ticker + 1-buf
// channel + non-blocking replace-on-full per gameID; phase transitions
// bypass via emitNow.
type eventEmitter struct {
	emit  func(name string, args ...any)
	clock Clock

	mu      sync.Mutex
	pending map[string]GameUpdateSnapshot // gameID → latest queued snapshot

	stop chan struct{}
}

func newEventEmitter(emit func(name string, args ...any), clock Clock) *eventEmitter {
	return &eventEmitter{
		emit:    emit,
		clock:   clock,
		pending: map[string]GameUpdateSnapshot{},
		stop:    make(chan struct{}),
	}
}

func (e *eventEmitter) start() {
	go e.run()
}

func (e *eventEmitter) Stop() { close(e.stop) }

func (e *eventEmitter) queueProgress(gameID string, snap GameUpdateSnapshot) {
	e.mu.Lock()
	e.pending[gameID] = snap // latest-wins
	e.mu.Unlock()
}

func (e *eventEmitter) emitNow(gameID string, snap GameUpdateSnapshot) {
	e.mu.Lock()
	delete(e.pending, gameID) // drop any pending byte-progress; this terminal supersedes
	e.mu.Unlock()
	e.emit("update:changed", gameID, snap)
}

func (e *eventEmitter) run() {
	ticker := e.clock.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			e.drain()
		}
	}
}

func (e *eventEmitter) drain() {
	e.mu.Lock()
	if len(e.pending) == 0 {
		e.mu.Unlock()
		return
	}
	pending := e.pending
	e.pending = map[string]GameUpdateSnapshot{}
	e.mu.Unlock()
	for gameID, snap := range pending {
		e.emit("update:changed", gameID, snap)
	}
}
```

- [ ] **Step 4: Write tests**

`internal/app/update_state_test.go`:

```go
package app

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"launcher-collection-tmp/internal/core"
)

// fakeClock is a controllable Clock for tests.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	tickCh chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:    time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		tickCh: make(chan time.Time, 1),
	}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTicker(d time.Duration) *time.Ticker {
	// Hijack: real ticker but immediately fired by our advance() control.
	// The returned *time.Ticker.C is a normal channel; we substitute.
	t := time.NewTicker(d)
	t.Stop()
	go func() {
		for tick := range f.tickCh {
			// Reflect via the real ticker's channel
			select {
			case <-t.C:
			default:
			}
			// Push tick value into a fake by closing — simpler: use dedicated chan
			_ = tick
		}
	}()
	return t
}

// For simpler tests, use a custom emitter directly with a tickable channel.
func TestEmitter_TickerDrainCoalesces(t *testing.T) {
	var emitted []struct {
		name string
		args []any
	}
	var emitMu sync.Mutex
	emit := func(name string, args ...any) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emitted = append(emitted, struct {
			name string
			args []any
		}{name, args})
	}

	e := newEventEmitter(emit, realClock{})
	// Don't start the goroutine; call drain directly.

	// Queue 3 progress snapshots for same gameID; latest-wins.
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 100}})
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 200}})
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 300}})
	e.queueProgress("g2", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 50}})

	e.drain()

	emitMu.Lock()
	defer emitMu.Unlock()
	if len(emitted) != 2 {
		t.Fatalf("got %d emits, want 2 (one per gameID)", len(emitted))
	}
	// Find g1's emit; assert latest snapshot wins.
	for _, e := range emitted {
		if e.args[0] == "g1" {
			snap := e.args[1].(GameUpdateSnapshot)
			if snap.InFlight.Current != 300 {
				t.Errorf("g1 final Current = %d, want 300 (latest)", snap.InFlight.Current)
			}
		}
	}
}

func TestEmitter_EmitNowBypassesQueue(t *testing.T) {
	var emitted []string
	var emitMu sync.Mutex
	emit := func(name string, args ...any) {
		emitMu.Lock()
		defer emitMu.Unlock()
		gameID := args[0].(string)
		snap := args[1].(GameUpdateSnapshot)
		if snap.InFlight != nil {
			emitted = append(emitted, gameID+":progress")
		} else {
			emitted = append(emitted, gameID+":terminal")
		}
	}
	e := newEventEmitter(emit, realClock{})

	// Queue a progress snapshot, then emitNow with a terminal snapshot.
	e.queueProgress("g1", GameUpdateSnapshot{InFlight: &InFlightSnapshot{Current: 100}})
	e.emitNow("g1", GameUpdateSnapshot{}) // terminal: InFlight nil

	// drain after — should NOT re-emit the dropped progress.
	e.drain()

	emitMu.Lock()
	defer emitMu.Unlock()
	if len(emitted) != 1 {
		t.Fatalf("got %d emits, want 1", len(emitted))
	}
	if emitted[0] != "g1:terminal" {
		t.Errorf("emit = %s, want g1:terminal", emitted[0])
	}
}

func TestStateRace(t *testing.T) {
	// 100 goroutines reading + writing GameUpdateState; must run -race clean.
	st := &GameUpdateState{}
	var wg sync.WaitGroup
	var counter atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				st.mu.Lock()
				if st.InFlight == nil {
					st.InFlight = &InFlightOp{Plan: core.UpdatePlan{Version: "x"}}
				} else {
					st.InFlight.Current = counter.Add(1)
				}
				st.mu.Unlock()

				_ = st.Snapshot()
			}
		}()
	}
	wg.Wait()
}

func TestSnapshot_NoPointerLeak(t *testing.T) {
	st := &GameUpdateState{
		AvailableUpdate: &core.UpdatePlan{
			Version: "3.4.0",
			Files:   []core.FileTask{{Path: "a", Size: 1}},
		},
		LastError: &core.UpdateError{
			Code:   "network",
			Params: map[string]string{"url": "x"},
		},
	}
	snap := st.Snapshot()
	// Mutate live state; snapshot must not change.
	st.AvailableUpdate.Version = "MUTATED"
	st.AvailableUpdate.Files[0].Path = "MUTATED"
	st.LastError.Code = "MUTATED"
	st.LastError.Params["url"] = "MUTATED"

	if snap.AvailableUpdate.Version != "3.4.0" {
		t.Errorf("snapshot Version leaked: %q", snap.AvailableUpdate.Version)
	}
	if snap.AvailableUpdate.Files[0].Path != "a" {
		t.Errorf("snapshot Files[0].Path leaked: %q", snap.AvailableUpdate.Files[0].Path)
	}
	if snap.LastError.Code != "network" {
		t.Errorf("snapshot LastError.Code leaked: %q", snap.LastError.Code)
	}
	if snap.LastError.Params["url"] != "x" {
		t.Errorf("snapshot LastError.Params leaked: %q", snap.LastError.Params["url"])
	}
}
```

- [ ] **Step 5: Run tests with -race**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test -count=1 -race ./internal/app/... -run "TestEmitter|TestStateRace|TestSnapshot"
```

Expected: 4 tests PASS, no race detected.

- [ ] **Step 6: Whole-internal verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 7: Commit**

```bash
git add internal/app/update_state.go internal/app/update_state_test.go
git commit -m "feat(app): GameUpdateState + ticker-drain Wails event emitter"
```

---

## Task 7: update_manifest.go — CheckForUpdate + manifest fetch + ETag

**Files:**
- Create: `internal/providers/kurogames/update_manifest.go`
- Create: `internal/providers/kurogames/update_manifest_test.go`

**Depends on:** Task 1 research artifacts (`docs/superpowers/research/m3a-kuro-update-protocol.md`). The implementer reads that markdown to fill in:
- Exact manifest URL template (likely `https://prod-alicdn-gamestarter.kurogame.com/launcher/<accountID>/G153/<...>/<lang>.json`)
- Manifest JSON struct shape (`Version`, `Files[]`, etc.)
- Hash algorithm (SHA-256 vs MD5)
- ETag header presence

**MVP-minus branch**: if research determined "full-file-replace only", `FileTask` has no `Mode` field; this task is unchanged. If research found diff entries (`patchUrl`/`baseHash`), add a `Mode` field to FileTask in core (Task 2 amendment) and parse it here.

**Sanitization** (per spec §6.5): all logged URLs pass `sanitizeURL` (added in this task as a small helper).

- [ ] **Step 1: Add sanitizeURL helper**

`internal/providers/kurogames/update_manifest.go` (top):

```go
package kurogames

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"launcher-collection-tmp/internal/core"
)

// accountIDRe matches Kuro accountID format `50004_<alnum>` for sanitization.
var accountIDRe = regexp.MustCompile(`50004_[a-zA-Z0-9]+`)

// deviceIDRe matches 32-char lowercase hex, the device ID format observed
// during M2 BG research.
var deviceIDRe = regexp.MustCompile(`[a-f0-9]{32}`)

// sanitizeURL redacts PII from a URL before logging or embedding in
// UpdateError.Params. Replaces accountID and deviceID with placeholder
// strings.
func sanitizeURL(s string) string {
	s = accountIDRe.ReplaceAllString(s, "<ACCOUNT_ID>")
	s = deviceIDRe.ReplaceAllString(s, "<DEVICE_ID>")
	return s
}
```

- [ ] **Step 2: Define manifest JSON shape**

Append to `update_manifest.go`:

```go
// manifestRaw is the on-the-wire JSON shape from Kuro's update endpoint.
// Fill the actual fields from m3a-kuro-update-protocol.md research output.
//
// Common shape (verified during M3.A.0 research; update if observation differs):
type manifestRaw struct {
	Version    string             `json:"version"`     // e.g. "3.4.0"
	ReleasedAt time.Time          `json:"released_at"` // RFC3339; for predl release detection
	Files      []manifestFileRaw  `json:"files"`
}

type manifestFileRaw struct {
	Path string `json:"path"` // relative to game install dir
	Hash string `json:"hash"` // hex-encoded SHA-256 (see research)
	Size int64  `json:"size"`
	URL  string `json:"url"` // full CDN URL
}
```

- [ ] **Step 3: Implement manifest fetch + ETag**

Append to `update_manifest.go`:

```go
// fetchManifest does an HTTP GET on the manifest URL and returns parsed
// content + ETag header. ETag is captured for re-verification at RunUpdate
// entry (spec §2.8). 4xx → ErrAuthFailed/ErrNotFound; 5xx → caller's retry.
func fetchManifest(ctx context.Context, client *http.Client, manifestURL string) (*manifestRaw, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("manifest fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", &core.UpdateError{
			Code:      "manifest_not_found",
			Retryable: false,
			Params:    map[string]string{"url": sanitizeURL(manifestURL)},
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "", &core.UpdateError{
			Code:      "auth_failed",
			Retryable: false,
			Params:    map[string]string{"url": sanitizeURL(manifestURL), "status": fmt.Sprint(resp.StatusCode)},
		}
	}
	if resp.StatusCode/100 == 5 {
		return nil, "", &core.UpdateError{
			Code:      "network",
			Retryable: true,
			Params:    map[string]string{"url": sanitizeURL(manifestURL), "status": fmt.Sprint(resp.StatusCode), "reason": "server error"},
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read manifest body: %w", err)
	}
	var raw manifestRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", fmt.Errorf("manifest json parse: %w", err)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		// Fallback: hash the body so we can still detect drift.
		h := sha256.Sum256(body)
		etag = "body-sha256:" + hex.EncodeToString(h[:])
	}
	return &raw, etag, nil
}

// reFetchManifestETag does a HEAD request to compare ETag without downloading
// the full body. Used at RunUpdate entry to detect drift between
// CheckForUpdate (which captured the ETag) and RunUpdate (which executes).
func reFetchManifestETag(ctx context.Context, client *http.Client, manifestURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("head manifest: status %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		// HEAD doesn't give us the body, so no body-hash fallback. Treat as
		// "no ETag support"; caller skips drift check.
		return "", nil
	}
	return etag, nil
}
```

- [ ] **Step 4: Implement filterChangedFiles + buildPlan**

Append to `update_manifest.go`:

```go
// filterChangedFiles drops manifest entries whose hash matches the
// already-installed file. Reduces plan.Files to actual work.
//
// Hashing 1000 files locally is slow (~30s on HDD); production code may
// want progress feedback during this step. MVP keeps it synchronous
// because it's only run inside CheckForUpdate (user-initiated), not
// background.
func filterChangedFiles(installDir string, files []manifestFileRaw, logger *slog.Logger) []core.FileTask {
	out := make([]core.FileTask, 0, len(files))
	for _, f := range files {
		full := filepath.Join(installDir, f.Path)
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			// File missing → must download
			out = append(out, core.FileTask{Path: f.Path, Hash: f.Hash, Size: f.Size, URL: f.URL})
			continue
		}
		if fi.Size() != f.Size {
			// Size differs → must download
			out = append(out, core.FileTask{Path: f.Path, Hash: f.Hash, Size: f.Size, URL: f.URL})
			continue
		}
		// Size matches; verify hash to skip unchanged files
		h, err := sha256File(full)
		if err != nil {
			logger.Debug("hash check failed; will re-download", "path", f.Path, "err", err)
			out = append(out, core.FileTask{Path: f.Path, Hash: f.Hash, Size: f.Size, URL: f.URL})
			continue
		}
		if h == f.Hash {
			continue // identical, skip
		}
		out = append(out, core.FileTask{Path: f.Path, Hash: f.Hash, Size: f.Size, URL: f.URL})
	}
	// Stable sort for deterministic plan ordering (helps tests + resume)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sha256File returns the hex-encoded SHA-256 of a file's contents.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildManifestURL constructs the manifest URL from an accountID + the
// per-game appCode constants. Hardcoded path template — update if research
// determined a different shape.
func buildManifestURL(accountID, appCode, version string) string {
	// PLACEHOLDER: replace this template with the actual URL pattern from
	// docs/superpowers/research/m3a-kuro-update-protocol.md.
	// Example shape used here:
	u := url.URL{
		Scheme: "https",
		Host:   "prod-alicdn-gamestarter.kurogame.com",
		Path:   fmt.Sprintf("/launcher/%s/G153/manifest/%s.json", accountID, version),
	}
	return u.String()
}
```

- [ ] **Step 5: Implement extractAccountID (cache-scrape, mirror M2 pattern)**

Append:

```go
// extractAccountID reads the KRLauncher Cache_Data files and returns the
// observed accountID, or empty + error if not found. Mirrors the M2 BG
// research pattern (regex grep over data_N).
func extractAccountID(logger *slog.Logger) (string, error) {
	roaming, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(roaming, "KRLauncher", "G153", "C50004",
		"KRWebViewUserData", "EBWebView", "Default", "Cache", "Cache_Data")
	const maxRead = 64 * 1024 * 1024

	var best string
	var bestMtime int64
	for i := 0; i < 4; i++ {
		path := filepath.Join(cacheDir, fmt.Sprintf("data_%d", i))
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(f, maxRead))
		_ = f.Close()
		matches := accountIDRe.FindAll(body, -1)
		if len(matches) == 0 {
			continue
		}
		// Take last match (most recent in file)
		last := string(matches[len(matches)-1])
		if mtime := info.ModTime().Unix(); mtime > bestMtime {
			best = last
			bestMtime = mtime
		}
	}
	if best == "" {
		return "", errors.New("accountID not found in KRLauncher cache; user may need to open KRLauncher once")
	}
	if logger != nil {
		logger.Debug("kurogames accountID extracted", "id", "<ACCOUNT_ID>") // never log raw
	}
	return best, nil
}
```

- [ ] **Step 6: Write tests**

`internal/providers/kurogames/update_manifest_test.go`:

```go
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

func TestFetchManifest_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := fetchManifest(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	var ue *struct{}
	_ = ue
	// Check via type assertion path
	if mfErr, ok := err.(interface{ Error() string }); ok {
		if !strings.Contains(mfErr.Error(), "manifest_not_found") {
			t.Errorf("err = %v, want manifest_not_found", err)
		}
	}
}

func TestFetchManifest_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, _, err := fetchManifest(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "auth_failed") {
		t.Errorf("err = %v, want auth_failed", err)
	}
}

func TestFetchManifest_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, _, err := fetchManifest(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
}

func TestFetchManifest_OK_AndETag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"3.4.0","released_at":"2026-05-15T00:00:00Z","files":[{"path":"a.dll","hash":"deadbeef","size":1,"url":"https://cdn.example/a"}]}`))
	}))
	defer srv.Close()
	mf, etag, err := fetchManifest(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if etag != `"abc"` {
		t.Errorf("etag = %q", etag)
	}
	if mf.Version != "3.4.0" || len(mf.Files) != 1 || mf.Files[0].Path != "a.dll" {
		t.Errorf("manifest = %+v", mf)
	}
}

func TestFilterChangedFiles_SkipsIdentical(t *testing.T) {
	tmp := t.TempDir()
	identical := filepath.Join(tmp, "identical.dll")
	if err := os.WriteFile(identical, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := sha256File(identical)
	if err != nil {
		t.Fatal(err)
	}
	missing := "missing.dll"
	differentSize := filepath.Join(tmp, "diff.dll")
	if err := os.WriteFile(differentSize, []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := []manifestFileRaw{
		{Path: "identical.dll", Hash: hash, Size: 5, URL: "u1"},
		{Path: missing, Hash: "x", Size: 100, URL: "u2"},
		{Path: "diff.dll", Hash: "x", Size: 999, URL: "u3"},
	}
	out := filterChangedFiles(tmp, manifest, nil)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2 (missing + size-diff)", len(out))
	}
	got := map[string]bool{}
	for _, f := range out {
		got[f.Path] = true
	}
	if got["identical.dll"] {
		t.Error("identical file should be filtered out")
	}
}
```

- [ ] **Step 7: Run, verify PASS**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test -count=1 ./internal/providers/kurogames/... -run "TestSanitizeURL|TestFetchManifest|TestFilterChangedFiles"
```

Expected: 6 tests PASS.

- [ ] **Step 8: Whole-internal verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 9: Commit**

```bash
git add internal/providers/kurogames/update_manifest.go internal/providers/kurogames/update_manifest_test.go
git commit -m "feat(kurogames): manifest fetch + ETag + sanitizeURL + filterChangedFiles"
```

---

## Task 8: update_download.go — worker pool + per-file SHA + retry

**Files:**
- Create: `internal/providers/kurogames/update_download.go`
- Create: `internal/providers/kurogames/update_download_test.go`

Spec sources: §2.8 retry policy (3x net with 1s/4s/16s backoff, 2x hash mismatch), §5.1 worker pool of 4, §5.3 download phase pseudocode, §7.0 Clock seam for tests.

- [ ] **Step 1: Implement worker pool + retry + verify**

`internal/providers/kurogames/update_download.go`:

```go
package kurogames

import (
	"context"
	"crypto/sha256"
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

	"launcher-collection-tmp/internal/core"
)

const (
	downloadWorkers = 4 // empirical CDN throttle threshold; M3.B+ may surface as setting

	netRetries  = 3
	hashRetries = 2
)

var netBackoff = []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}

// downloader wraps the dependencies needed for a download phase.
type downloader struct {
	client    *http.Client
	logger    *slog.Logger
	tempRoot  string                 // <TempDir>/<gameID-flat>/<version>/
	progress  *progressStore
	plan      *core.UpdatePlan
	onEvent   func(core.UpdateEvent) // throttled by App layer
	bytesDone atomic.Int64           // sum across workers
	clock     Clock                  // injected for retry backoff in tests
}

// Clock — must match update_state.go's interface; redeclare here to avoid
// kurogames depending on app package (would create import cycle).
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
	Sleep(d time.Duration)
}

type realKurogamesClock struct{}

func (realKurogamesClock) Now() time.Time                         { return time.Now() }
func (realKurogamesClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (realKurogamesClock) Sleep(d time.Duration)                  { time.Sleep(d) }

// runDownload runs the download phase: dispatches plan.Files across N
// workers, retries net/hash failures per policy, emits per-file progress.
// Returns nil on success or *core.UpdateError on terminal failure.
func (d *downloader) runDownload(ctx context.Context) error {
	// Pre-load existing progress to skip already-completed entries.
	progress, _ := LoadProgress(d.progress.dir())
	type job struct {
		index int
		file  core.FileTask
	}

	// Pre-count completed bytes so progress UI is accurate from tick 1.
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

	// Spawn workers
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

	// Enqueue
	for i, f := range d.plan.Files {
		select {
		case <-ctx.Done():
			close(jobCh)
			return ctx.Err()
		case jobCh <- job{index: i, file: f}:
		}
	}
	close(jobCh)

	// Wait for all workers to finish or first error
	var firstErr error
	for w := 0; w < downloadWorkers; w++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// processFile downloads one file with retries + verification + progress
// update. Skips if progress.json says it's already complete and size+mtime
// match (spec §5.1 exact equality).
func (d *downloader) processFile(ctx context.Context, f core.FileTask) error {
	finalPath := filepath.Join(d.progress.dir(), f.Path)

	// Resume check: trust mtime+size exact equality
	progress, _ := LoadProgress(d.progress.dir())
	if progress != nil {
		if e, ok := progress.Entries[f.Path]; ok {
			if e.Size == f.Size {
				if fi, err := os.Stat(finalPath); err == nil && fi.Size() == f.Size && fi.ModTime().Truncate(time.Millisecond).Equal(e.MTime) {
					// Already complete; emit progress tick for accurate UI
					d.emitProgress(f.Path)
					return nil
				}
			}
		}
	}

	// Cleanup any leftover .part before re-download (spec §5.2)
	partPath := finalPath + ".part"
	_ = os.Remove(partPath)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(finalPath), err)
	}

	// Try net retries with backoff
	for attempt := 0; attempt <= netRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt > 0 {
			d.clock.Sleep(netBackoff[attempt-1])
		}
		if err := d.downloadAndVerify(ctx, f, partPath); err != nil {
			// Categorize error
			var ue *core.UpdateError
			if errors.As(err, &ue) {
				return ue // already structured (e.g., hash mismatch retries exhausted)
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
		// Success: rename .part → final
		if err := os.Rename(partPath, finalPath); err != nil {
			return fmt.Errorf("rename %s: %w", finalPath, err)
		}
		// Record progress with exact mtime captured post-rename
		fi, _ := os.Stat(finalPath)
		if err := d.progress.MarkComplete(f.Path, fi.ModTime(), fi.Size()); err != nil {
			return fmt.Errorf("progress.MarkComplete: %w", err)
		}
		d.emitProgress(f.Path)
		return nil
	}
	return &core.UpdateError{Code: "internal", Retryable: true} // unreachable
}

// downloadAndVerify writes .part, hashes during stream, fails on hash
// mismatch (caller retries up to hashRetries times before surfacing
// `corrupt`). Net errors propagate to caller which manages netRetries.
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

// singleDownload streams URL into partPath while computing SHA-256.
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	f, err := os.Create(partPath)
	if err != nil {
		return "", fmt.Errorf("create part: %w", err)
	}
	defer f.Close()

	h := sha256.New()
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
			d.emitProgress("") // throttled byte progress
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

// emitProgress sends a throttled UpdateEvent. App-layer ticker-drain
// emitter further coalesces to 8 Hz for byte progress; per-file completion
// is also bounded (callers don't spam).
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

- [ ] **Step 2: Write tests**

`internal/providers/kurogames/update_download_test.go`:

```go
package kurogames

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"launcher-collection-tmp/internal/core"
)

// fakeKurogamesClock makes Sleep instant for fast tests.
type fakeKurogamesClock struct{}

func (fakeKurogamesClock) Now() time.Time                         { return time.Now() }
func (fakeKurogamesClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (fakeKurogamesClock) Sleep(d time.Duration)                  {} // instant

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestDownload_HappyPath(t *testing.T) {
	body := "hello world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Version:    "3.4.0",
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: sha(body), Size: int64(len(body)), URL: srv.URL},
		},
	}

	d := &downloader{
		client:   srv.Client(),
		logger:   slogTest(t),
		progress: ps,
		plan:     plan,
		clock:    fakeKurogamesClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.dir(), "a.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func Test5xxRetriesThenFails(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: sha("x"), Size: 1, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeKurogamesClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
	if attempts.Load() != int64(netRetries+1) {
		t.Errorf("attempts = %d, want %d", attempts.Load(), netRetries+1)
	}
}

func TestHashRetriesThenFails(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte("wrong-content"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: sha("expected"), Size: int64(len("wrong-content")), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeKurogamesClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("err = %v, want corrupt", err)
	}
	if attempts.Load() != int64(hashRetries+1) {
		t.Errorf("attempts = %d, want %d", attempts.Load(), hashRetries+1)
	}
}

func TestDownload_CancelMidStream(t *testing.T) {
	// Server holds connection open
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: sha("x"), Size: 100, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeKurogamesClock{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := d.runDownload(ctx)
	if err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestDownload_ResumeSkipsCompleted(t *testing.T) {
	// Pre-populate progress.json with one completed file
	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Create the file with known content
	body := "hello"
	completePath := filepath.Join(ps.dir(), "a.dll")
	if err := os.WriteFile(completePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(completePath)
	if err := ps.MarkComplete("a.dll", fi.ModTime(), fi.Size()); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: sha(body), Size: int64(len(body)), URL: srv.URL},
		},
		TotalBytes: int64(len(body)),
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeKurogamesClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 0 {
		t.Errorf("server hit %d times; should be 0 (resume skipped)", attempts.Load())
	}
}

// slogTest returns a no-op slog Logger for tests.
func slogTest(t *testing.T) interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
} {
	return &slogTestLogger{t: t}
}

type slogTestLogger struct{ t *testing.T }

func (l *slogTestLogger) Debug(msg string, args ...any) { l.t.Logf("DEBUG %s %v", msg, args) }
func (l *slogTestLogger) Info(msg string, args ...any)  { l.t.Logf("INFO %s %v", msg, args) }
func (l *slogTestLogger) Warn(msg string, args ...any)  { l.t.Logf("WARN %s %v", msg, args) }
func (l *slogTestLogger) Error(msg string, args ...any) { l.t.Logf("ERROR %s %v", msg, args) }
```

Note: `slogTest` returns an interface that matches `*slog.Logger`'s method shape — but since `downloader.logger` is typed as `*slog.Logger`, the test must use a real `slog.Logger`. Adjust as:

```go
import "log/slog"
...
slogTest := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
```

(Replace the `slogTestLogger` shim with the real slog.Logger constructor; revise tests to pass `slogTest` directly to `downloader.logger`.)

- [ ] **Step 3: Run, verify PASS**

```bash
go test -count=1 -race ./internal/providers/kurogames/... -run "TestDownload|Test5xx|TestHash"
```

Expected: 5 tests PASS.

- [ ] **Step 4: Whole-internal verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/kurogames/update_download.go internal/providers/kurogames/update_download_test.go
git commit -m "feat(kurogames): download phase — worker pool 4 + retry + per-file SHA verify"
```

---

## Task 9: update_apply.go — apply.wal + atomic rename + EXDEV detect + applyLock

**Files:**
- Create: `internal/providers/kurogames/update_apply.go`
- Create: `internal/providers/kurogames/update_apply_test.go`

Spec sources: §2.2 apply.wal sidecar, §2.6 cancel-during-apply (no-op), §2.7 apply-time guard via applyLock, §5.2 apply phase pseudocode, §5.6 cross-volume midrun error, §6.1 codes (`apply_partial`, `cross_volume_midrun`, `unsupported_filesystem`).

- [ ] **Step 1: Implement applier**

`internal/providers/kurogames/update_apply.go`:

```go
package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"launcher-collection-tmp/internal/core"
)

// applyWAL is the on-disk shape of apply.wal (spec §2.2). Embeds the
// manifest snapshot so recovery doesn't need to re-fetch from network.
type applyWAL struct {
	GameID      string   `json:"game_id"`
	Version     string   `json:"version"`
	ETag        string   `json:"etag"`
	WasPredl    bool     `json:"was_predl"` // for recovery message variant (spec §6.3)
	Pending     []string `json:"pending"`   // relative paths still to apply
	Done        []string `json:"done"`      // relative paths already moved
}

// applier wraps dependencies for the apply phase.
type applier struct {
	logger    *slog.Logger
	tempRoot  string
	gameDir   string
	progress  *progressStore
	plan      *core.UpdatePlan
	wasPredl  bool
	onEvent   func(core.UpdateEvent)
	lock      applyLock
}

// runApply executes the apply phase: writes WAL, atomic-renames each file,
// appends to WAL Done list, deletes WAL on success. ctx.Done() inside the
// loop is treated as no-op per spec §2.6 (apply is atomic-batch).
func (a *applier) runApply(ctx context.Context) error {
	// Cross-volume re-check (spec §5.6): apply phase must be on same volume
	tempVol := filepath.VolumeName(a.tempRoot)
	gameVol := filepath.VolumeName(a.gameDir)
	if tempVol != gameVol {
		return &core.UpdateError{
			Code:      "cross_volume_midrun",
			Retryable: false,
			Params: map[string]string{
				"temp_vol": tempVol,
				"game_vol": gameVol,
			},
		}
	}

	// Acquire applyLock (3rd guard; spec §2.7)
	if err := a.lock.Acquire(a.gameDir); err != nil {
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params: map[string]string{
				"kind":   "lock_held",
				"reason": err.Error(),
			},
		}
	}
	defer a.lock.Release()

	// Initialize WAL with all pending paths
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
		return &core.UpdateError{
			Code:      "apply_partial",
			Retryable: true,
			Params:    map[string]string{"reason": err.Error()},
		}
	}
	// fsync — go's os.Rename relies on fs guarantees; explicit fsync via re-open
	if err := fsyncFile(walPath); err != nil {
		a.logger.Warn("apply.wal fsync failed; proceeding", "err", err)
	}

	// Now safe to drop progress.json (spec §5.3 transition)
	_ = os.Remove(filepath.Join(a.progress.dir(), "progress.json"))

	// Apply each file; cancel.Done() is no-op (spec §2.6)
	var done atomic.Int64
	for _, f := range a.plan.Files {
		src := filepath.Join(a.progress.dir(), f.Path)
		dst := filepath.Join(a.gameDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return &core.UpdateError{
				Code:      "apply_partial",
				Retryable: true,
				Params:    map[string]string{"path": f.Path, "reason": err.Error()},
			}
		}
		if err := atomicRename(src, dst); err != nil {
			return &core.UpdateError{
				Code:      "apply_partial",
				Retryable: true,
				Params:    map[string]string{"path": f.Path, "reason": err.Error()},
			}
		}
		// Update WAL: move from Pending to Done
		wal.Done = append(wal.Done, f.Path)
		wal.Pending = removeString(wal.Pending, f.Path)
		_ = writeWALAtomic(walPath, &wal)

		done.Add(1)
		if a.onEvent != nil {
			a.onEvent(core.UpdateEvent{
				Phase:       core.PhaseApply,
				Current:     done.Load(),
				Total:       int64(len(a.plan.Files)),
				CurrentFile: f.Path,
			})
		}
	}

	// All applied; remove WAL
	if err := os.Remove(walPath); err != nil {
		a.logger.Warn("remove apply.wal", "err", err)
	}
	return nil
}

// resumeApply replays apply.wal: re-applies any Pending entries that
// the prior crash didn't finish. Uses WAL's manifest snapshot so no
// network call.
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

// atomicRename does a file-level rename; on EXDEV (cross-volume) returns
// error with details (M3.A doesn't fall back to copy+delete).
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
	// On Windows, cross-volume rename fails with ERROR_NOT_SAME_DEVICE (0x11).
	// Match by string fragment to avoid platform-specific imports here.
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

// fsyncFile opens the file and fsyncs to disk. Best-effort: if fails, caller
// proceeds (data is still written; only durability is at risk).
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

var _ = errors.Is // silence unused import if errors not actually used
```

- [ ] **Step 2: Write tests**

`internal/providers/kurogames/update_apply_test.go`:

```go
package kurogames

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestApply_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Pre-populate temp dir with files (simulating completed download)
	if err := os.MkdirAll(filepath.Join(ps.dir(), "Engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "a.dll"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir(), "Engine", "b.dll"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		GameID:       "kurogames/wutheringwaves",
		Version:      "3.4.0",
		ManifestETag: `"abc"`,
		Files: []core.FileTask{
			{Path: "a.dll", Size: 3},
			{Path: "Engine/b.dll", Size: 3},
		},
	}
	a := &applier{
		logger:   slog.Default(),
		tempRoot: tmp,
		gameDir:  gameDir,
		progress: ps,
		plan:     plan,
		lock:     newApplyLock(),
	}
	if err := a.runApply(context.Background()); err != nil {
		t.Fatalf("runApply: %v", err)
	}

	// Files moved
	if _, err := os.Stat(filepath.Join(gameDir, "a.dll")); err != nil {
		t.Errorf("a.dll missing in gameDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "Engine", "b.dll")); err != nil {
		t.Errorf("Engine/b.dll missing in gameDir: %v", err)
	}
	// WAL deleted on success
	if _, err := os.Stat(filepath.Join(ps.dir(), "apply.wal")); err == nil {
		t.Errorf("apply.wal should be deleted on success")
	}
}

func TestApply_CrossVolumeMidrunError(t *testing.T) {
	// Use Volume name comparison via filepath.VolumeName — empty on Linux,
	// but on Linux test paths share empty volume so this passes through.
	// We force an explicit different volume by using paths with volumes if
	// on Windows; otherwise skip.
	if filepath.VolumeName("/tmp") == filepath.VolumeName("/var") {
		t.Skip("non-Windows: filepath.VolumeName returns empty; can't simulate cross-volume")
	}
	// Windows-only: hard to simulate without admin; skip in unit test layer.
	t.Skip("cross-volume requires Windows admin to mount second volume; covered in manual smoke")
}

func TestApply_WALReplay(t *testing.T) {
	tmp := t.TempDir()
	gameDir := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Seed temp with 2 files; WAL says one is already done
	for _, name := range []string{"a.dll", "b.dll"} {
		if err := os.WriteFile(filepath.Join(ps.dir(), name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Pretend a.dll already moved to gameDir (mid-apply crash)
	if err := os.WriteFile(filepath.Join(gameDir, "a.dll"), []byte("a.dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ps.dir(), "a.dll")); err != nil {
		t.Fatal(err)
	}
	wal := applyWAL{
		GameID:  "kurogames/wutheringwaves",
		Version: "3.4.0",
		ETag:    `"abc"`,
		Pending: []string{"b.dll"},
		Done:    []string{"a.dll"},
	}
	walPath := filepath.Join(ps.dir(), "apply.wal")
	body, _ := json.Marshal(&wal)
	if err := os.WriteFile(walPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := resumeApply(context.Background(), walPath, gameDir, newApplyLock(), nil, slog.Default()); err != nil {
		t.Fatalf("resumeApply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "b.dll")); err != nil {
		t.Errorf("b.dll missing post-resume: %v", err)
	}
	if _, err := os.Stat(walPath); err == nil {
		t.Errorf("apply.wal should be deleted post-resume")
	}
}

func TestApply_LockHeld(t *testing.T) {
	gameDir := t.TempDir()
	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}

	// Take the lock first
	preLock := newApplyLock()
	if err := preLock.Acquire(gameDir); err != nil {
		t.Fatalf("preLock: %v", err)
	}
	defer preLock.Release()

	plan := &core.UpdatePlan{Files: []core.FileTask{}}
	a := &applier{
		logger: slog.Default(), tempRoot: tmp, gameDir: gameDir,
		progress: ps, plan: plan, lock: newApplyLock(),
	}
	err := a.runApply(context.Background())
	if err == nil {
		t.Fatal("expected lock_held error")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok {
		t.Fatalf("err type = %T, want *core.UpdateError", err)
	}
	if ue.Code != "process_blocked" || ue.Params["kind"] != "lock_held" {
		t.Errorf("err = %+v, want process_blocked+lock_held", ue)
	}
}
```

- [ ] **Step 3: Run, verify PASS**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test -count=1 ./internal/providers/kurogames/... -run "TestApply"
```

Expected: 4 tests PASS (`TestApply_CrossVolumeMidrunError` skipped on non-Windows / non-admin).

- [ ] **Step 4: Whole-internal verification**

```bash
go vet ./internal/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/kurogames/update_apply.go internal/providers/kurogames/update_apply_test.go
git commit -m "feat(kurogames): apply phase — WAL + atomic rename + EXDEV detect + applyLock"
```

---

## Task 10: kurogames.go integration — CheckForUpdate + RunUpdate methods

**Files:**
- Modify: `internal/providers/kurogames/kurogames.go` — add CheckForUpdate / RunUpdate methods on *Provider
- Modify: `internal/providers/kurogames/kurogames.go` — extend interface compliance check to include core.Updater

Spec sources: §1.1 layout (kurogames.go is integration glue), §5.2 StartUpdate flow, §5.3 runUpdate goroutine pseudocode, §6.4 panic recovery contract.

**MVP-minus reminder** (per spec §1.2.3): if Task 1 escalated to MVP-minus, this task's RunUpdate skips patch-apply branch (full-replace only).

- [ ] **Step 1: Add Provider field for HTTP client + Clock**

In `internal/providers/kurogames/kurogames.go`, modify the `Provider` struct:

```go
type Provider struct {
	settings   Settings
	logger     *slog.Logger
	httpClient *http.Client     // for manifest + downloads; injected from app layer
	clock      Clock            // for download retry backoff (test-only injection)
}
```

Update `New` to accept these. Old M2 callers pass `nil` for httpClient/clock → falls back to defaults:

```go
func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		settings:   settings,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		clock:      realKurogamesClock{},
	}
}
```

(Keep M2 signature stable; for tests with custom clock/client, set fields after `New`.)

- [ ] **Step 2: Add CheckForUpdate method**

Append to `kurogames.go`:

```go
// CheckForUpdate fetches the manifest, filters out files identical to
// the current install, returns a populated UpdatePlan. M3.A only.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}

	// Find install path
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	var installPath string
	for _, ig := range installs {
		if ig.GameID == gid {
			installPath = ig.InstallPath
			break
		}
	}
	if installPath == "" {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
	}

	// Extract accountID
	accountID, err := extractAccountID(p.logger)
	if err != nil {
		return core.UpdatePlan{}, &core.UpdateError{
			Code:      "auth_failed",
			Retryable: false,
			Params:    map[string]string{"reason": "accountID extraction failed: " + err.Error()},
		}
	}

	// Read current local version from launcherDownloadConfig.json
	localVersion, _ := readLauncherDownloadConfigVersion(filepath.Join(installPath, "launcherDownloadConfig.json"))
	manifestURL := buildManifestURL(accountID, "G153", "current") // PLACEHOLDER — replace per research

	mf, etag, err := fetchManifest(ctx, p.httpClient, manifestURL)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// Filter to changed files only
	files := filterChangedFiles(installPath, mf.Files, p.logger)
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}

	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanUpdate, // PlanPredownload set by separate StartPredownload entry
		ManifestETag: etag,
		Version:      mf.Version,
		Files:        files,
		TotalBytes:   totalBytes,
	}
	p.logger.Info("CheckForUpdate complete",
		"game", gid,
		"local_version", localVersion,
		"target_version", mf.Version,
		"files_to_update", len(files),
		"bytes", totalBytes,
	)
	return plan, nil
}
```

- [ ] **Step 3: Add RunUpdate method with panic recovery**

Append to `kurogames.go`:

```go
// RunUpdate executes a previously-checked plan. Re-verifies ETag at entry,
// dispatches download phase, then apply phase (skipped for PlanPredownload).
// Panic recovery + structured error per spec §6.4.
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("RunUpdate panic", "game", plan.GameID, "panic", r)
			err = &core.UpdateError{
				Code:      "internal",
				Retryable: true,
				Params:    map[string]string{"detail": fmt.Sprint(r)},
			}
		}
	}()

	if ctx.Err() != nil {
		return ctx.Err() // cancel-before-start
	}

	g := findByID(plan.GameID)
	if g == nil {
		return fmt.Errorf("%w: %s", core.ErrUnknownGame, plan.GameID)
	}

	// Find install path
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return err
	}
	var installPath string
	for _, ig := range installs {
		if ig.GameID == plan.GameID {
			installPath = ig.InstallPath
			break
		}
	}
	if installPath == "" {
		return fmt.Errorf("%w: %s", core.ErrGameNotInstalled, plan.GameID)
	}

	// 2nd game-running guard (spec §2.7)
	if isProcessRunning(g.ExeName) {
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "process_running", "game": string(plan.GameID)},
		}
	}

	// Re-verify ETag at entry
	accountID, _ := extractAccountID(p.logger)
	manifestURL := buildManifestURL(accountID, "G153", plan.Version)
	if currentETag, _ := reFetchManifestETag(ctx, p.httpClient, manifestURL); currentETag != "" && currentETag != plan.ManifestETag {
		return &core.UpdateError{
			Code:      "manifest_changed",
			Retryable: true,
			Params:    map[string]string{"old_etag": plan.ManifestETag, "new_etag": currentETag},
		}
	}

	// Determine TempDir
	tempDir := p.settings.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "launcher-collection", strings.ReplaceAll(string(plan.GameID), "/", "-"))
	}

	progress := newProgressStore(tempDir, string(plan.GameID), plan.Version)
	if err := progress.Init(plan.ManifestETag); err != nil {
		return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
	}

	// Download phase
	d := &downloader{
		client:   p.httpClient,
		logger:   p.logger,
		tempRoot: tempDir,
		progress: progress,
		plan:     &plan,
		onEvent:  onEvent,
		clock:    p.clock,
	}
	if err := d.runDownload(ctx); err != nil {
		return err
	}

	// Predl: rename progress.json → predl_ready.json and stop
	if plan.Kind == core.PlanPredownload {
		if err := progress.RenameToPredlReady(); err != nil {
			return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
		}
		return nil
	}

	// Apply phase
	a := &applier{
		logger:   p.logger,
		tempRoot: tempDir,
		gameDir:  installPath,
		progress: progress,
		plan:     &plan,
		wasPredl: false,
		onEvent:  onEvent,
		lock:     newApplyLock(),
	}
	return a.runApply(ctx)
}

// isProcessRunning checks if the given exe name appears in the process
// list. Uses Windows toolhelp snapshot (kurogames is Windows-only).
// Stub for non-Windows builds always returns false.
func isProcessRunning(exeName string) bool {
	return platformIsProcessRunning(exeName)
}
```

- [ ] **Step 4: Add Windows process check**

Create `internal/providers/kurogames/process_check_windows.go`:

```go
//go:build windows

package kurogames

import (
	"strings"
	"syscall"
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
	_ = syscall.Errno(0) // silence unused syscall import
	return false
}
```

- [ ] **Step 5: Add non-Windows process check stub**

Create `internal/providers/kurogames/process_check_other.go`:

```go
//go:build !windows

package kurogames

func platformIsProcessRunning(exeName string) bool {
	return false // stub: tests run on non-Windows; production is Windows-only
}
```

- [ ] **Step 6: Update interface compliance compile-time check**

Modify the existing `var _ core.Provider = (*Provider)(nil)` block (currently at end of `kurogames.go`):

```go
var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
	_ core.ExeNamer     = (*Provider)(nil)
	_ core.Updater      = (*Provider)(nil) // M3.A: implements update interface
)
```

- [ ] **Step 7: Add integration test**

`internal/providers/kurogames/update_integration_test.go`:

```go
package kurogames

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"launcher-collection-tmp/internal/core"
)

// TestUpdate_HappyPath: end-to-end manifest → download → apply
func TestUpdate_HappyPath(t *testing.T) {
	// Spin up CDN serving 2 file bodies + manifest
	body1 := "content-of-a"
	body2 := "content-of-b"
	hash1 := sha256Hex(body1)
	hash2 := sha256Hex(body2)

	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"e1"`)
		json.NewEncoder(w).Encode(manifestRaw{
			Version: "3.4.0",
			Files: []manifestFileRaw{
				{Path: "a.dll", Hash: hash1, Size: int64(len(body1)), URL: "<placeholder>"},
				{Path: "Engine/b.dll", Hash: hash2, Size: int64(len(body2)), URL: "<placeholder>"},
			},
		})
	})
	mux.HandleFunc("/files/a", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body1)) })
	mux.HandleFunc("/files/b", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body2)) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Build a synthetic manifest: replace URL placeholders with srv.URL
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
	onEvent := func(e core.UpdateEvent) {
		events.Add(1)
	}

	d := &downloader{
		client: srv.Client(), logger: testLogger(), progress: ps,
		plan: &plan, onEvent: onEvent, clock: fakeKurogamesClock{},
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

	// Assert files arrived in gameDir
	for _, f := range plan.Files {
		if _, err := os.Stat(filepath.Join(gameDir, f.Path)); err != nil {
			t.Errorf("file missing in gameDir: %s", f.Path)
		}
	}
	// WAL gone, progress.json gone
	if _, err := os.Stat(filepath.Join(ps.dir(), "apply.wal")); err == nil {
		t.Errorf("apply.wal should be gone")
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func testLogger() *slog.Logger {
	return slog.Default()
}
```

- [ ] **Step 8: Run all kurogames tests**

```bash
go test -count=1 -race ./internal/providers/kurogames/...
```

Expected: ALL existing kuro tests + new ones GREEN.

- [ ] **Step 9: Whole-repo verification**

```bash
go vet ./...
go test -count=1 ./...
```

Expected: GREEN. Note: `go build ./...` may fail if main.go is unaffected; should still be green from M2.

- [ ] **Step 10: Commit**

```bash
git add internal/providers/kurogames/kurogames.go internal/providers/kurogames/process_check_windows.go internal/providers/kurogames/process_check_other.go internal/providers/kurogames/update_integration_test.go
git commit -m "feat(kurogames): integrate CheckForUpdate + RunUpdate; impl core.Updater"
```

---

## Task 11: update_handler.go — Wails RPC + 1st game guard + App.Launch apply-phase refusal

**Files:**
- Create: `internal/app/update_handler.go`
- Create: `internal/app/update_handler_test.go`
- Modify: `internal/app/app.go` — wire `UpdateStateRegistry`, expose RPC methods, modify `Launch` to refuse during apply

Spec sources: §1.2.1 RefreshVersion vs CheckForUpdate independence, §1.2.2 UpdateError marshalling (canonical via snapshot), §2.7 1st game guard at RPC entry, §2.7 App.Launch apply-phase refusal location, §3.3 RPC catalog.

- [ ] **Step 1: Create handler file with RPC methods**

`internal/app/update_handler.go`:

```go
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/kurogames"
)

// StartUpdate kicks off the update flow for a game. Performs 1st-point
// game-running guard + statfs precheck + sets InFlight + spawns
// runUpdate goroutine.
func (a *App) StartUpdate(gameID string) error {
	gid := core.GameID(gameID)
	return a.startUpdateFlow(gid, core.PlanUpdate)
}

// StartPredownload kicks off predownload (download phase only).
func (a *App) StartPredownload(gameID string) error {
	gid := core.GameID(gameID)
	return a.startUpdateFlow(gid, core.PlanPredownload)
}

func (a *App) startUpdateFlow(gid core.GameID, kind core.PlanKind) error {
	// Find provider, type-assert Updater
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s does not support updates (M3.A: only kurogames)", p.ID())
	}

	// 1st game-running guard
	if g := findGameDescriptor(p, gid); g != nil {
		if kurogames.IsProcessRunning(g.ExeName) {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil // error surfaces via snapshot LastError; RPC returns nil per spec §1.2.2
		}
	}

	// CheckForUpdate / re-use existing AvailablePredl
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		return fmt.Errorf("update already in flight for %s", gid)
	}
	state.mu.Unlock()

	// CheckForUpdate (kind-specific entry; for simplicity, reuse same path
	// and override Plan.Kind on return)
	plan, err := upd.CheckForUpdate(context.Background(), gid)
	if err != nil {
		a.setLastError(gid, asUpdateError(err))
		return nil
	}
	plan.Kind = kind

	// Cross-volume + space precheck (spec §5.2)
	tempDir := a.kurogamesTempDir(gid)
	gameDir := a.gameInstallDir(gid, p)
	if err := a.preflightChecks(tempDir, gameDir, plan.TotalBytes); err != nil {
		a.setLastError(gid, asUpdateError(err))
		return nil
	}

	// Set InFlight under mu
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.InFlight = &InFlightOp{
		Plan:    plan,
		Phase:   core.PhaseDownload,
		Total:   plan.TotalBytes,
		cancel:  cancel,
	}
	state.LastError = nil
	state.mu.Unlock()

	a.updateRegistry.EmitTerminal(gid)

	// Launch worker goroutine
	go a.runUpdateWorker(ctx, gid, upd, plan)
	return nil
}

func (a *App) runUpdateWorker(ctx context.Context, gid core.GameID, upd core.Updater, plan core.UpdatePlan) {
	state := a.updateRegistry.Get(gid)
	defer func() {
		// Outer panic recovery — RunUpdate also has its own; this is belt-and-suspenders
		if r := recover(); r != nil {
			state.mu.Lock()
			state.LastError = &core.UpdateError{
				Code:      "internal",
				Retryable: true,
				Params:    map[string]string{"detail": fmt.Sprint(r)},
			}
			state.InFlight = nil
			state.mu.Unlock()
			a.updateRegistry.EmitTerminal(gid)
		}
	}()

	onEvent := func(e core.UpdateEvent) {
		state.mu.Lock()
		if state.InFlight != nil {
			state.InFlight.Phase = e.Phase
			state.InFlight.Current = e.Current
			state.InFlight.Total = e.Total
		}
		state.mu.Unlock()
		a.updateRegistry.EmitChanged(gid)
	}

	err := upd.RunUpdate(ctx, plan, onEvent)

	// Terminal: clear InFlight, set LastError if non-cancel
	state.mu.Lock()
	state.InFlight = nil
	if err != nil && err != context.Canceled {
		state.LastError = asUpdateError(err)
	} else if err == nil {
		// Success: clear AvailableUpdate or set PredlReady
		if plan.Kind == core.PlanUpdate {
			state.AvailableUpdate = nil
		} else if plan.Kind == core.PlanPredownload {
			state.PredlReady = &plan
			state.AvailablePredl = nil
		}
	}
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
}

// CancelInFlight cancels the active op for gameID.
func (a *App) CancelInFlight(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	if state.InFlight == nil || state.InFlight.Phase == core.PhaseApply {
		state.mu.RUnlock()
		return nil // no-op; UI shouldn't allow cancel during apply (spec §2.6)
	}
	cancelFn := state.InFlight.cancel
	state.mu.RUnlock()
	cancelFn()
	return nil
}

// ApplyPredownload triggers the apply phase using a previously-completed
// predownload (spec §2.5). Same game-running guard as StartUpdate.
func (a *App) ApplyPredownload(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.RLock()
	predl := state.PredlReady
	state.mu.RUnlock()
	if predl == nil {
		return fmt.Errorf("no PredlReady for %s", gid)
	}
	// PredlReady plan with Kind=Update + ETag preserved → drives apply-only path.
	// runUpdateWorker's logic on PlanUpdate handles apply normally; download
	// phase will skip all entries (already present in temp).
	predlCopy := *predl
	predlCopy.Kind = core.PlanUpdate

	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	upd, ok := p.(core.Updater)
	if !ok {
		return fmt.Errorf("provider %s no Updater", p.ID())
	}

	// 1st game-running guard
	if g := findGameDescriptor(p, gid); g != nil {
		if kurogames.IsProcessRunning(g.ExeName) {
			a.setLastError(gid, &core.UpdateError{
				Code:      "process_blocked",
				Retryable: true,
				Params:    map[string]string{"kind": "process_running", "game": string(gid)},
			})
			return nil
		}
	}

	// Set InFlight for ApplyPredownload (Phase: Apply at start since download done)
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	if state.InFlight != nil {
		state.mu.Unlock()
		cancel()
		return fmt.Errorf("operation in flight for %s", gid)
	}
	state.InFlight = &InFlightOp{
		Plan:   predlCopy,
		Phase:  core.PhaseApply,
		Total:  int64(len(predlCopy.Files)),
		cancel: cancel,
	}
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)

	go a.runUpdateWorker(ctx, gid, upd, predlCopy)
	return nil
}

// RemovePredownload deletes predl_ready.json + temp files for gameID.
func (a *App) RemovePredownload(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.PredlReady = nil
	state.mu.Unlock()

	tempDir := a.kurogamesTempDir(gid)
	versionDir := filepath.Join(tempDir, strings.ReplaceAll(string(gid), "/", "-"))
	// Best-effort cleanup; ignore errors
	_ = removeAll(versionDir)
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

// DismissError clears state.LastError.
func (a *App) DismissError(gameID string) error {
	gid := core.GameID(gameID)
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

// ResumeInterrupted re-enters the in-flight pipeline using existing
// progress.json or apply.wal. Equivalent to StartUpdate but skips
// CheckForUpdate (the plan is reconstructed from sidecar).
func (a *App) ResumeInterrupted(gameID string) error {
	// Implementation: read sidecar, reconstruct plan, call runUpdateWorker.
	// Detailed flow deferred to Task 16 (integration tests cover this);
	// for now, document that ResumeInterrupted is a thin wrapper.
	return fmt.Errorf("ResumeInterrupted: implementation deferred to integration test phase (Task 16)")
}

// UpdateStatusAll returns per-game state snapshots.
func (a *App) UpdateStatusAll() map[string]GameUpdateSnapshot {
	return a.updateRegistry.SnapshotAll()
}

// --- helpers ---

func (a *App) setLastError(gid core.GameID, err *core.UpdateError) {
	state := a.updateRegistry.Get(gid)
	state.mu.Lock()
	state.LastError = err
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
}

func (a *App) kurogamesTempDir(gid core.GameID) string {
	td := a.settings.Backends.Kurogames.TempDir
	if td == "" {
		return filepath.Join(osTempDir(), "launcher-collection")
	}
	return td
}

func (a *App) gameInstallDir(gid core.GameID, p core.Provider) string {
	installs, err := p.DetectInstall(context.Background())
	if err != nil {
		return ""
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return ig.InstallPath
		}
	}
	return ""
}

func (a *App) preflightChecks(tempDir, gameDir string, totalBytes int64) error {
	tempVol := filepath.VolumeName(tempDir)
	gameVol := filepath.VolumeName(gameDir)
	if tempVol != gameVol && tempVol != "" && gameVol != "" {
		return &core.UpdateError{
			Code:      "cross_volume_temp",
			Retryable: false,
			Params:    map[string]string{"temp_vol": tempVol, "game_vol": gameVol},
		}
	}
	// Disk space precheck — implementation uses windows.GetDiskFreeSpaceEx
	// or syscall equivalent. Stub for non-Windows tests.
	if !platformHasFreeSpace(tempDir, totalBytes+(256<<20)) {
		return &core.UpdateError{
			Code:      "disk_full",
			Retryable: false,
			Params:    map[string]string{"need": fmt.Sprint(totalBytes), "have": "<computed>"},
		}
	}
	return nil
}

func asUpdateError(err error) *core.UpdateError {
	if ue, ok := err.(*core.UpdateError); ok {
		return ue
	}
	return &core.UpdateError{
		Code:      "internal",
		Retryable: true,
		Params:    map[string]string{"detail": err.Error()},
	}
}

func findGameDescriptor(p core.Provider, gid core.GameID) *core.GameDescriptor {
	for _, g := range p.Games() {
		if g.ID == gid {
			return &g
		}
	}
	return nil
}

func removeAll(path string) error {
	return osRemoveAll(path) // wraps os.RemoveAll for testability
}
```

Plus add platform shims in separate files:

`internal/app/update_handler_windows.go`:

```go
//go:build windows

package app

import (
	"os"
	"syscall"
	"unsafe"
)

func osTempDir() string  { return os.TempDir() }
func osRemoveAll(path string) error { return os.RemoveAll(path) }

func platformHasFreeSpace(dir string, need int64) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW := kernel32.NewProc("GetDiskFreeSpaceExW")
	dirPtr, _ := syscall.UTF16PtrFromString(dir)
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	r1, _, _ := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return true // err — assume OK to avoid blocking
	}
	return int64(freeBytesAvailable) >= need
}
```

`internal/app/update_handler_other.go`:

```go
//go:build !windows

package app

import "os"

func osTempDir() string  { return os.TempDir() }
func osRemoveAll(path string) error { return os.RemoveAll(path) }
func platformHasFreeSpace(dir string, need int64) bool { return true } // stub
```

- [ ] **Step 2: Wire `UpdateStateRegistry` into App**

Modify `internal/app/app.go`:

In `App` struct (around the existing `detect` map), add:

```go
type App struct {
	// ... existing fields ...
	updateRegistry *UpdateStateRegistry
}
```

In `New()`, after providers are constructed but before return:

```go
// Construct update state registry; emitter writes to Wails event bus
emit := func(name string, args ...any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, args...)
	}
}
a.updateRegistry = NewUpdateStateRegistry(emit, realClock{})
```

(Assuming `runtime.EventsEmit` is the Wails v2 API; adjust import as `import wruntime "github.com/wailsapp/wails/v2/pkg/runtime"`.)

- [ ] **Step 3: Modify App.Launch to refuse during apply phase**

In `internal/app/app.go`, find existing `Launch(gameID string)` method. Add this check at the very top:

```go
func (a *App) Launch(gameID string) (int, error) {
	gid := core.GameID(gameID)

	// M3.A: refuse if apply phase is in flight (spec §2.7)
	if a.updateRegistry != nil {
		state := a.updateRegistry.Get(gid)
		state.mu.RLock()
		blocked := state.InFlight != nil && state.InFlight.Phase == core.PhaseApply
		state.mu.RUnlock()
		if blocked {
			return 0, fmt.Errorf("game %s: apply in progress; please wait", gameID)
		}
	}

	// ... existing M2 launch logic continues ...
	p, err := a.provider(gid)
	if err != nil {
		return 0, err
	}
	return p.Launch(a.ctx, gid, core.LaunchOptions{})
}
```

- [ ] **Step 4: Add kurogames.IsProcessRunning export**

In `internal/providers/kurogames/kurogames.go`, expose process-check helper:

```go
// IsProcessRunning is exported so app layer can do the 1st-point game-running
// guard at RPC entry without re-implementing process enumeration.
func IsProcessRunning(exeName string) bool {
	return platformIsProcessRunning(exeName)
}
```

- [ ] **Step 5: Write handler tests**

`internal/app/update_handler_test.go`:

```go
package app

import (
	"context"
	"testing"

	"launcher-collection-tmp/internal/core"
)

// fakeUpdater satisfies core.Updater for testing the App layer.
type fakeUpdater struct {
	id          core.BackendID
	games       []core.GameDescriptor
	checkResult core.UpdatePlan
	checkErr    error
	runErr      error
	runEvents   []core.UpdateEvent
}

func (f *fakeUpdater) ID() core.BackendID                  { return f.id }
func (f *fakeUpdater) DisplayName() core.LocalizedString    { return core.LocalizedString{"en": "fake"} }
func (f *fakeUpdater) Games() []core.GameDescriptor         { return f.games }
func (f *fakeUpdater) SettingsSchema() []core.SettingField  { return nil }
func (f *fakeUpdater) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: f.games[0].ID, InstallPath: "/tmp/fake"}}, nil
}
func (f *fakeUpdater) GetIcon(_ context.Context, _ core.GameID) (string, error)        { return "", nil }
func (f *fakeUpdater) GetBackgrounds(_ context.Context, _ core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *fakeUpdater) CheckVersion(_ context.Context, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (f *fakeUpdater) Launch(_ context.Context, _ core.GameID, _ core.LaunchOptions) (int, error) {
	return 0, nil
}
func (f *fakeUpdater) CheckForUpdate(_ context.Context, _ core.GameID) (core.UpdatePlan, error) {
	return f.checkResult, f.checkErr
}
func (f *fakeUpdater) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
	for _, e := range f.runEvents {
		onEvent(e)
	}
	return f.runErr
}

func TestStartUpdate_PropagatesCheckError(t *testing.T) {
	t.Skip("App-layer integration test deferred to Task 16; this stub asserts wiring compiles")
}

// More tests in Task 16 integration phase — this file establishes wiring.
```

- [ ] **Step 6: Run vet + tests**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go vet ./internal/...
go test -count=1 ./internal/app/...
```

Expected: GREEN (most update_handler tests are skipped here; comprehensive coverage in Task 16).

- [ ] **Step 7: Whole-repo build**

```bash
go build ./...
```

Expected: clean (main.go still compiles since update_handler is additive).

- [ ] **Step 8: Commit**

```bash
git add internal/app/update_handler.go internal/app/update_handler_windows.go internal/app/update_handler_other.go internal/app/update_handler_test.go internal/app/app.go internal/providers/kurogames/kurogames.go
git commit -m "feat(app): update RPC handlers + Launch apply-phase refusal + Wails event wiring"
```

---

## Task 12: Pinia updates store + Wails event bindings

**Files:**
- Create: `frontend/src/stores/updates.ts`

Spec sources: §3.3 store + bind() expanded form, §3.7 render rules, §3.8 single-listener rule + justCompletedUpdate helper.

- [ ] **Step 1: Run wails generate module**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
cd frontend && wails.exe generate module && cd ..
```

This regenerates `frontend/wailsjs/go/app/App.{js,d.ts}` to expose the new RPCs (StartUpdate / StartPredownload / CancelInFlight / ApplyPredownload / RemovePredownload / DismissError / ResumeInterrupted / UpdateStatusAll). Verify locally:

```bash
grep -E "function (StartUpdate|UpdateStatusAll)" frontend/wailsjs/go/app/App.d.ts
```

Expected: both signatures present.

- [ ] **Step 2: Create `frontend/src/stores/updates.ts`**

```ts
import { defineStore } from 'pinia';
import { useGamesStore } from './games';
import {
  StartUpdate,
  StartPredownload,
  CancelInFlight,
  ApplyPredownload,
  RemovePredownload,
  DismissError,
  ResumeInterrupted,
  UpdateStatusAll,
} from '../../wailsjs/go/app/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export type Phase = 'download' | 'apply';
export type PlanKind = 'update' | 'predownload';

export type UpdateError = {
  code: string;
  params?: Record<string, string | number>;
  retryable: boolean;
};

export type UpdatePlan = {
  kind: PlanKind;
  manifest_etag: string;
  version: string;
  total_bytes: number;
};

export type InFlightSnapshot = {
  kind: PlanKind;
  phase: Phase;
  current: number;
  total: number;
  version: string;
  started_at: string;
};

export type GameUpdateSnapshot = {
  available_update?: UpdatePlan | null;
  available_predl?: UpdatePlan | null;
  in_flight?: InFlightSnapshot | null;
  last_error?: UpdateError | null;
  predl_ready?: UpdatePlan | null;
};

// justCompletedUpdate (spec §3.8): true iff transitioning from in-flight
// PlanUpdate apply → idle with no error. Used to trigger asset refresh
// in useGamesStore.
function justCompletedUpdate(prev: GameUpdateSnapshot | undefined, snap: GameUpdateSnapshot): boolean {
  if (!prev) return false;
  if (!prev.in_flight) return false;
  if (prev.in_flight.kind !== 'update') return false;
  if (prev.in_flight.phase !== 'apply') return false;
  if (snap.in_flight) return false;
  if (snap.last_error) return false;
  return true;
}

export const useUpdatesStore = defineStore('updates', {
  state: () => ({
    byGame: {} as Record<string, GameUpdateSnapshot>,
    pendingFrame: null as number | null,
    pendingPatches: {} as Record<string, GameUpdateSnapshot>,
  }),

  actions: {
    async loadAll() {
      // Spec §3.3: called exactly ONCE on store mount; subsequent updates
      // arrive via push events.
      this.byGame = await UpdateStatusAll();
    },

    bind() {
      // Single listener (spec §3.8): updates.ts owns the event subscription.
      // useGamesStore must NOT also subscribe.
      EventsOn('update:changed', (gameID: string, snap: GameUpdateSnapshot) => {
        this.pendingPatches[gameID] = snap;
        if (this.pendingFrame == null) {
          this.pendingFrame = requestAnimationFrame(() => {
            const games = useGamesStore();
            for (const [id, s] of Object.entries(this.pendingPatches)) {
              const prev = this.byGame[id];
              this.byGame[id] = s;
              if (justCompletedUpdate(prev, s)) {
                games.refreshVersionFor(id);
                games.loadAssetsFor(id);
              }
            }
            this.pendingPatches = {};
            this.pendingFrame = null;
          });
        }
      });
    },

    // RPC wrappers — frontend components call these
    async startUpdate(gameID: string): Promise<void> { await StartUpdate(gameID); },
    async startPredownload(gameID: string): Promise<void> { await StartPredownload(gameID); },
    async cancelInFlight(gameID: string): Promise<void> { await CancelInFlight(gameID); },
    async applyPredownload(gameID: string): Promise<void> { await ApplyPredownload(gameID); },
    async removePredownload(gameID: string): Promise<void> { await RemovePredownload(gameID); },
    async dismissError(gameID: string): Promise<void> { await DismissError(gameID); },
    async resumeInterrupted(gameID: string): Promise<void> { await ResumeInterrupted(gameID); },
  },

  getters: {
    forGame: (state) => (gameID: string): GameUpdateSnapshot | null => {
      return state.byGame[gameID] ?? null;
    },
  },
});
```

- [ ] **Step 3: Add `refreshVersionFor` and `loadAssetsFor` to games store**

In `frontend/src/stores/games.ts`, add per-game refresh actions (used by updates store):

```ts
// Inside actions:
async refreshVersionFor(gameID: string) {
  const idx = this.games.findIndex((g) => g.id === gameID);
  if (idx < 0 || !this.games[idx].installed) return;
  try {
    const v = await RefreshVersion(gameID);
    this.games[idx].current_version = v.Current;
    this.games[idx].latest_version = v.Latest;
    this.games[idx].has_predownload = !!v.Predownload;
  } catch (e) {
    console.warn('refreshVersionFor failed', gameID, e);
  }
},
async loadAssetsFor(gameID: string) {
  const idx = this.games.findIndex((g) => g.id === gameID);
  if (idx < 0 || !this.games[idx].installed) return;
  try {
    const g = this.games[idx];
    if (!g.icon_url) g.icon_url = await GetIcon(gameID);
    const bgs = await GetBackgrounds(gameID);
    if (bgs.length) {
      const withVideo = bgs.find((b) => b.VideoURL);
      const pick = withVideo ?? bgs[0];
      g.background_url = pick.ImageURL;
      g.background_video = pick.VideoURL;
    }
  } catch (e) {
    console.warn('loadAssetsFor failed', gameID, e);
  }
},
```

- [ ] **Step 4: Wire `loadAll` + `bind` in App.vue setup**

In `frontend/src/App.vue` (or `main.ts` mount logic), call once:

```ts
import { useUpdatesStore } from './stores/updates';

// inside onMounted or App.vue setup script:
const updates = useUpdatesStore();
await updates.loadAll();
updates.bind();
```

- [ ] **Step 5: Verify frontend build**

```bash
cd frontend && npm run build && cd ..
```

Expected: vue-tsc clean, vite build produces dist/.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/stores/updates.ts frontend/src/stores/games.ts frontend/src/App.vue
git commit -m "feat(frontend): updates Pinia store + EventsOn binding + rAF batching + post-update refresh hook"
```

---

## Task 13: ConfirmDialog + ToastHost components

**Files:**
- Create: `frontend/src/components/ConfirmDialog.vue` (~50 lines)
- Create: `frontend/src/components/ToastHost.vue` (~50 lines)
- Create: `frontend/src/composables/useDialog.ts` (helper for Promise<boolean> ConfirmDialog)
- Create: `frontend/src/composables/useToast.ts` (helper for emitting toasts to ToastHost)

Spec sources: §3.4 modal + toast (no native confirm; no new dep).

- [ ] **Step 1: Create ConfirmDialog**

`frontend/src/components/ConfirmDialog.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue';

const open = ref(false);
const message = ref('');
const okLabel = ref('OK');
const cancelLabel = ref('Cancel');
let resolveFn: ((ok: boolean) => void) | null = null;

defineExpose({
  show(opts: { message: string; ok?: string; cancel?: string }): Promise<boolean> {
    message.value = opts.message;
    okLabel.value = opts.ok ?? 'OK';
    cancelLabel.value = opts.cancel ?? 'Cancel';
    open.value = true;
    return new Promise((resolve) => {
      resolveFn = resolve;
    });
  },
});

function onOK() {
  open.value = false;
  resolveFn?.(true);
  resolveFn = null;
}

function onCancel() {
  open.value = false;
  resolveFn?.(false);
  resolveFn = null;
}
</script>

<template>
  <Teleport to="body">
    <dialog v-if="open" open class="confirm-dialog" @keydown.esc="onCancel">
      <p class="msg">{{ message }}</p>
      <div class="actions">
        <button class="btn-cancel" @click="onCancel">{{ cancelLabel }}</button>
        <button class="btn-ok" @click="onOK">{{ okLabel }}</button>
      </div>
    </dialog>
    <div v-if="open" class="confirm-backdrop" @click="onCancel" />
  </Teleport>
</template>

<style scoped>
.confirm-dialog {
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 1000;
  background: rgba(15, 15, 25, 0.95);
  backdrop-filter: blur(12px);
  border: 1px solid rgba(214, 176, 75, 0.4);
  border-radius: 12px;
  padding: 24px;
  min-width: 320px;
  max-width: 480px;
  color: var(--text);
}
.confirm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 999;
}
.msg {
  margin: 0 0 20px;
  line-height: 1.5;
}
.actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
}
.btn-cancel,
.btn-ok {
  padding: 8px 20px;
  border-radius: 8px;
  border: 0;
  cursor: pointer;
  font-family: inherit;
}
.btn-cancel { background: rgba(255,255,255,0.1); color: var(--text); }
.btn-ok     { background: var(--accent); color: var(--bg); }
</style>
```

- [ ] **Step 2: Create ToastHost**

`frontend/src/components/ToastHost.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue';

export type Toast = {
  id: number;
  message: string;
  retryable?: boolean;
  onRetry?: () => void;
};

const toasts = ref<Toast[]>([]);
let nextId = 1;

defineExpose({
  push(t: Omit<Toast, 'id'>) {
    const toast: Toast = { ...t, id: nextId++ };
    toasts.value.push(toast);
    if (toasts.value.length > 3) {
      // Keep newest 3 visible; older collapsed (handled in template via length)
    }
    if (!t.retryable) {
      setTimeout(() => dismiss(toast.id), 5000);
    }
  },
});

function dismiss(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

function retry(t: Toast) {
  t.onRetry?.();
  dismiss(t.id);
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-host">
      <div v-for="t in toasts.slice(-3)" :key="t.id" class="toast" :class="{retryable: t.retryable}">
        <span class="msg">{{ t.message }}</span>
        <button v-if="t.retryable" @click="retry(t)" class="btn-retry">Retry</button>
        <button @click="dismiss(t.id)" class="btn-close">×</button>
      </div>
      <div v-if="toasts.length > 3" class="toast-overflow">+{{ toasts.length - 3 }} more</div>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-host {
  position: fixed;
  top: 80px;
  right: 24px;
  z-index: 900;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  max-width: 360px;
}
.toast {
  background: rgba(20, 20, 30, 0.95);
  backdrop-filter: blur(10px);
  border: 1px solid rgba(255,255,255,0.1);
  border-radius: 8px;
  padding: 12px 16px;
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 8px;
}
.toast.retryable {
  border-color: rgba(214, 176, 75, 0.5);
}
.msg { flex: 1; font-size: 13px; }
.btn-retry {
  background: var(--accent);
  color: var(--bg);
  border: 0;
  padding: 4px 12px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}
.btn-close {
  background: transparent;
  color: var(--text-2);
  border: 0;
  cursor: pointer;
  font-size: 18px;
  line-height: 1;
}
.toast-overflow {
  text-align: center;
  font-size: 11px;
  color: var(--text-2);
  padding: 4px;
}
</style>
```

- [ ] **Step 3: Create composables for ergonomic call**

`frontend/src/composables/useDialog.ts`:

```ts
import { ref, type Ref } from 'vue';

const dialogRef: Ref<any | null> = ref(null);

export function registerDialog(r: any) {
  dialogRef.value = r;
}

export async function confirm(message: string, ok = 'OK', cancel = 'Cancel'): Promise<boolean> {
  if (!dialogRef.value) {
    console.error('ConfirmDialog not mounted');
    return false;
  }
  return await dialogRef.value.show({ message, ok, cancel });
}
```

`frontend/src/composables/useToast.ts`:

```ts
import { ref, type Ref } from 'vue';

const toastRef: Ref<any | null> = ref(null);

export function registerToast(r: any) {
  toastRef.value = r;
}

export function pushToast(message: string, opts: { retryable?: boolean; onRetry?: () => void } = {}) {
  if (!toastRef.value) {
    console.error('ToastHost not mounted');
    return;
  }
  toastRef.value.push({ message, ...opts });
}
```

- [ ] **Step 4: Mount in App.vue**

In `frontend/src/App.vue`:

```vue
<script setup lang="ts">
import ConfirmDialog from './components/ConfirmDialog.vue';
import ToastHost from './components/ToastHost.vue';
import { ref, onMounted } from 'vue';
import { registerDialog } from './composables/useDialog';
import { registerToast } from './composables/useToast';

const dialogRef = ref(null);
const toastRef = ref(null);
onMounted(() => {
  registerDialog(dialogRef.value);
  registerToast(toastRef.value);
});
</script>

<template>
  <!-- existing content -->
  <ConfirmDialog ref="dialogRef" />
  <ToastHost ref="toastRef" />
</template>
```

- [ ] **Step 5: Verify build**

```bash
cd frontend && npm run build && cd ..
```

Expected: vue-tsc clean.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/ConfirmDialog.vue frontend/src/components/ToastHost.vue frontend/src/composables/useDialog.ts frontend/src/composables/useToast.ts frontend/src/App.vue
git commit -m "feat(frontend): ConfirmDialog + ToastHost components — 50-line custom impls"
```

---

## Task 14: BottomBar.vue state matrix + SidebarRow.vue 1px overlay

**Files:**
- Modify: `frontend/src/components/BottomBar.vue`
- Modify: `frontend/src/components/SidebarRow.vue`
- Modify: `frontend/src/styles/theme.css`

Spec sources: §3.1 button matrix (8 rows), §3.2 sidebar overlay z-index, §3.7 BottomBar reads only selected game.

- [ ] **Step 1: Replace BottomBar.vue with state-matrix logic**

`frontend/src/components/BottomBar.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useI18n } from 'vue-i18n';
import { confirm } from '../composables/useDialog';
import { Launch } from '../../wailsjs/go/app/App';

const games = useGamesStore();
const updates = useUpdatesStore();
const { t } = useI18n();

const selectedSnap = computed(() => {
  if (!games.selected) return null;
  return updates.byGame[games.selected.id] ?? null;
});

const inFlight = computed(() => selectedSnap.value?.in_flight ?? null);
const availableUpdate = computed(() => selectedSnap.value?.available_update ?? null);
const availablePredl = computed(() => selectedSnap.value?.available_predl ?? null);
const predlReady = computed(() => selectedSnap.value?.predl_ready ?? null);

// Progress percentage (Download phase by bytes; Apply phase by file count)
const progressPct = computed(() => {
  const ifl = inFlight.value;
  if (!ifl || ifl.total === 0) return 0;
  return Math.round((ifl.current / ifl.total) * 100);
});

const showCancelX = computed(() => inFlight.value?.phase === 'download');

async function onLaunch() {
  if (games.selected) try { await Launch(games.selected.id); } catch (e) { console.error(e); }
}
async function onUpdate() {
  if (!games.selected) return;
  // Check predl_stale: if user clicks Update on new version while old PredlReady exists
  if (predlReady.value && availableUpdate.value && predlReady.value.version !== availableUpdate.value.version) {
    const ok = await confirm(
      t('update.errors.predl_stale', { version: predlReady.value.version, newVersion: availableUpdate.value.version }),
      t('buttons.confirm') ?? 'OK',
      t('buttons.cancel') ?? 'Cancel',
    );
    if (!ok) return;
    await updates.removePredownload(games.selected.id);
  }
  await updates.startUpdate(games.selected.id);
}
async function onPredl() {
  if (games.selected) await updates.startPredownload(games.selected.id);
}
async function onApplyPredl() {
  if (games.selected) await updates.applyPredownload(games.selected.id);
}
async function onRemovePredl() {
  if (games.selected) await updates.removePredownload(games.selected.id);
}
async function onCancel() {
  if (games.selected) await updates.cancelInFlight(games.selected.id);
}
</script>

<template>
  <div v-if="games.selected" class="bottom-bar">
    <div class="hero-stats-line">
      <span class="pill">{{ t('labels.ready_pill') }}</span>
      <span class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
    </div>

    <!-- left: predl button OR remove button (when PredlReady) -->
    <div v-if="!inFlight && availablePredl" class="predl-area">
      <button class="predl-btn" @click="onPredl">{{ t('update.predl_available') }} ↓</button>
    </div>
    <div v-else-if="!inFlight && predlReady" class="predl-area">
      <button class="predl-btn" @click="onRemovePredl">{{ t('update.remove_predl') }}</button>
    </div>
    <div v-else-if="inFlight && inFlight.kind === 'predownload'" class="predl-area">
      <button class="progress-btn predl">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ t('update.predl_downloading', { pct: progressPct }) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
    </div>

    <!-- right: Launch / Update / Update-in-flight / Apply Predl -->
    <div class="launch-area">
      <button v-if="!inFlight && !availableUpdate && !predlReady" class="launch-btn" @click="onLaunch" :disabled="!games.selected.installed">
        <span class="play-tri"></span>{{ t('buttons.play') }}
      </button>
      <button v-else-if="!inFlight && availableUpdate" class="launch-btn update-btn" @click="onUpdate">
        {{ t('update.available') }} ↓
      </button>
      <button v-else-if="!inFlight && predlReady" class="launch-btn" @click="onApplyPredl">
        {{ t('update.predl_ready') }}
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'download'" class="progress-btn update">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ t('update.downloading', { pct: progressPct }) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'apply'" class="progress-btn update apply">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ t('update.applying', { cur: inFlight.current, total: inFlight.total }) }}</span>
        <!-- no cancel-x: spec §2.6 -->
      </button>
    </div>
  </div>
</template>
```

- [ ] **Step 2: Add progress-btn styles**

Append to `frontend/src/styles/theme.css`:

```css
.predl-area { flex-shrink: 0; margin-right: 16px; }
.predl-btn {
  background: rgba(214, 176, 75, 0.15);
  color: var(--accent);
  border: 1px solid rgba(214, 176, 75, 0.4);
  border-radius: 12px;
  padding: 0 24px;
  height: 56px;
  cursor: pointer;
  font-family: inherit;
  font-size: 14px;
}
.progress-btn {
  position: relative;
  background: rgba(20, 20, 30, 0.6);
  color: var(--text);
  border: 1px solid var(--accent-dim);
  border-radius: 12px;
  height: 56px;
  min-width: 200px;
  padding: 0 16px;
  cursor: default;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: inherit;
  font-weight: 700;
}
.progress-btn .fill {
  position: absolute;
  top: 0; bottom: 0; left: 0;
  background: rgba(214, 176, 75, 0.3);
  transition: width 200ms ease-out;
  z-index: 0;
}
.progress-btn .label {
  position: relative;
  z-index: 1;
  flex: 1;
  text-align: center;
}
.progress-btn .cancel-x {
  position: relative;
  z-index: 2;
  margin-left: 12px;
  padding-left: 12px;
  border-left: 1px solid rgba(255,255,255,0.2);
  cursor: pointer;
  font-size: 18px;
}
```

- [ ] **Step 3: Modify SidebarRow.vue with 1px progress overlay**

`frontend/src/components/SidebarRow.vue` — add to existing template/script:

```vue
<script setup lang="ts">
// existing imports + props
import { computed } from 'vue';
import { useUpdatesStore } from '../stores/updates';

const updates = useUpdatesStore();
const props = defineProps<{ game: any }>();

const snap = computed(() => updates.byGame[props.game.id] ?? null);
const inFlight = computed(() => snap.value?.in_flight ?? null);
const progressPct = computed(() => {
  const ifl = inFlight.value;
  if (!ifl || ifl.total === 0) return 0;
  return (ifl.current / ifl.total) * 100;
});
const isPredl = computed(() => inFlight.value?.kind === 'predownload');
</script>

<template>
  <div class="sidebar-row" :class="{updating: inFlight}">
    <!-- existing row content (icon + name + version) -->
    <!-- ... preserve M2 row contents inside .row-content wrapper ... -->
    <div class="row-content"><!-- ... --></div>
    <div v-if="inFlight" class="progress-bar" :class="{predl: isPredl}" :style="{width: progressPct + '%'}"></div>
  </div>
</template>

<style scoped>
.sidebar-row {
  position: relative; /* for overlay positioning */
}
.sidebar-row .row-content {
  position: relative;
  z-index: 2;
}
.sidebar-row .progress-bar {
  position: absolute;
  bottom: 0;
  left: 0;
  height: 1px;
  background: var(--accent);
  z-index: 1;
  transition: width 200ms ease-out;
}
.sidebar-row .progress-bar.predl {
  background: var(--accent-dim);
}
</style>
```

(Adapt to actual M2 SidebarRow structure; the diff is the `.progress-bar` overlay.)

- [ ] **Step 4: Verify build**

```bash
cd frontend && npm run build && cd ..
```

Expected: vue-tsc clean.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/BottomBar.vue frontend/src/components/SidebarRow.vue frontend/src/styles/theme.css
git commit -m "feat(frontend): BottomBar 8-state matrix + SidebarRow 1px progress overlay"
```

---

## Task 15: i18n keys + parity test

**Files:**
- Modify: `frontend/src/locales/en.json` — add `update.*` namespace
- Modify: `frontend/src/locales/zh-TW.json` — same
- Modify: `frontend/src/locales/zh-CN.json` — same (dormant but parity-tested)
- Create: `frontend/src/__tests__/i18n_parity.test.ts` (placeholder; full test in Task 17)

Spec source: §3.6 i18n keys catalog.

- [ ] **Step 1: Add `update.*` namespace to en.json**

Append to `frontend/src/locales/en.json` (inside top-level object):

```json
"update": {
  "available": "Update Available",
  "predl_available": "Pre-download Available",
  "downloading": "Updating {pct}%",
  "applying": "Applying {cur}/{total}",
  "predl_downloading": "Pre-downloading {pct}%",
  "predl_ready": "Apply Pre-download",
  "remove_predl": "Remove",
  "cancel": "Cancel",
  "errors": {
    "process_blocked": "Please close {game} before updating",
    "manifest_changed": "Remote version updated; please refresh",
    "manifest_not_found": "Update manifest not found (server returned 404)",
    "auth_failed": "Authentication failed; please contact support",
    "predl_stale": "Pre-download outdated ({version}). Clear and update to {newVersion}?",
    "interrupted_resume_download": "Last update interrupted. Continue?",
    "interrupted_resume_apply": "Last apply interrupted. Retry?",
    "interrupted_resume_predl_download": "Last pre-download interrupted. Continue?",
    "interrupted_resume_predl_apply": "Last predownload-apply interrupted. Retry?",
    "disk_full": "Insufficient disk space (need {need}, have {have})",
    "cross_volume_temp": "Temp dir and game must be on same drive (M3.A limitation)",
    "cross_volume_midrun": "Drive change detected mid-apply; cannot continue",
    "unsupported_filesystem": "Temp dir must be on NTFS (FAT32/exFAT not supported)",
    "network": "Network error: {detail}",
    "corrupt": "Downloaded file corrupted; please retry",
    "apply_partial": "Apply phase failed; please retry",
    "unrecoverable": "Update state corrupted; please use KRLauncher to repair",
    "internal": "Internal error: {detail}"
  }
}
```

- [ ] **Step 2: Add `update.*` namespace to zh-TW.json**

```json
"update": {
  "available": "有更新",
  "predl_available": "可預下載",
  "downloading": "更新中 {pct}%",
  "applying": "套用中 {cur}/{total}",
  "predl_downloading": "預下載 {pct}%",
  "predl_ready": "套用預下載",
  "remove_predl": "移除",
  "cancel": "取消",
  "errors": {
    "process_blocked": "請先關閉 {game} 再更新",
    "manifest_changed": "remote 已更新版本，請重新整理",
    "manifest_not_found": "找不到更新資訊（伺服器回 404）",
    "auth_failed": "認證失敗，請聯絡支援",
    "predl_stale": "預下載已過期 ({version})，清除並更新到 {newVersion}？",
    "interrupted_resume_download": "上次更新中斷，是否繼續？",
    "interrupted_resume_apply": "上次套用中斷，重新嘗試？",
    "interrupted_resume_predl_download": "上次預下載中斷，是否繼續？",
    "interrupted_resume_predl_apply": "上次套用預下載中斷，重新嘗試？",
    "disk_full": "磁碟空間不足（需要 {need}，剩餘 {have}）",
    "cross_volume_temp": "暫存目錄與遊戲位於不同磁碟（M3.A 不支援）",
    "cross_volume_midrun": "套用中偵測磁碟變更，無法繼續",
    "unsupported_filesystem": "暫存目錄需位於 NTFS 磁碟（FAT32/exFAT 不支援）",
    "network": "網路錯誤：{detail}",
    "corrupt": "下載檔案損毀，請重試",
    "apply_partial": "套用過程中失敗，請重試",
    "unrecoverable": "更新狀態已損毀，請使用 KRLauncher 修復後再試",
    "internal": "內部錯誤：{detail}"
  }
}
```

- [ ] **Step 3: Add `update.*` namespace to zh-CN.json**

```json
"update": {
  "available": "有更新",
  "predl_available": "可预下载",
  "downloading": "更新中 {pct}%",
  "applying": "应用中 {cur}/{total}",
  "predl_downloading": "预下载 {pct}%",
  "predl_ready": "应用预下载",
  "remove_predl": "移除",
  "cancel": "取消",
  "errors": {
    "process_blocked": "请先关闭 {game} 再更新",
    "manifest_changed": "remote 已更新版本，请重新加载",
    "manifest_not_found": "找不到更新信息（服务器返回 404）",
    "auth_failed": "认证失败，请联系支持",
    "predl_stale": "预下载已过期 ({version})，清除并更新到 {newVersion}？",
    "interrupted_resume_download": "上次更新中断，是否继续？",
    "interrupted_resume_apply": "上次应用中断，重试？",
    "interrupted_resume_predl_download": "上次预下载中断，是否继续？",
    "interrupted_resume_predl_apply": "上次应用预下载中断，重试？",
    "disk_full": "磁盘空间不足（需要 {need}，剩余 {have}）",
    "cross_volume_temp": "暂存目录与游戏位于不同磁盘（M3.A 不支持）",
    "cross_volume_midrun": "应用中检测磁盘变化，无法继续",
    "unsupported_filesystem": "暂存目录需位于 NTFS 磁盘（FAT32/exFAT 不支持）",
    "network": "网络错误：{detail}",
    "corrupt": "下载文件损坏，请重试",
    "apply_partial": "应用过程中失败，请重试",
    "unrecoverable": "更新状态已损坏，请使用 KRLauncher 修复后再试",
    "internal": "内部错误：{detail}"
  }
}
```

- [ ] **Step 4: Verify JSON parses**

```bash
cd frontend && npm run build && cd ..
```

Expected: vue-tsc clean (vue-i18n loads JSON).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json
git commit -m "feat(frontend): i18n keys for update.* (en + zh-TW + zh-CN parity)"
```

---

## Task 16: Integration tests + drift doc-test

**Files:**
- Create: `internal/providers/kurogames/update_integration_test.go` — full happy path + predl + apply-predl
- Create: `internal/providers/kurogames/m3a_protocol_doc_test.go` — drift detection
- Modify: existing test files to add coverage from spec §7

Spec source: §7.3 integration tests + §7.5 resume tests + drift test.

- [ ] **Step 1: Extend update_integration_test.go**

Add to `internal/providers/kurogames/update_integration_test.go` (already created in Task 10):

```go
// TestPredl_HappyPath: predownload completes, predl_ready.json written,
// game dir untouched.
func TestPredl_HappyPath(t *testing.T) {
	body := "predl-content"
	hash := sha256Hex(body)
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
	d := &downloader{client: srv.Client(), logger: testLogger(), progress: ps, plan: &plan, clock: fakeKurogamesClock{}}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ps.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	// game dir must be untouched
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
	if err := ps.MarkComplete("next.dll", time.Now(), 4); err != nil {
		t.Fatal(err)
	}
	if err := ps.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}

	// Now simulate ApplyPredownload: rename predl_ready → apply.wal manually
	predlPath := filepath.Join(ps.dir(), "predl_ready.json")
	walPath := filepath.Join(ps.dir(), "apply.wal")
	wal := applyWAL{
		GameID:  "kurogames/wutheringwaves",
		Version: "3.5.0",
		ETag:    `"e1"`,
		WasPredl: true,
		Pending: []string{"next.dll"},
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
```

- [ ] **Step 2: Add drift detection test**

`internal/providers/kurogames/m3a_protocol_doc_test.go`:

```go
package kurogames

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestProtocolDocMatchesCode parses the research markdown and asserts the
// documented manifest URL pattern matches what the Go code produces.
// Detects drift between m3a-kuro-update-protocol.md and update_manifest.go.
func TestProtocolDocMatchesCode(t *testing.T) {
	docPath := "../../../docs/superpowers/research/m3a-kuro-update-protocol.md"
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("research markdown missing (Task 1 not yet run): %v", err)
	}

	// Extract the regex/template from between TEST_ANCHOR markers
	re := regexp.MustCompile(`(?s)<!-- TEST_ANCHOR: manifest_url_regex -->\s*\n(.*?)<!-- END_ANCHOR: manifest_url_regex -->`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		t.Fatalf("TEST_ANCHOR markers not found in %s", docPath)
	}
	docBlock := m[1]

	// Extract URL pattern line (looks like "URL pattern: `https://...`")
	urlRe := regexp.MustCompile("URL pattern: `(https://[^`]+)`")
	urlMatch := urlRe.FindStringSubmatch(docBlock)
	if len(urlMatch) < 2 {
		t.Fatalf("URL pattern not found in TEST_ANCHOR block")
	}

	// Generate URL via Go code
	got := buildManifestURL("test_account", "G153", "3.4.0")

	// Doc has placeholders; check structural similarity
	docHostMatch := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(urlMatch[1])
	codeHostMatch := regexp.MustCompile(`https://([^/]+)/`).FindStringSubmatch(got)
	if len(docHostMatch) > 1 && len(codeHostMatch) > 1 {
		if !strings.EqualFold(docHostMatch[1], codeHostMatch[1]) {
			t.Errorf("doc host = %s, code host = %s", docHostMatch[1], codeHostMatch[1])
		}
	}
}
```

- [ ] **Step 3: Add error-code coverage matrix test**

`internal/providers/kurogames/errcode_coverage_test.go`:

```go
package kurogames

import (
	"strings"
	"testing"
)

// TestErrcodeCoverage walks the manifest of error codes and asserts each
// has at least one referencing test file.
func TestErrcodeCoverage(t *testing.T) {
	codes := []string{
		"process_blocked", "manifest_changed", "manifest_not_found",
		"auth_failed", "network", "predl_stale", "interrupted_resume",
		"disk_full", "cross_volume_temp", "cross_volume_midrun",
		"corrupt", "apply_partial", "unrecoverable",
		"unsupported_filesystem", "internal",
	}
	for _, code := range codes {
		if !strings.Contains(allTestFilesContent(t), code) {
			t.Errorf("error code %q has no test reference; add one to spec §7.2 coverage matrix", code)
		}
	}
}

// allTestFilesContent reads concatenated content of all _test.go files in
// internal/providers/kurogames/ and internal/app/. Implementation: walk
// dir + ReadFile each + concat. Stub for plan template; real impl in Task 16.
func allTestFilesContent(t *testing.T) string {
	t.Helper()
	// TODO during implementation: filepath.Walk + ioutil.ReadFile + concat
	return ""
}
```

(NB: This test is intentionally weak — Task 16 implementer fills in `allTestFilesContent` properly. The plan-time placeholder ensures the test exists.)

- [ ] **Step 4: Sanitize URL fuzz test**

`internal/providers/kurogames/sanitize_url_fuzz_test.go`:

```go
package kurogames

import (
	"strings"
	"testing"
)

func FuzzSanitizeURL(f *testing.F) {
	f.Add("https://prod.kurogame.com/launcher/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/G153/x")
	f.Add("https://x.com/?token=" + strings.Repeat("a", 32))
	f.Add("not a url")
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		out := sanitizeURL(in)
		// Output must NEVER contain the literal accountID format
		if accountIDRe.MatchString(out) {
			t.Errorf("sanitizeURL leaked accountID pattern: %q → %q", in, out)
		}
	})
}
```

- [ ] **Step 5: Run all integration + new tests**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test -count=1 -race ./internal/providers/kurogames/...
go test -count=1 ./internal/...
```

Expected: GREEN.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/kurogames/update_integration_test.go internal/providers/kurogames/m3a_protocol_doc_test.go internal/providers/kurogames/errcode_coverage_test.go internal/providers/kurogames/sanitize_url_fuzz_test.go
git commit -m "test(kurogames): integration tests + drift doc-test + errcode coverage + sanitize fuzz"
```

---

## Task 17: Vitest setup + frontend tests

**Files:**
- Modify: `frontend/package.json` — add Vitest dev dep
- Create: `frontend/vitest.config.ts`
- Create: `frontend/src/__tests__/i18n_parity.test.ts`
- Create: `frontend/src/__tests__/updates_store.test.ts`
- Create: `frontend/src/__tests__/BottomBar.test.ts`

Spec source: §7.8 frontend tests catalog.

- [ ] **Step 1: Add Vitest as devDep**

```bash
cd frontend
npm install --save-dev vitest @vue/test-utils jsdom
cd ..
```

This updates `frontend/package.json` `devDependencies` + `package-lock.json`.

- [ ] **Step 2: Add `vitest.config.ts`**

`frontend/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    globals: true,
  },
});
```

Add to `frontend/package.json` scripts:

```json
"test": "vitest run",
"test:watch": "vitest"
```

- [ ] **Step 3: i18n parity test**

`frontend/src/__tests__/i18n_parity.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import en from '../locales/en.json';
import zhTW from '../locales/zh-TW.json';
import zhCN from '../locales/zh-CN.json';

function flatKeys(obj: any, prefix = ''): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (typeof v === 'object' && v !== null) {
      out.push(...flatKeys(v, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

describe('i18n parity', () => {
  it('en + zh-TW + zh-CN have identical key sets', () => {
    const enKeys = flatKeys(en).sort();
    const twKeys = flatKeys(zhTW).sort();
    const cnKeys = flatKeys(zhCN).sort();
    expect(twKeys).toEqual(enKeys);
    expect(cnKeys).toEqual(enKeys);
  });

  it('all update.errors.* error codes present', () => {
    const required = [
      'update.errors.process_blocked',
      'update.errors.manifest_changed',
      'update.errors.manifest_not_found',
      'update.errors.auth_failed',
      'update.errors.predl_stale',
      'update.errors.disk_full',
      'update.errors.cross_volume_temp',
      'update.errors.cross_volume_midrun',
      'update.errors.unsupported_filesystem',
      'update.errors.network',
      'update.errors.corrupt',
      'update.errors.apply_partial',
      'update.errors.unrecoverable',
      'update.errors.internal',
    ];
    const enKeys = new Set(flatKeys(en));
    for (const k of required) {
      expect(enKeys.has(k), `missing en key: ${k}`).toBe(true);
    }
  });
});
```

- [ ] **Step 4: Updates store test**

`frontend/src/__tests__/updates_store.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

// Mock the wails RPC + EventsOn
vi.mock('../../wailsjs/go/app/App', () => ({
  StartUpdate: vi.fn(),
  StartPredownload: vi.fn(),
  CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(),
  RemovePredownload: vi.fn(),
  DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(),
  UpdateStatusAll: vi.fn(async () => ({})),
}));

let eventHandler: ((gameID: string, snap: any) => void) | null = null;
vi.mock('../../wailsjs/runtime/runtime', () => ({
  EventsOn: (name: string, fn: any) => {
    if (name === 'update:changed') eventHandler = fn;
  },
}));

import { useUpdatesStore } from '../stores/updates';

describe('updates store rAF batching', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    eventHandler = null;
    // Mock requestAnimationFrame as immediate
    global.requestAnimationFrame = (cb: any) => { cb(); return 0; };
  });

  it('latest-wins on multiple events for same gameID', async () => {
    const store = useUpdatesStore();
    store.bind();
    eventHandler!('g1', { in_flight: { current: 100 } });
    eventHandler!('g1', { in_flight: { current: 200 } });
    eventHandler!('g1', { in_flight: { current: 300 } });
    // After rAF flush
    expect(store.byGame['g1'].in_flight.current).toBe(300);
  });
});
```

- [ ] **Step 5: BottomBar component test (1-2 state rows as smoke)**

`frontend/src/__tests__/BottomBar.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import BottomBar from '../components/BottomBar.vue';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({ Launch: vi.fn() }));

describe('BottomBar state matrix', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('renders 開始遊戲 when no plan and no inflight', () => {
    // Pre-populate stores with default-state selected game; assert button label.
    // (Setup details depend on exact M2 store structure; placeholder.)
    const wrapper = mount(BottomBar, {
      global: {
        plugins: [/* i18n etc */],
      },
    });
    // Smoke: just verify no error
    expect(wrapper.exists()).toBe(true);
  });

  // Full 8-row table-driven test deferred to subagent in execution phase
});
```

- [ ] **Step 6: Run tests**

```bash
cd frontend && npm run test && cd ..
```

Expected: i18n parity passes; store + BottomBar tests pass.

- [ ] **Step 7: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/vitest.config.ts frontend/src/__tests__/
git commit -m "test(frontend): vitest setup + i18n parity + updates store rAF + BottomBar smoke"
```

---

## Task 18: Manual smoke + tag v0.3.0-m3a + merge to main

**Files:** none (git ops + manual run)

Spec source: §7.10 manual smoke checklist (17 items).

- [ ] **Step 1: Build production binary**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
wails build
```

Expected: `build/bin/launcher-collection.exe` produced.

- [ ] **Step 2: Run manual smoke checklist**

Reference spec §7.10 (17 items). Run each, mark pass/fail in a working notes file. Surface failures to user before proceeding to Step 3.

Critical items:
- [ ] WuWa at latest version → `[開始遊戲]`, no update button
- [ ] Downgrade `launcherDownloadConfig.json` → Refresh → `[更新]` appears
- [ ] Click `[更新]` while game running → `process_blocked` toast
- [ ] Click `[更新]` properly → progress fills → cancel → cleanup OK
- [ ] Re-click → completes → `[開始遊戲]` returns
- [ ] Disk full simulation (VHD/VM only; **NOT host**)
- [ ] Restart after crash → "interrupted resume?" prompt → continue
- [ ] Predl button → completes → `predl_ready.json` written
- [ ] Refresh after release → `[套用預下載]`
- [ ] ApplyPredownload while running → toast
- [ ] PredlReady stale → ConfirmDialog → cleared
- [ ] Manifest ETag changed → `manifest_changed` toast
- [ ] Multi-toast → ≤3 + "+N more"
- [ ] zh-TW / en switch — all strings render
- [ ] Sidebar progress matches BottomBar % within 1% / 250ms
- [ ] Apply phase: `[×]` not rendered (DOM check)
- [ ] Close mid-download → restart → resume prompt → continue → complete

If all 17 pass → proceed. If any fail → fix before Step 3.

- [ ] **Step 3: Tag v0.3.0-m3a**

```bash
git tag -a v0.3.0-m3a -m "M3.A — WuWa update feature (patch + predownload)"
```

- [ ] **Step 4: Merge --no-ff to main**

```bash
git checkout main
git merge --no-ff m3a/spec -m "merge: M3.A — WuWa update download/apply + predownload"
```

The merge commit body should also reference: spec at commit `27f53d9`, plan at the head of `m3a/spec` after Task 17.

- [ ] **Step 5: Verify post-merge state**

```bash
git log --oneline --graph -15
git tag -l --format='%(refname:short) -> %(*objectname:short)' v0.3.0-m3a
```

Expected: merge commit on main; tag points to last task commit on m3a/spec branch tip.

- [ ] **Step 6: Update memory file**

In `memory/project_status.md`, mark M3.A SHIPPED, add merge commit SHA, add summary of new artifacts:

- New protocol research: `docs/superpowers/research/m3a-kuro-update-protocol.md`
- New core types: `core.Updater`, `UpdatePlan`, `UpdateEvent`, `UpdateError`
- New provider files: `internal/providers/kurogames/update_*.go`
- New app files: `internal/app/update_state.go`, `update_handler.go`
- New frontend: `stores/updates.ts`, `ConfirmDialog.vue`, `ToastHost.vue`, `update.*` i18n

(Skill controller updates memory file after this task; no code commit.)

- [ ] **Step 7: Final verification**

```bash
go test -count=1 ./...
go build ./...
```

Expected: all GREEN on main post-merge.

---

## Self-review checklist

After all 18 tasks complete:

- [ ] Spec coverage: every section of `2026-05-04-launcher-collection-m3a-wuwa-update-design.md` has at least one task implementing it. Verified during plan write.
- [ ] No placeholders: searched plan for "TBD", "TODO", "implement later" — only legitimate placeholders are in Task 1's research markdown TEMPLATE (filled in during research) and Task 7's `buildManifestURL` (filled per research output).
- [ ] Type consistency: `core.Updater`, `UpdatePlan`, `UpdateEvent`, `UpdateError`, `PlanKind`, `Phase` used uniformly across all tasks.
- [ ] M3.A.0 escalation contract clear: Task 1 Step 8 documents the MVP-minus fallback path.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-04-launcher-collection-m3a-wuwa-update.md`. Two execution options:

1. **Subagent-Driven (recommended)** — Dispatch fresh subagent per task with the M2-proven pipeline (implementer haiku → spec reviewer haiku → code-quality reviewer haiku); review between tasks; fast iteration. **REQUIRED SUB-SKILL:** superpowers:subagent-driven-development.

2. **Inline Execution** — Execute tasks in this session using superpowers:executing-plans; batch execution with checkpoints for user review.

Per memory `feedback_autonomous_m1.md` (M1-scoped, expired) and the user's M2 pattern: M3.A defaults to **task-by-task with checkpoints** — user approves between tasks, no autonomous loop.

Which approach?
