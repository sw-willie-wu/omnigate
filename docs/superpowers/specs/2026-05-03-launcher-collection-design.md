# launcher-collection — Design Spec

**Date**: 2026-05-03
**Status**: Approved (brainstorm) → ready for plan
**License**: AGPL-3.0
**Scope**: Phase 1 implementation; architecture extensible to Phase 2-4.

---

## 1. Overview

A unified desktop launcher that integrates 6 Chinese gacha / live-service games whose official launchers all look similar but require switching between four separate apps:

| Backend (Code / 中文)        | Games (Code / 中文)                                                                                          |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `hoyoverse` / 米哈遊         | `genshin` / 原神 ; `starrail` / 崩壞：星穹鐵道 ; `zzz` / 絕區零                                              |
| `kurogames` / 庫洛           | `wutheringwaves` / 鳴潮                                                                                      |
| `hypergryph` / 鷹角          | `endfield` / 明日方舟：終末地                                                                                |
| `perfectworld` / 完美世界    | `nte` / 異環                                                                                                 |

The launcher reads each backend's official API/manifest, executes the game `.exe` directly (skipping the official launcher window), and reimplements version-check / download / patch-apply / pre-download in our own UI — using `hpatchz` for HDiff and bundled official binaries (e.g., `KRInstallExternal.exe`, `NTEGlobalUpdate.exe`) where required.

### Goals

1. **One UI** replacing the 4 official launcher chrome experiences.
2. Cover the four core operations: **launch / version-check / update / pre-download**.
3. **No fresh-install logic** — when a game is not installed, redirect to the official launcher (mixed approach, "C" path).
4. Architecture supports Phase 2 (open-source release with `kurogames`, then `hypergryph`, `perfectworld`).

### Non-goals (Phase 1)

- No news / announcement feed (banner art carries identity; no ticker).
- No social / friends / cloud-save.
- No fresh install. No anti-cheat circumvention.
- No region toggling at runtime — Global only, region is a per-provider config field.

---

## 2. Scope

### Phase 1 (B-scope, C-architecture)

| Aspect              | Phase 1                                                                                          |
| ------------------- | ------------------------------------------------------------------------------------------------ |
| **Audience**        | Self + close friends                                                                             |
| **Region**          | Global only (`hyp_global` / `_global` variants)                                                  |
| **Provider**        | `hoyoverse` end-to-end (covers 3 games)                                                          |
| **Languages**       | `zh-TW` + `en` (LocalizedString supports CJK / Latin / future ja/ko)                             |
| **Install**         | Read-only — detect existing installs only                                                        |
| **Window**          | Frameless, fixed 1280×720 (16:9), minimize + close only                                          |
| **Estimate**        | 3-4 weeks (leveraging Collapse Launcher as protocol reference)                                   |

### Phase 2-4 (post-Phase 1)

- Phase 2: `kurogames` provider (鳴潮); add `zh-CN` locale.
- Phase 3: `hypergryph` provider (終末地). Less community reference — protocol capture required.
- Phase 4: `perfectworld` provider (異環). PatcherSDK reverse engineering required.

Each subsequent provider is a new module; the `Provider` interface and frontend stay unchanged.

---

## 3. Tech Stack

| Layer       | Choice                       | Rationale                                                  |
| ----------- | ---------------------------- | ---------------------------------------------------------- |
| Desktop     | **Wails 2** (frameless mode) | ~10 MB binary, system WebView2, Go backend                 |
| Backend     | **Go 1.22+**                 | goroutines for parallel I/O; easy to translate Collapse C# |
| Frontend    | **Vue 3** + Composition API  | TypeScript, Vite, Pinia                                    |
| Patching    | `hpatchz.exe` (bundled)      | Same algorithm HoYoverse uses; AGPL-compatible             |
| Protobuf    | `protoc-gen-go`              | Sophon manifest decoding                                   |
| HTTP        | Go `net/http` + `golang.org/x/sync/errgroup` | parallel downloads with cancel propagation |
| TOML        | `pelletier/go-toml/v2`       | settings + state files                                     |
| Logging     | `log/slog` (Go stdlib 1.21+) | structured JSON logs, level configurable                   |

### Window config

```go
err := wails.Run(&options.App{
    Title:         "launcher-collection",
    Width:         1280,
    Height:        720,
    Frameless:     true,
    DisableResize: true,
    // ...
})
```

Drag region: topbar via CSS `-webkit-app-region: drag` with buttons opted out via `no-drag`.

Window controls: minimize / close (no maximize) — call `wailsjs/runtime` from Vue:
```ts
import { WindowMinimise, Quit } from 'wailsjs/runtime';
```

---

## 4. Architecture

### Directory layout

```
launcher-collection/
├── frontend/                      # Vue 3 + TS + Vite
│   ├── src/
│   │   ├── App.vue
│   │   ├── components/            # SidebarRow, GridCard, BottomBar, etc.
│   │   ├── stores/                # Pinia stores (games, settings, view-state)
│   │   ├── locales/{en,zh-TW}.json
│   │   └── views/                 # DetailView, GridView
│   ├── package.json
│   └── vite.config.ts
├── internal/
│   ├── app/
│   │   ├── app.go                 # Wails struct, exposed methods
│   │   ├── settings.go            # settings.toml load/save
│   │   ├── state.go               # state.toml load/save
│   │   └── registry.go            # Provider registry
│   ├── core/
│   │   ├── provider.go            # Provider interface + types
│   │   ├── http/                  # parallel range download, retry
│   │   ├── patch/                 # hpatchz wrapper
│   │   └── integrity/             # MD5/SHA1 verify
│   └── providers/
│       ├── hoyoverse/             # phase 1
│       ├── kurogames/             # phase 2 (stub)
│       ├── hypergryph/            # phase 3 (stub)
│       └── perfectworld/          # phase 4 (stub)
├── bin/hpatchz.exe                # bundled patch tool
├── main.go
├── wails.json
├── go.mod
├── LICENSE                        # AGPL-3.0
└── docs/superpowers/specs/...
```

### Runtime data flow

```
┌──────────────────────────────────────────────────────┐
│  Vue Frontend                                        │
│  - reactive game list (Pinia)                        │
│  - hero bg img/video swap on selection               │
│  - i18n via vue-i18n                                 │
└────────────────┬─────────────────────────────────────┘
                 │ Wails IPC: command + event bus
┌────────────────┴─────────────────────────────────────┐
│  Go Core                                             │
│                                                      │
│  app::commands  (Wails-exposed)                      │
│   ├── ListInstalledGames() → []InstalledGame          │
│   ├── CheckVersion(gameID) → VersionInfo              │
│   ├── PlanUpdate(gameID, kind) → UpdatePlan           │
│   ├── StartDownload(planID) [streams progress]        │
│   ├── ApplyUpdate(planID)                             │
│   ├── Launch(gameID, opts) → Pid                      │
│   └── OpenOfficialForInstall(backendID)               │
│                                                      │
│  Provider registry  (compile-time via build tags)    │
│   └── map[BackendID]Provider                          │
│                                                      │
│  Providers (each its own module)                     │
└──────────────────────────────────────────────────────┘
```

### Build tags for provider selection

```go
//go:build phase1 || hoyoverse
// +build phase1 hoyoverse

package hoyoverse
```

Phase 1 build: `wails build -tags phase1`. Adds providers progressively in later phases.

---

## 5. Provider Trait

```go
type Provider interface {
    ID() BackendID                         // "hoyoverse"
    DisplayName() LocalizedString          // {"zh-TW":"米哈遊","en":"HoYoverse"}
    Games() []GameDescriptor               // games this backend hosts
    SettingsSchema() []SettingField        // for schema-driven settings UI

    DetectInstall(ctx context.Context) ([]InstalledGame, error)
    GetIcon(ctx context.Context, gid GameID) (string /* file path or url */, error)
    GetBackgrounds(ctx context.Context, gid GameID) ([]Background, error)

    CheckVersion(ctx context.Context, gid GameID) (VersionInfo, error)
    PlanUpdate(ctx context.Context, gid GameID, kind PlanKind) (UpdatePlan, error)

    Download(ctx context.Context, plan UpdatePlan, progress chan<- ProgressEvent) error
    Apply(ctx context.Context, plan UpdatePlan, progress chan<- ProgressEvent) error

    Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
    OpenOfficialForInstall(ctx context.Context) error
}
```

`PlanKind` values: `PlanUpdate` (current → latest) and `PlanPredownload` (current → next-version, defer apply). The same `Download` and `Apply` paths handle both — apply is gated separately.

`Background.Type` enum: `IMAGE` / `VIDEO` (matches HoYoverse `BACKGROUND_TYPE_*`). Frontend honors `settings.banner_animation_pref` (`always-static` / `video-when-available` / `never`).

---

## 6. Data Model

### Core types

```go
type BackendID string
type GameID string                         // "hoyoverse/genshin"
type LocalizedString map[string]string     // {"zh-TW": "原神", "en": "Genshin Impact"}

type GameDescriptor struct {
    ID                GameID
    Backend           BackendID
    DisplayName       LocalizedString
    SupportedRegions  []string             // ["global"]; phase 2 may add "cn"
}

type InstalledGame struct {
    GameID         GameID
    InstallPath    string
    CurrentVersion string
}

type VersionInfo struct {
    Current      string
    Latest       string
    Predownload  *PredownloadInfo          // nil if not available
}

type UpdatePlan struct {
    Kind          PlanKind
    TargetVersion string
    Files         []DownloadFile
    TotalBytes    uint64
}

type DownloadFile struct {
    URL      string
    DestPath string
    Size     uint64
    Hash     FileHash                      // {algo: "md5"|"sha1", value: hex}
    IsHDiff  bool                          // apply via hpatchz
}

type Background struct {
    ImageURL string
    VideoURL string                        // empty if no video
    Type     BackgroundType                // IMAGE | VIDEO
}
```

### Disk layout (portable mode)

```
launcher-collection/
├─ launcher-collection.exe
├─ settings.toml          # human-editable
├─ state.toml             # auto-written runtime state
├─ downloads/
│   └─ <backend>-<game>-<version>/
│       ├─ plan.json
│       └─ <patch / chunk files>
├─ cache/
│   └─ banners/<game_id>.<ext>  + .etag
├─ logs/
└─ bin/
    └─ hpatchz.exe
```

Portable note: must be installed to a user-writable location (not `C:\Program Files\` — UAC blocks writes).

### `settings.toml` example

```toml
[app]
language = "zh-TW"
banner_animation_pref = "video-when-available"   # | "always-static" | "never"
show_technical_info = false                      # toggles biz IDs / paths in UI

[backends.hoyoverse]
hoyoplay_path = "C:/Program Files/HoYoPlay"
region = "global"

[games."hoyoverse/genshin"]
launch_extra_args = []
```

### Schema-driven settings UI

```go
func (p *HoYoversePvd) SettingsSchema() []SettingField {
    return []SettingField{
        SettingPath("hoyoplay_path", t("HoYoPlay 安裝資料夾", "HoYoPlay install folder")),
        SettingSelect("region", t("區域", "Region"), []string{"global", "cn"}),
    }
}
```

Frontend renders a generic form from this schema; new providers in Phase 2-4 plug in without UI changes.

### State machine (per game)

```
[未偵測到]
    │ DetectInstall
    ▼
[Installed] ──CheckVersion──▶ [UpToDate]
    │                              ▲
    │                              │
    ├─▶ [UpdateAvailable]          │
    │      │ PlanUpdate(Update)    │
    │      ▼                       │
    │   [PlanReady]                │
    │      │ Download              │
    │      ▼                       │
    │   [Downloaded]               │
    │      │ Apply                 │
    │      └─────────────────────▶ ┘
    │
    └─▶ [PredownloadAvailable]
          │ PlanUpdate(Predownload)
          ▼
       [PlanReady] → Download → [Predownloaded] ─(版本上線後)→ Apply
```

---

## 7. HoYoverse Provider (Phase 1 deep dive)

Reference implementation: **Collapse Launcher** (AGPL-3.0). We translate its protocol behavior to Go; we do **not** copy code (clean enough re-impl). Project license matches AGPL.

### 7.1 API endpoints

```
Base: https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/

GET getGames?launcher_id=VYTpXlbWo8&language=zh-tw
   → game list with display name + icon URL + logo URL

GET getAllGameBasicInfo?launcher_id=VYTpXlbWo8&language=zh-tw&game_id=<id>
   → backgrounds[] (image + video URLs), icons

GET getGameContent?launcher_id=VYTpXlbWo8&language=zh-tw&game_id=<id>
   → banners[] (carousel), posts[] (announcements — we ignore)

GET getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=<biz_id>
   → main_package + pre_download (URL + size + md5)
   → tag.version_chunks[] (Sophon path)
```

### 7.2 Identifiers

| Game           | `game_id`    | `biz`            | Game folder under `HoYoPlay/games/` | Game `.exe`           |
| -------------- | ------------ | ---------------- | ----------------------------------- | --------------------- |
| 原神           | `gopR6Cufr3` | `hk4e_global`    | `Genshin Impact game`               | `GenshinImpact.exe`   |
| 崩壞：星穹鐵道 | `4ziysqXOQ8` | `hkrpg_global`   | `Star Rail Games`                   | `StarRail.exe`        |
| 絕區零         | `U5hbdsT9W7` | `nap_global`     | `ZenlessZoneZero Game`              | `ZenlessZoneZero.exe` |

`launcher_id` (Global launcher): `VYTpXlbWo8`.

### 7.3 Two download protocols

HoYoverse currently uses two protocols simultaneously across games:

**Path A — Legacy direct URLs**
- API returns archive URLs (.zip / .7z) + .hdiff per from→to version.
- Plan file: `[{url, dest, size, md5, is_hdiff}, ...]`
- Apply: extract archives, run `hpatchz` for hdiff, atomic rename.

**Path B — Sophon (chunked, protobuf)**
- API returns `manifest.url` (protobuf-encoded chunk list).
- Each chunk has `(url, offset_in_target, size, sha1)`.
- Plan file lists chunks; "apply" reassembles target files from chunks.
- Implementation: `protoc-gen-go` for schema; community-documented `.proto` definitions.

Phase 1 implementation order: implement legacy first (simpler to test), Sophon as Phase 1.5 if active games require it. (Likely both are required — Sophon is the newer default for full installs and large updates.)

### 7.4 Workflow per game

```
DetectInstall:
  1. Read settings.backends.hoyoverse.hoyoplay_path
  2. Parse <hoyoplay>/config.ini — verify cps=hyp_hoyoverse, primary_game=hyp_global
  3. Enumerate <hoyoplay>/games/*; map folder name → game_id via lookup table
  4. Read each game's local version (location TBD during impl: likely <game>/config.ini or game data file)

CheckVersion:
  GET getGamePackages with game's biz_id
  → parse current vs latest vs pre_download

PlanUpdate(Update | Predownload):
  Decide path A or B based on response shape
  Build UpdatePlan; persist plan.json under downloads/

Download:
  Parallel workers (4 — fixed in phase 1; setting in phase 2)
  HTTP range requests; resumable on disconnect
  Per-file md5/sha1 verify; retry up to 3 on hash mismatch

Apply:
  for each file in plan:
    if hdiff:
      run "bin/hpatchz.exe <old> <hdiff> <new.tmp>"; verify hash; atomic rename
    elif full_replace:
      verify staging hash; atomic move
    elif chunk (Sophon):
      assemble chunks into target; verify final hash
    elif delete:
      rm
  Commit: write new version → state.toml.<game>.current_version

Launch:
  exec.Command(<install>/<game>.exe)
  Working dir = install path
  Optional args from settings.games.<game>.launch_extra_args
  Spawned, no stdout capture, no inject (avoid anti-cheat false positives)
  Return pid; persist last_launched_at to state.toml
```

### 7.5 Phase 1 timeline

| Week | Work                                                          |
| ---- | ------------------------------------------------------------- |
| 1    | Project scaffold (Wails + Go modules + Vue), Provider trait, settings/state TOML, frontend chrome (immersive UI) |
| 2    | DetectInstall + CheckVersion (read-only, no downloads). HoYoverse API translated from Collapse. UI: 6 games visible with versions + statuses. |
| 3    | Download (legacy URL path), Apply (full-replace + hdiff). Real Genshin update test on willie's machine. |
| 4    | Pre-download flow. Sophon path if any active game requires it. Error handling + integrity passes. UI polish + i18n keys. |

If Sophon path needed: +1-2 weeks. Total realistic: **3-6 weeks**.

---

## 8. UI Design

### 8.1 Aesthetic direction

**Operator console + immersive bg.** Distinguishing principle: we replace 4 marketing-heavy launcher chromes with a unified utility-feel chrome that lets the game's own banner art carry visual identity.

| Element             | Choice                                                                |
| ------------------- | --------------------------------------------------------------------- |
| **Background**      | Full-bleed banner from API (image or video). Cross-fade on game switch. |
| **Chrome**          | Sidebar / topbar / footbar with semi-transparent dark + 3px blur.      |
| **Top scrim**       | Separate overlay layer 130px tall (`linear-gradient` from rgba(0,0,0,0.85) to 0). Smooth into banner; no hard band. |
| **Display font**    | Unbounded (Latin display) + Noto Sans TC (CJK).                       |
| **Body font**       | JetBrains Mono.                                                       |
| **Material icons**  | Material Symbols Outlined (translate / grid_view / settings).         |
| **Accent**          | Amber `#d6b04b` — used minimally (active row border, launch button bg, subtle highlights). |
| **Status colors**   | ready `#8bc472` / update `#e89d5a` / predownload `#7aabd6` / downloading `#b8b8c4` / error `#d66666`. |

### 8.2 Layout

```
┌────────────────────┬──────────────────────────────────────┐
│ ⟨   sidebar  ⟩     │                  topbar              │  ◀ topbar in main col only
│  (collapsible      ├──────────────────────────────────────┤
│   280px ↔ 64px)    │                                      │
│                    │           main (banner)              │
│ • icon  原神       │                                      │
│ • icon  崩鐵       │                                      │
│ ─ ─ ─              │  [就緒] v5.5.0 · 上次啟動 …          │
│   米哈遊           │                                      │   ⇣ bottom-bar
│                    │                          [▶ 開始遊戲]│   ⇣ absolute
├────────────────────┴──────────────────────────────────────┤
│                          footbar                          │
└───────────────────────────────────────────────────────────┘
```

- Sidebar spans rows 1+2 (extends to top, no chrome above it).
- Topbar lives only in the main column, holds toolbar (translate / grid-view / settings / minimize / close).
- Footbar full-width.
- 16:9 locked at runtime via Wails fixed window size; mockup uses CSS `width: min(100vw, 100vh*16/9)` for parity.

### 8.3 Two display modes

| Mode       | Use                                                                       |
| ---------- | ------------------------------------------------------------------------- |
| **Sidebar** | Default. Sidebar shows 6 games grouped by publisher. Main shows selected game's banner + bottom-bar (status + LAUNCH). |
| **Grid**   | Browsing. Sidebar hidden; main is a card grid. No LAUNCH button (browse-only). Click any card → switch to Sidebar mode + select that game. |

Toggle: single icon button (`grid_view`) in topbar; active when grid mode is on.

### 8.4 Interaction details

- **Sidebar collapse**: chevron at top of sidebar; 280 ↔ 64. Game icons stay anchored at the same x-coordinate during the 0.25s transition (no horizontal jumping).
- **Language toggle**: single icon button (`translate`); cycles `zh-TW` ↔ `en`. All `data-zh-TW`/`data-en` elements update; layout dimensions stay identical (verified via Playwright: 0px diff on key elements).
- **Background cross-fade**: 2 stacked `<img>` elements + 1 `<video>` overlay. `setBg(url, fallback, videoUrl)` swaps them with 0.6s opacity transition.
- **Drag region**: topbar via `-webkit-app-region: drag`; buttons opted out via `no-drag`. Only takes effect in Wails (browser ignores).
- **Window controls**: `─` (minimize) and `×` (close) at far right of topbar. Hover changes color only (no background fill); close hover is `#e85555`.
- **Chrome legibility on bright banners**: top-fade overlay 130px tall + per-icon `text-shadow: 0 1px 3px rgba(0,0,0,0.6)`. Verified passing on white / pastel banner art.

### 8.5 Icon strategy

- **Game icons** (sidebar + grid card): from each provider's `GetIcon(gid)`. HoYoverse: from `getGames` API `display.icon.url`. Other providers (Phase 2-4): provider-specific.
- **Toolbar icons**: Google Material Symbols Outlined.
- **Window controls (─, ×)**: text characters (avoids font dependency for the most-clicked controls).

### 8.6 Layout stability under language switch (verified)

Hard rules to prevent reflow on locale change:
1. All translatable text elements have explicit pixel `line-height` (CJK and Latin glyph metrics differ enough to shift layout otherwise).
2. Sidebar group-label text wrapped in inner `<span>` with `min-width: 100px`; outer dashed line via `::after flex: 1` keeps separator position constant.
3. Game-name + status-mini have `white-space: nowrap; overflow: hidden; text-overflow: ellipsis`.
4. Toolbar buttons have explicit `width` (not `min-width`).
5. Launch button has `min-width: 200px`.
6. Game-row has `min-height: 56px`.
7. Hero-subtitle has `nowrap + ellipsis`.
8. Breadcrumb (when present) has `nowrap + ellipsis + line-height`.

---

## 9. Error Handling, Integrity, Testing

### 9.1 Error taxonomy

```go
type ProviderErr int
const (
    ErrNetwork       ProviderErr = iota  // retry with backoff
    ErrCDNGone                            // log + UI "資源已下架"
    ErrManifestParse                      // bug — log + report
    ErrChecksumFail                       // retry the single file
    ErrPatchApplyFail                     // restore .bak; user-facing detail
    ErrDiskFull                           // surface; pause queue
    ErrPermission                         // tell user to choose writable path
    ErrLaunchFail                         // surface OS error
)
```

UI: failed-state row → red marker; selected game → red `[錯誤]` pill in stats line + 1-line summary + "重試 / 詳情" buttons.

### 9.2 Integrity protocol

```
download phase:
  - per-file hash check (md5 / sha1 per provider spec)
  - retry up to 3 on mismatch
  - persist plan.json + per-file .meta (URL, size, expected hash, status)

apply phase:
  - hdiff: verify old_file → run hpatchz → verify new_file → atomic rename
  - full replace: verify staging → atomic move
  - chunk (Sophon): assemble → verify reassembled file → atomic move
  - any failure on a file → restore .bak (or refetch); other files unaffected

commit phase:
  - only after ALL files apply OK → write new version to state.toml
  - mid-apply crash → state.toml still old → game still launches normally
  - launcher startup: scan downloads/; surface incomplete plans for resume/cancel
```

### 9.3 Testing

| Layer                     | Tools                                  | Focus                                                                       |
| ------------------------- | -------------------------------------- | --------------------------------------------------------------------------- |
| Provider unit             | Go `testing` + `httptest`              | manifest parse, plan computation, hash logic                                |
| Download / patch integration | `httptest` simulating slow / 404 / cut | retry, range resume, single-file failure isolation                          |
| Integrity                 | testdata with intentional bit-flips    | confirm hash detection + restore                                            |
| Frontend                  | Vitest + Playwright (Vue components)   | i18n switch, view toggle, bg swap, hidden states (validated zero-diff)      |
| E2E real launcher         | Manual (willie + friends)              | one real Genshin update + pre-download cycle per release                    |

Not in Phase 1: automated E2E against live HoYoverse API (rate-limit + ToS risk). Use fixture-based tests + manual smoke twice per quarter.

### 9.4 Logging

- `log/slog` JSON to `./logs/app-YYYY-MM-DD.log` + stderr.
- Levels: `INFO` default; settings toggle to `DEBUG`.
- Per-provider component log (`provider=hoyoverse game=genshin`).
- HTTP request/response only at DEBUG.

---

## 10. Phase 1 Plan Outline (drives `writing-plans`)

1. Project scaffold (Wails 2 + Go modules; Vue 3 + Vite + i18n + Pinia; AGPL-3.0 LICENSE; .gitignore; build tags).
2. Frontend immersive chrome (matches v14 mockup): topbar / sidebar (collapsible) / main / footbar / bottom-bar / grid view / language switch / view toggle / window controls. Verified zero-reflow across locales.
3. Settings + state TOML load/save; Wails command bindings exposed.
4. Provider interface + registry + LocalizedString helpers.
5. HoYoverse provider:
   - Constants: launcher_id, game IDs, biz IDs, exe names, folder names.
   - DetectInstall (read HoYoPlay config + games subfolder).
   - GetIcon / GetBackgrounds (hit `getGames` + `getAllGameBasicInfo`).
   - CheckVersion (`getGamePackages`).
   - PlanUpdate (legacy URL path first).
   - Download (parallel range, retry, resume).
   - Apply (full-replace + hdiff via hpatchz).
   - Launch (exec game.exe, no instrumentation).
6. UI wiring: detect → check → plan → download (with progress events) → apply → launch.
7. Pre-download flow.
8. Sophon path (if blocked by current games' protocol).
9. Error handling + integrity + tests + logs.
10. End-to-end real Genshin update test.

---

## 11. References

- **Collapse Launcher** — protocol reference (AGPL-3.0): https://github.com/CollapseLauncher/Collapse
- **Hoyo Launcher** community implementations — older legacy URL path docs.
- **HDiffPatch** (used as `hpatchz`): https://github.com/sisong/HDiffPatch
- **Wails 2** docs: https://wails.io
- **Vue 3** + **Vite** + **vue-i18n** + **Pinia**.
- This spec's design discussion: chat 2026-05-03.
- Mockup (v14): `.superpowers/brainstorm/<session>/content/aesthetic-v14.html` (gitignored — use mockup screenshots for reference if needed).

---

## Appendix A: Settings schema (frontend renders from this)

```json
{
  "app": [
    { "key": "language", "kind": "select", "label_zh-TW": "語言", "label_en": "Language", "options": ["zh-TW", "en"] },
    { "key": "banner_animation_pref", "kind": "select", "options": ["always-static", "video-when-available", "never"] },
    { "key": "show_technical_info", "kind": "bool" }
  ],
  "backends.hoyoverse": [
    { "key": "hoyoplay_path", "kind": "path" },
    { "key": "region", "kind": "select", "options": ["global"] }
  ]
}
```

## Appendix B: Game name & publisher i18n table

```toml
# zh-TW                  en
"米哈遊"             = "HoYoverse"
"庫洛"               = "Kuro Games"
"鷹角"               = "Hypergryph"
"完美世界"           = "Perfect World"

"原神"               = "Genshin Impact"
"崩壞：星穹鐵道"     = "Honkai: Star Rail"
"絕區零"             = "Zenless Zone Zero"
"鳴潮"               = "Wuthering Waves"
"明日方舟：終末地"   = "Arknights: Endfield"
"異環"               = "Neverness to Everness"
```
