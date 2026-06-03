# omnigate — P2 NewsPanel（最新情報）設計

> 日期：2026-06-03 · 狀態：設計（待 plan）
> 範圍：P2 第二塊子系統 = **NewsPanel 最新情報面板**。活動倒數 pill 經研究確認免登入下不可行 → 砍掉、主視覺左上留空。NewsPanel 涵蓋 5 款（Endfield 走官網 HTML 爬取）。帳號/開拓力屬 P3，不在此。

## Context（為什麼做這個）

P1 主畫面重構（merge `7e9eb87`）把 detail view 補成有結構的版面，但 mockup（`Desktop/export/omnigate Main*.{md,html}` §3.5）的**右上情報面板 NewsPanel** 與 §3.4 左上**活動倒數 pill** 當時延後到 P2。P2 第一塊 last-played 已 SHIPPED 到 `dev`（merge `4d526ed`）。本 spec 做第二塊 NewsPanel。

依 P1 鐵則：**需要資料一律走新增 Go endpoint（Wails binding），不前端直接 fetch**。

預期成果：選取遊戲時，主視覺右上出現「最新情報」面板，列出該遊戲的公告/活動/資訊（點擊外開官方頁），含載入中／空／錯誤狀態；資料來源乾淨的 4 款（原神/星穹/絕區零/鳴潮）走公開端點，Endfield 走官網 HTML 爬取（脆弱、優雅降級）。

## 研究結論（資料源可行性，2026-06-03 web research spike）

> 遵守 memory `feedback_collapse_reference.md`：HoYoverse 以參照 **Collapse Launcher** 原始碼／社群文件為準、不對 HoYoverse API 做實驗性 live 探測；Kuro/Hypergryph 輕量參照。

| 遊戲 | 公開情報來源 | 倒數起訖時間？ |
|---|---|---|
| 原神/星穹/絕區零（HoYoverse）| HoYoPlay 情報內容（Collapse `LauncherNewsURL`）回 `content.posts[]`（type/title/link/date/縮圖）+ `content.banners[]` | ❌ 僅顯示日期字串、無起訖 |
| 鳴潮（Kuro）| 免登入 CDN JSON `…/launcher/50004_…/G153/information/<lang>.json` → `guidance.{activity,notice,news}` + `slideshow` | ❌ 僅 `time` 顯示字串 |
| Endfield（Hypergryph）| **無乾淨公開端點**：GRYPHLINK 走需授權 batch RPC；社群封存只有下載/patch manifest | ❌ |

- **倒數 pill 全 5 款 NOT-FEASIBLE（免登入下）**：唯一含真實 banner `start/end_timestamp` 的是 HoYoverse `act_calendar`，需 HoYoLab cookie + DS 簽章（違反免登入＋collapse-reference）。其餘平面只有顯示用日期字串。→ **砍掉倒數 pill**，主視覺左上維持留空（同 P1）。
- **使用者拍板**：Endfield 改走**爬官網 `endfield.gryphline.com/<lang>/news` 的 HTML**（已知脆弱、隨改版維護，接受）。

## 使用者已拍板的範圍決策

- **倒數 pill 砍掉**，左上留空（資料不可行；未來若願意接帶登入的 act_calendar 再議）。
- **Endfield 爬官網 news HTML**（非「不做面板」、非「靜態連結」）。
- **架構 = per-provider 可選介面（方案 A）**：新增 `core.NewsProvider`，App 開 Wails binding 呼叫，各 provider 自行抓取。契合既有 `InstallLocator`/`LastPlayedProbe` 格局；否決 app 中央抓取（各家 API 差異大、集中反亂）。
- **需資料走 Go binding、不前端 fetch**（P1 鐵則）。

## 設計

### A. 核心型別與可選介面 — `internal/core`

放 `internal/core/provider.go`（與 `InstallLocator`/`LastPlayedProbe` 同檔同格）：

```go
// NewsCategory groups a news item for the NewsPanel filter (全部/公告/活動).
type NewsCategory string

const (
	NewsAnnounce NewsCategory = "announce" // 公告
	NewsActivity NewsCategory = "activity" // 活動
	NewsInfo     NewsCategory = "info"     // 資訊（前端只在「全部」顯示）
)

// NewsItem is one entry in a game's news feed (announcements / activities / info).
type NewsItem struct {
	Title     string       `json:"title"`
	Category  NewsCategory `json:"category"`
	Date      string       `json:"date"`                // 來源提供的顯示字串，原樣帶過（不解析、不重格式化）
	URL       string       `json:"url"`                 // 點擊：外開瀏覽器
	Thumbnail string       `json:"thumbnail,omitempty"` // 部分來源無逐項縮圖（如鳴潮）→ 前端 placeholder
}

// NewsProvider is an optional Provider capability: fetch a game's public news
// feed (no auth). lang is the app UI language (zh-TW/zh-CN/en); the provider
// maps it to its own source language code. Best-effort: a fetch/parse failure
// returns an error (the App surfaces it as an error/empty state, never fatal).
type NewsProvider interface {
	GetNews(ctx context.Context, gid GameID, lang string) ([]NewsItem, error)
}
```

> mockup 的「熱門紅點」無公開資料來源 → 不做（YAGNI），`NewsItem` 不含 Hot 欄位。

### B. App binding — `internal/app`

- 新增 `func (a *App) GetNews(gameID string) ([]core.NewsItem, error)`：
  - `provider(gid)` 取 provider（沿用既有 error 早退）。
  - type-assert `core.NewsProvider`；**未實作 → 回 `([]core.NewsItem{}, nil)`**（優雅降級，非錯誤）。
  - lang 取 `a.settings.App.Language`（讀取走 `settingsMu.RLock`，與既有讀取一致）；傳給 `GetNews`，由各 provider 自行 map。
  - 用帶 timeout 的 ctx（如 `context.WithTimeout(a.ctx, 15s)`）；網路錯誤回傳 error（前端顯示錯誤/空狀態），App 記 log、不 panic。
- **外開連結 binding**（repo 現無任何 `BrowserOpenURL`/external-open）：新增 `func (a *App) OpenExternalURL(rawURL string) error`，內部包 Wails `wruntime.BrowserOpenURL(a.ctx, rawURL)`；**僅允許 `http`/`https`**（`url.Parse` 後檢 scheme，拒其他，避免任意協定外開）。

### C. Provider 實作（各新 `news.go` + `news_test.go`）

- **hoyoverse**（重用 `apiClient`/`apiEnvelope`）：呼叫 HoYoPlay 情報內容端點解析 `content.posts[]`。
  > **端點待釘死**：Collapse 模型分 `LauncherSpriteURL`（backgrounds，= 我們已用的 `getAllGameBasicInfo`）與 `LauncherNewsURL`（content）。content 區可能在 `getAllGameBasicInfo` 同回應、或 sibling `getGameContent`，**plan 階段對照 Collapse `HypApiLoader.cs` 釘死確切 path 與 query**。欄位形狀已確認。
  - 映射：`type` → `POST_TYPE_ANNOUNCE→announce` / `POST_TYPE_ACTIVITY→activity` / `POST_TYPE_INFO→info`；`title`→Title；`link`→URL；`date`→Date；縮圖 `url`/`hover_url`→Thumbnail。
  - lang map：zh-TW→`zh-tw`、zh-CN→`zh-cn`、en→`en-us`（未知→`en-us`）。
- **kurogames**（新 `news.go`）：GET `https://prod-alicdn-gamestarter.kurogame.com/launcher/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/G153/information/<lang>.json`。
  - 映射：`guidance.notice.contents[]`→announce、`guidance.activity.contents[]`→activity、`guidance.news.contents[]`→info；每項 `content`→Title、`jumpUrl`→URL、`time`→Date；無逐項縮圖 → Thumbnail 空。
  - lang map：zh-TW→`zh-Hant`、zh-CN→`zh-Hans`、en→`en`（**確切 slug plan 階段對照來源釘死**；未知→`en`）。
- **hypergryph**（新 `news.go`）：**爬 `https://endfield.gryphline.com/<lang>/news` 的 HTML**。
  - 用 `golang.org/x/net/html`（**go.mod 既有 indirect dep `golang.org/x/net v0.35.0`**，提升為直接相依、零新模組）解析；抽出新聞項的標題/日期/連結/縮圖。
  - **已知脆弱**：實際 DOM 結構需 **impl 時實查活頁**定案選擇器；spec 不臆測選擇器。任何 fetch/parse 失敗或結構不符 → 回 `([]core.NewsItem{}, nil)`（走空狀態，不報致命錯）。category 預設 `info`（官網未必分類）；lang map：zh-TW/zh-CN/en → 官網對應 locale 段（plan 釘死）。
  - spec 明列：此爬取隨官網改版易壞，屬接受的漸進降級（最差顯示「暫無情報」）。

> 各 provider `GetNews` 僅做 HTTP/parse、無狀態、`context` 可取消；HTTP client 沿用各 provider 既有 `httpClient` nil-fallback 模式。

### D. 前端

- 新 `stores/news.ts`（Pinia）：per-gid 狀態 `{ items: NewsItem[], loading: bool, error: bool, loaded: bool }`。
  - `load(gid)`：**惰性**——該 gid 已 `loaded` 則不重抓；否則 set loading→呼叫 `GetNews(gid)`→填 items/clear loading；catch→set error。
  - Topbar 重新整理時**清空快取**（比照既有 store 在 refresh 鏈被重置的模式；plan 對照 `Topbar.onRefresh` 鏈釘確切接點）。
- 新 `components/NewsPanel.vue`：mount 在 `DetailView` 右上（目前 `DetailView.vue` 是空 placeholder，P1 已預留右上給 P2）。
  - 標頭：bell icon +「最新情報」。
  - 篩選 pill：`全部 / 公告 / 活動`（選中金）。`info` 類別**只在「全部」**出現（公告=announce、活動=activity）。
  - 列表項：58×42 縮圖（無 → placeholder）＋類別標籤（公告金/活動藍）＋日期 mono＋兩行標題 clamp。點擊 → `OpenExternalURL(item.url)`。
  - 底部：「查看全部情報 ›」→ 開該遊戲官方情報頁（per-backend 靜態 URL，best-effort；無則隱藏此列）。
  - **三狀態（mockup §3.5 強制）**：載入中 skeleton／空狀態「暫無情報」／錯誤狀態（不開天窗）。
  - 監看 `games.selected` 變更 → 呼叫 `news.load(selectedGid)`。
- 縮圖 = 外部 CDN URL 直接 `<img :src>`（同既有 background 由 CDN 載入的做法；webview 已允許外部圖）。
- i18n：新增 keys（標頭、三個篩選、空/錯誤狀態、查看全部、類別標籤），**zh-TW/zh-CN/en 三檔 parity**（`i18n_parity.test.ts` 守）。

### E. 測試

- **Go**（`go test ./...`，drop `-race`，本機 CGO_ENABLED=0）：
  - 各 provider `news_test.go`：用 httptest server／靜態 fixture 餵回應，斷言 parse→`[]NewsItem` 映射正確（類別/標題/URL/日期/縮圖）+ lang map。Endfield 存一份**範例 HTML fixture** 測爬取映射 + 壞 HTML → 回空。
  - app：`GetNews` 路由到 provider、未實作 `NewsProvider`→回空；`OpenExternalURL` 拒非 http(s) scheme。
- **前端**（vitest，沿用既有模式）：
  - `NewsPanel`：載入中/空/錯誤/列表渲染；篩選 pill（全部含 info、公告/活動過濾）；點擊呼叫外開。
  - `news` store：惰性抓 + 已 loaded 不重抓 + Refresh 清空。
  - i18n parity（新 key 三檔齊）。

### F. 非目標（明確排除）

- 活動倒數 pill（資料不可行）、主視覺左上標題/副標（P1 已定不放）。
- banner/slideshow 輪播（mockup NewsPanel §3.5 是純列表，YAGNI；`banners[]`/`slideshow[]` 不解析）。
- 熱門紅點（無公開資料）。
- 帳號晶片/開拓力/抽卡分析（P3）。
- 帶登入的 act_calendar 倒數（未來議題）。

## 起點與分支衛生

目前在 `dev`（last-played-p2 已合 `4d526ed`，工作樹乾淨）。依 memory `feedback_commits.md`（main + feature branch + `merge --no-ff`、無 `Co-Authored-By`），從 `dev` 開 feature 分支 `news-panel-p2`。spec/plan 提交於此分支；完成後 `--no-ff` 併回 `dev`。

## 風險與注意

- **Endfield HTML 爬取最脆弱**：選擇器須 impl 實查活頁；任何失敗一律降級回空狀態，不得讓 NewsPanel 崩或卡 loading。
- **HoYoverse 端點未百分百釘死**（getAllGameBasicInfo content 區 vs getGameContent）——plan 階段對照 Collapse 確認；欄位形狀已確認，端點選擇不影響資料模型。
- 各家 lang slug（鳴潮 `zh-Hant`、Endfield locale 段）須 plan/impl 對照來源實值釘死。
- `OpenExternalURL` 須限 http(s)，避免任意協定外開。
- 網路抓取一律帶 timeout，避免 binding 卡住 UI；前端 loading 狀態覆蓋延遲。

## 驗證方式

1. `go test ./...`（drop `-race`）— 各 provider news parse + app GetNews/OpenExternalURL 測試綠。
2. 前端 `npm run test`（vitest）— NewsPanel 三狀態/篩選 + news store + i18n parity 綠。
3. `wails dev` 實機：
   - 選原神/星穹/絕區零 → 右上出現最新情報列表，公告/活動篩選正確，點擊外開官方頁。
   - 選鳴潮 → 出現情報（無逐項縮圖走 placeholder）。
   - 選 Endfield → 若爬取成功顯示列表；失敗顯示「暫無情報」空狀態、不崩。
   - 切換語言 → 情報語言跟著變（各 provider lang map 生效）。
   - Topbar 重新整理 → 情報重抓。
