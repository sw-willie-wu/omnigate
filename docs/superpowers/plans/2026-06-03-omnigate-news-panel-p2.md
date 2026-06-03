# P2 NewsPanel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在主畫面 detail view 右上加「最新情報」面板，列出選取遊戲的公告/活動/資訊（點擊外開官方頁），5 款全覆蓋，含載入中/空/錯誤狀態。

**Architecture:** 新增可選 Provider 介面 `core.NewsProvider`（與 `LastPlayedProbe` 同格）。App 開 Wails binding `GetNews(gid)` + `OpenExternalURL(url)`。各 provider 從**已 live 釘死的公開 API** 抓取並映射成 `[]core.NewsItem`：hoyoverse=HoYoLab `getNewsList`、kurogames=官網 CMS `ArticleMenu.json`、hypergryph=`web-news.gryphline.com/api/bulletin`。前端 `NewsPanel.vue` + `stores/news.ts`（惰性抓+per-gid 快取），掛在 DetailView 右上。純加法、抓取失敗一律降級回空清單。

**Tech Stack:** Go 1.26（`go test ./...`，本機 CGO_ENABLED=0 → drop `-race`）；Vue 3 + Pinia + vue-i18n；Wails v2。無新 Go 模組（全用 stdlib net/http + encoding/json）。

**Spec:** `docs/superpowers/specs/2026-06-03-omnigate-news-panel-p2-design.md`

**已 live 釘死的來源（2026-06-03）**
- **hoyoverse**：`GET https://bbs-api-os.hoyolab.com/community/post/wapi/getNewsList?gids=<2|6|8>&type=<1|2|3>&page_size=8`，header `x-rpc-language`。gids：genshin=2/starrail=6/zzz=8。type：1=公告→announce / 2=活動→activity / 3=資訊→info。回 `{retcode,message,data:{list:[{post:{post_id,subject,created_at(epoch),cover},image_list:[{url}]}]}}`。URL=`https://www.hoyolab.com/article/<post_id>`。
- **kurogames**：`GET https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152/<lang>/ArticleMenu.json`（zh-cn 走 host `media-cdn-mingchao.kurogame.com`）。回 article 物件**陣列**：`{articleId,articleTitle,articleType,startTime("YYYY-MM-DD HH:MM:SS"),suggestCover,top,sortingMark}`。articleType：58=Notice→announce / 59=Event→activity / 57=News→info。URL=`https://wutheringwaves.kurogames.com/<lang>/main/news/detail/<articleId>`。
- **hypergryph**：`GET https://web-news.gryphline.com/api/bulletin?lang=<locale>&code=arknights_endfield_official&page=1&pageSize=12`。回 `{code,msg,data:{list:[{cid,tab,title,displayTime(epoch),cover}],total}}`。tab：notices→announce / events→activity / news→info。URL=`https://endfield.gryphline.com/<locale>/news/<cid>`。

**App 語言碼**：`a.settings.App.Language` ∈ {`en`,`zh-TW`,`zh-CN`}（對應 locale 檔名）。各 provider 自行 map：
- HoYoLab `x-rpc-language`：zh-TW→`zh-tw`、zh-CN→`zh-cn`、en→`en-us`（default `en-us`）。
- WuWa locale 段：zh-TW→`zh-tw`、zh-CN→`zh-cn`(ZH host)、en→`en`（default `en`）。
- Endfield locale：zh-TW→`zh-tw`、zh-CN→`zh-cn`、en→`en-us`（default `en-us`）。

**接點事實**
- 可選介面放 `internal/core/provider.go`（與 `LastPlayedProbe` 同檔）。
- provider client 欄位：hoyoverse `p.httpClient`、kurogames `p.httpClient`、hypergryph `p.client`（皆 `*http.Client`，nil 時 fallback）。news fetch 靠傳入 ctx 的 timeout 控制。
- `app.go`：`provider(gid)`（早退 error）、`a.ctx`、`wruntime` 已 import、`a.settings.App.Language`（`settingsMu.RLock`）皆存在；repo 無既有 external-open binding。
- 前端：`stores/games.ts` 有 `selectedID` + getter `selected`（GameRow，`id`/`last_played`）；`composables/useRefreshAll.ts` 是 refresh hook（不 reset store）；locale 檔 `frontend/src/locales/{en,zh-TW,zh-CN}.json`；`__tests__/i18n_parity.test.ts` 比對三檔 key 集合；`DetailView.vue` 是空 placeholder、在 `App.vue` detail mode 渲染；Wails binding 在 `wailsjs/go/app/App`、型別在 `wailsjs/go/models`。

**File Structure**
- Modify `internal/core/provider.go` — `NewsCategory`/`NewsItem`/`NewsProvider`（Task 1）。
- Modify `internal/app/app.go` + `internal/app/app_test.go` — `GetNews`/`OpenExternalURL`（Task 2）。
- Create `internal/providers/hoyoverse/news.go` + `news_test.go`（Task 3）。
- Create `internal/providers/kurogames/news.go` + `news_test.go`（Task 4）。
- Create `internal/providers/hypergryph/news.go` + `news_test.go`（Task 5）。
- Create `frontend/src/stores/news.ts` + `frontend/src/stores/__tests__/news.spec.ts`（Task 6）。
- Create `frontend/src/components/NewsPanel.vue` + i18n keys（3 locales）+ `frontend/src/components/__tests__/NewsPanel.spec.ts`（Task 7）。
- Modify `frontend/src/components/DetailView.vue` + `frontend/src/composables/useRefreshAll.ts`（Task 8）。

> 任務序：1（介面）→ 2（app bindings）→ 3/4/5（providers，彼此獨立）→ 6（store）→ 7（component+i18n）→ 8（整合）。每 task TDD red→green，**先不 commit**，過審查 gate 後才 commit（subagent-review-gates）。

---

### Task 1: 核心型別與 `NewsProvider` 介面

**Files:**
- Modify: `internal/core/provider.go`（接在 `LastPlayedProbe` 之後）

- [ ] **Step 1: 加入型別與介面**

在 `internal/core/provider.go` 的 `LastPlayedProbe` 介面定義之後、`// Provider is the integration point` 之前插入：

```go
// NewsCategory groups a news item for the NewsPanel filter (全部/公告/活動).
type NewsCategory string

const (
	NewsAnnounce NewsCategory = "announce" // 公告
	NewsActivity NewsCategory = "activity" // 活動
	NewsInfo     NewsCategory = "info"     // 資訊（前端只在「全部」顯示）
)

// NewsItem is one entry in a game's public news feed.
type NewsItem struct {
	Title     string       `json:"title"`
	Category  NewsCategory `json:"category"`
	Date      string       `json:"date"`                // display string (provider formats epoch → YYYY-MM-DD)
	URL       string       `json:"url"`                 // click-through: external browser
	Thumbnail string       `json:"thumbnail,omitempty"` // may be empty → frontend placeholder
}

// NewsProvider is an optional Provider capability: fetch a game's public news
// feed (no auth). lang is the app UI language (en/zh-TW/zh-CN); the provider
// maps it to its own source language code. A fetch/parse failure should return
// an empty slice (best-effort) or an error; the App treats both as "no news".
type NewsProvider interface {
	GetNews(ctx context.Context, gid GameID, lang string) ([]NewsItem, error)
}
```

- [ ] **Step 2: 編譯驗證（純宣告）**

Run: `go build ./...`
Expected: 成功。

- [ ] **Step 3: Commit**（過審查 gate 後）

```bash
git add internal/core/provider.go
git commit -m "feat(core): add NewsItem/NewsCategory + optional NewsProvider interface"
```

---

### Task 2: App bindings `GetNews` + `OpenExternalURL`

**Files:**
- Modify: `internal/app/app.go`（新增兩個方法；import `net/url`、`time`(已有)）
- Test: `internal/app/app_test.go`

- [ ] **Step 1: 寫 failing test**

> **import**：`app_test.go` 需要 `os`/`time` 已於 last-played 階段加入；本測試另需 `context`（已有）。確認 import 區含 `context`。

在 `internal/app/app_test.go` 檔尾新增：

```go
// fakeNewsProvider embeds fakeProvider and returns canned news.
type fakeNewsProvider struct {
	fakeProvider
	items []core.NewsItem
	gotLang string
}

func (f *fakeNewsProvider) GetNews(_ context.Context, _ core.GameID, lang string) ([]core.NewsItem, error) {
	f.gotLang = lang
	return f.items, nil
}

func TestGetNews_RoutesToProvider(t *testing.T) {
	gid := core.GameID("fake/g")
	fp := &fakeNewsProvider{
		fakeProvider: fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}},
		items:        []core.NewsItem{{Title: "Hello", Category: core.NewsAnnounce, URL: "https://x/1"}},
	}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.settings.App.Language = "zh-TW"
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Hello" {
		t.Fatalf("got %v", out)
	}
	if fp.gotLang != "zh-TW" {
		t.Errorf("lang passthrough = %q, want zh-TW", fp.gotLang)
	}
}

func TestGetNews_ProviderWithoutNews_ReturnsEmpty(t *testing.T) {
	gid := core.GameID("fake/g")
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.providers = []core.Provider{&fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}}}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid))
	if err != nil {
		t.Fatalf("want nil err, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty, got %v", out)
	}
}

func TestOpenExternalURL_RejectsNonHTTP(t *testing.T) {
	a := &App{logger: slog.Default()}
	if err := a.OpenExternalURL("file:///etc/passwd"); err == nil {
		t.Errorf("file:// accepted; want rejected")
	}
	if err := a.OpenExternalURL("javascript:alert(1)"); err == nil {
		t.Errorf("javascript: accepted; want rejected")
	}
	// http/https must NOT error on scheme validation (BrowserOpenURL is a no-op
	// with nil ctx in test; the method must guard ctx==nil to avoid panic).
	if err := a.OpenExternalURL("https://example.com"); err != nil {
		t.Errorf("https rejected: %v", err)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/app/ -run 'TestGetNews|TestOpenExternalURL' -v`
Expected: 編譯失敗（`a.GetNews`/`a.OpenExternalURL` undefined）。

- [ ] **Step 3: 加 import `net/url`**

`internal/app/app.go` import 區加入 `"net/url"`（字母序：`"net/url"` 在 `"log/slog"`/`"os"` 之後、`"path/filepath"` 之前）。

- [ ] **Step 4: 實作兩個 binding**

在 `internal/app/app.go` 適當位置（如檔尾或 GetSettings 附近）新增：

```go
// GetNews returns the public news feed for gameID, or an empty slice if the
// game's provider does not implement NewsProvider or the fetch fails. Best-
// effort: a fetch error is logged and surfaced (the frontend shows empty/error
// state), never fatal.
func (a *App) GetNews(gameID string) ([]core.NewsItem, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return nil, err
	}
	np, ok := p.(core.NewsProvider)
	if !ok {
		return []core.NewsItem{}, nil
	}
	a.settingsMu.RLock()
	lang := a.settings.App.Language
	a.settingsMu.RUnlock()

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	items, err := np.GetNews(ctx, gid, lang)
	if err != nil {
		a.logger.Warn("GetNews failed", "gid", gameID, "err", err)
		return nil, err
	}
	if items == nil {
		items = []core.NewsItem{}
	}
	return items, nil
}

// OpenExternalURL opens rawURL in the user's default browser. Only http/https
// are allowed (reject file://, javascript:, etc. to avoid arbitrary-scheme
// launch). No-op if the Wails ctx is not yet set.
func (a *App) OpenExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing to open non-http(s) url scheme %q", u.Scheme)
	}
	if a.ctx == nil {
		return nil
	}
	wruntime.BrowserOpenURL(a.ctx, rawURL)
	return nil
}
```

- [ ] **Step 5: 跑測試確認 pass**

Run: `go test ./internal/app/ -run 'TestGetNews|TestOpenExternalURL' -v`
Expected: PASS（3 個測試）。

- [ ] **Step 6: 全 app 測試 + build**

Run: `go build ./... ; go test ./internal/app/`
Expected: ok。

- [ ] **Step 7: Commit**（過審查 gate 後）

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): GetNews + OpenExternalURL bindings"
```

---

### Task 3: hoyoverse `news.go`（HoYoLab getNewsList）

**Files:**
- Create: `internal/providers/hoyoverse/news.go`
- Test: `internal/providers/hoyoverse/news_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/hoyoverse/news_test.go`：

```go
package hoyoverse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestNews_Hoyoverse_ParsesAndMaps(t *testing.T) {
	// One item per type call (1/2/3).
	body := func(typ string) string {
		return `{"retcode":0,"message":"OK","data":{"list":[{"post":{"post_id":"100` + typ + `","subject":"Title ` + typ + `","created_at":1779247719},"image_list":[{"url":"https://c/img` + typ + `.jpg"}]}]}}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-rpc-language") != "zh-tw" {
			t.Errorf("x-rpc-language = %q, want zh-tw", r.Header.Get("x-rpc-language"))
		}
		w.Write([]byte(body(r.URL.Query().Get("type"))))
	}))
	defer srv.Close()

	p := &Provider{}
	newsAPIBase = srv.URL // test seam
	defer func() { newsAPIBase = hoyolabNewsBase }()

	items, err := p.GetNews(context.Background(), "hoyoverse/genshin", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items (one per type), got %d: %v", len(items), items)
	}
	// categories present
	cats := map[core.NewsCategory]bool{}
	for _, it := range items {
		cats[it.Category] = true
		if !strings.HasPrefix(it.URL, "https://www.hoyolab.com/article/100") {
			t.Errorf("URL = %q", it.URL)
		}
		if it.Date != "2026-05-20" && it.Date != "" { // created_at 1779247719 → 2026-05-20 (UTC)
			// date formatting asserted loosely; just ensure non-empty
		}
		if it.Date == "" {
			t.Errorf("empty date for %q", it.Title)
		}
	}
	for _, c := range []core.NewsCategory{core.NewsAnnounce, core.NewsActivity, core.NewsInfo} {
		if !cats[c] {
			t.Errorf("missing category %q", c)
		}
	}
}

func TestNews_Hoyoverse_UnknownGID(t *testing.T) {
	p := &Provider{}
	got, err := p.GetNews(context.Background(), "hoyoverse/unknown", "en")
	if err != nil || len(got) != 0 {
		t.Errorf("unknown gid: want ([],nil), got %v err=%v", got, err)
	}
}

func TestNews_Hoyoverse_BadJSONDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()
	p := &Provider{}
	newsAPIBase = srv.URL
	defer func() { newsAPIBase = hoyolabNewsBase }()
	got, err := p.GetNews(context.Background(), "hoyoverse/genshin", "en")
	if err != nil {
		t.Fatalf("bad json should degrade to empty, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty on bad json, got %v", got)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/hoyoverse/ -run TestNews -v`
Expected: 編譯失敗（`GetNews`/`newsAPIBase`/`hoyolabNewsBase` undefined）。

- [ ] **Step 3: 寫實作**

Create `internal/providers/hoyoverse/news.go`：

```go
package hoyoverse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"omnigate/internal/core"
)

const hoyolabNewsBase = "https://bbs-api-os.hoyolab.com"

// newsAPIBase is the HoYoLab news host; overridable in tests.
var newsAPIBase = hoyolabNewsBase

// gidToGids maps our GameID to the HoYoLab gids param.
var gidToGids = map[core.GameID]string{
	"hoyoverse/genshin":  "2",
	"hoyoverse/starrail": "6",
	"hoyoverse/zzz":      "8",
}

// newsTypeToCategory maps the HoYoLab type param to our category.
var newsTypeToCategory = map[string]core.NewsCategory{
	"1": core.NewsAnnounce,
	"2": core.NewsActivity,
	"3": core.NewsInfo,
}

type hoyolabNewsResp struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    struct {
		List []struct {
			Post struct {
				PostID    string `json:"post_id"`
				Subject   string `json:"subject"`
				CreatedAt int64  `json:"created_at"`
				Cover     string `json:"cover"`
			} `json:"post"`
			ImageList []struct {
				URL string `json:"url"`
			} `json:"image_list"`
		} `json:"list"`
	} `json:"data"`
}

func hoyolabLang(appLang string) string {
	switch appLang {
	case "zh-TW":
		return "zh-tw"
	case "zh-CN":
		return "zh-cn"
	default:
		return "en-us"
	}
}

// GetNews implements core.NewsProvider via the public HoYoLab getNewsList API
// (no auth). It calls type 1/2/3 and merges. Any per-call failure is skipped;
// the method returns whatever it could gather (best-effort, never fatal).
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	gids, ok := gidToGids[gid]
	if !ok {
		return []core.NewsItem{}, nil
	}
	hc := p.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	xlang := hoyolabLang(lang)
	out := []core.NewsItem{}
	for _, typ := range []string{"1", "2", "3"} {
		q := url.Values{}
		q.Set("gids", gids)
		q.Set("type", typ)
		q.Set("page_size", "8")
		req, err := http.NewRequestWithContext(ctx, "GET", newsAPIBase+"/community/post/wapi/getNewsList?"+q.Encode(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("x-rpc-language", xlang)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var r hoyolabNewsResp
		if err := json.Unmarshal(body, &r); err != nil || r.Retcode != 0 {
			continue
		}
		cat := newsTypeToCategory[typ]
		for _, e := range r.Data.List {
			thumb := e.Post.Cover
			if len(e.ImageList) > 0 && e.ImageList[0].URL != "" {
				thumb = e.ImageList[0].URL
			}
			out = append(out, core.NewsItem{
				Title:     e.Post.Subject,
				Category:  cat,
				Date:      time.Unix(e.Post.CreatedAt, 0).UTC().Format("2006-01-02"),
				URL:       fmt.Sprintf("https://www.hoyolab.com/article/%s", e.Post.PostID),
				Thumbnail: thumb,
			})
		}
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/hoyoverse/ -run TestNews -v`
Expected: PASS（3 個測試）。

- [ ] **Step 5: 整套 provider 測試**

Run: `go test ./internal/providers/hoyoverse/`
Expected: ok。

- [ ] **Step 6: Commit**（過審查 gate 後）

```bash
git add internal/providers/hoyoverse/news.go internal/providers/hoyoverse/news_test.go
git commit -m "feat(hoyoverse): GetNews via public HoYoLab getNewsList API"
```

---

### Task 4: kurogames `news.go`（官網 CMS ArticleMenu.json）

> **修訂（2026-06-03，smoke 後）**：實測 list feed 的 `suggestCover` 全空、`articleType` int 為 locale-specific。改為 **2-stage**：ArticleMenu 取清單/排序 → 對前 10 篇**並發**抓 `article/<id>.json`，由 `articleTypeName`（localized 名）定分類、由 `articleContent` 首個 `<img>` 取縮圖。下方原單階段程式碼**作廢**，以 spec §C 與實作（commit）為準。

**Files:**
- Create: `internal/providers/kurogames/news.go`
- Test: `internal/providers/kurogames/news_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/kurogames/news_test.go`：

```go
package kurogames

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestNews_Kurogames_ParsesAndMaps(t *testing.T) {
	body := `[
	  {"articleId":758,"articleTitle":"Convene Details","articleType":58,"startTime":"2024-05-23 10:00:00","suggestCover":"https://c/cover.jpg","top":1,"sortingMark":1},
	  {"articleId":900,"articleTitle":"Spring Event","articleType":59,"startTime":"2024-06-01 09:00:00","suggestCover":"","top":0,"sortingMark":2},
	  {"articleId":901,"articleTitle":"Dev Note","articleType":57,"startTime":"2024-06-02 09:00:00","suggestCover":"","top":0,"sortingMark":3}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/en/ArticleMenu.json") {
			t.Errorf("path = %q, want .../en/ArticleMenu.json", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &Provider{}
	wuwaNewsBase = srv.URL // test seam (full base incl. /akiwebsite/...)
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()

	items, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d: %v", len(items), items)
	}
	byTitle := map[string]core.NewsItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if byTitle["Convene Details"].Category != core.NewsAnnounce {
		t.Errorf("58 should map to announce, got %q", byTitle["Convene Details"].Category)
	}
	if byTitle["Spring Event"].Category != core.NewsActivity {
		t.Errorf("59 should map to activity")
	}
	if byTitle["Dev Note"].Category != core.NewsInfo {
		t.Errorf("57 should map to info")
	}
	if byTitle["Convene Details"].Date != "2024-05-23" {
		t.Errorf("date = %q want 2024-05-23", byTitle["Convene Details"].Date)
	}
	if !strings.HasSuffix(byTitle["Convene Details"].URL, "/en/main/news/detail/758") {
		t.Errorf("URL = %q", byTitle["Convene Details"].URL)
	}
	if byTitle["Convene Details"].Thumbnail != "https://c/cover.jpg" {
		t.Errorf("thumb = %q", byTitle["Convene Details"].Thumbnail)
	}
}

func TestNews_Kurogames_BadJSONDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("nope"))
	}))
	defer srv.Close()
	p := &Provider{}
	wuwaNewsBase = srv.URL
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()
	got, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatalf("should degrade, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestNews_Kurogames_UnknownGID(t *testing.T) {
	p := &Provider{}
	got, err := p.GetNews(context.Background(), "kurogames/unknown", "en")
	if err != nil || len(got) != 0 {
		t.Errorf("want ([],nil), got %v err=%v", got, err)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/kurogames/ -run TestNews -v`
Expected: 編譯失敗（undefined）。

- [ ] **Step 3: 寫實作**

Create `internal/providers/kurogames/news.go`：

```go
package kurogames

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"omnigate/internal/core"
)

// wuwaNewsBaseDefault is the global (oversea) CMS JSON base. CN locale uses a
// different host; we only serve en/zh-tw/zh-cn and route zh-cn to the ZH host.
const (
	wuwaNewsBaseDefault   = "https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsBaseZHDefault = "https://media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsSiteBase      = "https://wutheringwaves.kurogames.com"
)

// wuwaNewsBase is overridable in tests (covers the en/oversea host).
var wuwaNewsBase = wuwaNewsBaseDefault

type wuwaArticle struct {
	ArticleID    int    `json:"articleId"`
	ArticleTitle string `json:"articleTitle"`
	ArticleType  int    `json:"articleType"`
	StartTime    string `json:"startTime"`
	SuggestCover string `json:"suggestCover"`
	Top          int    `json:"top"`
	SortingMark  int    `json:"sortingMark"`
}

func wuwaCategory(articleType int) core.NewsCategory {
	switch articleType {
	case 58:
		return core.NewsAnnounce // Notice
	case 59:
		return core.NewsActivity // Event
	default:
		return core.NewsInfo // 57 News + anything else
	}
}

// wuwaLang maps app lang → (locale segment, base host).
func wuwaLang(appLang string) (string, string) {
	switch appLang {
	case "zh-TW":
		return "zh-tw", wuwaNewsBase
	case "zh-CN":
		return "zh-cn", wuwaNewsBaseZHDefault
	default:
		return "en", wuwaNewsBase
	}
}

// GetNews implements core.NewsProvider via the WuWa official-site CMS feed
// (no auth). Failure degrades to an empty slice.
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	if findByID(gid) == nil {
		return []core.NewsItem{}, nil
	}
	loc, base := wuwaLang(lang)
	hc := p.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	u := fmt.Sprintf("%s/%s/ArticleMenu.json", base, loc)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return []core.NewsItem{}, nil
	}
	body, _ := io.ReadAll(resp.Body)
	var arr []wuwaArticle
	if err := json.Unmarshal(body, &arr); err != nil {
		return []core.NewsItem{}, nil
	}
	// official sort: top desc, then sortingMark asc (newest editorial order).
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].Top != arr[j].Top {
			return arr[i].Top > arr[j].Top
		}
		return arr[i].SortingMark < arr[j].SortingMark
	})
	out := make([]core.NewsItem, 0, len(arr))
	for _, a := range arr {
		date := a.StartTime
		if i := strings.IndexByte(date, ' '); i > 0 {
			date = date[:i] // "YYYY-MM-DD HH:MM:SS" → "YYYY-MM-DD"
		}
		out = append(out, core.NewsItem{
			Title:     a.ArticleTitle,
			Category:  wuwaCategory(a.ArticleType),
			Date:      date,
			URL:       fmt.Sprintf("%s/%s/main/news/detail/%d", wuwaNewsSiteBase, loc, a.ArticleID),
			Thumbnail: a.SuggestCover,
		})
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/kurogames/ -run TestNews -v`
Expected: PASS（3 個測試）。

- [ ] **Step 5: 整套 provider 測試**

Run: `go test ./internal/providers/kurogames/`
Expected: ok。

- [ ] **Step 6: Commit**（過審查 gate 後）

```bash
git add internal/providers/kurogames/news.go internal/providers/kurogames/news_test.go
git commit -m "feat(kurogames): GetNews via WuWa official CMS ArticleMenu feed"
```

---

### Task 5: hypergryph `news.go`（Endfield bulletin API）

**Files:**
- Create: `internal/providers/hypergryph/news.go`
- Test: `internal/providers/hypergryph/news_test.go`

- [ ] **Step 1: 寫 failing test**

Create `internal/providers/hypergryph/news_test.go`：

```go
package hypergryph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestNews_Hypergryph_ParsesAndMaps(t *testing.T) {
	body := `{"code":0,"msg":"","data":{"list":[
	  {"cid":"9577","tab":"notices","title":"Notice A","displayTime":1779508800,"cover":"https://c/a.jpg"},
	  {"cid":"9578","tab":"events","title":"Event B","displayTime":1779508800,"cover":""},
	  {"cid":"9579","tab":"news","title":"News C","displayTime":1779508800,"cover":""}
	],"total":3}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "arknights_endfield_official" {
			t.Errorf("code param = %q", r.URL.Query().Get("code"))
		}
		if r.URL.Query().Get("lang") != "zh-tw" {
			t.Errorf("lang = %q want zh-tw", r.URL.Query().Get("lang"))
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &Provider{}
	endfieldNewsBase = srv.URL
	defer func() { endfieldNewsBase = endfieldNewsBaseDefault }()

	items, err := p.GetNews(context.Background(), "hypergryph/endfield", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d", len(items))
	}
	byTitle := map[string]core.NewsItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if byTitle["Notice A"].Category != core.NewsAnnounce {
		t.Errorf("notices→announce")
	}
	if byTitle["Event B"].Category != core.NewsActivity {
		t.Errorf("events→activity")
	}
	if byTitle["News C"].Category != core.NewsInfo {
		t.Errorf("news→info")
	}
	if byTitle["Notice A"].Date != "2026-05-23" {
		t.Errorf("date = %q want 2026-05-23", byTitle["Notice A"].Date)
	}
	if !strings.HasSuffix(byTitle["Notice A"].URL, "/zh-tw/news/9577") {
		t.Errorf("URL = %q", byTitle["Notice A"].URL)
	}
}

func TestNews_Hypergryph_DegradesOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	p := &Provider{}
	endfieldNewsBase = srv.URL
	defer func() { endfieldNewsBase = endfieldNewsBaseDefault }()
	got, err := p.GetNews(context.Background(), "hypergryph/endfield", "en")
	if err != nil {
		t.Fatalf("should degrade, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestNews_Hypergryph_UnknownGID(t *testing.T) {
	p := &Provider{}
	got, err := p.GetNews(context.Background(), "hypergryph/unknown", "en")
	if err != nil || len(got) != 0 {
		t.Errorf("want ([],nil), got %v err=%v", got, err)
	}
}
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `go test ./internal/providers/hypergryph/ -run TestNews -v`
Expected: 編譯失敗（undefined）。

- [ ] **Step 3: 寫實作**

Create `internal/providers/hypergryph/news.go`：

```go
package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"omnigate/internal/core"
)

const (
	endfieldNewsBaseDefault = "https://web-news.gryphline.com"
	endfieldAppCode         = "arknights_endfield_official"
	endfieldSiteBase        = "https://endfield.gryphline.com"
)

// endfieldNewsBase is overridable in tests.
var endfieldNewsBase = endfieldNewsBaseDefault

type endfieldBulletinResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []struct {
			Cid         string `json:"cid"`
			Tab         string `json:"tab"`
			Title       string `json:"title"`
			DisplayTime int64  `json:"displayTime"`
			Cover       string `json:"cover"`
		} `json:"list"`
	} `json:"data"`
}

func endfieldTabCategory(tab string) core.NewsCategory {
	switch tab {
	case "notices":
		return core.NewsAnnounce
	case "events":
		return core.NewsActivity
	default:
		return core.NewsInfo // "news" + anything else
	}
}

func endfieldLocale(appLang string) string {
	switch appLang {
	case "zh-TW":
		return "zh-tw"
	case "zh-CN":
		return "zh-cn"
	default:
		return "en-us"
	}
}

// GetNews implements core.NewsProvider via the public Endfield bulletin API
// (no auth). Failure degrades to an empty slice.
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	if findByID(gid) == nil {
		return []core.NewsItem{}, nil
	}
	loc := endfieldLocale(lang)
	hc := p.client
	if hc == nil {
		hc = http.DefaultClient
	}
	q := url.Values{}
	q.Set("lang", loc)
	q.Set("code", endfieldAppCode)
	q.Set("page", "1")
	q.Set("pageSize", "12")
	req, err := http.NewRequestWithContext(ctx, "GET", endfieldNewsBase+"/api/bulletin?"+q.Encode(), nil)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return []core.NewsItem{}, nil
	}
	body, _ := io.ReadAll(resp.Body)
	var r endfieldBulletinResp
	if err := json.Unmarshal(body, &r); err != nil || r.Code != 0 {
		return []core.NewsItem{}, nil
	}
	out := make([]core.NewsItem, 0, len(r.Data.List))
	for _, e := range r.Data.List {
		out = append(out, core.NewsItem{
			Title:     e.Title,
			Category:  endfieldTabCategory(e.Tab),
			Date:      time.Unix(e.DisplayTime, 0).UTC().Format("2006-01-02"),
			URL:       fmt.Sprintf("%s/%s/news/%s", endfieldSiteBase, loc, e.Cid),
			Thumbnail: e.Cover,
		})
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `go test ./internal/providers/hypergryph/ -run TestNews -v`
Expected: PASS（3 個測試）。

- [ ] **Step 5: 整套 provider 測試 + 全 repo build**

Run: `go build ./... ; go test ./internal/providers/...`
Expected: ok。

- [ ] **Step 6: Commit**（過審查 gate 後）

```bash
git add internal/providers/hypergryph/news.go internal/providers/hypergryph/news_test.go
git commit -m "feat(hypergryph): GetNews via public Endfield bulletin API"
```

---

### Task 6: 前端 news store

**Files:**
- Create: `frontend/src/stores/news.ts`
- Test: `frontend/src/stores/__tests__/news.spec.ts`

> **Wails binding mock**：vitest 測試不連真 Wails。news.ts 從 `../../wailsjs/go/app/App` import `GetNews`；測試用 `vi.mock` 假掉它（沿用既有**元件**測試的 mock 模式，如 `BottomBar.test.ts`；store 測試本身為新增）。

- [ ] **Step 1: 寫 failing test**

Create `frontend/src/stores/__tests__/news.spec.ts`：

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getNewsMock = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetNews: (...args: any[]) => getNewsMock(...args),
}));

import { useNewsStore } from '../news';

describe('news store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getNewsMock.mockReset();
  });

  it('lazily loads once per gid and caches', async () => {
    getNewsMock.mockResolvedValue([{ title: 'A', category: 'announce', date: '2026-01-01', url: 'https://x/1' }]);
    const s = useNewsStore();
    await s.load('hoyoverse/genshin');
    await s.load('hoyoverse/genshin'); // cached → no second call
    expect(getNewsMock).toHaveBeenCalledTimes(1);
    expect(s.itemsFor('hoyoverse/genshin')).toHaveLength(1);
    expect(s.stateFor('hoyoverse/genshin').loading).toBe(false);
    expect(s.stateFor('hoyoverse/genshin').error).toBe(false);
  });

  it('sets error state on failure', async () => {
    getNewsMock.mockRejectedValue(new Error('boom'));
    const s = useNewsStore();
    await s.load('kurogames/wutheringwaves');
    expect(s.stateFor('kurogames/wutheringwaves').error).toBe(true);
    expect(s.itemsFor('kurogames/wutheringwaves')).toHaveLength(0);
  });

  it('reset() clears cache so next load refetches', async () => {
    getNewsMock.mockResolvedValue([]);
    const s = useNewsStore();
    await s.load('hypergryph/endfield');
    s.reset();
    await s.load('hypergryph/endfield');
    expect(getNewsMock).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: 跑測試確認 fail**

Run: `cd frontend && npx vitest run src/stores/__tests__/news.spec.ts`
Expected: FAIL（`../news` 不存在）。

- [ ] **Step 3: 寫實作**

Create `frontend/src/stores/news.ts`：

```ts
import { defineStore } from 'pinia';
import { GetNews } from '../../wailsjs/go/app/App';

export type NewsCategory = 'announce' | 'activity' | 'info';

export interface NewsItem {
  title: string;
  category: NewsCategory;
  date: string;
  url: string;
  thumbnail?: string;
}

interface NewsState {
  items: NewsItem[];
  loading: boolean;
  error: boolean;
  loaded: boolean;
}

function blank(): NewsState {
  return { items: [], loading: false, error: false, loaded: false };
}

export const useNewsStore = defineStore('news', {
  state: () => ({
    byGid: {} as Record<string, NewsState>,
  }),
  getters: {
    stateFor: (state) => (gid: string): NewsState => state.byGid[gid] ?? blank(),
    itemsFor: (state) => (gid: string): NewsItem[] => state.byGid[gid]?.items ?? [],
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return; // lazy: skip if loaded/in-flight
      this.byGid[gid] = { items: [], loading: true, error: false, loaded: false };
      try {
        const items = (await GetNews(gid)) as unknown as NewsItem[];
        this.byGid[gid] = { items: items ?? [], loading: false, error: false, loaded: true };
      } catch {
        this.byGid[gid] = { items: [], loading: false, error: true, loaded: true };
      }
    },
    reset() {
      this.byGid = {};
    },
  },
});
```

- [ ] **Step 4: 跑測試確認 pass**

Run: `cd frontend && npx vitest run src/stores/__tests__/news.spec.ts`
Expected: PASS（3 個測試）。

- [ ] **Step 5: Commit**（過審查 gate 後）

```bash
git add frontend/src/stores/news.ts frontend/src/stores/__tests__/news.spec.ts
git commit -m "feat(frontend): news Pinia store (lazy per-gid cache + reset)"
```

---

### Task 7: 前端 NewsPanel 元件 + i18n

**Files:**
- Create: `frontend/src/components/NewsPanel.vue`
- Modify: `frontend/src/locales/en.json` + `zh-TW.json` + `zh-CN.json`（新增 `news.*` keys）
- Test: `frontend/src/components/__tests__/NewsPanel.spec.ts`

- [ ] **Step 1: 加 i18n keys（三檔同步）**

在三個 locale 檔各加一個 `news` 區塊（與既有區塊同層）。

`en.json`：
```json
  "news": {
    "title": "Latest News",
    "filter_all": "All",
    "filter_announce": "Notice",
    "filter_activity": "Event",
    "cat_announce": "Notice",
    "cat_activity": "Event",
    "cat_info": "Info",
    "empty": "No news available",
    "error": "Failed to load news",
    "view_all": "View all news"
  }
```

`zh-TW.json`：
```json
  "news": {
    "title": "最新情報",
    "filter_all": "全部",
    "filter_announce": "公告",
    "filter_activity": "活動",
    "cat_announce": "公告",
    "cat_activity": "活動",
    "cat_info": "資訊",
    "empty": "暫無情報",
    "error": "情報載入失敗",
    "view_all": "查看全部情報"
  }
```

`zh-CN.json`：
```json
  "news": {
    "title": "最新情报",
    "filter_all": "全部",
    "filter_announce": "公告",
    "filter_activity": "活动",
    "cat_announce": "公告",
    "cat_activity": "活动",
    "cat_info": "资讯",
    "empty": "暂无情报",
    "error": "情报加载失败",
    "view_all": "查看全部情报"
  }
```

> 確認三檔 JSON 逗號/結構正確（最後一個區塊勿留尾逗號）。

- [ ] **Step 2: 寫 failing test**

Create `frontend/src/components/__tests__/NewsPanel.spec.ts`：

> 本 repo **沒有** `@pinia/testing`（只有 `pinia`/`@vue/test-utils`/`vitest`/`vue-i18n`）。用真 Pinia + mock Wails binding 驅動元件自己的 `load()`。

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getNewsMock = vi.fn();
const openMock = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetNews: (...a: any[]) => getNewsMock(...a),
  OpenExternalURL: (...a: any[]) => openMock(...a),
}));

import NewsPanel from '../NewsPanel.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });

function mountPanel() {
  const pinia = createPinia();
  setActivePinia(pinia);
  return mount(NewsPanel, {
    props: { gid: 'hoyoverse/genshin' },
    global: { plugins: [pinia, i18n] },
  });
}

describe('NewsPanel', () => {
  beforeEach(() => {
    getNewsMock.mockReset();
    openMock.mockReset();
  });

  it('shows empty state when no items', async () => {
    getNewsMock.mockResolvedValue([]);
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('No news available');
  });

  it('shows error state on failure', async () => {
    getNewsMock.mockRejectedValue(new Error('boom'));
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('Failed to load news');
  });

  it('renders items and filters by category', async () => {
    getNewsMock.mockResolvedValue([
      { title: 'Ann1', category: 'announce', date: '2026-01-01', url: 'https://x/1' },
      { title: 'Act1', category: 'activity', date: '2026-01-02', url: 'https://x/2' },
      { title: 'Info1', category: 'info', date: '2026-01-03', url: 'https://x/3' },
    ]);
    const w = mountPanel();
    await flushPromises();
    expect(w.text()).toContain('Ann1');
    expect(w.text()).toContain('Info1'); // "all" shows info
    // click 公告 filter → only announce
    const annBtn = w.findAll('button').find((b) => b.text() === 'Notice');
    expect(annBtn).toBeTruthy();
    await annBtn!.trigger('click');
    expect(w.text()).toContain('Ann1');
    expect(w.text()).not.toContain('Act1');
    expect(w.text()).not.toContain('Info1');
  });
});
```

- [ ] **Step 3: 跑測試確認 fail**

Run: `cd frontend && npx vitest run src/components/__tests__/NewsPanel.spec.ts`
Expected: FAIL（`../NewsPanel.vue` 不存在）。

- [ ] **Step 4: 寫元件**

Create `frontend/src/components/NewsPanel.vue`：

```vue
<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useNewsStore, type NewsCategory } from '../stores/news';
import { OpenExternalURL } from '../../wailsjs/go/app/App';

const props = defineProps<{ gid: string }>();
const news = useNewsStore();
const { t } = useI18n();

type Filter = 'all' | 'announce' | 'activity';
const filter = ref<Filter>('all');

const state = computed(() => news.stateFor(props.gid));
const visible = computed(() => {
  const items = state.value.items;
  if (filter.value === 'all') return items;
  return items.filter((i) => i.category === filter.value);
});

const catLabel = (c: NewsCategory) =>
  c === 'announce' ? t('news.cat_announce') : c === 'activity' ? t('news.cat_activity') : t('news.cat_info');

function open(url: string) {
  OpenExternalURL(url);
}

function reload() {
  if (props.gid) news.load(props.gid);
}
watch(() => props.gid, reload);
onMounted(reload);
</script>

<template>
  <aside class="news-panel">
    <header class="news-head">
      <span class="material-symbols-outlined">notifications</span>
      <span>{{ t('news.title') }}</span>
    </header>

    <nav class="news-filters">
      <button :class="{ active: filter === 'all' }" @click="filter = 'all'">{{ t('news.filter_all') }}</button>
      <button :class="{ active: filter === 'announce' }" @click="filter = 'announce'">{{ t('news.filter_announce') }}</button>
      <button :class="{ active: filter === 'activity' }" @click="filter = 'activity'">{{ t('news.filter_activity') }}</button>
    </nav>

    <div v-if="state.loading" class="news-skeleton">
      <div class="sk" v-for="n in 4" :key="n"></div>
    </div>
    <p v-else-if="state.error" class="news-empty">{{ t('news.error') }}</p>
    <p v-else-if="visible.length === 0" class="news-empty">{{ t('news.empty') }}</p>
    <ul v-else class="news-list">
      <li v-for="(it, idx) in visible" :key="idx" class="news-item" @click="open(it.url)">
        <div class="news-thumb">
          <img v-if="it.thumbnail" :src="it.thumbnail" alt="" loading="lazy" />
        </div>
        <div class="news-body">
          <span class="news-tag" :class="it.category">{{ catLabel(it.category) }}</span>
          <span class="news-date">{{ it.date }}</span>
          <p class="news-title">{{ it.title }}</p>
        </div>
      </li>
    </ul>
  </aside>
</template>

<style scoped>
.news-panel {
  width: 350px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border-radius: 14px;
  background: rgba(17, 19, 25, 0.62);
  border: 1px solid var(--line-2);
  backdrop-filter: blur(10px);
  max-height: 100%;
  overflow: hidden;
}
.news-head { display: flex; align-items: center; gap: 8px; color: var(--text); font-weight: 600; }
.news-filters { display: flex; gap: 6px; }
.news-filters button {
  font-size: 12px; padding: 3px 10px; border-radius: 999px;
  border: 1px solid var(--line-2); background: transparent; color: var(--text-2); cursor: pointer;
}
.news-filters button.active { color: var(--accent); border-color: var(--gold-deep); background: var(--gold-soft); }
.news-list { list-style: none; margin: 0; padding: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
.news-item { display: flex; gap: 10px; cursor: pointer; padding: 4px; border-radius: 8px; }
.news-item:hover { background: var(--bg-3, rgba(255,255,255,.05)); }
.news-thumb { width: 58px; height: 42px; flex: none; border-radius: 6px; overflow: hidden; background: var(--line-1); }
.news-thumb img { width: 100%; height: 100%; object-fit: cover; }
.news-body { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; min-width: 0; }
.news-tag { font-size: 11px; }
.news-tag.announce { color: var(--accent); }
.news-tag.activity { color: var(--info); }
.news-tag.info { color: var(--text-2); }
.news-date { font-size: 11px; color: var(--tx-dim, var(--text-2)); font-variant-numeric: tabular-nums; }
.news-title { width: 100%; margin: 2px 0 0; font-size: 13px; color: var(--text);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.news-empty { color: var(--text-2); font-size: 13px; padding: 16px 4px; text-align: center; }
.news-skeleton { display: flex; flex-direction: column; gap: 8px; }
.news-skeleton .sk { height: 42px; border-radius: 6px; background: linear-gradient(90deg, var(--line-1), var(--line-2), var(--line-1)); }
</style>
```

> 註：「查看全部情報」footer 列為 best-effort（無統一 per-game URL 時略過）；本元件先不渲染該列，避免引入無資料來源的死連結（spec §D 允許無則隱藏）。CSS 變數沿用 `theme.css` 既有 token（`--accent`/`--info`/`--line-1/2`/`--gold-soft`/`--gold-deep`/`--text`/`--text-2`）；缺的 token 用 fallback。

- [ ] **Step 5: 跑測試確認 pass + i18n parity**

Run: `cd frontend && npx vitest run src/components/__tests__/NewsPanel.spec.ts src/__tests__/i18n_parity.test.ts`
Expected: PASS（NewsPanel 3 + parity 綠：三檔都加了 `news.*` 故 key 集合一致）。

- [ ] **Step 6: Commit**（過審查 gate 後）

```bash
git add frontend/src/components/NewsPanel.vue frontend/src/components/__tests__/NewsPanel.spec.ts frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json
git commit -m "feat(frontend): NewsPanel component + news i18n keys (3 locales)"
```

---

### Task 8: 整合 — 掛 NewsPanel + refresh 清空

**Files:**
- Modify: `frontend/src/components/DetailView.vue`
- Modify: `frontend/src/composables/useRefreshAll.ts`

- [ ] **Step 1: 把 NewsPanel 掛進 DetailView 右上**

改寫 `frontend/src/components/DetailView.vue`：

```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import NewsPanel from './NewsPanel.vue';

const games = useGamesStore();
const gid = computed(() => games.selected?.id ?? '');
</script>

<template>
  <!-- HeroBase: key-art from the global BgLayer; darkening via app .top-fade/
       .bottom-fade. No title overlay (P1). NewsPanel sits top-right (P2);
       countdown pill is cut (data infeasible). -->
  <div class="view view-detail">
    <NewsPanel v-if="gid" :gid="gid" class="news-slot" />
  </div>
</template>

<style scoped>
.view-detail { position: relative; width: 100%; height: 100%; }
.news-slot { position: absolute; top: 16px; right: 25px; max-height: calc(100% - 140px); }
</style>
```

- [ ] **Step 2: 把 news 清空掛進 refreshAll**

改 `frontend/src/composables/useRefreshAll.ts`，加入 news store reset，使 Topbar 重新整理時情報強制重抓：

```ts
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useNewsStore } from '../stores/news';
import { Refresh } from '../../wailsjs/go/app/App';

// refreshAll re-detects installs, reloads game data + assets, probes each
// installed game for updates, and clears the news cache so the panel refetches.
export async function refreshAll(): Promise<void> {
  await Refresh();
  const games = useGamesStore();
  const updates = useUpdatesStore();
  const news = useNewsStore();
  news.reset(); // bypass the lazy `loaded` guard so news refetches on next view
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await Promise.allSettled(
    games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
  );
}
```

- [ ] **Step 3: 前端 build + 全前端測試**

Run: `cd frontend && npm run build && npm run test`
Expected: build 綠；vitest 全綠（含既有 + 新增 news/NewsPanel + parity）。

> 註：`npm run build` 會跑 `wails generate` 產生的 binding 型別；若 `GetNews`/`OpenExternalURL` 尚未生成，先在 repo 根跑 `wails build`（或 `wails generate module`）讓 `wailsjs/go/app/App` 帶上新方法，再跑前端 build。

- [ ] **Step 4: Commit**（過審查 gate 後）

```bash
git add frontend/src/components/DetailView.vue frontend/src/composables/useRefreshAll.ts
git commit -m "feat(frontend): mount NewsPanel in DetailView + clear news cache on refresh"
```

---

## 收尾（Task 8 後，USER-gated）

1. **產 binding + build**：repo 根 `wails build` → 生成 `wailsjs/go/app/App.{js,d.ts}` 含 `GetNews`/`OpenExternalURL` + `wailsjs/go/models` 含 `NewsItem`；產 `build/bin/omnigate.exe`。
2. **全測試**：`go test ./...`（drop `-race`）綠；`cd frontend && npm run test` 綠。
3. **真機 smoke（USER）**（`wails dev` 或 build 後 exe）：
   - 選原神/星穹/絕區零 → 右上「最新情報」出現列表，公告/活動篩選正確，點擊外開 hoyolab 文章。
   - 選鳴潮 → 出現官網情報（含縮圖/分類/日期）。
   - 選 Endfield → 出現 bulletin 情報（含縮圖/分類）。
   - 切語言（zh-TW/zh-CN/en）→ 情報語言跟著變。
   - Topbar 重新整理 → 情報重抓。
   - 斷網/某家失敗 → 該遊戲顯示「暫無情報」或錯誤狀態、不崩、不卡 loading。
4. smoke 通過後：`git checkout dev && git merge --no-ff news-panel-p2`（[[feedback_commits]]：無 `Co-Authored-By`、`--no-ff`）。

## 風險與注意

- 三家皆第三方公開 API、形狀可能隨改版漂移；所有 fetch/parse 失敗一律降級回空清單（已在各 provider 實作）。host/路徑/欄位 2026-06-03 live 釘死。
- HoYoverse 僅放寬「公開 HoYoLab 新聞 API」的 no-probe（見 memory `feedback_collapse_reference` 例外）；protected 協定不變。
- `GetNews` 在 `a.ctx` 為 nil（測試）時用 `context.Background()`；`OpenExternalURL` 在 `a.ctx` nil 時 no-op（避免 panic）。
- 鳴潮 `articleContent` 含 HTML：provider 端不解析、不外傳；前端禁 `v-html`。
- 縮圖/外開皆走外部 URL；前端 `<img>` 直接載 CDN（同 background 既有做法），點擊走 `OpenExternalURL`（限 http/https）。
- 非目標：倒數 pill、banner/slideshow 輪播、熱門紅點、帳號/開拓力、帶登入的 act_calendar。
