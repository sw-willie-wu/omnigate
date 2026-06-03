# omnigate — 主畫面 P1 重構設計

> 日期：2026-06-03 · 狀態：設計（待 plan）
> 範圍：P1 純前端重構 + 一個小 Go endpoint。延伸的 NewsPanel／帳號／抽卡分析屬 P2/P3，不在本 spec。

## Context（為什麼做這個）

主畫面 detail view 目前的 `DetailView.vue` 是空殼（`<div class="view view-detail"></div>`），實際只靠背景 `BgLayer` 顯示主視覺，加上浮在上面的 `BottomBar` 動作列。設計稿（`omnigate Main - Prompt.md` + standalone HTML 的 `LauncherScreen` 定稿元件）描繪了一個更完整的主畫面：常駐導覽列（總覽／抽卡分析頁籤）、主視覺 HeroBase、最新情報面板、底部 HomeActionBar。

本次先做能純前端落地的部分（P1），把空的主視覺補成有結構的版面，並把動作列重構成設計稿的 HomeActionBar 樣式。需要資料的元素（情報、活動倒數、帳號、開拓力、抽卡分析）一律延後，且未來凡需資料一律走新增 Go endpoint，不從前端直接 fetch。

預期成果：主畫面 detail view 有一條 NavStrip（總覽 active／抽卡分析 disabled）、主視覺底部有重構後的 HomeActionBar（狀態 + 版本 + 上次遊玩 + 中性玻璃設定齒輪 + 金邊開始遊戲 CTA），配色對齊設計稿。

## 使用者已拍板的範圍決策

- **範圍 = P1 純前端重構**（NewsPanel/帳號/抽卡分析 = P2/P3）。
- **主視覺不疊遊戲名稱**：略過設計稿的 Title（英文副標 + 大標題）。
- **需要資料一律開新的 Go endpoint（Wails binding）**，不前端直接 fetch。
- **配色換成設計稿新值**，採「保留現有變數名只換值」策略。
- **NavStrip 加殼**：總覽 active，抽卡分析 disabled；右側帳號晶片不放。
- **「上次遊玩」要做**，用獨立 `playstate.json`（不塞進 settings.toml）。
- **Follow-up**：playstate 等狀態未來統一遷 SQLite（見 memory `future-sqlite-for-cross-game-state-and-gacha`）。

## 設計

### A. 設計 token 配色遷移 — `frontend/src/styles/theme.css` `:root`

策略：**保留現有變數名、只換值**，讓 GridCard / Sidebar / 既有 pill 自動跟著變色，不必逐檔改 class。

| 變數 | 現值 | 新值 | 說明 |
|---|---|---|---|
| `--ok` | `#8bc472` | `#74d68a` | ready 綠（設計稿 `--ready`） |
| `--info` | `#7aabd6` | `#6fa8ff` | update/predl 藍（設計稿 `--update`） |
| `--warn` | `#e89d5a` | `#e8b865` | 對齊設計稿 |

新增（給新元件用，不影響舊樣式）：
```
--gold-hi #f4dd9b · --gold-deep #b88f3c · --gold-glow rgba(230,197,115,0.28) · --gold-soft rgba(230,197,115,0.12)
--ready-soft rgba(116,214,138,0.14) · --hot #ff6f6f · --tx-dim #4a5060
```
> `--accent`（金）維持現值。

**必須一併更新的硬編碼 border rgba（非選用）**：現有 pill/card 的邊框色寫死成舊色相，換值後會出現「新綠文字 + 舊綠邊框」的可見色相不一致。必須同步更新以下字面值（`theme.css`）：
- 舊綠 `rgba(139,196,114,0.4)`（約 line 320 hero pill.ok、348 grid-card ready）→ 對齊新綠 `rgba(116,214,138,...)`
- 舊藍 `rgba(122,171,214,0.5)`（約 line 319 pill.info、350 grid-card predownload）→ 對齊新藍 `rgba(111,168,255,...)`
- 舊橘 `rgba(232,157,90,0.5)`（約 line 318 pill.warn、349 grid-card update）→ 對齊 `rgba(232,184,101,...)`

（行號以審查當下為準，實作時以實際比對為主。）

### B. 殼層與導覽 — `stores/view.ts` + 新 `components/NavStrip.vue` + `App.vue`

- `view.ts`：detail view 內新增分頁狀態 `homeTab: 'overview' | 'gacha'`（預設 `overview`），加 `setHomeTab(t)` action。`gacha` 暫不可選。
- 新 `NavStrip.vue`（h52）：
  - 兩個頁籤 `總覽`（active：金字 + 金 soft 底 + 金邊）/ `抽卡分析`（**disabled**：灰字、`cursor: not-allowed`、附 trend 圖示與「即將推出」title）。disabled 頁籤**不綁任何 click handler**，`setHomeTab('gacha')` 不接線，確保完全 inert。
  - 右側帳號晶片區**留空**（P3）。
  - 樣式對齊設計稿 NavStrip（底線 `--line-1`、漸層底）。
- `App.vue` 版面整合（解決 NavStrip 與絕對定位 BottomBar 的疊放）：
  - NavStrip **只在 `viewMode === 'detail'` 時 render**，grid/settings 模式不出現。
  - 目前 `.main` 有 padding（約 `36px 56px 24px`），`.bottom-bar` 為 `position:absolute; bottom:24px`。NavStrip 為 detail 內容區頂端的**常態流（非絕對定位）h52 列**，置於 `.main` padding 之內、HeroBase 之上；HeroBase 佔滿剩餘高度，BottomBar 仍絕對定位於 HeroBase 底部，兩者不重疊。實作時確認 NavStrip 不被 `.main` 的 top padding 推擠、也不蓋住 BottomBar。
  - **無選取遊戲時**（boot 前 `games.selected` 為 undefined、或遊戲清單為空）：NavStrip 照常顯示（頁籤是全域導覽，與選取無關）；HeroBase 顯示空背景；BottomBar 沿用既有 `v-if="games.selected"` → 自動不顯示。
  - **側欄收合**（`sidebarCollapsed`）時 NavStrip/HeroBase 隨 `.main` 寬度自適應，無特殊處理。

### C. 主視覺 HeroBase — `components/DetailView.vue`（填實）

- 背景沿用 `BgLayer`（不動）。DetailView 自身提供 HeroBase 的疊層 scrim（上下暗角，保證可讀）。
- **左上：不放任何標題/副標**（依使用者指示）。活動倒數 pill 延後到 P2 → P1 左上留空。
- **右上：NewsPanel 延後到 P2** → P1 不放。
- **底部：HomeActionBar**（即重構後的 BottomBar，見 D）。
- DetailView 與 BottomBar 的職責邊界維持現狀（BottomBar 仍是獨立元件、絕對定位於 main 區底部），只是視覺上成為 HeroBase 的底部列。

### D. HomeActionBar — 重構 `components/BottomBar.vue`

**保留所有現有更新/預下載/CTA 狀態邏輯**（`pillClass`、`availableUpdate`、`availablePredl`、`predlReady`、`inFlight` 進度條、`GameConfigPopover`、error line、verifying、cancel-X 等 computed 與 `v-if` 分支一律 byte-identical 不動）。

> 釐清：這**不是純 CSS**。左下 meta 改成直向兩行需要新增 DOM — 把現有 `.hero-stats-line`（pill+版本）包進一個新的 flex-column 容器，並新增第二行（上次遊玩）的 markup。允許新增節點/容器與調整 class，但**不得改動上述狀態判斷邏輯**，靠既有 + 新增前端測試守住。

- **左下 meta 區**（直向兩行）：
  - 第一行：狀態 pill（就緒綠／可更新／可預下載，沿用現有 `pillLabel`/`pillClass`）+ 版本號 mono。
  - 第二行：`⏱ 上次遊玩 · <格式化時間>`；`last_played` 為空時顯示「尚未遊玩」。
- **右下動作群**：
  - `⚙ 設定齒輪` = 現有 `GameConfigPopover`，改**中性玻璃 56×56**（`rgba(20,22,28,0.72)` + `--line-2` 邊、`--tx-mid` 字），**hover 才透金**。注意現有 `.game-config-btn` 是 40×40 且套用 `.icon-btn`（hover 由 `--text-2`→`--text`），restyle 需**覆寫 `.icon-btn` 的尺寸與 hover 顏色**；popover 內部（`theme.css` 約 389–445）不動。
  - `▶ 開始遊戲` = 金邊發光 CTA（沿用現有 launch/update/apply-predl/progress 狀態切換），套用設計稿 CTA-B 樣式（高 56、圓角 14、金漸層底 + `--gold-glow` 陰影）。
- **主從原則**：齒輪中性、只有 CTA 金光，兩顆共用同一套玻璃語言（圓角 14 / 毛玻璃 / 高 56）。
- 既有的 error line、verifying、cancel-X 等行為原樣保留。

### E. 「上次遊玩」Go endpoint — 新 `internal/app/playstate.go`

與 `settings.toml` 分離的獨立 atomic JSON state 檔，存放**持久使用者狀態**（非執行期暫存）。

> 修正既有分層認知：predl_ready.json / progress.json / update_state 是放在 `<TEMP>/omnigate/...` 的**暫存** sidecar（temp 可被清除），不適合放「上次遊玩」這種要長期保留的狀態。settings.toml 才是持久層，故 playstate 與它同層。

- **檔案位置**：`filepath.Join(filepath.Dir(a.settingsP), "playstate.json")`。
  > 注意：`main.go` 以 `app.New("")` 啟動 → `settingsP` 預設為裸相對檔名 `"settings.toml"`，`filepath.Dir` 得 `"."`（行程 CWD）。因此 playstate.json 目前落在 CWD，與 settings.toml 同處。這是現狀已知行為；未來 settings 路徑改動或遷 SQLite 時一併處理（見 follow-up）。
- **無共用 atomic-write helper**：現有各 provider 各自寫 temp→rename，app 套件沒有共用函式。playstate.go 自行實作 `writeFileAtomic`（write-temp → `os.Rename`）。
- 新檔 `playstate.go`：
  - 型別 `playState`：`map[string]time.Time`（key = gameID）+ **自有 `sync.Mutex`** + 檔案路徑。
  - `loadPlayState(path)`：讀檔；不存在/壞 JSON → 回空 map（容錯，不報致命錯，記 log）。
  - `Record(gid)`：取自身鎖 → 寫入當下時間 → atomic 存檔。
  - `Get(gid)`：取自身鎖 → 回時間或零值。
- **鎖序（必守）**：`playStateMu` 永遠是**最內層鎖**。`gameRowLocked` 在持有 `settingsMu` 時呼叫 `Get` → 順序 `settingsMu → playStateMu`，可接受。**絕不可在持有 `playStateMu` 時去取 `settingsMu`**。`Launch` 不持有 `settingsMu`（只取 per-game update-state RLock），其 `Record` 為 playStateMu-only，無交叉。
- `App`：新增 `playState *playState` 欄位。`New`/`Startup` 載入（路徑由 settingsP 推導）。
- `App.Launch`：現有有**三個** return 點（apply-block 早退、provider-error、成功）。**僅**成功路徑記錄。由於成功路徑現為 `return p.Launch(...)`，需改寫成 `pid, err := p.Launch(a.ctx, gid, ...); if err == nil { a.playState.Record(gid) }; return pid, err`。apply-block 與 provider-error 路徑不記錄。存檔失敗只記 log，不改 `Launch` 的 `(pid, err)` 回傳語意。
- `GameRow` 新增 `LastPlayed string`（RFC3339；空 = 未玩過）。`gameRowLocked` 經 `playState.Get` 填值。
  - **必須 nil-guard**：`gameRowLocked` 是 `ListGames`/`SetGameOverride`/`RefreshGame` 的讀取路徑，而既有測試 helper `newAppForTest` 與 `buildAppWithResolved` 都建 `&App{}` 且**不初始化 `playState`**（多個現有測試經此路徑）。若無條件 `a.playState.Get(...)` 會 nil-panic、打爆 `go test ./...`。故填值必須寫成：`if a.playState != nil { row.LastPlayed = a.playState.Get(g.ID).Format(time.RFC3339) }`（零值 time → 空字串；本欄維持 `omitempty`）。
- **刷新路徑（修正）**：`Launch` 目前**不會**重跑 `ListGames`、不發事件、不觸發前端重載（只回 `(pid, err)`）。後端 `LastPlayed` 只在**下次冷啟動**經 `games.load()` 流到前端。啟動當下的即時反映**只能**靠前端樂觀更新。
- **前端樂觀更新（修正，避免 `_replaceRow` 副作用）**：
  - `stores/games.ts` 的 `GameRow` 型別加 `last_played?: string`。
  - Launch 成功後**直接欄位賦值** `selected.last_played = new Date().toISOString()`（直接 mutate Pinia state 中該 row 的欄位）。
  - **嚴禁**改走 `_replaceRow`/`refreshGame`：`_replaceRow`（games.ts:103-110）會整列 splice 替換並重抓 icon/background 資產，會把 `icon_url`/`background_url`/`background_video` 洗掉。
- **i18n（修正：重用既有孤兒 key + parity 約束）**：
  - 既有 `labels.last_run` / `labels.minutes_ago` / `labels.days_ago`（`frontend/src/locales/en.json` 約 29–32）目前**全前端無人引用**（孤兒 key），可重用：`days_ago`/`minutes_ago` 直接拿來格式化。
  - 需**新增**：`labels.played_today`（今天 `HH:mm`）、`labels.played_yesterday`（昨天 `HH:mm`）、`labels.never_played`（尚未遊玩）、以及行內標籤 `labels.last_played`（「上次遊玩」前綴，可考慮重用 `last_run`）。最終確切 key 名於 plan 階段定案並列舉。
  - **parity 約束**：`i18n_parity.test.ts` 斷言 en / zh-TW / zh-CN 三檔 key 集合完全相同。任何新增 key **必須三檔同步加**，否則該測試 fail。

### F. 測試

- **Go**：
  - `playstate_test.go`：`writeFileAtomic` + load/save round-trip、`Record` 更新時間、檔案不存在與壞 JSON 的容錯（回空 map）。
  - `Launch` 成功後有呼叫 `Record`：注意現有 `newAppForTest` 不會初始化 `playState`，測試需**自行建立指向 `t.TempDir()` 的 playstate 路徑並注入 App**（或讓 `New` 在測試用 settingsP 下自動推導）；以 `fakeProvider`（`Launch` 回 `(0,nil)`）跑成功路徑後斷言 `playState.Get(gid)` 非零。亦測 apply-block 早退路徑**不**記錄。
- **前端**（vitest，沿用既有模式）：
  - `NavStrip`：總覽 active class、抽卡分析 disabled（不可點）。
  - `HomeActionBar`（BottomBar）：ready/update/predl 三狀態 pill 與 CTA 渲染；`last_played` 有值 vs 空（「尚未遊玩」）。
  - last-played 時間格式化函式單元測試（今天/昨天/N 天前/日期 邊界）。

### G. 明確不在 P1（留 P2/P3）

- **P2（需公開端點，屆時開 Go endpoint）**：NewsPanel 最新情報、活動倒數 pill。
- **P3（需帳號系統）**：帳號晶片（暱稱/UID/多帳號切換）、開拓力 180/240、抽卡分析頁（`GachaBoardA`）。

## 起點與分支衛生（實作前先處理）

開工時工作樹**已有未提交變更**，必須先釐清，避免污染 P1 diff：
- `build/appicon.png`、`build/windows/icon.ico`：已認可的深色 app icon 替換。
- `frontend/src/components/Footbar.vue`：已認可的「移除 5 款遊戲」。
- `frontend/src/components/BottomBar.vue` + `styles/theme.css`：已認可的「就緒→綠」(`.pill.ok`)。
- `go.mod`：**非本次意圖** — 是 `wails dev` 跑 `go mod tidy` 的副產物，應 `git checkout -- go.mod` 還原。

處理方式：先把上述**三項已認可 UI 變更**（icon / footer / ready-green）整理成一個 baseline commit，`go.mod` 還原。目前在 `dev` 分支；本專案採 main + feature branch + `merge --no-ff`（見 memory `commit-conventions`），近期 feature 皆 merge 進 `dev`，故 **P1 從 `dev` 開新 feature 分支**進行。注意 P1 會再次重構 BottomBar/theme.css，baseline 的 ready-green 變更會被 P1 的 HomeActionBar 重構吸收。

## 風險與注意

- `view.ts` 的 `ContentView` 擴充要確保 grid/settings 切換、settings 關閉回 HOME 的行為不破（既有 `setView`/`closeSettings` 邏輯）。
- BottomBar 重構面積大、狀態分支多（update/predl/inFlight/apply），重構時**只動排版/樣式、不碰狀態判斷**，並靠既有 + 新增前端測試守住。
- `Launch` 加 `Record` 不可影響回傳 pid/err 語意；存檔失敗降級為 log。
- 配色換值後，逐一目視確認 GridCard / SidebarRow / 既有 pill 仍正確（綠/藍/橘語意未錯位）。

## 驗證方式

1. `go test ./...`（drop `-race`，本機 CGO_ENABLED=0）— playstate + Launch 測試綠。
2. 前端 `npm run test`（vitest）— NavStrip / HomeActionBar / 格式化測試綠。
3. `wails dev` 實機：
   - 主畫面出現 NavStrip，總覽 active、抽卡分析灰且不可點。
   - 主視覺左上無標題；底部 HomeActionBar 版型正確，齒輪中性玻璃、CTA 金光。
   - 點開始遊戲啟動一款遊戲後，「上次遊玩」即時更新；重開 app 仍保留。
   - 就緒=綠、可更新=藍、可預下載=藍 語意色正確。
