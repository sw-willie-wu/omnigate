# omnigate — P2 last-played 強化設計（反映 omnigate 外的遊玩）

> 日期：2026-06-03 · 狀態：設計（待 plan）
> 範圍：P2 的兩塊獨立子系統之一。本 spec 只涵蓋 **last-played 強化**；NewsPanel + 活動倒數 pill 屬 P2 的另一塊，獨立 spec→plan→實作，不在此。

## Context（為什麼做這個）

P1 已 ship 的 `internal/app/playstate.go` 把「上次遊玩」記成 per-game timestamp（`map[gameID]time.Time`），但 `playState.Record(gid)` **只在使用者透過 omnigate 成功啟動遊戲時**寫入（`App.Launch` 成功路徑）。因此若使用者改用官方啟動器或直接雙擊 exe 玩，omnigate 的「上次遊玩」不會更新，顯示會偏舊、與事實不符。

P2 的目標（使用者 2026-06-03 要求併入）：**讓「上次遊玩」也反映在 omnigate 之外的遊玩**。做法是探測每款遊戲「每次啟動會更新、但 patch／閒置不會動」的 runtime 檔案的 mtime，最終顯示值取 `max(playstate 時間, runtime 檔 mtime)`。

預期成果：開 omnigate 時，即使上次是用官方啟動器玩的，「上次遊玩」也能顯示正確時間；純後端變更，前端零改動。

## 使用者已拍板的範圍決策

- **只做「上次遊玩時間」單一 timestamp**：不做遊玩時長 playtime、不做進程監看（那是 Collapse 式自記，且不反映 omnigate 外啟動，與本目標相反）。
- **架構 = per-provider 可選探測介面（方案 A）**：探測知識（哪個檔算數）放在各 provider，契合既有 `InstallLocator`/`ProcessChecker`/`AssetServer`/`bg.go` 的「provider 擁有各家專屬知識」格局。否決把知識集中到 app 層的方案 B、以及讓 provider 自己做 IO 的方案 C。
- **App 傳已解析 installDir 進介面**：provider 當「純路徑建構器」，HoYoverse／Endfield 忽略 installDir、鳴潮用它。
- **純加法、優雅降級**：provider 未實作介面、或檔案不存在 → 行為等同現狀（只看 playstate）。
- **前端不動**：max 發生在後端 `gameRowLocked`，前端 `last_played` 既有消費與格式化（`utils/lastPlayed.ts`）、Launch 樂觀更新全部不變。

## 本機已驗證的 per-game runtime 檔

下表為本機（5 款皆安裝、game-paths 階段已 live-verified）實際翻查所得。這是**本機檔案系統檢視**、非 server 探測，符合 memory `feedback_collapse_reference.md` 規範。

> **key = 真正的 `core.GameID` 常數**（探測介面收到的就是這個），**不是 HoYoverse API 的 biz code**。biz 只是內部 API plumbing，與本探測無關，僅附註供辨識。

| 遊戲 | `core.GameID`（probe 收到的 key） | biz（僅附註） | runtime 檔（mtime 每次啟動更新） | 來源位置 | 引擎 |
|---|---|---|---|---|---|
| 原神 Genshin | `hoyoverse/genshin` | hk4e_global | `%USERPROFILE%\AppData\LocalLow\miHoYo\Genshin Impact\output_log.txt` | LocalLow | Unity |
| 星穹鐵道 HSR | `hoyoverse/starrail` | hkrpg_global | `%USERPROFILE%\AppData\LocalLow\Cognosphere\Star Rail\output_log.txt` | LocalLow | Unity |
| 絕區零 ZZZ | `hoyoverse/zzz` | nap_global | `%USERPROFILE%\AppData\LocalLow\miHoYo\ZenlessZoneZero\output_log.txt` | LocalLow | Unity |
| 鳴潮 WuWa | `kurogames/wutheringwaves` | — | `<installDir>\Client\Saved\Logs\Client.log` | 安裝目錄 | Unreal 4 |
| Endfield | `hypergryph/endfield` | — | `%USERPROFILE%\AppData\LocalLow\Gryphline\Endfield\Player.log`（有 `Player-prev.log` 佐證每啟動輪替） | LocalLow | Unity |

> gameID 常數來源：`hoyoverse/{genshin,starrail,zzz}`（`internal/providers/hoyoverse/meta.go:43,50,55`）、`kurogames/wutheringwaves`（`internal/providers/kurogames/meta.go:20`）、`hypergryph/endfield`（`internal/providers/hypergryph/meta.go:23`）。

**關鍵發現**：HoYoverse 三款的 LocalLow publisher 資料夾名**不一致**——Genshin/ZZZ 在 `miHoYo\`、HSR 在 `Cognosphere\`。不能硬編單一 publisher，須 per-gid 各列一條。檔名亦不一致：HoYoverse 用 `output_log.txt`、Endfield 用 `Player.log`、鳴潮 UE4 用 `Client\Saved\Logs\Client.log`。HoYoverse probe **以 `g.ID`（`hoyoverse/genshin` 等）為 switch key、不是 biz**（biz 會 map 不到、探測永遠落空）。

> 上表資料夾名為 **global 版**。CN 版 publisher/product 名不同，但 omnigate 只支援 global，非目標。

## 設計

### A. 核心可選介面 — `internal/core`

新增與既有可選 Provider 能力同格的介面（建議放 `internal/core/provider.go` 或新檔，依既有 optional-iface 擺放慣例）：

```go
// LastPlayedProbe is an optional Provider capability. Given a game and its
// resolved install dir, it returns filesystem paths whose mtime indicates the
// game was launched — including launches outside omnigate. The App stats each
// path and takes the most recent mtime. An empty/nil return = no extra signal
// (the App falls back to the recorded playstate timestamp). Implementations must
// be pure path construction (no filesystem IO, no errors): non-existent paths
// are filtered by the App's stat step.
type LastPlayedProbe interface {
    LastPlayedFiles(gid GameID, installDir string) []string
}
```

- App 傳 `installDir`（已解析安裝路徑，可能為空字串＝未解析）。provider 不需自存安裝路徑。
- 回傳順序不重要：App 對全部候選 stat 取最大 mtime。
- 介面為**可選**：providers 透過 Go interface type-assert 偵測，未實作者自然降級。

### B. App 取 max — `internal/app/app.go`

**新增可注入的 stat seam**（套件層 `var`，供測試替換——沿用本套件既有的 var-seam 先例 `osTempDir`/`osRemoveAll`，見 `update_handler_windows.go:14-15` / `update_handler_other.go:7-8`；注意 `statDir` 是普通 `func`、非 seam）：

```go
// statModTime returns a path's mtime, or (zero,false) if it cannot be stat'd.
// Package var so tests can stub it. Default: real os.Stat.
var statModTime = func(p string) (time.Time, bool) {
    fi, err := os.Stat(p)
    if err != nil {
        return time.Time{}, false
    }
    return fi.ModTime(), true
}
```

**新增取 max helper**：

```go
// lastPlayedLocked returns the effective last-played time for gid: the later of
// the recorded playstate timestamp and the mtime of any LastPlayedProbe file.
// Caller holds settingsMu (read or write) — same lock discipline as gameRowLocked.
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

**`gameRowLocked` 改造**：現簽名 `gameRowLocked(g core.GameDescriptor) GameRow` 無 provider 在手，但要呼叫 provider 的 probe，需 provider。改為 `gameRowLocked(p core.Provider, g core.GameDescriptor) GameRow`：

- 呼叫點 1：`ListGames`（app.go:296-300）迴圈 `for _, p := range a.providers { for _, g := range p.Games() { ... gameRowLocked(p, g) } }` — `p` 已在 scope、非 nil（map 值）。
- 呼叫點 2：`gameRow(gid, p)`（app.go:391-396，`SetGameOverride`/`ClearGameOverride`/`RefreshGame` 用）— 內部 app.go:396 呼叫 `gameRowLocked(g)`，改為傳入 `p`。`p` 來自 `a.provider(gid)`，三個呼叫者皆於該 error 早退，故進到 `gameRow` 時 `p` 必非 nil；且 `p.(core.LastPlayedProbe)` type-assert 本就 nil-safe。grep `gameRowLocked` 僅此兩處呼叫點，無遺漏。

`gameRowLocked` 內把現有的：
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
（`e := a.resolved[g.ID]` 已在函式內、`e.Path` 即 installDir。`lastPlayedLocked` 內已 nil-guard `playState`，保留既有測試 helper 不初始化 `playState` 也不 panic 的特性。）

**鎖序**：stat 在既有 `settingsMu.RLock`／`Lock` 下執行，與現況 `statDir(e.Path)`（app.go:317）同層、行為一致。`playState.Get` 內部取 `playStateMu`（最內層鎖），順序 `settingsMu → playStateMu` 不變，無新交叉。stat 是純檔案系統呼叫、不取任何鎖。

### C. Provider 實作（具體路徑表）

各 provider 新增 `LastPlayedFiles` 方法（平台無關，路徑純字串組裝）。LocalLow base 用 `os.UserHomeDir()` + `AppData/LocalLow`（`os.UserHomeDir` 在 Windows 回 `%USERPROFILE%`）。`UserHomeDir` 失敗（理論上不會）→ 回 nil。

- **hoyoverse**（`internal/providers/hoyoverse`）：以 **`g.ID`（`core.GameID`）為 switch key**（透過既有 `findByID`／`gameMeta.ID`，**不是 biz**），map 到 LocalLow 下的 product 子路徑：
  - `hoyoverse/genshin` → `miHoYo/Genshin Impact`
  - `hoyoverse/starrail` → `Cognosphere/Star Rail`
  - `hoyoverse/zzz` → `miHoYo/ZenlessZoneZero`
  每款回 **兩個候選**：`<sub>/output_log.txt` 與 `<sub>/Player.log`（對未來 Unity 由 `output_log.txt` 改名 `Player.log` 留韌性）。未知 gid → 回 nil。
- **hypergryph**（`internal/providers/hypergryph`，gid `hypergryph/endfield`）：`Gryphline/Endfield` 下 `Player.log` 與 `output_log.txt` 兩候選。
- **kurogames**（`internal/providers/kurogames`，gid `kurogames/wutheringwaves`）：`installDir == "" → nil`；否則回 `filepath.Join(installDir, "Client", "Saved", "Logs", "Client.log")` 單一候選。
  > **installDir 層級（必釘死）**：App 傳入的 `installDir == a.resolved[gid].Path`，而 kurogames detect（`detect.go:54,65`）已將其解析為 `filepath.Join(launcherRoot, FolderName)` = `<launcherRoot>\Wuthering Waves Game`（**遊戲資料夾**，非 launcher root）。故 `Client\Saved\Logs\Client.log` 直接 join 在此資料夾下、不可再往上一層。

> 確切 gameID 常數已於上表與本節列出（對照 `meta.go`）；plan 階段沿用既有 `findByID` 等 helper。

### D. 行為與邊界

- **刷新時機**：`LastPlayed` 值在 `ListGames` 被呼叫時重算（boot 的 `games.load()`、Topbar 手動 Refresh）。目標情境「omnigate 關著時用官方啟動器玩」→ 下次開 omnigate boot 即正確反映。omnigate 開著時於外部啟動的遊戲，要到下次 load/Refresh 才更新——**可接受**，不加 window-focus 自動重探（避免 scope creep；列為非目標）。
- **max 語意**：候選檔不存在 → `statModTime` 回 false、跳過；檔 mtime 比 playstate 舊 → playstate 勝；皆為零 → `LastPlayed` 空（`omitempty`）。
- **不 gate Installed**：即使某遊戲現未被視為 installed，只要其 log 存在就照算 mtime——有 log 即代表當時確實玩過，語意正確且實作更簡單。
- **已知脆弱**（驗收與維護須知）：依賴各引擎「player log 每啟動改寫」的行為，遊戲改版若改動 log 路徑／檔名／改寫時機，本探測即失準——屬可接受的漸進降級（最差退回 playstate-only），隨遊戲改版維護。

### E. 前端

**無變更**。`GameRow.last_played` 既有 JSON 欄位、前端型別、`utils/lastPlayed.ts` 格式化、Launch 成功後的樂觀欄位賦值全部不動。本功能僅改變後端回填的值來源。

### F. 測試（`go test ./...`，drop `-race`，本機 CGO_ENABLED=0）

- **provider 單元**（各 provider 套件）：
  - hoyoverse：三個 gid 各回含正確 product 子路徑的候選（用 `strings.HasSuffix` 斷言 `miHoYo/Genshin Impact/output_log.txt` 等，避開 `UserHomeDir` 的 env 耦合；同時斷言含 `Player.log` 候選）；未知 gid → nil。
  - hypergryph：回含 `Gryphline/Endfield/Player.log` 的候選。
  - kurogames：給定 installDir → 回 `…/Client/Saved/Logs/Client.log`（用 `filepath.Join` 比對）；installDir 空 → nil。
- **app 單元**（`internal/app`）：
  - 以 `fakeProvider` 實作 `LastPlayedProbe`，回指向 `t.TempDir()` 內、以 `os.Chtimes` 設好 mtime 的檔。
  - 案例：(1) 檔 mtime 比 playstate **新** → `LastPlayed` 採檔 mtime；(2) 檔 mtime 比 playstate **舊** → 採 playstate；(3) provider **未**實作 `LastPlayedProbe` → 等同現況（只 playstate）；(4) 候選檔不存在 → 退回 playstate。
  - 可直接測 `lastPlayedLocked`，或經 `gameRowLocked` 觀察 `LastPlayed` 字串。沿用既有 `newAppForTest`/`buildAppWithResolved` 模式。
- **既有測試不可破**：`gameRowLocked` 改簽名後，所有現有呼叫點與測試需同步更新；`TestLaunch_RecordsLastPlayed`（app_test.go:398）等 playstate 既有測試維持綠。

### G. 非目標（明確排除）

- 遊玩時長 playtime、進程監看自記。
- window-focus／定時自動重探（只在 ListGames 時重算）。
- CN 版 LocalLow 資料夾。
- SQLite 遷移（見 memory `future-sqlite-for-cross-game-state-and-gacha`，未來統一處理）。
- NewsPanel + 活動倒數 pill（P2 另一塊，獨立 spec）。

## 起點與分支衛生

目前在 `dev`（乾淨）。依 memory `feedback_commits.md` 慣例（main + feature branch + `merge --no-ff`、無 `Co-Authored-By`），已從 `dev` 開 feature 分支 `last-played-p2`。spec／plan 提交於此分支；完成後 `--no-ff` 併回 `dev`。

## 風險與注意

- `gameRowLocked` 改簽名牽動所有呼叫點與測試——plan 階段須逐一列舉呼叫點，確保編譯與既有測試同步更新。
- stat 在 `settingsMu` 下執行：候選檔最多 2 個／遊戲、stat 極廉，與現況 `statDir` 同層，無效能疑慮。
- `os.UserHomeDir` 為純函式、無副作用；失敗回 nil 候選（降級）。
- max 取 `.After`：任何真實 mtime 皆 `After` 零時 → 正確覆蓋；皆零 → 空字串。

## 驗證方式

1. `go test ./...`（drop `-race`）— provider 探測單元 + app max 邏輯 + 既有 playstate/Launch 測試全綠。
2. 前端 `npm run test`（vitest）— 應**無新增**，既有測試維持綠（確認前端零改動未破壞）。
3. `wails dev` 實機：
   - 用官方啟動器（非 omnigate）玩一款遊戲 → 關閉 → 開 omnigate → 該遊戲「上次遊玩」反映剛才時間（取自 log mtime）。
   - 透過 omnigate 啟動另一款 → 「上次遊玩」即時更新（樂觀更新，現況行為不變）。
   - 未實作探測或無 log 的情況 → 退回 playstate-only，不報錯、不空白異常。
