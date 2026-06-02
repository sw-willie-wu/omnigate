# Per-Game Install Paths Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move install location from per-backend roots to per-game folders, resolved via override → launcher-config → default-scan → unresolved, editable from a gear button + popover left of the Play button.

**Architecture:** The App layer owns resolution and is the single source of truth for each game's install folder. Providers expose a `DefaultScan` (layer 3) and an optional `InstallLocator` (layer 2, reads the launcher's own records); the App combines them with per-game overrides (layer 1) into a resolved `map[GameID]resolvedEntry{path,source}`, stat-validates non-override layers, and injects the resolved folders into each provider via `SetResolvedPaths` (generalising hoyoverse's existing `gameDirFn` seam). `GameRow` carries the resolved path + source + raw override so the popover can render everything.

**Tech Stack:** Go 1.26 (Wails backend), Vue 3 + Pinia + vue-i18n (frontend), TOML settings, Vitest + `go test`.

**Spec:** `docs/superpowers/specs/2026-06-02-omnigate-per-game-install-paths-design.md`

**Conventions:** TDD red→green→commit per task. No `Co-Authored-By` trailer. Commit messages use conventional prefixes (`feat`/`test`/`refactor`/`fix`). Go on PATH for subagents: `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. CGO is disabled — never pass `-race`. Run Go tests with `-count=1` for the touched package; whole-repo `go test ./...` before phase boundaries.

---

## File Structure

**Core (`internal/core/`)**
- `provider.go` — add `InstallLocator` interface, `InstallSource` enum constants, `Installer`/`ResolvedPathSetter` capability interfaces.

**Providers (`internal/providers/{hoyoverse,kurogames,hypergryph}/`)**
- `detect.go` — add a `DefaultRoot` constant + `DefaultScan(ctx)` method delegating to the existing package `DetectInstall(ctx, root)`.
- `<provider>.go` — add `resolvedPaths` field + `SetResolvedPaths`; repurpose `DetectInstall(ctx)` to return injected resolved entries that exist; route `gameDir`/loop sites through the injected map; remove the `path` field from `SettingsSchema()`.
- (Phase 2) `locator.go` + `locator_test.go` + `testdata/` — `InstallLocator` impl per launcher.

**App (`internal/app/`)**
- `settings.go` — schema v2 (`Games` map, drop `Backends.*.Path`), v1→v2 migration.
- `resolve.go` (new) — resolution chain + per-backend resolution cache + override validity.
- `app.go` — `GameRow` extension; `ListGames`/`ListBackends` read resolution; inject resolved paths in `constructProviders`; new RPCs.
- `update_handler.go` — `gameInstallDir` reads the App resolution map.

**Frontend (`frontend/src/`)**
- `stores/games.ts` — `GameRow` type fields + per-game override actions.
- `components/GameConfigPopover.vue` (new) + `BottomBar.vue` (gear button).
- `components/SettingsPanel.vue` — remove per-backend Path rows.
- `locales/{en,zh-TW,zh-CN}.json` — new keys.

---

# PHASE 1 — Core + contract (test-verified; no user-visible override yet)

## Task 1: Provider default-root constant + `DefaultScan` method

**Files:**
- Modify: `internal/providers/hoyoverse/detect.go`, `internal/providers/kurogames/detect.go`, `internal/providers/hypergryph/detect.go`
- Modify: `internal/providers/hoyoverse/hoyoverse.go`, `internal/providers/kurogames/kurogames.go`, `internal/providers/hypergryph/hypergryph.go`
- Test: each provider's existing `detect_test.go`

Each backend keeps its **existing, non-uniform** scan (`DetectInstall(ctx, root)`) and gains a `DefaultRoot` constant + a `DefaultScan(ctx)` method that returns `map[GameID]string`.

- [ ] **Step 1: Write failing test** (hoyoverse `detect_test.go`)

```go
func TestDefaultScan_UsesDefaultRoot(t *testing.T) {
	// DefaultScan delegates to DetectInstall against DefaultRoot; with no install
	// present it returns an empty (non-nil) map, no error.
	p := New(Settings{}, nil)
	got, err := p.DefaultScan(context.Background())
	if err != nil {
		t.Fatalf("DefaultScan err: %v", err)
	}
	if got == nil {
		t.Fatalf("DefaultScan returned nil map")
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/providers/hoyoverse/ -run TestDefaultScan -count=1` → `p.DefaultScan undefined`.

- [ ] **Step 3: Implement** — in `hoyoverse/detect.go` add:

```go
// DefaultRoot is the layer-3 fallback install root for this backend (the former
// settings default). Per-game resolution scans here when no override or
// launcher-config entry applies.
const DefaultRoot = `C:\Program Files\HoYoPlay`
```

In `hoyoverse.go` add (near `DetectInstall`):

```go
// DefaultScan returns each known game found under DefaultRoot (layer 3 of
// resolution). Keyed by game ID. Empty map when nothing is installed.
func (p *Provider) DefaultScan(ctx context.Context) (map[core.GameID]string, error) {
	games, err := DetectInstall(ctx, DefaultRoot)
	if err != nil {
		return nil, err
	}
	out := make(map[core.GameID]string, len(games))
	for _, g := range games {
		out[g.GameID] = g.InstallPath
	}
	return out, nil
}
```

Repeat for kurogames (`DefaultRoot = ``C:\Program Files\Wuthering Waves``) and hypergryph (`DefaultRoot = ``C:\Program Files\GRYPHLINK``), each delegating to its own package `DetectInstall(ctx, DefaultRoot)` (which already has the correct, backend-specific join + existence semantics — DO NOT change those).

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/providers/... -run TestDefaultScan -count=1`.

- [ ] **Step 5: Commit** — `feat(game-paths): per-provider DefaultRoot + DefaultScan`

---

## Task 2: `core.InstallLocator` + `InstallSource` enum + resolved-path injection interfaces

**Files:**
- Modify: `internal/core/provider.go`
- Test: `internal/core/provider_test.go` (compile-only assertion)

- [ ] **Step 1: Write failing test**

```go
func TestInstallSourceConstants(t *testing.T) {
	for _, s := range []InstallSource{SourceOverride, SourceLauncher, SourceDefault, SourceUnresolved} {
		if string(s) == "" {
			t.Errorf("empty InstallSource constant")
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/core/ -run TestInstallSourceConstants -count=1` → undefined.

- [ ] **Step 3: Implement** — in `internal/core/provider.go`:

```go
// InstallSource is how a game's resolved install folder was determined.
type InstallSource string

const (
	SourceOverride   InstallSource = "override"   // user-set Settings.Games[id].Path
	SourceLauncher   InstallSource = "launcher"   // read from the launcher's own records
	SourceDefault    InstallSource = "default"    // found under the backend DefaultRoot
	SourceUnresolved InstallSource = "unresolved" // not found anywhere
)

// InstallLocator is an optional Provider capability: read the launcher's own
// record of where each installed game lives (registry / AppData), independent
// of any configured root. Best-effort — partial/empty results and errors are
// acceptable; the App stat-validates every returned path before trusting it.
type InstallLocator interface {
	LocateInstalls(ctx context.Context) (map[GameID]string, error)
}

// ResolvedPathSetter is an optional Provider capability: accept the App-resolved
// per-game install folders so the provider's launch/version/update operations
// use them instead of re-deriving from a single root.
type ResolvedPathSetter interface {
	SetResolvedPaths(paths map[GameID]string)
}
```

- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** — `feat(game-paths): core InstallLocator + InstallSource + ResolvedPathSetter`

---

## Task 3: Provider resolved-path injection (`SetResolvedPaths`) + repurpose `DetectInstall`

**Files:**
- Modify: `internal/providers/{hoyoverse,kurogames,hypergryph}/<provider>.go`
- Test: each provider's `*_test.go`

Each provider stores an injected `resolvedPaths map[GameID]string`. `DetectInstall(ctx)` now returns the injected entries that **stat-exist** (instead of scanning `settings.Path`). For hoyoverse, wire the injected map through the existing `gameDirFn` seam so `gameDir` reads it.

- [ ] **Step 1: Write failing test** (hoyoverse `hoyoverse_test.go`)

```go
func TestSetResolvedPaths_DetectInstallReturnsExisting(t *testing.T) {
	dir := t.TempDir() // exists
	gid := core.GameID("hoyoverse/genshin")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{
		gid:                      dir,
		"hoyoverse/starrail":     filepath.Join(dir, "does-not-exist"),
	})
	got, err := p.DetectInstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only the existing path is reported installed.
	if len(got) != 1 || got[0].GameID != gid || got[0].InstallPath != dir {
		t.Fatalf("got %+v, want only %s at %s", got, gid, dir)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `SetResolvedPaths undefined`.

- [ ] **Step 3: Implement** — in `hoyoverse.go`:
  - Add field `resolvedPaths map[core.GameID]string` to `Provider`.
  - Add method:
    ```go
    // SetResolvedPaths injects the App-resolved per-game install folders.
    func (p *Provider) SetResolvedPaths(paths map[core.GameID]string) {
        p.resolvedPaths = paths
        // Route gameDir through the injected map (subsumes the old gameDirFn seam).
        p.gameDirFn = func(gid core.GameID) (string, error) {
            if dir, ok := p.resolvedPaths[gid]; ok && dir != "" {
                return dir, nil
            }
            return "", fmt.Errorf("gameDir: %w (gid=%s)", core.ErrUnknownGame, gid)
        }
    }
    ```
  - Replace the body of `DetectInstall(ctx)`:
    ```go
    func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
        out := []core.InstalledGame{}
        for gid, dir := range p.resolvedPaths {
            if dir == "" {
                continue
            }
            if st, err := os.Stat(dir); err == nil && st.IsDir() {
                out = append(out, core.InstalledGame{GameID: gid, InstallPath: dir})
            }
        }
        return out, nil
    }
    ```
    (Add `os` import if missing.)
  - Repeat for kurogames + hypergryph: add `resolvedPaths` field + `SetResolvedPaths` (no `gameDirFn` for those — they have none; instead their CheckVersion/Launch/update loop sites read `p.resolvedPaths[gid]` directly; see Task 4). Their `DetectInstall(ctx)` becomes the same existing-entry loop.

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/providers/... -run TestSetResolvedPaths -count=1`.
- [ ] **Step 5: Commit** — `refactor(game-paths): providers report App-injected resolved paths`

---

## Task 4: Route kuro/hyper public methods through resolved paths

**Files:**
- Modify: `internal/providers/kurogames/kurogames.go` (CheckVersion, Launch, CheckForUpdateWithProgress, RunUpdate), `internal/providers/hypergryph/hypergryph.go` (CheckVersion, Launch, installPathFor)
- Test: kurogames/hypergryph `*_test.go`

These methods currently call `DetectInstall(ctx, p.settings.Path)` (package func) inline. Replace each with a helper that reads the injected map, falling back to the (now resolved) `DetectInstall(ctx)` method.

- [ ] **Step 1: Write failing test** (kurogames `kurogames_test.go`)

```go
func TestGameDirFromResolved(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("kurogames/wutheringwaves")
	p := New(Settings{}, nil)
	p.SetResolvedPaths(map[core.GameID]string{gid: dir})
	got, err := p.gameDir(gid)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("gameDir = %q, want %q", got, dir)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `p.gameDir undefined` (or wrong source).

- [ ] **Step 3: Implement** — add to kurogames (and hypergryph) a private resolver and route all four/three sites through it:

```go
// gameDir returns the resolved install folder for gid (App-injected).
func (p *Provider) gameDir(gid core.GameID) (string, error) {
	if dir, ok := p.resolvedPaths[gid]; ok && dir != "" {
		return dir, nil
	}
	return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}
```

Replace each inline `DetectInstall(ctx, p.settings.Path)` + loop with `dir, err := p.gameDir(gid)`. For hypergryph rename/replace `installPathFor` to delegate to `gameDir`. Keep behavior identical otherwise.

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/providers/kurogames/ ./internal/providers/hypergryph/ -count=1`.
- [ ] **Step 5: Commit** — `refactor(game-paths): kuro/hyper ops read resolved paths`

---

## Task 5: Settings schema v2 (`Games` map, drop `Backends.*.Path`)

**Files:**
- Modify: `internal/app/settings.go`
- Test: `internal/app/settings_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestSettingsV2_GamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.toml")
	s := defaultSettings()
	s.Games = map[string]GameSettings{"hoyoverse/genshin": {Path: `D:\G`}}
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if got.Games["hoyoverse/genshin"].Path != `D:\G` {
		t.Errorf("override not round-tripped: %+v", got.Games)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `s.Games undefined` / version 1.

- [ ] **Step 3: Implement** — in `settings.go`:
  - Add `Games map[string]GameSettings \`toml:"games"\`` to `Settings`; add `type GameSettings struct { Path string \`toml:"path,omitempty"\` }`.
  - Remove `Path` from `HoyoverseSettings`/`KurogamesSettings`/`HypergryphSettings` (keep `Region`, `TempDir`).
  - `defaultSettings()`: set `Version: 2`, drop the three `Path:` values, init `Games: map[string]GameSettings{}`.
  - In `rawTOML`: drop `Path` from the per-backend structs (hoyoverse keeps `HoYoplayPath` for v0 migration + `Region`), add `Games map[string]GameSettings \`toml:"games"\``.
  - In `LoadSettings`: change the trailing `out.Version = 1` to the migration logic of Task 6 (for now, set `out.Version = 2` and copy `out.Games = raw.Games` if non-nil, else `map{}`).
  - In `SaveSettings`: change `s.Version = 1` → `s.Version = 2`.

- [ ] **Step 4: Run, expect PASS.** Also run `go build ./...` — expect compile errors at every `settings.Backends.*.Path` reader (app.go constructProviders, provider PrimaryPath). These are fixed in Tasks 3/7/9; for THIS task, only update `constructProviders` to stop passing `Path` into provider `Settings` (the provider `Settings` structs lose `Path` too — Task 3 removed their use; remove the `Path` field from each provider `Settings` struct and the `Path:` line in `constructProviders`). Re-run `go build ./...` green.

- [ ] **Step 5: Commit** — `feat(game-paths): settings schema v2 (per-game overrides)`

---

## Task 6: v1→v2 migration

**Files:**
- Modify: `internal/app/settings.go`
- Test: `internal/app/settings_test.go`

On loading a v1 file (`version < 2`, has `Backends.*.Path`), derive per-game overrides so no currently-detected game is lost, preferring NO override when the path equals the default-scan path (so post-move re-detection still works), and seeding overrides for a custom-but-offline root without requiring stat.

- [ ] **Step 1: Write failing tests**

```go
func TestMigrateV1_CustomRoot_WritesOverrides(t *testing.T) {
	root := t.TempDir()
	mk := func(seg ...string) { _ = os.MkdirAll(filepath.Join(append([]string{root}, seg...)...), 0o755) }
	mk("games", "GenshinImpact") // hoyoverse layout
	raw := "version = 1\n[backends.hoyoverse]\npath = '" + root + "'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)

	got, err := LoadSettings(p)
	if err != nil { t.Fatal(err) }
	if got.Version != 2 { t.Fatalf("version=%d", got.Version) }
	want := filepath.Join(root, "games", "GenshinImpact")
	if got.Games["hoyoverse/genshin"].Path != want {
		t.Errorf("override=%q want %q", got.Games["hoyoverse/genshin"].Path, want)
	}
}

func TestMigrateV1_DefaultRoot_NoOverride(t *testing.T) {
	// A v1 file whose root == the backend DefaultRoot yields no override
	// (so the game stays auto-detected and re-detectable after a move).
	raw := "version = 1\n[backends.hoyoverse]\npath = 'C:\\Program Files\\HoYoPlay'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)
	got, _ := LoadSettings(p)
	if _, ok := got.Games["hoyoverse/genshin"]; ok {
		t.Errorf("unexpected override for default-root user: %+v", got.Games)
	}
}
```

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement** — add a migration helper invoked from `LoadSettings` when `raw.Version < 2` and a per-backend root is present. For each backend, compute candidate per-game paths from the v1 root using the **backend's known layout** (a small migration table mapping backend → (hasGamesSegment bool, gameID → folderName)). For each known game:
  - candidate = `filepath.Join(root, [games,] folderName)`.
  - defaultCandidate = `filepath.Join(<DefaultRoot>, [games,] folderName)`.
  - If `root == DefaultRoot`: write no override.
  - Else: write `Games[gid] = {Path: candidate}` (do NOT stat — preserves offline custom drives).

  Define the folder-name table in `settings.go` (mirrors each provider's `games` table; keep it local to migration to avoid an import cycle). Set `out.Version = 2`.

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/app/ -run TestMigrateV1 -count=1`. Also keep the v0→v1 `hoyoplay_path` test green.
- [ ] **Step 5: Commit** — `feat(game-paths): v1→v2 settings migration`

---

## Task 7: App resolution chain (`resolve.go`)

**Files:**
- Create: `internal/app/resolve.go`, `internal/app/resolve_test.go`
- Modify: `internal/app/app.go` (App struct: add resolution cache field)

- [ ] **Step 1: Write failing test** (`resolve_test.go`)

```go
type fakeResolveProvider struct {
	id       core.BackendID
	def      map[core.GameID]string
	located  map[core.GameID]string // nil → no InstallLocator
}
func (f *fakeResolveProvider) ID() core.BackendID { return f.id }
func (f *fakeResolveProvider) DefaultScan(context.Context) (map[core.GameID]string, error) { return f.def, nil }
func (f *fakeResolveProvider) LocateInstalls(context.Context) (map[core.GameID]string, error) {
	if f.located == nil { return nil, nil }
	return f.located, nil
}

func TestResolve_Precedence(t *testing.T) {
	existing := t.TempDir()
	gid := core.GameID("x/a")
	a := &App{settings: Settings{Games: map[string]GameSettings{}}}

	// override (verbatim, even if missing) beats everything
	a.settings.Games[string(gid)] = GameSettings{Path: `Z:\override`}
	r := a.resolveOne(context.Background(), &fakeResolveProvider{id: "x", def: map[core.GameID]string{gid: existing}, located: map[core.GameID]string{gid: existing}}, gid)
	if r.Path != `Z:\override` || r.Source != core.SourceOverride { t.Errorf("override: %+v", r) }

	// no override: launcher (stat-valid) beats default
	delete(a.settings.Games, string(gid))
	r = a.resolveOne(context.Background(), &fakeResolveProvider{id: "x", def: map[core.GameID]string{gid: `Q:\nope`}, located: map[core.GameID]string{gid: existing}}, gid)
	if r.Path != existing || r.Source != core.SourceLauncher { t.Errorf("launcher: %+v", r) }

	// launcher stale (missing) → falls to default
	r = a.resolveOne(context.Background(), &fakeResolveProvider{id: "x", def: map[core.GameID]string{gid: existing}, located: map[core.GameID]string{gid: `Q:\nope`}}, gid)
	if r.Path != existing || r.Source != core.SourceDefault { t.Errorf("default: %+v", r) }

	// nothing → unresolved
	r = a.resolveOne(context.Background(), &fakeResolveProvider{id: "x", def: map[core.GameID]string{}}, gid)
	if r.Source != core.SourceUnresolved { t.Errorf("unresolved: %+v", r) }
}
```

- [ ] **Step 2: Run, expect FAIL** — `resolveOne undefined`.

- [ ] **Step 3: Implement** — `resolve.go`:

```go
package app

import (
	"context"
	"os"

	"omnigate/internal/core"
)

type resolvedEntry struct {
	Path   string
	Source core.InstallSource
}

// defaultScanner/installLocator are the subset of provider capabilities
// resolution needs (kept narrow for testing).
type defaultScanner interface {
	ID() core.BackendID
	DefaultScan(ctx context.Context) (map[core.GameID]string, error)
}

func statDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// resolveOne resolves a single game's install folder + source.
func (a *App) resolveOne(ctx context.Context, p defaultScanner, gid core.GameID) resolvedEntry {
	// 1. override — verbatim, even if it doesn't exist (validity surfaced by caller)
	a.settingsMu.RLock()
	ov := a.settings.Games[string(gid)].Path
	a.settingsMu.RUnlock()
	if ov != "" {
		return resolvedEntry{Path: ov, Source: core.SourceOverride}
	}
	// 2. launcher-config (stat-validated)
	if loc, ok := p.(core.InstallLocator); ok {
		if m, err := loc.LocateInstalls(ctx); err == nil {
			if dir := m[gid]; statDir(dir) {
				return resolvedEntry{Path: dir, Source: core.SourceLauncher}
			}
		}
	}
	// 3. default scan
	if m, err := p.DefaultScan(ctx); err == nil {
		if dir := m[gid]; statDir(dir) {
			return resolvedEntry{Path: dir, Source: core.SourceDefault}
		}
	}
	// 4. unresolved
	return resolvedEntry{Source: core.SourceUnresolved}
}
```

Add to `App` struct in `app.go`: `resolved map[core.GameID]resolvedEntry` + `resolveMu sync.Mutex` (or reuse `detectMu`). Add `resolveBackend(ctx, p)` that resolves all of `p.Games()` and stores into `a.resolved`, and `installedAndExists(gid)` helper: `e := a.resolved[gid]; return e.Source != SourceUnresolved && statDir(e.Path)`.

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/app/ -run TestResolve -count=1`.
- [ ] **Step 5: Commit** — `feat(game-paths): App per-game resolution chain`

---

## Task 8: Wire resolution into provider construction + injection

**Files:**
- Modify: `internal/app/app.go` (`constructProviders`, add resolve+inject)
- Test: `internal/app/app_test.go`

- [ ] **Step 1: Write failing test** — after `constructProviders`, a provider with a real install under its DefaultRoot (use a fake provider registered via the test helper with a temp DefaultScan) reports the resolved path through `DetectInstall`. (Use the existing `newAppForTest` + a fake provider exposing `DefaultScan`/`SetResolvedPaths`.)

```go
func TestConstructProviders_ResolvesAndInjects(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	fp := &fakeInjectProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}, def: map[core.GameID]string{gid: dir}}
	a := &App{settings: Settings{Version: 2, Games: map[string]GameSettings{}}, detect: map[core.BackendID]detectEntry{}, logger: slog.Default(), resolved: map[core.GameID]resolvedEntry{}}
	a.providers = []core.Provider{fp}
	a.resolveAll(context.Background()) // resolve + inject
	if fp.injected[gid] != dir {
		t.Fatalf("not injected: %+v", fp.injected)
	}
}
```

(Define `fakeInjectProvider` implementing `core.Provider` minimally + `DefaultScan` + `SetResolvedPaths` capturing into `injected`.)

- [ ] **Step 2: Run, expect FAIL** — `resolveAll undefined`.

- [ ] **Step 3: Implement** — `resolveAll(ctx)`: for each provider, `resolveBackend`, build `map[GameID]string` of resolved paths (include override-invalid as the literal path so downstream Launch refuses on its own existence check — actually only inject existing paths: `Installed` gating happens in ListGames; inject the resolved path regardless of existence so Launch/version operate on the user's chosen dir, and existence is enforced by `DetectInstall`'s stat). Inject via `p.(core.ResolvedPathSetter).SetResolvedPaths(m)`. Call `resolveAll` at the end of `constructProviders` (it already runs under the `settingsMu` write lock from `New`/`UpdateSettings`; resolution's RLock would deadlock — so `resolveOne` must read overrides WITHOUT re-locking when called from the construct path: pass overrides in, or use a non-locking internal `resolveOneLocked`). **Decision:** `resolveAll`/`resolveBackend` take the already-held-lock assumption and use a non-locking override read; the public `RefreshGame`/`SetGameOverride` acquire the lock then call the same inner logic. Document this in `resolve.go`.

- [ ] **Step 4: Run, expect PASS.** Run `go test ./internal/app/ -count=1`.
- [ ] **Step 5: Commit** — `feat(game-paths): resolve + inject per-game paths at construct time`

---

## Task 9: `GameRow` extension + `ListGames`/`gameInstallDir`/`ListBackends` read resolution

**Files:**
- Modify: `internal/app/app.go` (`GameRow`, `ListGames`, `ListBackends`), `internal/app/update_handler.go` (`gameInstallDir`)
- Test: `internal/app/app_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestListGames_CarriesResolvedSource(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithFakeInstalled(t, gid, dir) // helper: provider + resolveAll done
	rows, _ := a.ListGames()
	var row *GameRow
	for i := range rows { if rows[i].ID == string(gid) { row = &rows[i] } }
	if row == nil || !row.Installed || row.ResolvedPath != dir || row.PathSource != string(core.SourceDefault) {
		t.Fatalf("row=%+v", row)
	}
}
```

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement**
  - `GameRow`: add `ResolvedPath string \`json:"resolved_path,omitempty"\``, `PathSource string \`json:"path_source"\``, `OverridePath string \`json:"override_path,omitempty"\``.
  - `ListGames`: instead of (or in addition to) `cachedDetect`, read `a.resolved[g.ID]` for every game in `p.Games()`. Set `Installed = entry.Source != Unresolved && statDir(entry.Path)`, `InstallPath`/`ResolvedPath = entry.Path`, `PathSource = string(entry.Source)`, `OverridePath = a.settings.Games[id].Path`.
  - `gameInstallDir`: `return a.resolved[gid].Path` (delete the `DetectInstall` loop).
  - `ListBackends`: replace the `PrimaryPath`-based switch with: resolve the backend's games; `ok` if any installed-exists, else `empty`; `error` if DefaultScan/Locate errored. Remove the `path_unset`/`launcher_missing` states. Update the `BackendStatus.Status` doc comment.

- [ ] **Step 4: Run, expect PASS.** `go test ./internal/app/ -count=1`; `go build ./...`.
- [ ] **Step 5: Commit** — `feat(game-paths): GameRow carries resolved path + source`

---

## Task 10: Remove `path` from provider `SettingsSchema` + drop `PathProvider` reliance

**Files:**
- Modify: `internal/providers/{hoyoverse,kurogames,hypergryph}/<provider>.go` (`SettingsSchema`, `PrimaryPath`)
- Test: provider `*_test.go`

- [ ] **Step 1: Write failing test** (hoyoverse) — `SettingsSchema()` no longer contains a `path` key:

```go
func TestSettingsSchema_NoPathField(t *testing.T) {
	for _, f := range New(Settings{}, nil).SettingsSchema() {
		if f.Key == "path" {
			t.Errorf("path field should be removed from SettingsSchema")
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — remove the `{Key:"path",...}` entry from each provider's `SettingsSchema()` (hoyoverse keeps the `region` field). Remove `PrimaryPath()` methods (or have them return "" — but `ListBackends` no longer calls them after Task 9, so delete them and the `core.PathProvider` assertion usage). Confirm `go build ./...`.
- [ ] **Step 4: Run, expect PASS.** Whole-repo `go test ./... -count=1`.
- [ ] **Step 5: Commit** — `refactor(game-paths): drop per-backend path from SettingsSchema`

### Phase 1 gate
- [ ] Whole-repo `go test ./... -count=1` GREEN; `go build ./...` GREEN.
- [ ] Two-stage subagent review (spec + code-quality) of Phase 1 as a unit.

---

# PHASE 2 — Launcher locators (one at a time)

Each locator implements `core.InstallLocator` behind an **injectable source** so
it is fixture-testable with no live launcher. The App auto-uses it via the
`p.(core.InstallLocator)` assertion already coded in `resolveOne` (Task 7); no
App change is needed when a locator lands. Each task is **research → fixture →
impl**, mirroring the M3.A protocol-research pattern.

## Task 11: HoYoPlay `InstallLocator`

**Files:**
- Create: `internal/providers/hoyoverse/locator.go`, `locator_test.go`, `testdata/hoyoplay-config-sample.<ext>`
- Modify: `internal/providers/hoyoverse/hoyoverse.go` (construct locator with a real source; expose `LocateInstalls`)
- Research doc: `docs/superpowers/research/hoyoplay-install-locator.md`

- [ ] **Step 1: Research** — determine where HoYoPlay records each installed game's path. Sources, in order: (1) Collapse Launcher source (memory: *trust Collapse for HoYoverse*) — find the registry key / config file it reads; (2) inspect the live machine's HoYoPlay config under `%APPDATA%`/registry. Write findings (exact key/file + format + which value maps to which game biz) to the research doc. Capture a **sanitized** sample into `testdata/`.

- [ ] **Step 2: Write failing test** — `LocateInstalls` parses the sanitized fixture into `map[GameID]string` via an injected source (a config-root dir for a file source, or a `func(path string) (string,bool)` registry-reader). Example shape:

```go
func TestHoYoPlayLocator_ParsesFixture(t *testing.T) {
	loc := newHoyoplayLocator(hoyoplaySourceFromDir("testdata")) // injected source
	got, err := loc.LocateInstalls(context.Background())
	if err != nil { t.Fatal(err) }
	if got["hoyoverse/genshin"] == "" {
		t.Fatalf("genshin path not located: %+v", got)
	}
}
```

- [ ] **Step 3: Run, expect FAIL.**
- [ ] **Step 4: Implement** `locator.go` per the research doc; constructor takes the injectable source. In `New`, build the locator from the real source (registry reader or `%APPDATA%` root). Add `func (p *Provider) LocateInstalls(ctx) (map[core.GameID]string, error)` delegating to it. If registry-backed, add `golang.org/x/sys/windows/registry` (already an indirect dep family). Map launcher game identifiers → our `GameID`s using the existing `games` table.
- [ ] **Step 5: Run, expect PASS.** `go test ./internal/providers/hoyoverse/ -run Locator -count=1`.
- [ ] **Step 6: Commit** — `feat(game-paths): HoYoPlay install locator`

## Task 12: KRLauncher (kurogames) `InstallLocator`
Same shape as Task 11. Research source: `%APPDATA%\KRLauncher\...` (navigated during M2 background research). Files: `kurogames/locator.go` + test + `testdata/` + research doc `krlauncher-install-locator.md`. Lower confidence; if no reliable record is found, document that and ship **without** a locator (default-scan + override cover it) — record the decision in the research doc and skip the impl steps. Commit: `feat(game-paths): KRLauncher install locator` (or `docs: KRLauncher locator deferred`).

## Task 13: GRYPHLINK (hypergryph) `InstallLocator`
Same shape. Source: `%LOCALAPPDATA%\Games\<hash>\…`. Files: `hypergryph/locator.go` + test + `testdata/` + research doc. Lowest confidence; same defer-with-doc fallback rule as Task 12. Commit accordingly.

### Phase 2 gate
- [ ] Whole-repo `go test ./... -count=1` GREEN.
- [ ] Two-stage review of each locator that ships.

---

# PHASE 3 — Frontend (popover + RPCs + SettingsPanel + i18n)

## Task 14: App RPCs `SetGameOverride` / `ClearGameOverride` / `RefreshGame`

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go`
- After landing: regenerate bindings (`wails generate module` from repo root).

- [ ] **Step 1: Write failing test**

```go
func TestSetGameOverride_PersistsResolvesReturnsRow(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithFakeInstalled(t, gid, t.TempDir()) // starts default-resolved elsewhere
	row, err := a.SetGameOverride(string(gid), dir)
	if err != nil { t.Fatal(err) }
	if row.PathSource != string(core.SourceOverride) || row.ResolvedPath != dir || row.OverridePath != dir {
		t.Fatalf("row=%+v", row)
	}
	// persisted
	a.settingsMu.RLock(); got := a.settings.Games[string(gid)].Path; a.settingsMu.RUnlock()
	if got != dir { t.Errorf("not persisted: %q", got) }
}
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**

```go
func (a *App) SetGameOverride(gameID, path string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil { return GameRow{}, err }
	a.settingsMu.Lock()
	if a.settings.Games == nil { a.settings.Games = map[string]GameSettings{} }
	a.settings.Games[gameID] = GameSettings{Path: path}
	err = SaveSettings(a.settingsP, a.settings)
	a.settingsMu.Unlock()
	if err != nil { return GameRow{}, err }
	return a.refreshGameRow(gid, p), nil
}

func (a *App) ClearGameOverride(gameID string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil { return GameRow{}, err }
	a.settingsMu.Lock()
	delete(a.settings.Games, gameID)
	err = SaveSettings(a.settingsP, a.settings)
	a.settingsMu.Unlock()
	if err != nil { return GameRow{}, err }
	return a.refreshGameRow(gid, p), nil
}

func (a *App) RefreshGame(gameID string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil { return GameRow{}, err }
	return a.refreshGameRow(gid, p), nil
}
```

`refreshGameRow(gid, p)`: re-resolve the backend (invalidate its detect/default-scan cache), re-inject `SetResolvedPaths`, and build the single `GameRow` (same field population as `ListGames`). Invalidation is backend-granular (re-resolving ≤3 games is cheap).

- [ ] **Step 4: Run, expect PASS.** Regenerate bindings; confirm `frontend/wailsjs/go/app/App.d.ts` has the three functions.
- [ ] **Step 5: Commit** — `feat(game-paths): per-game override RPCs`

## Task 15: games store types + per-game actions

**Files:**
- Modify: `frontend/src/stores/games.ts`
- Test: `frontend/src/__tests__/games_store.test.ts` (new)

- [ ] **Step 1: Write failing test** — `setOverride(gameID, path)` calls `SetGameOverride`, replaces the row in `games` with the returned row (updating `installed`/`resolved_path`/`path_source`/`override_path`).

```ts
vi.mock('../../wailsjs/go/app/App', () => ({
  SetGameOverride: vi.fn(async (id: string, path: string) => ({ id, backend: 'fake', display_name: {en:'F'}, installed: true, resolved_path: path, path_source: 'override', override_path: path, has_predownload: false })),
  ClearGameOverride: vi.fn(), RefreshGame: vi.fn(),
}));
// mount-less store test: set games, call setOverride, assert row replaced.
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — extend `GameRow` type with `resolved_path?`, `path_source?`, `override_path?`. Add actions `setOverride`, `clearOverride`, `refreshGame` that call the RPCs and replace the matching row in `this.games`, **then** (if now installed) call `refreshVersionFor(id)` + `loadAssetsFor(id)`. Relax `refreshVersionFor`/`loadAssetsFor` `!installed` early-return so a just-located game is refreshed (guard on `resolved_path` instead, or re-order after the row is marked installed).
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** — `feat(game-paths): games store per-game override actions`

## Task 16: `GameConfigPopover.vue` + gear button in BottomBar

**Files:**
- Create: `frontend/src/components/GameConfigPopover.vue`
- Modify: `frontend/src/components/BottomBar.vue`, `frontend/src/styles/theme.css`
- Test: `frontend/src/__tests__/game_config_popover.test.ts` (new)

- [ ] **Step 1: Write failing tests** — (a) gear button has `data-testid="game-config-btn"` and is rendered even when `!installed`; (b) clicking it opens the popover (`data-testid="game-config-popover"`); (c) the source badge text matches `path_source`; (d) Browse→Save calls `games.setOverride`; (e) Save button is disabled when the selected game has `in_flight`.

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**
  - `GameConfigPopover.vue`: props `{ row }`; local `open` ref; popover anchored **above** the trigger; window `keydown` ESC + outside-click close (bind on open, remove on close + `onUnmounted`, mirroring SettingsPanel); closes when `games.selectedID` changes (`watch`). Fields: text input bound to a local `draft` seeded from `row.override_path`; Browse (`BrowseForDirectory`), Reset (`games.clearOverride`), Save (`games.setOverride`); source badge from `row.path_source` (`override`→`手動`, `override`+`!installed`→`手動・找不到`, `launcher`→`自動偵測`, `default`→`預設位置`, `unresolved`→Locate prompt). Save disabled when `updates.byGame[row.id]?.in_flight` (reuse BottomBar's pattern), with the `gamecfg.save_disabled_inflight` tooltip.
  - `BottomBar.vue`: add the gear button + `<GameConfigPopover :row="games.selected" />` immediately left of `.launch-area`, positioned so it doesn't shift when the launch button morphs into a progress bar. Always rendered while a game is selected.
  - CSS: `.game-config-btn`, `.game-config-popover` (absolute, bottom-anchored above the bar), badge styles.
- [ ] **Step 4: Run, expect PASS.** `vue-tsc` + `vite build` + vitest green.
- [ ] **Step 5: Commit** — `feat(game-paths): per-game config gear button + popover`

## Task 17: Remove per-backend Path rows from SettingsPanel

**Files:**
- Modify: `frontend/src/components/SettingsPanel.vue`, `frontend/src/__tests__/settings_panel.test.ts`

- [ ] **Step 1: Update test** — settings_panel.test asserts `findAll('input')[0]` is the HoYoverse path; after removal the first input is HoYoverse **TempDir**. Update the assertion to target temp_dir, and assert no install-path input remains.
- [ ] **Step 2: Run, expect FAIL (old assertion).**
- [ ] **Step 3: Implement** — remove the three `Backends.*.Path` label+row blocks (`SettingsPanel.vue:81-92, 105-109, 113-117` install-path rows), keep TempDir rows. The save still sends the full draft (Path no longer present in the schema).
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** — `refactor(game-paths): drop per-backend path rows from settings`

## Task 18: i18n keys (3 locales)

**Files:**
- Modify: `frontend/src/locales/{en,zh-TW,zh-CN}.json`
- Test: `frontend/src/__tests__/i18n_parity.test.ts` (auto-gates)

- [ ] **Step 1:** Add a `gamecfg` block to all three locales with keys: `path_label`, `browse`, `reset`, `save`, `save_disabled_inflight`, `locate`, `src_launcher`, `src_default`, `src_override`, `src_invalid`. (en values e.g. `"src_launcher": "Auto-detected"`, `"src_invalid": "Set, but not found"`; zh-TW `自動偵測`/`手動・找不到`; zh-CN parallel.)
- [ ] **Step 2: Run** `npx vitest run i18n_parity` — expect PASS (identical key sets).
- [ ] **Step 3: Commit** — `feat(game-paths): i18n for per-game config popover`

### Phase 3 gate
- [ ] `npm run build` (vue-tsc + vite) GREEN; `npx vitest run` GREEN; whole-repo `go test ./...` GREEN.
- [ ] Two-stage review of the frontend phase.

---

# PHASE 4 — Smoke (USER)

## Task 19: Manual smoke checklist (requires user; real installs)

Run `wails dev`. Verify:
1. Each installed game shows the gear left of Play; popover opens above it; ESC + click-away close.
2. Source badge correct: a default-location install shows `預設位置` (or `自動偵測` once that backend's locator ships); an override shows `手動`.
3. Browse → pick a folder → Save: the game's version/icon refresh **without** a full app refresh; badge flips to `手動`.
4. Reset → badge reverts to detection; game re-resolves live.
5. **Locate undetected:** temporarily rename a game folder so it's unresolved → gear still present → Browse to the real folder → it becomes installed.
6. **Custom location:** move a game to another drive; with its locator shipped it's auto-found (`自動偵測`); without, Browse to it once.
7. **Migration:** with a pre-existing v1 `settings.toml` that has a custom `[backends.*].path`, launch once → all previously-visible games still visible at the same paths; `settings.toml` rewrites to `version = 2` with `[games."..."]` overrides only for custom paths.
8. Save is disabled while that game has an update in-flight.
9. SettingsPanel no longer shows per-backend install-path inputs (TempDir remains).
10. `go test ./...`, `npm run build`, vitest all green.

## Task 20: Ship
- [ ] After smoke passes: `git checkout dev && git merge --no-ff game-paths/spec` (per convention; no `Co-Authored-By`). Tag if the user wants a version bump.
- [ ] Update memory `project_status.md` with the shipped state.

---

## Self-review notes (author)
- Spec coverage: §3 schema → T5; §4 resolution + §4.5 validity → T7; §4.6 lifecycle/lock → T8 (locked-read note); §5.1 DefaultScan → T1; §5.2 InstallLocator → T2 + T11-13; §5.3 injection → T3/T4/T9; §5.4 SettingsSchema/PrimaryPath → T10; §6.1 RPCs → T14; §6.2 ListBackends → T9; §7 UI → T15-18; §8 migration → T6; §9 testing → throughout; §11 phasing → phase boundaries.
- Open implementation decision deferred to execution: exact injected-map contents for an **invalid override** (inject literal path so Launch operates on user's chosen dir but `DetectInstall` stat-gate keeps it out of "installed") — see T8 Step 3; reviewer should confirm during Phase-1 review.

