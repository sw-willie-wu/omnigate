# m3-refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract kurogames-specific coupling from `internal/app/update_handler.go` into `internal/core` interfaces and shared utilities so future M3.B (HoYoverse) and M3.C (Hypergryph) updaters can plug in without if/else dispatch.

**Architecture:** Move generic types and pure functions (`ProgressFile`, `RecoveryPhase`, `ScanRecovery`, `LoadProgress`, etc.) from `internal/providers/kurogames/update_progress.go` to `internal/core/{progress,recovery,sidecar}.go`. Introduce one optional interface (`core.ProcessChecker`) for game-running detection — kurogames `Provider` implements it. Generalise the `kurogamesTempDir` helper into `tempDirFor(backend, gid)` defined on `*App` in `internal/app/app.go`. Pure refactor: zero behavior change beyond a single deliberate error-string trim.

**Tech Stack:** Go 1.26.2, Wails v2.12.0, Vue 3 + Pinia (frontend untouched), `golang.org/x/sys/windows`, stdlib (`encoding/json`, `os`, `path/filepath`, `strings`, `sync`, `time`).

**Spec:** `docs/superpowers/specs/2026-05-05-launcher-collection-m3-refactor-design.md`

---

## File map

### Created
- `internal/core/progress.go` — `ProgressFile`, `ProgressEntry` types
- `internal/core/recovery.go` — `RecoveryPhase` enum + 5 const + `RecoveryState` + `ScanRecovery` func
- `internal/core/sidecar.go` — `LoadProgress` / `LoadProgressFromPath` / `ReadWALETag` + private `loadProgressFile` / `fileExists`
- `internal/core/process_checker.go` — `ProcessChecker` interface
- `internal/core/recovery_test.go` — migrated tests for `ScanRecovery` / `RecoveryPhase*`
- `internal/core/sidecar_test.go` — migrated tests for `LoadProgress` parse error
- `docs/superpowers/research/m3-refactor-baseline-tests.txt` — pre-refactor `go test ./...` snapshot (committed alongside Task 1)

### Modified
- `internal/providers/kurogames/update_progress.go` — strip moved types/funcs/private helpers; `progressStore` consumes `core.ProgressFile` / `core.ProgressEntry`
- `internal/providers/kurogames/update_progress_test.go` — keep tests for `progressStore`; remove tests of moved symbols (migrated to core)
- `internal/providers/kurogames/update_download.go` — 2 sites of `LoadProgress` → `core.LoadProgress`
- `internal/providers/kurogames/update_load_test.go` — 1 site of private `loadProgressFile` → `core.LoadProgressFromPath`
- `internal/providers/kurogames/kurogames.go` — add `Provider.IsGameRunning(gid)`; delete public `IsProcessRunning(exeName)`; private `isProcessRunning` stays
- `internal/app/update_handler.go` — replace all `kurogames.X` sidecar/recovery refs with `core.X`; replace 3 `IsProcessRunning` sites with `ProcessChecker` type-assert; trim line-38 error string parenthetical; delete the local `kurogamesTempDir` helper; replace 4 call sites with `tempDirFor(...)`; delete the `kurogames` import
- `internal/app/app.go` — add `tempDirFor(backend, gid)` method (kurogames-only switch arm in v0.3.1); replace 1 call site at the existing `kurogamesTempDir(gid)` location

### Untouched
- Frontend (`frontend/**`)
- Settings TOML schema / `internal/app/settings.go` (no field add)
- Other provider packages (`hoyoverse`, `hypergryph`, `iconext`, `dirver`)
- All `internal/providers/kurogames/*.go` files except those listed above

---

## Conventions

- All commits use plain messages (no `Co-Authored-By:` trailer per `memory/feedback_commits.md`).
- After every code change, run `go build ./...` to catch compile errors fast.
- After Tasks 1–5, run `go test ./...` and confirm count is **>= baseline** with all GREEN.
- Subagent shells: prepend `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"` (Go is not on default PATH on this Windows host).
- This Windows host has `CGO_ENABLED=0`; do NOT pass `-race` to `go test` (per `memory/feedback_no_cgo_race.md`).

---

## Task 1: Move types + private helpers to `internal/core/`

**Goal:** Move `ProgressFile`, `ProgressEntry`, `RecoveryPhase` enum + 5 const, `RecoveryState`, plus the private helpers `loadProgressFile` and `fileExists` to `internal/core/`. Create `core.ProgressFile`-typed callers in kurogames as compile-driven follow-ups.

**Files:**
- Create: `internal/core/progress.go`
- Create: `internal/core/recovery.go` (function bodies in Task 2; only types + enum here)
- Modify: `internal/providers/kurogames/update_progress.go` (strip moved types + helpers; switch internal callers to `core.*`)
- Create: `docs/superpowers/research/m3-refactor-baseline-tests.txt` (baseline snapshot)

- [ ] **Step 1.1: Capture baseline test count**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
mkdir -p docs/superpowers/research
go test ./... 2>&1 | tee docs/superpowers/research/m3-refactor-baseline-tests.txt
grep -c "^ok " docs/superpowers/research/m3-refactor-baseline-tests.txt
```

Expected: a number printed (the count of `ok` lines = number of test packages that passed). Record this number — every subsequent verification must match or exceed.

- [ ] **Step 1.2: Create `internal/core/progress.go`**

```go
package core

import "time"

// ProgressEntry is one downloaded file's resume metadata after successful
// download + hash verify + atomic rename. mtime+size exact-equality is the
// resume trust check.
type ProgressEntry struct {
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
	Hash  string    `json:"hash,omitempty"`
}

// ProgressFile is the on-disk shape of progress.json (and predl_ready.json
// after rename — same schema). Provider-agnostic; M3.A kurogames was the
// first consumer, M3.B HoYoverse / M3.C Hypergryph reuse this type.
type ProgressFile struct {
	GameID  string                   `json:"game_id"`
	Version string                   `json:"version"`
	ETag    string                   `json:"etag"`
	Entries map[string]ProgressEntry `json:"entries"`
}
```

- [ ] **Step 1.3: Create `internal/core/recovery.go` with types only (functions added in Task 2)**

```go
package core

// RecoveryPhase identifies the in-progress sidecar state a directory contains.
// Used by ScanRecovery (defined in this package) to surface what the App layer
// should resume on next launch.
type RecoveryPhase int

const (
	RecoveryNone RecoveryPhase = iota
	RecoveryPhaseDownloadResume
	RecoveryPhaseApplyResume
	RecoveryPhasePredlAwaiting
	RecoveryCorrupt
)

// RecoveryState bundles the result of ScanRecovery: which phase the sidecar
// directory is in, whether the apply.wal was originally a predownload (so UI
// can surface "predl-resume" copy), and any parse error encountered.
type RecoveryState struct {
	Phase    RecoveryPhase
	WasPredl bool
	Err      error
}
```

- [ ] **Step 1.4: Strip the moved types from `kurogames/update_progress.go`**

Open `internal/providers/kurogames/update_progress.go`. Delete the following blocks (line numbers are pre-refactor):
- Lines 18–22: `ProgressEntry struct` block
- Lines 24–31: `ProgressFile struct` block
- Lines 157–166: `RecoveryPhase` type + `const ( ... )` block
- Lines 168–172: `RecoveryState struct` block

Keep the imports in place; some imports (`encoding/json`, `os`, `path/filepath`, `time`, `bytes`, `errors`, `fmt`, `strings`, `sync`) are still needed by `progressStore`, `ScanRecovery`, `LoadProgress`, etc. (Task 2 moves the functions out; for now leave imports as-is — Go's `goimports` cleans up unused imports automatically when run; otherwise the next `go build` will flag them.)

- [ ] **Step 1.5: Add core import + retype `progressStore` literal sites in `update_progress.go`**

Add `"launcher-collection-tmp/internal/core"` to the import block in `update_progress.go`.

Inside `progressStore.Init` (around line 58 pre-refactor):
```go
// BEFORE:
pf := ProgressFile{
	GameID:  p.gameID,
	Version: p.version,
	ETag:    etag,
	Entries: map[string]ProgressEntry{},
}

// AFTER:
pf := core.ProgressFile{
	GameID:  p.gameID,
	Version: p.version,
	ETag:    etag,
	Entries: map[string]core.ProgressEntry{},
}
```

Inside `progressStore.MarkComplete` (around line 78 pre-refactor):
```go
// BEFORE:
pf.Entries[relPath] = ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond)}

// AFTER:
pf.Entries[relPath] = core.ProgressEntry{Size: size, MTime: mtime.Truncate(time.Millisecond)}
```

Inside `loadProgressFile` (around line 145 pre-refactor — function still lives here for Step 1.6):
```go
// BEFORE:
var pf ProgressFile

// AFTER:
var pf core.ProgressFile
```

And update `loadProgressFile`'s return type:
```go
// BEFORE:
func loadProgressFile(path string) (*ProgressFile, error) {

// AFTER:
func loadProgressFile(path string) (*core.ProgressFile, error) {
```

- [ ] **Step 1.6: Run `go build ./...` to confirm kurogames + core compile**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go build ./...
```

Expected: silent (exit 0). If any error mentions `ProgressFile`/`ProgressEntry` undefined inside kurogames, that's a Step 1.5 leftover — find the call site and prefix with `core.`.

- [ ] **Step 1.7: Run `go test ./...` to confirm test count holds**

```bash
go test ./... 2>&1 | tee /tmp/m3r-task1-tests.txt
grep -c "^ok " /tmp/m3r-task1-tests.txt
```

Expected: same number as Step 1.1's baseline. All GREEN.

(Note: `LoadProgress`/`ScanRecovery`/`RecoveryPhase*` symbols still live in the kurogames package as of this task — Task 2 moves the functions; Task 1's scope is just types + private helpers.)

- [ ] **Step 1.8: Commit**

```bash
git add internal/core/progress.go internal/core/recovery.go internal/providers/kurogames/update_progress.go docs/superpowers/research/m3-refactor-baseline-tests.txt
git commit -m "refactor(m3-refactor): move ProgressFile/RecoveryPhase types to core"
```

---

## Task 2: Move `ScanRecovery` / `LoadProgress` / `LoadProgressFromPath` / `ReadWALETag` + private helpers to core

**Goal:** Move the four public sidecar functions plus the private `loadProgressFile` and `fileExists` helpers from kurogames to core. Migrate the corresponding tests. Update kurogames internal callers (download workers, load benchmark) to use `core.*`.

**Files:**
- Create: `internal/core/sidecar.go` (LoadProgress, LoadProgressFromPath, ReadWALETag, private loadProgressFile, private fileExists)
- Modify: `internal/core/recovery.go` (add ScanRecovery body)
- Create: `internal/core/recovery_test.go` (migrated 6 tests)
- Create: `internal/core/sidecar_test.go` (migrated 1 test)
- Modify: `internal/providers/kurogames/update_progress.go` (delete moved bodies; remove now-unused imports)
- Modify: `internal/providers/kurogames/update_progress_test.go` (delete migrated tests; keep `progressStore` tests)
- Modify: `internal/providers/kurogames/update_download.go` (lines 68, 129: `LoadProgress` → `core.LoadProgress`)
- Modify: `internal/providers/kurogames/update_load_test.go` (line 99: `loadProgressFile` → `core.LoadProgressFromPath`)

- [ ] **Step 2.1: Create `internal/core/sidecar.go`**

```go
package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadProgress parses progress.json from the given dir.
func LoadProgress(dir string) (*ProgressFile, error) {
	return loadProgressFile(filepath.Join(dir, "progress.json"))
}

// LoadProgressFromPath parses a sidecar ProgressFile (progress.json or
// predl_ready.json — same schema) from an explicit path. Used by App
// layer's ResumeInterrupted ETag drift check.
func LoadProgressFromPath(path string) (*ProgressFile, error) {
	return loadProgressFile(path)
}

// ReadWALETag returns the ETag recorded in apply.wal's header line.
// Returns "" if file missing/unreadable/header malformed. Used by App
// layer's ResumeInterrupted ETag drift check.
//
// WAL format: line 1 is JSON header `{"etag":"<value>","plan_files":[...]}`;
// subsequent lines are `<relpath> OK\n` per applied file.
func ReadWALETag(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		nl = len(body)
	}
	var hdr struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body[:nl], &hdr); err != nil {
		return ""
	}
	return hdr.ETag
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
```

- [ ] **Step 2.2: Append `ScanRecovery` body to `internal/core/recovery.go`**

Open `internal/core/recovery.go` and append (after the existing const + struct blocks):

```go
import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ScanRecovery resolves sidecar collisions in a version-scoped temp dir.
// Returns the recovery phase based on which sidecar files exist + their parse
// state. Side effects: cleans up stale companions when a definitive sidecar
// is found (e.g. apply.wal supersedes progress.json + predl_ready.json).
//
// WasPredl is set from apply.wal's `was_predl` header field — distinguishes
// "interrupted apply originated from a predl" from "interrupted apply from a
// fresh download", surfaced in the resume prompt copy.
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
		walPath := filepath.Join(dir, "apply.wal")
		body, err := os.ReadFile(walPath)
		if err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		var hdr struct {
			WasPredl bool `json:"was_predl"`
		}
		if err := json.Unmarshal(body, &hdr); err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		return RecoveryState{Phase: RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

	case hasProgress && hasPredl:
		_ = os.Remove(filepath.Join(dir, "progress.json"))
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

	case hasProgress:
		if _, err := loadProgressFile(filepath.Join(dir, "progress.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhaseDownloadResume}

	case hasPredl:
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

	default:
		return RecoveryState{Phase: RecoveryNone}
	}
}
```

(Note: the `import` block above goes at the **top** of the file, after `package core`. Merge with the file's existing imports if any are already there. As of Task 1's commit, recovery.go has no imports — just types — so this block is the file's first `import`.)

- [ ] **Step 2.3: Create `internal/core/sidecar_test.go` with the migrated `TestProgress_LoadCorruptReturnsErr`**

```go
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProgress_CorruptReturnsErr(t *testing.T) {
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
```

- [ ] **Step 2.4: Create `internal/core/recovery_test.go` with 6 migrated tests**

```go
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRecovery_ApplyWalWins(t *testing.T) {
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
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestScanRecovery_PredlOverProgress(t *testing.T) {
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
	if state.Phase != RecoveryPhasePredlAwaiting {
		t.Errorf("Phase = %v, want RecoveryPhasePredlAwaiting", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestScanRecovery_DownloadOnly_NotFromPredl(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseDownloadResume {
		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; download-only sidecar has no predl context")
	}
}

func TestScanRecovery_ApplyResume_FromFreshDownload(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":false,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; WAL was_predl=false")
	}
}

func TestScanRecovery_ApplyResume_FromPredl(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":true,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if !state.WasPredl {
		t.Errorf("WasPredl = false; WAL was_predl=true")
	}
}

func TestScanRecovery_CorruptWal(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryCorrupt {
		t.Errorf("Phase = %v, want RecoveryCorrupt", state.Phase)
	}
}
```

- [ ] **Step 2.5: Delete the moved function bodies + private helpers from `kurogames/update_progress.go`**

Open `internal/providers/kurogames/update_progress.go`. Delete:
- The `LoadProgress` function definition (around lines 107–110 pre-refactor)
- The `LoadProgressFromPath` function definition (around lines 112–117)
- The `ReadWALETag` function definition + its godoc (around lines 119–143)
- The `loadProgressFile` private helper (around lines 145–155)
- The `ScanRecovery` function definition + its godoc (around lines 174–232)
- The `fileExists` private helper (around lines 234–237)

Now `progressStore.MarkComplete` and `progressStore.Init` will call `loadProgressFile` → undefined locally. Inside `MarkComplete` (around line 74 pre-refactor):
```go
// BEFORE:
pf, err := loadProgressFile(filepath.Join(p.dir(), "progress.json"))

// AFTER:
pf, err := core.LoadProgressFromPath(filepath.Join(p.dir(), "progress.json"))
```

(Imports check: `bytes`, `errors`, `fmt` may now be unused in `update_progress.go`; let `go build` flag and remove. `core` was added in Task 1.)

- [ ] **Step 2.6: Update `kurogames/update_download.go` LoadProgress call sites**

Open `internal/providers/kurogames/update_download.go`. Add `"launcher-collection-tmp/internal/core"` to imports if not already present.

Line 68:
```go
// BEFORE:
progress, _ := LoadProgress(d.progress.dir())

// AFTER:
progress, _ := core.LoadProgress(d.progress.dir())
```

Line 129 — same edit (verify with `grep -n "LoadProgress" internal/providers/kurogames/update_download.go`).

- [ ] **Step 2.7: Update `kurogames/update_load_test.go` private helper call**

Open `internal/providers/kurogames/update_load_test.go`. Add `"launcher-collection-tmp/internal/core"` to imports if not already present.

Line 99:
```go
// BEFORE:
pf, err := loadProgressFile(progressPath)

// AFTER:
pf, err := core.LoadProgressFromPath(progressPath)
```

- [ ] **Step 2.8: Delete the migrated tests from `kurogames/update_progress_test.go`**

Open `internal/providers/kurogames/update_progress_test.go`. Delete:
- `TestProgress_LoadCorruptReturnsErr` (lines 53–65 pre-refactor) — migrated to `core/sidecar_test.go`
- `TestProgress_RecoveryScan_ApplyWalWins` (lines 94–113) — migrated to `core/recovery_test.go`
- `TestProgress_RecoveryScan_PredlOverProgress` (lines 115–134) — migrated to `core/recovery_test.go`
- `TestRecoveryScan_DownloadOnly_NotFromPredl` (lines 140–152) — migrated to `core/recovery_test.go`
- `TestRecoveryScan_ApplyResume_FromFreshDownload` (lines 154–167) — migrated to `core/recovery_test.go`
- `TestRecoveryScan_ApplyResume_FromPredl` (lines 169–182) — migrated to `core/recovery_test.go`
- `TestRecoveryScan_CorruptWal` (lines 184–193) — migrated to `core/recovery_test.go`

Keep:
- `TestProgress_WriteAndLoadEntry` (lines 11–35)
- `TestProgress_AtomicWrite` (lines 37–51)
- `TestProgress_RenameToPredlReady` (lines 67–92)

Inside the surviving `TestProgress_WriteAndLoadEntry`, rewire `LoadProgress` to `core.LoadProgress`:
```go
// BEFORE:
loaded, err := LoadProgress(p.dir())

// AFTER (also add core import to this file):
loaded, err := core.LoadProgress(p.dir())
```

Add `"launcher-collection-tmp/internal/core"` to imports. The `// Spec §7.2 mandates 4 RecoveryScan variants...` comment block (lines 136–138) becomes orphaned — delete it too.

- [ ] **Step 2.9: Run `go build ./...` to confirm everything compiles**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go build ./...
```

Expected: silent (exit 0). If any error mentions an unused import, edit the file and remove the import.

- [ ] **Step 2.10: Run `go test ./...` and verify count >= baseline, all GREEN**

```bash
go test ./... 2>&1 | tee /tmp/m3r-task2-tests.txt
grep -c "^ok " /tmp/m3r-task2-tests.txt
```

Expected: same number as Step 1.1's baseline (the `ok` count counts packages, not tests; total test count grows because core gets new tests, but `ok` package count stays equal).

Verify the new core tests are actually running:
```bash
go test -v -run "TestScanRecovery|TestLoadProgress" ./internal/core/ 2>&1 | head -30
```

Expected: 7 tests listed (`TestLoadProgress_CorruptReturnsErr` + 6 `TestScanRecovery_*`), all PASS.

- [ ] **Step 2.11: Commit**

```bash
git add internal/core/sidecar.go internal/core/recovery.go internal/core/sidecar_test.go internal/core/recovery_test.go internal/providers/kurogames/update_progress.go internal/providers/kurogames/update_progress_test.go internal/providers/kurogames/update_download.go internal/providers/kurogames/update_load_test.go
git commit -m "refactor(m3-refactor): move ScanRecovery/LoadProgress/ReadWALETag to core; migrate tests"
```

---

## Task 3: Add `core.ProcessChecker` interface + Kurogames `Provider.IsGameRunning` impl + delete public `IsProcessRunning`

**Goal:** Introduce one optional interface for game-running detection. Kurogames `Provider` implements it via composition of existing `ExeName` (M2 ExeNamer) and `platformIsProcessRunning` (build-tagged). Delete the public `kurogames.IsProcessRunning(exeName)` package func; private `isProcessRunning` stays (still used by `RunUpdate` at `kurogames.go:305`).

**Files:**
- Create: `internal/core/process_checker.go`
- Modify: `internal/providers/kurogames/kurogames.go` (add IsGameRunning method + compile-time check; delete public IsProcessRunning)

- [ ] **Step 3.1: Create `internal/core/process_checker.go`**

```go
package core

// ProcessChecker is an optional capability: providers that can detect whether
// their game is currently running implement this. Callers type-assert
// (mirrors core.ExeNamer / core.CheckForUpdateProgress / core.PathProvider).
type ProcessChecker interface {
	IsGameRunning(gid GameID) (bool, error)
}
```

- [ ] **Step 3.2: Add `Provider.IsGameRunning` method to `kurogames/kurogames.go`**

Open `internal/providers/kurogames/kurogames.go`. Find the `ExeName` method (around lines 151–157). Immediately after it, add:

```go
// IsGameRunning implements core.ProcessChecker — used by App layer's update
// flow as the 1st-point game-running guard. Composes ExeName (the existing
// core.ExeNamer impl) with the build-tag-gated platformIsProcessRunning.
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
	exe, ok := p.ExeName(gid)
	if !ok {
		return false, nil
	}
	return platformIsProcessRunning(exe), nil
}
```

- [ ] **Step 3.3: Add compile-time check for ProcessChecker**

In `kurogames/kurogames.go`, find the existing compile-time interface assertions block (around lines 384–390 pre-refactor — `var ( _ core.Provider = (*Provider)(nil); ...`). Add to the block:

```go
var (
	_ core.Provider                = (*Provider)(nil)
	_ core.ExeNamer                = (*Provider)(nil)
	_ core.PathProvider            = (*Provider)(nil)
	_ core.Updater                 = (*Provider)(nil)
	_ core.CheckForUpdateProgress  = (*Provider)(nil)
	_ core.ProcessChecker          = (*Provider)(nil)  // <-- NEW
)
```

(Keep the existing entries verbatim; the new line is the one with `core.ProcessChecker`. Adjust formatting to match the file's existing alignment style.)

- [ ] **Step 3.4: Delete the public `IsProcessRunning(exeName)` package func**

In `kurogames/kurogames.go`, locate the public `IsProcessRunning` function (around lines 377–381 pre-refactor) and delete:

```go
// DELETE:
// IsProcessRunning is exported so app layer can do the 1st-point game-running
// guard at RPC entry without re-implementing process enumeration.
func IsProcessRunning(exeName string) bool {
	return platformIsProcessRunning(exeName)
}
```

The private `isProcessRunning` (lines 370–375) **stays** — `Provider.RunUpdate` at `kurogames.go:305` still calls it.

- [ ] **Step 3.5: Run `go build ./...`**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go build ./...
```

Expected: silent (exit 0). If `internal/app/update_handler.go` complains about `kurogames.IsProcessRunning` undefined — that's expected; Task 4 fixes those 3 sites.

⚠️ Task 4 has not run yet, so the project intentionally does NOT build clean at this point. **Skip the build success check; proceed to Task 4 directly. Do NOT commit until Task 4 lands.**

Actually, this step (3.5) just runs build for diagnostics — record the failures (3 expected: `update_handler.go:46`, `:250`, `:343` referencing `kurogames.IsProcessRunning`).

- [ ] **Step 3.6: DO NOT COMMIT YET**

Task 3's changes intentionally leave `update_handler.go` with broken references to the deleted `kurogames.IsProcessRunning`. Bundle Task 3 + Task 4 commits OR include a stub call to keep build green between commits. We choose **bundle**: leave Task 3 staged but uncommitted; proceed to Task 4 and commit them together.

```bash
git add internal/core/process_checker.go internal/providers/kurogames/kurogames.go
# DO NOT git commit yet — Task 4 must land first
```

---

## Task 4: Replace all `kurogames.X` refs in `update_handler.go` with `core.X`; switch process-check sites to `ProcessChecker` type-assert; trim line-38 error string

**Goal:** Make `internal/app/update_handler.go` provider-agnostic (except the two `"kurogames"` string literals at `:299` and `:671` documented as v0.3.1 anchors). After this task, the only `kurogames\.` package refs in the entire `internal/app/` tree are 2 lines in `app.go` (the provider-construction kept).

**Files:**
- Modify: `internal/app/update_handler.go`

- [ ] **Step 4.1: Remove the `kurogames` import from `update_handler.go`**

Open `internal/app/update_handler.go`. At line 13, delete the line:
```go
"launcher-collection-tmp/internal/providers/kurogames"
```

Add `"launcher-collection-tmp/internal/core"` if it isn't already present in the import block (it should be — `update_handler.go` already references `core.GameID` etc.).

- [ ] **Step 4.2: Trim the line-38 error string**

```go
// BEFORE (line 38):
return fmt.Errorf("provider %s does not support updates (M3.A: only kurogames)", p.ID())

// AFTER:
return fmt.Errorf("provider %s does not support updates", p.ID())
```

This is the single deliberate non-byte-exact behavior change of the entire refactor (per spec §1.5.1).

- [ ] **Step 4.3: Replace the 3 `kurogames.IsProcessRunning(exeName)` sites with `ProcessChecker` type-assert**

Site at `update_handler.go:46` (inside `startUpdateFlow`):

```go
// BEFORE (lines around 42–55):
// 1st game-running guard — resolve exe name via core.ExeNamer interface ...
if exeName, ok := gameExeName(p, gid); ok {
	if kurogames.IsProcessRunning(exeName) {
		a.setLastError(gid, &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "process_running", "game": string(gid)},
		})
		return nil
	}
}

// AFTER:
// 1st game-running guard — provider's ProcessChecker capability (if implemented).
if pc, ok := p.(core.ProcessChecker); ok {
	if running, _ := pc.IsGameRunning(gid); running {
		a.setLastError(gid, &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "process_running", "game": string(gid)},
		})
		return nil
	}
}
```

(Note: `gameExeName(p, gid)` helper may now be unused if it had only this one call site. Check at end of step 4.3 with `grep -n "gameExeName" internal/app/update_handler.go`. If unused, mark for deletion in Step 4.7.)

Site at `update_handler.go:250` (inside `ApplyPredownload`) — same structural rewrite:
```go
// BEFORE:
if exeName, ok := gameExeName(p, gid); ok {
	if kurogames.IsProcessRunning(exeName) {
		// ... same setLastError + return ...
	}
}

// AFTER:
if pc, ok := p.(core.ProcessChecker); ok {
	if running, _ := pc.IsGameRunning(gid); running {
		// ... same setLastError + return ...
	}
}
```

Site at `update_handler.go:343` (inside `ResumeInterrupted`) — same structural rewrite. The exact `setLastError` body in ResumeInterrupted may differ from the StartUpdate one; preserve it verbatim, only swap the outer `if` predicates.

- [ ] **Step 4.4: Replace `kurogames.LoadProgress` / `LoadProgressFromPath` / `ReadWALETag` with `core.X`**

In `update_handler.go`, find each of these 4 sites and replace the package qualifier:

Line 467 area: `kurogames.LoadProgress(...)` → `core.LoadProgress(...)`
Line 471 area: `kurogames.LoadProgressFromPath(...)` → `core.LoadProgressFromPath(...)`
Line 476 area: `kurogames.ReadWALETag(...)` → `core.ReadWALETag(...)`
Line 738 area: `kurogames.LoadProgressFromPath(...)` → `core.LoadProgressFromPath(...)`

Quick verification grep:
```bash
grep -n "kurogames\.\(Load\|Read\|Scan\)" internal/app/update_handler.go
```
Expected: zero hits after this step.

- [ ] **Step 4.5: Replace `kurogames.ScanRecovery(...)` with `core.ScanRecovery(...)`**

Line 423 area: `kurogames.ScanRecovery(sidecarDir)` → `core.ScanRecovery(sidecarDir)`
Line 705 area: same.

Quick grep:
```bash
grep -n "kurogames\.ScanRecovery" internal/app/update_handler.go
```
Expected: zero hits.

- [ ] **Step 4.6: Replace `kurogames.RecoveryPhase*` const usage with `core.RecoveryPhase*`**

In `update_handler.go`, replace **every occurrence** of these symbols (6 sites at lines 441, 709, 722, 735, 766, 777):
- `kurogames.RecoveryNone` → `core.RecoveryNone`
- `kurogames.RecoveryPhaseDownloadResume` → `core.RecoveryPhaseDownloadResume`
- `kurogames.RecoveryPhaseApplyResume` → `core.RecoveryPhaseApplyResume`
- `kurogames.RecoveryPhasePredlAwaiting` → `core.RecoveryPhasePredlAwaiting`
- `kurogames.RecoveryCorrupt` → `core.RecoveryCorrupt`

Easiest with editor "replace all in file" of the package qualifier `kurogames.Recovery` → `core.Recovery`. (Verify no false matches by grepping after.)

Also if `update_handler.go` declared any local variable typed `kurogames.RecoveryState`, change to `core.RecoveryState`.

Quick verification:
```bash
grep -n "kurogames\." internal/app/update_handler.go
```
Expected: **zero hits**. (The two `"kurogames"` string literals at lines 299 + 671 will still exist after Task 5 — those are not `kurogames.` package refs and won't match this grep.)

- [ ] **Step 4.7: Delete `gameExeName` helper if now unused**

```bash
grep -n "gameExeName" internal/app/
```

If only the function definition survives (no callers), delete the function definition + its godoc. Otherwise leave it.

- [ ] **Step 4.8: Run `go build ./...`**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go build ./...
```

Expected: silent (exit 0). The `update_handler.go` should now compile clean. The `kurogamesTempDir` helper at `update_handler.go:560` and its 4 call sites are still in their original form — Task 5 fixes them.

- [ ] **Step 4.9: Run `go test ./...` and verify**

```bash
go test ./... 2>&1 | tee /tmp/m3r-task4-tests.txt
grep -c "^ok " /tmp/m3r-task4-tests.txt
```

Expected: same number as Step 1.1's baseline. All GREEN.

- [ ] **Step 4.10: Commit (bundles Task 3 + Task 4 staged changes)**

```bash
git add internal/app/update_handler.go
git commit -m "refactor(m3-refactor): introduce core.ProcessChecker + retire kurogames.IsProcessRunning + replace sidecar/recovery refs in update_handler"
```

(Task 3's staged changes — `internal/core/process_checker.go` + `internal/providers/kurogames/kurogames.go` — are committed together with Task 4's changes by this `git add`. Verify with `git status` before commit that the staged set includes all 3 files.)

---

## Task 5: Rename `kurogamesTempDir` → `tempDirFor(backend, gid)`; update 5 call sites

**Goal:** Generalise the per-backend tempRoot resolver. v0.3.1 keeps the kurogames-only switch arm; M3.B / M3.C will add their own cases. `scanForRecovery` stays single-rooted (multi-walk deferred to M3.B).

**Files:**
- Modify: `internal/app/update_handler.go` (delete the local `kurogamesTempDir` helper at lines 560–566; update 4 call sites at 131, 299, 420, 671)
- Modify: `internal/app/app.go` (add `tempDirFor` method; update 1 call site at 296)

- [ ] **Step 5.1: Add `tempDirFor` method to `internal/app/app.go`**

Open `internal/app/app.go`. The file already imports `"launcher-collection-tmp/internal/providers/kurogames"` for provider construction (line 16). Add the following method to the `App` type (place it next to other `App` settings-aware helpers, e.g. near `constructProviders` or at end of file):

```go
// tempDirFor resolves the per-backend temp root for sidecar/staging files.
// In v0.3.1 only kurogames has a configurable TempDir; hoyoverse / hypergryph
// cases will be added in M3.B / M3.C alongside their respective settings
// fields. The default branch is currently unreachable in production (no
// non-kurogames caller exists yet) but exists so future cases can be added
// without modifying call sites.
//
// Bit-exact preservation for kurogames: returns the same value as the legacy
// kurogamesTempDir helper — settings-override OR <TEMP>/launcher-collection
// (no backend/gid suffix; per-game flattening happens inside progressStore).
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	switch backend {
	case kurogames.BackendID:
		if td := a.settings.Backends.Kurogames.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "launcher-collection")
	}
	// Default for backends without a settings TempDir field: per-backend subdir
	// to avoid collisions. Unreachable in v0.3.1.
	return filepath.Join(osTempDir(), "launcher-collection", string(backend))
}
```

If `app.go` doesn't already import `"path/filepath"`, add it. The `osTempDir()` symbol is the package-level wrapper from `update_handler.go` — it's accessible since both files are in `package app`.

- [ ] **Step 5.2: Delete the legacy `kurogamesTempDir` helper from `update_handler.go`**

Open `internal/app/update_handler.go`. Locate the helper (around lines 560–566 pre-refactor):

```go
// DELETE:
func (a *App) kurogamesTempDir(gid core.GameID) string {
	td := a.settings.Backends.Kurogames.TempDir
	if td == "" {
		return filepath.Join(osTempDir(), "launcher-collection")
	}
	return td
}
```

- [ ] **Step 5.3: Update the 4 `kurogamesTempDir` call sites in `update_handler.go`**

Pre-refactor sites: lines 131, 299, 420, 671.

Site 1 — `update_handler.go:131` (inside `runStartUpdateAsync`, has `p core.Provider` parameter in scope):
```go
// BEFORE:
tempDir := a.kurogamesTempDir(gid)

// AFTER:
tempDir := a.tempDirFor(p.ID(), gid)
```

Site 2 — `update_handler.go:299` (inside `RemovePredownload`, NO Provider in scope; this is a string-literal anchor for v0.3.1):
```go
// BEFORE:
tempDir := a.kurogamesTempDir(gid)

// AFTER:
tempDir := a.tempDirFor("kurogames", gid)
```

Site 3 — `update_handler.go:420` (inside `runResumeAsync`, has `p core.Provider` in scope):
```go
// BEFORE:
tempRoot := a.kurogamesTempDir(gid)

// AFTER:
tempRoot := a.tempDirFor(p.ID(), gid)
```

Site 4 — `update_handler.go:671` (inside `scanForRecovery`, single-rooted v0.3.1 anchor):
```go
// BEFORE:
tempRoot := a.kurogamesTempDir("") // empty gid: returns settings.TempDir or default root

// AFTER:
tempRoot := a.tempDirFor("kurogames", "") // v0.3.1: single-rooted; multi-walk deferred to M3.B
```

- [ ] **Step 5.4: Update the 1 call site in `app.go`**

`app.go:296` (inside `RefreshVersion` phantom-predl cleanup):
```go
// BEFORE:
tempDir := a.kurogamesTempDir(gid)

// AFTER (kurogames.BackendID is reachable in app.go via the existing import):
tempDir := a.tempDirFor(kurogames.BackendID, gid)
```

- [ ] **Step 5.5: Run `go build ./...`**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go build ./...
```

Expected: silent (exit 0). If `kurogamesTempDir` is referenced anywhere not yet edited (e.g., a test file that escaped earlier audits), `go build` flags it.

- [ ] **Step 5.6: Final whole-repo grep — ensure 2-line target hit**

```bash
git grep -n "kurogames\." internal/app/
```

Expected exactly 2 lines:
```
internal/app/app.go:90:	kuro := kurogames.New(
internal/app/app.go:91:		kurogames.Settings{
```

(The line numbers may differ slightly post-edits if `app.go` got new content; the **count** is what matters: 2.)

If extra hits: investigate. Likely a Step 4 or 5 leftover.

Also verify the `app.go:16` import is still there (kept for provider construction):
```bash
grep -n "providers/kurogames" internal/app/app.go
```
Expected: `internal/app/app.go:16:	"launcher-collection-tmp/internal/providers/kurogames"` (1 line).

And confirm `update_handler.go` no longer imports kurogames:
```bash
grep -n "providers/kurogames" internal/app/update_handler.go
```
Expected: zero lines.

- [ ] **Step 5.7: Run `go test ./...`**

```bash
go test ./... 2>&1 | tee /tmp/m3r-task5-tests.txt
grep -c "^ok " /tmp/m3r-task5-tests.txt
```

Expected: same `ok` count as Step 1.1's baseline. All GREEN.

- [ ] **Step 5.8: Add unit test for the unreachable default branch**

Open `internal/app/app_test.go`. Add a test (place near other helper tests):

```go
func TestTempDirFor_DefaultBranch(t *testing.T) {
	a := newAppForTest(t /* whatever fakeProvider args the test helper expects */)
	got := a.tempDirFor("nonexistent-backend", "any/game")
	want := filepath.Join(osTempDir(), "launcher-collection", "nonexistent-backend")
	if got != want {
		t.Errorf("tempDirFor default = %q, want %q", got, want)
	}
}

func TestTempDirFor_KurogamesNoSettings(t *testing.T) {
	a := newAppForTest(t /* helper signature TBD */)
	got := a.tempDirFor("kurogames", "kurogames/wuwa")
	want := filepath.Join(osTempDir(), "launcher-collection")
	if got != want {
		t.Errorf("tempDirFor kurogames default = %q, want %q", got, want)
	}
}
```

(`newAppForTest`'s exact signature is in `app_test.go`; match it. The implementer should read that helper's existing signature and adapt — these tests' point is just to exercise the two `tempDirFor` arms. Adjust `t.TempDir()`-style construction if the existing helper expects a setup struct.)

If the test helper makes adding these tests painful (e.g., requires settings setup that fights the kurogames-default arm), it's acceptable to add a single combined test instead — but the **default branch must be exercised** (per spec §1.6).

```bash
go test -run "TestTempDirFor" ./internal/app/ -v
```
Expected: 2 (or 1 combined) tests PASS.

- [ ] **Step 5.9: Commit**

```bash
git add internal/app/update_handler.go internal/app/app.go internal/app/app_test.go
git commit -m "refactor(m3-refactor): rename kurogamesTempDir to tempDirFor(backend, gid); single-rooted scan retained"
```

---

## Task 6: Final verification — `go test ./...`, `go vet ./...`, `wails build`, manual 7-point WuWa smoke

**Goal:** Ship gate. Confirm no test regressions, build produces working binary, and manual smoke exercises the 7 core-flow points to verify zero behavior change in production.

**Files:** None modified — verification only.

- [ ] **Step 6.1: `go test ./...` final check (count, GREEN)**

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
go test ./... 2>&1 | tee /tmp/m3r-final-tests.txt
grep -c "^ok " /tmp/m3r-final-tests.txt
```

Expected: same `ok` count as Step 1.1's baseline (`docs/superpowers/research/m3-refactor-baseline-tests.txt`). All packages GREEN. No FAIL or `--- FAIL`.

```bash
diff <(grep "^ok\|^FAIL\|^---" /tmp/m3r-final-tests.txt | awk '{print $1, $2}') \
     <(grep "^ok\|^FAIL\|^---" docs/superpowers/research/m3-refactor-baseline-tests.txt | awk '{print $1, $2}')
```

Expected: no diff (or only pure addition for the new `core/recovery_test.go` + `core/sidecar_test.go` package — `internal/core` should now appear in the list with more time spent).

- [ ] **Step 6.2: `go vet ./...` clean**

```bash
go vet ./...
```

Expected: silent (exit 0). No new vet warnings introduced by the refactor.

- [ ] **Step 6.3: `wails build` produces binary**

```bash
/c/Users/willie/go/bin/wails.exe build -nosyncgomod 2>&1 | tail -10
ls -la build/bin/launcher-collection.exe
```

Expected: `Built '...launcher-collection.exe' in N.NNNs.` line at end of output. Binary timestamp updated. Size ~12.2-12.3 MB.

- [ ] **Step 6.4: Manual 7-point smoke — back up production state first**

Inform the user it's smoke time. The user must:

1. Close any running launcher-collection / HoYoPlay / KRLauncher instance
2. Back up `C:/Program Files/Wuthering Waves/Wuthering Waves Game/launcherDownloadConfig.json` to e.g. `C:/Users/willie/Desktop/launcherDownloadConfig.backup.json` so Step 6.5 #2 can fake a stale version safely.

Also recommend backing up `<TEMP>/launcher-collection/<gid>/` directories if any in-progress sidecars exist (`ls "$LOCALAPPDATA\Temp\launcher-collection"`).

- [ ] **Step 6.5: Run the 7-point smoke checklist**

The user runs `build/bin/launcher-collection.exe` and walks through:

1. **WuWa already at latest version** → `[開始遊戲]` button visible, no `[更新]`. PASS criterion: button text matches expected (zh-TW: `開始遊戲`).
2. Edit `launcherDownloadConfig.json` (now backed up) — set `"version": "3.0.0"` — close it. In launcher click Refresh (Topbar). `[更新遊戲]` button appears. PASS criterion: BottomBar updates within ~30s.
3. Click `[更新遊戲]` → progress bar fills. PASS criterion: % visible, advances within ~30s for first chunk.
4. Click × (cancel) → state returns to `[更新遊戲]`. PASS criterion: temp dir cleaned (`<TEMP>/launcher-collection/kurogames-wuwa/<version>/` empty or removed), no LastError surfaced.
5. Re-click `[更新遊戲]` → download → apply → completes → `[開始遊戲]` returns. PASS criterion: full flow completes; `launcherDownloadConfig.json` rewritten with target version.
6. Toggle zh-TW ↔ en (gear icon top-right). PASS criterion: every visible string switches; no missing-key fallback (`update.*` keys all render).
7. Close app mid-download (kill process) → reopen launcher → bell icon highlights / drawer prompts "上次更新中斷，是否繼續？". PASS criterion: prompt appears within ~5s of relaunch; OK button resumes from sidecar.

If all 7 PASS → ship-ready. If any fail → log the failure mode + which task likely introduced it; bisect with `git log --oneline m3-refactor` and re-run from that task.

- [ ] **Step 6.6: Restore the backup**

```
copy "C:/Users/willie/Desktop/launcherDownloadConfig.backup.json" "C:/Program Files/Wuthering Waves/Wuthering Waves Game/launcherDownloadConfig.json"
```

(Requires admin if WuWa is in Program Files. The user runs this themselves; the agent confirms.)

- [ ] **Step 6.7: Tag v0.3.1 + merge to main + update memory**

After all 7 smoke points pass:

```bash
# Tag the m3-refactor branch tip
git tag -a v0.3.1 -m "m3-refactor — extract app/kurogames coupling for M3.B/C plug-in"

# Merge to main with --no-ff per memory feedback_commits.md
git checkout main
git merge --no-ff m3-refactor -m "merge: m3-refactor — extract app/kurogames coupling for M3.B/C plug-in"

# Verify
git log --oneline --graph -10
git tag -l v0.3.1

# Final whole-repo check
go test ./...
go build ./...
```

Expected: clean merge commit on main; tag points to the m3-refactor tip.

After merge, update `memory/project_status.md` to mark m3-refactor SHIPPED with the merge commit SHA. Branch `m3-refactor` is preserved per convention (do not delete).

- [ ] **Step 6.8: Update memory**

Edit `C:\Users\willie\.claude\projects\C--Users-willie-Repos-launcher-collection\memory\project_status.md`. Add a new section above the M3.A summary noting m3-refactor SHIPPED with:
- Tag `v0.3.1`
- Merge SHA (from `git rev-parse HEAD` after the merge)
- 6-task summary
- Pointer to spec + plan paths
- "M3.B HoYoverse brainstorming opens fresh from clean main tree."

This unblocks the user-facing handoff documented in spec §6.

---

## Self-review

**Spec coverage:** Spec §1.1 (move types) → Tasks 1+2. §1.2 (progressStore stays) → Task 1 step 1.5 + Task 2 step 2.5. §1.2.1 (intra-kurogames rewrites) → Task 2 steps 2.5/2.6/2.7/2.8. §1.2.2 (delete public IsProcessRunning) → Task 3 step 3.4. §1.3 (ProcessChecker iface) → Task 3 step 3.1. §1.4 (Provider.IsGameRunning impl) → Task 3 step 3.2. §1.5 (call-site replacements) → Task 4 steps 4.1–4.7. §1.5.1 (line-38 string change) → Task 4 step 4.2. §1.6 (tempDirFor) → Task 5 steps 5.1–5.4 + 5.8. §1.7 (scanForRecovery single-rooted) → Task 5 step 5.3 site-4 (literal anchor). §3.1 / §3.1.1 (baseline) → Task 1 step 1.1. §3.1.2 (test migration map) → Task 2 steps 2.3/2.4/2.8. §3.2 (7-point smoke) → Task 6 steps 6.4–6.6. §5 (ship sequence) → Task 6 step 6.7+6.8.

**Placeholder scan:** Step 5.8's `newAppForTest(t /* whatever fakeProvider args the test helper expects */)` is the closest to a placeholder — kept intentionally because `app_test.go`'s existing signature is the source of truth (subagent reads that file and adapts). All other steps have complete code or exact commands.

**Type consistency:** `tempDirFor(backend core.BackendID, gid core.GameID) string` consistent across all 5 call sites (Step 5.1 def, Steps 5.3.1–5.3.4 + 5.4 calls). `Provider.IsGameRunning(gid core.GameID) (bool, error)` consistent (Step 3.1 iface, Step 3.2 impl, Step 4.3 caller). `core.ProgressFile` / `core.ProgressEntry` types consistent (Step 1.5 progressStore retype, Step 2.5 caller retype). `core.RecoveryPhase*` const names match the iota order (Step 1.3 def, Step 4.6 caller list).

---

## Risks recap (from spec §4)

| Risk | Mitigation |
|---|---|
| Missed kurogames intra-package ref breaks compile | Task 5 step 5.6 grep assertion (exactly 2 lines in `internal/app/`) |
| Test coverage drops because tests in wrong package | Task 2 step 2.10 explicit count check vs baseline |
| `Provider.IsGameRunning` doesn't compose cleanly | Task 3 step 3.2 + 3.5 build check |
| 5th `kurogamesTempDir` site missed | Task 5 step 5.4 (`app.go:296`) explicit |
| Future M3.B sidecar layout diverges from core schema | Spec lock — sidecar convention is the contract; M3.B spec must honor it |

---

## Execution handoff

After this plan is committed alongside the spec, two execution options:

1. **Subagent-Driven (recommended)** — fresh haiku implementer per task; spec-reviewer + code-quality-reviewer between tasks; same pattern that worked for M3.A Tasks 9–17.
2. **Inline Execution** — execute tasks in this session using `superpowers:executing-plans`; batch execution with checkpoints.

User must explicitly authorize the cadence (per `memory/feedback_autonomous_m3a_batch.md` precedent — batch autonomy is task-scoped and must be re-granted).
