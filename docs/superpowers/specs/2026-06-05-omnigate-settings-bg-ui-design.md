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
- **背景**：後端 `App.GetBackgrounds(gameID) []core.Background` 回傳完整清單（每筆有 `ImageURL` + `VideoURL` + `Type`）。前端 `stores/games.ts` 在 `loadAssets`/`loadAssetsFor` 時把清單**砍成一張**（「優先挑第一個有影片的，否則第一張」），存進 `GameRow.background_url` / `background_video`。`_replaceRow`（override/refresh 路徑）會用後端新 `GameRow` 整列 splice 替換、**抹掉** icon/background 欄位後再 async `loadAssetsFor` 補回（程式碼已有註解警告）。`BgLayer.vue` watch 這兩個固定欄位做 dual-buffer cross-fade（image 墊底、video 疊上 autoplay/loop），其 `pending`/`canplay` 狀態機是為「一次性換遊戲」設計、非為快速來回切換。`BottomBar.vue`（HomeActionBar）用 `space-between` 排版：左 = `hero-meta`（狀態膠囊＋版號＋上次遊玩），右 = `bottombar-right`（齒輪＋「開始遊戲」CTA）；**中央沒有現成容器**，圓點需新增一個中間 flex 子節點並確認 `space-between` 不會擠壞。

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
  - 行為：`v-model` 綁到 draft 的 `App.Language`；**選擇即時套用** `setLang(value)`（不必等 Save，沿用 Topbar 既有即時行為），並在 `onSave` 隨 `UpdateSettings` 一併持久化。
  - **取消還原（含第 4 條關閉路徑，MINOR）**：面板開啟時（`watch(settingsOpen)` open 分支）擷取 `i18n.global.locale.value` 存為 `openLang`。還原邏輯放在 **`watch(settingsOpen)` 的 close（`else`）分支**——關閉時若目前 locale ≠ `openLang` 且**非經 Save 關閉**則 `setLang(openLang)`。理由：`settingsOpen` 是 `viewMode==='settings'` 的 getter，齒輪 `toggleSettings()` 會把 `viewMode` 翻回 HOME 而**不經** `onCancel`（第 4 條路徑），故不能只掛在 `onCancel`。實作：`onSave` 設一個 `savedThisSession` 旗標，close 分支據此決定是否還原，然後重置旗標。Cancel/ESC/齒輪三種非 Save 關閉都會還原。
  - i18n key：`settings.language_label` ×3 locale。
- 後端：**零新增欄位**（`App.Language` 已存在）。持久化只走 `UpdateSettings`；既有 `SetLanguage` Go binding 不再被前端呼叫，**保留但成 frontend-dead**（不在本任務移除，避免牽動 i18n 啟動路徑）。

### 4.2 暫存位置（全域單一）

**schema 與版本（精確列出所有必改點）**
- `AppSettings` 新增 `TempDir string \`toml:"temp_dir,omitempty"\``。
- `Backends.{Hoyoverse,Kurogames,Hypergryph}.TempDir` 三個欄位**從 active `Settings` 結構移除**（會造成 `constructProviders` 編譯破壞，於下方 wiring 一併改）。
- **rawTOML 遷移讀取**：`rawTOML.Backends.Kurogames`/`.Hypergryph` 已是真結構、本就帶 `TempDir`；但 **`hoyoverseRawTOML` 目前沒有 `temp_dir`（BLOCKER）** → 必須補 `TempDir string \`toml:"temp_dir"\``，否則使用者設過的 hoyoverse temp_dir 會在 load 時靜默遺失、遷移永遠讀不到。
- **版本 bump v2→v3，三處硬編碼都要改**：`defaultSettings()` `Version: 2`→`3`；`LoadSettings` 收尾 `out.Version = 2`→`3`；`SaveSettings` `s.Version = 2`→`3`。
- **既有測試同步**：`settings_test.go` 內所有斷言 `version == 2` / `Version != 2` 的點（約 10 處）一律改 3，否則無法編譯/通過——此屬本任務範圍，非新增測試。

**解析（`tempDirFor`）**
- `App.tempDirFor(backend, gid)` 的 root 改為：`App.TempDir` 非空則用之，否則 `<TEMP>/omnigate`。底下子目錄**維持現狀**（hoyoverse→`<root>/hoyoverse`、kurogames→`<root>` 扁平、hypergryph→`<root>/hypergryph`）。預設磁碟佈局完全不變。

**provider wiring（採低風險統一方案：三個 backend 全走 `SetTempRootFn`）**
- 不再於建構時把 `Settings.TempDir` 傳進各 provider（那會在 `UpdateSettings` 後 capture 舊值、且 App 側 `tempDirFor` 與 provider 內部可能 split-brain）。
- 對 `kurogames` 與 `hypergryph` **比照 hoyoverse 加上 `tempRootFn` 欄位 + `SetTempRootFn(...)`**。注意這兩個 provider 目前是**內聯**算 tempDir（`kurogames.go:325-329`、`hypergryph.go:251-253`），**沒有** `tempRoot(gid)` 方法 → 需先**抽出**一個 `tempRoot(gid)` helper：`tempRootFn != nil` 則用之，否則沿用原內聯預設（保留為 `SetTempRootFn` 未呼叫時的 fallback，使 `hypergryph/update_integration_test.go:104` 等仍傳 `Settings{TempDir}` 的測試維持綠）。
- `constructProviders` 對三個 provider 都呼叫 `SetTempRootFn(func(gid){ return a.tempDirFor(<backend>, gid) })`，並移除傳入的 `Settings.TempDir`。如此 **App 側（`scanForRecovery`、predl-consume）與 provider 側（download/apply）三條消費路徑共用同一個 `tempDirFor`，不可能 desync**。
- hoyoverse 的 `Settings.TempDir` 欄位變為無用 → 一併移除其 `Settings` 欄位與 `app.go` 傳值。

**設定面板**
- 移除三個 backend 區塊的 TempDir 欄位，改為「一般 / App」區塊內**一個**暫存位置欄位（input + 瀏覽 + 清除），綁 `draft.App.TempDir`。

**遷移**（`LoadSettings`，`raw.Version < 3` 時）
- 讀 `raw.Backends.Hoyoverse.TempDir` / `.Kurogames.TempDir` / `.Hypergryph.TempDir`（**從 `raw`，不是 `out`**，因 `out` 已無此欄位），依此固定順序取**第一個非空**填入 `out.App.TempDir` 並 `slog.Warn` 一行（多個不同值時明確以第一個為準）；都空則維持空（=預設）。
- 完成後 `out.Version = 3`。`SaveSettings` 不再寫出 `backends.*.temp_dir`。
- 與既有 v1→v2 `migrateV1ToV2`（讀 `out.Backends.*.Path`、gate `raw.Version < 2`）**獨立互不干擾**：v1 檔會依序命中兩段遷移；`Backends.*.Path` 保留不動，故 `migrateV1ToV2` 不受影響。

### 4.3 背景輪播 + 圓點

- **store（`games.ts`）**：`GameRow`（前端型別）改帶
  - `backgrounds: { image: string; video: string }[]`（**per-row**；官方完整清單；自訂圖存在時 = 單一筆 `{image: <customDataURL>, video: ''}`）。
  - `bgIndex: number`（**per-row**，故切走再切回會記得上次位置）。
  - 移除「只挑第一個有影片」的塌縮邏輯。`loadAssets`/`loadAssetsFor` 改填整個 `backgrounds`，並**同時**設一個落在範圍內的隨機 `bgIndex`（見下「隨機起點」）。有 `BackgroundPath` 時呼叫 `GetCustomBackground` 並以其單筆結果**取代**清單；data URL **快取在 row**，`BackgroundPath` 未變則不重抓（見 §4.4）。
  - 既有 `background_url`/`background_video` 欄位**移除**，由 `BgLayer` 直接讀 `selected.backgrounds[bgIndex]`，避免雙來源。
  - **其他消費端（MAJOR）**：`GridCard.vue:22`（grid/library 模式縮圖）目前綁 `row.background_url`，必須改讀 `row.backgrounds?.[0]?.image`（grid 縮圖用第一張即可，不需 `bgIndex`）；`GridView` 透過 `GridCard` 連帶處理。移除欄位前須一併改，否則 TS 編譯破 + grid 縮圖壞。
  - **`_replaceRow` 再 seed（MAJOR）**：`_replaceRow` 會 splice 進無 `backgrounds`/`bgIndex` 的新 row；其後既有的 async `loadAssetsFor` 必須負責填 `backgrounds` 並重設隨機 `bgIndex`。在補回前，消費端須容忍 `backgrounds` 為 `undefined`/空。
- **隨機起點**：在以下三個觸發點設 `bgIndex = len>0 ? Math.floor(Math.random()*len) : 0`：(a) `loadAssets`（初次 `load()` 含預設 select）、(b) `loadAssetsFor`（含 `_replaceRow` 之後）、(c) 若實作上 select 已有資產則於 `select(id)` action 重抽。空清單 → 無背景（沿用現狀 fallback）。
- **圓點（`BottomBar.vue`）**：放一排圓點，**以 `position: absolute` 水平置中**疊在 bottombar 上（不當 `space-between` 的第 3 個 flex sibling），避免被既有條件式 `.update-error` 列（`BottomBar.vue:163`，錯誤態才出現）推歪、也不擠壓左右兩側。
  - 數量 = `selected?.backgrounds?.length ?? 0`；`active` = `bgIndex`；點擊 → 設該 row 的 `bgIndex`。
  - `length <= 1`（或 undefined）時整排隱藏。
  - 樣式沿用既有設計 token（金色 active / 暗色 inactive），小尺寸、置中、`pointer-events` 僅圓點本體（不擋下方）。
- **`BgLayer.vue`**：watch 來源從固定 `background_url/video` 改為 `() => selected.backgrounds?.[bgIndex]`（同時涵蓋 `selected` 與 `bgIndex` 變化；`undefined` 守衛）。dual-buffer image/video cross-fade 主體沿用，但**不可假設「邏輯完全不動」**：原 `pending`/`canplay` 狀態機是為一次性換遊戲設計，需確保**快速來回點圓點**（含 image↔video 混切）時 `pending` 不會卡死、不會讓隱形 slot 偷走可見性——必要時在每次來源變更前重置/讓步 `pending`。此情境須有測試（見 §7）。

### 4.4 每遊戲自訂背景圖

- **schema**：`GameSettings` 新增 `BackgroundPath string \`toml:"background_path,omitempty"\``。
- **後端 binding**：
  - `App.BrowseForImage(current string) (string, error)` — `wruntime.OpenFileDialog`，`OpenDialogOptions{ Filters: []FileFilter{{DisplayName, Pattern:"*.png;*.jpg;*.jpeg;*.webp;*.bmp"}}, DefaultDirectory: dialogDefaultDir(filepath.Dir(current)) }`，nil-ctx guard 同 `BrowseForDirectory` 回 `("",nil)`。
  - `App.GetCustomBackground(gameID string) (string, error)` — **取 `settingsMu.RLock()`** 讀 `Settings.Games[id].BackgroundPath`（與 `GetSettings` 同鎖紀律）；空路徑回 `("", nil)`；讀檔失敗回 `("", err)`。成功則回 `data:<mime>;base64,...`。**MIME 由明確 ext→mime map 決定**（`.png`→image/png、`.jpg`/`.jpeg`→image/jpeg、`.webp`→image/webp、`.bmp`→image/bmp；未知副檔名拒絕回 err），**不依賴** `mime.TypeByExtension`（webp/bmp 在 Windows 不可靠）。
- **設定面板（per-game 區塊）**：新增一個「背景圖」區塊，從 games store 列出各遊戲（顯示名 + 一個自訂背景圖欄位：目前路徑 / 瀏覽（`BrowseForImage`）/ 清除）。綁 `draft.Games[id].BackgroundPath`（draft 內 `Games[id]` 不存在則建立空物件再設）。
- **生效**：Save → `UpdateSettings` → `refreshAll`。store 在組 `backgrounds` 時，若該遊戲 `BackgroundPath` 非空 → 清單 = `[{image: <GetCustomBackground 的 data URL>, video: ''}]`（取代官方），圓點因 length=1 自動隱藏。
- **快取/體積**：`GetCustomBackground` 的 data URL 可能很大（4K PNG ~ 數十 MB base64 經 Wails JSON bridge）。store **以 `BackgroundPath` 為 key 快取** data URL，`loadAssetsFor`/`_replaceRow` 重跑時若路徑未變則不重呼叫 binding。
- **rebuild 副作用（已知、可接受）**：`BackgroundPath` 屬純 UI 欄位，但經 `UpdateSettings` 存檔會觸發一次 provider rebuild + detect-cache 失效（與其他設定一致）。v1 接受。

## 5. 元件 / 介面邊界

- **Go**
  - `Settings` schema：+`App.TempDir`、+`GameSettings.BackgroundPath`、−`Backends.*.TempDir`（active，含 `hoyoverse.Settings.TempDir`）；`hoyoverseRawTOML` +`TempDir`（遷移讀取）；版本 2→3（`defaultSettings`/`LoadSettings`/`SaveSettings`）；`LoadSettings` v2→v3 遷移；`SaveSettings` 不寫舊欄位。
  - `App.tempDirFor`：root 改讀 `App.TempDir`。
  - `kurogames`/`hypergryph` provider：+`tempRootFn` 欄位 + `SetTempRootFn(...)`，內部 `tempRoot(gid)` 優先用之（比照 hoyoverse）。
  - `constructProviders`：三個 provider 都 `SetTempRootFn → tempDirFor`，移除傳入的 `Settings.TempDir`。
  - 新 binding：`BrowseForImage`、`GetCustomBackground`（`dialog.go` / `app.go`）。
- **前端**
  - `Topbar.vue`：移除語言按鈕。
  - `SettingsPanel.vue`：新「一般」區塊（語言下拉 + 全域暫存）、移除 per-backend TempDir、新增 per-game 背景圖區塊。
  - `stores/games.ts`：`GameRow` +`backgrounds[]`/`bgIndex`、−`background_url`/`background_video`；`loadAssets`/`loadAssetsFor`/`_replaceRow` 改寫；自訂圖整合 + data URL 快取。
  - `BgLayer.vue`：watch 來源改為 `backgrounds[bgIndex]` + rapid-toggle 健壯化。
  - `BottomBar.vue`：絕對置中圓點指示器。
  - `GridCard.vue`：縮圖改綁 `backgrounds?.[0]?.image`。
  - 既有測試更新：`games_launch.test.ts`（去 `background_url`）、`settings_test.go`（移除/改寫六個 per-backend temp_dir 測試 + version 斷言改 3）。
  - i18n：`settings.language_label`、`settings.general`、`settings.custom_bg_label`、`settings.tempdir_label`（沿用，文案調為全域）等 ×3 locale。

## 6. 錯誤處理 / 邊界

- 自訂圖讀取失敗（檔案不存在/非圖檔）→ `GetCustomBackground` 回 err，前端 fallback 官方清單（不阻塞、不 toast，console.warn）。
- 全域暫存改變後，舊 root 下的在途 staging 不會被新 root 找到 → 屬可重抓的 staging，可接受（與既有 per-backend 改路徑行為一致）。
- 語言即時套用後取消 → 還原開啟面板當下語言。
- 空背景清單 / 單張 → 不顯示圓點，`BgLayer` 行為同現狀。
- `_replaceRow` 之後到 `loadAssetsFor` 補回前的空窗：`backgrounds` 可能 `undefined`/空、`bgIndex` 可能 `undefined` → 消費端（`BottomBar` 圓點、`BgLayer` 來源）一律 `?.` 守衛，不可拋錯。
- 快速來回點圓點（含 image↔video 混切）→ `BgLayer` 須在每次來源變更前妥善處理 in-flight `pending`，避免卡死或隱形 slot 偷可見性。
- 設定遷移：多個 backend 設了不同 temp_dir → 取第一個非空、warn（無法合併三個不同目錄，明確以「第一個」為準）。

## 7. 測試策略

- **Go**
  - `settings_test.go`：**v2→v3 遷移**——(a) **hoyoverse** temp_dir 有值（驗 BLOCKER 1：`hoyoverseRawTOML.TempDir` 真的被讀到、不遺失）、(b) 三 backend 都空→維持空、(c) 多個不同值→取第一個非空（固定順序 hoyo→kuro→gryph）；`SaveSettings` 寫 `version = 3` 且不寫 `backends.*.temp_dir`；既有所有 `version == 2` 斷言改 3（settings_test.go:20/49/92/108/130/256/274 一帶）；`AppSettings.TempDir` 與 `GameSettings.BackgroundPath` round-trip；v1 檔同時命中 v1→v2（per-game path）與 v2→v3（temp_dir）兩段遷移。
  - **既有 compile-break 測試處理（MAJOR）**：移除 active `Backends.*.TempDir` 後，下列直接讀寫該欄位的測試會編譯破，須**刪除或改寫成 `App.TempDir` 等價斷言**——`settings_test.go` 的 `KurogamesTempDir_DefaultEmpty`(138)/`_RoundTrip`(149)/`_BackwardCompat`(166)/`HoyoverseSettings_TempDir_RoundTrip`(185)/`_Omitempty`(209)/`HypergryphTempDirRoundTrip`(226)。`hypergryph/update_integration_test.go:104` 仍用 provider 側 `Settings{TempDir}` → **保留**（provider 欄位不移除）。
  - `app_test.go` / `resolve_test.go`：`tempDirFor` root 改讀 `App.TempDir`（預設 fallback + override；kuro 扁平 / hoyo+gryph 子目錄不變）。
  - **provider 消費端 parity（MAJOR 4）**：驗 `App.TempDir` override 後，`kurogames`/`hypergryph` provider 的 download/apply 用的 tempRoot（經新 `SetTempRootFn`）與 `tempDirFor` 一致——不只測 App 側 `tempDirFor`。
  - `dialog_test.go`：`BrowseForImage` nil-ctx guard 回 `("",nil)`。
  - `GetCustomBackground`：路徑空（回 `"",nil`）/ 不存在（回 err）/ 正常讀取（temp 檔，驗 data URL 前綴）/ **MIME 正確性**（`.webp`、`.bmp` 走明確 map 而非 `mime.TypeByExtension`）/ 未知副檔名拒絕；RLock 不死鎖。
- **前端（vitest）**
  - `settings_panel.test.ts`：語言下拉 3 選項 + 即時套用 + **取消還原**（Cancel 與 ESC 兩路徑）；單一全域暫存欄位；per-game 背景圖列表渲染 + 綁定（`Games[id]` 不存在時建立）。
  - `games_store.test.ts`：`backgrounds[]` 填入、隨機起點落在範圍內、**`_replaceRow` 後 `backgrounds`/`bgIndex` 被重 seed**、自訂圖取代官方且 data URL 依 `BackgroundPath` 快取（路徑未變不重抓）。
  - **既有 compile-break 測試（MAJOR）**：`games_launch.test.ts:22,32` 以 `background_url` literal 建 `GameRow` 並斷言該欄位——移除欄位後須改用 `backgrounds`/`bgIndex`。`GridCard.vue` 改綁 `backgrounds?.[0]?.image` 後，若有其元件測一併更新。
  - `BottomBar` 元件測：圓點數量/active/點擊切換；`length<=1` 或 undefined 隱藏；置中節點不破壞兩側 `space-between`。
  - `BgLayer` 元件測：依 `bgIndex` 切換來源；**快速來回點圓點（含 image↔video 混切）`pending` 不卡死、隱形 slot 不偷可見性**。
  - i18n parity（3 locale 新 key 齊全）。
- **build/smoke**：`go build/vet/test ./...`、`npm run build` + vitest、`wails build`；真機 smoke 驗語言切換、暫存單欄、背景圓點切換、自訂圖取代與 fallback。

## 8. 流程

依使用者指定走 `subagent-review-gates`：spec → plan（opus 審查閘）→ TDD 逐 task（每 task spec 合規 + 程式品質雙 opus gate）→ smoke → `--no-ff` 併回 dev。Commit 不加 `Co-Authored-By`。
