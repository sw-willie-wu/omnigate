# Omnigate — Per-Game Install Paths & Launcher-Aware Detection (Design)

Date: 2026-06-02
Status: DRAFT v2 (revised after two architect reviews; awaiting user review)
Branch: `game-paths/spec`

## §0. Summary

Today the install location is configured **per backend** (one root per
publisher: HoYoPlay, Wuthering Waves, GRYPHLINK), and games are discovered by
scanning known subfolders under that root. This is unintuitive — most visibly
for HoYoverse, where one HoYoPlay root is shared by Genshin / Star Rail / ZZZ —
and it silently fails when a game is installed anywhere other than the
default/configured root.

This feature reworks install locations to be **per game**:

1. Each game resolves its own install folder (the directory containing its
   `.exe`) through a chain: **user override → launcher-config detection →
   default-location scan → unresolved**.
2. A **launcher-aware detector** reads each official launcher's own record of
   where its games are installed (registry / AppData), so games are found even
   when installed to a custom drive.
3. The per-game install folder is editable from a **config (gear) button in the
   BottomBar, immediately left of the Play button**, via a popover above it —
   working even when the game isn't detected, so the user can locate it.

The per-backend root setting is removed. Per-backend `temp_dir` (update staging)
is unrelated and stays.

## §1. Current state (verified against code)

**Settings** (`internal/app/settings.go`, `version = 1`):
- `Settings.Backends.{Hoyoverse,Kurogames,Hypergryph}.Path` — one root per
  backend. `defaultSettings()` ships `C:\Program Files\HoYoPlay` /
  `…\Wuthering Waves` / `…\GRYPHLINK`. `HoyoverseSettings` also has `Region`.
- Each backend also has `TempDir` (update staging; **out of scope**).
- `LoadSettings` ends with an unconditional `out.Version = 1` (`settings.go:140`);
  `SaveSettings` forces `s.Version = 1` (`:148`); v0→v1 migration projects
  `hoyoplay_path → path` via `rawTOML`/`hoyoverseRawTOML` (`:48-62, :111-120`).

**Detection** (`internal/providers/*/detect.go`) — NOT uniform:
- hoyoverse: `filepath.Join(root, "games", g.FolderName)`; includes a game when
  the **folder** exists (`detect.go:34-35`).
- kurogames: `filepath.Join(root, g.FolderName)` (no `games/`); requires **folder
  AND exe** (`detect.go:35-43`).
- hypergryph: `filepath.Join(root, g.FolderName)` (no `games/`); requires
  **folder AND exe** (`detect.go:31-37`).
- All return `([], nil)` when the root doesn't exist (`detect.go:18`).

**Wiring** (key for §5):
- `core.Provider.DetectInstall(ctx)` (method, ctx-only) calls the package
  `DetectInstall(ctx, p.settings.Path)`.
- `App.cachedDetect(ctx, p)` caches per **BackendID** (`app.go:170-186`);
  `invalidateDetect()` clears the whole map (`app.go:190`).
- `App.ListGames()` builds `GameRow{ID,Backend,DisplayName,Installed,InstallPath}`
  from detection (`app.go:202,222-247`).
- **Consumers of the per-game folder already read `InstallPath` / `DetectInstall`,
  not a single root:**
  - App `gameInstallDir(gid, p)` (`update_handler.go:595-606`) → `p.DetectInstall`
    → feeds `preflightChecks` (`:130`) and volume checks.
  - icon `serveIcon` (`asset_handler.go:82-117`) → `cachedDetect` → `inst.InstallPath`.
  - hoyoverse `gameDir(gid)` (`hoyoverse.go:439-453`) routes through `DetectInstall`
    and **already has an injection seam** `gameDirFn`/`SetGameDirFn`
    (`hoyoverse.go:36,480`, today labelled test-only). Also `tempRootFn`/
    `SetTempRootFn` seam (`:464`).
  - Provider public methods that loop `DetectInstall` results for the gid:
    hoyoverse `Launch` (`:143`); kurogames `CheckVersion`/`Launch`/
    `CheckForUpdateWithProgress`/`RunUpdate` (`kurogames.go:103,134,193,301`);
    hypergryph `CheckVersion`/`Launch`/`installPathFor` (`hypergryph.go:98,111,276`).
- `PrimaryPath()` returns `p.settings.Path`. **Only consumer: `ListBackends`**
  (`app.go:253-296`); `serveIcon` does **not** use it.
- Each provider's `SettingsSchema()` advertises a `path` field
  (hoyoverse `:77`, kurogames `:60`, hypergryph `:60`), rendered by
  `SettingsPanel.vue` as the per-backend path input (`:83-116`).

**The empty DetailView**: `DetailView.vue` renders an empty `<div>`; per-game
controls live in `BottomBar.vue` (a tight `v-if/else-if` chain over
`inFlight`/`availableUpdate`/`predlReady`, `:176-197`, with the SettingsPanel
`anyInFlight`-disabled-save precedent at `SettingsPanel.vue`).

## §2. Goals / non-goals

**Goals**
- Per-game install folder, resolved independently per game.
- Detection that finds games at non-default locations via the launcher's own
  install records.
- Per-game editing UI from the Play row; usable to locate undetected games.
- Zero-regression migration from the current per-backend roots.

**Non-goals**
- `temp_dir` (stays per-backend); update/launch/version protocols.
- Building the rest of the DetailView body (only the gear + popover).
- Installing/moving game files.
- Cross-backend folder disambiguation — overrides are per-game folders; we do not
  detect or resolve two backends claiming the same folder.

## §3. Data model

### §3.1 Settings schema v2
Bump `Settings.Version` to `2`.
- **Remove** `Backends.*.Path`. **Keep** `Backends.*.TempDir`, `HoyoverseSettings.Region`, `App.*`.
- **Add** a per-game override table keyed by game ID:
  ```toml
  version = 2
  [games."hoyoverse/genshin"]
  path = 'D:\Games\GenshinImpact'   # user override of the install FOLDER
  [backends.hoyoverse]
  temp_dir = '...'                  # unchanged
  ```
  ```go
  type Settings struct {
      Version  int
      App      AppSettings
      Backends BackendSettings           // Path removed; TempDir + Region kept
      Games    map[string]GameSettings   // key = game ID; only overrides stored
  }
  type GameSettings struct { Path string `toml:"path,omitempty"` }
  ```
  Only **overrides** are persisted. The old `defaultSettings()` root paths become
  **detection constants** (§5.1), not user settings.

### §3.2 `GameRow` extension (the frontend/data contract)
`GameRow` gains three fields, populated for **every** game (installed or not):
- `ResolvedPath string` — the folder resolution settled on ("" if unresolved).
- `PathSource string` — enum: `override` | `launcher` | `default` | `unresolved`.
- `OverridePath string` — the raw `Settings.Games[id].Path` ("" if none); the
  popover's text field edits **this**, which can differ from `ResolvedPath` when
  an override is invalid (§4.5).

`Installed` keeps its meaning but is now defined by resolution (§4).

## §4. Path resolution

The App layer owns resolution (it holds settings + the provider registry). For
each game the resolved folder + source is the first that yields a path:

1. **Override** — `Settings.Games[id].Path` if non-empty → source `override`.
   Returned **verbatim** (not silently dropped); validity handled in §4.5.
2. **Launcher-config** — the backend's `InstallLocator` (§5.2) entry for this
   game, **only if the path stat-exists** → source `launcher`. A stale/missing
   locator path falls through to layer 3.
3. **Default scan** — the backend's `DefaultScan` (§5.1) entry → source `default`.
4. **Unresolved** — `ResolvedPath=""`, source `unresolved`.

`Installed` = `ResolvedPath != "" && <ResolvedPath stat-exists>`. (An override
pointing nowhere yields `ResolvedPath != ""` but `Installed=false` — see §4.5.)

### §4.5 Override validity
- Resolution returns the override path **verbatim** so the popover can always
  show "your override: X".
- `Installed` is gated on the override folder **existing** (`os.Stat` dir). Exe
  presence is **not** required for `Installed` (matches today's hoyoverse
  folder-only check; avoids per-backend exe coupling in the App layer).
- A set-but-nonexistent override is a distinct UI state: source `override` +
  an `invalid` flag (badge `手動・找不到`). The App marks this by `PathSource =
  "override"` with `Installed=false` and `ResolvedPath` = the (missing) override.
- Launch/Update on an unresolved-or-invalid game refuse exactly as an
  un-installed game does today (the buttons are already gated on `Installed`).

### §4.6 Resolution lifecycle & locking
- Resolution runs (a) at `constructProviders` time, (b) on `RefreshGame`, (c)
  after `SetGameOverride`/`ClearGameOverride`. It reads `Settings.Games` under
  `settingsMu` RLock and the per-backend `DefaultScan`/`LocateInstalls` results.
- The App caches `DefaultScan`/`LocateInstalls` per **BackendID** (reusing the
  existing `detect` cache granularity). Per-game override changes invalidate the
  **affected backend's** cache and re-resolve that backend (re-resolving ≤3 games
  is cheap); `invalidateDetect()` stays whole-map for the global Refresh.
- The App holds the authoritative `map[GameID]{path,source}` and **injects the
  resolved folder map into each provider** (§5.3) at the same point it builds
  providers (under the `constructProviders` write lock) and after each
  per-game refresh.

## §5. Provider changes

### §5.1 `DefaultScan` (layer 3)
Each provider exposes a default-root folder scan that preserves its **existing,
non-uniform** join + existence semantics (hoyoverse `root/games/Folder`,
folder-only; kuro/hyper `root/Folder`, folder+exe), against a **hardcoded
default-root constant** (the former `defaultSettings()` value). Returns
`map[GameID]string` of existing folders. This replaces the `settings.Path`-fed
`DetectInstall`.

### §5.2 `core.InstallLocator` (layer 2, optional)
```go
type InstallLocator interface {
    LocateInstalls(ctx context.Context) (map[GameID]string, error)
}
```
- Reads the launcher's own record of installed games (any drive). Best-effort;
  partial/empty/error → those games fall through. **App stat-validates every
  returned path** before accepting it at layer 2.
- Each implementation MUST take its **source as an injectable parameter** — a
  root dir for file/AppData-based launchers, or a `registry-reader func` for
  registry-based ones — so it is unit-testable against sanitized fixtures with no
  live launcher (§9). Providers without a locator are skipped at layer 2.
- Per-launcher format is research (§5.5).

### §5.3 Resolved-path injection (single mechanism)
**Decision:** the App is the single source of truth. It computes the resolved
`map[GameID]string` and pushes it into each provider via a production
`SetResolvedPaths(map[GameID]string)` (generalising hoyoverse's existing
`gameDirFn` seam to all providers, and **subsuming** `gameDirFn` — no parallel
mechanism). Then:
- Provider internal/public per-game-folder lookups (hoyoverse `gameDir`; the
  kuro/hyper `DetectInstall`-loop sites in Launch/CheckVersion/Update) read the
  injected map instead of `DetectInstall(ctx, settings.Path)`.
- App `gameInstallDir` (`update_handler.go:595`) reads the App's resolved map
  directly — its independent `DetectInstall` call is deleted.
- `serveIcon` already reads `InstallPath` from `cachedDetect`; once `cachedDetect`
  yields resolved entries, it is **unchanged**.
- `core.Provider.DetectInstall(ctx)` is **repurposed** to "report my resolved
  installed games" (entries from the injected map that stat-exist), so
  `cachedDetect`/`ListGames` keep working with minimal change.
- **Out of scope, do not reroute:** `tempRootFn`/`tempDirFor`/sidecar/staging
  paths and `scanForRecovery` — they operate on the temp root, never the install
  folder. (Note: `preflightChecks`/`validateSameVolume` compare temp-vol vs
  game-vol, where game-vol comes from `gameInstallDir`, so they are affected
  **transitively** and correctly once `gameInstallDir` reads the resolved map.)

### §5.4 `SettingsSchema` & `PrimaryPath`
- Remove the `path` `SettingField` from each provider's `SettingsSchema()` (the
  per-backend path input goes away; §7 UI).
- `PrimaryPath()` (single root) is removed/repurposed; its only consumer is
  `ListBackends` (§6.2).

### §5.5 Per-launcher locator research (plan tasks, sequenced first)
Behind `InstallLocator` with the default-scan fallback guaranteeing function:
- **HoYoPlay** — highest confidence; Collapse Launcher reads HoYoverse installs
  (memory: *trust Collapse for HoYoverse*). Likely registry / Cognosphere config.
- **KRLauncher** — `%APPDATA%\KRLauncher\...` (navigated during bg research).
- **GRYPHLINK** — `%LOCALAPPDATA%\Games\<hash>\…`. Lowest confidence;
  default-scan + override cover it until solved.
Light probing acceptable for Kuro/Hyper (memory `feedback_collapse_reference.md`).

## §6. App RPC surface & backend status

### §6.1 New per-game RPCs
- `SetGameOverride(gameID, path string) (GameRow, error)` — writes
  `Settings.Games[id].Path`, persists, invalidates the backend's cache,
  re-resolves, returns the updated row.
- `ClearGameOverride(gameID string) (GameRow, error)` — deletes the override,
  invalidates + re-resolves so the badge flips to `launcher`/`default`/
  `unresolved` synchronously.
- `RefreshGame(gameID string) (GameRow, error)` — per-game re-resolve + re-detect.
These avoid the whole-blob `UpdateSettings` and the global `Refresh()`.

### §6.2 `ListBackends` status, redefined
The old states (`path_unset`/`launcher_missing` from a single root) are
meaningless. Redefine backend status as an **aggregate of per-game resolution**:
`ok` if any game resolves+exists, else `empty` (and `error` on locator failure).
Update `BackendStatus` producers and any consumer.

## §7. UI

### §7.1 Per-game config button + popover
- **Gear button** in `BottomBar.vue`'s launch-area, **immediately left of the
  Play/Update button**, with a stable position across all five launch-area
  states (Play/Update/ApplyPredl/download-progress/apply-progress). Visible for
  the selected game **regardless of `Installed`** (so undetected games can be
  located). `data-testid` on the gear and popover root.
- **Popover** opens **above** the gear (anchored, lightweight; follows the
  SettingsPanel listener-lifecycle: window `keydown` ESC bound on open / removed
  on close + `onUnmounted`; **click-away** closes; closes when the selected game
  changes; single instance). Contents:
  - **Install path** field bound to `OverridePath`, with **瀏覽…**
    (`App.BrowseForDirectory`) and **重設** (clear override).
  - **Source badge** from `PathSource`: `自動偵測` (launcher), `預設位置`
    (default), `手動` (override), `手動・找不到` (override invalid), and a
    Locate prompt when `unresolved`.
  - **Save** is **disabled while that game has `in_flight != null`** (reuse the
    SettingsPanel `anyInFlight` pattern + tooltip) — changing an install path
    under a running apply is unsafe.
- **Save flow (order matters):** call `SetGameOverride` → update that row's
  `Installed`/`ResolvedPath`/`PathSource`/`OverridePath` from the returned
  `GameRow` → **then** `refreshVersionFor(id)` + `loadAssetsFor(id)`. Because the
  `*For` helpers early-return on `!installed` (`games.ts:60,72`), the row's
  `installed` must be updated first (or relax those guards).

### §7.2 SettingsPanel
Remove the three per-backend `Backends.*.Path` rows (`SettingsPanel.vue:83-116`);
keep the `TempDir` rows. Update `settings_panel.test.ts` (it currently asserts
`findAll('input')[0]` is the HoYoverse path).

### §7.3 i18n keys (all of `en`/`zh-TW`/`zh-CN`; parity test gates)
`gamecfg.path_label`, `.browse`, `.reset`, `.locate`, badges `.src_launcher`/
`.src_default`/`.src_override`/`.src_invalid`, `.save`, `.save_disabled_inflight`.

## §8. Migration (v1 → v2)

On loading a v1 file (no `Games`, has `Backends.*.Path`):
1. For each backend, run that **backend's own scan shape** (hoyoverse with the
   `games/` segment; kuro/hyper without) against the **v1 configured root**.
2. For each game found, write a per-game override **only if the resolved path
   differs from the new default-scan path**. (A default-root user gets **no**
   overrides — which is behaviorally required: persisting them would pin the game
   and defeat post-move re-detection. A custom-root user gets overrides that
   preserve their layout.)
3. **Offline-drive safety:** if the v1 root is custom (≠ default) but currently
   un-stat-able (external drive unplugged), do **not** silently drop it —
   seed overrides from `root/[games/]FolderName` for the backend's known games
   **without** requiring the stat to succeed, so configuration is not lost.
4. Drop `Backends.*.Path`; write `version = 2`.

Touch-points: `rawTOML`/`hoyoverseRawTOML` (add `Games`, drop per-backend
`Path`), the hardcoded `out.Version = 1` / `s.Version = 1` (become 2 with a
v1-detection branch), and `defaultSettings()` (loses the three `Path:` values →
detection constants per §5.1). Preserve the existing v0→v1 `hoyoplay_path`
projection.

Guarantee: *every game visible (and at what path) before migration is visible
after* — including custom-root and offline-drive cases.

## §9. Testing

- **Settings**: v2 round-trip; v1→v2 migration (default root → no overrides;
  custom root → overrides; offline custom drive → overrides seeded; partial
  install); v0→v1 still works; backward-compat load.
- **Resolution**: table-driven over the chain (override-valid wins; override-
  invalid → `Installed=false`, source `override`; locator-valid wins over
  default; locator-stale → falls to default; default; unresolved) with a fake
  locator + fake default-scan.
- **Locators**: each tested against sanitized fixtures via its injectable source
  (root dir / registry-reader func) — no live launcher in CI (M3.A fixture
  precedent).
- **Provider injection**: hoyoverse `gameDir` and the kuro/hyper Launch/
  CheckVersion sites resolve from the injected map; `gameInstallDir` reads the
  App map; `serveIcon` unchanged.
- **RPCs**: `SetGameOverride`/`ClearGameOverride`/`RefreshGame` return updated
  rows; backend cache invalidation re-resolves the right backend.
- **Frontend**: popover open/ESC/click-away/close-on-game-change; source badge
  per state; browse→override→save flow + per-game refresh; gear visible when not
  installed; Save disabled while that game in-flight; SettingsPanel path rows
  gone; i18n parity.
- Whole-repo `go test ./...`, `npm run build`, vitest green.

## §10. Risks

1. **Launcher-config formats unknown until research.** `InstallLocator` +
   default-scan fallback ⇒ functional (with manual override) even if a locator
   is never built. HoYoPlay high-confidence; Kuro/Hyper may ship default-scan-only.
2. **Registry access** (if HoYoPlay uses registry): `golang.org/x/sys/windows/
   registry`; the locator's registry-reader is injected for testability.
3. **Injection refactor** touches the enumerated provider sites + `gameInstallDir`
   — smaller than it first appears because consumers already read
   `InstallPath`/`DetectInstall`; the lever is making those return resolved paths.
4. **`ListBackends` status** model changes (§6.2) — frontend currently has no
   consumer (the status pills were removed), so blast radius is small, but the
   status producer/`BackendStatus` shape changes.

## §11. Phasing

Full scope, built game-by-game on `game-paths/spec`, via spec → plan →
implementation with subagent review gates. Order:

1. **Core + contract (test-verified):** settings v2 + migration; resolution chain
   + override validity; `DefaultScan`; `InstallLocator` interface (no impls yet);
   resolved-map injection + `gameInstallDir`/`DetectInstall` rewiring; `GameRow`
   extension; the `SetGameOverride`/`ClearGameOverride`/`RefreshGame` RPCs;
   `ListBackends` status. **Verifiable by Go tests; no user-visible override yet
   (the UI is Phase 3).**
2. **Locators, one at a time:** HoYoPlay → KRLauncher → GRYPHLINK.
3. **Frontend:** gear + popover + per-game refresh wiring + SettingsPanel path
   removal + i18n. **This is when manual override becomes user-usable** — so the
   "manual override is the safety net" guarantee is only live from Phase 3 on.
4. **Smoke (USER):** real installs per backend; custom-location; migration
   (default/custom/offline); locate-undetected; in-flight Save guard.
