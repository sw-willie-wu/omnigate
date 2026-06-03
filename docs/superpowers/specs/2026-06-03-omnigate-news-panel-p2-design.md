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
| 原神/星穹/絕區零（HoYoverse）| ✅ **公開 HoYoLab 新聞 API**（live 釘死，使用者 2026-06-03 放寬 no-probe 規則）：`GET https://bbs-api-os.hoyolab.com/community/post/wapi/getNewsList?gids=<2/6/8>&type=<1/2/3>&page_size=N`，header `x-rpc-language`，免 cookie | ❌ |
| 鳴潮（Kuro）| ✅ **官網 CMS feed**（live 釘死，使用者選用、比 launcher 豐富）：`GET https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152/<lang>/ArticleMenu.json`（CN 走 `media-cdn-mingchao…`），含分類/日期/縮圖/置頂 | ❌ 僅顯示日期 |
| Endfield（Hypergryph）| ✅ **公開 JSON API**（plan 階段 web research 釘死）：官網 v4 SPA 由 `GET https://web-news.gryphline.com/api/bulletin?lang=<locale>&code=arknights_endfield_official&page=1&pageSize=N` 取得，免登入、回 `{code,msg,data:{list:[{cid,tab,title,displayTime(epoch),cover,brief}],total}}` | ❌ |

- **倒數 pill 全 5 款 NOT-FEASIBLE（免登入下）**：唯一含真實 banner `start/end_timestamp` 的是 HoYoverse `act_calendar`，需 HoYoLab cookie + DS 簽章（違反免登入＋collapse-reference）。其餘平面只有顯示用日期字串。→ **砍掉倒數 pill**，主視覺左上維持留空（同 P1）。
- **Endfield 資料源（plan 研究更新）**：原規劃「爬官網 HTML」，但官網 v4 是 client-rendered Next.js SPA、初始 HTML 無情報內容；research 在 JS bundle 找到其背後**乾淨的公開 JSON API**（`web-news.gryphline.com/api/bulletin`，host/appCode 由 `arknights_endfield_official` config 確認，list/detail 端點 live 驗證成功，zh-tw/en-us 皆正常，且含真實 epoch 時間與縮圖）。**改採此 JSON API**——比 HTML 爬取穩定、有結構化分類（`tab`）與日期，並**移除原本的 `golang.org/x/net/html` 依賴**。HTML 爬取方案作廢。

## 使用者已拍板的範圍決策

- **倒數 pill 砍掉**，左上留空（資料不可行；未來若願意接帶登入的 act_calendar 再議）。
- **Endfield 出情報面板**（非「不做面板」、非「靜態連結」）；原選「爬官網 HTML」於 plan 研究階段升級為**公開 JSON API**（見上）——de-risk，無 HTML 選擇器脆弱性。
- **資料源升級（2026-06-03，研究後拍板）**：
  - **米哈遊**：放寬 memory `feedback_collapse_reference` 的 no-probe 規則（**僅限公開 HoYoLab 新聞 API**，protected 協定仍 no-probe），改用 **HoYoLab `getNewsList`**（比 launcher content posts 完整、含 type 分類與 epoch 時間）。memory 已加例外註記。
  - **鳴潮**：改用**官網 CMS feed `ArticleMenu.json`**（比 launcher `information.json` 完整：分類/日期/縮圖/置頂）。launcher CDN 方案作廢。
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

- **hoyoverse**（新 `news.go`；HoYoLab 公開新聞 API，**非** HoYoPlay launcher content）：`GET https://bbs-api-os.hoyolab.com/community/post/wapi/getNewsList`（**LIVE 釘死**），header `x-rpc-language: <lang>`，免 cookie。每款各呼叫 **type 1/2/3** 三次（或併發）合併。
  - query：`gids=<2=genshin|6=hsr|8=zzz>`、`type=<1=notice→announce|2=activity→activity|3=info→info>`、`page_size=8`。
  - 回應 `data.list[]`，每項 `post.{subject,post_id,created_at,cover}` + `image_list[]`。映射：`post.subject`→Title；type→Category；`post.created_at`（**epoch 秒**）→格式化 `YYYY-MM-DD`→Date；`image_list[0].url`（無則 `post.cover`）→Thumbnail；URL=`https://www.hoyolab.com/article/<post.post_id>`。
  - gids 由 gid map：`hoyoverse/genshin`→2、`hoyoverse/starrail`→6、`hoyoverse/zzz`→8（未知→跳過/空）。
  - lang map（`x-rpc-language`）：zh-TW→`zh-tw`、zh-CN→`zh-cn`、en→`en-us`（未知→`en-us`）。
  - 註：此 API 與既有 `api.go`（HoYoPlay `sg-hyp-api` 協定）是**不同 host/用途**，news 走獨立 client、不影響既有 background/version 邏輯。
- **kurogames**（新 `news.go`；官網 CMS feed，**非** launcher `information.json`）：`GET https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152/<lang>/ArticleMenu.json`（**LIVE 釘死**；CN locale 改走 host `https://media-cdn-mingchao.kurogame.com/...`）。
  - 回應是 article 物件**陣列**，每項 `articleId`、`articleTitle`、`articleType`、`createTime`、`startTime`、`sortingMark`、`suggestCover`、`top`（置頂）、`articleContent`（內含 HTML，**不使用**）。
  - 映射：`articleType` → `58→announce`(Notice) / `59→activity`(Event) / `57→info`(News)（分類碼由同目錄 `MainMenu.json` 確認；未知碼→info）；`articleTitle`→Title；`startTime`（`YYYY-MM-DD HH:MM:SS`，取日期段）→Date；`suggestCover`→Thumbnail（可能空）；URL=`https://wutheringwaves.kurogames.com/<lang>/main/news/detail/<articleId>`。依 `top` desc + `sortingMark` 排序（比照官網 `articleSort`）。
  - lang map（LIVE 驗證）：zh-TW→`zh-tw`、zh-CN→`zh-cn`（ZH host）、en→`en`（未知→`en`）。
  - **不渲染 `articleContent`**（內含 HTML，避免 XSS；列表只需標題/分類/日期/縮圖）。
- **hypergryph**（新 `news.go`，**公開 JSON API、非 HTML 爬取**）：GET `https://web-news.gryphline.com/api/bulletin?lang=<locale>&code=arknights_endfield_official&page=1&pageSize=12`（**LIVE 驗證：免登入 200**）。
  - 回應 `{code:0,msg:"",data:{list:[{cid,tab,sticky,title,author,displayTime,cover,extraCover,brief}],total}}`。
  - 映射：`tab` → `notices→announce` / `events→activity` / `news→info`（未知 tab→info）；`title`→Title；`cover`→Thumbnail；`displayTime`（**epoch 秒**）→ 格式化為 `YYYY-MM-DD` 字串放 Date（與他家「原樣字串」不同，因這家給的是 epoch；格式化在 provider 端統一成日期字串）；URL = `https://endfield.gryphline.com/<locale>/news/<cid>`（詳情頁）。
  - lang map（LIVE 驗證）：zh-TW→`zh-tw`、zh-CN→`zh-cn`、en→`en-us`（未知→`en-us`）。
  - 失敗（非 200／`code!=0`／JSON 壞）→ 回 `([]core.NewsItem{}, nil)`（走空狀態，不報致命錯）。**比 HTML 爬取穩定**：有結構化分類與 epoch 時間，無選擇器脆弱性。

> 各 provider `GetNews` 僅做 HTTP/parse、無狀態、`context` 可取消；HTTP client 沿用各 provider 既有 `httpClient` nil-fallback 模式。

### D. 前端

- 新 `stores/news.ts`（Pinia）：per-gid 狀態 `{ items: NewsItem[], loading: bool, error: bool, loaded: bool }`。
  - `load(gid)`：**惰性**——該 gid 已 `loaded` 則不重抓；否則 set loading→呼叫 `GetNews(gid)`→填 items/clear loading；catch→set error。
  - Topbar 重新整理時**清空快取並強制重抓**。⚠️ **修正既有認知**：現行 refresh 鏈是 `composables/useRefreshAll.ts`（`Topbar.vue` 呼叫），它**只是冪等地重呼 `games.load()` 等、不 reset 任何 store**。因此光把 `news.load(gid)` 加進 refreshAll 會被惰性 `loaded` guard 擋成 no-op、情報變陳舊。**plan 必須在 `refreshAll` 明確加一步「清空 news store（或對當前 gid 略過 loaded guard 強制重抓）」**——repo 目前沒有任何 store reset hook 可比照。
- 新 `components/NewsPanel.vue`：mount 在 `DetailView` 右上（目前 `DetailView.vue` 是空 placeholder，P1 已預留右上給 P2）。
  - 標頭：bell icon +「最新情報」。
  - 篩選 pill：`全部 / 公告 / 活動`（選中金）。`info` 類別**只在「全部」**出現（公告=announce、活動=activity）。
  - 列表項：58×42 縮圖（無 → placeholder）＋類別標籤（公告金/活動藍）＋日期 mono＋兩行標題 clamp。點擊 → `OpenExternalURL(item.url)`。
  - 底部：「查看全部情報 ›」→ 開該遊戲官方情報頁（per-backend 靜態 URL，best-effort；無則隱藏此列）。
  - **三狀態（mockup §3.5 強制）**：載入中 skeleton／空狀態「暫無情報」／錯誤狀態（不開天窗）。
  - 監看 `games.selected` 變更 → 呼叫 `news.load(selectedGid)`。
- 縮圖 = 外部 CDN URL 直接 `<img :src>`（同既有 background 由 CDN 載入的做法；webview 已允許外部圖）。
- **安全**：news 標題/日期等來自外部 API 一律走 Vue 純文字插值 `{{ }}`（自動轉義），**禁用 `v-html`**，防外部內容 XSS。（repo 現無任何 `v-html`，維持此現況。鳴潮 `articleContent` 含 HTML，**provider 端不解析、不帶到前端**。）
- i18n：新增 keys（標頭、三個篩選、空/錯誤狀態、查看全部、類別標籤），**zh-TW/zh-CN/en 三檔 parity**（`i18n_parity.test.ts` 守）。

### E. 測試

- **Go**（`go test ./...`，drop `-race`，本機 CGO_ENABLED=0）：
  - 各 provider `news_test.go`：用 httptest server／靜態 **JSON fixture**（各家各存一份真實回應樣本）餵回應，斷言 parse→`[]NewsItem` 映射正確（類別碼對應/標題/URL 組法/epoch→日期格式化/縮圖 fallback）+ lang map。壞 JSON／非 200／`code!=0` → 回 `([],nil)` 空清單（降級）。
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

- **三家皆第三方公開 API、形狀可能隨改版漂移**：任何 fetch/parse 失敗（非 200／`retcode`/`code`!=0／JSON 壞／欄位缺）一律降級回 `([],nil)` 空清單，不得讓 NewsPanel 崩或卡 loading。三家 host/路徑/欄位皆已 **live 釘死**（2026-06-03），但非官方契約、需隨改版維護。
- **HoYoLab news API 放寬 no-probe 規則僅限此公開新聞端點**（見 memory `feedback_collapse_reference` 例外註記）；protected 協定（`sg-hyp-api` getGameBranches/Sophon）仍 no-probe。
- 各家 lang map 已 live 釘死：HoYoLab `x-rpc-language` `zh-tw/zh-cn/en-us`；鳴潮官網 `zh-tw/zh-cn(ZH host)/en`；Endfield `zh-tw/zh-cn/en-us`。
- 鳴潮 `articleContent` 含 HTML：provider 端**不解析、不外傳**（防 XSS）。
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
