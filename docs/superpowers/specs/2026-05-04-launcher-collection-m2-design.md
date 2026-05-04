# launcher-collection — M2 design (Kuro + Hypergryph providers)

**Status**: approved 2026-05-04, ready for plan-writing.
**Predecessor**: M1 (`v0.1.0-m1` on `main`) — single-publisher (HoYoverse) read-only library.
**Successor**: M3 (download/apply for HoYoverse, plus possibly Perfect World launcher integration with auth research).

## 1. Scope

Add **2 new providers** alongside M1's `hoyoverse`:

| Backend ID | Publisher (zh-TW) | Game | Game ID |
|---|---|---|---|
| `kurogames` | 庫洛 | 鳴潮 / Wuthering Waves | `kurogames/wutheringwaves` |
| `hypergryph` | 鷹角 | 終末地 / Arknights: Endfield | `hypergryph/endfield` |

After M2 the launcher shows **5 games across 3 publisher groups** (3 HoYoverse + 1 Kuro + 1 Hypergryph).

### In scope

- Detect each game's install (filesystem scan rooted at the publisher's launcher path)
- Launch each game directly via `windows.ShellExecute` (NOT through the publisher's launcher)
- Display the game's icon in the sidebar (extracted from the game's exe PE resource at runtime)
- Display the game's static background art in the detail view (per-publisher source: HTTP API or local cache scrape — researched per-publisher during impl)
- Show the game's version in the sidebar status (Kuro only — Hypergryph has no clean on-disk version, displays "就緒" without version)
- Settings TOML extends with `[backends.kurogames]` and `[backends.hypergryph]` sections

### Out of scope (M3 or later)

- **Perfect World 異環 (NTE)** — direct exe launch breaks the auth flow; auth-injection mechanism unclear (CommandLine empty); reverse-engineering risk too high for an M2 PoC. Deferred to a later milestone alongside auth research.
- Download / install / patch (Phase 2 deliverable per the original launcher-collection design spec — still M3+)
- Background videos for the new providers (M2 uses static images only for Kuro/Hypergryph; HoYoverse keeps its existing video support)
- Settings UI in the app — settings.toml stays hand-edited
- Local-version detection for HoYoverse games (faked equal-to-latest in M1, still faked in M2 — fixed in M3)
- Provider hot-reload, telemetry, performance budgets

## 2. Architectural decisions

### 2.1 Provider registry on App

Replace M1's `App.hoyo *hoyoverse.Provider` field with a slice:

```go
type App struct {
    ctx       context.Context
    settings  Settings
    settingsP string
    providers []core.Provider
    detect    map[core.BackendID]detectEntry  // detection cache (see §2.4)
    detectMu  sync.Mutex
    logger    *slog.Logger
}
```

Routing helper centralizes the GameID → Provider lookup:

```go
func (a *App) provider(gid core.GameID) (core.Provider, error) {
    backendID, _, err := core.ParseGameID(gid)
    if err != nil { return nil, err }
    for _, p := range a.providers {
        if p.ID() == backendID { return p, nil }
    }
    return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}
```

Wails-bound commands become two-liners:

```go
func (a *App) Launch(gameID string) (int, error) {
    p, err := a.provider(core.GameID(gameID))
    if err != nil { return 0, err }
    return p.Launch(a.ctx, core.GameID(gameID), core.LaunchOptions{})
}
```

At provider registration, App validates that every game emitted by `Provider.Games()` has a `core.GameID` whose backend-prefix matches that provider's `ID()`. Mismatch → fail-fast at startup, not at first command.

### 2.2 GameID format & parser

`GameID` is a string of shape `"<backend>/<suffix>"`. Format is **part of the contract** — frontend, App routing, and asset URLs all depend on it.

```go
// internal/core/provider.go (additions)

// GameID is "<backend>/<suffix>", e.g. "hoyoverse/genshin".
// The format is part of the contract.
func ParseGameID(s GameID) (BackendID, string, error) {
    parts := strings.SplitN(string(s), "/", 2)
    if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
        return "", "", fmt.Errorf("invalid game id %q (want <backend>/<suffix>)", s)
    }
    return BackendID(parts[0]), parts[1], nil
}
```

The `<suffix>` part is useful in log lines (`slog.String("game_suffix", suffix)`) and as the trailing key in asset URLs.

### 2.3 Asset routing — path on existing AssetServer

The Wails `assetserver.Options.Handler` handles all asset HTTP requests on the WebView's `wails.localhost` origin. Register a path-based route — **not** a custom URL scheme — to serve binary assets that the providers extract / scrape locally:

```
GET wails.localhost/_asset/<backendID>/<kind>/<key>
```

- `<backendID>`: must match an existing provider's `ID()`. Reject otherwise.
- `<kind>`: small allowlist — `icon` and `bg` for M2. Anything else → 404.
- `<key>`: the GameID suffix (e.g. `wutheringwaves`). `..` and path separators rejected.

The middleware:

```go
func newAssetHandler(getApp func() *App) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 1. parse path → backendID, kind, key (validate kind allowlist, reject ".." in key)
        // 2. get provider by backendID
        // 3. type-assert provider into core.AssetServer (optional interface)
        // 4. call ServeAsset; write bytes + mime; or 404
    })
}
```

`Provider` itself does NOT grow a `ServeAsset` method. Instead, the **optional interface** lives on its own:

```go
// internal/core/assetserver.go
type AssetServer interface {
    ServeAsset(ctx context.Context, kind, key string) (data []byte, mime string, err error)
}
```

`hoyoverse` (CDN-URL-based) does not implement it — type-assert in middleware returns false → 404 → that scheme isn't used by hoyoverse anyway, so no caller will reach there.

`kurogames` and `hypergryph` implement it (for their cached/extracted icon and bg bytes).

### 2.4 Detection cache at App layer

M1 `Provider.Launch` re-runs `DetectInstall` on every click. For M2 with 3 providers, multiply that and you have 3 FS scans per click. Worse: `ListGames` and `Launch` running back-to-back duplicate work.

Cache lives at the App layer — providers stay pure functions of `(settings, ctx)`:

```go
type detectEntry struct {
    games []core.InstalledGame
    at    time.Time
    err   error
}

func (a *App) cachedDetect(ctx context.Context, p core.Provider) ([]core.InstalledGame, error) {
    a.detectMu.Lock()
    e, ok := a.detect[p.ID()]
    a.detectMu.Unlock()
    if ok && e.err == nil { return e.games, nil }

    games, err := p.DetectInstall(ctx)
    a.detectMu.Lock()
    a.detect[p.ID()] = detectEntry{games: games, at: time.Now(), err: err}
    a.detectMu.Unlock()
    return games, err
}
```

**Invalidation: event-based, NOT TTL.**
- `UpdateSettings` clears the entire cache (providers may be reconstructed too).
- A new Wails-bound command `Refresh()` clears the cache; UI gets a manual refresh hook.
- (Optional, implementer's call) hook the Wails `WindowFocus` event and invalidate on regaining focus — catches "user installed Genshin via HoYoPlay while our app was alt-tabbed" without polling.

`atomic.Pointer[detectEntry]` would be a finer choice than mutex for read-mostly access, but the mutex is simpler and the workload (a few commands per second worst case) makes lock contention irrelevant.

### 2.5 DetectionStatus derivation (App-side)

No new method on `core.Provider`. App derives status from `(PrimaryPath(), DetectInstall(ctx, path))`:

| State | Derivation |
|---|---|
| `path_unset` | `Provider.(PathProvider).PrimaryPath() == ""` |
| `launcher_missing` | `os.Stat(path)` returns `ErrNotExist` |
| `empty` | `DetectInstall` succeeds but returns 0 InstalledGames |
| `error` | `DetectInstall` returns non-nil error |
| `ok` | otherwise |

The optional `core.PathProvider` interface unifies path access across publishers (each has differently-named TOML keys M1 used `hoyoplay_path`; M2 normalizes to `path`):

```go
// internal/core/pathprovider.go (optional interface)
type PathProvider interface {
    PrimaryPath() string  // empty when not configured
}
```

A new Wails-bound command exposes this to the frontend:

```go
type BackendStatus struct {
    BackendID   string                `json:"backend_id"`
    DisplayName core.LocalizedString  `json:"display_name"`
    Status      string                `json:"status"`            // ok | path_unset | launcher_missing | empty | error
    Detail      string                `json:"detail,omitempty"`
}
func (a *App) ListBackends() []BackendStatus
```

UI consumption is **best-effort in M2** — the API is wired and exercisable from the frontend, but the sidebar group label rendering doesn't yet visualize per-backend status. M3 can add the pill / dimmed group treatment.

### 2.6 Launch via ShellExecute, no polling (M1 pattern)

`launch_windows.go` per provider is essentially a copy of M1 hoyoverse's: `windows.ShellExecute` with `verb=nil` (manifest-driven UAC), return `(0, nil)` on success. **PID intentionally 0 — anti-cheat-friendly: no polling parent process.** This invariant is documented in `core.Provider.Launch`'s godoc:

```go
// Launch starts the game by executing its main exe. Returns (0, nil) on
// successful spawn — the spawned process is intentionally NOT tracked by
// the launcher (anti-cheat may flag a polling parent). Implementations
// MUST NOT call cmd.Wait() or otherwise observe the child after spawn.
//
// ctx may short-circuit pre-spawn work (UTF-16 conversions, cache lookup)
// via ctx.Err() but does NOT bind to the spawned process lifetime.
Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
```

### 2.7 Icon — `iconext` shared package

Pure function: `func Extract(exePath string) ([]byte, error)` returns a PNG-encoded byte slice from the .exe's largest available PE icon resource.

```
internal/providers/iconext/
  iconext.go            // public API: Extract(exePath); ErrUnsupported sentinel
  iconext_windows.go    //go:build windows — real impl using x/sys/windows
  iconext_other.go      //go:build !windows — stub returns ErrUnsupported
```

Windows impl steps:
1. `ExtractIconExW(exePath, 0, &large, nil, 1)` — get largest icon HICON
2. `GetIconInfo` → bitmap handles (color + mask)
3. `GetDIBits` → raw RGBA pixels
4. `image.NewRGBA` + `png.Encode` → []byte
5. `DestroyIcon`

**Caching**: in-memory `lru.Cache[string, cacheEntry]` keyed by `exePath`, with a hard byte cap (32 MB). Cache entry stores `(mtime, bytes)` and re-extracts when mtime changes (icon changed across game updates).

Each provider's `GetIcon` is pure:

```go
func (p *Provider) GetIcon(_ context.Context, gid core.GameID) (string, error) {
    g := findByID(gid)
    if g == nil { return "", core.ErrUnknownGame }
    _, suffix, _ := core.ParseGameID(gid)
    return fmt.Sprintf("/_asset/%s/icon/%s", p.ID(), suffix), nil
}
```

The actual PE extraction happens lazily inside `ServeAsset` when the WebView requests the URL. Pseudocode (illustrative; actual wiring of "how does Provider reach the App-level detection cache" is a plan-level detail — candidates: constructor-injected `InstallLookup` interface satisfied by `*App`; or middleware resolves the install upfront and passes it to a per-publisher helper; or App's middleware handles `kind == "icon"` uniformly across all providers and only delegates `kind == "bg"` to per-publisher logic):

```go
// Conceptual shape — wiring TBD in plan
func (p *Provider) ServeAsset(ctx context.Context, kind, key string) ([]byte, string, error) {
    if kind != "icon" && kind != "bg" {
        return nil, "", core.ErrAssetNotAvailable
    }
    inst, ok := p.lookupInstall(ctx, key)  // <-- plan decides the wiring
    if !ok { return nil, "", core.ErrGameNotInstalled }

    switch kind {
    case "icon":
        // exeNameFor(key) returns the configured ExeName for that game from this
        // provider's gameMeta — small per-provider helper, ~5 LOC.
        bytes, err := iconext.Extract(filepath.Join(inst.InstallPath, exeNameFor(key)))
        return bytes, "image/png", err
    case "bg":
        return p.serveBg(ctx, inst, key)  // per-publisher impl in bg.go
    }
}
```

### 2.8 Background art per-publisher

Per-publisher decision is finalized during the **impl task** for that publisher, not pre-lock. Decision matrix template:

```
Publisher: <name>
  API endpoint:    <URL or "none">
  API auth:        <key/header/none>
  Response shape:  <JSON path to image URL>
  Cache fallback:  <local path or "none">
  Decision:        A (use API → return https URL) | B (cache scrape → return /_asset URL)
```

`api.go` exists in the provider package only when decision = A. `bg.go` exists only when decision = B. One of the two; not both.

For each publisher's research (during impl), follow the workflow:
1. Check Collapse Launcher source (https://github.com/CollapseLauncher/Collapse) for known coverage.
2. Inspect local launcher AppData / install dir for endpoint hints.
3. Light live probe (1-2 endpoints) if offline sources inconclusive.

### 2.9 Version per-publisher

| Publisher | Source | Method |
|---|---|---|
| Kuro | `<path>\Wuthering Waves Game\launcherDownloadConfig.json` `.version` field | `os.ReadFile` + `json.Unmarshal`; produces e.g. `"3.3.0"` |
| Hypergryph | (none clean) | Returns `""` — sidebar shows "就緒" without `· vX.Y` |
| HoYoverse | (no change) | Existing M1 API call |

Hypergryph's `Endfield.exe` PE FileVersion is `2021.3.34f5` (Unity engine), `app.info` only has `Gryphline\nEndfield`. Real game version requires Hypergryph's launcher API which we'd treat as scope creep. M3 can revisit.

### 2.10 Settings — TOML schema with version field

```toml
version = 1

[app]
language = "zh-TW"
banner_animation_pref = "video-when-available"
show_technical_info = false

[backends.hoyoverse]
path = "C:\\Program Files\\HoYoPlay"
region = "global"

[backends.kurogames]
path = "C:\\Program Files\\Wuthering Waves"

[backends.hypergryph]
path = "C:\\Program Files\\GRYPHLINK"
```

Go schema:

```go
type Settings struct {
    Version  int             `toml:"version"`
    App      AppSettings     `toml:"app"`
    Backends BackendSettings `toml:"backends"`
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

type KurogamesSettings  struct { Path string `toml:"path"` }
type HypergryphSettings struct { Path string `toml:"path"` }
```

**Migration from M1**:
- A separate `hoyoverseRawTOML struct { Path, HoYoplayPath string }` is unmarshalled from the raw TOML in `LoadSettings`.
- If `Path == ""` and `HoYoplayPath != ""`, project the legacy field into `Path` and `slog.Warn("migrated legacy hoyoplay_path → path")`.
- `Version == 0` (absent) is treated as M1 format and triggers the migration.
- On `SaveSettings`, the canonical schema is written — `hoyoplay_path` is dropped. Legacy field is read-only and never persisted on the in-memory `HoyoverseSettings`.

**Malformed TOML** (parse error): return `defaultSettings()` and `slog.Error("settings TOML malformed, using defaults", "err", ...)`. M1's `s, _ := LoadSettings(...)` was silent — fix it.

### 2.11 Logging — slog per-provider

`main.go` constructs the root `slog.Logger` once, passes it into `app.New`, which passes per-provider variants to each Provider constructor:

```go
logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

a := app.New("", logger)
hoyo := hoyoverse.New(hoyoverseSettings, logger.With("backend", "hoyoverse"))
kuro := kurogames.New(kurogamesSettings, logger.With("backend", "kurogames"))
gryph := hypergryph.New(hypergryphSettings, logger.With("backend", "hypergryph"))
```

Each Provider stores `logger *slog.Logger` and uses it in detect / version / API / icon / bg paths. M2 logs at TextHandler with Debug level; M3 can add `[app] log_level = "info"` setting.

`main()` wraps the run loop in a `defer recover()` that calls `logger.Error("panic", "err", r, "stack", string(debug.Stack()))` and re-panics (or exits with a sentinel code) — prevents one provider's panic from black-screening the whole app.

### 2.12 Sentinel errors

```go
// internal/core/errors.go
var (
    ErrUnknownGame          = errors.New("unknown game id")
    ErrGameNotInstalled     = errors.New("game not installed")
    ErrBackendNotConfigured = errors.New("backend not configured")
    ErrLauncherMissing      = errors.New("launcher folder not found")
    ErrAssetNotAvailable    = errors.New("asset not available")
)

// ErrorCode returns a stable JSON-friendly code for a wrapped error.
// Frontend uses this code to choose UX (CTA, retry, dim).
func ErrorCode(err error) string {
    switch {
    case errors.Is(err, ErrUnknownGame):          return "unknown_game"
    case errors.Is(err, ErrGameNotInstalled):     return "not_installed"
    case errors.Is(err, ErrBackendNotConfigured): return "not_configured"
    case errors.Is(err, ErrLauncherMissing):      return "launcher_missing"
    case errors.Is(err, ErrAssetNotAvailable):    return "asset_unavailable"
    default:                                      return "internal"
    }
}
```

App binds `ErrorCode(s string) string` and `ErrorMessage(s string) string` (the latter localized) so the frontend can route on stable codes without round-tripping the raw error string. Provider impls wrap with `%w`:

```go
return 0, fmt.Errorf("%w: %s not found in %s", core.ErrGameNotInstalled, gid, path)
```

### 2.13 LocalizedString locale fallback chain

Endfield (Hypergryph) is a CN-origin title; the official zh-CN name uses simplified characters. Extend `LocalizedString.Get(locale)` to fall through:

```
zh-CN → zh-TW → en → first non-empty entry → ""
zh-TW → en → first non-empty → ""
en    → first non-empty → ""
```

`hoyoverse` games stay populated with zh-TW + en only — Get("zh-CN") falls through to zh-TW automatically.
Endfield is populated with all three keys — zh-CN explicitly distinct from zh-TW.

### 2.14 Frontend changes (minimal)

| File | Change |
|---|---|
| `frontend/src/components/Footbar.vue` | `backends: 1` → `backends: new Set(games.games.map(g => g.backend)).size` |
| `frontend/src/stores/games.ts` | No structural change — `loadAssets` already iterates; ` GetIcon` / `GetBackgrounds` still return strings |
| `frontend/src/stores/backends.ts` (NEW) | Pinia store calling `ListBackends`; M2 wires it up (consumed by Footbar's count and an optional debug overlay), even if sidebar grouping doesn't yet visualize status pills |
| `frontend/src/components/Sidebar.vue` | No structural change — grouping by backend already supports N publishers |

## 3. Per-publisher integration table (locked)

| Publisher | Backend ID | Default path | Detect heuristic | Launch target | Version source | Background source |
|---|---|---|---|---|---|---|
| HoYoverse | `hoyoverse` | `C:\Program Files\HoYoPlay` | `<path>\games\<g.FolderName>\` | `<path>\games\<g.FolderName>\<g.ExeName>` | M1 API | M1 API |
| Kuro | `kurogames` | `C:\Program Files\Wuthering Waves` | `<path>\Wuthering Waves Game\Wuthering Waves.exe` exists | `<path>\Wuthering Waves Game\Wuthering Waves.exe` | `<path>\Wuthering Waves Game\launcherDownloadConfig.json` `.version` | TBD (impl task) |
| Hypergryph | `hypergryph` | `C:\Program Files\GRYPHLINK` | `<path>\games\EndField Game\Endfield.exe` exists | `<path>\games\EndField Game\Endfield.exe` | (none — display "就緒" without version) | TBD (impl task) |

`gameMeta` for each (in respective `meta.go`):

```go
// kurogames/meta.go
var games = []gameMeta{
    {
        ID:          "kurogames/wutheringwaves",
        FolderName:  "Wuthering Waves Game",
        ExeName:     "Wuthering Waves.exe",
        VersionFile: "launcherDownloadConfig.json",
        Display:     core.LocalizedString{
            "zh-TW": "鳴潮",
            "zh-CN": "鸣潮",
            "en":    "Wuthering Waves",
        },
    },
}

// hypergryph/meta.go
var games = []gameMeta{
    {
        ID:         "hypergryph/endfield",
        FolderName: filepath.Join("games", "EndField Game"),
        ExeName:    "Endfield.exe",
        Display:    core.LocalizedString{
            "zh-TW": "明日方舟：終末地",
            "zh-CN": "明日方舟：终末地",
            "en":    "Arknights: Endfield",
        },
    },
}
```

## 4. Implementation task ordering (strict serial)

```
Task 1   core: ParseGameID + sentinel errors + ErrorCode + AssetServer optional iface +
              PathProvider optional iface + LocalizedString fallback chain extension
Task 2   util: internal/util/dirver shared version-dir parser + tests
Task 3   iconext: shared package + Win impl + non-Win stub + tests w/ mock exe fixture
Task 4   App refactor: providers []core.Provider; provider() helper; cachedDetect at App
              layer; ListBackends; ErrorCode/ErrorMessage binds; Refresh command
Task 5   Settings: extend BackendSettings; legacy hoyoplay_path migration; malformed-TOML
              recovery; version=1 schema; tests
Task 6   M1 hoyoverse adapter changes: add logger field; no ServeAsset (CDN URLs);
              path key migrated to `path`; PathProvider implementation
Task 7   kurogames provider: meta.go, detect.go, version.go (json read),
              launch_windows.go, kurogames.go (Provider impl); BG research → A or B → impl;
              ServeAsset for icon (always) + bg (if B) + tests
Task 8   AssetServer middleware on the existing Wails AssetServer.Handler — placed AFTER
              Task 7 so the contract is validated against a real consumer (kurogames),
              not designed in a vacuum
Task 9   hypergryph provider: same shape; version returns ""; BG research → A or B → impl;
              ServeAsset for icon + bg (if B) + tests
Task 10  Frontend: Footbar count fix; backends.ts store; locale chain test
Task 11  Manual smoke (see §6)
Task 12  Tag v0.2.0-m2; merge to main with --no-ff
```

12 tasks total. Strict serial — kurogames discovers any abstraction issues with Tasks 4-6's
work before hypergryph copies the pattern. Don't fan out.

## 5. Testing strategy

| Layer | Test type | Coverage |
|---|---|---|
| `core/errors.go` | Unit | sentinel `errors.Is`; `ErrorCode` covers each sentinel |
| `core.ParseGameID` | Unit | valid / empty / single-segment / leading slash / Unicode |
| `core.LocalizedString.Get` | Unit | fallback chain zh-CN → zh-TW → en → first → "" |
| `internal/util/dirver` | Unit | 3-part / 4-part / non-version dirs / max selection |
| `iconext` (Windows) | Unit | mock exe fixture under `testdata/`; expected PNG bytes match |
| `iconext` (non-Windows) | Unit | returns `ErrUnsupported` |
| each provider's `detect.go` | Unit + integration | temp-dir fake installs; expected `InstalledGame` outputs |
| `kurogames/version.go` | Unit | mock `launcherDownloadConfig.json` content → "3.3.0" |
| `hypergryph` (no version) | Unit | confirms returns `""`, no error |
| each provider's `api.go` (if exists) | Unit via httptest | response shape parsing, error paths |
| each provider's `Provider` impl | Compile-time | `var _ core.Provider = (*Provider)(nil)` in provider package |
| App `provider()` lookup + GameID prefix validation | Unit | bad provider with mismatched prefix → register fails |
| App `cachedDetect` | Unit | concurrent goroutines (`-race`); UpdateSettings invalidates; Refresh triggers |
| AssetServer middleware | Unit via httptest | kind allowlist; reject `..` in key; 404 path doesn't leak internal info; correct mime |
| `LoadSettings` migration | Unit | M1 TOML with `hoyoplay_path` → loaded with `Path` populated + warning log |
| `LoadSettings` malformed | Unit | empty file / unknown keys / wrong type — appropriate fallbacks + error log |
| `ListBackends` | Unit | each `DetectionStatus` state derived correctly from configured fake providers |

**No automated tests for `Launch`** (would actually spawn games — same as M1). Manual smoke in Task 11.

## 6. Manual smoke checklist (Task 11)

```
[ ] All 5 games visible in sidebar grouped under 3 publisher groups
[ ] Genshin / Star Rail / ZZZ launch (M1 regression)
[ ] Wuthering Waves launches via Wuthering Waves.exe (UAC may prompt, expected)
[ ] Endfield launches via Endfield.exe (UAC may prompt, expected)
[ ] Wuthering Waves icon shows in sidebar (PE-extracted, not first-letter fallback)
[ ] Endfield icon shows in sidebar (PE-extracted, not first-letter fallback)
[ ] Wuthering Waves bg shows in main view (real art, source A or B per impl decision)
[ ] Endfield bg shows in main view (real art, source A or B per impl decision)
[ ] Wuthering Waves sidebar status: 「就緒 · v3.3.0」 (or current version)
[ ] Endfield sidebar status: 「就緒」 (no version, deliberately)
[ ] Footbar shows "3 backends · 5 games"
[ ] Editing settings.toml's `hoyoplay_path` → app loads with warning log + auto-migrates `path` on save
[ ] Removing one publisher's path from settings.toml → ListBackends returns DetectionStatus.PathUnset for that backend
[ ] Refresh command (Wails-bound) re-runs DetectInstall and updates UI
[ ] Language toggle zh-TW ↔ zh-CN ↔ en updates sidebar names live (Endfield uses different chars per locale)
[ ] Stopwatch: ListGames returns < 2 seconds with all 3 providers configured
[ ] No structured-logging gaps (every provider's detect/version paths log at debug+)
[ ] Optional: Process Explorer or Task Manager shows the launcher does NOT keep handles to spawned game processes (anti-cheat-friendly)
```

## 7. Open observations / known limitations carried forward

These are M2-acceptable and deferred to M3:

1. **HoYoverse local version is still faked** to equal latest version (M1 known limitation). M2 doesn't fix it.
2. **No settings UI** in M2; settings.toml stays hand-edited. The schema work (struct, migration, validation) is done — just the UI panel waits for M3.
3. **No download/install flow** — M2 launches what's already installed. Phase 2 (per the original launcher-collection design spec) is M3+.
4. **Per-publisher BG decision matrix is filled during impl, not pre-lock**. The spec doesn't predict the outcome; the impl task starts with the offline-first research workflow and locks the decision in code + a short note in the provider package's package-level comment.
5. **No video backgrounds for Kuro / Hypergryph** — M2 ships static images only. HoYoverse keeps its existing video support.
6. **NTE / Perfect World deferred** — direct exe launch breaks login (auth not in argv; Perfect World likely uses env vars, named pipes, or parent-process check). Reverse-engineering risk high. Revisit alongside M3+ download/apply work.
7. **`backends.ts` Pinia store is wired in M2 but UI usage is minimal** — Sidebar group label rendering doesn't yet show status pills. M3 can add visualization.
8. **Frontend localization toggle** — zh-CN added but switching at runtime is best-effort (the i18n locale is set at app boot). M2 ships with a topbar toggle that calls `setLang` and re-renders; runtime audit confirmed in Task 11 smoke.

## 8. Cross-references

- M1 design spec: `docs/superpowers/specs/2026-05-03-launcher-collection-design.md`
- M1 plan: `docs/superpowers/plans/2026-05-03-launcher-collection-m1-read-only-library.md`
- M1 ship commit: `5d0e05e merge: M1 read-only library` on `main`, tag `v0.1.0-m1`
- Collapse Launcher source (research reference for protocol shape): https://github.com/CollapseLauncher/Collapse
- v14 mockup (sidebar grouping by backend already supports N publishers): `.superpowers/brainstorm/<latest-session>/content/aesthetic-v14.html` (gitignored)
