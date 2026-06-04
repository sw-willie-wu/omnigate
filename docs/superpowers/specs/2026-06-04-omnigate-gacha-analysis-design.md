# omnigate — P3 抽卡分析（Gacha Analysis）設計

> 日期：2026-06-04 · 狀態：設計（待 plan）
> 範圍：P3 第一塊、最自包含的子系統 = **抽卡分析儀表板**。免帳密（走遊戲本機 log/webCaches 解出的 history URL）。**架構支援五款，但驗證程度分級**（星穹/終末地已驗證、原神/絕區零待 spike、鳴潮為研究風險）；以**分階段交付**收斂風險（見「交付分階段」）。帳號晶片、開拓力（需真帳號登入）屬 P3 後續，不在此。

## Context（為什麼做這個）

P1 主畫面重構（merge `7e9eb87`）在 NavStrip 放了 `總覽 / 抽卡分析` 兩個分頁，其中 **`抽卡分析` 目前是 disabled 空殼**（view store 已有 `homeTab: 'overview'|'gacha'` + `setHomeTab`）。P2（last-played `4d526ed` + NewsPanel `da2fdcd`）已 SHIPPED 到 `dev`。本 spec 接上抽卡分析頁。

抽卡分析有**獨立設計稿**：`Desktop/export/omnigate Gacha (standalone).html` + `omnigate Gacha - Prompt.md`（對應主 mockup §3.3 的 `GachaBoardA`），HTML 為 7.1MB 內嵌資產，版面以 Prompt.md 為準。

依 P1/P2 鐵則：**需要資料一律走新增 Go binding（Wails），不前端直接 fetch**；前端新頁面元件 + Pinia store + i18n 三檔 parity；per-game 優雅降級（同 NewsPanel/last-played）。

預期成果：點 NavStrip 的「抽卡分析」→ 主視覺內容區換成抽卡儀表板（外殼/NavStrip 不跑位），顯示該遊戲當前帳號的抽卡統計（總抽數/估算花費/最高星數/平均出貨/本機幸運值/保底進度/出貨分佈/最近最高星時間軸）；「重新整理紀錄」走遊戲本機 log/webCaches 解 history URL → 打 record API → 增量去重寫入 SQLite；含載入中／空（未匯入）／錯誤（URL 失效引導重開）／未支援 四狀態。

## 研究結論（資料源可行性，2026-06-04 research spike）

> HoYoverse no-probe 規則放寬範圍：使用者 2026-06-04 明確放寬 **gacha-log API**（用使用者自己的 authkey/token，gacha-log 是玩家自身資料、社群 exporter 廣用），比照 NewsPanel news API 例外。**`genAuthKey` 等鑄憑證/受保護帳號協定仍 no-probe、不放寬**（見「URL 取得策略」）。
> 參考來源同時涵蓋使用者本機既有研究 repo `C:\Users\willie\Repos\gacha-tracker`（已實作 Endfield + Star Rail 抓取）。

**抓取模式（共同骨架，但 URL 取得**不**統一）**：

```
取得 history URL（帶 token/authkey）→ 打該遊戲 record API（cursor 分頁）
   → 正規化成 GachaPull → 寫入 store（去重）
```

⚠️ **「URL 取得」不是五款統一的**——分兩種來源，且 **HoYoverse 內部也不一致**：(a) **文字 log regex**（Star Rail / Endfield 已驗證寫整段 history URL 到 log）；(b) **webCaches 二進位掃描**（Genshin 歷來**不**把完整 authkey URL 寫進 Player.log，社群 exporter 改掃 `<Game>_Data/webCaches/.../Cache/Cache_Data/data_2` 撈含 authkey 的 URL）。因此 `FetchGacha` 必須**允許 per-game URL-source 策略**（text-log vs binary-cache）。各家差異落在五項：**URL 來源類型、log/cache 路徑、history URL regex、record API（host/路徑/參數/cursor）、星級制 + 保底模型**。

**證據分級（誠實標示——避免把未驗證當已驗證）**：

| 遊戲 | 驗證程度 | URL 來源 | record API / cursor | 最高★ | 參考 |
|---|---|---|---|---|---|
| 星穹鐵道（HoYoverse）| ✅ **已驗證**（使用者 repo 腳本）| 文字 log regex：`Player.log` 內 `…mihoyo.com…/gacha/v[0-9]/history?token=…` | `getGachaLog`，`end_id` cursor、`gacha_type` 分池 | 5 | gacha-tracker `starrail-export.ps1`、biuuu/genshin-wish-export、UIGF |
| 終末地（Hypergryph）| ✅ **已驗證**（使用者 repo 完整實作）| 文字 log regex：`…\Gryphline\Endfield\sdklogs\HGWebview.log` 內 `https://ef-webview.gryphline.com/page/gacha_…` | `/api/record/char`，`seq_id` cursor、`pool_type` 分池 | 6 | **gacha-tracker（本機實作）**、bhaoo/endfield-gacha、daydreamer-json |
| 原神（HoYoverse）| 🟡 **類比 + 待 spike**（不同 URL 來源）| **webCaches `data_2` 二進位掃描**（非 Player.log）撈 `getGachaLog?authkey=…` | `getGachaLog`，`end_id` cursor | 5 | biuuu/genshin-wish-export、sunfkny/genshin-gacha-export |
| 絕區零（HoYoverse）| 🟡 **類比 + 待 spike**（URL 來源待確認 log vs webCaches）| spike 釘（gids=8 已知，URL 來源未驗證）| `getGachaLog` 系 | 5 | 同上 |
| 鳴潮（Kuro）| 🔴 **研究風險，未驗證**（使用者 repo **無** WuWa 腳本）| install-dir `Client/Saved/Logs` debug log 內 convene URL（regex 未釘）| Kuro record API（host/路徑/POST 形狀**全未知**）| 5 | Ikram001/wuwa-pull-tracker-local、Luzefiru gist、Anubhav1603/URL-Extractor（**僅證明社群做得到、本專案未自行釘死**）|

- **「已驗證」的精確範圍**：Star Rail 只有 **URL 取得步驟**（Player.log regex）經使用者腳本驗證；其 `getGachaLog` **fetch/cursor/正規化**側使用者無程式、僅社群參考（biuuu/UIGF）→ plan spike 仍須把 SR 的 record-API 抓取當**未驗證**對待。Endfield 則 URL + record API + 正規化在使用者 repo 皆完整。
- **WuWa 是最弱的一腳、不可當已確認**：使用者 repo 無 WuWa 程式，record API host/路徑/POST body 皆未知。plan 階段 spike **必須帶 kill-switch**：若 WuWa 機制無法在合理時間內釘死，該款**降級為「未支援」**，不阻塞其餘四款交付。
- **各款 host/路徑/參數/cursor/region/lang map 由 plan 階段 research spike 逐款釘死**（HoYoverse 三款 host/biz/region 不同且 **URL 來源可能不同**；鳴潮整套未知；終末地參數已由使用者 repo 釘死，見下）。
- **終末地細節（gacha-tracker 已釘死）**：URL regex `https://ef-webview\.gryphline\.com/page/gacha_[^ ]*`；API `/api/record/char` params `token`/`pool_type`/`lang`/`server_id`/`seq_id`；pool_type ∈ {`E_CharacterGachaPoolType_Standard`(基礎尋訪), `_Special`(特許尋訪), `_Beginner`(啟程尋訪)}（spike 確認是否另有武器池）；回應 `data.{list:[{poolId,poolName,charId,charName,rarity,gachaTs,seqId,isFree}],hasMore}`；`hasMore`+`seqId` cursor 分頁。

## 使用者已拍板的範圍決策（2026-06-04 brainstorm）

- **v1 五款全納入架構**，拿不到機制的 per-game 優雅降級（顯示未連結/未支援狀態）。**驗證程度分級**（見研究結論）：星穹/終末地已驗證；原神/絕區零類比待 spike（原神 URL 來源是 webCaches 非 log）；**鳴潮為研究風險、可能降級未支援**。架構支援五款不等於五款都保證 ship。
- **gacha API 可輕量探測**（同 NewsPanel news 例外，用使用者自己帳號的 authkey/token 釘 host/路徑/region）。**`genAuthKey` 受保護協定不放寬**。
- **持久化 = SQLite（`modernc.org/sqlite`，pure Go，配合 CGO_ENABLED=0），只給抽卡紀錄用**，藏在 swappable store 介面後（`internal/store`）。last-played（`playstate.json`）與 Sophon chunk-dedup 維持原 JSON 不動、不在此遷移（見 memory `future-sqlite-for-cross-game-state-and-gacha`）。
- **幸運值本機推算**（平均出貨抽數 vs 理論期望值），**不宣稱全服百分位**（需外部基準分佈、不可行，比照 P2 砍倒數 pill）。小/大保底命中%、最非紀錄等皆本機算。
- **多帳號按 (game, uid) 儲存、UI 顯示當前抓到的 UID**（不做帳號切換選單；帳號晶片屬 P3 後續）。
- **匯出報表 v1 不做**（YAGNI）；未來若做，採 **UIGF**（社群通用抽卡交換格式）。
- **URL 取得 = 開一次 + 智慧快取**：保留「在遊戲內開抽卡紀錄頁一次」這步（token 由伺服器現場簽發、不常駐本機，故必經此步），但把 URL+token 快取進 store；**有效期內（HoYoverse authkey ~24h）重新整理直接重打 API、不用再開遊戲**，token 過期才引導再開一次。**不做憑證重放**（讀 registry 登入 token + 重放 genAuthKey）：碰帳號憑證、踩受保護協定、脆弱、且鳴潮/終末地幾無人做 → 排除。
- **架構 = per-provider 可選介面 + backend-agnostic store/統計引擎（方案 A）**。否決「provider 直接回算好的 summary」（各家重算統計、易不一致）。

## 設計

### A. 核心型別與可選介面 — `internal/core`

放 `internal/core/provider.go`（與 `InstallLocator`/`LastPlayedProbe`/`NewsProvider` 同檔同格）：

```go
// GachaPull is one normalized pull record (backend-agnostic).
type GachaPull struct {
	ID        string // provider 的唯一遞增 id（去重鍵：HoYoverse `id`、Endfield `seqId`）
	BannerKey string // per-game banner 鍵（"char"/"weapon"/"standard"/"beginner"…），對應 GachaConfig.Banners
	ItemType  string // 正規化道具型別（角色/光錐/武器…，顯示用）
	Rank      int    // 星級，不假設範圍（HoYoverse 3/4/5、Endfield 4/5/6）
	Name      string // 道具名
	Time      string // 來源回傳的當地時間字串（原樣帶過）
	IsFree    bool   // 免費抽（終末地有；HoYoverse 恆 false）→ 排除於保底計數與花費估算
}

// GachaFetchResult is one refresh's outcome: the account uid + the pulls fetched
// this round (may overlap with stored history; the store dedups by (game,uid,id)).
type GachaFetchResult struct {
	UID   string
	Pulls []GachaPull
	URL   string // 本次使用/取得的 history URL（store 快取，供有效期內重打）
}

// PityModel 算「距上一個最高星還累積幾抽」等保底語意，per-game 可插拔。
// 不同遊戲保底機制差很大（HoYoverse 硬保底+50/50；Endfield 軟/硬保底+isFree 排除+
// 限定保證），故抽成策略而非單一數字。實作為純函式、無 IO。
type PityModel interface {
	// HardPity 該 banner 的硬保底上限（顯示「N/上限」進度條用）。
	HardPity() int
	// PityAfter 給「依時間+id 昇序排好、且屬同一 banner」的紀錄，回傳序列尾端
	// 當前已累積、尚未中最高星的抽數（即下一抽的保底進度）。Endfield 的 carryover/
	// isFree 規則在此內部算掉、不外露。
	PityAfter(sortedSameBanner []GachaPull, headlineRank int) int
	// Has5050 此 banner 是否有小保底/大保底（50/50）機制（HoYoverse 限定池 true；
	// Endfield/常駐池 false）。決定 GachaSummary.WinRate5050 是否計算。
	Has5050() bool
}

// BannerConfig 描述一個卡池在 UI/統計裡的呈現。
type BannerConfig struct {
	Key   string          // 對應 GachaPull.BannerKey
	Label LocalizedString // 顯示名（限定角色池/光錐池/常駐池…）
	Pity  PityModel
}

// GachaConfig 是 per-game 的星級制 + 卡池 + 估價設定，餵給 backend-agnostic 統計引擎。
type GachaConfig struct {
	HeadlineRank int                     // 最高星級：HoYoverse=5、Endfield=6
	RankLabels   map[int]LocalizedString // 星級顯示標籤（避免前端寫死「五星」）
	Banners      []BannerConfig
	PullPrice    int    // 單抽估價（估算花費用；明標「估算」）
	Currency     string // 估價幣別顯示（如 "NT$"）
}

// GachaProvider is an optional Provider capability: fetch a game's gacha history
// (no password; reads the short-lived history URL/token from the game's local
// state). 未實作此介面的遊戲 → 抽卡分析頁顯示「未支援」。
//
// ⚠️ URL 來源 per-game 不同（見研究結論）：文字 log regex（SR/Endfield）vs
// webCaches 二進位掃描（Genshin）。FetchGacha 的實作各自決定來源；介面不假設來源。
type GachaProvider interface {
	// FetchGacha 取 history URL（log regex 或 webCaches 掃描，或用 cachedURL 在有效期內
	// 重打）→ 打 record API → 分頁拉 → 回正規化紀錄 + uid。
	//   installDir：App 解析後的安裝目錄（webCaches 來源需要；log 來源多在 LocalLow）。
	//   cachedURL：store 上次快取的 URL（""=無）；provider 優先用本機最新的，無有效 URL
	//     時退回 cachedURL。
	//   URL 失效/找不到 → 回 ErrGachaURLUnavailable（App 轉前端「重開抽卡紀錄」引導）。
	// 註：與 LastPlayedProbe（純路徑、無 IO）不同，FetchGacha 做真實檔案 IO + 網路。
	FetchGacha(ctx context.Context, gid GameID, installDir, cachedURL string) (GachaFetchResult, error)
	// GachaConfig 回該遊戲的星級/卡池/估價設定（純資料）。
	GachaConfig(gid GameID) GachaConfig
}
```

> **`ErrGachaURLUnavailable` 哨兵錯誤**置於既有 core error 哨兵所在檔（與 `ErrUnknownGame` 等同處，保持一致），其檔需 import `errors`；**不**直接塞進 `provider.go`（該檔目前只 import `context`/`fmt`/`strings`）。
>
> **路徑勿盲目沿用 LastPlayedProbe**：⚠️ 既有 `hoyoverse/lastplayed.go` 把 starrail 映到 `Cognosphere\Star Rail`，但已驗證的 `starrail-export.ps1` 讀的是 `miHoYo\Honkai: Star Rail\Player.log`——**兩者不同**（region/version layout 差異）。gacha log 路徑須 plan spike 逐款實機確認，**不可假設 `localLowProduct` 可原樣重用**。

> mockup 的「歐非全服百分位」需外部基準分佈 → 不做（改本機推算，見統計引擎）；「匯出報表」v1 不做。

### B. 持久化 — `internal/store`（SQLite，swappable）

- 新增 `internal/store` 套件，介面先抽出、SQLite 為唯一實作（未來可換）：
  ```go
  type GachaStore interface {
      // UpsertPulls 以 (game,uid,id) 主鍵插入即去重，回新增筆數。
      UpsertPulls(game, uid string, pulls []core.GachaPull) (added int, err error)
      AllPulls(game, uid string) ([]core.GachaPull, error)
      KnownUIDs(game string) ([]string, error)            // 多帳號：列出該遊戲已知 uid
      LatestUID(game string) (string, error)              // 最近一次抓到的 uid（顯示當前）
      GetURLCache(game, uid string) (url string, fetchedAt time.Time, err error)
      PutURLCache(game, uid, url string) error            // 快取 history URL + 時間戳
      Close() error
  }
  ```
- 實作 `internal/store/sqlite`（`modernc.org/sqlite`）。schema（單檔 DB）：
  - `pulls(game TEXT, uid TEXT, id TEXT, banner_key TEXT, item_type TEXT, rank INTEGER, name TEXT, time TEXT, is_free INTEGER, PRIMARY KEY(game, uid, id))` → 插入即去重。索引 `(game, uid)`。
  - `url_cache(game TEXT, uid TEXT, url TEXT, fetched_at INTEGER, PRIMARY KEY(game, uid))`。
  - `meta(key, value)`（schema 版本、`latest_uid:<game>`）。
- **DB 檔位置**：跟 settings 同目錄（目前實際是 CWD，已知行為，見 memory `main-screen-p1` last-played 段；未來統一遷移時一併處理）。檔名如 `gacha.db`。
- 連線單例由 App 持有；`modernc.org/sqlite` 為 pure Go，**不需 CGO**（與本機 `CGO_ENABLED=0` 相容）。go.mod 新增此唯一依賴。
- **並發**（`RefreshGacha` 寫 vs `GetGachaSummary` 讀皆可獨立被 Wails 呼叫）：**先把分頁結果全部抓進記憶體，再開一個短交易 `UpsertPulls`**——**絕不在分頁 HTTP 期間持有寫交易**。`*sql.DB` 設 `SetMaxOpenConns(1)`（單檔避免 `SQLITE_BUSY`），或 store 層加 mutex。
- **schema 版本/遷移**：開 DB 時讀 `meta.schema_version`；缺 → 建表並設 v1；保留一個 migration hook（v1 不需真遷移，但首次 schema 變更前要有掛點，避免 ship 後破壞）。
- **token-at-rest 決策（N3）**：`url_cache.url` 含**有效約 24h 的 token/authkey**——等於把短期憑證寫進 CWD 的未加密 `gacha.db`。本專案為單機單使用者 launcher、DB 與 settings 同信任域 → **接受此風險**，但強制：(a) **絕不把含 token 的 URL 寫進 log**（provider/app log 一律遮蔽 query string）；(b) token 過期自然失效、無長期外洩面。未來若做雲端同步須重新評估。

### C. 統計引擎 — `internal/core/gacha`（backend-agnostic 純函式）

輸入 `[]GachaPull`（某 (game,uid) 全量）+ `GachaConfig`，輸出 mockup §2 的所有數字。**不寫死 5★/「光錐」**，一律走 `HeadlineRank` + `RankLabels` + 各 banner 的 `PityModel`：

```go
type GachaSummary struct {
	Supported   bool
	UID         string
	LastUpdated string // 最後更新時間（store URL_cache fetchedAt 或最近一筆 pull）
	// 4 張統計卡
	TotalPulls   int            // 總抽數（可分 banner）
	PerBanner    map[string]int // 各 banner 抽數
	SpendEst     int            // 估算花費 = (非免費抽數) × PullPrice
	Currency     string
	HeadlineCnt  int            // 最高星數量
	HeadlineByType map[string]int // 角色/光錐… 分佈
	AvgPity      float64        // 平均出貨抽/最高星
	// 歐非（本機推算）
	LuckScore    int            // 由 AvgPity vs 理論期望映射的 0-100 分
	LuckLabel    string         // 微歐/微非… （前端 i18n key）
	WinRate5050  *float64       // 小保底命中%（僅有 50/50 機制的遊戲；否則 nil）
	WorstPull    int            // 最非紀錄（單次最高星花最多抽）
	// 保底進度
	Pity []BannerPity // 每 banner：距上一最高星抽數 / 硬保底上限 / nearPity 旗標
	// 出貨分佈：最高星出貨抽數落在各區間（1-9…80+）的次數
	Distribution []int
	// 最近最高星 + 時間軸
	RecentHeadline []HeadlineEntry // 名稱/日期/花費抽數/是否 UP
}
```

- **平均出貨/保底/分佈/時間軸**：把紀錄依 banner 分組、依 (time, id 數值序) 昇序排，逐抽累計，遇 `Rank == HeadlineRank` 記錄花費抽數並依該 banner 的 `PityModel` 重置（HoYoverse 歸 0；Endfield 依 isFree/carryover 規則，見 PityModel 實作）。
- **本機幸運值**：`AvgPity` 對映「理論期望出貨抽數」（per-game 常數，如 HoYoverse 角色池 ≈ 62.5）→ 線性映射成 0-100 LuckScore + 結論標籤（微歐/微非）。**不需外部資料**。
- **估算花費**：`SpendEst = 非免費抽數 × PullPrice`，前端**明標「估算」**。
- **時間解析/排序（載入關鍵，N5）**：`GachaPull.Time` 是來源當地時間字串、無時區，排序靠 `(time, id 數值序)`。Go 無 JS `Date` 的寬鬆解析 → **plan 須逐款定義精確的 time parse layout**（HoYoverse server-local `YYYY-MM-DD HH:MM:SS`、Endfield `gachaTs`），及 **id 數值序 tiebreak**（同時間戳的十連必須穩定排序，否則保底計數錯亂）。比照 gacha-tracker `gachaStore.ts` 的 time + numeric-id tiebreak，但用 Go 明確 layout。
- **PityModel carryover 內含（N6）**：Endfield 的 milestone-60 carryover + isFree 排除等狀態**全在該 banner 的 `PityModel.PityAfter` 內部算掉**、不外露到 `GachaSummary`（v1 mockup §2.3 只顯示 `N/上限`，`int` 足夠）。介面刻意只回單一 pity 進度，避免實作中途發現「介面裝不下 carryover」。
- **`WinRate5050`（N7）**：小保底命中%是 HoYoverse 特性、Endfield 無 50/50（走限定保證/carryover）。此欄是否計算由 **`GachaConfig`/`PityModel` 的 flag 決定**（如 `PityModel` 增 `Has5050() bool`），**不在統計引擎裡硬編 provider 判斷**；無 50/50 機制 → `nil`。
- 純函式、可單測；不碰 IO/network。

### D. App bindings — `internal/app`

- `func (a *App) RefreshGacha(gameID string) (core.GachaSummary, error)`：
  1. `provider(gid)`；type-assert `core.GachaProvider`，未實作 → 回 `{Supported:false}`。
  2. 解析 install dir：讀 `a.resolved[gid].Path`（`a.resolved` 值型別是 `resolvedEntry`、非字串；讀取須在 `settingsMu.RLock` 下，比照 `gameRowLocked` 紀律）。
  3. 讀 store URL 快取 → `provider.FetchGacha(ctx, gid, installDir, cachedURL)`（帶 timeout，分頁拉可能較久，給較寬 timeout 並尊重 ctx 取消；provider 內各頁間加 rate-limit sleep 比照 bhaoo/endfield-gacha 500–1000ms，避免觸發風控）。
  4. `ErrGachaURLUnavailable` → 回可辨識錯誤（前端顯示「請在遊戲內開啟抽卡紀錄」引導）。**須在 `core/errors.go` 的 `ErrorCode()` 加一個 case**（如 `→ "gacha_url"`），否則 fallthrough 成 `"internal"`、前端 `errKind:'url'` 分不出來。
  5. 成功 → `store.UpsertPulls` + `store.PutURLCache` + 記 latest uid → 讀全量 → 統計引擎 → 回 summary。
- `func (a *App) GetGachaSummary(gameID string) (core.GachaSummary, error)`：**只讀 store**（不打網路）。取 latest uid → AllPulls → 統計引擎。無資料 → `{Supported:true, 空}`（前端空狀態，引導匯入第一份）。非 GachaProvider → `{Supported:false}`。
- 沿用既有 `OpenExternalURL`（P2 已加）供「查看官方抽卡頁」等外開（若需要）。
- App 持有 `GachaStore` 單例（啟動時開 DB；關閉時 Close）。

### E. 前端

- 新 `stores/gacha.ts`（Pinia）：per-`gid` 狀態 `{ summary, loading, error, errKind: 'url'|'other'|null, loaded }`（多帳號 v1 顯示當前 uid，故快取 key 用 gid 即可；summary 內含 uid）。
  - `load(gid)`：惰性——已 `loaded` 不重抓；呼 `GetGachaSummary(gid)`（只讀 store，秒回）。
  - `refresh(gid)`：呼 `RefreshGacha(gid)`（解 log → 打 API），成功覆蓋 summary；`ErrGachaURLUnavailable` → set `errKind='url'`。
  - `useRefreshAll` 加 `gacha.reset()`（**比照 NewsPanel `news.reset()` 的 lazy 模式**，非主動 per-gid refresh）：⚠️ 既有 `composables/useRefreshAll.ts` 是疊代**所有**已安裝遊戲、無「當前/選取 gid」概念 → 用 `reset()` 清快取、下次進該頁惰性重抓（低風險、貼合既有 pattern），**不**在 refreshAll 裡主動打網路。
- 新 `components/GachaBoard.vue`：掛在 `homeTab==='gacha'` 時的內容區（外殼/NavStrip 不跑位）。版面依 Prompt.md §2 的 4 欄網格：
  - 頂部 4 張統計卡（總抽數/估算花費/最高星數/平均出貨）。
  - 歐非評比（甜甜圈 LuckScore + 結論 + 小/大保底%/最非）。
  - 保底進度（多條，>80% 金色發光 + 「距保底 N 抽」）。
  - 出貨分佈迷你長條圖。
  - 最近最高星橫向卡 + 五星時間軸列表。
  - 頂部動作：「重新整理紀錄」→ `refresh`；副標顯示 UID + 「最後更新 N 分鐘前」。
  - **四狀態（Prompt.md §4 強制容錯）**：未支援（顯示「此遊戲尚未支援抽卡分析」）／空（新帳號無資料，引導「在遊戲內開啟抽卡紀錄後按重新整理」）／URL 失效（同引導文案）／錯誤。**不顯示空白或假數據**。
  - 標籤一律由 summary/i18n 出，**不寫死「五星/光錐」**（終末地走 6★/角色）。
- NavStrip：把 `抽卡分析` 從 disabled 改可點（`setHomeTab('gacha')`）。⚠️ 既有 `frontend/src/__tests__/NavStrip.test.ts` 斷言該分頁 `.disabled`/inert → **plan 須含一條更新此測試的任務**。
- i18n：新增 `gacha.*` keys（卡標題、歐非結論、保底、分佈、時間軸、四狀態、重新整理、引導文案、星級/卡池標籤），**zh-TW/zh-CN/en 三檔 parity**（`i18n_parity.test.ts` 守）。
- **安全**：所有來自 record API 的字串（道具名/卡池名）走 Vue 純文字插值，**禁 `v-html`**。

### F. Provider 實作

各 provider 新增 `gacha.go` + `gacha_test.go`，實作 `GachaProvider`：

- **hoyoverse**（三款共用 record API 抓取邏輯、host/biz/region 由 map 分；但 **URL 來源 per-game 不同**）：
  - **URL 取得（spike 逐款釘）**：Star Rail = 文字 log regex（`Player.log`，路徑須實機確認、**勿假設等同 `localLowProduct`**，見上 ⚠️）；Genshin = **webCaches `data_2` 二進位掃描**（`<InstallDir>/<Game>_Data/webCaches/<ver>/Cache/Cache_Data/data_2`，撈含 `authkey` 的 `getGachaLog` URL）；ZZZ = spike 確認 log vs webCaches。抽出共用 `urlSource` 策略（text-log / webcache-scan），per-game 指定。
  - **record API**：`getGachaLog`，`gacha_type` 分池、`end_id` cursor、`size=20`，各頁間 sleep。映射 `id/gacha_type/item_type/rank_type/name/time/uid`。region/biz/host 三款各異（spike 釘）。
- **kurogames**（鳴潮）：解 install-dir `Client/Saved/Logs` debug log 取 convene URL → Kuro record API（POST 分頁）。映射 convene 欄位 → GachaPull。
- **hypergryph**（終末地）：解 `…\Gryphline\Endfield\sdklogs\HGWebview.log` 取 `ef-webview…/page/gacha_…` URL → `/api/record/char`，`pool_type` 分池、`seq_id` cursor、`hasMore`。映射 `seqId→ID`、`poolId→BannerKey`、`charName→Name`、`rarity→Rank`、`gachaTs→Time`、`isFree→IsFree`（依 gacha-tracker 已釘死欄位）。`GachaConfig.HeadlineRank=6`。
- 各家 `GachaConfig` 提供 banner 設定 + `PityModel` 實作（HoYoverse 硬保底+50/50；Endfield 依使用者 repo 實測模型 + bhaoo/endfield-gacha 對齊：軟/硬保底、isFree 排除、限定保證/carryover——**spike 釘死實際參數**，pluggable 設計吸收）。
- 各 provider 抓取/解析失敗一律降級（回 `ErrGachaURLUnavailable` 或空），不崩。

### G. 測試

- **Go**（`go test ./...`，drop `-race`，本機 CGO_ENABLED=0）：
  - 統計引擎 `internal/core/gacha`：純函式單測，餵造紀錄驗每個數字（總抽/估算/最高星/平均出貨/各 banner 保底/分佈/時間軸/LuckScore），含 Endfield 6★ + isFree 排除 case。
  - store SQLite：UpsertPulls 去重（重複 id 不重覆計）、AllPulls 還原、URL 快取讀寫、多 uid 隔離。
  - 各 provider `gacha_test.go`：httptest server / JSON fixture 餵 record API 回應，斷言分頁（cursor 推進）+ 正規化映射 + lang/region map；log 解析用暫存 log fixture 驗 URL regex；URL 缺失 → `ErrGachaURLUnavailable`。
  - app：`RefreshGacha`/`GetGachaSummary` 用 fake GachaProvider 測路由、未實作→`Supported:false`、URL 失效錯誤透傳。
- **前端**（vitest）：`gacha` store（load 只讀/refresh/errKind）；`GachaBoard` 四狀態 + 各區塊渲染（用造 summary）+ 標籤不寫死；i18n parity。
- **真機 smoke**：五款各開抽卡分析、（在遊戲內開過抽卡紀錄後）重新整理、URL 失效引導、切 UID 顯示當前、未支援/空狀態不崩、Topbar 重新整理重抓。

### H. 非目標（明確排除）

- 全服百分位歐非（需外部基準分佈，不可行 → 改本機推算）。
- 匯出報表（v1 不做；未來走 UIGF）。
- 憑證重放/自動鑄 authkey（碰受保護協定 + 帳號憑證，排除；保留「開一次+快取」）。
- 帳號切換選單、帳號晶片、開拓力（P3 後續，需真帳號登入）。
- last-played / Sophon chunk-dedup 遷 SQLite（不在此 feature；未來統一里程碑）。

## 交付分階段（de-risk，N8）

範圍大（5 款 × URL 取得策略不一 + 分頁 API + 正規化 + PityModel + 新 SQLite store + 統計引擎 + 前端板 + 三語 i18n）→ **不一次到位**，plan 按下列階段拆任務，每階段都是可 ship 的里程碑（任一後段卡住不影響前段）：

1. **基礎 + 已驗證一款**：`internal/store`（SQLite）+ 統計引擎 + `core` 型別/介面 + App bindings + 前端 `GachaBoard`/store/i18n + **Star Rail**（已驗證、文字 log 來源最單純）。此階段交付即「抽卡分析可動」。
2. **終末地**（已驗證、使用者 repo 完整參考；驗 6★/isFree/carryover PityModel + seq_id 分頁）。
3. **原神 + 絕區零**（新增 webCaches `data_2` 二進位掃描 urlSource；spike 釘死後接）。
4. **鳴潮**（研究風險，spike 帶 **kill-switch**：API 釘不死 → 留「未支援」降級，不阻塞 1–3）。

> 統計引擎/store/前端板在階段 1 就 backend-agnostic 完成，後續每款只加 provider `gacha.go` + `GachaConfig` + `PityModel`，零改動引擎/前端。

## 起點與分支衛生

目前在 `dev`（P2 已合，工作樹乾淨）。依 memory `feedback_commits.md`（main + feature branch + `merge --no-ff`、無 `Co-Authored-By`），從 `dev` 開 feature 分支 `gacha-p3`。spec/plan 提交於此分支；完成後 `--no-ff` 併回 `dev`。`dev→main` 仍待使用者要發時再收（email 決定 = A 維持現狀）。

## 風險與注意

- **token 短期失效（~24h）**：智慧快取後仍會過期 → record API 回 auth 錯誤時，provider 應轉 `ErrGachaURLUnavailable`、前端引導重開。不得卡 loading 或顯示假數據。
- **record API 為第三方非官方契約、隨改版漂移**：任何 fetch/parse 失敗降級（空/錯誤狀態），不崩。host/路徑/region/cursor 各款 spike 釘死後仍需隨改版維護。
- **no-probe 放寬僅限 gacha-log API**（玩家自身資料）；`genAuthKey`/受保護帳號協定不放寬（見 memory 例外註記，本 spec 後須更新該 memory）。
- **rate-limit/風控**：分頁拉紀錄各頁間加 sleep（比照社群 500–1000ms），避免觸發伺服器風控。
- **終末地保底模型不確定**：官方攻略（80 硬/65 軟/120 限定保證、6★）與使用者 repo 實測模型（milestone-60 carryover + isFree 排除）有出入（可能版本差異）→ spike 以 gacha-tracker + bhaoo/endfield-gacha 對齊，PityModel pluggable 吸收結果。
- **SQLite 依賴**：`modernc.org/sqlite` 為 pure Go、相容 CGO_ENABLED=0，但 binary 增約 +10MB（memory `future-sqlite` 已認可此成本）。
- **DB 檔在 CWD**（同 settings/playstate 現況），已知行為，未來統一遷移。

## 驗證方式

1. `go test ./...`（drop `-race`）— 統計引擎 + store + 各 provider gacha parse + app binding 測試綠。
2. 前端 `npm run test`（vitest）— GachaBoard 四狀態 + gacha store + i18n parity 綠。
3. `wails build` → `build/bin/omnigate.exe`；go.mod 僅新增 `modernc.org/sqlite`。
4. `wails dev` 實機：
   - 五款各點「抽卡分析」→ 出儀表板或對應降級狀態。
   - 在遊戲內開過抽卡紀錄頁後按「重新整理紀錄」→ 解 log → 拉紀錄 → 數字更新；再按一次（快取有效期內）不需重開遊戲。
   - 切換遊戲帳號（換 UID）→ 重抓顯示當前 UID。
   - URL 失效 → 顯示引導、不崩；未支援遊戲 → 未支援狀態。
   - Topbar 重新整理 → 當前遊戲抽卡資料重抓。
