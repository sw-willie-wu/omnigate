# launcher-collection M1 — Read-Only Library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a working Wails desktop app that detects installed HoYoverse games (原神 / 崩鐵 / 絕區零) via HoYoPlay, displays them in the immersive 16:9 UI with real banners/icons from the official API, and launches the selected game's `.exe`. Read-only — no downloads or updates yet (those are M2).

**Architecture:** Wails 2 frameless app, fixed 1280×720. Go backend with `Provider` interface; only `hoyoverse` provider implemented in M1. Vue 3 + Pinia frontend, i18n via vue-i18n. Backend exposes commands; frontend renders reactive state.

**Tech Stack:** Wails 2 / Go 1.22+ / Vue 3 + Vite + TypeScript / Pinia / vue-i18n / pelletier/go-toml/v2.

**Branch model:** All M1 work happens on `m1/read-only-library`. Each task commits to that branch. After all tasks pass, merge to `main` with `--no-ff`.

---

## Workflow rules (apply to every task)

- **TDD where practical**: Write test → run (FAIL) → implement → run (PASS) → commit.
- **Frequent commits**: Each task ends with a commit. Conventional commit prefixes (`feat:`, `chore:`, `refactor:`, `test:`, `docs:`, `fix:`).
- **No `Co-Authored-By` trailer** in commit messages.
- **Branch**: stay on `m1/read-only-library` for all tasks. Create at start: `git checkout -b m1/read-only-library`.
- **Merge** at end of M1: `git checkout main && git merge --no-ff m1/read-only-library`.

---

## File Structure (M1 deliverable)

```
launcher-collection/
├── main.go                                     # Wails entrypoint
├── go.mod / go.sum
├── wails.json
├── LICENSE                                     # AGPL-3.0
├── README.md
├── bin/
│   └── hpatchz.exe                             # placeholder (used in M2)
├── internal/
│   ├── app/
│   │   ├── app.go                              # Wails-bound struct + commands
│   │   ├── settings.go                         # settings.toml load/save
│   │   ├── settings_test.go
│   │   ├── state.go                            # state.toml load/save
│   │   ├── state_test.go
│   │   └── registry.go                         # provider registry
│   ├── core/
│   │   ├── provider.go                         # Provider interface + types
│   │   └── locstring.go                        # LocalizedString helpers
│   └── providers/
│       └── hoyoverse/
│           ├── hoyoverse.go                    # provider entry
│           ├── api.go                          # HTTP client + endpoints
│           ├── api_test.go
│           ├── detect.go                       # DetectInstall logic
│           ├── detect_test.go
│           ├── version.go                      # CheckVersion logic
│           ├── version_test.go
│           ├── meta.go                         # constants (game ids, exe names, biz)
│           └── launch.go                       # game launch
├── frontend/
│   ├── package.json
│   ├── vite.config.ts
│   ├── tsconfig.json
│   ├── index.html
│   └── src/
│       ├── main.ts
│       ├── App.vue
│       ├── i18n.ts
│       ├── locales/
│       │   ├── zh-TW.json
│       │   └── en.json
│       ├── stores/
│       │   ├── games.ts                        # game list + selection
│       │   ├── settings.ts                     # settings store
│       │   └── view.ts                         # view-state (sidebar collapsed, view-mode)
│       ├── components/
│       │   ├── Sidebar.vue
│       │   ├── SidebarRow.vue
│       │   ├── Topbar.vue
│       │   ├── BottomBar.vue
│       │   ├── Footbar.vue
│       │   ├── BgLayer.vue
│       │   ├── DetailView.vue
│       │   ├── GridView.vue
│       │   └── GridCard.vue
│       └── styles/
│           └── theme.css
└── docs/superpowers/{specs,plans}/...          # already committed
```

---

## Task 1: Bootstrap branch + scaffold Wails project

**Files:**
- Create: `main.go`, `wails.json`, `go.mod`, `frontend/package.json`, `frontend/vite.config.ts`, `frontend/index.html`, `frontend/src/main.ts`, `frontend/src/App.vue`

- [ ] **Step 1: Create the working branch**

```bash
git checkout main
git checkout -b m1/read-only-library
```

- [ ] **Step 2: Verify Wails CLI installed (>= v2.10)**

```bash
wails version
```

Expected: `v2.10.x` or later. If not installed: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

- [ ] **Step 3: Init Wails Vue+TS template, then move generated files to repo root**

```bash
cd ..
wails init -n launcher-collection-tmp -t vue-ts
# Move contents into our existing repo (preserves docs/, .gitignore, .git/)
```

Manual move (PowerShell): copy everything from `launcher-collection-tmp/` into `launcher-collection/` except `.gitignore` (keep ours) and `LICENSE` (we'll write our own).

- [ ] **Step 4: Verify default Wails app builds and runs**

```bash
cd launcher-collection
wails dev
```

Expected: a window opens showing the default Vue greeting page. Close it.

- [ ] **Step 5: Configure window for frameless fixed 1280×720 in `main.go`**

Replace the `wails.Run(&options.App{...})` call with:

```go
err := wails.Run(&options.App{
    Title:             "launcher-collection",
    Width:             1280,
    Height:            720,
    MinWidth:          1280,
    MinHeight:         720,
    MaxWidth:          1280,
    MaxHeight:         720,
    DisableResize:     true,
    Frameless:         true,
    AssetServer:       &assetserver.Options{ Assets: assets },
    BackgroundColour:  &options.RGBA{R: 8, G: 8, B: 14, A: 255},
    OnStartup:         app.startup,
    Bind:              []interface{}{ app },
})
```

- [ ] **Step 6: Run again, confirm frameless fixed window**

```bash
wails dev
```

Expected: window has no title bar, cannot be resized, default Vue page visible.

- [ ] **Step 7: Commit**

```bash
git add .
git commit -m "chore: scaffold Wails 2 + Vue 3 + TS frameless window (1280x720)"
```

---

## Task 2: Add AGPL-3.0 LICENSE and README stub

**Files:**
- Create: `LICENSE`, `README.md`

- [ ] **Step 1: Write the AGPL-3.0 LICENSE file**

Download the canonical text from `https://www.gnu.org/licenses/agpl-3.0.txt` and save as `LICENSE`. (Or copy the standard 33KB file from any AGPL-3.0 project — the text is the same.)

- [ ] **Step 2: Write `README.md`**

```markdown
# launcher-collection

Unified desktop launcher for several Chinese live-service games (Genshin / Star Rail / Zenless Zone Zero / Wuthering Waves / Endfield / Neverness to Everness).

**Status:** M1 (read-only library) — work in progress.

**License:** [AGPL-3.0](LICENSE).

**References:**
- Protocol behavior referenced from [Collapse Launcher](https://github.com/CollapseLauncher/Collapse) (AGPL-3.0).
- HDiff patches via bundled [`hpatchz`](https://github.com/sisong/HDiffPatch).

**Design spec:** `docs/superpowers/specs/2026-05-03-launcher-collection-design.md`.
```

- [ ] **Step 3: Commit**

```bash
git add LICENSE README.md
git commit -m "docs: add AGPL-3.0 LICENSE and README stub"
```

---

## Task 3: Define `LocalizedString` helper with tests

**Files:**
- Create: `internal/core/locstring.go`, `internal/core/locstring_test.go`

- [ ] **Step 1: Write the failing test in `internal/core/locstring_test.go`**

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
```

- [ ] **Step 2: Run test, verify it fails**

```bash
go test ./internal/core/...
```

Expected: FAIL — `LocalizedString` not defined.

- [ ] **Step 3: Implement `internal/core/locstring.go`**

```go
package core

// LocalizedString maps language tag (e.g. "zh-TW", "en") to the localized
// string. Use Get() to resolve with English fallback.
type LocalizedString map[string]string

// Get returns the value for the given language. Falls back to "en" if missing,
// then to empty string.
func (l LocalizedString) Get(lang string) string {
    if v, ok := l[lang]; ok {
        return v
    }
    if v, ok := l["en"]; ok {
        return v
    }
    return ""
}
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./internal/core/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/locstring.go internal/core/locstring_test.go
git commit -m "feat(core): add LocalizedString with English fallback"
```

---

## Task 4: Define `Provider` interface and core types

**Files:**
- Create: `internal/core/provider.go`

- [ ] **Step 1: Write `internal/core/provider.go`**

```go
package core

import "context"

type BackendID string
type GameID string
type PlanKind int

const (
    PlanUpdate PlanKind = iota
    PlanPredownload
)

type GameDescriptor struct {
    ID                GameID
    Backend           BackendID
    DisplayName       LocalizedString
    SupportedRegions  []string
}

type InstalledGame struct {
    GameID         GameID
    InstallPath    string
    CurrentVersion string
}

type VersionInfo struct {
    Current     string
    Latest      string
    Predownload *PredownloadInfo
}

type PredownloadInfo struct {
    TargetVersion string
    TotalBytes    uint64
}

type BackgroundType int

const (
    BackgroundImage BackgroundType = iota
    BackgroundVideo
)

type Background struct {
    ImageURL string
    VideoURL string
    Type     BackgroundType
}

type SettingFieldKind int

const (
    SettingPath SettingFieldKind = iota
    SettingSelectKind
    SettingBool
)

type SettingField struct {
    Key     string
    Kind    SettingFieldKind
    Label   LocalizedString
    Options []string
}

type LaunchOptions struct {
    ExtraArgs []string
}

// Provider is the integration point for one launcher backend (one publisher).
// Phase 1 = hoyoverse only.
type Provider interface {
    ID() BackendID
    DisplayName() LocalizedString
    Games() []GameDescriptor
    SettingsSchema() []SettingField

    DetectInstall(ctx context.Context) ([]InstalledGame, error)
    GetIcon(ctx context.Context, gid GameID) (string, error)
    GetBackgrounds(ctx context.Context, gid GameID) ([]Background, error)
    CheckVersion(ctx context.Context, gid GameID) (VersionInfo, error)
    Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
}
```

(M2 will extend `Provider` with `PlanUpdate`, `Download`, `Apply`. Phase 1 keeps the interface minimal.)

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/core/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/core/provider.go
git commit -m "feat(core): define Provider interface and core types"
```

---

## Task 5: HoYoverse provider — constants and metadata

**Files:**
- Create: `internal/providers/hoyoverse/meta.go`

- [ ] **Step 1: Write `internal/providers/hoyoverse/meta.go`**

```go
package hoyoverse

import "github.com/willie/launcher-collection/internal/core"

const (
    BackendID  core.BackendID = "hoyoverse"
    LauncherID                = "VYTpXlbWo8"
    APIBase                   = "https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api"
    UserAgent                 = "launcher-collection/0.1 (+https://github.com/willie/launcher-collection)"
)

// gameMeta holds compile-time constants per supported game.
type gameMeta struct {
    ID         core.GameID
    APIGameID  string // HoYoverse API "game_id" param value
    Biz        string // e.g. "hk4e_global"
    FolderName string // subfolder under HoYoPlay/games/
    ExeName    string // launches via this exe
    Display    core.LocalizedString
}

var games = []gameMeta{
    {
        ID: "hoyoverse/genshin", APIGameID: "gopR6Cufr3", Biz: "hk4e_global",
        FolderName: "Genshin Impact game", ExeName: "GenshinImpact.exe",
        Display: core.LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"},
    },
    {
        ID: "hoyoverse/starrail", APIGameID: "4ziysqXOQ8", Biz: "hkrpg_global",
        FolderName: "Star Rail Games", ExeName: "StarRail.exe",
        Display: core.LocalizedString{"zh-TW": "崩壞：星穹鐵道", "en": "Honkai: Star Rail"},
    },
    {
        ID: "hoyoverse/zzz", APIGameID: "U5hbdsT9W7", Biz: "nap_global",
        FolderName: "ZenlessZoneZero Game", ExeName: "ZenlessZoneZero.exe",
        Display: core.LocalizedString{"zh-TW": "絕區零", "en": "Zenless Zone Zero"},
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

(Replace `github.com/willie/launcher-collection` with whatever module path `go.mod` uses — check `go.mod` to confirm.)

- [ ] **Step 2: Verify build**

```bash
go build ./internal/providers/hoyoverse/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/providers/hoyoverse/meta.go
git commit -m "feat(hoyoverse): add per-game constants (genshin / starrail / zzz)"
```

---

## Task 6: HoYoverse `DetectInstall` with tests

**Files:**
- Create: `internal/providers/hoyoverse/detect.go`, `internal/providers/hoyoverse/detect_test.go`

- [ ] **Step 1: Write the failing test**

```go
package hoyoverse

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestDetectInstall_FindsKnownGames(t *testing.T) {
    // Build a fake HoYoPlay tree
    tmp := t.TempDir()
    gamesDir := filepath.Join(tmp, "games")
    if err := os.MkdirAll(filepath.Join(gamesDir, "Genshin Impact game"), 0o755); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(filepath.Join(gamesDir, "Star Rail Games"), 0o755); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(filepath.Join(gamesDir, "ZenlessZoneZero Game"), 0o755); err != nil {
        t.Fatal(err)
    }
    // (config.ini is read by detect; for now, place a minimal stub)
    if err := os.WriteFile(filepath.Join(tmp, "config.ini"),
        []byte("[hyp]\nchannel=1\nprimary_game=hyp_global\n"), 0o644); err != nil {
        t.Fatal(err)
    }

    got, err := DetectInstall(context.Background(), tmp)
    if err != nil {
        t.Fatalf("DetectInstall: %v", err)
    }
    if len(got) != 3 {
        t.Fatalf("got %d games, want 3", len(got))
    }
    seen := map[core.GameID]bool{}
    for _, ig := range got {
        seen[ig.GameID] = true
    }
    for _, want := range []core.GameID{"hoyoverse/genshin", "hoyoverse/starrail", "hoyoverse/zzz"} {
        if !seen[want] {
            t.Errorf("missing game %s", want)
        }
    }
}

func TestDetectInstall_MissingFolderReturnsEmpty(t *testing.T) {
    got, err := DetectInstall(context.Background(), "C:/path/that/does/not/exist")
    if err != nil {
        t.Fatalf("expected nil error, got %v", err)
    }
    if len(got) != 0 {
        t.Errorf("expected 0 games, got %d", len(got))
    }
}
```

(Add `import "github.com/willie/launcher-collection/internal/core"` at top.)

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/providers/hoyoverse/...
```

Expected: FAIL — `DetectInstall` not defined.

- [ ] **Step 3: Implement `internal/providers/hoyoverse/detect.go`**

```go
package hoyoverse

import (
    "context"
    "os"
    "path/filepath"

    "github.com/willie/launcher-collection/internal/core"
)

// DetectInstall scans a HoYoPlay install folder (default: C:\Program Files\HoYoPlay)
// and returns each known game whose folder is present.
//
// hoyoplayPath should point at the directory that contains config.ini and games/.
// Missing path returns ([], nil) — not an error; user may not have HoYoPlay installed.
func DetectInstall(ctx context.Context, hoyoplayPath string) ([]core.InstalledGame, error) {
    info, err := os.Stat(hoyoplayPath)
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
        gamePath := filepath.Join(hoyoplayPath, "games", g.FolderName)
        if st, err := os.Stat(gamePath); err == nil && st.IsDir() {
            out = append(out, core.InstalledGame{
                GameID:         g.ID,
                InstallPath:    gamePath,
                CurrentVersion: "", // populated by CheckVersion later
            })
        }
    }
    return out, nil
}
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./internal/providers/hoyoverse/...
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/hoyoverse/detect.go internal/providers/hoyoverse/detect_test.go
git commit -m "feat(hoyoverse): DetectInstall scans HoYoPlay/games/ for known games"
```

---

## Task 7: HoYoverse API client — `getAllGameBasicInfo`

**Files:**
- Create: `internal/providers/hoyoverse/api.go`, `internal/providers/hoyoverse/api_test.go`

- [ ] **Step 1: Write the failing test using `httptest.NewServer`**

```go
package hoyoverse

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestFetchBasicInfo_ParsesBackgrounds(t *testing.T) {
    body := `{"retcode":0,"message":"OK","data":{"game_info_list":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"backgrounds":[{"id":"a","background":{"url":"https://cdn/img1.webp"},"video":{"url":""},"type":"BACKGROUND_TYPE_UNSPECIFIED"},{"id":"b","background":{"url":"https://cdn/img2.webp"},"video":{"url":"https://cdn/v.webm"},"type":"BACKGROUND_TYPE_VIDEO"}]}]}}`
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if got := r.URL.Path; got != "/getAllGameBasicInfo" {
            t.Errorf("path = %s, want /getAllGameBasicInfo", got)
        }
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(body))
    }))
    defer srv.Close()

    c := newAPIClient(srv.URL, http.DefaultClient)
    got, err := c.fetchBasicInfo(context.Background(), "gopR6Cufr3", "zh-tw")
    if err != nil {
        t.Fatalf("fetchBasicInfo: %v", err)
    }
    if len(got) != 2 {
        t.Fatalf("got %d backgrounds, want 2", len(got))
    }
    if got[0].ImageURL != "https://cdn/img1.webp" {
        t.Errorf("bg[0].ImageURL = %s", got[0].ImageURL)
    }
    if got[1].VideoURL != "https://cdn/v.webm" {
        t.Errorf("bg[1].VideoURL = %s", got[1].VideoURL)
    }
    if got[1].Type != core.BackgroundVideo {
        t.Errorf("bg[1].Type = %v, want video", got[1].Type)
    }
    _ = json.Marshal // silence unused import if test grows
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/providers/hoyoverse/...
```

Expected: FAIL — `newAPIClient` / `fetchBasicInfo` not defined.

- [ ] **Step 3: Implement `internal/providers/hoyoverse/api.go`**

```go
package hoyoverse

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"

    "github.com/willie/launcher-collection/internal/core"
)

type apiClient struct {
    base string
    http *http.Client
}

func newAPIClient(base string, hc *http.Client) *apiClient {
    if hc == nil {
        hc = http.DefaultClient
    }
    return &apiClient{base: base, http: hc}
}

type apiEnvelope struct {
    Retcode int             `json:"retcode"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data"`
}

type rawBasicInfo struct {
    GameInfoList []struct {
        Game struct {
            ID  string `json:"id"`
            Biz string `json:"biz"`
        } `json:"game"`
        Backgrounds []struct {
            ID         string `json:"id"`
            Background struct{ URL string `json:"url"` } `json:"background"`
            Video      struct{ URL string `json:"url"` } `json:"video"`
            Type       string `json:"type"`
        } `json:"backgrounds"`
    } `json:"game_info_list"`
}

// fetchBasicInfo calls /getAllGameBasicInfo and returns the backgrounds for one game.
func (c *apiClient) fetchBasicInfo(ctx context.Context, apiGameID, lang string) ([]core.Background, error) {
    q := url.Values{}
    q.Set("launcher_id", LauncherID)
    q.Set("language", lang)
    q.Set("game_id", apiGameID)
    req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getAllGameBasicInfo?"+q.Encode(), nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("User-Agent", UserAgent)
    resp, err := c.http.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("api %s: http %d", "getAllGameBasicInfo", resp.StatusCode)
    }
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    var env apiEnvelope
    if err := json.Unmarshal(body, &env); err != nil {
        return nil, err
    }
    if env.Retcode != 0 {
        return nil, fmt.Errorf("api retcode=%d msg=%q", env.Retcode, env.Message)
    }
    var raw rawBasicInfo
    if err := json.Unmarshal(env.Data, &raw); err != nil {
        return nil, err
    }
    if len(raw.GameInfoList) == 0 {
        return nil, errors.New("api returned empty game_info_list")
    }
    out := make([]core.Background, 0, len(raw.GameInfoList[0].Backgrounds))
    for _, b := range raw.GameInfoList[0].Backgrounds {
        bg := core.Background{ImageURL: b.Background.URL, VideoURL: b.Video.URL}
        if b.Type == "BACKGROUND_TYPE_VIDEO" {
            bg.Type = core.BackgroundVideo
        } else {
            bg.Type = core.BackgroundImage
        }
        out = append(out, bg)
    }
    return out, nil
}
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./internal/providers/hoyoverse/... -run TestFetchBasicInfo
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/hoyoverse/api.go internal/providers/hoyoverse/api_test.go
git commit -m "feat(hoyoverse): API client with getAllGameBasicInfo"
```

---

## Task 8: HoYoverse API — `getGames` (icon URL)

**Files:**
- Modify: `internal/providers/hoyoverse/api.go`, `internal/providers/hoyoverse/api_test.go`

- [ ] **Step 1: Add the failing test**

Append to `api_test.go`:

```go
func TestFetchGameIcon_FindsByBiz(t *testing.T) {
    body := `{"retcode":0,"message":"OK","data":{"games":[{"biz":"hk4e_global","display":{"name":"Genshin Impact","icon":{"url":"https://cdn/icon-genshin.png"}}},{"biz":"hkrpg_global","display":{"name":"Star Rail","icon":{"url":"https://cdn/icon-rail.png"}}}]}}`
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(body))
    }))
    defer srv.Close()
    c := newAPIClient(srv.URL, http.DefaultClient)
    got, err := c.fetchGameIcon(context.Background(), "hk4e_global", "zh-tw")
    if err != nil {
        t.Fatal(err)
    }
    if got != "https://cdn/icon-genshin.png" {
        t.Errorf("icon = %s", got)
    }
}

func TestFetchGameIcon_NotFound(t *testing.T) {
    body := `{"retcode":0,"data":{"games":[]}}`
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(body))
    }))
    defer srv.Close()
    c := newAPIClient(srv.URL, http.DefaultClient)
    _, err := c.fetchGameIcon(context.Background(), "missing_biz", "en")
    if err == nil {
        t.Errorf("expected error, got nil")
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/providers/hoyoverse/... -run TestFetchGameIcon
```

- [ ] **Step 3: Implement `fetchGameIcon` in `api.go`**

```go
type rawGames struct {
    Games []struct {
        Biz     string `json:"biz"`
        Display struct {
            Name string `json:"name"`
            Icon struct{ URL string `json:"url"` } `json:"icon"`
        } `json:"display"`
    } `json:"games"`
}

func (c *apiClient) fetchGameIcon(ctx context.Context, biz, lang string) (string, error) {
    q := url.Values{}
    q.Set("launcher_id", LauncherID)
    q.Set("language", lang)
    req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getGames?"+q.Encode(), nil)
    if err != nil {
        return "", err
    }
    req.Header.Set("User-Agent", UserAgent)
    resp, err := c.http.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    var env apiEnvelope
    if err := json.Unmarshal(body, &env); err != nil {
        return "", err
    }
    var raw rawGames
    if err := json.Unmarshal(env.Data, &raw); err != nil {
        return "", err
    }
    for _, g := range raw.Games {
        if g.Biz == biz {
            return g.Display.Icon.URL, nil
        }
    }
    return "", fmt.Errorf("biz %q not in /getGames response", biz)
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/providers/hoyoverse/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/providers/hoyoverse/api.go internal/providers/hoyoverse/api_test.go
git commit -m "feat(hoyoverse): fetchGameIcon via /getGames"
```

---

## Task 9: HoYoverse `CheckVersion` via `getGamePackages`

**Files:**
- Create: `internal/providers/hoyoverse/version.go`, `internal/providers/hoyoverse/version_test.go`

- [ ] **Step 1: Write failing test**

```go
package hoyoverse

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestCheckVersion_ParsesMainAndPredownload(t *testing.T) {
    body := `{"retcode":0,"data":{"game_packages":[{"game":{"biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"},"patches":[]},"pre_download":{"major":{"version":"5.6.0"},"patches":[]}}]}}`
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(body))
    }))
    defer srv.Close()

    c := newAPIClient(srv.URL, http.DefaultClient)
    got, err := c.fetchVersion(context.Background(), "hk4e_global", "5.5.0")
    if err != nil {
        t.Fatal(err)
    }
    if got.Latest != "5.5.0" {
        t.Errorf("Latest = %s, want 5.5.0", got.Latest)
    }
    if got.Current != "5.5.0" {
        t.Errorf("Current = %s, want 5.5.0", got.Current)
    }
    if got.Predownload == nil || got.Predownload.TargetVersion != "5.6.0" {
        t.Errorf("Predownload = %+v, want target 5.6.0", got.Predownload)
    }
}

func TestCheckVersion_NoPredownload(t *testing.T) {
    body := `{"retcode":0,"data":{"game_packages":[{"game":{"biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"}}}]}}`
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(body))
    }))
    defer srv.Close()
    c := newAPIClient(srv.URL, http.DefaultClient)
    got, err := c.fetchVersion(context.Background(), "hk4e_global", "5.5.0")
    if err != nil {
        t.Fatal(err)
    }
    if got.Predownload != nil {
        t.Errorf("Predownload = %+v, want nil", got.Predownload)
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/providers/hoyoverse/... -run TestCheckVersion
```

- [ ] **Step 3: Implement `version.go`**

```go
package hoyoverse

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"

    "github.com/willie/launcher-collection/internal/core"
)

type rawGamePackages struct {
    GamePackages []struct {
        Game struct{ Biz string `json:"biz"` } `json:"game"`
        Main struct {
            Major struct{ Version string `json:"version"` } `json:"major"`
        } `json:"main"`
        PreDownload *struct {
            Major struct{ Version string `json:"version"` } `json:"major"`
        } `json:"pre_download,omitempty"`
    } `json:"game_packages"`
}

// fetchVersion returns version info for one biz id. currentLocal is the
// caller-side detected version (or "" if unknown).
func (c *apiClient) fetchVersion(ctx context.Context, biz, currentLocal string) (core.VersionInfo, error) {
    q := url.Values{}
    q.Set("launcher_id", LauncherID)
    q.Add("game_ids[]", "")
    // hoyoverse expects game_ids[]= for each; we use single
    // (NOTE: param key is biz id mapped via gameMeta — caller passes biz)
    qs := q.Encode() + "&game_ids[]=" + biz
    req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getGamePackages?"+qs, nil)
    if err != nil {
        return core.VersionInfo{}, err
    }
    req.Header.Set("User-Agent", UserAgent)
    resp, err := c.http.Do(req)
    if err != nil {
        return core.VersionInfo{}, err
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    var env apiEnvelope
    if err := json.Unmarshal(body, &env); err != nil {
        return core.VersionInfo{}, err
    }
    var raw rawGamePackages
    if err := json.Unmarshal(env.Data, &raw); err != nil {
        return core.VersionInfo{}, err
    }
    for _, p := range raw.GamePackages {
        if p.Game.Biz != biz {
            continue
        }
        info := core.VersionInfo{
            Current: currentLocal,
            Latest:  p.Main.Major.Version,
        }
        if info.Current == "" {
            info.Current = info.Latest
        }
        if p.PreDownload != nil && p.PreDownload.Major.Version != "" {
            info.Predownload = &core.PredownloadInfo{TargetVersion: p.PreDownload.Major.Version}
        }
        return info, nil
    }
    return core.VersionInfo{}, fmt.Errorf("biz %q not in response", biz)
}
```

(The actual `game_ids[]` query encoding deviates from `url.Values.Encode()` — verify by hitting the real API in Task 18 and adjust if HoYoverse rejects it. If so, build the query string manually as `?launcher_id=...&game_ids[]=hk4e_global`.)

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/providers/hoyoverse/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/providers/hoyoverse/version.go internal/providers/hoyoverse/version_test.go
git commit -m "feat(hoyoverse): CheckVersion via getGamePackages"
```

---

## Task 10: HoYoverse `Launch` — exec game.exe

**Files:**
- Create: `internal/providers/hoyoverse/launch.go`

- [ ] **Step 1: Write `launch.go`**

```go
package hoyoverse

import (
    "context"
    "fmt"
    "os/exec"
    "path/filepath"

    "github.com/willie/launcher-collection/internal/core"
)

// Launch starts the game by executing its main exe. Returns the spawned PID.
// We do not capture stdout/stderr nor inject anything into the process, to
// avoid anti-cheat false positives.
func Launch(ctx context.Context, installPath string, gid core.GameID, opts core.LaunchOptions) (int, error) {
    g := findByID(gid)
    if g == nil {
        return 0, fmt.Errorf("unknown game id %q", gid)
    }
    exePath := filepath.Join(installPath, g.ExeName)
    cmd := exec.CommandContext(ctx, exePath, opts.ExtraArgs...)
    cmd.Dir = installPath
    if err := cmd.Start(); err != nil {
        return 0, fmt.Errorf("start %s: %w", exePath, err)
    }
    return cmd.Process.Pid, nil
}
```

(No automated test for `Launch` — would actually spawn games. Manual test in Task 18.)

- [ ] **Step 2: Verify build**

```bash
go build ./internal/providers/hoyoverse/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/providers/hoyoverse/launch.go
git commit -m "feat(hoyoverse): Launch executes game exe with install path as cwd"
```

---

## Task 11: HoYoverse provider entry implementing `Provider`

**Files:**
- Create: `internal/providers/hoyoverse/hoyoverse.go`

- [ ] **Step 1: Write `hoyoverse.go`**

```go
package hoyoverse

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/willie/launcher-collection/internal/core"
)

type Settings struct {
    HoYoplayPath string
    Region       string // "global" or "cn" — only "global" supported in M1
}

type Provider struct {
    api      *apiClient
    settings Settings
}

func New(settings Settings) *Provider {
    return &Provider{
        api:      newAPIClient(APIBase, &http.Client{Timeout: 30 * time.Second}),
        settings: settings,
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
        {Key: "hoyoplay_path", Kind: core.SettingPath,
            Label: core.LocalizedString{"zh-TW": "HoYoPlay 安裝資料夾", "en": "HoYoPlay install folder"}},
        {Key: "region", Kind: core.SettingSelectKind,
            Label:   core.LocalizedString{"zh-TW": "區域", "en": "Region"},
            Options: []string{"global"}},
    }
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
    return DetectInstall(ctx, p.settings.HoYoplayPath)
}

func (p *Provider) GetIcon(ctx context.Context, gid core.GameID) (string, error) {
    g := findByID(gid)
    if g == nil {
        return "", fmt.Errorf("unknown game %q", gid)
    }
    return p.api.fetchGameIcon(ctx, g.Biz, "zh-tw")
}

func (p *Provider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
    g := findByID(gid)
    if g == nil {
        return nil, fmt.Errorf("unknown game %q", gid)
    }
    return p.api.fetchBasicInfo(ctx, g.APIGameID, "zh-tw")
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
    g := findByID(gid)
    if g == nil {
        return core.VersionInfo{}, fmt.Errorf("unknown game %q", gid)
    }
    return p.api.fetchVersion(ctx, g.Biz, "")
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
    return 0, fmt.Errorf("game %q not installed", gid)
}

// compile-time check
var _ core.Provider = (*Provider)(nil)
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/providers/hoyoverse/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/providers/hoyoverse/hoyoverse.go
git commit -m "feat(hoyoverse): wire components into Provider implementation"
```

---

## Task 12: Settings TOML model + load/save with tests

**Files:**
- Create: `internal/app/settings.go`, `internal/app/settings_test.go`

- [ ] **Step 1: Add `go-toml` dependency**

```bash
go get github.com/pelletier/go-toml/v2
```

- [ ] **Step 2: Write failing test**

```go
package app

import (
    "os"
    "path/filepath"
    "testing"
)

func TestSettings_LoadDefaultsWhenMissing(t *testing.T) {
    tmp := t.TempDir()
    s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
    if err != nil {
        t.Fatal(err)
    }
    if s.App.Language != "zh-TW" {
        t.Errorf("default lang = %s, want zh-TW", s.App.Language)
    }
    if s.App.BannerAnimationPref != "video-when-available" {
        t.Errorf("default bannerPref = %s", s.App.BannerAnimationPref)
    }
    if s.Backends.Hoyoverse.HoYoplayPath != `C:\Program Files\HoYoPlay` {
        t.Errorf("default HoYoPlay path = %s", s.Backends.Hoyoverse.HoYoplayPath)
    }
}

func TestSettings_RoundTrip(t *testing.T) {
    tmp := t.TempDir()
    p := filepath.Join(tmp, "settings.toml")
    s := defaultSettings()
    s.App.Language = "en"
    s.Backends.Hoyoverse.HoYoplayPath = "D:/HoYoPlay"
    if err := SaveSettings(p, s); err != nil {
        t.Fatal(err)
    }
    got, err := LoadSettings(p)
    if err != nil {
        t.Fatal(err)
    }
    if got.App.Language != "en" || got.Backends.Hoyoverse.HoYoplayPath != "D:/HoYoPlay" {
        t.Errorf("round-trip mismatch: %+v", got)
    }
    // Confirm file exists on disk
    if _, err := os.Stat(p); err != nil {
        t.Errorf("file not written: %v", err)
    }
}
```

- [ ] **Step 3: Run, verify FAIL**

```bash
go test ./internal/app/...
```

- [ ] **Step 4: Implement `internal/app/settings.go`**

```go
package app

import (
    "errors"
    "os"

    "github.com/pelletier/go-toml/v2"
)

type Settings struct {
    App      AppSettings      `toml:"app"`
    Backends BackendSettings  `toml:"backends"`
}

type AppSettings struct {
    Language            string `toml:"language"`
    BannerAnimationPref string `toml:"banner_animation_pref"`
    ShowTechnicalInfo   bool   `toml:"show_technical_info"`
}

type BackendSettings struct {
    Hoyoverse HoyoverseSettings `toml:"hoyoverse"`
}

type HoyoverseSettings struct {
    HoYoplayPath string `toml:"hoyoplay_path"`
    Region       string `toml:"region"`
}

func defaultSettings() Settings {
    return Settings{
        App: AppSettings{
            Language:            "zh-TW",
            BannerAnimationPref: "video-when-available",
            ShowTechnicalInfo:   false,
        },
        Backends: BackendSettings{
            Hoyoverse: HoyoverseSettings{
                HoYoplayPath: `C:\Program Files\HoYoPlay`,
                Region:       "global",
            },
        },
    }
}

func LoadSettings(path string) (Settings, error) {
    s := defaultSettings()
    b, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) {
        return s, nil
    }
    if err != nil {
        return s, err
    }
    if err := toml.Unmarshal(b, &s); err != nil {
        return s, err
    }
    return s, nil
}

func SaveSettings(path string, s Settings) error {
    b, err := toml.Marshal(s)
    if err != nil {
        return err
    }
    return os.WriteFile(path, b, 0o644)
}
```

- [ ] **Step 5: Run, verify PASS**

```bash
go test ./internal/app/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/app/settings.go internal/app/settings_test.go go.mod go.sum
git commit -m "feat(app): settings.toml load/save with sensible defaults"
```

---

## Task 13: Wails `App` struct with bound commands

**Files:**
- Modify: existing `app.go` from Wails template (rename / replace)
- Create / modify: `internal/app/app.go`

- [ ] **Step 1: Replace Wails template `app.go` content with command bindings**

Replace `app.go` in repo root (Wails template's, OR move logic to `internal/app/app.go` and have root `app.go` just embed it):

```go
package app

import (
    "context"
    "path/filepath"

    "github.com/willie/launcher-collection/internal/core"
    "github.com/willie/launcher-collection/internal/providers/hoyoverse"
)

type App struct {
    ctx       context.Context
    settings  Settings
    settingsP string
    hoyo      *hoyoverse.Provider
}

// New returns an App. settingsPath may be "" → default to alongside the binary.
func New(settingsPath string) *App {
    if settingsPath == "" {
        settingsPath = "settings.toml"
    }
    s, _ := LoadSettings(settingsPath)
    return &App{
        settings:  s,
        settingsP: settingsPath,
        hoyo:      hoyoverse.New(hoyoverse.Settings{HoYoplayPath: s.Backends.Hoyoverse.HoYoplayPath, Region: s.Backends.Hoyoverse.Region}),
    }
}

func (a *App) Startup(ctx context.Context) {
    a.ctx = ctx
}

// ─── Wails-bound commands (return values must be JSON-serializable) ───

type GameRow struct {
    ID            string                  `json:"id"`
    Backend       string                  `json:"backend"`
    DisplayName   core.LocalizedString    `json:"display_name"`
    Installed     bool                    `json:"installed"`
    InstallPath   string                  `json:"install_path,omitempty"`
    Current       string                  `json:"current_version,omitempty"`
    Latest        string                  `json:"latest_version,omitempty"`
    HasPredownload bool                   `json:"has_predownload"`
    IconURL       string                  `json:"icon_url,omitempty"`
}

// ListGames returns one GameRow per known game (installed or not).
func (a *App) ListGames() ([]GameRow, error) {
    installed, err := a.hoyo.DetectInstall(a.ctx)
    if err != nil {
        return nil, err
    }
    seen := map[core.GameID]core.InstalledGame{}
    for _, ig := range installed {
        seen[ig.GameID] = ig
    }
    out := []GameRow{}
    for _, g := range a.hoyo.Games() {
        row := GameRow{
            ID: string(g.ID), Backend: string(g.Backend), DisplayName: g.DisplayName,
        }
        if ig, ok := seen[g.ID]; ok {
            row.Installed = true
            row.InstallPath = ig.InstallPath
        }
        out = append(out, row)
    }
    return out, nil
}

func (a *App) RefreshVersion(gameID string) (core.VersionInfo, error) {
    return a.hoyo.CheckVersion(a.ctx, core.GameID(gameID))
}

func (a *App) GetIcon(gameID string) (string, error) {
    return a.hoyo.GetIcon(a.ctx, core.GameID(gameID))
}

func (a *App) GetBackgrounds(gameID string) ([]core.Background, error) {
    return a.hoyo.GetBackgrounds(a.ctx, core.GameID(gameID))
}

func (a *App) Launch(gameID string) (int, error) {
    return a.hoyo.Launch(a.ctx, core.GameID(gameID), core.LaunchOptions{})
}

func (a *App) GetSettings() Settings { return a.settings }

func (a *App) UpdateSettings(s Settings) error {
    if err := SaveSettings(a.settingsP, s); err != nil {
        return err
    }
    a.settings = s
    a.hoyo = hoyoverse.New(hoyoverse.Settings{HoYoplayPath: s.Backends.Hoyoverse.HoYoplayPath, Region: s.Backends.Hoyoverse.Region})
    return nil
}

// resolveSettingsPath returns ./settings.toml relative to the binary.
func resolveSettingsPath() string { return filepath.Join(".", "settings.toml") }
```

- [ ] **Step 2: Update `main.go` to wire `App`**

```go
package main

import (
    "embed"

    "github.com/willie/launcher-collection/internal/app"

    "github.com/wailsapp/wails/v2"
    "github.com/wailsapp/wails/v2/pkg/options"
    "github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
    a := app.New("")
    err := wails.Run(&options.App{
        Title:            "launcher-collection",
        Width:            1280, Height: 720,
        MinWidth:         1280, MinHeight: 720,
        MaxWidth:         1280, MaxHeight: 720,
        DisableResize:    true,
        Frameless:        true,
        BackgroundColour: &options.RGBA{R: 8, G: 8, B: 14, A: 255},
        AssetServer:      &assetserver.Options{Assets: assets},
        OnStartup:        a.Startup,
        Bind:             []interface{}{a},
    })
    if err != nil {
        println("Error:", err.Error())
    }
}
```

- [ ] **Step 3: Build to confirm compile**

```bash
wails build
```

(May fail if Vue scaffold isn't set yet; check error. If build complains about embedding `frontend/dist` that doesn't exist yet — that's fine, we'll set up frontend next.)

- [ ] **Step 4: Commit**

```bash
git add main.go internal/app/app.go
git commit -m "feat(app): Wails-bound App with ListGames / RefreshVersion / Launch / settings cmds"
```

---

## Task 14: Frontend project structure (Vue 3 + Pinia + i18n)

**Files:**
- Create: `frontend/src/i18n.ts`, `frontend/src/locales/{en,zh-TW}.json`, `frontend/src/stores/{games,settings,view}.ts`, `frontend/src/main.ts` (rewrite)
- Modify: `frontend/package.json`

- [ ] **Step 1: Add Pinia + vue-i18n + Material Symbols**

```bash
cd frontend
npm install pinia vue-i18n@9
```

- [ ] **Step 2: Write `frontend/src/locales/zh-TW.json`**

```json
{
  "publishers": {
    "hoyoverse": "米哈遊",
    "kurogames": "庫洛",
    "hypergryph": "鷹角",
    "perfectworld": "完美世界"
  },
  "buttons": {
    "play": "開始遊戲",
    "play_short": "開始",
    "update": "更新",
    "pause": "暫停"
  },
  "status": {
    "ready": "就緒",
    "update": "更新可用",
    "predownload": "預下載",
    "downloading": "下載中",
    "applying": "套用中",
    "error": "錯誤"
  },
  "labels": {
    "last_run": "上次啟動",
    "checked": "檢查於",
    "minutes_ago": "{n} 分鐘前",
    "days_ago": "{n} 天前",
    "ready_pill": "就緒"
  },
  "footer": {
    "summary": "{backends} 個後端 · {games} 款遊戲",
    "synced": "已同步"
  }
}
```

- [ ] **Step 3: Write `frontend/src/locales/en.json`**

```json
{
  "publishers": {
    "hoyoverse": "HoYoverse",
    "kurogames": "Kuro Games",
    "hypergryph": "Hypergryph",
    "perfectworld": "Perfect World"
  },
  "buttons": {
    "play": "PLAY",
    "play_short": "PLAY",
    "update": "UPDATE",
    "pause": "PAUSE"
  },
  "status": {
    "ready": "READY",
    "update": "UPDATE",
    "predownload": "PRE-DL",
    "downloading": "DOWNLOADING",
    "applying": "APPLYING",
    "error": "ERROR"
  },
  "labels": {
    "last_run": "LAST RUN",
    "checked": "CHECKED",
    "minutes_ago": "{n} MIN AGO",
    "days_ago": "{n} DAYS AGO",
    "ready_pill": "READY"
  },
  "footer": {
    "summary": "{backends} BACKENDS · {games} GAMES",
    "synced": "SYNCED"
  }
}
```

- [ ] **Step 4: Write `frontend/src/i18n.ts`**

```ts
import { createI18n } from 'vue-i18n';
import zhTW from './locales/zh-TW.json';
import en from './locales/en.json';

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-TW',
  fallbackLocale: 'en',
  messages: { 'zh-TW': zhTW, en },
});

export function setLang(lang: 'zh-TW' | 'en') {
  i18n.global.locale.value = lang;
  document.documentElement.setAttribute('lang', lang === 'en' ? 'en' : 'zh-Hant');
}
```

- [ ] **Step 5: Write `frontend/src/stores/games.ts`**

```ts
import { defineStore } from 'pinia';
import { ListGames, RefreshVersion, GetIcon, GetBackgrounds } from '../../wailsjs/go/app/App';

export type GameRow = {
  id: string;
  backend: string;
  display_name: Record<string, string>;
  installed: boolean;
  install_path?: string;
  current_version?: string;
  latest_version?: string;
  has_predownload: boolean;
  icon_url?: string;
  background_url?: string;
  background_video?: string;
};

export const useGamesStore = defineStore('games', {
  state: () => ({
    games: [] as GameRow[],
    selectedID: '' as string,
  }),
  actions: {
    async load() {
      this.games = await ListGames();
      if (!this.selectedID && this.games.length) this.selectedID = this.games[0].id;
    },
    async refreshVersions() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          const v = await RefreshVersion(g.id);
          g.current_version = v.Current;
          g.latest_version = v.Latest;
          g.has_predownload = !!v.Predownload;
        } catch (e) {
          console.warn('version check failed', g.id, e);
        }
      }
    },
    async loadAssets() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          if (!g.icon_url) g.icon_url = await GetIcon(g.id);
          const bgs = await GetBackgrounds(g.id);
          if (bgs.length) {
            g.background_url = bgs[0].ImageURL;
            g.background_video = bgs[0].VideoURL;
          }
        } catch (e) {
          console.warn('assets failed', g.id, e);
        }
      }
    },
    select(id: string) { this.selectedID = id; },
  },
  getters: {
    selected(state): GameRow | undefined {
      return state.games.find((g) => g.id === state.selectedID);
    },
  },
});
```

- [ ] **Step 6: Write `frontend/src/stores/view.ts`**

```ts
import { defineStore } from 'pinia';

export const useViewStore = defineStore('view', {
  state: () => ({
    sidebarCollapsed: false,
    viewMode: 'detail' as 'detail' | 'grid',
  }),
  actions: {
    toggleSidebar() { this.sidebarCollapsed = !this.sidebarCollapsed; },
    setView(v: 'detail' | 'grid') { this.viewMode = v; },
  },
});
```

- [ ] **Step 7: Update `frontend/src/main.ts`**

```ts
import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { i18n } from './i18n';
import App from './App.vue';

createApp(App).use(createPinia()).use(i18n).mount('#app');
```

- [ ] **Step 8: Smoke build**

```bash
npm run build
```

Expected: succeeds (Wails-generated bindings are stubs; if `wailsjs/go/app/App` import fails, run `wails generate module` from repo root).

- [ ] **Step 9: Commit**

```bash
cd ..
git add frontend/
git commit -m "feat(frontend): Pinia + vue-i18n setup with games / view stores and locale JSON"
```

---

## Task 15: Frontend chrome — copy v14 mockup CSS into `theme.css`

**Files:**
- Create: `frontend/src/styles/theme.css`

- [ ] **Step 1: Copy CSS from `aesthetic-v14.html` into `frontend/src/styles/theme.css`**

Open `.superpowers/brainstorm/<latest-session>/content/aesthetic-v14.html` (or wherever v14 currently lives), copy the entire `<style>...</style>` content, paste into `frontend/src/styles/theme.css`. Keep CSS variables (`:root`), all `.app-wrap` / `.topbar` / `.sidebar` / `.bottom-bar` / `.grid-card` / etc.

- [ ] **Step 2: Add Google Fonts imports at top of `theme.css`**

```css
@import url('https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@300;400;500;600;700;800&family=Unbounded:wght@400;500;700;900&family=Noto+Sans+TC:wght@500;700;900&display=swap');
@import url('https://fonts.googleapis.com/css2?family=Material+Symbols+Outlined:opsz,wght,FILL,GRAD@24,400,0,0');
```

- [ ] **Step 3: Import `theme.css` in `App.vue`'s `<style>` block (or in `main.ts`)**

In `frontend/src/main.ts` add:
```ts
import './styles/theme.css';
```

- [ ] **Step 4: Smoke build**

```bash
cd frontend && npm run build
```

- [ ] **Step 5: Commit**

```bash
cd ..
git add frontend/src/styles/theme.css frontend/src/main.ts
git commit -m "feat(frontend): port v14 mockup theme.css (operator-console aesthetic)"
```

---

## Task 16: Frontend components — port v14 markup into Vue components

**Files:**
- Create: `frontend/src/App.vue`, `frontend/src/components/{BgLayer,Topbar,Sidebar,SidebarRow,DetailView,GridView,GridCard,BottomBar,Footbar}.vue`

- [ ] **Step 1: Write `App.vue`** — top-level layout matching v14 structure

```vue
<script setup lang="ts">
import { onMounted, computed } from 'vue';
import { useGamesStore } from './stores/games';
import { useViewStore } from './stores/view';
import BgLayer from './components/BgLayer.vue';
import Topbar from './components/Topbar.vue';
import Sidebar from './components/Sidebar.vue';
import DetailView from './components/DetailView.vue';
import GridView from './components/GridView.vue';
import BottomBar from './components/BottomBar.vue';
import Footbar from './components/Footbar.vue';

const games = useGamesStore();
const view = useViewStore();

const appClass = computed(() => ({
  collapsed: view.sidebarCollapsed,
  'grid-mode': view.viewMode === 'grid',
}));

onMounted(async () => {
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
});
</script>

<template>
  <div class="app-wrap">
    <BgLayer />
    <div class="top-fade"></div>
    <div class="app" :class="appClass">
      <Sidebar />
      <Topbar />
      <main class="main">
        <DetailView v-if="view.viewMode === 'detail'" />
        <GridView v-else />
      </main>
      <Footbar />
      <BottomBar v-if="view.viewMode === 'detail'" />
    </div>
  </div>
</template>
```

- [ ] **Step 2: Write `BgLayer.vue`** (cross-fade two img + video element)

```vue
<script setup lang="ts">
import { ref, watch } from 'vue';
import { useGamesStore } from '../stores/games';

const games = useGamesStore();
const slotA = ref(''); const slotB = ref(''); const video = ref(''); const useA = ref(true);

watch(() => games.selected, (g) => {
  if (!g) return;
  if (g.background_video) { video.value = g.background_video; return; }
  video.value = '';
  const url = g.background_url || '';
  if (useA.value) { slotB.value = url; useA.value = false; }
  else            { slotA.value = url; useA.value = true; }
}, { immediate: true });
</script>

<template>
  <div class="app-bg-layer">
    <img class="app-bg" :class="{fading: !useA}" :src="slotA" alt="" />
    <img class="app-bg" :class="{fading:  useA}" :src="slotB" alt="" />
    <video class="app-bg-video" :class="{hidden: !video}" :src="video" autoplay muted loop playsinline></video>
  </div>
</template>
```

- [ ] **Step 3: Write `Sidebar.vue`** + `SidebarRow.vue` (groups, rows, collapse chevron)

`Sidebar.vue`:
```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore, type GameRow } from '../stores/games';
import { useViewStore } from '../stores/view';
import { useI18n } from 'vue-i18n';
import SidebarRow from './SidebarRow.vue';

const games = useGamesStore();
const view = useViewStore();
const { t } = useI18n();

type Group = { backend: string; rows: GameRow[] };
const groups = computed<Group[]>(() => {
  const m = new Map<string, GameRow[]>();
  for (const g of games.games) (m.get(g.backend) ?? m.set(g.backend, []).get(g.backend)!).push(g);
  return [...m.entries()].map(([backend, rows]) => ({ backend, rows }));
});
</script>

<template>
  <aside class="sidebar" :class="{collapsed: view.sidebarCollapsed}">
    <div class="sidebar-header">
      <button class="icon-btn" @click="view.toggleSidebar()" title="Toggle">‹</button>
    </div>
    <template v-for="g in groups" :key="g.backend">
      <div class="group-label"><span>{{ t(`publishers.${g.backend}`) }}</span></div>
      <SidebarRow v-for="row in g.rows" :key="row.id" :row="row" />
    </template>
  </aside>
</template>
```

`SidebarRow.vue`:
```vue
<script setup lang="ts">
import { useGamesStore, type GameRow } from '../stores/games';
import { useI18n } from 'vue-i18n';

const props = defineProps<{ row: GameRow }>();
const games = useGamesStore();
const { t, locale } = useI18n();

const status = () => {
  if (!props.row.installed) return { key: 'not_installed', label: '—', cls: '' };
  if (props.row.has_predownload) return { key: 'predownload', label: t('status.predownload') + ' · 0%', cls: 'predownload' };
  if (props.row.latest_version && props.row.current_version && props.row.latest_version !== props.row.current_version)
    return { key: 'update', label: `${t('status.update')} · ${props.row.current_version} → ${props.row.latest_version}`, cls: 'update' };
  return { key: 'ready', label: `${t('status.ready')} · v${props.row.current_version || props.row.latest_version || '?'}`, cls: 'ready' };
};
</script>

<template>
  <div class="game-row" :class="{active: row.id === games.selectedID}" @click="games.select(row.id)" :title="row.display_name[locale as string] || row.display_name.en">
    <div class="game-icon">
      <img v-if="row.icon_url" :src="row.icon_url" :alt="row.display_name.en" />
      <span v-else>{{ (row.display_name['zh-TW'] || row.display_name.en)[0] }}</span>
    </div>
    <div>
      <div class="game-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
      <div class="game-status-mini" :class="status().cls">{{ status().label }}</div>
    </div>
    <span class="game-marker" :class="status().cls"></span>
  </div>
</template>
```

- [ ] **Step 4: Write `Topbar.vue`** — language / grid toggle / settings + window controls

```vue
<script setup lang="ts">
import { useViewStore } from '../stores/view';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';

const view = useViewStore();
const toggleLang = () => setLang(i18n.global.locale.value === 'zh-TW' ? 'en' : 'zh-TW');
</script>

<template>
  <div class="topbar">
    <div class="toolbar">
      <button class="icon-btn" @click="toggleLang"><span class="material-symbols-outlined">translate</span></button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>
      <button class="icon-btn window-btn" @click="WindowMinimise()" title="Minimize">─</button>
      <button class="icon-btn window-btn close" @click="Quit()" title="Close">×</button>
    </div>
  </div>
</template>
```

- [ ] **Step 5: Write `BottomBar.vue`** (status line + LAUNCH)

```vue
<script setup lang="ts">
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';
import { Launch } from '../../wailsjs/go/app/App';

const games = useGamesStore();
const { t } = useI18n();
const onLaunch = async () => {
  if (games.selected) try { await Launch(games.selected.id); } catch (e) { console.error(e); }
};
</script>

<template>
  <div v-if="games.selected" class="bottom-bar">
    <div class="hero-stats-line">
      <span class="pill">{{ t('labels.ready_pill') }}</span>
      <span class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
    </div>
    <div class="launch-area">
      <button class="launch-btn" @click="onLaunch" :disabled="!games.selected.installed">
        <span class="play-tri"></span>{{ t('buttons.play') }}
      </button>
    </div>
  </div>
</template>
```

- [ ] **Step 6: Write `DetailView.vue`** (currently just an empty container — bg layer handles visuals)

```vue
<template><div class="view view-detail"></div></template>
```

- [ ] **Step 7: Write `GridView.vue` + `GridCard.vue`** (grouped cards)

`GridView.vue`:
```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import GridCard from './GridCard.vue';

const games = useGamesStore();
const view = useViewStore();
const { t } = useI18n();
const groups = computed(() => {
  const m = new Map<string, typeof games.games>();
  for (const g of games.games) (m.get(g.backend) ?? m.set(g.backend, []).get(g.backend)!).push(g);
  return [...m.entries()];
});
const onCard = (id: string) => { games.select(id); view.setView('detail'); };
</script>

<template>
  <div class="view view-grid">
    <div v-for="[backend, rows] in groups" :key="backend">
      <div class="grid-section-label"><span>{{ t(`publishers.${backend}`) }}</span></div>
      <div class="grid-cards">
        <GridCard v-for="row in rows" :key="row.id" :row="row" @click="onCard(row.id)" />
      </div>
    </div>
  </div>
</template>
```

`GridCard.vue`:
```vue
<script setup lang="ts">
import type { GameRow } from '../stores/games';
import { useI18n } from 'vue-i18n';
const props = defineProps<{ row: GameRow }>();
const { t, locale } = useI18n();
</script>

<template>
  <div class="grid-card">
    <div class="grid-card-art">
      <img v-if="row.background_url" :src="row.background_url" alt="" />
      <span class="grid-card-status-pill ready">v{{ row.current_version || '?' }}</span>
      <div class="grid-card-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
    </div>
    <div class="grid-card-foot">
      <span class="grid-card-substatus ready">{{ t('status.ready') }}</span>
      <button class="grid-card-btn primary" @click.stop="">{{ t('buttons.play_short') }}</button>
    </div>
  </div>
</template>
```

- [ ] **Step 8: Write `Footbar.vue`**

```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';

const games = useGamesStore();
const { t } = useI18n();
const summary = computed(() => t('footer.summary', { backends: 1, games: games.games.length }));
</script>

<template>
  <div class="footbar">
    <span><span class="ok">●</span> {{ summary }}</span>
    <span class="bl">{{ t('footer.synced') }}</span>
    <span style="margin-left: auto">v0.1.0 · AGPL-3.0</span>
  </div>
</template>
```

- [ ] **Step 9: Build, smoke-check it compiles**

```bash
cd frontend && npm run build
```

- [ ] **Step 10: Commit**

```bash
cd ..
git add frontend/
git commit -m "feat(frontend): port v14 mockup into Vue components (sidebar / topbar / detail / grid / bottom-bar / footbar)"
```

---

## Task 17: End-to-end dev run + manual smoke

**Files:** none (manual QA + minor fixes if found)

- [ ] **Step 1: Generate Wails bindings**

```bash
wails generate module
```

Expected: writes `frontend/wailsjs/go/app/App.{js,d.ts}` and `frontend/wailsjs/runtime/runtime.{js,d.ts}`.

- [ ] **Step 2: Run dev mode**

```bash
wails dev
```

Expected:
- Frameless 1280×720 window opens.
- Sidebar shows 3 games (原神 / 崩壞：星穹鐵道 / 絕區零) under "米哈遊" group, each with real icon from API + `就緒 · vX.Y.Z`.
- Detail view shows the selected game's banner; selecting another game cross-fades to its banner. ZZZ shows the `.webm` video bg.
- Toolbar: clicking `translate` switches all chrome / game names to English. Clicking `grid_view` shows grid; clicking a card returns to detail with that game selected. Clicking `─` minimizes; `×` exits.
- Bottom-right: `▶ 開始遊戲` button. Clicking it spawns the game's actual `.exe` (HoYoPlay is installed).

- [ ] **Step 3: If anything breaks, fix in-place. Common issues**

- API rate-limit / network: ensure Settings has correct `hoyoplay_path`.
- `game_ids[]=` query encoding: if `getGamePackages` returns empty, build the query manually in `version.go` (avoid `url.Values` for that param).
- Wails binding mismatch: re-run `wails generate module`.

- [ ] **Step 4: Commit any fixes**

```bash
git add .
git commit -m "fix: end-to-end smoke fixes from dev run"
```

(Skip commit if no fixes were needed.)

---

## Task 18: Tag M1, merge to main

- [ ] **Step 1: Final test pass**

```bash
go test ./...
cd frontend && npm run build && cd ..
```

Expected: all green.

- [ ] **Step 2: Merge `m1/read-only-library` to `main` with `--no-ff`**

```bash
git checkout main
git merge --no-ff m1/read-only-library -m "merge: M1 read-only library (detect / version / banner / launch)"
```

- [ ] **Step 3: Tag M1**

```bash
git tag v0.1.0-m1
```

(No `git push` — local only unless user opts in.)

- [ ] **Step 4: Verify history**

```bash
git log --graph --oneline --all
```

Expected: visible branch-out + merge-in pattern, M1 commits attached, tag on the merge commit.

---

## Self-Review Notes

- **Spec coverage**: M1 covers Phase-1 spec sections 1, 2 (audience: self), 3 (stack), 4 (architecture skeleton + provider abstraction), 5 (Provider interface — with placeholders for `PlanUpdate` / `Download` / `Apply` left to M2), 6 (data model: types + settings TOML), 7.1-7.2 + 7.4 (HoYoverse API + DetectInstall + Launch only — Download / Apply / patches deferred to M2), 8 (UI design from mockup), 9 (logging stub via Go default; full `slog` setup in M2), 10 (plan items 1-5 + 6-launch only).
- **Deferred to M2**: download / apply / hpatchz / pre-download flow, Sophon path, plan persistence, integrity checks, error UI surface, structured logging.
- **Placeholder check**: no `TBD` / `implement later` left (one code comment about manual API verification in Task 9 step 3 is actionable, not vague).
- **Type consistency**: `GameID`, `BackendID`, `LocalizedString`, `Background`, `VersionInfo` used consistently across tasks. Provider methods match interface declared in Task 4.

---

## What's next after M1

- **Plan #2 (M2)**: Download + Apply (legacy URL path, full-replace + hdiff via `hpatchz`). Plan persistence to `downloads/<plan>.json`. Per-file integrity. Cancel/resume. UI progress events.
- **Plan #3 (M3)**: Pre-download flow. Sophon path (if active games require). Error handling UI. `slog` structured logging. Real e2e smoke against next live patch release.
