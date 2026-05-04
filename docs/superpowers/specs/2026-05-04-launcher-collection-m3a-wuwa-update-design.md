# M3.A — WuWa Update Feature Design

**Date**: 2026-05-04
**Status**: Spec — pending implementation plan
**Scope**: Patch-update + predownload for Wuthering Waves (kurogames provider). No fresh-install. Single game. M3.B (HoYoverse update) and M3.C (hypergryph update) follow as separate sub-projects.

---

## 0. Goals & non-goals

### Goals

- Detect when WuWa has a newer version available (extends M2's `RefreshVersion`).
- Download the patch into a temp area without disturbing the running game install until apply.
- Apply the patch (atomic file moves into game dir) with the game closed.
- Predownload: same flow but skip the apply phase; persist the downloaded set for later application when the version is released.
- Show download / apply progress live in the BottomBar (per-game) and Sidebar (per-game).
- Survive app crash / network drop / disk-full mid-download (file-level resume).

### Non-goals (M3.A)

- Fresh install (game not yet installed). M4.
- Cross-game concurrent updates. M3.B+ (HoYoverse provider lands first; cross-provider concurrency is a refactor target).
- Byte-level resume (HTTP Range mid-file). M4.
- Pause / resume control beyond cancel + restart. M4.
- Bandwidth throttling, scheduled downloads. M4.
- Settings UI for TempDir / concurrency. M4.
- Auto-prompt-on-Refresh predownload. M4.
- Auto-retry policy customization. M4.

### Established M2 baseline this builds on

- `core.Provider` registry in `internal/app/app.go`.
- Optional interfaces (`PathProvider`, `ExeNamer`, `AssetServer`) in `internal/core/`.
- `RefreshVersion(gameID) → core.VersionInfo{Current, Latest, Predownload}` Wails RPC.
- `internal/providers/kurogames/` with `meta.go`, `detect.go`, `version.go`, `launch_windows.go`, `bg.go` (hybrid URL-from-cache).
- `slog` logging with `Frameless: true` Wails window + `assetMux` middleware mounted on `/_asset/`.
- Frontend Vue + Pinia + vue-i18n, `frontend/src/stores/games.ts` for pull-style state.
- Tests run via `go test ./...` + `go vet ./...`; whole-repo green required.

---

## 1. Architecture & package layout

### 1.1 Layout

```
internal/
  core/
    updater.go              new — Updater optional iface + UpdatePlan/UpdateEvent/UpdateError types
    provider.go             ← add PlanKind enum (PlanUpdate / PlanPredownload) and Phase enum (PhaseDownload / PhaseApply)
  providers/kurogames/
    update_manifest.go      manifest fetcher + parser (depends on §4 research output)
    update_manifest_test.go
    update_download.go      worker pool + per-file SHA verify
    update_download_test.go
    update_apply.go         temp → game atomic batch move; WAL handling
    update_apply_test.go
    update_progress.go      sidecar JSON resume store + UpdateEvent emission
    update_progress_test.go
    apply_lock_windows.go   golang.org/x/sys/windows.LockFileEx wrapper (//go:build windows)
    apply_lock_stub.go      no-op stub for non-Windows builds
    apply_lock_windows_test.go
    apply_lock_other_test.go
    update_integration_test.go      end-to-end: manifest → download → verify → apply
    kurogames.go            ← add (p *Provider) CheckForUpdate / RunUpdate methods (impl Updater iface)
  app/
    update_handler.go       Wails RPC: StartUpdate / StartPredownload / CancelInFlight / ApplyPredownload / RemovePredownload / DismissError / ResumeInterrupted / UpdateStatusAll
    update_handler_test.go
    update_state.go         per-game GameUpdateState + per-game context.CancelFunc map + Wails event emitter goroutine
    update_state_test.go
    settings.go             ← add KurogamesSettings.TempDir string (empty → os.TempDir()/launcher-collection/<gameID>)

frontend/
  src/
    stores/updates.ts                Pinia store mirroring per-game state via push events
    components/BottomBar.vue          ← modified: state-driven button matrix with progress fill + embedded [×]
    components/SidebarRow.vue         ← modified: 1px progress bar overlay
    components/ConfirmDialog.vue     new — Teleport + <dialog> + Promise<boolean>; replaces window.confirm()
    components/ToastHost.vue         new — Teleport-mounted, 5s auto-dismiss, sticky for retryable, 3-cap with "+N more"
    locales/en.json                  ← add update.* namespace
    locales/zh-TW.json               ← add update.* namespace
    locales/zh-CN.json               ← add update.* namespace (dormant but parity-tested)
```

Layout discipline: keep update logic in the kurogames package (flat file naming) for M3.A. Subpackage extraction deferred to M3.B once HoYoverse update reveals which pieces are actually shared.

### 1.2 Core types

```go
// internal/core/provider.go (additions)
type PlanKind int
const (
    PlanUpdate PlanKind = iota
    PlanPredownload
)

type Phase int
const (
    PhaseDownload Phase = iota
    PhaseApply
)

// internal/core/updater.go (new)
type Updater interface {
    // CheckForUpdate fetches the per-game manifest. Idempotent. Read-only.
    CheckForUpdate(ctx context.Context, gid GameID) (UpdatePlan, error)

    // RunUpdate executes a previously-checked plan. Emits progress via
    // onEvent (synchronous callback, not channel). Returns when the run
    // completes, fails, or ctx is cancelled.
    RunUpdate(ctx context.Context, plan UpdatePlan, onEvent func(UpdateEvent)) error
}

type UpdatePlan struct {
    GameID       GameID
    Kind         PlanKind        // Update | Predownload
    ManifestETag string          // re-checked at RunUpdate entry; mismatch → ErrManifestChanged
    Files        []FileTask      // already filtered: only files whose hash differs from current install
    TotalBytes   int64           // sum of Files[].Size
    Version      string          // human-readable version label, e.g. "3.4.0"
}

type FileTask struct {
    Path string  // relative to game install dir, e.g. "Wuthering Waves Game/foo/bar.dll"
    Hash string  // hex-encoded SHA-256 (or whatever §4 research determines)
    Size int64
    URL  string  // full CDN URL; sanitized before logging
}

type UpdateEvent struct {
    Phase       Phase     // Download | Apply
    Current     int64     // bytes done in Download phase, files-applied count in Apply phase
    Total       int64     // TotalBytes (Download) or len(plan.Files) (Apply)
    CurrentFile string    // optional, name of file in flight
}

type UpdateError struct {
    Code      string                 // see §6.1 catalog
    Params    map[string]string      // template substitution data; URLs sanitized
    Retryable bool
}

func (e *UpdateError) Error() string { return fmt.Sprintf("%s: %v", e.Code, e.Params) }
```

The interface is optional: kurogames implements it; hoyoverse / hypergryph providers do NOT in M3.A. App layer type-asserts at `RPC handler` entry.

### 1.2.1 Relationship to M2's `CheckVersion`

M2's `core.Provider.CheckVersion(ctx, gid) → VersionInfo{Current, Latest, Predownload}` is **kept unchanged**. It serves M2's sidebar version display via `App.RefreshVersion` and is cheap (no full manifest fetch). M3.A's `CheckForUpdate` is a separate, heavier call that fetches the full file manifest and returns a `UpdatePlan`. The two are independent:

- `App.RefreshVersion` (M2) keeps calling `p.CheckVersion` only.
- `App.StartUpdate / StartPredownload` (M3.A new) calls `p.(core.Updater).CheckForUpdate` to populate `state.AvailableUpdate / AvailablePredl`.
- Concurrent `RefreshVersion` and `StartUpdate` are safe: writes to `GameUpdateState` go through `mu`; the two RPCs hit different fields (`VersionInfo` is in `useGamesStore`, not `useUpdatesStore`).
- A separate "background CheckForUpdate to populate AvailableUpdate" can be added later (M4); for M3.A, `AvailableUpdate` is populated lazily on user click and the result cached on the snapshot.

### 1.2.2 `UpdateError` marshalling to frontend

Two paths, both structured (no rendered text from Go):

1. **Via state snapshot**: `GameUpdateState.LastError` carries the struct; `UpdateStatusAll` RPC returns it as JSON; frontend renders via `t(\`update.errors.${code}\`, params)`.
2. **As RPC return error**: Wails return-error path returns a string. RPC handlers that need to surface a structured `UpdateError` to a toast directly (e.g., `process_blocked` from `StartUpdate`) **also** write it to `state.LastError` and return a generic error. The frontend reads from snapshot, not from the error string. Document this in `update_handler.go`.

### 1.2.3 MVP-minus branch points

If §4.4 escalates to MVP-minus (full-file-replace only, no diff/patch), the following sections soften:

- **§1.2 `FileTask`** — no `Mode` field; all entries are full-replace.
- **§2.5 / §5.3 apply phase** — pure file rename; no patch invocation.
- **§5.1 `update_apply.go`** — no `update_patch.go` companion needed.

Plan-writer flags these as conditional on research outcome.

### 1.3 Settings additions

```go
type KurogamesSettings struct {
    Path    string `toml:"path"`
    TempDir string `toml:"temp_dir,omitempty"`  // optional; empty → os.TempDir()/launcher-collection/<gameID>
}
```

No UI for `temp_dir`; power-user override via `settings.toml` only. Defaults handled in `LoadSettings`.

**Backward-compat note**: existing `settings.toml` files with `version = 1` and no `temp_dir` field load cleanly (Go zero-value `""` triggers the runtime default). **No schema version bump needed**. Plan-writer should NOT add a settings migration task.

**Filesystem requirement**: TempDir MUST be on an NTFS volume (or any filesystem with sub-second mtime resolution). FAT32 / exFAT have 2-second mtime resolution which breaks the exact-equality semantics in §5.1. RunUpdate entry validates via `windows.GetVolumeInformation` for filesystem name; non-NTFS → `unsupported_filesystem` error (see §6.1). Default `os.TempDir()` is on the system drive (always NTFS in modern Windows).

---

## 2. Update lifecycle / state machine

State expressed as data shape, not enum. Per-game struct:

```go
// internal/app/update_state.go
type GameUpdateState struct {
    mu              sync.RWMutex
    AvailableUpdate *core.UpdatePlan        // CheckForUpdate result
    AvailablePredl  *core.UpdatePlan
    InFlight        *InFlightOp             // active op; nil = idle
    LastError       *core.UpdateError       // structured, Wails-marshalable
    PredlReady      *core.UpdatePlan        // download done, awaiting apply
}

type InFlightOp struct {
    Plan      core.UpdatePlan
    Phase     core.Phase
    Current   int64
    Total     int64
    cancel    context.CancelFunc            // unexported; cleared with InFlight under write-lock
    StartedAt time.Time
}
```

UI display derived from field combinations; see §3.1 for the matrix.

### 2.1 Concurrency rules

- `GameUpdateState.mu` is the **only** synchronization point.
- Writers: Wails RPC handlers (Start/Cancel) + `RunUpdate`'s `onEvent` callback — both take `Lock()`.
- Readers: `UpdateStatus*` Wails query — takes `RLock()` and returns a **value snapshot copy** (no pointer leakage to frontend → no torn reads).
- `cancel context.CancelFunc` always read/written together with `InFlight`; worker termination clears both under the same write-lock.

**Invariant**: `state.InFlight != nil` if and only if a `runUpdate` goroutine has been spawned and its `defer` has not yet run to completion. Equivalently: writers that set `InFlight = nil` must do so under `mu.Lock()` after **all** worker-side cleanup (temp dir cleanup, sidecar finalization) has completed; subsequent `StartUpdate` invocations only proceed once they observe `InFlight == nil` under their own `mu.Lock()`. Rapid Start→Cancel→Start sequences cannot interleave temp cleanup with new download because the new StartUpdate's `mu.Lock()` blocks until the previous goroutine's `defer` releases.

### 2.2 Persistence: three sidecars

`<TempDir>/<gameID>/<version>/` holds at most one of:

| File | Meaning | Written | Removed |
|---|---|---|---|
| `progress.json` | Download in-flight | After each file's hash verify; contains manifest ETag + completed file list | All files done → atomic rename → `predl_ready.json` (predl) or replaced by `apply.wal` (update) |
| `apply.wal` | Apply phase WAL | Before apply phase begins; embeds manifest snapshot + un-applied paths; appended `<path> OK` per successful rename | Apply phase fully successful → unlink |
| `predl_ready.json` | Predownload done, awaiting apply | Renamed from `progress.json` on predl-completion | User triggers ApplyPredownload (renamed → `apply.wal`); or new manifest invalidates (deleted with confirmation) |

State-machine view: `progress.json` →(rename) `predl_ready.json` OR →(rm + write) `apply.wal`; `apply.wal` →(rm).

**Sidecar collision rule** (§6.3): if `apply.wal` AND `progress.json` both exist, `apply.wal` wins; `progress.json` deleted during recovery scan.

### 2.3 Restart recovery scan

On app start, scan `<TempDir>/*/`:

| Sidecar present | Action |
|---|---|
| Only `progress.json` | Set `LastError = {Code: "interrupted_resume", Params: {phase: "download", wasPredl: false}, Retryable: true}`. UI prompts "上次更新中斷，繼續？" → on resume: re-fetch manifest header → compare ETag; mismatch → drop temp + emit `manifest_changed`; match → resume from progress (trust mtime+size exact-equality unless `--force-rehash` flag set; see §5.1). |
| Only `apply.wal` | Set `LastError = {Code: "interrupted_resume", Params: {phase: "apply", wasPredl: <bool>}, Retryable: true}`. UI prompts "上次套用中斷，重新嘗試？" → on resume: re-hash WAL-listed files only (not whole game dir), re-apply differing ones. |
| Only `predl_ready.json` | Parse → set `state.PredlReady`. No prompt. |
| Both `apply.wal` + `progress.json` | apply.wal wins; delete progress.json silently; treat as apply-resume case. |
| Both `apply.wal` + `predl_ready.json` | apply.wal wins; delete predl_ready.json. |
| Both `progress.json` + `predl_ready.json` | predl_ready wins (download was completing into predl); delete progress.json. |
| Corrupt `progress.json` or `predl_ready.json` | Log + delete; treat as if absent. |
| Corrupt `apply.wal` | `LastError = {Code: "unrecoverable"}`; UI prompts to use KRLauncher to repair. |

Recovery does not auto-trigger; user must click resume. Mid-apply guidance: if user dismisses the prompt without resuming, `apply.wal` stays on disk; next app start re-prompts.

### 2.4 PredlReady invalidation

- Refresh detects manifest version > PredlReady.Plan.Version → invalidate.
- UI confirms via ConfirmDialog "預下載已過期 (v2.0)，清除並更新到 v2.1？" → on Yes call `RemovePredownload` (deletes sidecar + temp files) + `StartUpdate`.
- Never reuse old predl files even if new version is a hotfix on top.

**Phantom-predl recovery**: if `current_version == PredlReady.Plan.Version` (e.g., user manually deleted the game and reinstalled via KRLauncher to that exact version, or KRLauncher applied the predl externally), the predl is no longer applicable. On Refresh, when CheckVersion's `Current == PredlReady.Plan.Version`, silently invalidate (delete sidecar + temp; no UI prompt — this is housekeeping, not a user choice).

### 2.5 Predl → Apply transition

- Refresh detects `manifest.released_at <= now` (or latest version equals PredlReady's plan version) → UI shows `[套用預下載]` button.
- Never auto-apply.
- On click: `ApplyPredownload` RPC → game-running guard → atomic rename `predl_ready.json` → `apply.wal` → enter apply phase via shared codepath (skips download).

### 2.6 Cancel rules

- **Download phase**: `ctx.Cancel()` → workers exit → cleanup TempDir for this game/version → `InFlight = nil`.
- **Apply phase**: cancel button **not rendered** in UI (primary defense). `ctx.Done()` inside apply loop is **no-op** (defensive — apply is atomic-batch; interruption leaves a half-patched install with no automated recovery short of full re-hash).
- **PredlReady idle**: user can clear via `RemovePredownload` ([移除] button) → deletes `predl_ready.json` + temp files → idle.

### 2.7 Game-running three-point guard

| Point | Action |
|---|---|
| RPC entry (StartUpdate / StartPredownload / ApplyPredownload) | Check via `windows.CreateToolhelp32Snapshot` + `Process32First/Next` or `wmic`-equivalent for the game's exe name; reject early with `process_blocked{kind: "process_running"}` |
| `RunUpdate` worker entry (after barrier release) | Re-check (race window between click and goroutine start); same error code |
| Apply phase begin | Acquire OS-level file lock on `<gameDir>\.lc_update.lock` via `windows.LockFileEx` with `LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY`; failure → `process_blocked{kind: "lock_held"}` |

**Launch refusal during apply**: M2's `App.Launch` RPC (in `internal/app/app.go`) gains a check: before calling provider `Launch()`, take `state.mu.RLock()`, check `state.InFlight != nil && state.InFlight.Phase == PhaseApply`, refuse with `process_blocked{kind: "lock_held"}`. This lives at App layer (not provider) so it applies uniformly across all providers — non-kuro games could in principle have apply-in-flight in M3.B+. Plan task adds this as a 5-line modification to `App.Launch`.

### 2.8 Retry / error policy

- Per-file network failure (5xx, connection drop): 3 retries with 1s / 4s / 16s exponential backoff.
- Per-file 4xx: no retry; `auth_failed` (401/403) or `manifest_not_found` (404).
- Hash mismatch: 2 retries (CDN edge corruption is non-trivial); then `corrupt`.
- Disk full: pre-checked at RunUpdate entry (`statfs`); mid-run ENOSPC → `disk_full` immediately, no retry.
- Manifest ETag drift: checked **only at RunUpdate entry**, not mid-run (a hotfix dropping mid-2-hour-download must not abort the run). Documented behavior.

### 2.9 `LastError` lifecycle

- Set under `mu.Lock()` whenever a non-cancel terminal error occurs.
- Cleared by `DismissError` RPC or by next successful StartUpdate.
- §3.7 render rule: when `InFlight != nil`, UI suppresses `LastError` even if non-nil (errors only surface at terminal).

---

## 3. UX wire (frontend)

### 3.1 BottomBar button-state matrix

| Condition | BottomBar middle-right | BottomBar middle-left |
|---|---|---|
| All nil | `[開始遊戲]` | — |
| Only `AvailablePredl` | `[開始遊戲]` | `[預下載 ↓]` |
| Only `AvailableUpdate` | `[更新 ↓]` | — |
| Both plans available | `[更新 ↓]` | `[預下載 ↓]` |
| `InFlight.Kind=Update, PhaseDownload` | `[更新中 78% │ ×]` | (predl button greyed) |
| `InFlight.Kind=Update, PhaseApply` | `[套用中 250/1200]` (× **not rendered**) | (predl button greyed) |
| `InFlight.Kind=Predl, PhaseDownload` | `[開始遊戲]` | `[預下載中 12% │ ×]` |
| `PredlReady != nil` | `[套用預下載]` | `[移除]` |

`[×]` is **embedded in the progress button** (right side, separated by 1px vertical divider) — single component, unambiguous ownership. During Apply phase the X is **not rendered** (avoids "broken control" feel of disabled state).

`BottomBar` reads only the **selected game**'s state: `useUpdatesStore().byGame[useGamesStore().selectedGameID]`.

### 3.2 Sidebar progress overlay

`SidebarRow.vue` adds a 1px-tall fill bar:

```css
.sidebar-row { position: relative; }
.sidebar-row .selection-bg { position: absolute; inset: 0; z-index: 0; }
.sidebar-row .row-content   { position: relative; z-index: 2; }
.sidebar-row .progress-bar  { position: absolute; bottom: 0; left: 0;
                              height: 1px; z-index: 1;
                              transition: width 200ms ease-out; }
```

z-index ordering: `selection-bg(0) < progress-bar(1) < row-content(2)`. Different colors for update (accent) vs predl (accent-dim).

Sidebar shows progress for all games with `InFlight != nil`; multiple games may have bars simultaneously.

### 3.3 Pinia store + Wails events

```ts
export type Phase = 'download' | 'apply';
export type PlanKind = 'update' | 'predownload';

export type UpdateError = {
  code: string;
  params?: Record<string, string | number>;
  retryable: boolean;
};

export const useUpdatesStore = defineStore('updates', {
  state: () => ({
    byGame: {} as Record<string, GameUpdateSnapshot>,
    pendingFrame: null as number | null,
    pendingPatches: {} as Record<string, GameUpdateSnapshot>,
  }),
  actions: {
    async loadAll() {
      this.byGame = await UpdateStatusAll();  // exactly ONCE on mount
    },
    bind() {
      EventsOn('update:changed', (gameID: string, snap: GameUpdateSnapshot) => {
        this.pendingPatches[gameID] = snap;
        if (this.pendingFrame == null) {
          this.pendingFrame = requestAnimationFrame(() => {
            for (const [id, s] of Object.entries(this.pendingPatches)) {
              this.byGame[id] = s;
            }
            this.pendingPatches = {};
            this.pendingFrame = null;
          });
        }
      });
    },
  },
});
```

**No polling**. `UpdateStatusAll` called exactly once on store mount; subsequent updates arrive via push events.

**Go-side throttle**: byte-progress events coalesce to 8 Hz per gameID (latest-wins) via a 125ms `time.Ticker` + 1-buffered channel + non-blocking replace-on-full. Phase transitions / cancel / error / done bypass the channel and emit synchronously (never dropped).

**RPC catalog**: `StartUpdate(gameID)` / `StartPredownload(gameID)` / `CancelInFlight(gameID)` / `ApplyPredownload(gameID)` / `RemovePredownload(gameID)` / `DismissError(gameID)` / `ResumeInterrupted(gameID)` / `UpdateStatusAll()` returning `Record<gameID, snapshot>`.

### 3.4 Modal + Toast

- **ConfirmDialog**: new `frontend/src/components/ConfirmDialog.vue` ~50 lines; uses `<dialog>` + Teleport; returns `Promise<boolean>`. **Not** `window.confirm()` — that blocks the JS event loop and backs up event queue from Go.
- **ToastHost**: new `frontend/src/components/ToastHost.vue` ~50 lines; Teleport-mounted; 5s auto-dismiss; `retryable: true` toasts are sticky until manual dismiss; cap 3 visible (newest on top), older collapsed to "+N more" pill. **No new dependency** (no vue-toastification etc.).

### 3.5 Confirmation flows

| Scenario | Flow |
|---|---|
| Game-running refusal | RPC returns `UpdateError{code: "process_blocked"}` → toast (no confirm) |
| PredlReady invalidate | User clicks `[更新]` (new version) → backend returns `predl_stale` → ConfirmDialog "預下載已過期 (v2.0)，清除並更新到 v2.1？" → on Yes call `RemovePredownload`, then `StartUpdate` |
| ApplyPredownload game-running | Pre-click RPC verifies `InFlight == nil`; backend re-verifies process state; same `process_blocked` error; toast |
| Restart resume | `loadAll()` → if snapshot has `LastError.code === "interrupted_resume"` → ConfirmDialog with i18n message variant per `params.phase` and `params.wasPredl` → on Yes call `ResumeInterrupted` |

### 3.6 i18n keys (en + zh-TW + zh-CN parity)

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
    "network": "網路錯誤：{detail}",
    "corrupt": "下載檔案損毀，請重試",
    "apply_partial": "套用過程中失敗，請重試",
    "unrecoverable": "更新狀態已損毀，請使用 KRLauncher 修復後再試",
    "internal": "內部錯誤：{detail}"
  }
}
```

Frontend resolves error display via `t(\`update.errors.${code}\`, params)`. Go side never sends rendered text.

### 3.7 Render-order rules

- `InFlight != nil` ⇒ UI hides `LastError` (errors only surface at terminal).
- Sidebar progress bar is **always above** `selection-bg` (z-index 1 > 0).
- BottomBar reads only the selected game's state.

### 3.8 Post-update asset / version refresh

`useUpdatesStore` is the single owner of the `update:changed` listener (§3.3). After the rAF batch applies its patch, it imports `useGamesStore` and calls the refresh APIs when a terminal-success transition is detected:

```ts
// inside useUpdatesStore.bind()'s rAF batch handler:
const prev = this.byGame[gameID];                      // read BEFORE applying patch
this.byGame[gameID] = snap;                             // apply patch
if (justCompletedUpdate(prev, snap)) {
  const games = useGamesStore();
  games.refreshVersionFor(gameID);
  games.loadAssetsFor(gameID);
}

// Helper (defined in updates.ts):
function justCompletedUpdate(prev, snap): boolean {
  return prev?.in_flight?.kind === 'update'
      && prev.in_flight.phase === 'apply'
      && snap.in_flight === null
      && snap.last_error === null;
}
```

Predownload completion (`prev.in_flight.kind === 'predownload'`) does NOT trigger refresh — current_version is unchanged. Apply-of-predl (`kind === 'update'` for ApplyPredownload's InFlightOp) DOES trigger.

**Single-listener rule**: only `useUpdatesStore.bind()` registers `EventsOn('update:changed', ...)`. `useGamesStore` does NOT subscribe. This keeps event-handler ownership unambiguous.

---

## 4. Protocol research methodology (M3.A.0)

### 4.1 Knowns / unknowns

| Item | Status | Acquisition |
|---|---|---|
| accountID extraction (P0 prerequisite) | Format known: `50004_<alnum>` (M2 BG research) | Cache-scrape from KRLauncher `Cache_Data\data_*` |
| Game manifest endpoint URL | Unknown | Sub-task C → A → B sequence |
| Manifest JSON shape | Unknown | mitmproxy capture |
| File CDN host | Known: `hw-pcdownload-qcloud.aki-game.net` | — |
| Diff / patch format | Unknown | Decision rule applied to manifest |
| Predownload endpoint | Unknown | Same flow |
| Range / resume support | Unknown | `curl -H "Range: bytes=0-1023"` verification |
| ETag / version identifier | Unknown | Capture response headers |

### 4.2 Source priority

1. **Cache scan** (free, zero-setup) — grep `data_N` for manifest-shaped URLs.
2. **mitmproxy on KRLauncher** (ground truth) — gated by smoke test.
3. **WuWa community / GitHub gist** — cross-check.
4. **Collapse Launcher** main branch — re-verify (M2 said zero coverage; lowest priority).

### 4.3 Research sub-tasks (M3.A.0)

**Gate-zero**: install mitmproxy + Windows trust CA + system proxy. Open KRLauncher (no clicks). Observe: does the known bg-config endpoint appear in flow list?
- Yes → proceed to A
- No → cert pinning suspected. Plan B: cache-scan-first; mitmproxy auxiliary; +1 day timebox

**Sub-task C — Cache scan (run first)**: grep `Cache_Data\data_*` for manifest-like URLs (`*.json`, `version`, `manifest`, `patch` keywords or `kurogame.com` host but non-background path). Rank candidates by containing `data_N` mtime, newest first. Output: ≤5 candidate URL list.

**Sub-task A — mitmproxy capture manifest fetch**:
- A1 (preferred): trigger KRLauncher's "verify game integrity" button.
- A2 (fallback): edit `Wuthering Waves Game/launcherDownloadConfig.json` to downgrade version (`3.3.0` → `3.0.0`), restart KRLauncher to force update detection.
- Output: full request + response (headers + body); if body > 1 MB store original gzipped (gitignored) + 10-20 entry synthetic minimal fixture (committed).

**Sub-task B — File download observation**: capture first 5-10 file downloads. Verify: CDN host, Content-Length, Range support (manual `curl -H "Range: bytes=0-1023"` returns 206), throttling headers. Output: 1 sample URL + Range behavior description.

**Sub-task D — Community / Collapse cross-check**: search Reddit / Discord / GitHub for "WuWa update protocol", "kurolauncher api"; check Collapse main branch for `kuro` / `wuthering` / `aki-game`. Output: agreements / disagreements list.

### 4.4 Timebox + escalation

- **Total cap: 1 working day** (A+B+C+D combined).
- If manifest endpoint not identified by EOD → escalate with: (i) captured material, (ii) fallback proposal **M3.A-MVP-minus** = full-file-replace only (simpler manifest), defer diff/patch support to M3.A.v2.

### 4.5 Task-graph dependencies

| Task | Blocked by M3.A.0 | Can run in parallel with M3.A.0 |
|---|---|---|
| `update_manifest.go` | yes | — |
| `update_download.go` | yes | — |
| `update_apply.go` | yes | — |
| `update_state.go` | no | yes |
| `update_progress.go` (sidecar) | no | yes |
| BottomBar / SidebarRow / Pinia store skeleton | no | yes |
| `update_handler.go` | partial | partial |
| ConfirmDialog / ToastHost / i18n | no | yes |

### 4.6 Research artifact

`docs/superpowers/research/m3a-kuro-update-protocol.md`:

```markdown
# Kuro WuWa update protocol — observed (YYYY-MM-DD)

## Auth prerequisite
- accountID source: <APPDATA path>; format: `50004_<alnum>`

## Endpoints

<!-- TEST_ANCHOR: manifest_url_regex -->
### Manifest (current version)
- URL pattern: `https://prod-alicdn-gamestarter.kurogame.com/launcher/.../manifest/<version>.json` (filled in)
- Method, headers, request body / params, response shape
- ETag header observed: yes / no
<!-- END_ANCHOR: manifest_url_regex -->

### Manifest (predownload, if separate)
...

### Files
- CDN: hw-pcdownload-qcloud.aki-game.net
- Range support: yes / no (verified `curl -H "Range: bytes=0-1023"` returned 206)

## JSON shapes
Synthetic minimal fixture (10-20 entries): `internal/providers/kurogames/testdata/manifest-sample.json`
Full capture (multi-MB): `manifest-full.json.gz`, .gitignore'd

## Diff format detection rule
- Entries contain `patchUrl/baseHash/patchSize` → diff-based; identify algorithm from magic bytes
- Flat `{path, hash, size, url}` only → full-replace
- Observed: <full-replace | HPatch v1 | ...>

## Sanitization (pre-commit regex)
- accountID `50004_[a-zA-Z0-9]+` → `<ACCOUNT_ID>`
- 32-char hex device IDs → `<DEVICE_ID>`

## Open questions / TODOs for M3.A.v2
- ...
```

### 4.7 Maintenance

- Kuro endpoint / JSON shape change → patch release (single-file fix `update_manifest.go`).
- Update protocol-research markdown's change-log section.
- Do NOT ship hardcoded endpoint fallback.
- **Future** (M4+): CI nightly smoke test against real manifest endpoint to detect server-side schema drift.

---

## 5. Components & data flow

### 5.1 Component responsibilities

| File | Responsibility |
|---|---|
| `update_manifest.go` | fetch + parse manifest → `UpdatePlan{Files[] (filtered to changed only), TotalBytes, ETag}`. Files identical between versions are filtered out at this stage; apply-phase Total reflects actual work. |
| `update_download.go` | 4 download workers (`const downloadWorkers = 4`; rationale comment: empirical CDN throttle threshold; M3.B+ exposes `KurogamesSettings.DownloadConcurrency`). Per-file SHA verify. Emits byte-progress (throttled). |
| `update_progress.go` | `progress.json` sidecar; `LoadProgress(tempDir)` for resume. Trust **exact equality** of mtime+size (`os.Stat(file).ModTime() == progress.entry.MTime && size == progress.entry.Size`); full-rehash with explicit force flag. progress.json is written AFTER each file's verify+rename, so its recorded mtime is captured post-fsync and matches the on-disk file's mtime byte-exactly. Reboot-safe because Windows lazy-flush is bounded by the rename's fsync. Mismatch → re-download from byte 0 (file-level recovery). |
| `update_apply.go` | Read manifest snapshot + temp dir → write `apply.wal` + fsync → unlink `progress.json` → per-file atomic rename → append WAL → on completion delete WAL. |
| `apply_lock_windows.go` / `apply_lock_stub.go` | `applyLock` interface (Acquire / Release). Windows uses `golang.org/x/sys/windows.LockFileEx` with `LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY` on `<gameDir>\.lc_update.lock`. Stub for non-Windows (CI Linux). |
| `kurogames.go` (existing) | Adds `(p *Provider) CheckForUpdate / RunUpdate` methods implementing `core.Updater`. |
| `internal/app/update_state.go` | `GameUpdateState` with mutex; Wails event emitter goroutine using **ticker-drain pattern** (125ms ticker + 1-buf channel + non-blocking replace; phase transitions / cancel / error / done bypass via sync direct emit). |
| `internal/app/update_handler.go` | Wails RPC handlers; 1st game-running guard at RPC entry; CancelInFlight reads `InFlightOp.cancel` under RLock then calls. |

### 5.2 Data flow — StartUpdate (and StartPredownload)

```
StartUpdate(gameID):
  1st game-running guard (tasklist)
  p := byID(gameID).(core.Updater)
  plan, _ := p.CheckForUpdate(ctx, gid)
  statfs precheck:
    required = TotalBytes + maxFileSize + 256MiB safety margin
    tempVol  := filepath.VolumeName(tempDir)
    gameVol  := filepath.VolumeName(gameDir)
    if tempVol != gameVol → ErrCrossVolumeTemp (no copy+delete fallback in M3.A; M3.B+)
    if free < required → ErrInsufficientSpace
  acquire mu.Lock()
  ctx, cancel = context.WithCancel(parent)
  state.InFlight = &InFlightOp{plan, Phase:Download, cancel, ...}
  release mu
  emit "update:changed"
  go runUpdate(ctx, plan, onEvent)
```

### 5.3 Data flow — runUpdate goroutine

```
runUpdate(ctx, plan, onEvent):
  defer:
    if r := recover(); r != nil:
      err = UpdateError{Code:"internal", Params:{detail: <sanitized>}, Retryable: true}
    acquire mu.Lock()
    state.InFlight = nil
    if err != nil and !errors.Is(err, ctx.Err()):
      state.LastError = err
    release mu
    emit "update:changed" (terminal)

  if ctx.Err() != nil:
    return ctx.Err()                                 // cancel-before-start handled

  2nd game-running guard
  re-fetch manifest header → if ETag != plan.ManifestETag → ErrManifestChanged

  Phase = Download
  progress = LoadProgress(tempDir)                    // nil if first run
  for each file in plan.Files:
    if ctx.Done() → return ctx.Err()
    if progress.has(file) and (mtime+size match OR (force=true and SHA match)):
      emit Current++ (throttled)
      continue
    partPath := <temp>/<rel>.part
    os.Remove(partPath)                              // .part cleanup pre-redownload
    download → SHA verify (retry 2x on mismatch, 3x on net err with 1s/4s/16s backoff)
    atomic rename .part → final
    atomic update progress.json (write tmp + rename)
    emit byte-progress (throttled to 8 Hz)

  if Kind == PlanPredownload:
    atomic rename progress.json → predl_ready.json
    state.PredlReady = plan; state.AvailablePredl = nil (under mu)
    return nil

  Phase = Apply
  re-run statfs precheck (volume parity + free space — install path may have changed)
    if cross-volume → ErrCrossVolumeMidrun
  acquire applyLock (3rd guard) → on fail ErrProcessBlocked{kind: lock_held}
  write apply.wal{manifest snapshot + un-applied paths}; fsync
  unlink progress.json
  for each file:
    if ctx.Done() → no-op (defensive; UI prevents cancel here)
    atomic rename <temp>/<rel> → <gameDir>/<rel>
    append <rel> OK to apply.wal
    emit Current++ (throttled)
  release applyLock
  rm apply.wal
  state.AvailableUpdate = nil (under mu)
  return nil
```

### 5.4 Data flow — ApplyPredownload

```
ApplyPredownload(gameID):
  1st game-running guard
  acquire mu.Lock()
  if !state.PredlReady → error
  ctx, cancel = WithCancel
  state.InFlight = &InFlightOp{Plan: state.PredlReady, Phase: Apply, cancel}
  release mu
  emit "update:changed"
  go applyOnly(ctx, plan, onEvent):
    acquire applyLock
    atomic rename predl_ready.json → apply.wal
    [reuse runUpdate's Apply phase steps]
    state.PredlReady = nil
```

### 5.5 Data flow — CancelInFlight

```
CancelInFlight(gameID):
  acquire mu.RLock()
  if InFlight == nil or InFlight.Phase == PhaseApply → no-op
  cancelFn := InFlight.cancel
  release mu.RUnlock()
  cancelFn()
  // worker goroutine's defer cleans state + temp + emits terminal
```

### 5.6 Concurrency model

- Single `sync.RWMutex` per `GameUpdateState`; reads return value snapshot (no pointer leak).
- 1 `runUpdate` goroutine per active update; internal worker pool of 4 download workers (constant).
- Cross-game: M3.A is single-game (WuWa) — when `InFlight != nil` for WuWa, other games' Refresh / Launch unaffected. Cross-game concurrent updates are M3.B+.
- **Cancel ownership**: `CancelFunc` lives on `InFlightOp`. `StartUpdate` creates and stores it. `CancelInFlight` RPC takes `mu.RLock`, reads `InFlightOp.cancel`, releases lock, calls cancel(). The original StartUpdate handler returns immediately after `go runUpdate(...)` and retains no reference.
- **Cancel-before-start contract**: `runUpdate` must check `ctx.Err()` before any I/O. `defer` clears InFlight + emits terminal even if main flow never entered.
- **Panic recovery**: `runUpdate`'s `defer recover()` translates panics to `UpdateError{Code:"internal", Retryable: true}`; logs full stack via `slog.Error`.

### 5.7 Test seams

| Component | Seam |
|---|---|
| manifest / download | `*http.Client` injected; `httptest.NewServer` for tests |
| apply | two `t.TempDir()` (temp + simulated game dir) |
| progress | pure fs |
| applyLock | `applyLock` interface; prod = Windows syscall, test = fake recording contention |
| state throttle | `Clock` interface (`Now() time.Time`, `NewTicker(d) Ticker`); injectable |
| start barrier | `startBarrier <-chan struct{}` constructor option for cancel-before-start tests |
| logger | `*slog.Logger` injected via constructor; **forbidden** to call `slog.SetDefault` in non-main packages |

---

## 6. Error handling & failure modes

### 6.1 Error code catalog

| Code | Trigger | Retryable | Notes |
|---|---|---|---|
| `process_blocked` | Any guard point detects process running OR applyLock fails | true | `Params.kind: "process_running" \| "lock_held"` |
| `manifest_changed` | RunUpdate entry ETag mismatch | true | |
| `manifest_not_found` | Manifest endpoint 404 | false | |
| `auth_failed` | Manifest endpoint 4xx (401/403) | false | accountID may be expired/banned |
| `network` | 5xx / connection drop after 3 retries | true | |
| `predl_stale` | Update triggered while PredlReady is old version | false | UX flow: dismiss → RemovePredownload |
| `interrupted_resume` | App load detects sidecar but no InFlight | true | `Params.phase: "download" \| "apply"`, `Params.wasPredl: bool` |
| `disk_full` | statfs precheck fails / mid-run ENOSPC | false | |
| `cross_volume_temp` | TempDir vs gameDir different volume | false | M3.B+ may add copy+delete fallback |
| `unsupported_filesystem` | TempDir is on FAT32 / exFAT (sub-second mtime resolution missing) — see §1.3 | false | Plan-writer adds matching i18n key `update.errors.unsupported_filesystem` |
| `cross_volume_midrun` | RunUpdate entry passed; apply phase detects volume change | false | |
| `corrupt` | 2 retries, hash still mismatch | true | |
| `apply_partial` | Apply phase fs operation failure (rename / WAL write) | true | WAL preserved for retry |
| `unrecoverable` | apply.wal parse failure or ResumeInterrupted retry still fails | false | UI prompts to use KRLauncher |
| `internal` | runUpdate panic recover or unexpected | true | `Params.detail` |

### 6.2 Failure-point behavior matrix

| Failure | Immediate action | Sidecar state | UI |
|---|---|---|---|
| Manifest 5xx / net err | retry 3x → `network` | none | toast |
| Manifest 4xx | immediate `auth_failed` / `manifest_not_found` | none | toast |
| Per-file net err (final) | retry 3x → `network`; preserve progress.json | progress.json | toast (resume on restart) |
| Per-file hash mismatch (final) | retry 2x → `corrupt`; preserve progress.json | progress.json | toast |
| Disk full (precheck) | `disk_full`, no retry | none | toast |
| Disk full (mid-run) | `disk_full` | progress.json | toast |
| Cross-volume @ RPC entry | `cross_volume_temp` | none | toast |
| Cross-volume @ apply | `cross_volume_midrun` | apply.wal | toast (user must move install / temp) |
| ETag drift @ RunUpdate entry | `manifest_changed` | progress.json cleared | toast |
| ETag drift mid-run | **not checked** (frozen plan runs to completion) | — | — |
| applyLock fail | `process_blocked{kind: lock_held}` | progress.json | toast: "其他程序鎖住遊戲" |
| Apply rename fail | `apply_partial`; WAL preserved | apply.wal | toast |
| Cancel @ download | clean temp + sidecar | all cleared | UI returns to previous state |
| Cancel @ apply | UI button not rendered (primary defense); ctx.Done() in apply loop is no-op | apply.wal | continues to completion |
| `runUpdate` panic | defer recover → `internal`; clear InFlight; emit terminal | depends on phase | toast |

### 6.3 Sidecar collision rules (recovery)

```
for each <TempDir>/<gameID>/<version>/:
  if can't parse progress.json     → log + delete; treat as no progress
  if can't parse apply.wal         → state.LastError = unrecoverable; UI prompts KRLauncher repair
  if can't parse predl_ready.json  → log + delete; no PredlReady

  collision rules (post-parse):
    has apply.wal:                       apply.wal wins; delete progress.json + predl_ready.json
    has progress.json + predl_ready:     predl_ready wins; delete progress.json
    has only progress.json:              download interrupted
    has only predl_ready.json:           predownload completed, awaiting release
```

### 6.4 Panic recovery contract

```go
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) (err error) {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("runUpdate panic",
                "game", plan.GameID,
                "panic", r,
                "stack", string(debug.Stack()))
            err = &core.UpdateError{
                Code:      "internal",
                Retryable: true,
                Params:    map[string]string{"detail": fmt.Sprint(r)},
            }
        }
    }()
    // ...
}
```

App layer's worker-launching code wraps an additional `defer recover()` for safety (in case a panic escapes RunUpdate's defer — e.g., from an `onEvent` callback into broken App-side code).

### 6.5 Logging policy

- `slog` structured fields: `gameID`, `version`, `phase`, `error_code`.
- INFO: phase entry / exit, cancel, final result.
- DEBUG: byte-progress (already throttled), retry attempt, ETag check.
- ERROR: each final `UpdateError` (not transient retries-in-progress).
- **URL sanitization**: any logged URL or `UpdateError.Params` URL passes `sanitizeURL(s string) string` first. Regex replace: `accountID` (`50004_[a-zA-Z0-9]+`) → `<ACCOUNT_ID>`, 32-char hex device IDs → `<DEVICE_ID>`. Required even though slog destination is local stderr (logs may end up in support bundles or screenshots).

### 6.6 Cancel-during-apply confirmed design

- **Primary defense**: UI does not render `[×]` during apply phase (§3.1).
- **Defensive secondary**: `runUpdate` apply loop has **no** `ctx.Done()` exit check (apply is atomic-batch; interruption leaves a half-patched install; documented).
- This is intentional. Future contributors must not "fix" the missing check.

### 6.7 `Params.detail` schema

For `network` / `apply_partial` / `internal`:

```json
{
  "status": "503",
  "url": "https://hw-pcdownload-qcloud.aki-game.net/.../<sanitized>",
  "reason": "connection refused"
}
```

No stack traces. No full headers.

### 6.8 Multi-toast policy

- Cap: 3 visible.
- Stacking: newest on top.
- Overflow: collapsed to "+N more" pill.
- `retryable: true` toasts are sticky until manual dismiss; others auto-dismiss in 5s.

### 6.9 User-initiated retry semantics

- Toast retry button calls the same RPC (`StartUpdate` / `StartPredownload` / `ApplyPredownload` / `ResumeInterrupted`).
- Retry = fresh `RunUpdate`; progress.json's already-completed entries skipped.
- No cooldown / per-error backoff at user level.

---

## 7. Testing strategy

### 7.0 Test-only seams (production code must expose)

| Seam | Production | Test |
|---|---|---|
| Clock | `realClock{}` (`time.Now`, `time.NewTicker`) | `fakeClock` (advance arbitrary, immediate ticker fire) |
| Start barrier | none (direct `go runUpdate`) | constructor option `startBarrier <-chan struct{}` |
| Logger | `*slog.Logger` injected via App constructor | test logger (records to `*bytes.Buffer`) |
| HTTP client | DI (`*http.Client`) | `httptest.NewServer.Client()` |
| applyLock | `windows.LockFileEx` impl | fake recording contention scenarios |
| FS root | All paths constructed from settings/config | `t.TempDir()` substitution |

### 7.1 Test pyramid

| Layer | Scope | Tooling |
|---|---|---|
| Unit | Single-file functions | `go test`, `httptest.NewServer`, `t.TempDir()` |
| Integration | manifest → download → verify → apply end-to-end | as above + multi-fixture |
| Race | `update_state.go` concurrency | `go test -race -count=10` |
| Load | 1000-entry manifest performance | `testing.B` + `-tags=load` |
| Frontend | Pinia store / Vue components | Vitest (new dep this milestone) |
| Drift | Doc vs code consistency | `m3a_protocol_doc_test.go` |
| Manual smoke | wails build → real KRLauncher | user-driven |

### 7.2 Error-code coverage

`errcode_coverage_test.go` walks the const block (or a hand-maintained `var allCodes = []string{...}`) and fails if any code lacks at least one referencing test.

| Code | Exercising test |
|---|---|
| `process_blocked` | `update_handler_test.go: TestStartUpdate_ProcessRunning`, `TestApply_LockHeld` |
| `manifest_changed` | `update_manifest_test.go: TestRunUpdate_ETagDriftRejects` |
| `manifest_not_found` | `update_manifest_test.go: TestFetchManifest_404` |
| `auth_failed` | `update_manifest_test.go: TestFetchManifest_401` |
| `network` | `update_download_test.go: Test5xxRetriesThenFails` |
| `predl_stale` | `update_handler_test.go: TestStartUpdate_PredlStale` |
| `interrupted_resume` | `update_state_test.go: TestRecoveryScan_*` (one per phase × wasPredl combo) |
| `disk_full` | `update_state_test.go: TestStatfs_PrecheckFails` + `_MidRunFails` |
| `cross_volume_temp` | `update_state_test.go: TestPrecheck_DifferentVolume` |
| `cross_volume_midrun` | `update_apply_test.go: TestApply_VolumeChangedBetweenPhases` |
| `corrupt` | `update_download_test.go: TestHashRetriesThenFails` |
| `apply_partial` | `update_apply_test.go: TestApply_RenameFails` |
| `unrecoverable` | `update_state_test.go: TestRecoveryScan_CorruptWal` |
| `unsupported_filesystem` | `update_state_test.go: TestPrecheck_NonNtfsTempDir` |
| `internal` | `update_handler_test.go: TestRunUpdate_PanicRecovers` |

### 7.3 Integration tests

| Test | Goal |
|---|---|
| `TestUpdate_HappyPath` | manifest → multi-file download → SHA verify → apply phase → game dir aligns → final event |
| `TestPredl_HappyPath` | PlanPredownload completes; progress.json → predl_ready.json; game dir untouched |
| `TestApplyPredl_HappyPath` | Start from predl_ready.json → ApplyPredownload RPC → apply.wal → apply phase → completion |
| `TestSanitizeURL` (+ `testing/F` fuzz) | accountID / device-id / token redaction |
| `m3a_protocol_doc_test.go` | Parse research markdown, find `<!-- TEST_ANCHOR: manifest_url_regex -->` block, assert `manifestURLTemplate` const matches |

Acceleration: retry backoff via injected `fakeClock` (microsecond advances); `httptest.NewServer` simulates 5xx/4xx/hash-mismatch/connection-drop/Range.

### 7.4 Cancel tests

- `TestCancel_DuringDownload`: barrier release → cancel mid-stream → temp + progress cleared, `InFlight = nil`, emit terminal.
- `TestCancel_DuringApply`: enter apply phase → cancel → apply continues to completion (ctx no-op); UI assertion is in frontend.
- `TestCancel_BeforeStart`: cancel() *before* barrier release → goroutine exits immediately, clears InFlight, emits terminal.
- `TestRapidStartCancelStart_NoInterleave`: Start, immediately Cancel, immediately Start again under `-race`. Asserts second StartUpdate observes `InFlight == nil` only after first goroutine's defer fully completes (worker temp-cleanup happened-before second Start). Validates §2.1 invariant.

### 7.5 Resume tests

- `TestResume_PartialProgress`: pre-write 5/10 verified progress.json + corresponding `.part` files → run → 5 skipped (mtime+size match), 5 re-downloaded, all `.part` cleaned up.
- `TestResume_PredlInvalidate`: predl_ready.json v2.0 + new manifest v2.1 → invalidate flow (clean temp, restart).
- `TestResume_ApplyWalReplay`: apply.wal residual marking 50% applied → re-hash WAL-listed unrenamed files → complete apply.
- `TestResume_BothSidecars`: progress.json + apply.wal coexist → apply.wal wins, progress.json deleted.
- `TestRefresh_PhantomPredlSilentInvalidate`: predl_ready.json on disk + Refresh returns `current_version == PredlReady.Plan.Version` (e.g., user reinstalled via KRLauncher to that version) → assert sidecar + temp dir deleted, NO ConfirmDialog event emitted, `state.PredlReady = nil`. Validates §2.4 phantom-predl rule.
- **Property-based** (recommended, optional): 8 sidecar-presence × {valid, corrupt, stale} combinations → assert recovery matches §6.3 truth table.

### 7.6 Concurrency / race

- `TestStateRace`: 100 goroutines reading/writing `GameUpdateState`, run with `-race`.
- `TestThrottle_8Hz`: fakeClock advances; 1000 events fed; ~8 outgoing per second; final event always emitted (phase transition / done bypass).
- `TestThrottle_FinalEventEmitted`: simulate completion sequence; assert at least the final byte-progress + done event reach frontend.

### 7.7 applyLock cross-platform

- `apply_lock_windows_test.go` (`//go:build windows`): spawn child process (test binary self-invocation + env flag) takes lock; parent tries to take → `LOCKFILE_FAIL_IMMEDIATELY` returns specific error. `t.Skip` if not Windows.
- `apply_lock_other_test.go` (`//go:build !windows`): assert stub Acquire returns documented sentinel; Release no-op.
- **CI matrix must include `windows-latest`**; if Linux-only, the Windows test never runs (dead).

### 7.8 Frontend tests (Vitest)

| File | Coverage |
|---|---|
| `stores/updates.test.ts` | rAF batching: 100 EventsOn arrivals → 1 frame mutation; snapshot copy doesn't leak refs |
| `components/BottomBar.test.ts` | **table-driven 8 rows**: `it.each(stateMatrix)` per row asserts button presence / disabled / label / progress fill |
| `components/SidebarRow.test.ts` | progress bar z-index, selection-bg overlap, update vs predl color |
| `components/ConfirmDialog.test.ts` | promise resolution, Teleport mount, Esc / Enter |
| `components/ToastHost.test.ts` | 5s auto-dismiss, sticky retryable, 3-cap with "+N more" |
| `i18n_parity.test.ts` | en + zh-TW + zh-CN `update.errors.*` keys deep-equal sorted |

### 7.9 Load fixtures

`internal/providers/kurogames/testdata/large_manifest.json.gz` (1000 entries, ~10 MB gzipped).

`update_load_test.go` (`//go:build load`, run with `go test -tags=load -bench=.`):

- `BenchmarkProgressAppend_1000Entries` (assert ≤50ms)
- `BenchmarkSidecarParse_LargeProgress` (assert ≤50ms)
- `BenchmarkApplyLoop_RenameOnly` (no-op rename of 1000 fake files; wall-clock budget)

### 7.10 Manual smoke checklist

1. WuWa installed at latest version → `[開始遊戲]`, no update / predl button.
2. Manually downgrade `launcherDownloadConfig.json` version → Refresh → `[更新]` button appears.
3. Click `[更新]` while game running → `process_blocked` toast.
4. Click `[更新]` properly → progress fills → cancel → temp cleaned, returns to previous state.
5. Re-click `[更新]` → completes → `[開始遊戲]` returns.
6. ⚠️ **Disk full simulation**: do **not** run on host. Use 512MiB VHD (`diskpart`) or `subst`-mounted small partition; or VM checkpoint. Trigger `disk_full` toast; progress.json preserved.
7. Restore space → restart → "interrupted resume?" prompt. Continue → completes. **Assert log line**: `verify_mode=sha256 reason=resume_after_crash` (or `trust_mtime`).
8. Predl button (fake `AvailablePredl`) → predownload → completes → `predl_ready.json` written.
9. Refresh after release → `[套用預下載]` shown.
10. ApplyPredownload while game running → toast.
11. PredlReady stale (new manifest version) → ConfirmDialog → cleared. `[manual, opportunistic]` — requires real Kuro release; mocked path is future work.
12. Manifest ETag changed (test via mock server in dev binary, if available) → `manifest_changed` toast.
13. Multi-toast → ≤3 visible + "+N more".
14. Switch zh-TW / en → all update strings render correctly.
15. Sidebar progress bar matches BottomBar % within 1% and updates within 250ms.
16. Apply phase `[×]` button is **not rendered** (assert in DOM).
17. Close app mid-download → restart → resume prompt → continue → complete.

---

## 8. Out-of-scope / future work (M3.A.v2 / M4)

- **Fresh install** (game not yet present): full game download flow.
- **Cross-game concurrent updates**: requires queue manager + per-provider throttling.
- **Byte-level resume** (HTTP Range mid-file): meaningful only for very large single files.
- **Pause / resume**: distinct from cancel; persistent paused state.
- **Bandwidth throttling** + **scheduled downloads**: settings UI.
- **Auto-prompt-on-Refresh predownload**: settings opt-in.
- **Cross-volume temp dir** (copy+delete fallback): treated as error in M3.A.
- **mid-run ETag re-check**: explicitly out per §2.8 to avoid hot-fix-mid-2-hour-download interruption.
- **Drift-detection CI smoke** against real Kuro endpoint nightly.
- **Settings UI** for TempDir / DownloadConcurrency.
- **Mock manifest endpoint** in the dev / test binary (for smoke #11 and #12 reproducibility).

---

## 9. Open questions for plan stage

These answers depend on §4 research outcomes and don't block spec approval:

1. Does Kuro's manifest use full-file replacement or HPatch (binary diff)? If HPatch → `update_patch.go` is added; if full-replace → not needed.
2. What's the exact manifest URL pattern (path, query params)?
3. Does the manifest endpoint require auth headers beyond accountID-in-path?
4. Are individual file URLs Range-supporting (for future M4 byte-level resume)?
5. Is there a separate predownload endpoint, or is it the same endpoint with a query flag?

Plan-writing should defer these to the M3.A.0 research task and keep manifest.go's exact constants as the deliverable of that research.

---

## Change log

- 2026-05-04: initial spec drafted via brainstorming → 7-section design loop with backend reviewer per section. All sections accepted with reviewer-suggested edits.
