# Omnigate — Settings & Background UI 重整 — Design

Date: 2026-06-05
Branch: `settings-bg-ui/spec`
Status: APPROVED (pending user review)

## 1. 目的與範圍

三項以使用者為主的 UI 調整，合成一個 feature：

1. **語言設定移到設定頁** — 從 Topbar 的切換按鈕移除，改在設定面板內用下拉選單（繁中 / 簡中 / English 三選一）。
2. **暫存位置改全域單一** — 移除 per-backend（hoyoverse / kurogames / hypergryph）各自一個的 `temp_dir`，改成一個全域共用的暫存位置。
3. **主畫面背景輪播 + 每遊戲自訂圖** — 把官方提供的多張背景（圖片＋影片）全部納入；進入遊戲時隨機選一張當起點，主畫面底部置中放圓點指示器手動切換；每款遊戲可各自指定一張自訂背景圖，設了即取代該遊戲的官方背景。

**範圍外（明確不做）**：自動定時輪播、每遊戲多張自訂圖、自訂影片、per-game install path 設定（仍延續先前 deferred 決議）、language 以外的 app 層偏好（`banner_animation_pref` / `show_technical_info` 仍是 dead，不在此 surface）。

## 2. 現況（baseline，已驗證）

- **語言**：`Topbar.vue` 有一顆 `translate` icon button，`cycleLang` 做 `zh-TW ↔ en` 兩段切換，呼叫 `setLang()`（即時）+ `SetLanguage()`（持久化到 `app.language`）。`zh-CN` locale 已存在但不在切換內。
- **暫存位置**：`Settings` 結構中 `Backends.{Hoyoverse,Kurogames,Hypergryph}.TempDir` 各一。
  - `App.tempDirFor(backend, gid)`（`internal/app/app.go`）是 App 層權威解析器，被 `scanForRecovery` 與 predl-consume 路徑使用；預設 `<TEMP>/omnigate`（kuro 扁平）、`<TEMP>/omnigate/hoyoverse`、`<TEMP>/omnigate/hypergryph`。
  - `hoyoverse` provider 透過 `SetTempRootFn(...)` 把 `tempDirFor` 接進來（與 App 一致）。
  - `kurogames` / `hypergryph` provider **不走** `SetTempRootFn`，改在 `constructProviders` 用建構時的 `Settings.TempDir` 傳入，provider 內部自行附 gameID 子目錄。
  - 設定面板 `SettingsPanel.vue` 目前只有三個 backend 區塊、各一個 TempDir 欄位（瀏覽 / 清除）。
- **背景**：後端 `App.GetBackgrounds(gameID) []core.Background` 回傳完整清單（每筆有 `ImageURL` + `VideoURL` + `Type`）。前端 `stores/games.ts` 在 `loadAssets`/`loadAssetsFor` 時把清單**砍成一張**（「優先挑第一個有影片的，否則第一張」），存進 `GameRow.background_url` / `background_video`。`BgLayer.vue` watch 這兩個固定欄位做 dual-buffer cross-fade（image 墊底、video 疊上 autoplay/loop）。`BottomBar.vue`（HomeActionBar）左下 = 狀態膠囊＋版號＋上次遊玩，右下 = 齒輪＋「開始遊戲」CTA，中間目前留空。

## 3. 技術選型（含理由）

- **自訂圖儲存** → **只存檔案絕對路徑**於 `Settings.Games[id].BackgroundPath`（新欄位）。後端新增 `GetCustomBackground(gameID) (string, error)` 讀檔回傳 data URL 餵前端 `<img src>`。
  - 不複製檔案、不把 base64 寫進 `settings.toml`。代價：使用者刪/搬原檔則回退官方背景（可接受，前端偵測讀取失敗即 fallback）。
  - 否決：複製進 app 資料夾 + asset server（要管檔案生命週期）；base64 直接進 settings（settings.toml 膨脹）。
- **輪播狀態** → 放**前端 store**。後端 `GetBackgrounds` 本就回傳完整清單，改動僅止於前端「不再砍成一張」。後端背景相關零改動。
- **暫存位置 root** → `App.tempDirFor` 的 **root** 改讀 `App.TempDir`，底下**維持** per-backend 子目錄結構。預設情況磁碟佈局完全不變，只有使用者改 root 時整包搬家（風險最低）。

## 4. 詳細設計

### 4.1 語言下拉（設定頁）

- **移除**：`Topbar.vue` 的 `translate` 按鈕、`cycleLang`、相關 import（`SetLanguage`/`setLang`/`i18n` 若僅此處用）。
- **新增**：`SettingsPanel.vue` 最上方一個「一般 / App」區塊，含語言 `<select>`：
  - 選項：`繁體中文 (zh-TW)` / `简体中文 (zh-CN)` / `English (en)`。
  - 行為：`v-model` 綁到 draft 的 `App.Language`；**選擇即時套用** `setLang(value)`（不必等 Save，沿用 Topbar 既有即時行為），並在 `onSave` 隨 `UpdateSettings` 一併持久化。取消（Cancel/ESC）時若已即時改過語言，還原為開啟面板當下的語言。
  - i18n key：`settings.language_label` ×3 locale。
- 後端：**零新增欄位**（`App.Language` 已存在；`SetLanguage` 既有 binding 保留，Save 走 `UpdateSettings`）。

### 4.2 暫存位置（全域單一）

- **schema**：`AppSettings` 新增 `TempDir string \`toml:"temp_dir,omitempty"\``。`Backends.*.TempDir` 三個欄位**從 active `Settings` schema 移除**，但 `rawTOML` 解析端**保留**舊欄位以做遷移讀取。Canonical `Version` 由 2 bump 到 **3**（`defaultSettings` + `LoadSettings` 收尾皆設 3；`SaveSettings` 寫 3）。
- **解析**：`App.tempDirFor(backend, gid)` 的 root 改為：`App.TempDir` 非空則用之，否則 `<TEMP>/omnigate`。底下子目錄維持現狀（hoyoverse→`<root>/hoyoverse`、kurogames→`<root>` 扁平、hypergryph→`<root>/hypergryph`）。
- **provider wiring**：`constructProviders` 傳給 `kurogames`/`hypergryph` 的 `Settings.TempDir`，改為由全域 root 衍生的對應 backend root（保留各自內部子目錄邏輯），使三條消費路徑（`tempDirFor` / hoyo `tempRootFn` / kuro+gryph 內部）一致。
- **設定面板**：移除三個 backend 區塊的 TempDir 欄位，改為「一般 / App」區塊內**一個**暫存位置欄位（input + 瀏覽 + 清除），綁 `draft.App.TempDir`。
- **遷移**（`LoadSettings`，`raw.Version < 3` 時）：舊檔 `backends.*.temp_dir`（由 `rawTOML` 讀入）若有值 → 取**第一個非空**填入 `App.TempDir` 並 `slog.Warn` 一行；都空則 `App.TempDir` 維持空（=預設）。完成後 `out.Version = 3`。`SaveSettings` 不再寫出 `backends.*.temp_dir`。此遷移與既有 v1→v2 per-game path 遷移並存、互不干擾。

### 4.3 背景輪播 + 圓點

- **store（`games.ts`）**：`GameRow` 改帶
  - `backgrounds: { image: string; video: string }[]`（官方完整清單；自訂圖存在時 = 單一筆 `{image: <customDataURL>, video: ''}`）。
  - `bgIndex: number`。
  - 移除「只挑第一個有影片」的塌縮邏輯；`loadAssets`/`loadAssetsFor` 改填整個 `backgrounds`（並在有 `BackgroundPath` 時呼叫 `GetCustomBackground` 取代清單）。
  - 既有 `background_url`/`background_video` 可保留為「目前 index 對應」的 derived getter，或直接由 `BgLayer` 讀 `backgrounds[bgIndex]`（實作時擇一，避免雙來源）。
- **隨機起點**：選到某遊戲（`games.selected` 改變、或 app 初次顯示 detail）時 `bgIndex = Math.floor(Math.random()*len)`。空清單時無背景（沿用現狀 fallback）。
- **圓點（`BottomBar.vue`）**：在中央（版號 ↔ 開始按鈕之間）加一排圓點：
  - 數量 = `selected.backgrounds.length`；`active` = `bgIndex`；點擊 → 設 `bgIndex`。
  - `length <= 1` 時整排隱藏。
  - 樣式沿用既有設計 token（金色 active / 暗色 inactive），小尺寸、置中。
- **`BgLayer.vue`**：watch 來源從固定 `background_url/video` 改為 `selected.backgrounds[bgIndex]`（同時 watch `selected` 與 `bgIndex`）。dual-buffer cross-fade 邏輯不動；image+video 都支援，video 照舊 autoplay/muted/loop。

### 4.4 每遊戲自訂背景圖

- **schema**：`GameSettings` 新增 `BackgroundPath string \`toml:"background_path,omitempty"\``。
- **後端 binding**：
  - `App.BrowseForImage(current string) (string, error)` — `wruntime.OpenFileDialog`，圖片濾鏡（`*.png;*.jpg;*.jpeg;*.webp;*.bmp`），nil-ctx guard 同 `BrowseForDirectory`。
  - `App.GetCustomBackground(gameID string) (string, error)` — 讀 `Settings.Games[id].BackgroundPath`，回傳 `data:<mime>;base64,...`；路徑空或讀取失敗回 `("", err)`（前端 fallback 官方）。
- **設定面板（per-game 區塊）**：新增一個「背景圖」區塊，從 games store 列出各遊戲（顯示名 + 一個自訂背景圖欄位：目前路徑 / 瀏覽（`BrowseForImage`）/ 清除）。綁 `draft.Games[id].BackgroundPath`（draft 內不存在則建立）。
- **生效**：Save → `UpdateSettings` → `refreshAll`。store 在組 `backgrounds` 時，若該遊戲 `BackgroundPath` 非空 → 清單 = `[{image: GetCustomBackground(id), video: ''}]`（取代官方），圓點因 length=1 自動隱藏。

## 5. 元件 / 介面邊界

- **Go**
  - `Settings` schema：+`App.TempDir`、+`GameSettings.BackgroundPath`、−`Backends.*.TempDir`（active）；`LoadSettings` 遷移；`SaveSettings` 不寫舊欄位。
  - `App.tempDirFor`：root 改讀 `App.TempDir`。
  - `constructProviders`：kuro/gryph 的 `Settings.TempDir` 由全域 root 衍生。
  - 新 binding：`BrowseForImage`、`GetCustomBackground`（`dialog.go` / `app.go`）。
- **前端**
  - `Topbar.vue`：移除語言按鈕。
  - `SettingsPanel.vue`：新「一般」區塊（語言下拉 + 全域暫存）、移除 per-backend TempDir、新增 per-game 背景圖區塊。
  - `stores/games.ts`：`GameRow` +`backgrounds[]`/`bgIndex`；`loadAssets`/`loadAssetsFor` 改寫；自訂圖整合。
  - `BgLayer.vue`：watch 來源改為 `backgrounds[bgIndex]`。
  - `BottomBar.vue`：中央圓點指示器。
  - i18n：`settings.language_label`、`settings.general`、`settings.custom_bg_label`、`settings.tempdir_label`（沿用，文案調為全域）等 ×3 locale。

## 6. 錯誤處理 / 邊界

- 自訂圖讀取失敗（檔案不存在/非圖檔）→ `GetCustomBackground` 回 err，前端 fallback 官方清單（不阻塞、不 toast，console.warn）。
- 全域暫存改變後，舊 root 下的在途 staging 不會被新 root 找到 → 屬可重抓的 staging，可接受（與既有 per-backend 改路徑行為一致）。
- 語言即時套用後取消 → 還原開啟面板當下語言。
- 空背景清單 / 單張 → 不顯示圓點，`BgLayer` 行為同現狀。
- 設定遷移：多個 backend 設了不同 temp_dir → 取第一個非空、warn（無法合併三個不同目錄，明確以「第一個」為準）。

## 7. 測試策略

- **Go**
  - `settings_test.go`：v2→v3 遷移（per-backend temp_dir → App.TempDir 取第一個非空、都空則維持空、多個不同值取第一個）；`SaveSettings` 寫 version 3 且不寫舊欄位；`AppSettings.TempDir` 與 `GameSettings.BackgroundPath` round-trip。
  - `app_test.go` / `resolve_test.go`：`tempDirFor` root 改讀 `App.TempDir`（含預設 fallback、含 kuro 扁平/hoyo+gryph 子目錄不變）。
  - `dialog_test.go`：`BrowseForImage` nil-ctx guard 回 `("",nil)`。
  - `GetCustomBackground`：路徑空 / 不存在 / 正常讀取（temp 檔）三案。
- **前端（vitest）**
  - `settings_panel.test.ts`：語言下拉 3 選項 + 即時套用 + 取消還原；單一全域暫存欄位；per-game 背景圖列表渲染 + 綁定。
  - `games_store.test.ts`：`backgrounds[]` 填入、隨機起點落在範圍內、自訂圖取代官方。
  - `BottomBar` / `BgLayer` 元件測：圓點數量/active/點擊；`length<=1` 隱藏；`BgLayer` 依 `bgIndex` 切換來源。
  - i18n parity（3 locale 新 key 齊全）。
- **build/smoke**：`go build/vet/test ./...`、`npm run build` + vitest、`wails build`；真機 smoke 驗語言切換、暫存單欄、背景圓點切換、自訂圖取代與 fallback。

## 8. 流程

依使用者指定走 `subagent-review-gates`：spec → plan（opus 審查閘）→ TDD 逐 task（每 task spec 合規 + 程式品質雙 opus gate）→ smoke → `--no-ff` 併回 dev。Commit 不加 `Co-Authored-By`。
