# Settings & Background UI 重整 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把語言設定從 Topbar 移到設定頁下拉、暫存位置改全域單一、主畫面背景改多張輪播（圓點手動切換）並支援每遊戲自訂背景圖。

**Architecture:** 後端先做 settings schema v2→v3（新增 `App.TempDir` / `GameSettings.BackgroundPath`、移除 per-backend `temp_dir`、遷移）與兩個新 binding（`BrowseForImage` / `GetCustomBackground`），並把三個 provider 的 temp root 統一走 `App.tempDirFor`。前端再改 SettingsPanel（語言下拉 + 全域暫存 + per-game 自訂圖）、games store（`backgrounds[]`+`bgIndex`）、`BgLayer`（依 index 切換來源）、`BottomBar`（絕對置中圓點）。

**Tech Stack:** Go (Wails v2, go-toml/v2), Vue 3 `<script setup>` + Pinia + vue-i18n, vitest。

**參考 spec：** `docs/superpowers/specs/2026-06-05-omnigate-settings-bg-ui-design.md`

**通用注意事項：**
- 測試指令（本機 CGO_ENABLED=0，**不要加 `-race`**）：Go 用 `go test ./internal/app/...`；前端用 `npm run test`（vitest）/ `npm run build`（型別檢查）。在 `frontend/` 目錄跑 npm。
- Commit message **不要** `Co-Authored-By` trailer。每個 task 一個 clean commit（由 review gate 通過後才 commit）。
- 分支已在 `settings-bg-ui/spec`。

---

### Task 1: Settings schema v2→v3 + 遷移（Go，保留 per-backend 暫不接線）

**Files:**
- Modify: `internal/app/settings.go`
- Test: `internal/app/settings_test.go`

本 task 只做 schema/遷移的**加法**：新增 `App.TempDir`、`GameSettings.BackgroundPath`、`hoyoverseRawTOML.TempDir`，版本 2→3，加 v2→v3 遷移（把舊 per-backend `temp_dir` 收斂進 `App.TempDir`）。**保留** `Backends.*.TempDir` 與現行 `tempDirFor`（Task 2 才移除/接線），確保本 task 編譯且全綠。

- [ ] **Step 1: 先把既有 version 斷言改成 3（會先紅）**

`internal/app/settings_test.go` 內所有 version 斷言改 3，**共 7 處**（漏任何一處 Task 1 都無法全綠）：
- 數值斷言 5 處 `Version != 2` / `want 2` → `!= 3` / `want 3`：`:20-21`、`:108-109`、`:130-131`、`:256-257`、`:274`（含下一行 errorf 文案）。例如：
```go
// :20
if s.Version != 3 {
    t.Errorf("default Version = %d, want 3", s.Version)
}
```
- **字串斷言 2 處**（在 `SaveSettings` 之後檢查 TOML 內容）：`:49` 與 `:92` 的 `strings.Contains(..., "version = 2")` → `"version = 3"`（這兩處 Step 5 把 `SaveSettings` 改寫 `version = 3` 後才會失敗，必須一起改；對應測試名含 `Version1`，可順手把函式名/註解的 1 改為 3）。

同時把 `internal/app/settings.go:210` `SaveSettings` 上方 doc 註解「Always includes version = 2」文字改為 3。

- [ ] **Step 2: 加新遷移 + 新欄位 round-trip 測試（先紅）**

在 `internal/app/settings_test.go` 末尾新增：

```go
func TestSettings_V2toV3_MigratesFirstNonEmptyTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// v2 file with hoyoverse temp_dir set (the BLOCKER case: must NOT be lost).
	v2 := "version = 2\n\n[backends.hoyoverse]\npath = \"C:\\\\HP\"\nregion = \"global\"\ntemp_dir = \"D:\\\\hoyo-temp\"\n\n[backends.kurogames]\npath = \"C:\\\\WW\"\ntemp_dir = \"D:\\\\kuro-temp\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 3 {
		t.Errorf("Version = %d, want 3", s.Version)
	}
	if s.App.TempDir != `D:\hoyo-temp` {
		t.Errorf("App.TempDir = %q, want D:\\hoyo-temp (first non-empty, hoyo first)", s.App.TempDir)
	}
}

func TestSettings_V2toV3_AllEmptyStaysEmpty(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	v2 := "version = 2\n\n[backends.hoyoverse]\npath = \"C:\\\\HP\"\nregion = \"global\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.App.TempDir != "" {
		t.Errorf("App.TempDir = %q, want empty", s.App.TempDir)
	}
}

func TestSettings_AppTempDir_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.TempDir = `D:\global-temp`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.App.TempDir != `D:\global-temp` {
		t.Errorf("round-trip App.TempDir = %q", loaded.App.TempDir)
	}
}

func TestSettings_GameBackgroundPath_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.Games = map[string]GameSettings{"hoyoverse/genshin": {BackgroundPath: `D:\pic.png`}}
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Games["hoyoverse/genshin"].BackgroundPath != `D:\pic.png` {
		t.Errorf("round-trip BackgroundPath = %q", loaded.Games["hoyoverse/genshin"].BackgroundPath)
	}
}

func TestSettings_SaveDoesNotEmitBackendTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.TempDir = `D:\g`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	out := string(b)
	// App temp_dir must be present (under [app]):
	if !strings.Contains(out, "temp_dir = \"D:\\\\g\"") {
		t.Errorf("App.TempDir not written:\n%s", out)
	}
	// No per-backend temp_dir: every temp_dir occurrence must be the [app] one.
	if strings.Count(out, "temp_dir") != 1 {
		t.Errorf("expected exactly one temp_dir ([app]); got:\n%s", out)
	}
}
```

- [ ] **Step 3: 跑測試確認紅**

Run: `go test ./internal/app/ -run 'V2toV3|AppTempDir|BackgroundPath|SaveDoesNotEmit' -v`
Expected: 編譯失敗（`App.TempDir` / `GameSettings.BackgroundPath` 未定義）。

- [ ] **Step 4: 改 schema 結構**

`internal/app/settings.go`：

`AppSettings` 加 `TempDir`：
```go
type AppSettings struct {
	Language            string `toml:"language"`
	TempDir             string `toml:"temp_dir,omitempty"`
	BannerAnimationPref string `toml:"banner_animation_pref"`
	ShowTechnicalInfo   bool   `toml:"show_technical_info"`
}
```

`GameSettings` 加 `BackgroundPath`：
```go
type GameSettings struct {
	Path           string `toml:"path,omitempty"`
	BackgroundPath string `toml:"background_path,omitempty"`
}
```

`hoyoverseRawTOML` 加 `TempDir`（**BLOCKER 修正**：否則遷移讀不到 hoyoverse 的舊值）：
```go
type hoyoverseRawTOML struct {
	Path         string `toml:"path"`
	HoYoplayPath string `toml:"hoyoplay_path"`
	Region       string `toml:"region"`
	TempDir      string `toml:"temp_dir"`
}
```

> 注意：本 task **保留** `HoyoverseSettings.TempDir` / `KurogamesSettings.TempDir` / `HypergryphSettings.TempDir`（Task 2 才移除），故現有 `tempDirFor` 與 `constructProviders` 不動、仍編譯。

- [ ] **Step 5: 版本 bump 2→3（三處）**

`defaultSettings()`：`Version: 2` → `Version: 3`。
`LoadSettings` 收尾：`out.Version = 2` → `out.Version = 3`（其上方註解一併更新文字為 v3）。
`SaveSettings`：`s.Version = 2` → `s.Version = 3`。

- [ ] **Step 6: 加 v2→v3 遷移**

在 `LoadSettings` 內、現有 `if raw.Version < 2 { migrateV1ToV2(&out) }` 之後、`out.Version = 3` 之前插入：

```go
	// v2→v3: collapse per-backend temp_dir into a single global App.TempDir.
	// Read from raw (out no longer carries per-backend temp_dir post-removal in
	// a later change; reading raw is the stable source). First non-empty wins,
	// in fixed order hoyoverse → kurogames → hypergryph (cannot merge differing
	// dirs into one). Only when App.TempDir not already set.
	if raw.Version < 3 && out.App.TempDir == "" {
		for _, td := range []string{
			raw.Backends.Hoyoverse.TempDir,
			raw.Backends.Kurogames.TempDir,
			raw.Backends.Hypergryph.TempDir,
		} {
			if td != "" {
				out.App.TempDir = td
				slog.Default().Warn("settings: migrated per-backend temp_dir → app.temp_dir", "value", td)
				break
			}
		}
	}
```

- [ ] **Step 7: 跑測試確認綠**

Run: `go test ./internal/app/ -v`
Expected: 全綠（含新測試與既有測試；既有 per-backend TempDir 測試此時仍存在且仍通過，因欄位尚在）。

- [ ] **Step 8: Commit**

```bash
git add internal/app/settings.go internal/app/settings_test.go
git commit -m "feat(settings): schema v3 — App.TempDir + GameSettings.BackgroundPath + v2→v3 temp_dir migration"
```

---

### Task 2: 全域暫存接線 + 移除 per-backend temp_dir（Go）

**Files:**
- Modify: `internal/app/settings.go`（移除 active per-backend `TempDir`）
- Modify: `internal/app/app.go`（`tempDirFor` root 讀 `App.TempDir`；`constructProviders` 三 provider 統一 `SetTempRootFn`）
- Modify: `internal/providers/kurogames/kurogames.go`（抽 `tempRoot(gid)` + `tempRootFn` + `SetTempRootFn`）
- Modify: `internal/providers/hypergryph/hypergryph.go`（同上）
- Modify: `internal/providers/hoyoverse/hoyoverse.go`（移除 `Settings.TempDir` 欄位）
- Test: `internal/app/settings_test.go`（移除 6 個 per-backend temp_dir 測試）、`internal/app/app_test.go`（`tempDirFor` 讀 App.TempDir）

- [ ] **Step 1: 加 `tempDirFor` 讀 App.TempDir 的測試（先紅）**

在 `internal/app/app_test.go` 末尾新增。`tempDirFor` 是 `*App` method；用既有測試建構 App 的方式（參考 app_test.go 內現有 `tempDirFor` 測試 `app_test.go:~317` 的 helper）。若現有測試用 `&App{settings: Settings{...}}` 直接建構，照抄該樣式：

```go
func TestTempDirFor_UsesGlobalAppTempDir(t *testing.T) {
	a := &App{settings: Settings{App: AppSettings{TempDir: `D:\custom`}}}
	if got := a.tempDirFor(hoyoverse.BackendID, "hoyoverse/genshin"); got != filepath.Join(`D:\custom`, "hoyoverse") {
		t.Errorf("hoyoverse tempDir = %q", got)
	}
	if got := a.tempDirFor(kurogames.BackendID, "kurogames/wuwa"); got != `D:\custom` {
		t.Errorf("kuro tempDir = %q (want flat root)", got)
	}
	if got := a.tempDirFor(hypergryph.BackendID, "hypergryph/endfield"); got != filepath.Join(`D:\custom`, "hypergryph") {
		t.Errorf("gryph tempDir = %q", got)
	}
}

func TestTempDirFor_EmptyFallsBackToOSTemp(t *testing.T) {
	a := &App{settings: Settings{App: AppSettings{TempDir: ""}}}
	if got := a.tempDirFor(kurogames.BackendID, "kurogames/wuwa"); got != filepath.Join(osTempDir(), "omnigate") {
		t.Errorf("default kuro tempDir = %q", got)
	}
}
```

> 確認 `app_test.go` 的 import 已含 `path/filepath` 與三個 provider 套件；缺則補。若 `&App{...}` 直接建構不可行（欄位未匯出但同 package 可存取，OK），照 app_test.go 既有 `tempDirFor` 測試樣式。

- [ ] **Step 2: 改 `tempDirFor` root 讀 App.TempDir**

`internal/app/app.go` `tempDirFor`：把三個 `osTempDir()` 改為共用 root。重寫如下：

```go
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	root := a.settings.App.TempDir
	if root == "" {
		root = filepath.Join(osTempDir(), "omnigate")
	}
	switch backend {
	case kurogames.BackendID:
		return root // flat (legacy bit-exact)
	case hoyoverse.BackendID:
		return filepath.Join(root, "hoyoverse")
	case hypergryph.BackendID:
		return filepath.Join(root, "hypergryph")
	}
	return filepath.Join(root, string(backend))
}
```

> 注意：預設（App.TempDir 空）時 `root = <TEMP>/omnigate`，故 kuro=`<TEMP>/omnigate`、hoyo=`<TEMP>/omnigate/hoyoverse`、gryph=`<TEMP>/omnigate/hypergryph` —— 與舊預設**完全一致**。

- [ ] **Step 3: kurogames 加 tempRoot/SetTempRootFn**

`internal/providers/kurogames/kurogames.go`：
- `Provider` struct 加欄位：`tempRootFn func(core.GameID) string`（放在 `settings` 附近）。
- 新增方法（放在 `New` 之後）：
```go
func (p *Provider) tempRoot(gid core.GameID) string {
	if p.tempRootFn != nil {
		return p.tempRootFn(gid)
	}
	if p.settings.TempDir != "" {
		return p.settings.TempDir
	}
	return filepath.Join(os.TempDir(), "omnigate")
}

// SetTempRootFn wires the app-provided temp resolver. Called by App.constructProviders.
func (p *Provider) SetTempRootFn(fn func(core.GameID) string) { p.tempRootFn = fn }
```
- 把 `kurogames.go:325-329` 內聯計算改為呼叫（`plan.GameID` 在該函式可取得）：
```go
	tempDir := p.tempRoot(plan.GameID)
```
（移除原 4 行 `tempDir := p.settings.TempDir; if tempDir == "" { ... }`。）

- [ ] **Step 4: hypergryph 加 tempRoot/SetTempRootFn**

`internal/providers/hypergryph/hypergryph.go`：
- `Provider` struct 加 `tempRootFn func(core.GameID) string`。
- 新增：
```go
func (p *Provider) tempRoot(gid core.GameID) string {
	if p.tempRootFn != nil {
		return p.tempRootFn(gid)
	}
	if p.settings.TempDir != "" {
		return p.settings.TempDir
	}
	return filepath.Join(os.TempDir(), "omnigate", "hypergryph")
}

func (p *Provider) SetTempRootFn(fn func(core.GameID) string) { p.tempRootFn = fn }
```
- 把 `hypergryph.go:250-254` 內聯改為 `tempDir := p.tempRoot(plan.GameID)`（移除原內聯 4 行）。

> 保留兩個 provider 的 `Settings.TempDir` 欄位（作為 `SetTempRootFn` 未呼叫時的 fallback；`hypergryph/update_integration_test.go:104` 仍用它，須維持綠）。

- [ ] **Step 5: hoyoverse 移除 Settings.TempDir 欄位**

`internal/providers/hoyoverse/hoyoverse.go`：`Settings` struct 移除 `TempDir string` 那行（已驗證生產碼無讀者；`tempRoot` 用 `tempRootFn`）。

- [ ] **Step 6: constructProviders 三 provider 統一接線 + 移除 active per-backend TempDir**

`internal/app/app.go` `constructProviders`：
- hoyoverse：`hoyoverse.Settings{Region: ..., TempDir: ...}` 移除 `TempDir` 欄位 →
```go
	hoyo := hoyoverse.New(
		hoyoverse.Settings{Region: a.settings.Backends.Hoyoverse.Region},
		a.logger.With("backend", "hoyoverse"),
	)
	hoyo.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(hoyoverse.BackendID, gid) })
```
- kurogames：移除 `Settings{TempDir: ...}` 傳值，改傳空 `Settings{}` 並接 fn：
```go
	kuro := kurogames.New(kurogames.Settings{}, a.logger.With("backend", "kurogames"))
	kuro.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(kurogames.BackendID, gid) })
```
- hypergryph 同：
```go
	gryph := hypergryph.New(hypergryph.Settings{}, a.logger.With("backend", "hypergryph"))
	gryph.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(hypergryph.BackendID, gid) })
```
（保留各自 `registerProvider` 呼叫。）

`internal/app/settings.go`：移除 active 結構的 per-backend TempDir 欄位：
```go
type HoyoverseSettings struct {
	Path   string `toml:"path"`
	Region string `toml:"region"`
}
type KurogamesSettings struct {
	Path string `toml:"path"`
}
type HypergryphSettings struct {
	Path string `toml:"path"`
}
```
並移除 `LoadSettings` 內對 `raw.Backends.Kurogames.TempDir` / `raw.Backends.Hypergryph.TempDir` 寫進 `out` 的兩段（`settings.go:142-144`、`:148-150`）——這些值現在只供 Task 1 的遷移用，不再進 active settings。

> `rawTOML.Backends.Kurogames`/`.Hypergryph` 仍是真結構帶 TempDir（遷移讀取用），不動。

另有一個非 settings_test 的 compile-break 點：`internal/app/update_phantom_predl_test.go:31` 用了 `KurogamesSettings{TempDir: tempRoot}`，移除欄位後不編譯。改為全域欄位（`tempDirFor(kurogames)` 現回 `App.TempDir` 扁平 root = `tempRoot`，第 39 行 `versionDir` 期望不變）：
```go
		settings:       Settings{App: AppSettings{TempDir: tempRoot}, Backends: BackendSettings{}},
```

- [ ] **Step 7: 移除 6 個 compile-break 測試 + 清未使用 import**

`internal/app/settings_test.go` 刪除整段：`TestSettings_KurogamesTempDir_DefaultEmpty`、`TestSettings_KurogamesTempDir_RoundTrip`、`TestSettings_KurogamesTempDir_BackwardCompat`（其 path-保留斷言已被其他測試覆蓋，可整段刪）、`TestSettings_HoyoverseSettings_TempDir_RoundTrip`、`TestSettings_HoyoverseSettings_TempDir_Omitempty`、`TestSettings_HypergryphTempDirRoundTrip`。

刪除上述測試後，`github.com/pelletier/go-toml/v2` 變成未使用 import（其唯一使用點 `:196/:201/:217` 都在被刪的 hoyoverse 測試內）→ **必須移除 `settings_test.go:9` 的 `"github.com/pelletier/go-toml/v2"` import**，否則 Step 8 `go build`/`go test` 編譯失敗。`strings`/`os`/`path/filepath` 等其他 import 仍有使用，保留。

- [ ] **Step 8: 跑全套後端測試**

Run: `go build ./... && go test ./internal/app/... ./internal/providers/...`
Expected: 全綠（含 `hypergryph/update_integration_test.go` 仍用 provider `Settings{TempDir}` fallback → 綠）。

- [ ] **Step 9: Commit**

```bash
git add internal/app/settings.go internal/app/app.go internal/app/settings_test.go internal/app/app_test.go internal/app/update_phantom_predl_test.go internal/providers/kurogames/kurogames.go internal/providers/hypergryph/hypergryph.go internal/providers/hoyoverse/hoyoverse.go
git commit -m "feat(settings): global temp dir — tempDirFor root from App.TempDir, unify all providers via SetTempRootFn, drop per-backend temp_dir"
```

---

### Task 3: Go bindings — BrowseForImage + GetCustomBackground

**Files:**
- Modify: `internal/app/dialog.go`（`BrowseForImage`）
- Modify: `internal/app/app.go`（`GetCustomBackground` + ext→mime helper）
- Test: `internal/app/dialog_test.go`、`internal/app/app_test.go`（或新 `custombg_test.go`）

- [ ] **Step 1: 寫測試（先紅）**

新增 `internal/app/custombg_test.go`：

```go
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseForImage_NilCtxGuard(t *testing.T) {
	a := &App{} // ctx nil
	got, err := a.BrowseForImage("")
	if err != nil || got != "" {
		t.Errorf("BrowseForImage nil-ctx = (%q,%v), want (\"\",nil)", got, err)
	}
}

func TestGetCustomBackground_EmptyPath(t *testing.T) {
	a := &App{settings: Settings{Games: map[string]GameSettings{}}}
	got, err := a.GetCustomBackground("hoyoverse/genshin")
	if err != nil || got != "" {
		t.Errorf("empty path = (%q,%v), want (\"\",nil)", got, err)
	}
}

func TestGetCustomBackground_Missing(t *testing.T) {
	a := &App{settings: Settings{Games: map[string]GameSettings{
		"hoyoverse/genshin": {BackgroundPath: `Z:\nope.png`},
	}}}
	if _, err := a.GetCustomBackground("hoyoverse/genshin"); err == nil {
		t.Error("missing file: want error, got nil")
	}
}

func TestGetCustomBackground_ReadsDataURL(t *testing.T) {
	tmp := t.TempDir()
	png := filepath.Join(tmp, "bg.png")
	// minimal 1x1 PNG header bytes are enough for base64 round-trip check.
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{settings: Settings{Games: map[string]GameSettings{
		"hoyoverse/genshin": {BackgroundPath: png},
	}}}
	got, err := a.GetCustomBackground("hoyoverse/genshin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("data URL prefix wrong: %.40q", got)
	}
}

func TestGetCustomBackground_WebpAndBmpMime(t *testing.T) {
	tmp := t.TempDir()
	for ext, wantMime := range map[string]string{".webp": "image/webp", ".bmp": "image/bmp"} {
		f := filepath.Join(tmp, "x"+ext)
		if err := os.WriteFile(f, []byte("xx"), 0o644); err != nil {
			t.Fatal(err)
		}
		a := &App{settings: Settings{Games: map[string]GameSettings{"g/x": {BackgroundPath: f}}}}
		got, err := a.GetCustomBackground("g/x")
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if !strings.HasPrefix(got, "data:"+wantMime+";base64,") {
			t.Errorf("%s mime: %.30q want %s", ext, got, wantMime)
		}
	}
}

func TestGetCustomBackground_UnknownExtRejected(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "x.gif")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	a := &App{settings: Settings{Games: map[string]GameSettings{"g/x": {BackgroundPath: f}}}}
	if _, err := a.GetCustomBackground("g/x"); err == nil {
		t.Error("unknown ext: want error")
	}
}
```

- [ ] **Step 2: 跑確認紅**

Run: `go test ./internal/app/ -run 'BrowseForImage|GetCustomBackground' -v`
Expected: 編譯失敗（方法未定義）。

- [ ] **Step 3: 實作 BrowseForImage**

`internal/app/dialog.go`（檔案已 import `os` 與 `wruntime`；新增 `path/filepath`）：

```go
// BrowseForImage opens the native file picker filtered to image files and
// returns the chosen absolute path, or "" if cancelled. nil-ctx guard mirrors
// BrowseForDirectory (avoids Wails getFrontend(nil) log.Fatalf in tests).
func (a *App) BrowseForImage(current string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "選擇背景圖片",
		DefaultDirectory: dialogDefaultDir(filepath.Dir(current)),
		Filters: []wruntime.FileFilter{
			{DisplayName: "Images (*.png;*.jpg;*.jpeg;*.webp;*.bmp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp;*.bmp"},
		},
	})
}
```

- [ ] **Step 4: 實作 GetCustomBackground + mime map**

`internal/app/app.go`（確認 import 有 `encoding/base64`、`os`、`path/filepath`、`strings`；缺則補）。新增：

```go
// imageMIME maps a lowercased file extension to its image MIME type. We do NOT
// use mime.TypeByExtension — webp/bmp are unreliable on Windows registries.
func imageMIME(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	}
	return "", false
}

// GetCustomBackground returns the per-game custom background as a base64 data
// URL, or "" (nil error) when no custom path is set. Read errors / unknown
// extensions return a non-nil error so the frontend falls back to official art.
func (a *App) GetCustomBackground(gameID string) (string, error) {
	a.settingsMu.RLock()
	path := a.settings.Games[gameID].BackgroundPath
	a.settingsMu.RUnlock()
	if path == "" {
		return "", nil
	}
	mimeType, ok := imageMIME(filepath.Ext(path))
	if !ok {
		return "", fmt.Errorf("unsupported image extension: %s", filepath.Ext(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 5: 跑確認綠**

Run: `go test ./internal/app/ -run 'BrowseForImage|GetCustomBackground' -v`
Expected: PASS。再 `go build ./...` 確認整體編譯。

- [ ] **Step 6: 重新產生 Wails bindings**

Run（專案根目錄）: `wails generate module`
這會把 `BrowseForImage` / `GetCustomBackground` 寫進 `frontend/wailsjs/go/app/App.js` 與 `App.d.ts`，並同步刷新 `frontend/wailsjs/go/models.ts`（Settings 結構已變：+`App.TempDir`/`GameSettings.BackgroundPath`、−per-backend `TempDir`）。若有 `models.ts` 變更也一併 commit。
若 `wails` CLI 不可用，手動在 `frontend/wailsjs/go/app/App.d.ts` 加：
```ts
export function BrowseForImage(arg1:string):Promise<string>;
export function GetCustomBackground(arg1:string):Promise<string>;
```
並在 `frontend/wailsjs/go/app/App.js` 加（仿照既有 `BrowseForDirectory` 樣式）：
```js
export function BrowseForImage(arg1) { return window['go']['app']['App']['BrowseForImage'](arg1); }
export function GetCustomBackground(arg1) { return window['go']['app']['App']['GetCustomBackground'](arg1); }
```

- [ ] **Step 7: Commit**

```bash
git add internal/app/dialog.go internal/app/app.go internal/app/custombg_test.go frontend/wailsjs/go/app/App.js frontend/wailsjs/go/app/App.d.ts frontend/wailsjs/go/models.ts
git commit -m "feat(app): BrowseForImage + GetCustomBackground (data URL, explicit image mime map)"
```

---

### Task 4: SettingsPanel「一般」區塊（語言下拉 + 全域暫存）+ Topbar 移除語言鈕

**Files:**
- Modify: `frontend/src/components/SettingsPanel.vue`
- Modify: `frontend/src/components/Topbar.vue`
- Modify: `frontend/src/locales/{zh-TW,en,zh-CN}.json`
- Test: `frontend/src/__tests__/settings_panel.test.ts`

- [ ] **Step 1: 加 i18n key（3 locale）**

三個 locale 的 `"settings"` 區塊（`:197`）內，把 `tempdir_label`/`tempdir_hint` 文案改為全域語意並新增 `general` / `language_label`：

zh-TW（取代 `:204-205` 並加 2 鍵，放在 `"clear"` 後）：
```json
    "general": "一般",
    "language_label": "語言",
    "tempdir_label": "暫存資料夾",
    "tempdir_hint": "留空 = 使用預設暫存資料夾（所有遊戲共用）",
```
en：
```json
    "general": "General",
    "language_label": "Language",
    "tempdir_label": "Temp folder",
    "tempdir_hint": "Leave empty to use the default temp folder (shared by all games)",
```
zh-CN：
```json
    "general": "常规",
    "language_label": "语言",
    "tempdir_label": "临时文件夹",
    "tempdir_hint": "留空 = 使用默认临时文件夹（所有游戏共用）",
```
（`backend.*` 鍵可保留，本任務不再用到，但 Task 6 的 per-game 區塊不需要它；保留無害。）

- [ ] **Step 2: 改 settings_panel.test.ts（先紅）**

既有測試 helper 是 `mountOpen()`（非 `mountPanelOpen`）；既有 `sampleSettings()` 缺 `App.TempDir` 且有兩個測試硬編 `Backends.Hoyoverse.TempDir`，新版會壞，須一併改：

(a) `sampleSettings()` 的 `App` 加 `TempDir: ''`：
```ts
  App: { Language: 'zh-TW', TempDir: '', BannerAnimationPref: 'video-when-available', ShowTechnicalInfo: false },
```

(b) `beforeEach` 末尾加一行，避免跨測試語言洩漏：
```ts
    i18n.global.locale.value = 'zh-TW';
```

(c) **改寫** `it('loads settings into the draft on open')`：第一個 text input 現在是全域暫存（空字串）：
```ts
  it('loads settings into the draft on open', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(GetSettings).toHaveBeenCalled();
    expect(w.find('input[data-test="settings-tempdir"]').exists()).toBe(true);
    expect((w.find('input[data-test="settings-tempdir"]').element as HTMLInputElement).value).toBe('');
  });
```

(d) **改寫** `it('Save calls UpdateSettings...')`：改編全域暫存、斷言 `arg.App.TempDir`：
```ts
  it('Save calls UpdateSettings with the (edited) draft, preserving unshown fields', async () => {
    const w = mountOpen();
    await flushPromises();
    await w.find('input[data-test="settings-tempdir"]').setValue('D:/NewTemp');
    await w.find('[data-test="settings-save"]').trigger('click');
    await flushPromises();
    expect(UpdateSettings).toHaveBeenCalledTimes(1);
    const arg = UpdateSettings.mock.calls[0][0];
    expect(arg.App.TempDir).toBe('D:/NewTemp');
    expect(arg.Backends.Hoyoverse.Path).toBe('C:/HoYoPlay'); // unshown preserved
    expect(arg.App.Language).toBe('zh-TW');
    expect(arg.Games['kurogames/wutheringwaves'].Path).toBe('D:/WW'); // per-game survives
  });
```

(e) 新增渲染斷言：
```ts
  it('renders a language dropdown with three options', async () => {
    const w = mountOpen();
    await flushPromises();
    const select = w.find('select[data-test="settings-language"]');
    expect(select.exists()).toBe(true);
    expect(select.findAll('option')).toHaveLength(3);
  });
```

- [ ] **Step 3: 重寫 SettingsPanel template + script**

`frontend/src/components/SettingsPanel.vue`：

script 區 import 與狀態新增（取代現有 import 行並加 i18n setLocale 能力）：
```ts
import { ref, computed, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useUpdatesStore } from '../stores/updates';
import { GetSettings, UpdateSettings, BrowseForDirectory } from '../../wailsjs/go/app/App';
import { refreshAll } from '../composables/useRefreshAll';
import { i18n, setLang } from '../i18n';

const { t } = useI18n();
const view = useViewStore();
const updates = useUpdatesStore();

const draft = ref<any>(null);
const saveError = ref('');
const saving = ref(false);
let openLang: 'zh-TW' | 'zh-CN' | 'en' = 'zh-TW';
let savedThisSession = false;
```

把 open/close watch 改為擷取 openLang + close 分支還原：
```ts
watch(
  () => view.settingsOpen,
  async (open) => {
    if (open) {
      saveError.value = '';
      savedThisSession = false;
      openLang = i18n.global.locale.value as typeof openLang;
      window.addEventListener('keydown', onKeydown);
      draft.value = JSON.parse(JSON.stringify(await GetSettings()));
    } else {
      window.removeEventListener('keydown', onKeydown);
      // Restore live language on any non-save close (Cancel / ESC / gear toggle).
      if (!savedThisSession && i18n.global.locale.value !== openLang) {
        setLang(openLang);
      }
      draft.value = null;
    }
  },
  { immediate: true },
);
```

語言即時套用 handler：
```ts
function onLangChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value as 'zh-TW' | 'zh-CN' | 'en';
  draft.value.App.Language = v;
  setLang(v);
}
```

`onSave` 設旗標（持久化含 App.Language/App.TempDir，draft 整包已含）：
```ts
async function onSave() {
  if (saveDisabled.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    await UpdateSettings(draft.value);
    savedThisSession = true;
    await refreshAll();
    view.closeSettings();
  } catch (e: any) {
    saveError.value = t('settings.save_error', { detail: e?.message ?? String(e) });
  } finally {
    saving.value = false;
  }
}
```

template `settings-body` 內，把三個 per-backend 區塊**整段移除**，改為一個「一般」區塊（per-game 自訂圖區塊由 Task 6 接，這裡先不放）：
```html
      <div class="settings-body">
        <div class="settings-group">
          <div class="grid-section-label"><span>{{ t('settings.general') }}</span></div>

          <label class="settings-label">{{ t('settings.language_label') }}</label>
          <div class="settings-row">
            <select data-test="settings-language" :value="draft.App.Language" @change="onLangChange">
              <option value="zh-TW">繁體中文</option>
              <option value="zh-CN">简体中文</option>
              <option value="en">English</option>
            </select>
          </div>

          <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
          <div class="settings-row">
            <input type="text" data-test="settings-tempdir" v-model="draft.App.TempDir" :placeholder="t('settings.tempdir_hint')" />
            <button class="settings-browse" @click="browse((p) => (draft.App.TempDir = p), draft.App.TempDir)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="draft.App.TempDir = ''">{{ t('settings.clear') }}</button>
          </div>
        </div>

        <div v-if="saveError" class="settings-error">{{ saveError }}</div>
      </div>
```

（`browse`、`onCancel`、`onKeydown`、`saveDisabled`、`anyInFlight`、footer 區塊保留不動。）

- [ ] **Step 4: Topbar 移除語言按鈕**

`frontend/src/components/Topbar.vue`：
- 刪除 template 的 `<button class="icon-btn" @click="cycleLang" ...>` 整行（`:39`）。
- 刪除 script 的 `cycleLang` 函式（`:21-25`）與其註解（`:19-20`）。
- 移除不再使用的 import：`import { i18n, setLang } from '../i18n';`（`:5`）與 `import { SetLanguage } from '../../wailsjs/go/app/App';`（`:6`）。

> 確認 Topbar 其餘程式碼不再引用 `i18n`/`setLang`/`SetLanguage`（grep 一次）；`useI18n`/`t` 仍可能他用，保留。

- [ ] **Step 5: 加語言即時套用 + 取消還原測試**

`settings_panel.test.ts` 新增（`i18n` 已在檔案 import；`mountOpen` 為既有 helper；beforeEach 已重置 locale）：
```ts
  it('applies language live on change and reverts on cancel', async () => {
    const w = mountOpen();
    await flushPromises();
    expect(i18n.global.locale.value).toBe('zh-TW');
    await w.find('select[data-test="settings-language"]').setValue('en');
    expect(i18n.global.locale.value).toBe('en');
    await w.find('[data-test="settings-cancel"]').trigger('click');
    await flushPromises();
    expect(i18n.global.locale.value).toBe('zh-TW');
  });
```

- [ ] **Step 6: 跑前端測試 + 型別**

Run（在 `frontend/`）: `npm run test -- settings_panel` 然後 `npm run build`
Expected: 綠；`npm run build` 型別檢查通過（Topbar 無未用 import）。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/SettingsPanel.vue frontend/src/components/Topbar.vue frontend/src/locales/zh-TW.json frontend/src/locales/en.json frontend/src/locales/zh-CN.json frontend/src/__tests__/settings_panel.test.ts
git commit -m "feat(settings-ui): move language to settings dropdown (live-apply + cancel-revert), single global temp dir field, remove Topbar language button"
```

---

### Task 5: 背景資料模型換裝（store backgrounds[] + bgIndex；BgLayer 依 index；GridCard）

**Files:**
- Modify: `frontend/src/stores/games.ts`
- Modify: `frontend/src/components/BgLayer.vue`
- Modify: `frontend/src/components/GridCard.vue`
- Test: `frontend/src/__tests__/games_store.test.ts`、`frontend/src/__tests__/games_launch.test.ts`、`frontend/src/__tests__/bg_layer.test.ts`（新）

- [ ] **Step 1: 改 games store 型別與資產載入測試（先紅）**

先在 `games_store.test.ts` 的 `vi.mock('../../wailsjs/go/app/App', ...)` factory 內加 `GetCustomBackground: vi.fn(() => Promise.resolve(''))`（並在需要自訂圖的測試裡 `mockResolvedValueOnce('data:image/png;base64,AAA')`）。`GetBackgrounds` mock 回傳 `[{ImageURL,VideoURL,Type},…]`。

`games_store.test.ts` 新增：
```ts
it('fills backgrounds[] (no collapse) and seeds bgIndex in range', async () => {
  // GetBackgrounds mock returns 3 entries; GetCustomBackground returns ''
  const store = useGamesStore();
  // ...load + loadAssets per existing harness...
  const g = store.games.find((x) => x.id === 'hoyoverse/genshin')!;
  expect(g.backgrounds!.length).toBe(3);
  expect(g.bgIndex!).toBeGreaterThanOrEqual(0);
  expect(g.bgIndex!).toBeLessThan(3);
});

it('custom background replaces official list', async () => {
  // GetCustomBackground mock returns 'data:image/png;base64,AAA'
  const store = useGamesStore();
  // ...loadAssetsFor('hoyoverse/genshin')...
  const g = store.games.find((x) => x.id === 'hoyoverse/genshin')!;
  expect(g.backgrounds).toEqual([{ image: 'data:image/png;base64,AAA', video: '' }]);
});
```

- [ ] **Step 2: 改 games_launch.test.ts（先紅）**

`games_launch.test.ts`：
- mock factory（`:5-9`）加 `GetCustomBackground: vi.fn(),`（store 現在 import 它；雖此測試不呼叫，補上保持模組完整）。
- 刪除 row literal 的 `background_url: 'bg://y'`（`:22`，欄位已從 `GameRow` 移除，留著會 TS excess-property error）。`icon_url: 'icon://x'` 保留。
- 刪除 `expect(row.background_url).toBe('bg://y')`（`:32`）；既有 `expect(row.icon_url).toBe('icon://x')`（`:31`）已足以驗證「launchGame 不抹資產欄位」。

- [ ] **Step 3: 跑確認紅**

Run（`frontend/`）: `npm run test -- games_store games_launch`
Expected: 失敗（`backgrounds`/`bgIndex` 不存在 / 型別錯）。

- [ ] **Step 4: 改 games store 實作**

`frontend/src/stores/games.ts`：

import 加 `GetCustomBackground`：
```ts
import { ListGames, RefreshVersion, GetIcon, GetBackgrounds, GetCustomBackground, SetGameOverride, ClearGameOverride, RefreshGame, Launch } from '../../wailsjs/go/app/App';
```

`GameRow` 型別：移除 `background_url`/`background_video`，加 backgrounds/bgIndex：
```ts
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
  backgrounds?: { image: string; video: string }[];
  bgIndex?: number;
  resolved_path?: string;
  path_source?: string;
  override_path?: string;
  last_played?: string;
};
```

state 加自訂圖快取：
```ts
  state: () => ({
    games: [] as GameRow[],
    selectedID: '' as string,
    _customBg: {} as Record<string, string>, // gameID → data URL ('' = fetched & none; undefined = unfetched)
  }),
```

新增 helper actions + 改寫 loadAssets/loadAssetsFor：
```ts
    _randomIndex(len: number): number {
      return len > 0 ? Math.floor(Math.random() * len) : 0;
    },
    // Returns true if a custom background replaced g.backgrounds.
    async _applyCustomBg(g: GameRow): Promise<boolean> {
      if (this._customBg[g.id] === undefined) {
        try { this._customBg[g.id] = await GetCustomBackground(g.id); }
        catch { this._customBg[g.id] = ''; }
      }
      const url = this._customBg[g.id];
      if (url) { g.backgrounds = [{ image: url, video: '' }]; return true; }
      return false;
    },
    invalidateCustomBg() { this._customBg = {}; },
    async loadAssets() {
      for (const g of this.games) {
        if (!g.installed) continue;
        try {
          if (!g.icon_url) g.icon_url = await GetIcon(g.id);
          if (!(await this._applyCustomBg(g))) {
            const bgs = await GetBackgrounds(g.id);
            g.backgrounds = bgs.map((b) => ({ image: b.ImageURL, video: b.VideoURL }));
          }
          g.bgIndex = this._randomIndex(g.backgrounds?.length ?? 0);
        } catch (e) {
          console.warn('assets failed', g.id, e);
        }
      }
    },
    async loadAssetsFor(gameID: string) {
      const idx = this.games.findIndex((g) => g.id === gameID);
      if (idx < 0 || !this.games[idx].installed) return;
      try {
        const g = this.games[idx];
        if (!g.icon_url) g.icon_url = await GetIcon(gameID);
        if (!(await this._applyCustomBg(g))) {
          const bgs = await GetBackgrounds(gameID);
          g.backgrounds = bgs.map((b) => ({ image: b.ImageURL, video: b.VideoURL }));
        }
        g.bgIndex = this._randomIndex(g.backgrounds?.length ?? 0);
      } catch (e) {
        console.warn('loadAssetsFor failed', gameID, e);
      }
    },
```

更新 `launchGame` 上方註解（把 `background_url/background_video` 字樣改為 `backgrounds/bgIndex`）。`_replaceRow` 不需改（它已呼叫 `loadAssetsFor`，會重 seed backgrounds+bgIndex；splice 進的新 row 暫無這些欄位，消費端需 `?.` 守衛，見 BgLayer/BottomBar）。

> `_customBg` 在 `loadAssetsFor` 仍用快取；自訂圖路徑改變由 SettingsPanel onSave 呼叫 `invalidateCustomBg()`（Task 6 接）；本 task 先確保快取邏輯與 invalidate action 存在。

- [ ] **Step 5: 改 BgLayer 依 backgrounds[bgIndex] + rapid-toggle 健壯化**

`frontend/src/components/BgLayer.vue` script 整段replace 為：
```ts
<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useGamesStore } from '../stores/games';

const games = useGamesStore();

const current = computed(() => {
  const g = games.selected;
  const list = g?.backgrounds;
  if (!list || !list.length) return { image: '', video: '' };
  const i = g!.bgIndex ?? 0;
  return list[i] ?? list[0];
});

// Static image dual-buffer
const slotA = ref(''); const slotB = ref(''); const useA = ref(true);
// Video dual-buffer
const videoA = ref(''); const videoB = ref('');
const visibleA = ref(false); const visibleB = ref(false);
// The video src we currently want visible ('' = none). A canplay only promotes
// its slot if that slot still holds wantVid — drops stale promotions from a
// superseded rapid switch (the dot-toggle robustness fix).
let wantVid = '';
let pending: 'A' | 'B' | null = null;

function promote(slot: 'A' | 'B') {
  if (slot === 'A') { visibleA.value = true; visibleB.value = false; }
  else { visibleB.value = true; visibleA.value = false; }
  pending = null;
}
function showVideoIn(slot: 'A' | 'B', src: string) {
  const cur = slot === 'A' ? videoA.value : videoB.value;
  if (slot === 'A') videoA.value = src; else videoB.value = src;
  pending = slot;
  if (cur === src && src) promote(slot); // no canplay will fire; flip manually
}

watch(current, ({ image, video }) => {
  if (image) {
    if (useA.value) { slotB.value = image; useA.value = false; }
    else            { slotA.value = image; useA.value = true; }
  }
  wantVid = video || '';
  if (!video) { visibleA.value = false; visibleB.value = false; return; }
  if (visibleA.value && videoA.value === video) return;
  if (visibleB.value && videoB.value === video) return;
  if (visibleA.value) showVideoIn('B', video);
  else                showVideoIn('A', video);
}, { immediate: true });

function onCanPlay(slot: 'A' | 'B') {
  if (pending !== slot) return;
  const src = slot === 'A' ? videoA.value : videoB.value;
  if (src !== wantVid) { pending = null; return; } // superseded → drop
  promote(slot);
}
const onVideoACanPlay = () => onCanPlay('A');
const onVideoBCanPlay = () => onCanPlay('B');
</script>
```
template 不變（兩張 `<img class="app-bg">` 用 `slotA`/`slotB`+`useA`，兩個 `<video class="app-bg-video">` 用 `videoA`/`videoB`+`visibleA/B` 與 `@canplay`）。

- [ ] **Step 6: 改 GridCard**

`frontend/src/components/GridCard.vue:22`：
```html
      <img v-if="row.backgrounds?.[0]?.image" :src="row.backgrounds[0].image" alt="" />
```

- [ ] **Step 7: 寫 BgLayer rapid-toggle 測試**

新增 `frontend/src/__tests__/bg_layer.test.ts`：
```ts
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { beforeEach, it, expect } from 'vitest';
import BgLayer from '../components/BgLayer.vue';
import { useGamesStore } from '../stores/games';

beforeEach(() => setActivePinia(createPinia()));

function fireCanPlay(wrapper: any) {
  wrapper.findAll('video').forEach((v: any) => v.element.dispatchEvent(new Event('canplay')));
}

it('switching to an image-only background hides both videos', async () => {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false,
    backgrounds: [{ image: 'i0', video: 'v0' }, { image: 'i1', video: '' }], bgIndex: 0 } as any];
  store.selectedID = 'g/1';
  const wrapper = mount(BgLayer);
  await wrapper.vm.$nextTick();
  fireCanPlay(wrapper); // promote v0
  store.games[0].bgIndex = 1; // switch to image-only
  await wrapper.vm.$nextTick();
  const vids = wrapper.findAll('video');
  expect(vids.every((v: any) => v.classes().includes('fading'))).toBe(true);
});

it('rapid video1→video2→video1 ends visible on video1 and ignores stale canplay', async () => {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false,
    backgrounds: [{ image: 'i0', video: 'v0' }, { image: 'i1', video: 'v1' }], bgIndex: 0 } as any];
  store.selectedID = 'g/1';
  const wrapper = mount(BgLayer);
  await wrapper.vm.$nextTick();
  fireCanPlay(wrapper);                 // v0 visible
  store.games[0].bgIndex = 1; await wrapper.vm.$nextTick(); // request v1
  store.games[0].bgIndex = 0; await wrapper.vm.$nextTick(); // back to v0 before v1 canplay
  fireCanPlay(wrapper);                 // both fire; only the slot matching wantVid(v0) may promote
  const vids = wrapper.findAll('video');
  const visible = vids.filter((v: any) => !v.classes().includes('fading'));
  expect(visible.length).toBe(1);
  expect((visible[0].element as HTMLVideoElement).getAttribute('src')).toBe('v0');
});
```

- [ ] **Step 8: 跑前端測試 + build**

Run（`frontend/`）: `npm run test -- games_store games_launch bg_layer` 然後 `npm run build`
Expected: 綠；型別檢查通過（無殘留 `background_url` 引用）。

- [ ] **Step 9: Commit**

```bash
git add frontend/src/stores/games.ts frontend/src/components/BgLayer.vue frontend/src/components/GridCard.vue frontend/src/__tests__/games_store.test.ts frontend/src/__tests__/games_launch.test.ts frontend/src/__tests__/bg_layer.test.ts
git commit -m "feat(bg): per-row backgrounds[]+bgIndex with random start, custom-bg replace+cache, BgLayer index-driven + rapid-toggle safe; GridCard uses backgrounds[0]"
```

---

### Task 6: BottomBar 圓點指示器（絕對置中）

**Files:**
- Modify: `frontend/src/components/BottomBar.vue`
- Test: `frontend/src/__tests__/bottombar_dots.test.ts`（新）

- [ ] **Step 1: 寫測試（先紅）**

新增 `frontend/src/__tests__/bottombar_dots.test.ts`：
```ts
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { beforeEach, it, expect } from 'vitest';
import { createI18n } from 'vue-i18n';
import BottomBar from '../components/BottomBar.vue';
import { useGamesStore } from '../stores/games';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {} }, missingWarn: false, fallbackWarn: false });

beforeEach(() => setActivePinia(createPinia()));

function mountWith(backgrounds: any[], bgIndex: number) {
  const store = useGamesStore();
  store.games = [{ id: 'g/1', backend: 'b', display_name: { en: 'x' }, installed: true, has_predownload: false, backgrounds, bgIndex } as any];
  store.selectedID = 'g/1';
  return mount(BottomBar, { global: { plugins: [i18n], stubs: { GameConfigPopover: true } } });
}

it('renders one dot per background, active on bgIndex', () => {
  const wrapper = mountWith([{ image: 'a', video: '' }, { image: 'b', video: '' }, { image: 'c', video: '' }], 1);
  const dots = wrapper.findAll('[data-test="bg-dot"]');
  expect(dots).toHaveLength(3);
  expect(dots[1].classes()).toContain('active');
});

it('clicking a dot sets bgIndex on the selected row', async () => {
  const wrapper = mountWith([{ image: 'a', video: '' }, { image: 'b', video: '' }], 0);
  await wrapper.findAll('[data-test="bg-dot"]')[1].trigger('click');
  expect(useGamesStore().selected!.bgIndex).toBe(1);
});

it('hides dots when fewer than 2 backgrounds', () => {
  const wrapper = mountWith([{ image: 'a', video: '' }], 0);
  expect(wrapper.findAll('[data-test="bg-dot"]')).toHaveLength(0);
});
```

- [ ] **Step 2: 跑確認紅**

Run（`frontend/`）: `npm run test -- bottombar_dots`
Expected: 失敗（無 `bg-dot`）。

- [ ] **Step 3: 加圓點 markup + 互動 + 樣式**

`frontend/src/components/BottomBar.vue`：

script 新增 computed + handler（放在 `lastPlayedLabel` 附近）：
```ts
const bgDots = computed(() => games.selected?.backgrounds?.length ?? 0);
function setBg(i: number) {
  if (games.selected) games.selected.bgIndex = i;
}
```

template：在 `<div class="bottom-bar">` 內（與 `hero-meta`/`bottombar-right` 同層、放在 `bottombar-right` 之前或之後皆可，因為用絕對定位）加：
```html
    <div v-if="bgDots > 1" class="bg-dots">
      <button
        v-for="i in bgDots"
        :key="i"
        data-test="bg-dot"
        class="bg-dot"
        :class="{ active: (games.selected!.bgIndex ?? 0) === i - 1 }"
        @click="setBg(i - 1)"
        aria-label="background"
      ></button>
    </div>
```

樣式：`.bottom-bar` 規則在 `frontend/src/styles/theme.css`（`theme.css:288`，且**只有 `theme.css` 被 `main.ts:5` import**；`frontend/src/style.css` 沒被任何地方 import，**不要**放那裡否則正式版無樣式）。把以下規則加到 `frontend/src/styles/theme.css`（`.bottom-bar` 已是 `position: absolute`，本身即定位脈絡，**不需**再加 `position: relative`）：
```css
.bg-dots {
  position: absolute;
  left: 50%;
  bottom: 18px;
  transform: translateX(-50%);
  display: flex;
  gap: 8px;
  pointer-events: none; /* container ignores; dots re-enable */
}
.bg-dot {
  pointer-events: auto;
  width: 8px;
  height: 8px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: var(--tx-dim, rgba(255,255,255,0.35));
  cursor: pointer;
  transition: background 0.2s, transform 0.2s;
}
.bg-dot.active {
  background: var(--gold-1, #e8c87a);
  transform: scale(1.25);
}
```

- [ ] **Step 4: 跑測試 + build**

Run（`frontend/`）: `npm run test -- bottombar_dots` 然後 `npm run build`
Expected: 綠。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/BottomBar.vue frontend/src/__tests__/bottombar_dots.test.ts frontend/src/styles/theme.css
git commit -m "feat(bg): BottomBar centered dot indicators for background carousel (manual switch, hidden when <2)"
```

---

### Task 7: SettingsPanel per-game 自訂背景圖區塊

**Files:**
- Modify: `frontend/src/components/SettingsPanel.vue`
- Modify: `frontend/src/locales/{zh-TW,en,zh-CN}.json`
- Test: `frontend/src/__tests__/settings_panel.test.ts`

- [ ] **Step 1: 加 i18n key（3 locale）**

`"settings"` 區塊新增：
zh-TW：`"custom_bg": "背景圖",` `"custom_bg_label": "自訂背景圖",`
en：`"custom_bg": "Background",` `"custom_bg_label": "Custom background",`
zh-CN：`"custom_bg": "背景图",` `"custom_bg_label": "自定义背景图",`

- [ ] **Step 2: 寫測試（先紅）**

先在 `settings_panel.test.ts` 的 `vi.mock('../../wailsjs/go/app/App', ...)` factory 內加 `BrowseForImage: vi.fn(() => Promise.resolve('')),`（SettingsPanel 現在 import 它）。`settings_panel.test.ts` 新增（先 seed games store 一筆遊戲，再 mountOpen）：
```ts
  it('lists games with a custom background field bound to Games[id].BackgroundPath', async () => {
    const games = useGamesStore();
    games.games = [{ id: 'hoyoverse/genshin', backend: 'hoyoverse', display_name: { 'zh-TW': '原神', en: 'Genshin' }, installed: true, has_predownload: false } as any];
    const w = mountOpen();
    await flushPromises();
    expect(w.find('input[data-test="settings-custombg-hoyoverse/genshin"]').exists()).toBe(true);
  });
```
（檔案頂 import `useGamesStore`：`import { useGamesStore } from '../stores/games';`。）

- [ ] **Step 3: SettingsPanel 加 per-game 區塊**

script 加 games store + helper：
```ts
import { useGamesStore } from '../stores/games';
import { BrowseForImage } from '../../wailsjs/go/app/App';
const games = useGamesStore();

function bgPath(id: string): string {
  return draft.value?.Games?.[id]?.BackgroundPath ?? '';
}
function setBgPath(id: string, p: string) {
  if (!draft.value.Games) draft.value.Games = {};
  if (!draft.value.Games[id]) draft.value.Games[id] = {};
  draft.value.Games[id].BackgroundPath = p;
}
async function browseImage(id: string) {
  try {
    const p = await BrowseForImage(bgPath(id));
    if (p) setBgPath(id, p);
  } catch (e) { console.error('BrowseForImage failed', e); }
}
function displayName(g: any): string {
  return g.display_name[i18n.global.locale.value] || g.display_name.en;
}
```

template：在「一般」`settings-group` 之後、`saveError` 之前加：
```html
        <div class="settings-group">
          <div class="grid-section-label"><span>{{ t('settings.custom_bg') }}</span></div>
          <template v-for="g in games.games" :key="g.id">
            <label class="settings-label">{{ displayName(g) }} — {{ t('settings.custom_bg_label') }}</label>
            <div class="settings-row">
              <input type="text" :data-test="`settings-custombg-${g.id}`" :value="bgPath(g.id)" @input="setBgPath(g.id, ($event.target as HTMLInputElement).value)" :placeholder="t('settings.custom_bg_label')" />
              <button class="settings-browse" @click="browseImage(g.id)">{{ t('settings.browse') }}</button>
              <button class="settings-clear" @click="setBgPath(g.id, '')">{{ t('settings.clear') }}</button>
            </div>
          </template>
        </div>
```

`onSave`：在 `UpdateSettings` 成功後、`refreshAll` 之前加 `games.invalidateCustomBg();`：
```ts
    await UpdateSettings(draft.value);
    savedThisSession = true;
    games.invalidateCustomBg();
    await refreshAll();
```

- [ ] **Step 4: 跑測試 + build**

Run（`frontend/`）: `npm run test -- settings_panel` 然後 `npm run build`
Expected: 綠。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/SettingsPanel.vue frontend/src/locales/zh-TW.json frontend/src/locales/en.json frontend/src/locales/zh-CN.json frontend/src/__tests__/settings_panel.test.ts
git commit -m "feat(settings-ui): per-game custom background field (BrowseForImage), invalidate cache on save"
```

---

### Task 8: 全套驗證 + wails build smoke 準備

**Files:** 無（驗證）

- [ ] **Step 1: 後端全綠**

Run: `go build ./... && go vet ./... && go test ./internal/...`
Expected: 全綠（不加 `-race`）。

- [ ] **Step 2: 前端全綠 + build**

Run（`frontend/`）: `npm run test` 然後 `npm run build`
Expected: 全綠；無殘留 `background_url`/`background_video` 引用、無未用 import。

- [ ] **Step 3: wails build**

Run: `wails build`
Expected: 產出 `build/bin/omnigate.exe`，無錯。

- [ ] **Step 4: 交付使用者 smoke**

提供 smoke checklist（繁中），請使用者驗：語言下拉三選一即時切換 + 取消還原 + 重啟保留；暫存單欄寫入並生效（看 settings.toml `[app] temp_dir`、無 `[backends.*] temp_dir`）；主畫面背景圓點切換（含影片↔圖片混切流暢）；每遊戲自訂圖瀏覽後取代官方、清除後回官方、刪檔 fallback。
（smoke 通過後才走 `--no-ff` 併回 `dev`，由使用者決定發版時機。）
