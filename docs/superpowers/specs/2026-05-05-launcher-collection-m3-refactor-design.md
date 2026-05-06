# m3-refactor — Extract app/kurogames coupling for M3.B/C plug-in

**Status:** DRAFT — 2026-05-05
**Target ship:** tag `v0.3.1` (mini-milestone between M3.A and M3.B)
**Branch:** `m3-refactor`

## Background

M3.A SHIPPED `internal/app/update_handler.go` (~800 LoC) with provider-specific coupling to the `kurogames` package. M3.B (HoYoverse) and M3.C (Hypergryph) need clean abstraction points to plug in their `Updater` implementations without `if/else` dispatch in the app layer.

This refactor is **pure** — zero behavior change. M3.A WuWa update flow must continue working byte-for-byte after merge.

## Scope

### In-scope
- Move generic types/funcs from `internal/providers/kurogames/update_progress.go` to `internal/core/`
- Introduce one new optional interface (`core.ProcessChecker`) for game-running detection
- Replace all hardcoded `kurogames.X` references in `internal/app/update_handler.go` with abstractions
- Generalize `a.kurogamesTempDir(gid)` → `a.tempDirFor(backend, gid)`

### Out-of-scope
- No publisher protocol logic (no fetchPackages / hpatchz / etc. — those are M3.B)
- No frontend / settings.toml schema changes
- No new logging or observability
- No multi-backend `scanForRecovery` walk (defer to M3.B when 2nd `tempRoot` exists)
- No `RecoveryScanner` / `SidecarReader` interfaces (YAGNI; sidecar convention is shared)
- No `HoyoverseSettings.TempDir` / `HypergryphSettings.TempDir` (those land with M3.B / M3.C)

## Current coupling map

`internal/app/update_handler.go` direct references to `kurogames.*` (verified at branch entry):

| Symbol | Sites |
|---|---|
| `import "...kurogames"` | line 13 |
| Hardcoded error msg `"M3.A: only kurogames"` | line 38 |
| `kurogames.IsProcessRunning(exeName)` | lines 46, 250, 343 |
| `kurogames.ScanRecovery(...)` | lines 423, 705 |
| `kurogames.LoadProgress` / `LoadProgressFromPath` / `ReadWALETag` | lines 467, 471, 476, 738 |
| `kurogames.RecoveryPhase*` const | lines 441, 709, 722, 735, 766, 777 |
| `a.kurogamesTempDir(gid)` (helper) | lines 131, 299, 420, 671 |

Plus `internal/app/app.go:296` (5th `kurogamesTempDir` call site, in `RefreshVersion` phantom-predl cleanup). The `kurogames` import in `app.go:16` (used for provider construction at lines 90–95) stays — that's not coupling, that's the registry.

## §1 Design

### §1.1 Move to `internal/core/`

**`internal/core/progress.go`** — types
- `ProgressFile struct` (fields: `GameID`, `Version`, `ETag`, `Entries map[string]ProgressEntry`)
- `ProgressEntry struct` (fields: `Size`, `MTime`, `Hash`)

**`internal/core/recovery.go`** — recovery surface
- `RecoveryPhase int` enum + 5 const: `RecoveryNone`, `RecoveryPhaseDownloadResume`, `RecoveryPhaseApplyResume`, `RecoveryPhasePredlAwaiting`, `RecoveryCorrupt`
- `RecoveryState struct` (fields: `Phase`, `WasPredl`, `Err`)
- `ScanRecovery(dir string) RecoveryState` function

**`internal/core/sidecar.go`** — sidecar I/O
- `LoadProgress(dir string) (*ProgressFile, error)`
- `LoadProgressFromPath(path string) (*ProgressFile, error)`
- `ReadWALETag(path string) string`
- (private) `loadProgressFile(path string)` — used by both `LoadProgress` and `ScanRecovery`
- (private) `fileExists(path string) bool` — used by `ScanRecovery`

Source: all from current `internal/providers/kurogames/update_progress.go:18-237`, except `progressStore` struct + writers (those stay).

### §1.2 Stays in `kurogames` package

- `progressStore` struct with `mu sync.Mutex`, `tempRoot`, `gameID`, `version`
- Methods: `Init`, `MarkComplete`, `RenameToPredlReady`, `writeAtomic`
- Body refactored to consume `core.ProgressFile` / `core.ProgressEntry` instead of own types

Each provider writes its own sidecar; reading is shared via core. M3.B will mirror this pattern: hoyoverse will have its own writer (`hoyoverse.progressStore` or similar) but use `core.ScanRecovery` / `core.LoadProgress` for reads.

### §1.2.1 Intra-kurogames rewrites (mechanical)

Moving the types/funcs out forces same-package callers to switch to `core.*`:

| Site | Before | After |
|---|---|---|
| `update_progress.go:58, :62, :78` | construct `ProgressFile{}` / `ProgressEntry{}` literals | `core.ProgressFile{}` / `core.ProgressEntry{}` |
| `update_progress.go:74` | `loadProgressFile(...)` (private call from `MarkComplete`) | `core.LoadProgressFromPath(...)` |
| `update_download.go:68, :129` | `LoadProgress(d.progress.dir())` | `core.LoadProgress(...)` |
| `update_load_test.go:99` (build-tagged) | `loadProgressFile(progressPath)` | `core.LoadProgressFromPath(progressPath)` |
| `update_progress_test.go` (multiple) | direct refs to moved types/funcs | `core.*` (see §3.3 test migration) |

These are part of Tasks 1+2; the implementer must edit them as part of the move so kurogames keeps compiling step-by-step.

### §1.2.2 Dead-code delete after Task 4

One function in `kurogames/kurogames.go` is dead post-refactor and must be deleted alongside Task 4:

- `IsProcessRunning(exeName string) bool` (kurogames.go:379-381, **public**) — only 3 production callers (the 3 `update_handler.go` sites being replaced); replaced by `Provider.IsGameRunning(gid)`.

The **private** `isProcessRunning` (kurogames.go:373-375) **stays** — it has a remaining caller at `kurogames.go:305` inside `Provider.RunUpdate`'s in-package game-running guard, which is out of scope for this refactor. After Task 4, the only public process-checking surface is the new `Provider.IsGameRunning(gid)` method.

Build-tag-gated `platformIsProcessRunning` (private) survives unchanged; both `Provider.IsGameRunning` and the surviving `isProcessRunning` (used by `RunUpdate`) call it directly.

(Verified at branch entry: no test files in `internal/providers/kurogames/` directly reference `IsProcessRunning` or `isProcessRunning` — nothing to migrate or delete on the test side.)

### §1.3 New interface: `core.ProcessChecker`

`internal/core/process_checker.go`:
```go
package core

// ProcessChecker is an optional capability: providers that can detect whether
// their game is currently running implement this. Callers type-assert
// (mirrors core.ExeNamer / core.CheckForUpdateProgress / core.PathProvider).
type ProcessChecker interface {
    IsGameRunning(gid GameID) (bool, error)
}
```

### §1.4 Kurogames `Provider` impl

In `internal/providers/kurogames/kurogames.go` (new method):

```go
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
    exe, ok := p.ExeName(gid)         // existing core.ExeNamer impl
    if !ok {
        return false, nil
    }
    return platformIsProcessRunning(exe), nil  // existing process_check_windows.go
}

// compile-time check
var _ core.ProcessChecker = (*Provider)(nil)
```

`platformIsProcessRunning` is the build-tag-gated function inside `process_check_{windows,other}.go`; the existing public `IsProcessRunning` package func is dead post-refactor and deleted (see §1.2.2). The private `isProcessRunning` survives — still used by `RunUpdate`'s in-package guard at `kurogames.go:305`.

### §1.5 `update_handler.go` call-site replacements

| Site | Before | After |
|---|---|---|
| `:13` | `import "...providers/kurogames"` | (delete; not needed) |
| `:38` | error msg `"... (M3.A: only kurogames)"` | `"provider %s does not support updates"` (see §1.5.1 — deliberate string change, only non-byte-exact delta of this refactor) |
| `:46, :250, :343` | `if kurogames.IsProcessRunning(exeName) { ... }` | `if pc, ok := p.(core.ProcessChecker); ok { running, _ := pc.IsGameRunning(gid); if running { ... } }` |
| `:423, :705` | `kurogames.ScanRecovery(...)` | `core.ScanRecovery(...)` |
| `:467, :471, :476, :738` | `kurogames.LoadProgress` / `...FromPath` / `ReadWALETag` | `core.LoadProgress` / `...FromPath` / `ReadWALETag` |
| `:441, :709, :722, :735, :766, :777` | `kurogames.RecoveryPhase*` | `core.RecoveryPhase*` |

Behavior: if a provider does **not** implement `core.ProcessChecker` (e.g., today's hoyoverse / hypergryph providers), the guard is skipped — same as current behavior (M3.A only ever ran the guard for kurogames).

### §1.5.1 Non-byte-exact delta (intentional)

This refactor is **otherwise** pure (byte-exact behavior preserved). The single intentional drift is the line 38 error message:

- Before: `"provider %s does not support updates (M3.A: only kurogames)"`
- After: `"provider %s does not support updates"`

Rationale: the parenthetical becomes false the moment M3.B lands; trimming it now (vs. on the M3.B PR) keeps the M3.B diff focused on protocol code. Verified at branch entry that no test asserts on the parenthetical (`update_handler_test.go` substring matches use only the leading clause). All other changes preserve identical strings, return values, file formats, and side effects.

### §1.6 `tempDirFor` abstraction

Replace `a.kurogamesTempDir(gid core.GameID) string` with:
```go
// in internal/app/app.go (alongside other settings-aware helpers; app.go already imports kurogames)
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
    switch backend {
    case kurogames.BackendID:
        if td := a.settings.Backends.Kurogames.TempDir; td != "" {
            return td
        }
        return filepath.Join(osTempDir(), "launcher-collection")
    }
    // hoyoverse / hypergryph cases added by M3.B / M3.C.
    // Default for unknown backends: per-backend subdir to avoid collisions.
    return filepath.Join(osTempDir(), "launcher-collection", string(backend))
}
```

**Bit-exact preservation for kurogames:** the existing helper at `update_handler.go:560-566` ignores `gid` and returns `<TEMP>/launcher-collection` (no backend/gid suffix) when `Backends.Kurogames.TempDir == ""`. The new `tempDirFor("kurogames", gid)` must return the identical value to keep behavior unchanged. The `gid` parameter stays in the signature for parity and future use; for the kurogames case it remains unused (per-game flattening happens inside `progressStore.dir()`, not here).

`osTempDir()` is the existing package-level wrapper around `os.TempDir()` (allows test injection); reuse it.

**Default-branch reachability in v0.3.1:** the `default` return path with `<backend>` subdir is **unreachable** by production code in v0.3.1 (only kurogames calls `tempDirFor`, which falls into the `case "kurogames":` arm). It exists so M3.B / M3.C can wire their own `case`s by addition rather than modification. A unit test in `app_test.go` should exercise the default branch with a fabricated backend ID to ensure it doesn't regress.

**Where this lives:** put `tempDirFor` in `internal/app/app.go` rather than `update_handler.go`, because `app.go` already imports `kurogames` (for provider construction at lines 90-91), so the `switch` arm using the `kurogames.BackendID` named constant (`internal/providers/kurogames/meta.go:6`) is free. This avoids stringly-typed `"kurogames"` literals and makes the constant's drift safer.

The signature change makes the backend explicit; future M3.B / M3.C add their `case` without touching call sites.

5 call sites switch:
- `update_handler.go:131, 420` → `a.tempDirFor(p.ID(), gid)` (these sites have `p, err := a.provider(gid)` already in scope; using `p.ID()` avoids re-importing kurogames into update_handler.go, which is a refactor goal)
- `update_handler.go:299` (inside `RemovePredownload`) → `a.tempDirFor("kurogames", gid)` string literal — `RemovePredownload` does NOT look up the Provider (it only takes `gameID string`); rather than fetch the provider just to derive its ID, anchor with literal "kurogames". This is the 1st of 2 documented v0.3.1 anchors.
- `update_handler.go:671` (inside `scanForRecovery`) → `a.tempDirFor("kurogames", "")` literal — single-rooted v0.3.1 walk; M3.B will replace with iteration over registered backends. 2nd v0.3.1 anchor.
- `app.go:296` → `a.tempDirFor(kurogames.BackendID, gid)` (app.go imports kurogames for provider construction; using the constant is free here)

The asymmetry is intentional:
- Inside `tempDirFor` definition and at `app.go:296`: use the named constant `kurogames.BackendID` (no extra cost — kurogames already imported).
- At update_handler.go sites with Provider in scope (`:131`, `:420`): use `p.ID()` to keep the import-list shrunk.
- At update_handler.go sites without Provider in scope (`:299`, `:671`): use string literal `"kurogames"`. These two literals are the documented v0.3.1 anchors; M3.B will revisit each (`:299` may fetch the Provider; `:671` becomes the multi-walk).

### §1.7 `scanForRecovery` left single-rooted (deferred multi-walk)

`scanForRecovery` continues to walk the single kurogames tempRoot. Multi-backend walk is deferred to M3.B (when `Backends.Hoyoverse.TempDir` exists and there's actually a 2nd tree to scan).

The `tempRoot` lookup inside switches from `a.kurogamesTempDir("")` to `a.tempDirFor("kurogames", "")` — same value, new function name.

## §2 Implementation tasks

Plan-task granularity (writing-plans skill will expand each):

| # | Task | Risk | Tests |
|---|---|---|---|
| 1 | Move types to core: `ProgressFile`, `ProgressEntry`, `RecoveryPhase` enum, `RecoveryState`; move private `loadProgressFile`, `fileExists` | Low | New `core/recovery_test.go` + `core/sidecar_test.go`; relevant tests migrated from `kurogames/update_progress_test.go` |
| 2 | Move funcs to core: `ScanRecovery`, `LoadProgress`, `LoadProgressFromPath`, `ReadWALETag` | Low | Bundled with Task 1 test migration |
| 3 | New `core.ProcessChecker` iface + kurogames `Provider.IsGameRunning(gid)` impl + compile-time check | Low | None (type-assertion based; fakes don't need new method) |
| 4 | `update_handler.go`: replace all kurogames sidecar/recovery refs with `core.*`; 3 sites swap to `ProcessChecker` type-assert; update line 38 error msg | Medium | `update_handler_test.go` may need `IsGameRunning` on a fake if a path exercises it |
| 5 | Rename `kurogamesTempDir` → `tempDirFor(backend, gid)`; update 5 call sites (`update_handler.go:131, 299, 420, 671` + `app.go:296`); `scanForRecovery` switches helper but stays single-rooted | Medium | Existing tests should pass unchanged |
| 6 | Verify: `go test ./...` GREEN; `go vet ./...` clean; `wails build` produces working binary; 7-point manual smoke on WuWa update flow | Medium | n/a (verification only) |

**Sequencing:** Tasks 1, 2, 3 can run in parallel (independent file moves + new file). Task 4 depends on 1, 2, 3. Task 5 depends on 4 (avoid call-site conflicts). Task 6 is the final gate. The plan-writing skill may collapse 1+2 into a single mechanical move-and-test-migration task.

## §3 Verification

### §3.1 Behavior invariance

At branch entry (before any task), capture baseline:
```
go test ./... 2>&1 | tee docs/superpowers/research/m3-refactor-baseline-tests.txt
```

After each task and at end of task 6, re-run and compare:
- Same total test count (or higher if new tests added in core; never lower)
- All GREEN
- No new `go vet` warnings

`go build ./...` and `wails build` must succeed without warning regressions.

### §3.1.1 Baseline test count

Capture **once** at branch entry (before any task), and use that fixed number as the comparison anchor for every subsequent task verification:

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"  # per memory feedback_no_cgo_race.md sibling
go test ./... 2>&1 | tee docs/superpowers/research/m3-refactor-baseline-tests.txt
```

The baseline file is committed alongside spec so reviewers can see the pre-refactor snapshot. Post-refactor (after Task 6) total test count must be **>= baseline** with all GREEN; the increase comes from `core/recovery_test.go` + `core/sidecar_test.go` (new test files holding migrated tests). No test moves to the `internal/app` namespace.

### §3.1.2 Test migration map (per Task 1+2 implementer prompt)

Tests from `internal/providers/kurogames/update_progress_test.go` split as follows:

| Test fn | Tests what | Destination |
|---|---|---|
| `TestProgress_LoadCorruptReturnsErr` (or equivalent) | `LoadProgress` parse error | `core/sidecar_test.go` |
| `TestProgress_RecoveryScan_*` (any case names) | `ScanRecovery` + `RecoveryPhase*` outcomes | `core/recovery_test.go` |
| `TestProgress_WriteAndLoadEntry` (or equivalent) | `progressStore.Init` / `MarkComplete` round-trip | **stay** in `kurogames/update_progress_test.go` (exercises kurogames-internal `progressStore` + uses `core.ProgressFile` from migration) |
| `TestProgress_AtomicWrite` / `TestProgress_RenameToPredlReady` | `progressStore` write side | **stay** in kurogames |
| Any test directly calling `loadProgressFile` (private) | private helper | **migrate** to `core/sidecar_test.go` (testing `LoadProgressFromPath` instead, since `loadProgressFile` becomes private to `core` after move) |
| `update_load_test.go` (build-tagged `load`) | `progressStore` benchmark | **stay** in kurogames; replace its single `loadProgressFile(progressPath)` call with `core.LoadProgressFromPath(progressPath)` |

Test names above are illustrative; implementer reads the actual file at branch entry and applies the same split logic. The split rule is: **tests for moved symbols move; tests for kurogames-only types (`progressStore`) stay.**

### §3.2 7-point manual smoke (from M3.A spec §7.10, abridged)

Run `build/bin/launcher-collection.exe` against real WuWa install (back up `C:/Program Files/Wuthering Waves/Wuthering Waves Game/launcherDownloadConfig.json` first):

1. WuWa already at latest version → `[開始遊戲]` visible, no `[更新]`
2. Edit `launcherDownloadConfig.json` to set version to `3.0.0` → close it → in-launcher Refresh → `[更新]` appears
3. Click `[更新]` → progress bar fills (download phase, % visible)
4. Click × (cancel) → state returns to `[更新]`, temp dir cleaned
5. Re-click `[更新]` → download → apply → completes → `[開始遊戲]` returns
6. Toggle zh-TW ↔ en → all update strings render (no missing-key fallback)
7. Close app mid-download → reopen → "上次更新中斷" prompt appears

Restore backed-up `launcherDownloadConfig.json` after smoke.

If all 7 pass → ship. If any fails → patch (subagent-driven fix loop) and re-smoke that point.

## §4 Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Missed kurogames internal ref breaks compile | Medium | Compiler-driven. Concrete assertion after Task 5: `git grep -n "kurogames\." internal/app/` returns exactly **2 lines** — `app.go:90` (`kurogames.New`), `app.go:91` (`kurogames.Settings`). The `app.go:16` import line is `"…/internal/providers/kurogames"` (path segment `kurogames` has no trailing dot, so it does NOT match `kurogames\.`); verify separately by inspection. Plus `update_handler.go:299` and `:671` retain string literals `"kurogames"` (documented in §1.6 as v0.3.1 anchors; not `kurogames.` package refs, won't match grep). |
| `kurogames.update_progress_test.go` test split between core and kurogames misjudged | Low | Spec lists which symbols moved; tests for moved symbols move with them |
| `Provider.IsGameRunning` composition assumes `ExeName` implemented | Low | `ExeName` exists on kurogames Provider since M2 (`kurogames.go:151-157`) |
| `tempDirFor` non-kurogames default path differs from M3.B's eventual layout, causing dead-code branch maintenance | Low | M3.B can change the default branch when adding the hoyoverse case; v0.3.1's path is just a placeholder |
| Reviewer found 5 (not 4) `kurogamesTempDir` sites | Tracked | Spec § 1.6 lists all 5 |
| Future M3.B sidecar layout diverges from core's expected schema | Low | Sidecar convention IS the contract — M3.B spec must use same 3 filenames (`progress.json`, `apply.wal`, `predl_ready.json`) and same `ProgressFile` shape |

## §5 Ship sequence

1. Branch `m3-refactor` from `main` (already done at branch entry)
2. Capture baseline test count → `docs/superpowers/research/m3-refactor-baseline-tests.txt`
3. Execute tasks 1–5; verify per §3.1 after each
4. Run task 6 (full verify + 7-point smoke)
5. Tag `v0.3.1` on the final commit
6. `git checkout main && git merge --no-ff m3-refactor -m "merge: m3-refactor — extract app/kurogames coupling for M3.B/C plug-in"`
7. Update `memory/project_status.md` to mark m3-refactor SHIPPED with merge SHA
8. (Optional) Build production binary `build/bin/launcher-collection.exe` for archive
9. Branch `m3-refactor` preserved (do not delete)

After ship: M3.B HoYoverse brainstorming opens fresh from clean main tree.

## §6 Dependencies / handoff to M3.B

Once shipped, M3.B can:
- Implement `core.Updater` on hoyoverse `Provider` and it plugs into `update_handler.go` automatically
- Add `case "hoyoverse":` branch in `tempDirFor` once `HoyoverseSettings.TempDir` is added (M3.B settings change)
- Reuse `core.ProgressFile` / `core.ScanRecovery` for sidecar reads (no need to re-implement)
- Implement `core.ProcessChecker` on hoyoverse `Provider` if desired (otherwise game-running guard is skipped, same as today)
- Generalize `scanForRecovery` to multi-backend walk when adding a 2nd tempRoot (deferred from this refactor)
