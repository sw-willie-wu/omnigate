# Main-Screen P1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the detail (home) screen into the mockup's shell — a NavStrip (總覽 active / 抽卡分析 disabled), a HeroBase key-art area (no title overlay), and a restructured HomeActionBar — plus a small "last played" Go endpoint, with colors migrated to the mockup palette.

**Architecture:** Pure-frontend Vue/Pinia restructure of the existing `detail` view + one new Go state file (`playstate.json`) surfaced through the existing `GameRow`/`ListGames` path. No new frontend `fetch`; data flows via Wails bindings. Deferred (P2/P3): NewsPanel, countdown, account chip, 開拓力, gacha analytics.

**Tech Stack:** Go (Wails v2 backend), Vue 3 + TypeScript + Pinia, vue-i18n, vitest, `go test` (CGO_ENABLED=0 → **never** pass `-race`).

**Spec:** `docs/superpowers/specs/2026-06-03-omnigate-main-screen-p1-design.md`

**Branch:** `main-screen-p1` (already created off `dev`; baseline UI + spec already committed). Each task below is implemented, reviewed, then committed as one clean commit (per subagent-review-gates).

---

## File Structure

| File | Create/Modify | Responsibility |
|---|---|---|
| `frontend/src/styles/theme.css` | Modify | `:root` token values + new gold tokens + pill/card border-literal alignment |
| `internal/app/playstate.go` | Create | `playState` type: load / Record / Get + atomic write |
| `internal/app/playstate_test.go` | Create | playstate unit tests |
| `internal/app/app.go` | Modify | App.playState field, New/Startup wiring, Launch records, GameRow.LastPlayed (nil-guarded) |
| `internal/app/app_test.go` | Modify | Launch-records-last-played test |
| `frontend/src/utils/lastPlayed.ts` | Create | `formatRelativeTime(iso, now, t)` pure formatter |
| `frontend/src/__tests__/lastPlayed.test.ts` | Create | formatter unit tests |
| `frontend/src/stores/games.ts` | Modify | `last_played?` on GameRow type + `launchGame()` action (optimistic) |
| `frontend/src/__tests__/games_launch.test.ts` | Create | launchGame optimistic-update test |
| `frontend/src/stores/view.ts` | Modify | `homeTab` state + `setHomeTab` |
| `frontend/src/components/NavStrip.vue` | Create | tab strip (總覽 active / 抽卡分析 disabled) |
| `frontend/src/__tests__/NavStrip.test.ts` | Create | NavStrip render/inertness test |
| `frontend/src/components/DetailView.vue` | Modify | HeroBase scrim shell (no title) |
| `frontend/src/App.vue` | Modify | render NavStrip in detail mode only |
| `frontend/src/components/BottomBar.vue` | Modify | 2-row meta (pill+version / last-played) + neutral gear + gold CTA |
| `frontend/src/locales/{en,zh-TW,zh-CN}.json` | Modify | new `labels.*` + `nav.*` keys (all three, parity) |

---

## Task 1: Migrate color tokens to mockup palette

**Files:**
- Modify: `frontend/src/styles/theme.css` (`:root` near lines 5–8; pill/card borders near lines 318–320, 347–350)

CSS-only; verification is the existing test suite staying green + a visual check (no unit test for raw token values).

- [ ] **Step 1: Swap the three semantic token values in `:root`**

Find the `:root` semantic line (currently `--warn: #e89d5a; --info: #7aabd6; --crit: #d66666; --ok: #8bc472;`) and change three values:
- `--ok: #8bc472` → `--ok: #74d68a`
- `--info: #7aabd6` → `--info: #6fa8ff`
- `--warn: #e89d5a` → `--warn: #e8b865`

Leave `--crit` and `--accent` unchanged.

- [ ] **Step 2: Add the new gold/semantic tokens to `:root`**

Add these lines inside `:root` (after the existing gold/`--accent` declaration):

```css
    --gold-hi: #f4dd9b;
    --gold-deep: #b88f3c;
    --gold-glow: rgba(230,197,115,0.28);
    --gold-soft: rgba(230,197,115,0.12);
    --ready-soft: rgba(116,214,138,0.14);
    --hot: #ff6f6f;
    --tx-dim: #4a5060;
    --line-1: rgba(255,255,255,0.06);
    --line-2: rgba(255,255,255,0.10);
```

> These `--line-1`/`--line-2` tokens do **not** exist in the codebase today (it uses `--border`/`--border-strong`); the new NavStrip/gear styling references them, so they must be added here. `--text-2`/`--text-3` already exist and are reused as-is.

- [ ] **Step 3: Align the hardcoded pill/card border literals to the new hues**

These borders are written as old-hue rgba literals; after the value swap they would clash with the new text colors. **The green literal appears with TWO different alphas** (hero `.pill.ok` = `rgba(139,196,114,0.5)`, grid-card `.ready` = `rgba(139,196,114,0.4)`), so replacing a full rgba string would miss one. Instead replace just the **RGB triple** (alpha preserved) with `replace_all` for each hue:
- green `139,196,114` → `116,214,138` (hits both `.pill.ok` 0.5 and grid-card `.ready` 0.4)
- blue `122,171,214` → `111,168,255` (`.pill.info`, grid-card `.predownload`)
- orange `232,157,90` → `232,184,101` (`.pill.warn`, grid-card `.update`, and `.notif-btn.open` tint — all are `--warn`-family, so recoloring all three is correct)

Grep first to confirm occurrences/counts: `rg "139,196,114|122,171,214|232,157,90" frontend/src/styles/theme.css`. After editing, re-grep to confirm zero old triples remain.

- [ ] **Step 4: Run the frontend test suite to confirm nothing broke**

Run: `cd frontend; npm run test`
Expected: all existing tests PASS (token changes don't touch logic; this guards against accidental edits).

- [ ] **Step 5: Visual check in dev**

Run `wails dev`; confirm READY pill = new green, update = new blue, predl = new blue, and pill text/border hues match (no green-text/old-green-border mismatch). This is a manual confirmation step.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/styles/theme.css
git commit -m "style(theme): migrate semantic tokens to mockup palette + gold token set"
```

---

## Task 2: `playstate.json` Go backend + last-played wiring

**Files:**
- Create: `internal/app/playstate.go`
- Create: `internal/app/playstate_test.go`
- Modify: `internal/app/app.go` (App struct ~25–36, New ~51–57, Launch ~466–485, GameRow ~264–277, gameRowLocked ~305–317)
- Modify: `internal/app/app_test.go` (add one test)

- [ ] **Step 1: Write the failing playstate test**

Create `internal/app/playstate_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlayState_RecordGetRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "playstate.json")
	ps := loadPlayState(p) // file absent → empty, no error
	if got := ps.Get("fake/g"); !got.IsZero() {
		t.Fatalf("absent game should be zero time, got %v", got)
	}
	ps.Record("fake/g")
	if got := ps.Get("fake/g"); got.IsZero() {
		t.Fatalf("Record then Get should be non-zero")
	}
	// Reload from disk: value persisted.
	ps2 := loadPlayState(p)
	if got := ps2.Get("fake/g"); got.IsZero() {
		t.Fatalf("value did not persist across reload")
	}
}

func TestPlayState_CorruptFileTolerated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "playstate.json")
	if err := os.WriteFile(p, []byte("}{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := loadPlayState(p) // must not panic / must return usable empty store
	if got := ps.Get("any/game"); !got.IsZero() {
		t.Fatalf("corrupt file should yield empty store")
	}
	ps.Record("any/game") // must still be writable
	if ps.Get("any/game").IsZero() {
		t.Fatalf("Record after corrupt-load failed")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/ -run TestPlayState`
Expected: FAIL — `loadPlayState` undefined.

- [ ] **Step 3: Implement `playstate.go`**

Create `internal/app/playstate.go`:

```go
package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// playState persists per-game last-launch timestamps. It is a *persistent*
// user-state file (not a temp sidecar), stored alongside settings.toml.
//
// LOCKING: mu is always the INNERMOST lock. gameRowLocked calls Get while
// holding settingsMu (order settingsMu → mu, fine). NEVER acquire settingsMu
// while holding mu.
type playState struct {
	mu   sync.Mutex
	path string
	last map[string]time.Time
}

// loadPlayState reads path into a playState. A missing or corrupt file yields
// an empty (but writable) store — last-played is best-effort, never fatal.
func loadPlayState(path string) *playState {
	ps := &playState{path: path, last: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Default().Warn("playstate read failed; starting empty", "err", err, "path", path)
		}
		return ps
	}
	if err := json.Unmarshal(b, &ps.last); err != nil {
		slog.Default().Warn("playstate parse failed; starting empty", "err", err, "path", path)
		ps.last = map[string]time.Time{}
	}
	return ps
}

// Get returns the recorded last-played time, or the zero time if none.
func (ps *playState) Get(gameID string) time.Time {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.last[gameID]
}

// Record stamps gameID with the current time and persists atomically.
// Persist failure is logged, not returned: a failed write must not block launch.
func (ps *playState) Record(gameID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.last[gameID] = time.Now()
	if err := ps.saveLocked(); err != nil {
		slog.Default().Warn("playstate save failed", "err", err, "path", ps.path)
	}
}

// saveLocked writes atomically (temp → rename). Caller must hold ps.mu.
func (ps *playState) saveLocked() error {
	b, err := json.MarshalIndent(ps.last, "", "  ")
	if err != nil {
		return err
	}
	tmp := ps.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ps.path)
}

// playStatePathFor derives the playstate.json location from the settings path
// (same directory). With the current main.go (app.New("") → "settings.toml"),
// filepath.Dir is "." → process CWD, alongside settings.toml. Known current
// behavior; revisit on settings-path change / SQLite migration.
func playStatePathFor(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "playstate.json")
}
```

- [ ] **Step 4: Run playstate tests — verify pass**

Run: `go test ./internal/app/ -run TestPlayState`
Expected: PASS.

- [ ] **Step 5: Wire `playState` into the App struct + New**

In `app.go`, add to the `App` struct (after `updateRegistry`):

```go
	playState      *playState
```

In `New`, after the `a := &App{...}` literal is built (before/around `constructProviders`), initialize it:

```go
	a.playState = loadPlayState(playStatePathFor(settingsPath))
```

(Place this assignment right after the `a := &App{...}` block at ~line 57, before `a.constructProviders()`.)

- [ ] **Step 6: Record on successful Launch**

In `app.go` `Launch` (~line 484), replace the success return:

```go
	return p.Launch(a.ctx, gid, core.LaunchOptions{})
```

with:

```go
	pid, err := p.Launch(a.ctx, gid, core.LaunchOptions{})
	if err == nil && a.playState != nil {
		a.playState.Record(string(gid))
	}
	return pid, err
```

Leave the apply-block early return (~476) and provider-error return (~482) unchanged.

- [ ] **Step 7: Add `LastPlayed` to GameRow + fill it (nil-guarded)**

In `app.go` `GameRow` struct, add after `OverridePath`:

```go
	LastPlayed     string               `json:"last_played,omitempty"`
```

In `gameRowLocked` (~305), after building the `GameRow{...}` value, set the field with a nil-guard (the struct literal can't reference `a.playState` safely for tests that build `&App{}` without it):

```go
	row := GameRow{
		ID:           string(g.ID),
		Backend:      string(g.Backend),
		DisplayName:  g.DisplayName,
		PathSource:   string(e.Source),
		ResolvedPath: e.Path,
		InstallPath:  e.Path,
		Installed:    e.Source != core.SourceUnresolved && statDir(e.Path),
		OverridePath: a.settings.Games[string(g.ID)].Path,
	}
	if a.playState != nil {
		if ts := a.playState.Get(string(g.ID)); !ts.IsZero() {
			row.LastPlayed = ts.Format(time.RFC3339)
		}
	}
	return row
```

(Convert the existing `return GameRow{...}` into the `row := GameRow{...}` form above.)

- [ ] **Step 8: Write the failing Launch-records test**

In `internal/app/app_test.go`, add:

```go
func TestLaunch_RecordsLastPlayed(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir)
	a.playState = loadPlayState(filepath.Join(t.TempDir(), "playstate.json"))

	if _, err := a.Launch(string(gid)); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if a.playState.Get(string(gid)).IsZero() {
		t.Fatalf("Launch did not record last-played")
	}

	// Row surfaces it through ListGames (read path is nil-guarded; here non-nil).
	rows, _ := a.ListGames()
	var lp string
	for i := range rows {
		if rows[i].ID == string(gid) {
			lp = rows[i].LastPlayed
		}
	}
	if lp == "" {
		t.Fatalf("ListGames row missing last_played")
	}
}
```

(`fakeProvider.Launch` returns `(0, nil)`, so this exercises the success path. `buildAppWithResolved` builds App with `updateRegistry == nil`, so the apply-block branch is skipped.)

- [ ] **Step 9: Run the full app package tests**

Run: `go test ./internal/app/`
Expected: PASS — including existing tests through `gameRowLocked` (nil-guard keeps `buildAppWithResolved`/`newAppForTest` panic-free) and the new `TestLaunch_RecordsLastPlayed`.

- [ ] **Step 10: Regenerate Wails bindings is NOT required for the field**

The frontend declares the TS shape manually (Task 4); `GameRow` JSON just carries `last_played`. No `wails generate` step needed for this plan. Confirm `go build ./...` succeeds: `go build ./...`.

- [ ] **Step 11: Commit**

```bash
git add internal/app/playstate.go internal/app/playstate_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): record per-game last-played in playstate.json, surface on GameRow"
```

---

## Task 3: Last-played relative-time formatter + i18n keys

**Files:**
- Create: `frontend/src/utils/lastPlayed.ts`
- Create: `frontend/src/__tests__/lastPlayed.test.ts`
- Modify: `frontend/src/locales/en.json`, `zh-TW.json`, `zh-CN.json`

- [ ] **Step 1: Add i18n keys to all three locale files (parity-safe)**

`i18n_parity.test.ts` asserts identical key sets across the three files, so add the SAME keys to all three. Under the `"labels"` object add:

en.json:
```json
    "last_played": "Last played",
    "never_played": "Never played",
    "played_today": "Today {time}",
    "played_yesterday": "Yesterday {time}",
    "played_days_ago": "{n} days ago"
```
zh-TW.json:
```json
    "last_played": "上次遊玩",
    "never_played": "尚未遊玩",
    "played_today": "今天 {time}",
    "played_yesterday": "昨天 {time}",
    "played_days_ago": "{n} 天前"
```
zh-CN.json:
```json
    "last_played": "上次游玩",
    "never_played": "尚未游玩",
    "played_today": "今天 {time}",
    "played_yesterday": "昨天 {time}",
    "played_days_ago": "{n} 天前"
```

(The pre-existing orphan keys `last_run`/`minutes_ago`/`days_ago` are left untouched; not reused to keep casing clean.)

- [ ] **Step 2: Write the failing formatter test**

Create `frontend/src/__tests__/lastPlayed.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { formatRelativeTime } from '../utils/lastPlayed';

// Deterministic translator stub: echoes key + params so we can assert branch.
const t = (key: string, params?: Record<string, unknown>) =>
  params ? `${key}|${JSON.stringify(params)}` : key;

const now = new Date('2026-06-03T20:00:00');

describe('formatRelativeTime', () => {
  it('returns empty string for empty/undefined input', () => {
    expect(formatRelativeTime('', now, t)).toBe('');
  });

  it('today → played_today with HH:mm', () => {
    const iso = new Date('2026-06-03T09:05:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_today|{"time":"09:05"}');
  });

  it('yesterday → played_yesterday with HH:mm', () => {
    const iso = new Date('2026-06-02T23:14:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_yesterday|{"time":"23:14"}');
  });

  it('within a week → played_days_ago with n', () => {
    const iso = new Date('2026-05-31T10:00:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_days_ago|{"n":3}');
  });

  it('older than a week → locale date string', () => {
    const d = new Date('2026-05-01T10:00:00');
    const iso = d.toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe(d.toLocaleDateString());
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd frontend; npx vitest run src/__tests__/lastPlayed.test.ts`
Expected: FAIL — module `../utils/lastPlayed` not found.

- [ ] **Step 4: Implement the formatter**

Create `frontend/src/utils/lastPlayed.ts`:

```ts
type Translate = (key: string, params?: Record<string, unknown>) => string;

function hhmm(d: Date): string {
  const h = d.getHours().toString().padStart(2, '0');
  const m = d.getMinutes().toString().padStart(2, '0');
  return `${h}:${m}`;
}

// Returns the formatted relative-time string (the part after "Last played · "),
// or '' when there is no timestamp. `now` is injected for deterministic tests.
export function formatRelativeTime(iso: string, now: Date, t: Translate): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';

  // Calendar-day difference (local), not raw 24h buckets.
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const dayDiff = Math.round((startOf(now) - startOf(d)) / 86_400_000);

  if (dayDiff <= 0) return t('labels.played_today', { time: hhmm(d) });
  if (dayDiff === 1) return t('labels.played_yesterday', { time: hhmm(d) });
  if (dayDiff < 7) return t('labels.played_days_ago', { n: dayDiff });
  return d.toLocaleDateString();
}
```

- [ ] **Step 5: Run formatter + parity tests — verify pass**

Run: `cd frontend; npx vitest run src/__tests__/lastPlayed.test.ts src/__tests__/i18n_parity.test.ts`
Expected: PASS (formatter branches correct; parity holds because keys added to all three locales).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/utils/lastPlayed.ts frontend/src/__tests__/lastPlayed.test.ts frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json
git commit -m "feat(i18n): last-played relative-time formatter + locale keys"
```

---

## Task 4: games store — `last_played` type + optimistic `launchGame` action

**Files:**
- Modify: `frontend/src/stores/games.ts` (GameRow type ~4–19; add action ~112)
- Modify: `frontend/src/components/BottomBar.vue` (`onLaunch` ~117–119, import ~7)
- Create: `frontend/src/__tests__/games_launch.test.ts`

- [ ] **Step 1: Add `last_played` to the `GameRow` TS type**

In `games.ts`, add to the `GameRow` type (after `override_path?`):

```ts
  last_played?: string;
```

- [ ] **Step 2: Write the failing store test**

Create `frontend/src/__tests__/games_launch.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const LaunchMock = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  ListGames: vi.fn(), RefreshVersion: vi.fn(), GetIcon: vi.fn(),
  GetBackgrounds: vi.fn(), SetGameOverride: vi.fn(), ClearGameOverride: vi.fn(),
  RefreshGame: vi.fn(), Launch: (...a: unknown[]) => LaunchMock(...a),
}));

import { useGamesStore } from '../stores/games';

describe('games.launchGame', () => {
  beforeEach(() => { setActivePinia(createPinia()); LaunchMock.mockReset(); });

  it('optimistically sets last_played by direct field mutation (preserves assets)', async () => {
    LaunchMock.mockResolvedValue(0);
    const games = useGamesStore();
    games.games = [{
      id: 'fake/g', backend: 'fake', display_name: { en: 'G' },
      installed: true, has_predownload: false,
      icon_url: 'icon://x', background_url: 'bg://y',
    }];
    games.selectedID = 'fake/g';

    await games.launchGame('fake/g');

    expect(LaunchMock).toHaveBeenCalledWith('fake/g');
    const row = games.games[0];
    expect(row.last_played).toBeTruthy();              // optimistic stamp
    expect(row.icon_url).toBe('icon://x');             // assets NOT wiped
    expect(row.background_url).toBe('bg://y');
  });

  it('does not stamp last_played when Launch rejects', async () => {
    LaunchMock.mockRejectedValue(new Error('nope'));
    const games = useGamesStore();
    games.games = [{ id: 'fake/g', backend: 'fake', display_name: {}, installed: true, has_predownload: false }];
    await expect(games.launchGame('fake/g')).rejects.toThrow();
    expect(games.games[0].last_played).toBeUndefined();
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd frontend; npx vitest run src/__tests__/games_launch.test.ts`
Expected: FAIL — `launchGame` is not a function.

- [ ] **Step 4: Implement `launchGame` action**

In `games.ts`, add the import at the top alongside the others:

```ts
import { ListGames, RefreshVersion, GetIcon, GetBackgrounds, SetGameOverride, ClearGameOverride, RefreshGame, Launch } from '../../wailsjs/go/app/App';
```

Add the action inside `actions` (e.g. right before `select(id)`):

```ts
    // Launch a game and optimistically stamp last_played on the live row.
    // Direct field mutation only — NEVER via _replaceRow (that re-fetches and
    // would wipe icon_url/background_url/background_video). Backend persists the
    // authoritative value to playstate.json; it reaches us on next cold start.
    async launchGame(gameID: string) {
      await Launch(gameID);
      const row = this.games.find((g) => g.id === gameID);
      if (row) row.last_played = new Date().toISOString();
    },
```

- [ ] **Step 5: Point BottomBar's Play handler at the store action**

In `BottomBar.vue`, the `Launch` import (line 7) is now unused there — remove `Launch` from `import { Launch } from '../../wailsjs/go/app/App';` (delete that import line entirely if nothing else is imported from it). Change `onLaunch` (~117):

```ts
async function onLaunch() {
  if (games.selected) try { await games.launchGame(games.selected.id); } catch (e) { console.error(e); }
}
```

- [ ] **Step 6: Run store test + BottomBar test — verify pass**

Run: `cd frontend; npx vitest run src/__tests__/games_launch.test.ts src/__tests__/BottomBar.test.ts`
Expected: PASS (BottomBar smoke still green; launchGame optimistic + assets preserved).

- [ ] **Step 7: Commit**

```bash
git add frontend/src/stores/games.ts frontend/src/components/BottomBar.vue frontend/src/__tests__/games_launch.test.ts
git commit -m "feat(games): launchGame action with optimistic last_played stamp"
```

---

## Task 5: view store `homeTab` + NavStrip component

**Files:**
- Modify: `frontend/src/stores/view.ts`
- Create: `frontend/src/components/NavStrip.vue`
- Create: `frontend/src/__tests__/NavStrip.test.ts`
- Modify: `frontend/src/locales/{en,zh-TW,zh-CN}.json` (add `nav.*`)

- [ ] **Step 1: Add `nav.*` i18n keys to all three locales (parity)**

Add a top-level `"nav"` object to each locale file (same keys in all three):

en.json: `"nav": { "overview": "Overview", "gacha": "Gacha Analytics", "coming_soon": "Coming soon" }`
zh-TW.json: `"nav": { "overview": "總覽", "gacha": "抽卡分析", "coming_soon": "即將推出" }`
zh-CN.json: `"nav": { "overview": "总览", "gacha": "抽卡分析", "coming_soon": "即将推出" }`

- [ ] **Step 2: Extend the view store with `homeTab`**

In `view.ts`, add to `state`:

```ts
    homeTab: 'overview' as 'overview' | 'gacha',
```

Add to `actions`:

```ts
    setHomeTab(t: 'overview' | 'gacha') { this.homeTab = t; },
```

(`gacha` is never wired to a click in P1; `setHomeTab` exists for the future tab.)

- [ ] **Step 3: Write the failing NavStrip test**

Create `frontend/src/__tests__/NavStrip.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import NavStrip from '../components/NavStrip.vue';
import en from '../locales/en.json';
import { useViewStore } from '../stores/view';

function mountNav() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(NavStrip, { global: { plugins: [i18n] } });
}

describe('NavStrip', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('marks 總覽/Overview active and 抽卡分析/Gacha disabled', () => {
    const w = mountNav();
    const active = w.find('.nav-tab.active');
    expect(active.exists()).toBe(true);
    expect(active.text()).toContain('Overview');

    const disabled = w.find('.nav-tab.disabled');
    expect(disabled.exists()).toBe(true);
    expect(disabled.text()).toContain('Gacha');
  });

  it('clicking the disabled gacha tab does NOT change homeTab', async () => {
    const w = mountNav();
    const view = useViewStore();
    await w.find('.nav-tab.disabled').trigger('click');
    expect(view.homeTab).toBe('overview'); // still default; gacha is inert
  });
});
```

- [ ] **Step 4: Run to verify it fails**

Run: `cd frontend; npx vitest run src/__tests__/NavStrip.test.ts`
Expected: FAIL — NavStrip component not found.

- [ ] **Step 5: Implement NavStrip.vue**

Create `frontend/src/components/NavStrip.vue`:

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';

const { t } = useI18n();
const view = useViewStore();
</script>

<template>
  <div class="nav-strip">
    <button
      class="nav-tab"
      :class="{ active: view.homeTab === 'overview' }"
      @click="view.setHomeTab('overview')"
    >{{ t('nav.overview') }}</button>
    <!-- gacha is a P1 shell tab: disabled, no click handler, inert -->
    <button
      class="nav-tab disabled"
      disabled
      :title="t('nav.coming_soon')"
    >
      <span class="nav-tab-trend">▲</span>{{ t('nav.gacha') }}
    </button>
  </div>
</template>
```

Add styling to `frontend/src/styles/theme.css` (near the other component blocks):

```css
  .nav-strip { height: 52px; flex: none; display: flex; align-items: center; gap: 6px; padding: 0 20px; border-bottom: 1px solid var(--line-1); background: linear-gradient(180deg, rgba(17,19,25,0.55), transparent); }
  .nav-tab { padding: 7px 15px; border-radius: 9px; border: 1px solid transparent; background: transparent; color: var(--text-2); font-family: inherit; font-size: 13px; font-weight: 700; letter-spacing: 0.04em; cursor: pointer; display: inline-flex; align-items: center; gap: 6px; }
  .nav-tab.active { background: var(--gold-soft); border-color: rgba(230,197,115,0.4); color: var(--gold-hi); }
  .nav-tab.disabled { color: var(--tx-dim); cursor: not-allowed; }
  .nav-tab-trend { font-size: 10px; }
```

> `--text-2` already exists; `--line-1`/`--gold-soft`/`--gold-hi`/`--tx-dim` are all added in Task 1.

- [ ] **Step 6: Run NavStrip + view-store + parity tests — verify pass**

Run: `cd frontend; npx vitest run src/__tests__/NavStrip.test.ts src/__tests__/i18n_parity.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/stores/view.ts frontend/src/components/NavStrip.vue frontend/src/__tests__/NavStrip.test.ts frontend/src/styles/theme.css frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json
git commit -m "feat(nav): NavStrip shell (overview active / gacha disabled) + homeTab state"
```

---

## Task 6: Mount NavStrip + HeroBase scrim in the detail view

**Files:**
- Modify: `frontend/src/App.vue` (template ~68–73, import)
- Modify: `frontend/src/components/DetailView.vue`
- Modify: `frontend/src/styles/theme.css` (DetailView scrim)
- Create: `frontend/src/__tests__/App_navstrip.test.ts`

- [ ] **Step 1: Write the failing App-integration test**

Create `frontend/src/__tests__/App_navstrip.test.ts`. App.vue's `onMounted` fires a long RPC chain (GetSettings, games.load, refreshVersions, loadAssets, updates.loadAll/bind, per-game checkForUpdate), making a full `mount(App)` brittle to mock. This test therefore guards the two things the integration relies on — the default `homeTab` and that NavStrip reflects it — without mounting the whole shell. (The `<main>` `v-else-if` wiring itself is verified visually in Step 6.)

```ts
import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../locales/en.json';
import NavStrip from '../components/NavStrip.vue';
import { useViewStore } from '../stores/view';

describe('home-tab contract', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('view store defaults homeTab to overview', () => {
    expect(useViewStore().homeTab).toBe('overview');
  });

  it('NavStrip overview tab is active by default', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const w = mount(NavStrip, { global: { plugins: [i18n] } });
    expect(w.find('.nav-tab.active').text()).toContain('Overview');
  });
});
```

- [ ] **Step 2: Run the contract test**

Run: `cd frontend; npx vitest run src/__tests__/App_navstrip.test.ts`
Expected: PASS (NavStrip + view store exist from Task 5). This is a contract/regression guard, not a red-first TDD step — the behavioral TDD for NavStrip lives in Task 5.

- [ ] **Step 3: Render NavStrip in detail mode in App.vue**

In `App.vue`, add the import next to the others:

```ts
import NavStrip from './components/NavStrip.vue';
```

Change the `<main>` block so NavStrip precedes DetailView only in detail mode:

```vue
      <main class="main">
        <SettingsPanel v-if="view.viewMode === 'settings'" />
        <template v-else-if="view.viewMode === 'detail'">
          <NavStrip />
          <DetailView />
        </template>
        <GridView v-else />
        <BottomBar v-if="view.viewMode === 'detail'" />
      </main>
```

(NavStrip renders ONLY in detail mode; grid/settings are unaffected. BottomBar's own `v-if="games.selected"` still governs whether it shows.)

- [ ] **Step 4: Fill DetailView with the HeroBase scrim (no title)**

Replace `DetailView.vue` contents:

```vue
<script setup lang="ts"></script>

<template>
  <!-- HeroBase: key-art comes from the global BgLayer; this view only lays the
       readability scrims. No title overlay (per P1 spec). The countdown pill
       and NewsPanel are deferred to P2. The BottomBar/HomeActionBar floats at
       the bottom (rendered by App.vue). -->
  <div class="view view-detail">
    <div class="hero-scrim-bottom"></div>
    <div class="hero-scrim-left"></div>
  </div>
</template>
```

Add to `theme.css`:

```css
  .view-detail { position: relative; flex: 1; min-height: 0; }
  .hero-scrim-bottom { position: absolute; inset: 0; pointer-events: none; background: linear-gradient(0deg, rgba(8,9,12,0.92) 0%, rgba(8,9,12,0.25) 40%, transparent 62%); }
  .hero-scrim-left { position: absolute; inset: 0; pointer-events: none; background: linear-gradient(90deg, rgba(8,9,12,0.5), transparent 28%); }
```

- [ ] **Step 5: Run the frontend suite — verify pass**

Run: `cd frontend; npm run test`
Expected: all PASS.

- [ ] **Step 6: Visual check**

`wails dev`: detail view shows NavStrip on top (總覽 active, 抽卡分析 greyed/unclickable), key art with bottom/left scrims, BottomBar at the bottom; switching to grid/settings hides NavStrip. NavStrip does not overlap the BottomBar.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/App.vue frontend/src/components/DetailView.vue frontend/src/styles/theme.css frontend/src/__tests__/App_navstrip.test.ts
git commit -m "feat(home): mount NavStrip + HeroBase scrim in detail view"
```

---

## Task 7: Restructure BottomBar into the HomeActionBar layout

**Files:**
- Modify: `frontend/src/components/BottomBar.vue` (template ~154–208, script add last-played computed)
- Modify: `frontend/src/styles/theme.css` (`.bottom-bar` layout, gear, CTA)
- Modify: `frontend/src/__tests__/BottomBar.test.ts` (add last-played assertions)

**Constraint:** Do NOT change any state computed (`pillClass`, `availableUpdate`, `availablePredl`, `predlReady`, `inFlight`, `errorLabel`, `stageLabel`, etc.) or the `v-if` branch structure of the right cluster. Only add the meta second line + restyle.

- [ ] **Step 1: Add a `lastPlayedLabel` computed to BottomBar**

In `BottomBar.vue` `<script setup>`, add imports + computed:

```ts
import { formatRelativeTime } from '../utils/lastPlayed';
```

```ts
const lastPlayedLabel = computed<string>(() => {
  const iso = games.selected?.last_played;
  if (!iso) return t('labels.never_played');
  const rel = formatRelativeTime(iso, new Date(), (k, p) => t(k, p as any));
  return `${t('labels.last_played')} · ${rel}`;
});
```

- [ ] **Step 2: Write the failing last-played render assertions**

Append to `frontend/src/__tests__/BottomBar.test.ts`:

```ts
test('meta line shows never_played when last_played is unset', async () => {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const wrapper = mount(BottomBar, { global: { plugins: [i18n] } });
  const games = useGamesStore();
  games.games = [{ id: 'fake/g', backend: 'fake', display_name: { en: 'G' }, installed: true, has_predownload: false, current_version: '1.0' }];
  games.selectedID = 'fake/g';
  await wrapper.vm.$nextTick();
  expect(wrapper.find('.last-played').text()).toContain('Never played');
});

test('meta line shows last-played when set', async () => {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const wrapper = mount(BottomBar, { global: { plugins: [i18n] } });
  const games = useGamesStore();
  games.games = [{ id: 'fake/g', backend: 'fake', display_name: { en: 'G' }, installed: true, has_predownload: false, current_version: '1.0', last_played: new Date().toISOString() }];
  games.selectedID = 'fake/g';
  await wrapper.vm.$nextTick();
  const txt = wrapper.find('.last-played').text();
  expect(txt).toContain('Last played');
  expect(txt).toContain('Today');
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd frontend; npx vitest run src/__tests__/BottomBar.test.ts`
Expected: FAIL — `.last-played` element not found.

- [ ] **Step 4: Restructure the BottomBar template — left meta becomes two rows**

Replace the left block (current lines ~156–160) so the pill row and a new last-played row sit in a flex column:

```vue
    <div v-if="errorLabel" class="update-error">{{ errorLabel }}</div>
    <div class="hero-meta">
      <div class="hero-stats-line">
        <span class="pill" :class="pillClass">{{ pillLabel }}</span>
        <span v-if="!availableUpdate" class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
      </div>
      <div class="last-played">
        <span class="lp-clock">◷</span>{{ lastPlayedLabel }}
      </div>
    </div>
```

Leave the entire `.bottombar-right` cluster (predl-area / GameConfigPopover / launch-area and all `v-if` branches) byte-identical.

- [ ] **Step 5: Restyle `.bottom-bar`, the gear (neutral glass 56), and the CTA (gold)**

In `theme.css`, first change the existing `.bottom-bar` rule's `align-items: center` (≈line 282) to `align-items: flex-end` (so the 2-row meta column bottom-aligns with the right cluster) — do this as a concrete Edit on the existing declaration, then add:

```css
  .hero-meta { display: flex; flex-direction: column; gap: 8px; }
  .last-played { display: inline-flex; align-items: center; gap: 6px; color: var(--text-3); font-size: 12px; letter-spacing: 0.02em; }
  .last-played .lp-clock { font-size: 12px; opacity: 0.8; }

  /* Neutral-glass gear (was 40×40 .icon-btn): override size + hover so it does
     NOT compete with the gold CTA. Hover only hints gold. */
  .bottom-bar .game-config-btn { width: 56px; height: 56px; border-radius: 14px; background: rgba(20,22,28,0.72); border: 1px solid var(--line-2); color: var(--text-2); backdrop-filter: blur(8px); }
  .bottom-bar .game-config-btn:hover { color: var(--gold-hi); border-color: rgba(230,197,115,0.4); }

  /* Gold-glow CTA (Play / Update / Apply) */
  .bottom-bar .launch-btn { height: 56px; border-radius: 14px; padding: 0 38px; color: var(--gold-hi); font-weight: 900; letter-spacing: 0.08em; background: linear-gradient(180deg, rgba(230,197,115,0.18), rgba(230,197,115,0.05)); border: 1.5px solid rgba(230,197,115,0.6); box-shadow: inset 0 1px 0 rgba(255,255,255,0.14), 0 10px 30px -8px var(--gold-glow); backdrop-filter: blur(6px); }
```

> Check existing `.bottom-bar`, `.launch-btn`, `.game-config-btn` rules first (`rg "\.launch-btn|\.game-config-btn|\.bottom-bar \{" frontend/src/styles/theme.css`) and adjust/merge rather than duplicate conflicting declarations. The new `.bottom-bar .launch-btn` / `.bottom-bar .game-config-btn` selectors (two classes) beat the existing single-class `.launch-btn` (≈line 327) / `.game-config-btn` on specificity, so source order doesn't matter for those. `--text-3` and `--line-2` (added in Task 1) are used directly — no nonexistent tokens.

- [ ] **Step 6: Run BottomBar tests + full suite — verify pass**

Run: `cd frontend; npm run test`
Expected: PASS — existing BottomBar smoke/apply/error tests still green (state logic untouched) + new last-played assertions pass.

- [ ] **Step 7: Visual check**

`wails dev`: select a game → bottom-left shows the status pill + version on row 1 and "Last played · …" (or "Never played") on row 2; bottom-right shows a neutral-glass 56px gear (gold on hover) + a gold-glow Play/Update CTA. Launch a game → meta updates to "Today HH:mm" immediately.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/BottomBar.vue frontend/src/styles/theme.css frontend/src/__tests__/BottomBar.test.ts
git commit -m "feat(home): restructure BottomBar into HomeActionBar (2-row meta, neutral gear, gold CTA)"
```

---

## Final verification (after all tasks)

- [ ] `go test ./...` (no `-race`) — all green, incl. playstate + Launch.
- [ ] `cd frontend; npm run test` — all green.
- [ ] `go build ./...` and `wails dev` smoke:
  - NavStrip: 總覽 active, 抽卡分析 greyed + unclickable; absent in grid/settings.
  - Key art with scrims, no title overlay.
  - HomeActionBar: 2-row meta, neutral gear (gold on hover), gold CTA; last-played updates on launch and persists across restart (check `playstate.json` in CWD).
  - Status colors: ready=green, update=blue, predl=blue; pill text/border hues consistent.
- [ ] Finish-branch: per `subagent-review-gates` exit + project convention, `merge --no-ff` `main-screen-p1` into `dev` (no `Co-Authored-By` trailer).

## Out of scope (P2/P3 — do NOT implement here)

- P2: NewsPanel (最新情報), activity countdown pill — will add Go endpoints for public announcement/activity data.
- P3: account chip (nickname/UID/multi-account), 開拓力, gacha analytics page.
