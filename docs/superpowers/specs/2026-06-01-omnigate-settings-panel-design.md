# Omnigate Settings Panel — Design Spec

Date: 2026-06-01
Branch: `settings-ui/spec`
Status: APPROVED (brainstorm) — pending plan

## §0 Goal

Wire the Topbar gear button (inert since M2) to a working settings panel that lets
the user edit the only settings that currently drive real behavior: each backend's
install **path** and (where applicable) its **temp_dir** override. The backend
already exposes `App.GetSettings()` / `App.UpdateSettings()`; the gap is purely the
frontend panel + one folder-picker bridge method.

## §1 Locked decisions

| # | Decision |
|---|---|
| 1 | **Presentation**: right slide-over drawer (`Teleport` to body, mirrors existing `notif-panel`). Backdrop click-away + ESC close. Bottom Save/Cancel bar. |
| 2 | **Content (lean)**: per-backend install path + temp_dir override only. |
| 3 | **Save model**: explicit. Edits are staged in a local draft; **Save** calls `UpdateSettings` once (→ re-detect); **Cancel**/close/ESC discards. |
| 4 | **Path editing**: native `OpenDirectoryDialog` via a new `App.BrowseForDirectory`, plus the field stays manually editable as fallback. |
| 5 | **temp_dir**: empty = runtime default (`<TEMP>/omnigate/<backend>/…`). Field clearable; hint explains empty=default. |

### Out of scope (excluded by design)
- **Language** — already toggled on the Topbar; not duplicated here. (Known: it is runtime-only / not persisted on startup; unchanged by this work.)
- **Region** (hoyoverse) — single value `global`; nothing to choose. Stored value preserved untouched.
- **`banner_animation_pref`, `show_technical_info`** — defined in `Settings` but not consumed anywhere; not surfaced (would be dead controls). Preserved untouched.
- In-panel "detected N games" feedback — re-detection is reflected by the sidebar; no extra in-panel status.

## §2 Backend settings shape (existing, unchanged)

`internal/app/settings.go`:

```go
Settings{
  Version int
  App     AppSettings{ Language, BannerAnimationPref string; ShowTechnicalInfo bool }
  Backends BackendSettings{
    Hoyoverse  HoyoverseSettings{ Path, Region, TempDir string }
    Kurogames  KurogamesSettings{ Path, TempDir string }
    Hypergryph HypergryphSettings{ Path string }   // no TempDir
  }
}
```

- `App.GetSettings() Settings` and `App.UpdateSettings(s Settings) error` are already
  Wails-bound. `UpdateSettings` saves TOML, replaces in-memory settings,
  `invalidateDetect()`, and `constructProviders()`.
- Editable fields in the panel: `Backends.Hoyoverse.{Path,TempDir}`,
  `Backends.Kurogames.{Path,TempDir}`, `Backends.Hypergryph.Path`.
- **Recognized fields round-trip; the draft preserves the unshown ones**: the draft is
  a full copy of `GetSettings()`; the panel mutates only the five fields above; Save
  writes the whole struct back. Caveats (existing `SaveSettings` behavior, not changed
  here): `Version` is **canonicalized to 1** (settings.go forces it), and any TOML keys
  that are not fields of `Settings` (manual/future keys) are **dropped** on Save because
  `SaveSettings` marshals the typed struct. `App.*` (incl. dead settings) and
  `Hoyoverse.Region` ARE struct fields, so they round-trip intact.
- **Wire shape**: `Settings` has only `toml:` tags, no `json:` tags, so over the Wails
  bridge the object uses Go's **PascalCase** field names. The panel binds to the
  generated `wailsjs/go/models` `Settings` type (`draft.Backends.Hoyoverse.TempDir`,
  etc.) — NOT toml snake_case. The `settings.*` names in §5 are i18n keys, unrelated to
  the wire field names.

## §2.5 Concurrency & save safety (BLOCKER from spec review)

`UpdateSettings` reassigns `a.settings` and rebuilds `a.providers` (`constructProviders`
sets `a.providers = nil` then appends) with **no synchronization**. Update flows read
both from a goroutine: `runStartUpdateAsync` (launched via `go`) reads `a.provider(gid)`
and `a.tempDirFor` (which reads `a.settings`) *during* a run; background
`CheckForUpdate`/refresh probes also read them. The panel is the **first** caller of
`UpdateSettings`, so it newly makes this race reachable. Two protections, both required:

1. **Panel-side Save gate (semantic)**: disable Save while ANY update is in-flight, to
   prevent a mid-run temp_dir/path swap (which would split staging across two temp roots).
   Reuse the existing frontend predicate
   `Object.values(updates.byGame).some(s => s.in_flight != null)` (the same one removed
   from the bell spinner). Show a disabled Save + tooltip explaining "更新進行中無法儲存".
2. **Go-side lock (data race) — non-reentrant-safe strategy**: add `settingsMu sync.RWMutex`
   to `App` guarding the `a.settings` and `a.providers` fields. Go's `RWMutex` is **NOT
   reentrant** (a nested `Lock`, or an `RLock` while a writer is pending, deadlocks), so the
   discipline below is mandatory — **exactly one acquisition per call path; no path locks twice**:
   - **`UpdateSettings` is the ONLY write-lock holder**: `settingsMu.Lock()` → set
     `a.settings = s` → call `constructProviders()` → `Unlock()` (one critical section).
   - **`constructProviders` and `cachedDetect` are LOCK-FREE** (must NOT touch `settingsMu`):
     `constructProviders` is called only from `New` (pre-concurrency) and from write-locked
     `UpdateSettings`; its inline `a.settings` reads are already covered by that write lock.
     `cachedDetect` touches only `detectMu` + the provider, never the settings fields.
   - **Leaf read accessors take a single-shot `RLock`/`RUnlock`** and must NOT call another
     locked accessor while holding it: `provider`, `byID`, `tempDirFor`'s `a.settings` reads,
     and **`GetSettings`** (`return a.settings` is a by-value multi-word struct copy that
     races with the write — the panel itself calls it, on its own RPC goroutine).
   - **Methods that iterate `a.providers` AND call another accessor in the loop**
     (`ListGames`, `ListBackends`, `UpdateStatusAll`, `knownBackendIDs`, `scanForRecovery`)
     must **snapshot `a.providers` into a local slice under a brief RLock, release, then iterate
     the copy** calling the lock-free `cachedDetect` — this is what avoids the nested-RLock
     deadlock.
   `detectMu` stays as-is (guards only the detect cache). This eliminates the race for
   concurrent readers the Save gate alone doesn't cover (e.g. a Wails-RPC refresh/CheckForUpdate
   probe on another goroutine firing during Save). §9 lists the sites; the **strategy above is
   normative** — site enumeration alone cannot rescue a re-entrant design.

## §3 New backend method

```go
// BrowseForDirectory opens the native folder picker seeded at `current`
// (only if it exists) and returns the chosen absolute path, or "" if cancelled.
func (a *App) BrowseForDirectory(current string) (string, error)
```

- Implementation: `wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "...", DefaultDirectory: dir})`.
- **`DefaultDirectory` MUST be set to `current` ONLY when `current` exists as a directory**,
  via the `dialogDefaultDir` helper; otherwise pass `""` (BLOCKER from spec review). Wails'
  `OpenDirectoryDialog` returns an error *without opening the dialog* when `DefaultDirectory`
  is a non-existent path (pkg/runtime/dialog.go:35-39) — and the panel's primary use case is
  correcting a wrong/missing path, so `current` is frequently non-existent. Without this
  guard, clicking Browse on a bad path does nothing.
- The existence check uses **`os.Lstat` + `IsDir()`** (NOT `os.Stat`) to bit-match Wails'
  internal `fs.DirExists` (which uses `Lstat`) — otherwise a symlinked dir could pass our
  check yet be rejected by Wails, re-triggering the exact error we guard against.
- **Cancel** → Wails swallows `ErrCancelled` and returns `("", nil)` on Windows
  (windows/dialog.go) → method returns `("", nil)`. This is the contract the panel relies on.
- Guard: if `a.ctx == nil` (no window, e.g. tests) return `("", nil)` — this prevents Wails'
  `getFrontend(ctx)` from calling `log.Fatalf` (process exit) on a nil ctx; it is NOT a panic.
- A genuine (non-cancel) dialog error IS returned to the caller (not swallowed); the panel
  logs it and leaves the field unchanged.
- Wails-bound; consumed by the panel's "Browse…" buttons.

## §4 Frontend component — `SettingsPanel.vue`

Mirrors `Topbar.vue`'s `notif-panel` overlay pattern (`Teleport to body`, backdrop +
panel, z-index above content).

**Open/close state**: a boolean in the `view` store (e.g. `settingsOpen`) so the
Topbar gear can toggle it and the panel can close itself. ESC + backdrop click close
(close == discard).

**Lifecycle**:
1. On open: `const draft = JSON.parse(JSON.stringify(await GetSettings()))` typed as the
   generated `wailsjs/go/models` `Settings` (PascalCase fields). Deep copy so Cancel can
   discard. (structuredClone also works; the object is plain JSON over the bridge.)
2. Render sections (one per backend) binding to `draft.Backends.*` (PascalCase: `.Path`, `.TempDir`).
3. "Browse…" button → `const p = await BrowseForDirectory(currentField); if (p) field = p`.
4. **Save** (disabled while any update in-flight — see §2.5): `await UpdateSettings(draft)`;
   on success → run the shared post-change refresh, then close. On error → show inline
   error (`settings.save_error`), keep open, keep draft.
5. **Cancel/ESC/backdrop**: discard draft, close. (No write.)

**Save-disabled guard**: Save is disabled (with `settings.save_disabled_inflight` tooltip)
when `Object.values(updates.byGame).some(s => s.in_flight != null)`.

**Shared post-change refresh**: extract Topbar `onRefresh`'s body
(`Refresh()` → `games.load()` → `games.refreshVersions()` → `games.loadAssets()` →
per-installed-game `updates.checkForUpdate` probe) into a reusable function (e.g. a
`games`-store action or a composable) and call it from BOTH Topbar and the panel's Save,
so they don't drift. The post-save refresh DOES include the update probe (intended:
a path change can reveal a newly-detected installed game).

**view store**: add `settingsOpen` state + `openSettings()`/`closeSettings()` actions
(matching the store's existing action idiom, e.g. `setView`/`toggleSidebar`); Topbar gear
calls `openSettings()`.

**Sections** (each = backend label + fields):
- HoYoverse: Path (text + Browse), TempDir (text + Browse + Clear, hint)
- Kuro: Path (text + Browse), TempDir (text + Browse + Clear, hint)
- Hypergryph: Path (text + Browse)

**Topbar wiring**: gear `<button>` gets `@click="view.openSettings()"`.

## §5 i18n (new `settings.*` keys, ×3 locales: zh-TW / zh-CN / en)

- `settings.title` — panel header ("設定")
- `settings.save`, `settings.cancel`, `settings.browse`, `settings.clear`
- `settings.path_label`, `settings.tempdir_label`
- `settings.tempdir_hint` — "留空 = 使用預設暫存資料夾"
- `settings.save_error` — inline save failure (with `{detail}`)
- `settings.save_disabled_inflight` — Save-disabled tooltip while an update runs
- `settings.backend.hoyoverse`, `settings.backend.kurogames`, `settings.backend.hypergryph` — section labels

The existing `__tests__/i18n_parity.test.ts` asserts **identical flattened key sets**
across zh-TW / zh-CN / en, so every new key MUST be added to all three locales (nested
`settings.backend.*` is fine — the flattener handles nesting) or the suite fails.

## §6 Error handling

| Case | Behavior |
|---|---|
| `UpdateSettings` returns error | Inline error in panel (`settings.save_error` + detail); panel stays open; draft kept. |
| `BrowseForDirectory` cancelled ("") | No field change. |
| `BrowseForDirectory` error (non-cancel) | Log + no field change; field stays manually editable. |
| `current` path non-existent when Browse clicked | Handled in §3: `DefaultDirectory` left empty so the dialog still opens (at OS default). |
| Save clicked while an update is in-flight | Prevented: Save is disabled (§2.5 gate). |
| Path points to a non-existent / wrong folder | Saved as-is; re-detection simply finds nothing for that backend. No pre-validation gate. |

## §7 Testing

**Vitest** (`SettingsPanel` + i18n):
- Panel loads `GetSettings()` into the draft and renders current path values.
- Editing a path then Save calls `UpdateSettings` with the edited draft (other fields preserved — assert App.Language/Region untouched).
- Cancel/ESC closes without calling `UpdateSettings`.
- Save error keeps the panel open and shows the error label.
- **Save is disabled when an in-flight update exists** (`updates.byGame` has `in_flight`).
- i18n parity: all new `settings.*` keys present + non-empty in zh-TW / zh-CN / en.

**Go**:
- Factor the §3 DefaultDirectory decision into a pure helper, e.g.
  `dialogDefaultDir(current string) string` → returns `current` iff `os.Lstat(current)` is a
  dir, else `""`. Unit-test it (existing dir → itself; missing path → ""; a file → ""). This
  covers BLOCKER-1's logic without a live window.
- `BrowseForDirectory` with `a.ctx == nil` returns `("", nil)` (guard).
- `settingsMu` (§2.5): with CGO off there's no `-race` here (see project memory), so the
  race fix is verified by code review + the §8 manual smoke, not an automated race test.
  Keep the lock discipline simple and reviewable.
- `UpdateSettings` round-trip already covered by `settings_test.go`.
- The live folder dialog itself needs a real Wails window → §8 manual smoke.

## §8 Manual smoke (post-implementation, USER)

1. Click gear → drawer slides in from right; shows current paths.
2. Browse… → native folder dialog opens; pick a folder → field updates.
3. Edit a temp_dir to a folder on a roomier drive; Save → no error; drawer closes.
4. Sidebar re-detects (game list reflects any path change).
5. Reopen settings → values persisted (TOML written). Restart app → still persisted.
6. Cancel/ESC discards edits.

## §9 Files touched

- `internal/app/dialog.go` (new) — `BrowseForDirectory` + `dialogDefaultDir` helper.
- `internal/app/app.go` — `settingsMu sync.RWMutex` guarding `a.settings` + `a.providers`
  per the §2.5 non-reentrant strategy: write-lock ONLY in `UpdateSettings`;
  `constructProviders` + `cachedDetect` stay **lock-free**; single-shot RLock in leaf readers
  (`GetSettings`, `provider`, `byID`, `tempDirFor` settings reads); snapshot-under-RLock-then-iterate in
  `ListGames`/`ListBackends`/`UpdateStatusAll`/`knownBackendIDs`/`scanForRecovery`. No path
  acquires the lock twice. (Plan confirms the exact site list against current app.go.)
- `frontend/src/components/SettingsPanel.vue` — new.
- `frontend/src/components/Topbar.vue` — wire gear `@click="view.openSettings()"`.
- `frontend/src/stores/view.ts` — `settingsOpen` state + `openSettings`/`closeSettings`.
- `frontend/src/stores/games.ts` (or a composable) — extract the shared post-change refresh
  used by Topbar `onRefresh` and the panel Save.
- `frontend/src/components/Topbar.vue` — switch `onRefresh` to the shared refresh.
- `frontend/src/styles/theme.css` — drawer styles (reuse notif-panel patterns).
- `frontend/src/locales/{zh-TW,zh-CN,en}.json` — `settings.*` keys (all three).
- Tests: `frontend/src/__tests__/settings_panel.test.ts`, i18n parity update,
  `internal/app/dialog_test.go` (`dialogDefaultDir` + nil-ctx guard).
- Wails bindings regenerated for `BrowseForDirectory` (`wails generate module` / build;
  `frontend/wailsjs/` is gitignored — binding is a build step, not a committed file).
