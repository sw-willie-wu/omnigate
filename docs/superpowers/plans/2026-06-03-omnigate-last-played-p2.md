# P2 last-played 強化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓每款遊戲的「上次遊玩」也反映在 omnigate 之外（官方啟動器／直接開 exe）的遊玩——取 `max(playstate 時間, 該遊戲 runtime 檔 mtime)`。

**Architecture:** 新增可選 Provider 介面 `core.LastPlayedProbe`，各 provider 回報「每次啟動會被改寫的引擎 player log」候選路徑（HoYoverse/Endfield 在 `%USERPROFILE%\AppData\LocalLow`、鳴潮 UE4 在安裝目錄 `Client\Saved\Logs\Client.log`）。App 在 `gameRowLocked` 對候選檔 `os.Stat` 取最大 mtime，再與 `playState.Get` 取 max。純後端、前端零改動、未實作或無檔即優雅降級回 playstate-only。

**Tech Stack:** Go 1.26（`go test ./...`，本機 CGO_ENABLED=0 故 drop `-race`）；Wails；無新依賴。

**Spec:** `docs/superpowers/specs/2026-06-03-omnigate-last-played-p2-design.md`

**設計要點（實作須知）**
- gameID 常數：`hoyoverse/{genshin,starrail,zzz}`、`kurogames/wutheringwaves`、`hypergryph/endfield`。
- HoYoverse LocalLow publisher 資料夾**不一致**：Genshin/ZZZ 在 `miHoYo`、Star Rail 在 `Cognosphere`。
- kurogames `installDir` = `a.resolved[gid].Path` = 遊戲資料夾（`<launcherRoot>\Wuthering Waves Game`），`Client\…` 直接 join、不可再往上層。
- 三 provider 皆為 `type Provider struct`，`&Provider{}` 零值可建（probe 方法不碰其欄位）。
- `app.go` 目前**未** import `os`，Task 5 需補。`time`/`filepath`/`strings` 已 import。
- 既有可選介面（`InstallLocator`/`ResolvedPathSetter`）放在 `internal/core/provider.go`；新介面同檔。

**File Structure**
- Modify `internal/core/provider.go` — 加 `LastPlayedProbe` 介面（Task 1）。
- Create `internal/providers/hoyoverse/lastplayed.go` + `lastplayed_test.go`（Task 2）。
- Create `internal/providers/hypergryph/lastplayed.go` + `lastplayed_test.go`（Task 3）。
- Create `internal/providers/kurogames/lastplayed.go` + `lastplayed_test.go`（Task 4）。
- Modify `internal/app/app.go`（`statModTime` seam + `lastPlayedLocked` + `gameRowLocked` 改簽名 + 兩呼叫點）+ `internal/app/app_test.go`（Task 5）。

> 任務序：1（介面）→ 2/3/4（providers，彼此獨立）→ 5（app 整合）。每 task TDD red→green，**先不 commit**，過審查 gate 後才 commit（見 subagent-review-gates）。

---

### Task 1: 核心可選介面 `core.LastPlayedProbe`

**Files:**
- Modify: `internal/core/provider.go`（接在 `ResolvedPathSetter` 之後，約 line 157）

- [ ] **Step 1: 加入介面定義**

在 `internal/core/provider.go` 中 `ResolvedPathSetter` 介面定義之後、`// Provider is the integration point` 註解之前，插入：

```go
// LastPlayedProbe is an optional Provider capability. Given a game and its
// resolved install dir, it returns filesystem paths whose mtime indicates the
// game was launched — including launches outside omnigate (the game engine's
// player log, rewritten on each launch). The App stats each path and takes the
// most recent mtime, then maxes it against the recorded playstate timestamp.
//
// Implementations MUST be pure path construction: no filesystem IO, no errors.
// Non-existent paths are filtered by the App's stat step. An empty/nil return
// means "no extra signal" (the App falls back to the playstate timestamp).
type LastPlayedProbe interface {
	LastPlayedFiles(gid GameID, installDir string) []string
}
```

- [ ] **Step 2: 編譯驗證（介面定義無行為測試）**

Run: `go build ./...`
Expected: 成功、無錯（純介面宣告，尚無實作者）。

- [ ] **Step 3: Commit**（過審查 gate 後才執行）

```bash
git add internal/core/provider.go
git commit -m "feat(core): add optional LastPlayedProbe provider interface"
```

---

### Task 2: hoyoverse `LastPlayedFiles`

**Files:**
- Create: `internal/providers/hoyoverse/lastplayed.go`
- Test: `internal/providers/hoyoverse/lastplayed_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/hoyoverse/lastplayed_test.go`：

```go
package hoyoverse

import (
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestLastPlayedFiles_Hoyoverse(t *testing.T) {
	p := &Provider{}
	cases := map[core.GameID]string{
		"hoyoverse/genshin":  filepath.Join("miHoYo", "Genshin Impact"),
		"hoyoverse/starrail": filepath.Join("Cognosphere", "Star Rail"),
		"hoyoverse/zzz":      filepath.Join("miHoYo", "ZenlessZoneZero"),
	}
	for gid, sub := range cases {
		files := p.LastPlayedFiles(gid, "")
		if len(files) != 2 {
			t.Fatalf("%s: want 2 candidates, got %d: %v", gid, len(files), files)
		}
		wantLog := filepath.Join("AppData", "LocalLow", sub, "output_log.txt")
		wantPlayer := filepath.Join("AppData", "LocalLow", sub, "Player.log")
		if !strings.HasSuffix(files[0], wantLog) {
			t.Errorf("%s: candidate[0]=%q want suffix %q", gid, files[0], wantLog)
		}
		if !strings.HasSuffix(files[1], wantPlayer) {
			t.Errorf("%s: candidate[1]=%q want suffix %q", gid, files[1], wantPlayer)
		}
	}
}

func TestLastPlayedFiles_Hoyoverse_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("hoyoverse/unknown", ""); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/hoyoverse/ -run TestLastPlayedFiles -v`
Expected: 編譯失敗（`p.LastPlayedFiles undefined`）。

- [ ] **Step 3: 寫最小實作**

Create `internal/providers/hoyoverse/lastplayed.go`：

```go
package hoyoverse

import (
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

// localLowProduct maps each supported game to its Unity player-log folder under
// %USERPROFILE%\AppData\LocalLow. Publisher folders are NOT uniform: Genshin and
// ZZZ live under miHoYo, Star Rail under Cognosphere (global-version names).
var localLowProduct = map[core.GameID]string{
	"hoyoverse/genshin":  filepath.Join("miHoYo", "Genshin Impact"),
	"hoyoverse/starrail": filepath.Join("Cognosphere", "Star Rail"),
	"hoyoverse/zzz":      filepath.Join("miHoYo", "ZenlessZoneZero"),
}

// LastPlayedFiles implements core.LastPlayedProbe. The Unity player log
// (output_log.txt; Player.log on newer Unity) is rewritten on each launch, so
// its mtime reflects the last play — including launches outside omnigate. The
// log lives under LocalLow, independent of installDir (ignored here).
func (p *Provider) LastPlayedFiles(gid core.GameID, _ string) []string {
	sub, ok := localLowProduct[gid]
	if !ok {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "AppData", "LocalLow", sub)
	return []string{
		filepath.Join(dir, "output_log.txt"),
		filepath.Join(dir, "Player.log"),
	}
}
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/hoyoverse/ -run TestLastPlayedFiles -v`
Expected: PASS（兩個測試）。

- [ ] **Step 5: 整套 provider 測試不破**

Run: `go test ./internal/providers/hoyoverse/`
Expected: ok（既有測試全綠）。

- [ ] **Step 6: Commit**（過審查 gate 後才執行）

```bash
git add internal/providers/hoyoverse/lastplayed.go internal/providers/hoyoverse/lastplayed_test.go
git commit -m "feat(hoyoverse): LastPlayedFiles probe (LocalLow Unity player log)"
```

---

### Task 3: hypergryph `LastPlayedFiles`

**Files:**
- Create: `internal/providers/hypergryph/lastplayed.go`
- Test: `internal/providers/hypergryph/lastplayed_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/hypergryph/lastplayed_test.go`：

```go
package hypergryph

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLastPlayedFiles_Hypergryph(t *testing.T) {
	p := &Provider{}
	files := p.LastPlayedFiles("hypergryph/endfield", "")
	if len(files) != 2 {
		t.Fatalf("want 2 candidates, got %d: %v", len(files), files)
	}
	want := filepath.Join("AppData", "LocalLow", "Gryphline", "Endfield", "Player.log")
	if !strings.HasSuffix(files[0], want) {
		t.Errorf("candidate[0]=%q want suffix %q", files[0], want)
	}
}

func TestLastPlayedFiles_Hypergryph_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("hypergryph/unknown", ""); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/hypergryph/ -run TestLastPlayedFiles -v`
Expected: 編譯失敗（`p.LastPlayedFiles undefined`）。

- [ ] **Step 3: 寫最小實作**

Create `internal/providers/hypergryph/lastplayed.go`：

```go
package hypergryph

import (
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

// LastPlayedFiles implements core.LastPlayedProbe. Endfield (Unity) writes its
// player log under %USERPROFILE%\AppData\LocalLow\Gryphline\Endfield, rewritten
// each launch (Player-prev.log confirms rotation). Independent of installDir.
func (p *Provider) LastPlayedFiles(gid core.GameID, _ string) []string {
	if findByID(gid) == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "AppData", "LocalLow", "Gryphline", "Endfield")
	return []string{
		filepath.Join(dir, "Player.log"),
		filepath.Join(dir, "output_log.txt"),
	}
}
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/hypergryph/ -run TestLastPlayedFiles -v`
Expected: PASS。

- [ ] **Step 5: 整套 provider 測試不破**

Run: `go test ./internal/providers/hypergryph/`
Expected: ok。

- [ ] **Step 6: Commit**（過審查 gate 後才執行）

```bash
git add internal/providers/hypergryph/lastplayed.go internal/providers/hypergryph/lastplayed_test.go
git commit -m "feat(hypergryph): LastPlayedFiles probe (LocalLow Endfield player log)"
```

---

### Task 4: kurogames `LastPlayedFiles`

**Files:**
- Create: `internal/providers/kurogames/lastplayed.go`
- Test: `internal/providers/kurogames/lastplayed_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/kurogames/lastplayed_test.go`：

```go
package kurogames

import (
	"path/filepath"
	"testing"
)

func TestLastPlayedFiles_Kurogames(t *testing.T) {
	p := &Provider{}
	dir := filepath.Join("C:\\", "Games", "Wuthering Waves Game")
	files := p.LastPlayedFiles("kurogames/wutheringwaves", dir)
	want := filepath.Join(dir, "Client", "Saved", "Logs", "Client.log")
	if len(files) != 1 || files[0] != want {
		t.Fatalf("got %v, want [%s]", files, want)
	}
}

func TestLastPlayedFiles_Kurogames_NoInstallDir(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("kurogames/wutheringwaves", ""); got != nil {
		t.Errorf("empty installDir: want nil, got %v", got)
	}
}

func TestLastPlayedFiles_Kurogames_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("kurogames/unknown", `C:\Games\X`); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/kurogames/ -run TestLastPlayedFiles -v`
Expected: 編譯失敗（`p.LastPlayedFiles undefined`）。

- [ ] **Step 3: 寫最小實作**

Create `internal/providers/kurogames/lastplayed.go`：

```go
package kurogames

import (
	"path/filepath"

	"omnigate/internal/core"
)

// LastPlayedFiles implements core.LastPlayedProbe. Wuthering Waves (Unreal 4)
// writes its client log to <installDir>\Client\Saved\Logs\Client.log, truncated
// on each launch. installDir is a.resolved[gid].Path == the GAME folder
// (<launcherRoot>\Wuthering Waves Game), so Client\… joins directly. An empty
// installDir (unresolved) yields no signal.
func (p *Provider) LastPlayedFiles(gid core.GameID, installDir string) []string {
	if findByID(gid) == nil || installDir == "" {
		return nil
	}
	return []string{filepath.Join(installDir, "Client", "Saved", "Logs", "Client.log")}
}
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/kurogames/ -run TestLastPlayedFiles -v`
Expected: PASS（三個測試）。

- [ ] **Step 5: 整套 provider 測試不破**

Run: `go test ./internal/providers/kurogames/`
Expected: ok。

- [ ] **Step 6: Commit**（過審查 gate 後才執行）

```bash
git add internal/providers/kurogames/lastplayed.go internal/providers/kurogames/lastplayed_test.go
git commit -m "feat(kurogames): LastPlayedFiles probe (UE4 Client.log in install dir)"
```

---

### Task 5: app 整合 — `statModTime` seam + `lastPlayedLocked` + `gameRowLocked` 改簽名

**Files:**
- Modify: `internal/app/app.go`（import `os`；新增 `statModTime` var 與 `lastPlayedLocked`；`gameRowLocked` 改簽名；更新兩呼叫點 app.go:298、app.go:396）
- Test: `internal/app/app_test.go`（新增 max-邏輯測試）

- [ ] **Step 1: 寫 failing test**

> **先補測試檔 import**：`internal/app/app_test.go` 現有 import 為 `context, errors, log/slog, path/filepath, testing, omnigate/internal/core`。下方測試新增用到 `os`（`os.WriteFile`/`os.Chtimes`）與 `time`（`time.Now`/`time.Time`/`time.Hour`/`time.Second`），需在 import 區塊補上 `"os"` 與 `"time"`（依字母序：`os` 放在 `log/slog` 後、`path/filepath` 前；`time` 放在 `testing` 後）。`path/filepath` 與 `core` 已 import、勿重複。

在 `internal/app/app_test.go` 檔尾新增（測試直接驗證 `lastPlayedLocked` 的 max 行為，含 fakeProvider 實作 `LastPlayedProbe`）：

```go
// fakeProbeProvider embeds fakeProvider and adds a LastPlayedProbe returning a
// fixed file list, so we can drive lastPlayedLocked's stat/max logic.
type fakeProbeProvider struct {
	fakeProvider
	files []string
}

func (f *fakeProbeProvider) LastPlayedFiles(_ core.GameID, _ string) []string {
	return f.files
}

func writeFileWithMtime(t *testing.T, path string, mt time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func TestLastPlayedLocked_FileNewerThanPlaystate(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	// playstate older than the file:
	a.playState.last["g/x"] = fileMt.Add(-24 * time.Hour)

	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(fileMt) {
		t.Errorf("want file mtime %v, got %v", fileMt, got)
	}
}

func TestLastPlayedLocked_PlaystateNewerThanFile(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v, got %v", psMt, got)
	}
}

func TestLastPlayedLocked_NoProbeInterface(t *testing.T) {
	dir := t.TempDir()
	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	// plain fakeProvider does NOT implement LastPlayedProbe → playstate only.
	got := a.lastPlayedLocked(&fakeProvider{}, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v, got %v", psMt, got)
	}
}

func TestLastPlayedLocked_MissingFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	missing := filepath.Join(dir, "does-not-exist.txt")
	p := &fakeProbeProvider{files: []string{missing}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v (missing file ignored), got %v", psMt, got)
	}
}

func TestLastPlayedLocked_NilPlayState(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{} // playState nil — must not panic
	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(fileMt) {
		t.Errorf("want file mtime %v, got %v", fileMt, got)
	}
}
```

> 註：本測試假設既有 `fakeProvider`（app_test.go:14）可被嵌入。若 `fakeProvider` 的 `Games()`/`ID()` 等方法不足以滿足 `core.Provider`，`lastPlayedLocked` 只取用 `LastPlayedProbe` type-assert，不呼叫其他方法，故嵌入即可；`fakeProbeProvider` 仍滿足 `core.Provider`（繼承自 `fakeProvider`）。直接傳 `&fakeProvider{}` 給 `lastPlayedLocked` 亦只測 type-assert 失敗路徑。

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/app/ -run TestLastPlayedLocked -v`
Expected: 編譯失敗（`a.lastPlayedLocked undefined`、`statModTime` 未用到此處但下一步加）。

- [ ] **Step 3: 加 `os` import**

`internal/app/app.go` 的 import 區塊加入 `"os"`（與既有 `"path/filepath"` 等並列，依字母序放在 `"log/slog"` 之後、`"path/filepath"` 之前）：

```go
	"log/slog"
	"os"
	"path/filepath"
```

- [ ] **Step 4: 加 `statModTime` seam 與 `lastPlayedLocked`**

在 `internal/app/app.go` 的 `gameRowLocked` 函式**之前**（約 line 303、`// gameRowLocked builds...` 註解之上）插入：

```go
// statModTime returns a path's mtime, or (zero,false) if it cannot be stat'd.
// Package var so tests can stub it (mirrors the osTempDir/osRemoveAll seams).
var statModTime = func(p string) (time.Time, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// lastPlayedLocked returns the effective last-played time for gid: the later of
// the recorded playstate timestamp and the mtime of any LastPlayedProbe file
// (which reflects play outside omnigate). Caller holds settingsMu (R or W) —
// same lock discipline as gameRowLocked. playState may be nil (test helpers).
func (a *App) lastPlayedLocked(p core.Provider, gid core.GameID, installDir string) time.Time {
	var ts time.Time
	if a.playState != nil {
		ts = a.playState.Get(string(gid))
	}
	if probe, ok := p.(core.LastPlayedProbe); ok {
		for _, f := range probe.LastPlayedFiles(gid, installDir) {
			if mt, ok := statModTime(f); ok && mt.After(ts) {
				ts = mt
			}
		}
	}
	return ts
}
```

- [ ] **Step 5: `gameRowLocked` 改簽名並改用 `lastPlayedLocked`**

把 `func (a *App) gameRowLocked(g core.GameDescriptor) GameRow {` 改為：

```go
func (a *App) gameRowLocked(p core.Provider, g core.GameDescriptor) GameRow {
```

並把函式內現有的 playState 區塊：

```go
	if a.playState != nil {
		if ts := a.playState.Get(string(g.ID)); !ts.IsZero() {
			row.LastPlayed = ts.Format(time.RFC3339)
		}
	}
```

換成：

```go
	if ts := a.lastPlayedLocked(p, g.ID, e.Path); !ts.IsZero() {
		row.LastPlayed = ts.Format(time.RFC3339)
	}
```

（`e := a.resolved[g.ID]` 已在函式頂端宣告，`e.Path` 即 installDir。）

- [ ] **Step 6: 更新兩個呼叫點**

`ListGames`（app.go 約 298）：
```go
			out = append(out, a.gameRowLocked(p, g))
```

`gameRow`（app.go 約 396）：
```go
			return a.gameRowLocked(p, g), nil
```

- [ ] **Step 7: 跑新測試確認 pass**

Run: `go test ./internal/app/ -run TestLastPlayedLocked -v`
Expected: PASS（6 個測試）。

- [ ] **Step 8: 整套 app 測試不破（含既有 `TestLaunch_RecordsLastPlayed`）**

Run: `go test ./internal/app/`
Expected: ok（既有 playstate/Launch/gameRow 測試全綠）。

- [ ] **Step 9: 全 repo 編譯 + 測試**

Run: `go build ./... ; go test ./...`
Expected: 全綠（drop `-race`，本機 CGO_ENABLED=0）。

- [ ] **Step 10: Commit**（過審查 gate 後才執行）

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): max last-played with LastPlayedProbe runtime-file mtime"
```

---

## 收尾（Task 5 後，USER-gated）

1. **前端零改動驗證**：`cd frontend && npm run test`（vitest 應全綠、無新增）+ `npm run build` 綠。
2. **`wails build`**：產生 `build/bin/omnigate.exe`。
3. **真機 smoke（USER）**：
   - 用官方啟動器（非 omnigate）玩一款 → 關閉 → 開 omnigate → 該遊戲「上次遊玩」反映剛才時間（取自 log mtime）。
   - 透過 omnigate 啟動另一款 → 「上次遊玩」即時更新（樂觀更新，現況不變）。
   - 無 log／未實作探測的情況 → 退回 playstate-only、不報錯。
4. smoke 通過後：`git checkout dev && git merge --no-ff last-played-p2`（依 memory `feedback_commits.md`：無 `Co-Authored-By`、`--no-ff`）。

## 風險與注意

- `gameRowLocked` 改簽名牽動兩呼叫點（app.go:298、:396）——必須同步改，否則編譯失敗（這是好事，編譯器把關）。
- stat 在 `settingsMu` 下執行：每遊戲 ≤2 候選、stat 極廉，與現況 `statDir` 同層、無效能／鎖序疑慮（`playState.Get` 取最內層 `playState.mu`，順序 `settingsMu → playStateMu` 不變）。
- 已知脆弱：依賴各引擎「player log 每啟動改寫」，遊戲改版改動 log 路徑／檔名即失準，最差退回 playstate-only。
- 非目標：playtime 時長、進程監看、focus 即時重探、CN 版資料夾、SQLite。
