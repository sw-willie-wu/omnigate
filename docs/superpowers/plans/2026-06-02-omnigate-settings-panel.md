# Settings Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the inert Topbar gear button to a right slide-over settings panel that edits each backend's install path + temp_dir override (the only settings that drive behavior), saving via the existing `UpdateSettings` RPC.

**Architecture:** New `SettingsPanel.vue` drawer (mirrors the existing `notif-panel` Teleport overlay) backed by `GetSettings`/`UpdateSettings` (already Wails-bound) + a new `BrowseForDirectory` bridge for the native folder picker. Because the panel is the FIRST runtime caller of `UpdateSettings`, a non-reentrant `settingsMu` RWMutex is added to `App` to make `a.settings`/`a.providers` race-safe, and the panel disables Save while any update is in-flight.

**Tech Stack:** Go (Wails v2.12.0 runtime), Vue 3 `<script setup>` + Pinia, vue-i18n, Vitest.

**Spec:** `docs/superpowers/specs/2026-06-01-omnigate-settings-panel-design.md` (opus-gate APPROVED). §2.5 (concurrency) and §3 (BrowseForDirectory) are normative — read them.

**Toolchain note:** subagent shells may lack Go on PATH — `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. CGO is off → never pass `-race`. Frontend commands run from `frontend/`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/app/app.go` (modify) | Add `settingsMu sync.RWMutex`; lock discipline per spec §2.5. |
| `internal/app/update_handler.go` (modify) | Snapshot `a.providers` in `UpdateStatusAll`/`knownBackendIDs`/`scanForRecovery`. |
| `internal/app/dialog.go` (create) | `BrowseForDirectory` + pure `dialogDefaultDir` helper. |
| `internal/app/dialog_test.go` (create) | `dialogDefaultDir` + nil-ctx guard tests. |
| `internal/app/settings_concurrency_test.go` (create) | No-deadlock-under-concurrency test. |
| `frontend/src/stores/view.ts` (modify) | `settingsOpen` state + `openSettings`/`closeSettings`. |
| `frontend/src/composables/useRefreshAll.ts` (create) | Shared post-change refresh (extracted from Topbar `onRefresh`). |
| `frontend/src/components/Topbar.vue` (modify) | Gear `@click`, mount `<SettingsPanel>`, use `useRefreshAll`. |
| `frontend/src/components/SettingsPanel.vue` (create) | The drawer UI + load/edit/save/cancel logic. |
| `frontend/src/styles/theme.css` (modify) | Drawer styles (reuse notif-panel patterns). |
| `frontend/src/locales/{en,zh-TW,zh-CN}.json` (modify) | `settings.*` keys. |
| `frontend/src/__tests__/settings_panel.test.ts` (create) | Panel behavior tests. |
| `frontend/src/__tests__/view_store.test.ts` (create) | view store action tests. |

---

## Task 1: App concurrency — `settingsMu` (Go)

Makes `a.settings`/`a.providers` race-safe per spec §2.5. **Non-reentrant discipline: exactly one lock acquisition per call path.** Write-lock only in `UpdateSettings`; `constructProviders`/`cachedDetect` lock-free; single-shot RLock in leaf readers; snapshot-then-iterate in provider-list methods.

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/update_handler.go`
- Test: `internal/app/settings_concurrency_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/app/settings_concurrency_test.go`:

```go
package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newConcurrencyTestApp builds a minimal but fully-wired App: real providers,
// a temp settings path (so UpdateSettings' disk write works), and an update
// registry (so UpdateStatusAll works).
func newConcurrencyTestApp(t *testing.T) *App {
	t.Helper()
	a := &App{
		settingsP: filepath.Join(t.TempDir(), "settings.toml"),
		detect:    map[core.BackendID]detectEntry{},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	a.ctx = context.Background()
	if err := a.constructProviders(); err != nil {
		t.Fatalf("constructProviders: %v", err)
	}
	a.updateRegistry = NewUpdateStateRegistry(func(string, ...any) {}, realClock{})
	t.Cleanup(func() { a.updateRegistry.emitter.Stop() })
	return a
}

func TestSettingsMu_NoDeadlockUnderConcurrency(t *testing.T) {
	a := newConcurrencyTestApp(t)

	var wg sync.WaitGroup
	// Readers hammer every guarded read path.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_ = a.GetSettings()
				_, _ = a.ListGames()
				_ = a.ListBackends()
				_ = a.UpdateStatusAll()
				_ = a.knownBackendIDs()
			}
		}()
	}
	// Writer reconstructs providers/settings repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			s := a.GetSettings()
			_ = a.UpdateSettings(s)
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: concurrent settings access did not complete within 15s")
	}
}
```

Add the missing imports to the test file: it also needs `"io"` and `"omnigate/internal/core"`. Final import block:

```go
import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"omnigate/internal/core"
)
```

- [ ] **Step 2: Run test to verify it fails (compile failure / wrong behavior)**

Run: `go test ./internal/app/ -run TestSettingsMu_NoDeadlockUnderConcurrency -count=1`
Expected: FAIL — compiles but exercises the unguarded code (the point of later steps is to make it pass cleanly; before the lock it relies on undefined behavior, and `UpdateStatusAll`/`ListGames` iterate `a.providers` while `UpdateSettings` reassigns it). If it happens to pass by luck, that's fine — the lock changes below are still required by the spec; proceed.

- [ ] **Step 3: Add the mutex field**

In `internal/app/app.go`, add `settingsMu` to the `App` struct (after `detectMu`):

```go
type App struct {
	ctx            context.Context
	settings       Settings
	settingsP      string
	providers      []core.Provider
	detect         map[core.BackendID]detectEntry
	detectMu       sync.Mutex
	settingsMu     sync.RWMutex // guards a.settings + a.providers (spec §2.5)
	logger         *slog.Logger
	updateRegistry *UpdateStateRegistry
}
```

- [ ] **Step 4: Document constructProviders as lock-free / caller-holds-write-lock**

In `internal/app/app.go`, replace the `constructProviders` doc comment (keep the body unchanged):

```go
// constructProviders builds the provider list from current settings.
//
// LOCKING (spec §2.5): this is LOCK-FREE and MUST NOT acquire settingsMu. It is
// called only from New (pre-concurrency) and from UpdateSettings while UpdateSettings
// already holds the settingsMu WRITE lock. Its inline a.settings reads are covered by
// that write lock. The SetTempRootFn closure it installs is only INVOKED later (from
// update operations), where tempDirFor takes a fresh RLock — never during construction.
func (a *App) constructProviders() error {
```

- [ ] **Step 5: Write-lock `UpdateSettings`**

In `internal/app/app.go`, replace `UpdateSettings`:

```go
func (a *App) UpdateSettings(s Settings) error {
	// Disk write first; it touches neither a.settings nor a.providers.
	if err := SaveSettings(a.settingsP, s); err != nil {
		return err
	}
	a.settingsMu.Lock()
	a.settings = s
	err := a.constructProviders() // lock-free; runs under this write lock
	a.settingsMu.Unlock()
	a.invalidateDetect()
	return err
}
```

- [ ] **Step 6: RLock the leaf readers (`GetSettings`, `provider`, `byID`, `tempDirFor`)**

In `internal/app/app.go`, replace each:

```go
func (a *App) GetSettings() Settings {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings
}
```

```go
func (a *App) provider(gid core.GameID) (core.Provider, error) {
	backendID, _, err := core.ParseGameID(gid)
	if err != nil {
		return nil, err
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}
```

```go
func (a *App) byID(backendID core.BackendID) core.Provider {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p
		}
	}
	return nil
}
```

`tempDirFor` — wrap the whole body (it only reads `a.settings`):

```go
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	switch backend {
	case kurogames.BackendID:
		if td := a.settings.Backends.Kurogames.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "omnigate")
	case hoyoverse.BackendID:
		if td := a.settings.Backends.Hoyoverse.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "omnigate", "hoyoverse")
	}
	return filepath.Join(osTempDir(), "omnigate", string(backend))
}
```

- [ ] **Step 7: Snapshot-then-iterate in `ListGames` and `ListBackends`**

In `internal/app/app.go`, change the loop headers to iterate a snapshot. `ListGames`:

```go
func (a *App) ListGames() ([]GameRow, error) {
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	out := []GameRow{}
	for _, p := range provs {
		installed, err := a.cachedDetect(a.ctx, p)
		// ... (rest of loop body UNCHANGED) ...
```

`ListBackends`:

```go
func (a *App) ListBackends() []BackendStatus {
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	out := make([]BackendStatus, 0, len(provs))
	for _, p := range provs {
		// ... (rest of loop body UNCHANGED) ...
```

(Only the first two lines of each loop change: snapshot under RLock, release, iterate `provs` instead of `a.providers`. `cachedDetect` stays lock-free — it uses `detectMu` only.)

- [ ] **Step 8: Snapshot-then-iterate in `update_handler.go`**

In `internal/app/update_handler.go`, `UpdateStatusAll` — after `snaps := a.updateRegistry.SnapshotAll()`:

```go
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	for _, p := range provs {
		// ... (rest UNCHANGED, was `for _, p := range a.providers`) ...
```

`knownBackendIDs`:

```go
func (a *App) knownBackendIDs() map[string]struct{} {
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	out := make(map[string]struct{}, len(provs))
	for _, p := range provs {
		out[string(p.ID())] = struct{}{}
	}
	return out
}
```

`scanForRecovery`:

```go
func (a *App) scanForRecovery() {
	skipNames := a.knownBackendIDs()
	a.settingsMu.RLock()
	provs := append([]core.Provider(nil), a.providers...)
	a.settingsMu.RUnlock()
	for _, p := range provs {
		root := a.tempDirFor(p.ID(), "")
		a.scanForRecoveryRoot(p.ID(), root, skipNames)
	}
}
```

(`knownBackendIDs()` runs to completion and releases its RLock BEFORE `scanForRecovery` takes its own — sequential, never nested. `tempDirFor` in the loop takes a fresh RLock after the snapshot is released.)

- [ ] **Step 9: Run the new test + the whole app package**

Run: `go test ./internal/app/ -count=1`
Expected: PASS (incl. `TestSettingsMu_NoDeadlockUnderConcurrency` completing well under 15s; existing app tests still green).

- [ ] **Step 10: Whole-repo build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 11: Commit**

```bash
git add internal/app/app.go internal/app/update_handler.go internal/app/settings_concurrency_test.go
git commit -m "fix(app): guard settings/providers with non-reentrant settingsMu"
```

---

## Task 2: `BrowseForDirectory` + `dialogDefaultDir` (Go)

Native folder picker bridge per spec §3. `dialogDefaultDir` only returns `current` when it's an existing directory (`os.Lstat`), else `""` — otherwise Wails errors without opening the dialog.

**Files:**
- Create: `internal/app/dialog.go`
- Test: `internal/app/dialog_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/app/dialog_test.go`:

```go
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDialogDefaultDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, in, want string
	}{
		{"existing dir", dir, dir},
		{"missing path", filepath.Join(dir, "nope"), ""},
		{"a file not a dir", file, ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dialogDefaultDir(tc.in); got != tc.want {
				t.Errorf("dialogDefaultDir(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBrowseForDirectory_NilCtx(t *testing.T) {
	a := &App{} // ctx is nil
	got, err := a.BrowseForDirectory("anything")
	if err != nil {
		t.Errorf("nil-ctx should return nil error, got %v", err)
	}
	if got != "" {
		t.Errorf("nil-ctx should return empty path, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestDialogDefaultDir|TestBrowseForDirectory_NilCtx' -count=1`
Expected: FAIL — `dialogDefaultDir`/`BrowseForDirectory` undefined.

- [ ] **Step 3: Implement `internal/app/dialog.go`**

```go
package app

import (
	"os"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dialogDefaultDir returns current iff it is an existing directory, else "".
// Wails' OpenDirectoryDialog returns an error WITHOUT opening the dialog when
// DefaultDirectory is a non-existent path (pkg/runtime/dialog.go), and the panel's
// primary use case is fixing a wrong/missing path. Uses os.Lstat (not Stat) to
// bit-match Wails' internal fs.DirExists, which uses Lstat.
func dialogDefaultDir(current string) string {
	if current == "" {
		return ""
	}
	if fi, err := os.Lstat(current); err == nil && fi.IsDir() {
		return current
	}
	return ""
}

// BrowseForDirectory opens the native folder picker (seeded at current only when
// it exists) and returns the chosen absolute path, or "" if cancelled. The
// a.ctx == nil guard prevents Wails' getFrontend(nil) from calling log.Fatalf
// (process exit) in headless/test contexts; it is not a panic.
func (a *App) BrowseForDirectory(current string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "選擇資料夾",
		DefaultDirectory: dialogDefaultDir(current),
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run 'TestDialogDefaultDir|TestBrowseForDirectory_NilCtx' -count=1`
Expected: PASS.

- [ ] **Step 5: Regenerate Wails bindings (so the frontend sees `BrowseForDirectory`)**

Run from repo root: `wails generate module`
Expected: updates `frontend/wailsjs/go/app/App.{js,d.ts}` to include `BrowseForDirectory`. (`frontend/wailsjs/` is gitignored — this is a local build artifact, NOT committed. If `wails generate module` is unavailable in this shell, `wails build` regenerates bindings as a side effect; the binding only needs to exist before the frontend `npm run build` in Task 8.)

- [ ] **Step 6: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/app/dialog.go internal/app/dialog_test.go
git commit -m "feat(app): BrowseForDirectory native folder picker bridge"
```

---

## Task 3: `view` store — `settingsOpen` + actions (frontend)

**Files:**
- Modify: `frontend/src/stores/view.ts`
- Test: `frontend/src/__tests__/view_store.test.ts` (create)

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/view_store.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { useViewStore } from '../stores/view';

describe('view store settings drawer', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('starts closed and toggles open/closed', () => {
    const v = useViewStore();
    expect(v.settingsOpen).toBe(false);
    v.openSettings();
    expect(v.settingsOpen).toBe(true);
    v.closeSettings();
    expect(v.settingsOpen).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `frontend/`): `npx vitest run src/__tests__/view_store.test.ts`
Expected: FAIL — `settingsOpen`/`openSettings`/`closeSettings` undefined.

- [ ] **Step 3: Add state + actions to the view store**

In `frontend/src/stores/view.ts`, add `settingsOpen` to state and the two actions, matching the store's existing option-store idiom. Add to the `state` object:

```ts
    settingsOpen: false,
```

Add to `actions`:

```ts
    openSettings() { this.settingsOpen = true; },
    closeSettings() { this.settingsOpen = false; },
```

(Place alongside the existing actions like `setView`/`toggleSidebar`; keep the existing state/actions intact.)

- [ ] **Step 4: Run test to verify it passes**

Run (from `frontend/`): `npx vitest run src/__tests__/view_store.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/stores/view.ts frontend/src/__tests__/view_store.test.ts
git commit -m "feat(frontend): view store settingsOpen drawer flag"
```

---

## Task 4: Shared refresh composable + Topbar rewire (frontend)

Extract Topbar `onRefresh`'s body into a reusable `useRefreshAll` so the panel's Save and the Topbar button share one implementation (spec §4).

**Files:**
- Create: `frontend/src/composables/useRefreshAll.ts`
- Modify: `frontend/src/components/Topbar.vue`

- [ ] **Step 1: Create the composable**

Create `frontend/src/composables/useRefreshAll.ts`:

```ts
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { Refresh } from '../../wailsjs/go/app/App';

// refreshAll re-detects installs, reloads game data + assets, and probes each
// installed game for updates. Shared by the Topbar refresh button and the
// settings panel's post-Save refresh so they cannot drift.
export async function refreshAll(): Promise<void> {
  await Refresh();
  const games = useGamesStore();
  const updates = useUpdatesStore();
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await Promise.allSettled(
    games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
  );
}
```

- [ ] **Step 2: Rewire Topbar `onRefresh` to use it**

In `frontend/src/components/Topbar.vue`, add the import:

```ts
import { refreshAll } from '../composables/useRefreshAll';
```

Replace the `onRefresh` body so it delegates (keep the try/catch + the function name/usage in the template):

```ts
const onRefresh = async () => {
  try {
    await refreshAll();
  } catch (e) {
    console.error('refresh failed', e);
  }
};
```

(Remove the now-unused direct imports of `Refresh` and the inline chain from Topbar IF they are no longer referenced elsewhere in the file; if `Refresh`/`games`/`updates` are still used by other code in Topbar, keep them.)

- [ ] **Step 3: Build the frontend to verify it compiles**

Run (from `frontend/`): `npm run build`
Expected: `vue-tsc` + `vite build` succeed (no unused-import or type errors).

- [ ] **Step 4: Run existing tests (no regressions)**

Run (from `frontend/`): `npx vitest run`
Expected: all existing suites pass.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/composables/useRefreshAll.ts frontend/src/components/Topbar.vue
git commit -m "refactor(frontend): extract refreshAll shared by Topbar and settings"
```

---

## Task 5: i18n `settings.*` keys (3 locales)

The existing `__tests__/i18n_parity.test.ts` enforces identical flattened key sets across en / zh-TW / zh-CN, so every key must be added to all three.

**Files:**
- Modify: `frontend/src/locales/en.json`, `frontend/src/locales/zh-TW.json`, `frontend/src/locales/zh-CN.json`

- [ ] **Step 1: Add the `settings` block to en.json**

In `frontend/src/locales/en.json`, add a top-level `"settings"` object (sibling of `"update"`, `"labels"`, etc.):

```json
  "settings": {
    "title": "Settings",
    "save": "Save",
    "cancel": "Cancel",
    "browse": "Browse…",
    "clear": "Clear",
    "path_label": "Install path",
    "tempdir_label": "Temp folder override",
    "tempdir_hint": "Leave empty to use the default temp folder",
    "save_error": "Save failed: {detail}",
    "save_disabled_inflight": "Cannot save while an update is in progress",
    "backend": {
      "hoyoverse": "HoYoverse",
      "kurogames": "Kuro Games",
      "hypergryph": "Hypergryph"
    }
  },
```

- [ ] **Step 2: Add the same block to zh-TW.json**

```json
  "settings": {
    "title": "設定",
    "save": "儲存",
    "cancel": "取消",
    "browse": "瀏覽…",
    "clear": "清除",
    "path_label": "安裝路徑",
    "tempdir_label": "暫存資料夾覆寫",
    "tempdir_hint": "留空 = 使用預設暫存資料夾",
    "save_error": "儲存失敗:{detail}",
    "save_disabled_inflight": "更新進行中無法儲存",
    "backend": {
      "hoyoverse": "米哈遊",
      "kurogames": "庫洛遊戲",
      "hypergryph": "鷹角網路"
    }
  },
```

- [ ] **Step 3: Add the same block to zh-CN.json**

```json
  "settings": {
    "title": "设置",
    "save": "保存",
    "cancel": "取消",
    "browse": "浏览…",
    "clear": "清除",
    "path_label": "安装路径",
    "tempdir_label": "临时文件夹覆盖",
    "tempdir_hint": "留空 = 使用默认临时文件夹",
    "save_error": "保存失败:{detail}",
    "save_disabled_inflight": "更新进行中无法保存",
    "backend": {
      "hoyoverse": "米哈游",
      "kurogames": "库洛游戏",
      "hypergryph": "鹰角网络"
    }
  },
```

- [ ] **Step 4: Run the i18n parity test**

Run (from `frontend/`): `npx vitest run src/__tests__/i18n_parity.test.ts`
Expected: PASS (identical key sets across all three locales; new keys non-empty).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json
git commit -m "feat(i18n): settings panel keys (en/zh-TW/zh-CN)"
```

---

## Task 6: `SettingsPanel.vue` + drawer styles

The drawer itself: loads a draft from `GetSettings`, renders per-backend path/temp_dir fields with Browse, Saves via `UpdateSettings` (disabled while an update is in-flight), Cancels by discarding.

**Files:**
- Create: `frontend/src/components/SettingsPanel.vue`
- Modify: `frontend/src/styles/theme.css`
- Test: `frontend/src/__tests__/settings_panel.test.ts` (create)

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/settings_panel.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';
import { mount, flushPromises } from '@vue/test-utils';
import { i18n } from '../i18n';
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';

const sampleSettings = () => ({
  Version: 1,
  App: { Language: 'zh-TW', BannerAnimationPref: 'video-when-available', ShowTechnicalInfo: false },
  Backends: {
    Hoyoverse: { Path: 'C:/HoYoPlay', Region: 'global', TempDir: '' },
    Kurogames: { Path: 'C:/Wuthering', TempDir: '' },
    Hypergryph: { Path: 'C:/Endfield' },
  },
});

const GetSettings = vi.fn();
const UpdateSettings = vi.fn();
const BrowseForDirectory = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  GetSettings: (...a: any[]) => GetSettings(...a),
  UpdateSettings: (...a: any[]) => UpdateSettings(...a),
  BrowseForDirectory: (...a: any[]) => BrowseForDirectory(...a),
  Refresh: vi.fn(() => Promise.resolve()),
}));
// refreshAll pulls stores; stub the games store loaders it calls.
vi.mock('../composables/useRefreshAll', () => ({ refreshAll: vi.fn(() => Promise.resolve()) }));

import SettingsPanel from '../components/SettingsPanel.vue';

function mountOpen() {
  const wrapper = mount(SettingsPanel, { global: { plugins: [i18n] } });
  const view = useViewStore();
  view.openSettings();
  return wrapper;
}

describe('SettingsPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    GetSettings.mockReset().mockResolvedValue(sampleSettings());
    UpdateSettings.mockReset().mockResolvedValue(undefined);
    BrowseForDirectory.mockReset().mockResolvedValue('');
  });

  it('loads settings into the draft on open', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(GetSettings).toHaveBeenCalled();
    expect(w.html()).toContain('C:/HoYoPlay');
  });

  it('Save calls UpdateSettings with the (edited) draft, preserving unshown fields', async () => {
    const w = mountOpen();
    await flushPromises();
    // Edit the hoyoverse path input (first text input in the panel).
    const input = w.findAll('input[type="text"]')[0];
    await input.setValue('D:/NewHoYo');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalledTimes(1);
    const arg = UpdateSettings.mock.calls[0][0];
    expect(arg.Backends.Hoyoverse.Path).toBe('D:/NewHoYo');
    expect(arg.App.Language).toBe('zh-TW'); // unshown field preserved
    expect(arg.Backends.Hoyoverse.Region).toBe('global'); // preserved
  });

  it('Cancel closes without calling UpdateSettings', async () => {
    const w = mountOpen();
    await flushPromises();
    await w.find('[data-test="settings-cancel"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).not.toHaveBeenCalled();
    expect(useViewStore().settingsOpen).toBe(false);
  });

  it('Save is disabled while an update is in-flight', async () => {
    const w = mountOpen();
    await flushPromises();
    const updates = useUpdatesStore();
    updates.byGame['hoyoverse/genshin'] = { in_flight: { phase: 'download' } } as any;
    await flushPromises();
    expect(w.find('[data-test="settings-save"]').attributes('disabled')).toBeDefined();
  });

  it('Save error keeps the panel open and shows the error', async () => {
    UpdateSettings.mockRejectedValueOnce(new Error('disk full'));
    const w = mountOpen();
    await flushPromises();
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(useViewStore().settingsOpen).toBe(true);
    expect(w.html()).toContain('disk full');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `frontend/`): `npx vitest run src/__tests__/settings_panel.test.ts`
Expected: FAIL — `SettingsPanel.vue` does not exist.

- [ ] **Step 3: Implement `SettingsPanel.vue`**

Create `frontend/src/components/SettingsPanel.vue`:

```vue
<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';
import { GetSettings, UpdateSettings, BrowseForDirectory } from '../../wailsjs/go/app/App';
import { refreshAll } from '../composables/useRefreshAll';

const { t } = useI18n();
const view = useViewStore();
const updates = useUpdatesStore();

const draft = ref<any>(null);
const saveError = ref('');
const saving = ref(false);

const anyInFlight = computed(() =>
  Object.values(updates.byGame).some((s: any) => s?.in_flight != null),
);
const saveDisabled = computed(() => saving.value || anyInFlight.value || !draft.value);

// Load a fresh draft each time the drawer opens; discard on close.
watch(
  () => view.settingsOpen,
  async (open) => {
    if (open) {
      saveError.value = '';
      draft.value = JSON.parse(JSON.stringify(await GetSettings()));
    } else {
      draft.value = null;
    }
  },
  { immediate: true },
);

async function browse(setter: (p: string) => void, current: string) {
  try {
    const p = await BrowseForDirectory(current || '');
    if (p) setter(p);
  } catch (e) {
    console.error('BrowseForDirectory failed', e);
  }
}

async function onSave() {
  if (saveDisabled.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    await UpdateSettings(draft.value);
    await refreshAll();
    view.closeSettings();
  } catch (e: any) {
    saveError.value = t('settings.save_error', { detail: e?.message ?? String(e) });
  } finally {
    saving.value = false;
  }
}

function onCancel() {
  view.closeSettings();
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') onCancel();
}
</script>

<template>
  <Teleport to="body">
    <div v-if="view.settingsOpen && draft" class="settings-backdrop" @click="onCancel"></div>
    <div
      v-if="view.settingsOpen && draft"
      class="settings-panel"
      tabindex="-1"
      @keydown="onKeydown"
    >
      <div class="settings-header">
        <span>{{ t('settings.title') }}</span>
        <button class="settings-close" @click="onCancel" aria-label="Close">×</button>
      </div>

      <div class="settings-body">
        <!-- HoYoverse -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.hoyoverse') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hoyoverse.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hoyoverse.Path = p), draft.Backends.Hoyoverse.Path)">{{ t('settings.browse') }}</button>
          </div>
          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hoyoverse.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hoyoverse.TempDir = p), draft.Backends.Hoyoverse.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.Backends.Hoyoverse.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <!-- Kuro -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.kurogames') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Kurogames.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Kurogames.Path = p), draft.Backends.Kurogames.Path)">{{ t('settings.browse') }}</button>
          </div>
          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Kurogames.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Kurogames.TempDir = p), draft.Backends.Kurogames.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.Backends.Kurogames.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <!-- Hypergryph -->
        <div class="settings-section">
          <div class="settings-section-title">{{ t('settings.backend.hypergryph') }}</div>
          <label class="settings-label">{{ t('settings.path_label') }}</label>
          <div class="settings-row">
            <input type="text" v-model="draft.Backends.Hypergryph.Path" />
            <button class="settings-browse" @click="browse((p) => (draft.Backends.Hypergryph.Path = p), draft.Backends.Hypergryph.Path)">{{ t('settings.browse') }}</button>
          </div>
        </div>

        <div v-if="saveError" class="settings-error">{{ saveError }}</div>
      </div>

      <div class="settings-footer">
        <button class="settings-btn-cancel" data-test="settings-cancel" @click="onCancel">{{ t('settings.cancel') }}</button>
        <button
          class="settings-btn-save"
          data-test="settings-save"
          :disabled="saveDisabled"
          :title="anyInFlight ? t('settings.save_disabled_inflight') : undefined"
          @click="onSave"
        >{{ t('settings.save') }}</button>
      </div>
    </div>
  </Teleport>
</template>
```

- [ ] **Step 4: Add drawer styles to theme.css**

In `frontend/src/styles/theme.css`, append (reuses the notif-panel visual language — dark, blurred, right-anchored):

```css
  .settings-backdrop { position: fixed; inset: 0; z-index: 1000; background: rgba(0,0,0,0.45); }
  .settings-panel {
    position: fixed; top: 0; right: 0; bottom: 0; z-index: 1001;
    width: 420px; max-width: 90vw;
    background: rgba(15, 15, 25, 0.97); backdrop-filter: blur(12px);
    border-left: 1px solid rgba(255,255,255,0.08);
    display: flex; flex-direction: column;
    box-shadow: -8px 0 30px rgba(0,0,0,0.5);
  }
  .settings-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 16px 18px; font-size: 15px; font-weight: 600;
    border-bottom: 1px solid rgba(255,255,255,0.08);
  }
  .settings-close { background: transparent; border: 0; color: var(--text-2); font-size: 22px; cursor: pointer; }
  .settings-close:hover { color: white; }
  .settings-body { flex: 1; overflow-y: auto; padding: 16px 18px; }
  .settings-section { margin-bottom: 22px; }
  .settings-section-title { font-size: 13px; font-weight: 700; color: var(--accent); margin-bottom: 8px; }
  .settings-label { display: block; font-size: 11px; color: var(--text-2); margin: 8px 0 4px; }
  .settings-row { display: flex; gap: 6px; align-items: center; }
  .settings-row input {
    flex: 1; min-width: 0; padding: 6px 8px; border-radius: 6px;
    border: 1px solid rgba(255,255,255,0.12); background: rgba(0,0,0,0.25); color: var(--text);
    font-size: 12px;
  }
  .settings-browse, .settings-clear {
    padding: 6px 10px; border-radius: 6px; border: 0; cursor: pointer; font-size: 11px;
    background: rgba(255,255,255,0.08); color: var(--text); white-space: nowrap;
  }
  .settings-browse:hover, .settings-clear:hover { background: rgba(255,255,255,0.14); }
  .settings-error { margin-top: 12px; color: var(--warn); font-size: 12px; }
  .settings-footer {
    display: flex; justify-content: flex-end; gap: 8px;
    padding: 14px 18px; border-top: 1px solid rgba(255,255,255,0.08);
  }
  .settings-btn-cancel { padding: 7px 16px; border-radius: 6px; border: 0; cursor: pointer; background: rgba(255,255,255,0.08); color: var(--text); font-size: 12px; }
  .settings-btn-save { padding: 7px 16px; border-radius: 6px; border: 0; cursor: pointer; background: var(--accent); color: var(--bg); font-weight: 600; font-size: 12px; }
  .settings-btn-save:disabled { opacity: 0.4; cursor: default; }
```

- [ ] **Step 5: Run the panel test to verify it passes**

Run (from `frontend/`): `npx vitest run src/__tests__/settings_panel.test.ts`
Expected: PASS (all 5 cases).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/SettingsPanel.vue frontend/src/styles/theme.css frontend/src/__tests__/settings_panel.test.ts
git commit -m "feat(frontend): SettingsPanel right-drawer with path/temp_dir editing"
```

---

## Task 7: Topbar gear wiring + mount the panel

**Files:**
- Modify: `frontend/src/components/Topbar.vue`

- [ ] **Step 1: Wire the gear button + import/mount the panel**

In `frontend/src/components/Topbar.vue`:

1. Add the import in `<script setup>`:

```ts
import SettingsPanel from './SettingsPanel.vue';
```

2. Ensure the `view` store is available (it already is: `const view = useViewStore();`). Change the inert gear button (`<button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>`) to:

```html
      <button class="icon-btn" @click="view.openSettings()" title="Settings"><span class="material-symbols-outlined">settings</span></button>
```

3. Mount the panel once, inside the Topbar template root (e.g. right after the `</Teleport>` of the notif panel, still inside the top-level `<div class="topbar">`):

```html
    <SettingsPanel />
```

- [ ] **Step 2: Build the frontend**

Run (from `frontend/`): `npm run build`
Expected: `vue-tsc` + `vite build` succeed. (Requires the `BrowseForDirectory` binding from Task 2 Step 5 to exist in `frontend/wailsjs`.)

- [ ] **Step 3: Run all frontend tests**

Run (from `frontend/`): `npx vitest run`
Expected: all suites pass.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/Topbar.vue
git commit -m "feat(frontend): open SettingsPanel from the Topbar gear"
```

---

## Task 8: Whole-app verification + manual smoke (USER)

**Files:** none (verification only).

- [ ] **Step 1: Whole-repo Go build/vet/test**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all packages GREEN.

- [ ] **Step 2: Frontend build + tests**

Run (from `frontend/`): `npm run build && npx vitest run`
Expected: build OK; all suites pass.

- [ ] **Step 3: Production build**

Run (repo root): `wails build`
Expected: `build/bin/omnigate.exe` produced (this also regenerates Wails bindings including `BrowseForDirectory`).

- [ ] **Step 4: USER manual smoke** (subagents cannot click)

1. Launch `omnigate.exe`. Click the gear → drawer slides in from the right, showing current paths.
2. Click "Browse…" on a backend path → native folder dialog opens (even when the current path is wrong/missing). Pick a folder → field updates.
3. Edit a temp_dir to a folder on a roomier drive → Save → no error, drawer closes, sidebar re-detects.
4. Reopen settings → values persisted. Restart app → still persisted (settings.toml written).
5. Cancel/ESC/backdrop-click → edits discarded.
6. Start an update on any game; open settings while it runs → Save is disabled with the in-flight tooltip.

- [ ] **Step 5: Finish the branch (USER-gated)**

After smoke passes: merge `settings-ui/spec` per the project's `feedback_commits` convention (feature branch → dev `--no-ff`, or as the user directs). Do NOT merge before the user confirms smoke.

---

## Notes for the executor

- **Commit-after-review:** under `subagent-review-gates`, each task's work stays uncommitted until BOTH the spec-compliance and code-quality reviews return `APPROVE`; the commit step then runs as written.
- **No `-race`** (CGO off on this host) — the concurrency test (Task 1) catches deadlocks, not data races; the race-freedom is established by the spec §2.5 review + code review.
- **Wails bindings** are gitignored; `BrowseForDirectory` appears after `wails generate module` / `wails build`. Frontend builds in Tasks 4/6/7 assume it exists locally — run Task 2 Step 5 first.
- Frontend field access is **PascalCase** (`draft.Backends.Hoyoverse.TempDir`) — the wire shape, since `Settings` has no `json:` tags.
