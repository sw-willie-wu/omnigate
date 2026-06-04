# Gacha Analysis P2 (HoYoverse) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** Implement `core.GachaProvider` on the existing HoYoverse provider so 抽卡分析 works for Genshin / Star Rail / ZZZ, reusing the backend-agnostic foundation (store + stats engine + app bindings + Vue board) shipped in Plan 1.

**Architecture:** HoYoverse gacha URLs are NOT in a text log (unlike Endfield) — the authkey lives in the game's Chromium `webCaches/.../data_*` files, on the **gacha webview page URL** (host `gs.hoyoverse.com/...` etc.), not the API endpoint URL. We scan those binary cache files for the freshest `authkey=`+`game_biz=` URL (by its `timestamp` query param), lift its auth query (`authkey/authkey_ver/sign_type/game_biz/lang/region`), and replay it against the per-game `getGachaLog` endpoint with `gacha_type/page/size/end_id` pagination. Records normalize to `core.GachaPull`; per-game `GachaConfig` supplies banner→pity mapping.

**Tech stack:** Go (CGO_ENABLED=0), stdlib `net/http`/`net/url`/`regexp`/`os`. No new deps.

**Live-verified (2026-06-04, against StarRail on the dev machine):** endpoint hosts (below); the auth-query-lifting approach; required params; error codes `-101 authkey timeout`, `-100 authkey error`, `-111 game name error` (missing game_biz). The **success response body shape was NOT live-verified** (all cached authkeys were expired — freshest was 3 days old); it is taken from the long-stable, universally-documented getGachaLog shape (genshin-wish-export / UIGF / Collapse). Flag it for fresh-authkey smoke (HoYoverse authkey lasts ~24h, so low-pressure).

---

## Verified protocol reference

**Endpoints (host + path), per game:**
| game | gid | getGachaLog endpoint | game_biz |
|---|---|---|---|
| Genshin | `hoyoverse/genshin` | `https://public-operation-hk4e-sg.hoyoverse.com/gacha_info/api/getGachaLog` | `hk4e_global` |
| Star Rail | `hoyoverse/starrail` | `https://public-operation-hkrpg-sg.hoyoverse.com/common/hkrpg_gacha_record/api/getGachaLog` | `hkrpg_global` |
| ZZZ | `hoyoverse/zzz` | `https://public-operation-common-sg.hoyoverse.com/common/gacha_record/api/getGachaLog` | `nap_global` |

**webCaches location:** `<installDir>/<DataDir>/webCaches/<newestVersionDir>/Cache/Cache_Data/data_2` (also scan `data_1`,`data_3`). `<DataDir>` = `<ExeName without .exe>_Data` (e.g. `GenshinImpact_Data`, `StarRail_Data`, `ZenlessZoneZero_Data`). `<newestVersionDir>` = lexically/mtime newest `*.*.*.*` subdir.

**Auth-query lifting:** in the cache bytes, find URLs matching `https://[^\s"\x00]*authkey=[^\s"\x00]*`; keep those whose parsed query has BOTH `authkey` and `game_biz`; pick the one with the largest `timestamp` query value (freshest — cache holds many across sessions). Lift `authkey, authkey_ver, sign_type, game_biz, lang, region` from its query.

**getGachaLog call:** GET endpoint with the lifted auth params + `gacha_type=<code>&page=<n>&size=20&end_id=<cursor>`. Pagination: start `end_id=0`,`page=1`; each page returns ≤ size records; set `end_id` = last record's `id`, `page++`; stop when returned list < size (or empty). Sleep ~300–500ms between pages (rate-limit; user warned "一次不要拉太多不然會被擋").

**Response shape (documented; pending live smoke):**
```json
{"retcode":0,"message":"OK","data":{"page":"1","size":"20","region":"prod_official_asia",
 "list":[{"uid":"…","gacha_id":"…","gacha_type":"11","item_id":"","count":"1",
          "time":"2026-06-01 12:00:00","name":"…","lang":"zh-tw","item_type":"角色|光錐|武器",
          "rank_type":"5","id":"169…"}]}}
```
`retcode != 0` (esp. -100/-101/-111) → treat as ErrGachaURLUnavailable (expired/invalid authkey → re-open guidance).

**Per-game banner (gacha_type → bannerKey) + pity:**
- **Genshin:** `301`+`400` → `character` (cap 90, 50/50); `302` → `weapon` (cap 80, has-guarantee); `200` → `standard` (cap 90); `100` → `beginner` (cap 90); `500` → `chronicled` (cap 90). NOTE: character banner spans BOTH 301 and 400 — both map to `character` so pity merges.
- **Star Rail:** `11` → `character` (90, 50/50); `12` → `lightcone` (80); `1` → `standard` (90); `2` → `beginner` (90).
- **ZZZ:** `2` → `character` (90, 50/50); `3` → `wengine` (80); `1` → `standard` (90); `5` → `bangboo` (80).
Headline rank = **5** all games. `rank_type` is a string ("3"/"4"/"5") → parse to int.

---

## File structure
- Create `internal/providers/hoyoverse/gacha.go` — `GachaProvider` impl: webCache URL extraction, per-game `GachaConfig`+pity, `FetchGacha`.
- Create `internal/providers/hoyoverse/gacha_test.go`.
- No app/frontend/store changes — the foundation routes by gid generically once `*Provider` implements `core.GachaProvider`.

---

## Task 1: webCache auth-query extraction

**Files:** Create `internal/providers/hoyoverse/gacha.go` (extraction portion); `internal/providers/hoyoverse/gacha_test.go`.

- [ ] **Step 1: failing test** — `gacha_test.go`:
```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractAuthQuery_PicksFreshestByTimestamp(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "GenshinImpact_Data", "webCaches", "2.51.0.0", "Cache", "Cache_Data")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// two gacha page URLs with authkey+game_biz; the larger timestamp must win.
	old := `https://gs.hoyoverse.com/genshin/event/e/index.html?authkey=OLD&authkey_ver=1&sign_type=2&game_biz=hk4e_global&lang=zh-tw&region=os_asia&timestamp=1000`
	newer := `https://gs.hoyoverse.com/genshin/event/e/index.html?authkey=NEW&authkey_ver=1&sign_type=2&game_biz=hk4e_global&lang=zh-tw&region=os_asia&timestamp=2000`
	blob := "garbage\x00" + old + "\x00noise " + newer + "\x00tail"
	if err := os.WriteFile(filepath.Join(cache, "data_2"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if q.Get("authkey") != "NEW" {
		t.Fatalf("authkey=%q want NEW (freshest by timestamp)", q.Get("authkey"))
	}
	if q.Get("game_biz") != "hk4e_global" {
		t.Fatalf("game_biz=%q", q.Get("game_biz"))
	}
}

func TestExtractAuthQuery_NoneFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data"); err == nil {
		t.Fatalf("want error when no webCache/authkey present")
	}
}
```

- [ ] **Step 2: run** `go test ./internal/providers/hoyoverse/ -run TestExtractAuthQuery` → FAIL (undefined).

- [ ] **Step 3: implement** — create `internal/providers/hoyoverse/gacha.go`:
```go
package hoyoverse

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"omnigate/internal/core"
)

var hoyoAuthURLRe = regexp.MustCompile(`https://[^\s"\x00]*authkey=[^\s"\x00]*`)

// extractHoyoAuthQuery scans the game's webCaches data_* files for the freshest
// gacha page URL carrying authkey+game_biz, and returns its query values.
// installDir is the App-resolved game dir; dataDir is "<Game>_Data".
func extractHoyoAuthQuery(installDir, dataDir string) (url.Values, error) {
	root := filepath.Join(installDir, dataDir, "webCaches")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, core.ErrGachaURLUnavailable
	}
	vers := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			vers = append(vers, e.Name())
		}
	}
	if len(vers) == 0 {
		return nil, core.ErrGachaURLUnavailable
	}
	sort.Strings(vers)
	newest := vers[len(vers)-1]

	var best url.Values
	var bestTS int64 = -1
	for _, n := range []string{"data_1", "data_2", "data_3"} {
		b, err := os.ReadFile(filepath.Join(root, newest, "Cache", "Cache_Data", n))
		if err != nil {
			continue
		}
		for _, m := range hoyoAuthURLRe.FindAll(b, -1) {
			u, e := url.Parse(string(m))
			if e != nil {
				continue
			}
			q := u.Query()
			if q.Get("authkey") == "" || q.Get("game_biz") == "" {
				continue
			}
			ts, _ := strconv.ParseInt(q.Get("timestamp"), 10, 64)
			if ts > bestTS {
				bestTS, best = ts, q
			}
		}
	}
	if best == nil {
		return nil, core.ErrGachaURLUnavailable
	}
	return best, nil
}

// dataDirFor derives "<Game>_Data" from the game's exe name (e.g.
// GenshinImpact.exe → GenshinImpact_Data).
func dataDirFor(m *gameMeta) string {
	base := m.ExeName
	if i := len(base) - len(".exe"); i > 0 && base[i:] == ".exe" {
		base = base[:i]
	}
	return base + "_Data"
}
```

- [ ] **Step 4: run** → PASS. Also `go build ./...`, `go vet ./internal/providers/hoyoverse/...`, `gofmt -l`.

- [ ] **Step 5: COMMIT DEFERRED** — do not commit (controller commits after review).

---

## Task 2: Per-game GachaConfig + pity models

**Files:** append to `internal/providers/hoyoverse/gacha.go`, `internal/providers/hoyoverse/gacha_test.go`.

- [ ] **Step 1: failing test** — append to `gacha_test.go`:
```go
import "omnigate/internal/core" // (ensure imported)

func TestHoyoPityWalk(t *testing.T) {
	m := hoyoPity{cap: 90, fifty: true}
	if m.HardPity() != 90 || !m.Has5050() {
		t.Fatalf("cap/has5050 wrong")
	}
	pulls := []core.GachaPull{{ID: "1", Rank: 4}, {ID: "2", Rank: 5, Name: "X"}, {ID: "3", Rank: 4}}
	hits, trailing := m.Walk(pulls, 5)
	if len(hits) != 1 || hits[0].Count != 2 || trailing != 1 {
		t.Fatalf("hits=%+v trailing=%d", hits, trailing)
	}
}

func TestHoyoConfigBanners(t *testing.T) {
	p := New(Settings{}, nil)
	for _, tc := range []struct {
		gid   core.GameID
		banner string
	}{
		{"hoyoverse/genshin", "character"},
		{"hoyoverse/genshin", "weapon"},
		{"hoyoverse/starrail", "lightcone"},
		{"hoyoverse/zzz", "bangboo"},
	} {
		cfg := p.GachaConfig(tc.gid)
		if cfg.HeadlineRank != 5 {
			t.Fatalf("%s headline=%d", tc.gid, cfg.HeadlineRank)
		}
		if cfg.BannerOf(tc.banner) == nil {
			t.Fatalf("%s missing banner %q", tc.gid, tc.banner)
		}
	}
}

func TestGachaTypeMapsToBanner(t *testing.T) {
	// Genshin 301 and 400 both → character (merged pity).
	if bannerForGachaType("hoyoverse/genshin", "301") != "character" ||
		bannerForGachaType("hoyoverse/genshin", "400") != "character" {
		t.Fatalf("genshin 301/400 must map to character")
	}
	if bannerForGachaType("hoyoverse/starrail", "11") != "character" {
		t.Fatalf("hsr 11 → character")
	}
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — append to `gacha.go`:
```go
// hoyoPity: HoYoverse standard pity — every pull counts, reset to 0 on a headline.
type hoyoPity struct {
	cap   int
	fifty bool
}

func (m hoyoPity) HardPity() int { return m.cap }
func (m hoyoPity) Has5050() bool { return m.fifty }
func (m hoyoPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
	hits, pity := []core.PityHit{}, 0
	for _, p := range sorted {
		pity++
		if p.Rank == headline {
			hits = append(hits, core.PityHit{Pull: p, Count: pity})
			pity = 0
		}
	}
	return hits, pity
}

// gachaTypeBanner maps a per-game gacha_type code → bannerKey. Genshin 301 & 400
// both fold into "character" so the limited-character pity merges across them.
var gachaTypeBanner = map[core.GameID]map[string]string{
	"hoyoverse/genshin":  {"301": "character", "400": "character", "302": "weapon", "200": "standard", "100": "beginner", "500": "chronicled"},
	"hoyoverse/starrail": {"11": "character", "12": "lightcone", "1": "standard", "2": "beginner"},
	"hoyoverse/zzz":      {"2": "character", "3": "wengine", "1": "standard", "5": "bangboo"},
}

func bannerForGachaType(gid core.GameID, gachaType string) string {
	if m, ok := gachaTypeBanner[gid]; ok {
		if b, ok := m[gachaType]; ok {
			return b
		}
	}
	return "standard"
}

// gachaTypesToQuery lists the gacha_type codes to fetch per game (one banner page
// loop each). Genshin queries 301 AND 400 (character spans both) plus the rest.
var gachaTypesToQuery = map[core.GameID][]string{
	"hoyoverse/genshin":  {"301", "400", "302", "200", "100", "500"},
	"hoyoverse/starrail": {"11", "12", "1", "2"},
	"hoyoverse/zzz":      {"2", "3", "1", "5"},
}

func loc(zhTW, zhCN, en string) core.LocalizedString {
	return core.LocalizedString{"zh-TW": zhTW, "zh-CN": zhCN, "en": en}
}

func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	rank := map[int]core.LocalizedString{5: loc("五星", "五星", "5★"), 4: loc("四星", "四星", "4★")}
	cfg := core.GachaConfig{HeadlineRank: 5, RankLabels: rank, PullPrice: 100, Currency: "NT$", ExpectedPity: 62.5}
	switch gid {
	case "hoyoverse/genshin":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}},
			{Key: "weapon", Label: loc("武器", "武器", "Weapon"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
			{Key: "chronicled", Label: loc("集錄", "集录", "Chronicled"), Pity: hoyoPity{90, false}},
		}
	case "hoyoverse/starrail":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}},
			{Key: "lightcone", Label: loc("光錐", "光锥", "Light Cone"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
		}
	case "hoyoverse/zzz":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定代理人", "限定代理人", "Character"), Pity: hoyoPity{90, true}},
			{Key: "wengine", Label: loc("音擎", "音擎", "W-Engine"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "bangboo", Label: loc("邦布", "邦布", "Bangboo"), Pity: hoyoPity{80, false}},
		}
	}
	return cfg
}
```

- [ ] **Step 4: run** → PASS. `go vet`, `gofmt -l`.
- [ ] **Step 5: COMMIT DEFERRED.**

---

## Task 3: FetchGacha (getGachaLog pagination + normalize) + interface assertion

**Files:** append to `internal/providers/hoyoverse/gacha.go`, `internal/providers/hoyoverse/gacha_test.go`.

- [ ] **Step 1: failing test** — append to `gacha_test.go`. NOTE: consolidate ALL test imports into one block at the top of the file. The full import set across Tasks 1–3 is: `context`, `errors`, `net/http`, `net/http/httptest`, `net/url`, `os`, `path/filepath`, `testing`, `time` (only if used), and `omnigate/internal/core`. **`net/url` is required** (the tests build `url.Values`).
```go
func TestFetchGachaPaginatesNormalizes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gt := r.URL.Query().Get("gacha_type")
		endID := r.URL.Query().Get("end_id")
		if gt == "11" && endID == "0" {
			w.Write([]byte(`{"retcode":0,"message":"OK","data":{"page":"1","size":"20","region":"prod","list":[
				{"id":"1002","gacha_type":"11","rank_type":"5","item_type":"角色","name":"Alpha","time":"2026-06-01 10:00:00","uid":"800"},
				{"id":"1001","gacha_type":"11","rank_type":"4","item_type":"光錐","name":"Beta","time":"2026-06-01 09:00:00","uid":"800"}]}}`))
			return
		}
		// any other request → empty list (end of that banner / other banners)
		w.Write([]byte(`{"retcode":0,"message":"OK","data":{"page":"1","size":"20","region":"prod","list":[]}}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL } // test seam
	p.gachaPageDelay = 0
	q := url.Values{"authkey": {"K"}, "authkey_ver": {"1"}, "sign_type": {"2"}, "game_biz": {"hkrpg_global"}, "lang": {"zh-tw"}, "region": {"prod"}}

	res, err := p.fetchHoyoGacha(context.Background(), "hoyoverse/starrail", q)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Pulls) != 2 {
		t.Fatalf("pulls=%d want 2", len(res.Pulls))
	}
	var top *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].ID == "1002" {
			top = &res.Pulls[i]
		}
	}
	if top == nil || top.Rank != 5 || top.BannerKey != "character" || top.Name != "Alpha" {
		t.Fatalf("normalize wrong: %+v", top)
	}
	if res.UID != "800" {
		t.Fatalf("uid=%q want 800", res.UID)
	}
}

func TestFetchGachaAuthkeyTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"retcode":-101,"message":"authkey timeout","data":null}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	q := url.Values{"authkey": {"K"}, "game_biz": {"hkrpg_global"}}
	_, err := p.fetchHoyoGacha(context.Background(), "hoyoverse/starrail", q)
	if !errors.Is(err, core.ErrGachaURLUnavailable) {
		t.Fatalf("err=%v want ErrGachaURLUnavailable on retcode -101", err)
	}
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — append to `gacha.go` (add imports `context`, `encoding/json`, `fmt`, `io`, `net/http`, `time`):
```go
var _ core.GachaProvider = (*Provider)(nil)

// gachaEndpoint/gachaPageDelay are test seams (nil/0 → real values).
// Add these fields to the Provider struct in hoyoverse.go:
//   gachaEndpoint  func(core.GameID) string
//   gachaPageDelay time.Duration
// and in New(): p.gachaPageDelay = 400 * time.Millisecond

func hoyoGetGachaLogEndpoint(gid core.GameID) string {
	switch gid {
	case "hoyoverse/genshin":
		return "https://public-operation-hk4e-sg.hoyoverse.com/gacha_info/api/getGachaLog"
	case "hoyoverse/starrail":
		return "https://public-operation-hkrpg-sg.hoyoverse.com/common/hkrpg_gacha_record/api/getGachaLog"
	case "hoyoverse/zzz":
		return "https://public-operation-common-sg.hoyoverse.com/common/gacha_record/api/getGachaLog"
	}
	return ""
}

type hoyoGachaLogResp struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    *struct {
		List []struct {
			ID        string `json:"id"`
			GachaType string `json:"gacha_type"`
			RankType  string `json:"rank_type"`
			ItemType  string `json:"item_type"`
			Name      string `json:"name"`
			Time      string `json:"time"`
			UID       string `json:"uid"`
		} `json:"list"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider. installDir is the resolved game dir.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, installDir, cachedURL string) (core.GachaFetchResult, error) {
	m := findByID(gid)
	if m == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	q, err := extractHoyoAuthQuery(installDir, dataDirFor(m))
	if err != nil {
		// fall back to a cached auth-query string if present
		if cachedURL == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cu, perr := url.Parse(cachedURL)
		if perr != nil || cu.Query().Get("authkey") == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		q = cu.Query()
	}
	return p.fetchHoyoGacha(ctx, gid, q)
}

func (p *Provider) endpointFor(gid core.GameID) string {
	if p.gachaEndpoint != nil {
		return p.gachaEndpoint(gid)
	}
	return hoyoGetGachaLogEndpoint(gid)
}

// fetchHoyoGacha replays the auth query against getGachaLog for every banner
// gacha_type, paginating by end_id. Caches the auth query (as URL) in the result.
func (p *Provider) fetchHoyoGacha(ctx context.Context, gid core.GameID, auth url.Values) (core.GachaFetchResult, error) {
	endpoint := p.endpointFor(gid)
	if endpoint == "" {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	out := core.GachaFetchResult{Pulls: []core.GachaPull{}}
	// cache the auth query as a URL string for the store (token-bearing; never logged)
	out.URL = endpoint + "?" + auth.Encode()

	for _, gt := range gachaTypesToQuery[gid] {
		endID := "0"
		for page := 1; page <= 100; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			q := url.Values{}
			for _, k := range []string{"authkey", "authkey_ver", "sign_type", "game_biz", "lang", "region"} {
				if v := auth.Get(k); v != "" {
					q.Set(k, v)
				}
			}
			q.Set("gacha_type", gt)
			q.Set("size", "20")
			q.Set("page", strconv.Itoa(page))
			q.Set("end_id", endID)
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
			if err != nil {
				return out, err
			}
			req.Header.Set("User-Agent", UserAgent)
			resp, err := hc.Do(req)
			if err != nil {
				return out, err
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				return out, fmt.Errorf("getGachaLog status %d", resp.StatusCode)
			}
			var r hoyoGachaLogResp
			if err := json.Unmarshal(body, &r); err != nil {
				return out, err
			}
			if r.Retcode != 0 || r.Data == nil {
				// -100/-101/-111 etc → expired/invalid authkey → re-open guidance
				return out, core.ErrGachaURLUnavailable
			}
			for _, e := range r.Data.List {
				rank, _ := strconv.Atoi(e.RankType)
				if out.UID == "" {
					out.UID = e.UID
				}
				out.Pulls = append(out.Pulls, core.GachaPull{
					ID:        e.ID,
					BannerKey: bannerForGachaType(gid, e.GachaType),
					ItemType:  e.ItemType,
					Rank:      rank,
					Name:      e.Name,
					Time:      e.Time,
				})
			}
			if len(r.Data.List) < 20 {
				break
			}
			endID = r.Data.List[len(r.Data.List)-1].ID
			if p.gachaPageDelay > 0 {
				time.Sleep(p.gachaPageDelay)
			}
		}
	}
	return out, nil
}
```

- [ ] **Step 4: add struct fields + New default** in `internal/providers/hoyoverse/hoyoverse.go`:
  - Add to `Provider` struct: `gachaEndpoint func(core.GameID) string` and `gachaPageDelay time.Duration`.
  - In `New`, before `return p`: `p.gachaPageDelay = 400 * time.Millisecond` (leave gachaEndpoint nil → real endpoints).

- [ ] **Step 5: run** `go test ./internal/providers/hoyoverse/...` → PASS (incl. all pre-existing). `go vet`, `gofmt -l`, `go build ./...`.
- [ ] **Step 6: COMMIT DEFERRED.**

---

## Task 4: Full verification

- [ ] `go test ./...` (no `-race`) — all green.
- [ ] `cd frontend && npx vitest run` — still 69 green (no frontend change, sanity).
- [ ] `wails build` — binary builds; bindings unchanged (no new App methods).
- [ ] Confirm `git status` clean except the two new hoyoverse files.

---

## Real-machine smoke (USER)
1. Open 躍遷/祈願/調頻 history page in-game (Genshin/HSR/ZZZ) — mints a fresh ~24h authkey.
2. omnigate → select that game → 抽卡分析 → 重新整理紀錄 → records pull in; stats populate.
3. Verify the **response shape** matches (this is the one unverified piece): if any field is empty/wrong (name/rank/banner), report — likely a json-tag tweak.
4. Re-refresh within 24h → no re-open needed (authkey cached). After expiry → re-open guidance.
5. Watch for rate-limit blocks; if hit, raise `gachaPageDelay`.

## Notes for executor
- Reviewers on `opus`; implement to green UNCOMMITTED; commit each task only after spec + quality review APPROVE.
- Token/authkey URL must NEVER be logged (it's in `out.URL`; the App's RefreshGacha already avoids logging it).
- The success **response shape** is documented-but-not-live-verified — keep the json tags faithful to the reference and flag for smoke. Everything else (endpoints, params, error handling, auth-query lifting) IS live-verified.
- WuWa is a separate later plan (URL extraction confirmed: `aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record`; record API `POST gmserver-api.aki-game2.net/gacha/record/query` — body schema TBD).
