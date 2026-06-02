# Omnigate — Per-Game Install Paths & Launcher-Aware Detection (Design)

Date: 2026-06-02
Status: DRAFT (awaiting user review)
Branch: `game-paths/spec`

## §0. Summary

Today the install location is configured **per backend** (one root per
publisher: HoYoPlay, Wuthering Waves, GRYPHLINK), and games are discovered by
scanning known subfolders under that root. This is unintuitive — most visibly
for HoYoverse, where a single HoYoPlay root is shared by Genshin / Star Rail /
ZZZ — and it silently fails when a game is installed anywhere other than the
default/configured root.

This feature reworks install locations to be **per game**:

1. Each game resolves its own install folder (the directory that contains its
   `.exe`) through a resolution chain: **user override → launcher-config
   detection → default-location scan → unresolved**.
2. A **launcher-aware detector** reads each official launcher's own record of
   where its games are installed (registry / AppData), so games are found even
   when installed to a custom drive.
3. The per-game install folder is editable from a **config button in the
   BottomBar, immediately left of the Play button**, via a small popover — and
   it works even when the game isn't detected, so the user can locate it.

The per-backend root setting is removed. Per-backend `temp_dir` (update staging)
is unrelated and stays.

## §1. Current state

**Settings** (`internal/app/settings.go`, schema `version = 1`):
- `Settings.Backends.{Hoyoverse,Kurogames,Hypergryph}.Path` — one root per
  backend. `defaultSettings()` ships `C:\Program Files\HoYoPlay` /
  `…\Wuthering Waves` / `…\GRYPHLINK`.
- Each backend also has `TempDir` (update staging; **out of scope here**).

**Detection** (`internal/providers/*/detect.go`): identical shape across all
three backends — `DetectInstall(ctx, root)` iterates the package's known games
and includes a game when `<root>[/games]/<FolderName>` exists, returning
`core.InstalledGame{GameID, InstallPath}`.

**Wiring**:
- `core.Provider.DetectInstall(ctx)` (method, ctx only) → calls package
  `DetectInstall(ctx, p.settings.Path)` with the provider's configured root.
- `App.cachedDetect(ctx, p)` caches the result; `App.ListGames()` builds
  `GameRow`s, marking a game installed and copying `InstallPath` from detection.
- Downstream consumers of `InstallPath` / the per-game folder: `Launch`,
  `CheckVersion` (reads `<gameDir>/config.ini` etc.), the update pipeline, and
  icon extraction (`/_asset/<backend>/icon/<key>` → `iconext.Extract`). Several
  providers re-derive the game folder internally from `p.settings.Path` (e.g.
  hoyoverse `gameDir(gid)`), **not** from the detected `InstallPath`.

**The empty DetailView**: `frontend/src/components/DetailView.vue` renders an
empty `<div>`; per-game launch/update controls live in `BottomBar.vue`.

## §2. Goals / non-goals

**Goals**
- Per-game install folder, resolved independently for every game.
- Detection that finds games at non-default locations by reading the launcher's
  own install records.
- Per-game editing UI reachable from the Play row; usable to locate undetected
  games.
- Zero-regression migration from the current per-backend roots.

**Non-goals**
- Changing `temp_dir` (stays per-backend).
- Changing the update / launch / version protocols themselves.
- Building out the rest of the DetailView body (only the config button + popover
  are in scope).
- Installing games from scratch / moving game files.

## §3. Data model — Settings schema v2

Bump `Settings.Version` to `2`.

- **Remove** `Backends.*.Path`.
- **Keep** `Backends.*.TempDir`, `App.*`, etc.
- **Add** a per-game table keyed by game ID:

  ```toml
  version = 2

  [games."hoyoverse/genshin"]
  path = 'D:\Games\GenshinImpact'   # user override of the install folder

  [backends.hoyoverse]
  temp_dir = '...'                  # unchanged
  ```

  Go shape (illustrative):
  ```go
  type Settings struct {
      Version  int
      App      AppSettings
      Backends BackendSettings            // Path removed; TempDir kept
      Games    map[string]GameSettings    // key = game ID, e.g. "hoyoverse/genshin"
  }
  type GameSettings struct {
      Path string `toml:"path,omitempty"` // empty → fall through to detection
  }
  ```

Only **overrides** are stored. A game with no entry (or empty `path`) is
resolved by detection. We never persist auto-detected paths except during
migration (§7).

## §4. Path resolution

The resolved install folder for a game is the first of:

1. **User override** — `Settings.Games[id].Path`, if non-empty. (Trusted as-is;
   validity surfaced in the UI, not silently dropped.)
2. **Launcher-config detection** — the backend's `InstallLocator` (§5), if it
   knows this game's path.
3. **Default-location scan** — the existing folder-scan against the **hardcoded
   default root** for that backend (the old `defaultSettings()` paths become
   detection constants, not user settings).
4. **Unresolved** — the game is shown as not-installed with a "Locate…" affordance
   in its config popover.

Resolution is **orchestrated by the App layer** (it owns settings + the provider
registry). The result is a per-game `map[GameID]string` of resolved folders that:
- feeds `ListGames` (installed = resolved ≠ "" and exists), and
- is **injected into the provider** so downstream ops (Launch / CheckVersion /
  update / icon) operate on the resolved folder rather than re-deriving from a
  single root (see §5.3).

## §5. Provider changes

### §5.1 Default scan
`DetectInstall(ctx)` keeps scanning known subfolders, but against a **hardcoded
default root** constant instead of `p.settings.Path` (which is removed). This is
the layer-3 fallback.

### §5.2 `core.InstallLocator` (new, optional)
```go
// Optional capability: read the launcher's own record of where each game is
// installed (registry / AppData), independent of any configured root.
type InstallLocator interface {
    LocateInstalls(ctx context.Context) (map[GameID]string, error)
}
```
- Providers that implement it return game→folder for whatever the launcher has
  recorded as installed (any drive). Best-effort: errors and partial results are
  fine; the App treats a missing entry as "fall through to default scan".
- Providers that don't implement it are simply skipped at layer 2.
- **Per-launcher detail is research (§5.4)** and is intentionally not fixed in
  this spec beyond the interface and fallback contract.

### §5.3 Injecting resolved paths into providers
Providers must operate on the App-resolved per-game folder, not a single root.
Mechanism (to be finalized in the plan): the App pushes the resolved map into
each provider after detection — e.g. `Provider` (optionally) implements
`SetInstallPaths(map[GameID]string)`, and internal helpers like hoyoverse
`gameDir(gid)` read from that map. `PrimaryPath()` (used by `ListBackends`
status + the icon asset handler) is re-expressed in per-game terms or derived
from the resolved map.

### §5.4 Per-launcher detection research (deferred to the plan, first tasks)
Each launcher records install locations differently; this is the uncertain part
and is sequenced first in implementation, **HoYoPlay first** (we have a proven
reference). Each lands behind the `InstallLocator` interface with the
default-scan fallback guaranteeing function if research is incomplete.

- **HoYoPlay (hoyoverse)** — highest confidence. Collapse Launcher reads
  HoYoverse installs (per memory: *trust Collapse as the HoYoverse reference*).
  Source is its config / registry under Cognosphere/HoYoPlay. Research confirms
  exact key/file + format.
- **KRLauncher (kurogames)** — config under `%APPDATA%\KRLauncher\...` (already
  navigated during background research). Light reverse-engineering to find the
  recorded install path.
- **GRYPHLINK (hypergryph)** — config under `%LOCALAPPDATA%\Games\<hash>\…`.
  Light reverse-engineering. Lowest confidence; default-scan + manual override
  cover it until solved.

Per memory `feedback_collapse_reference.md`: trust Collapse for HoYoverse; for
Kuro/Hyper, light live/offline probing is acceptable (same posture as the M2
background research).

## §6. UI — per-game config button + popover

- **Button**: a gear/cog icon button in `BottomBar.vue`'s launch area,
  **immediately left of the Play/Update button**. Visible for the selected game
  in detail view **regardless of installed state** (so undetected games can be
  located).
- **Popover**: opens **above** the button (anchored, lightweight — not a
  full-page panel). Contents:
  - **Install path**: the resolved folder, with a **source badge** — `自動偵測`
    (launcher-config), `預設位置` (default scan), or `手動` (override).
  - An editable text field + **瀏覽…** (directory picker, reuses
    `App.BrowseForDirectory`) to set an override.
  - **重設** — clears the override, reverting to detection.
  - Room for future per-game settings.
  - When unresolved: the popover is the "Locate…" entry point; Browse → sets the
    override → the game becomes installed on save.
- **Save**: persists the override via a focused RPC and re-resolves/refreshes
  **only that game** (re-detect + version), so a path fix takes effect
  immediately without a full app refresh.
- i18n keys for labels/badges across `en` / `zh-TW` / `zh-CN` (parity test).

## §7. Migration (v1 → v2)

A user with a **non-default** backend root must not lose their games when the
root setting is removed. On loading a v1 settings file:

1. For each backend, run the existing default-scan **against the v1 configured
   root** (the value being removed).
2. For every game found, **persist a per-game override** `Games[id].Path =
   <resolved folder>`.
3. Drop the `Backends.*.Path` fields and write `version = 2`.

This guarantees: *every game visible before migration is visible (at the same
path) after migration.* A user on the default root produces overrides equal to
the default-scan results — harmless, and immediately superseded by detection if
they later move a game and clear the override.

(Default-root users could alternatively migrate to **no** overrides; persisting
them is simpler and strictly safe. The plan may choose to skip writing an
override when the resolved path equals the default-scan path, to keep the file
clean. Either is acceptable; not load-bearing.)

## §8. Testing

- **Settings**: v2 round-trip; v1→v2 migration (default root → clean/equal;
  custom root → per-game overrides; partially-installed). Backward-compat load.
- **Resolution**: unit tests for the chain (override wins; locator wins over
  default; default fallback; unresolved). Table-driven with a fake locator.
- **InstallLocator**: per-backend locator tested against **sanitized fixtures**
  of the real launcher config/registry export (no live launcher dependency in
  CI), mirroring the M3.A manifest-fixture approach.
- **Provider path injection**: hoyoverse `gameDir(gid)` (and peers) resolve from
  the injected map; downstream Launch/CheckVersion use it.
- **Frontend**: popover open/close + source badge + browse→override→save flow;
  config button visible when not installed; i18n parity for new keys.
- Whole-repo `go test ./...` green; `npm run build` + vitest green.

## §9. Risks & open questions

1. **Launcher-config formats are unknown until research.** Mitigation: the
   `InstallLocator` interface + default-scan fallback means the feature is fully
   functional (with manual override) even if a launcher's locator is never
   built. HoYoPlay is high-confidence; Kuro/Hyper may ship as default-scan-only
   initially.
2. **Registry access on Windows** (if HoYoPlay stores paths in registry):
   `golang.org/x/sys/windows/registry` is the likely dependency. To confirm in
   research.
3. **Provider path-injection refactor** touches Launch/CheckVersion/update/icon
   call sites that currently re-derive from a single root. This is the largest
   blast radius; the plan must enumerate every `p.settings.Path` / single-root
   derivation and route it through the resolved map.
4. **`PrimaryPath()` semantics** change (no single root). `ListBackends` status
   and the icon asset handler need a per-game-aware replacement.
5. **Multi-game backends growth**: HoYoverse already has 3; the design is
   per-game from the start, so adding games is just more map entries.

## §10. Phasing

Per user direction: full scope (including launcher-aware detection), built game
by game, on `game-paths/spec`, via spec → plan → implementation with subagent
review gates. Implementation order (detail in the plan):

1. Core: settings v2 + migration; resolution chain; `InstallLocator` interface;
   provider path-injection refactor (default-scan only) — **fully working with
   manual override + default scan, no launcher-config yet.**
2. Launcher locators, one at a time: HoYoPlay → KRLauncher → GRYPHLINK.
3. Frontend: config button + popover + per-game RPCs + i18n.
4. Smoke (USER): real installs across backends, custom-location case, migration.
