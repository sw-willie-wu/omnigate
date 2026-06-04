# Gacha Analysis P3 (Wuthering Waves) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** Implement `core.GachaProvider` on the kurogames provider so 抽卡分析 works for Wuthering Waves (`kurogames/wutheringwaves`), reusing the Plan-1 foundation. Completes the 5-game set.

**Architecture:** WuWa's convene-history URL is written to the game's logs (`Client/Saved/Logs/Client.log` and `Client/Binaries/Win64/ThirdParty/KrPcSdk_Global/KRSDKRes/KRSDKWebView/debug.log`). It's a webview URL `https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?<params-in-fragment>`. We extract the freshest one, parse the **fragment** params (`svr_id/player_id/lang/record_id/resources_id/...`), then `POST` per `cardPoolType` (1–7) to `https://gmserver-api.aki-game2.net/gacha/record/query`. The response is the **full list per pool, no pagination**. Records have **no unique id**, so we synthesize a stable one (`<cardPoolType>-<zero-padded index from oldest>`) for store dedup. Per-pool pity (cap 80, no 50/50 — WuWa limited 5★ is always the featured item).

**Tech stack:** Go (CGO_ENABLED=0), stdlib net/http (POST + JSON), net/url, regexp, os. No new deps.

**Protocol source:** convene URL extraction from `wuwatracker/wuwatracker` (the user's cited tool) + `Ikram001/wuwa-pull-tracker-local`; record API body + response fields cross-confirmed from `Ikram001/wuwa-pull-tracker-local` (`server/src/routes/sync.ts`) and `Qawerz/WutheringWaves-Convence-Tracker` (`getHistory.py`) — both agree. **Live success response NOT yet verified** (convene `record_id` is short-lived; cached one from last night likely expired) → flag for fresh-token smoke. URL extraction + body field names ARE confirmed against two working tools.

---

## Verified protocol reference

**Convene URL** (in logs): host `aki-gm-resources-oversea.aki-game.net` (global; CN = `…aki-game.com`, also a `-back` variant). Path `/aki/gacha/index.html#/record?...`. Params live in the **fragment** (after `#`): `svr_id, player_id, lang (e.g. zh-Hant), gacha_id, gacha_type, svr_area=global, record_id, resources_id, platform`.
- Client.log regex: `https://aki-gm-resources(-oversea|-back)?\.aki-game\.(net|com)/aki/gacha/index\.html#/record[^\s"]*`
- debug.log form: `"#url": "<same url>"` — extract the quoted URL.
- Take the LAST (most recent) match across both files.

**Record API:** `POST https://gmserver-api.aki-game2.net/gacha/record/query`, `Content-Type: application/json`, body:
```json
{"cardPoolId":"<resources_id>","cardPoolType":<1-7 int>,"languageCode":"<lang>","playerId":"<player_id>","recordId":"<record_id>","serverId":"<svr_id>"}
```
Call once per `cardPoolType` 1–7. **No pagination** — each call returns the pool's full list.

**cardPoolType → bannerKey** (each pool has independent pity):
`1`→`character` (限定共鳴者), `2`→`weapon` (限定武器), `3`→`standard_char` (常駐共鳴者), `4`→`standard_weapon` (常駐武器), `5`→`beginner` (新手), `6`→`beginner_choice` (新手自選), `7`→`other` (感恩定向).

**Response:** `{"code":0,"message":"success","data":[{"cardPoolType":..,"resourceId":..,"qualityLevel":5,"resourceType":"角色|武器","name":"..","count":1,"time":"2026-06-01 12:00:00"}]}`. `code != 0` → ErrGachaURLUnavailable (expired/invalid). `qualityLevel` 3/4/5; headline rank = **5**.

**Synthetic ID:** API returns newest-first with no id. Reverse to oldest-first, enumerate `i=0..n-1` per pool; `ID = fmt.Sprintf("%d-%08d", cardPoolType, i)`. Stable across refreshes (old records keep their oldest-index as new ones append at higher i) → store dedup works; sorts correctly (`numLess` orders equal-length ids lexically; the engine groups by bannerKey first).

**UID:** use `player_id` (from the URL) as the store uid (the API has no per-record uid).

**Pity:** all pools cap 80, no 50/50 (`hoyoPity`-style every-pull-counts). ExpectedPity 62.5.

---

## File structure
- Create `internal/providers/kurogames/gacha.go` — extraction + config + pity + FetchGacha.
- Create `internal/providers/kurogames/gacha_test.go`.
- Modify `internal/providers/kurogames/kurogames.go` — add `recordAPIBase string` + `convLogPathsFn func(installDir string) []string` test seams to `Provider`; set defaults in `New`.
- No foundation/app/frontend change (generic routing by gid).

---

## Task 1: Convene URL extraction (logs → fragment params)

**Files:** Create `internal/providers/kurogames/gacha.go` (extraction portion) + `gacha_test.go`.

- [ ] **Step 1: failing test** — `gacha_test.go`:
```go
package kurogames

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractConveneParams_LatestWins(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "Client", "Saved", "Logs")
	os.MkdirAll(logs, 0o755)
	base := "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?svr_id=1&player_id=OLD&lang=zh-Hant&gacha_id=1&gacha_type=1&svr_area=global&record_id=R1&resources_id=RS1&platform=PC"
	newer := "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?svr_id=9&player_id=NEW&lang=zh-Hant&gacha_id=1&gacha_type=1&svr_area=global&record_id=R2&resources_id=RS2&platform=PC"
	os.WriteFile(filepath.Join(logs, "Client.log"), []byte("x "+base+"\ny "+newer+"\n"), 0o644)

	p := New(Settings{}, nil)
	f, err := p.extractConveneParams(dir)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if f.Get("player_id") != "NEW" || f.Get("record_id") != "R2" || f.Get("resources_id") != "RS2" || f.Get("svr_id") != "9" {
		t.Fatalf("did not pick newest: %v", f)
	}
}

func TestExtractConveneParams_DebugLogUrlForm(t *testing.T) {
	dir := t.TempDir()
	dbg := filepath.Join(dir, "Client", "Binaries", "Win64", "ThirdParty", "KrPcSdk_Global", "KRSDKRes", "KRSDKWebView")
	os.MkdirAll(dbg, 0o755)
	u := "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?svr_id=1&player_id=P&lang=zh-Hant&record_id=R&resources_id=RS&gacha_type=1&svr_area=global&platform=PC"
	os.WriteFile(filepath.Join(dbg, "debug.log"), []byte(`{"#url": "`+u+`"}`), 0o644)

	p := New(Settings{}, nil)
	f, err := p.extractConveneParams(dir)
	if err != nil || f.Get("player_id") != "P" {
		t.Fatalf("debug.log extract failed: %v err=%v", f, err)
	}
}

func TestExtractConveneParams_None(t *testing.T) {
	p := New(Settings{}, nil)
	if _, err := p.extractConveneParams(t.TempDir()); err == nil {
		t.Fatalf("want error when no convene url present")
	}
	_ = url.Values{}
}
```

- [ ] **Step 2: run** `go test ./internal/providers/kurogames/ -run TestExtractConvene` → FAIL.

- [ ] **Step 3: implement** — create `internal/providers/kurogames/gacha.go`:
```go
package kurogames

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"omnigate/internal/core"
)

var conveneURLRe = regexp.MustCompile(`https://aki-gm-resources(?:-oversea|-back)?\.aki-game\.(?:net|com)/aki/gacha/index\.html#/record[^\s"]*`)

// convLogPaths returns the candidate log files (relative to the WuWa game dir)
// that may carry the convene-history URL.
func defaultConvLogPaths(installDir string) []string {
	return []string{
		filepath.Join(installDir, "Client", "Saved", "Logs", "Client.log"),
		filepath.Join(installDir, "Client", "Binaries", "Win64", "ThirdParty", "KrPcSdk_Global", "KRSDKRes", "KRSDKWebView", "debug.log"),
	}
}

// extractConveneParams reads the WuWa logs, finds the most recent convene URL,
// and returns its fragment params (svr_id/player_id/lang/record_id/resources_id…).
func (p *Provider) extractConveneParams(installDir string) (url.Values, error) {
	paths := p.convLogPathsFn(installDir)
	var last string
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if m := conveneURLRe.FindAll(b, -1); len(m) > 0 {
			last = string(m[len(m)-1]) // most recent in this file
		}
	}
	if last == "" {
		return nil, core.ErrGachaURLUnavailable
	}
	// params are in the fragment after "#/record?"
	frag := last
	if i := strings.Index(frag, "?"); i >= 0 {
		frag = frag[i+1:]
	}
	q, err := url.ParseQuery(frag)
	if err != nil || q.Get("record_id") == "" || q.Get("player_id") == "" {
		return nil, core.ErrGachaURLUnavailable
	}
	return q, nil
}
```

- [ ] **Step 4: add seam fields + New defaults** in `internal/providers/kurogames/kurogames.go`:
  - Add to `Provider` struct: `recordAPIBase string`, `convLogPathsFn func(installDir string) []string`, and `recordDelay time.Duration`.
  - In `New`, before returning: `p.recordAPIBase = "https://gmserver-api.aki-game2.net"`, `p.convLogPathsFn = defaultConvLogPaths`, `p.recordDelay = 400 * time.Millisecond`. (Read the existing `New` first; it returns `&Provider{...}` — switch to a `p := &Provider{...}; …; return p` form, keeping ALL existing fields incl. `httpClient`. `time` is already imported in kurogames.go.)
  - Tests set `p.recordDelay = 0` to avoid sleeps.

- [ ] **Step 5: run** → PASS. `go vet`, `gofmt -l`, `go build ./...`.
- [ ] **Step 6: COMMIT DEFERRED.**

---

## Task 2: GachaConfig + pity + banner mapping

**Files:** append to `gacha.go`, `gacha_test.go`.

- [ ] **Step 1: failing test** — append to `gacha_test.go` (add `"omnigate/internal/core"` to imports):
```go
func TestWuwaConfigAndBanners(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("kurogames/wutheringwaves")
	if cfg.HeadlineRank != 5 {
		t.Fatalf("headline=%d want 5", cfg.HeadlineRank)
	}
	for _, k := range []string{"character", "weapon", "standard_char", "beginner"} {
		if cfg.BannerOf(k) == nil {
			t.Fatalf("missing banner %q", k)
		}
		if cfg.BannerOf(k).Pity.Has5050() {
			t.Fatalf("%q must not be 50/50 (WuWa featured is guaranteed)", k)
		}
	}
}

func TestWuwaPoolBanner(t *testing.T) {
	if poolBanner(1) != "character" || poolBanner(2) != "weapon" || poolBanner(7) != "other" {
		t.Fatalf("pool→banner map wrong")
	}
}

func TestWuwaPityWalk(t *testing.T) {
	m := wuwaPity{}
	pulls := []core.GachaPull{{ID: "1-00000000", Rank: 4}, {ID: "1-00000001", Rank: 5, Name: "X"}, {ID: "1-00000002", Rank: 4}}
	hits, trailing := m.Walk(pulls, 5)
	if len(hits) != 1 || hits[0].Count != 2 || trailing != 1 {
		t.Fatalf("hits=%+v trailing=%d", hits, trailing)
	}
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — append to `gacha.go`:
```go
// wuwaPity: every pull counts, reset on a headline; WuWa has no 50/50.
type wuwaPity struct{}

func (wuwaPity) HardPity() int { return 80 }
func (wuwaPity) Has5050() bool { return false }
func (wuwaPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
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

var wuwaPoolBanner = map[int]string{
	1: "character", 2: "weapon", 3: "standard_char", 4: "standard_weapon",
	5: "beginner", 6: "beginner_choice", 7: "other",
}

func poolBanner(t int) string {
	if b, ok := wuwaPoolBanner[t]; ok {
		return b
	}
	return "other"
}

func wuwaLoc(zhTW, zhCN, en string) core.LocalizedString {
	return core.LocalizedString{"zh-TW": zhTW, "zh-CN": zhCN, "en": en}
}

func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	return core.GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]core.LocalizedString{5: wuwaLoc("五星", "五星", "5★"), 4: wuwaLoc("四星", "四星", "4★")},
		Banners: []core.BannerConfig{
			{Key: "character", Label: wuwaLoc("限定共鳴者", "限定共鸣者", "Featured Resonator"), Pity: wuwaPity{}},
			{Key: "weapon", Label: wuwaLoc("限定武器", "限定武器", "Featured Weapon"), Pity: wuwaPity{}},
			{Key: "standard_char", Label: wuwaLoc("常駐共鳴者", "常驻共鸣者", "Standard Resonator"), Pity: wuwaPity{}},
			{Key: "standard_weapon", Label: wuwaLoc("常駐武器", "常驻武器", "Standard Weapon"), Pity: wuwaPity{}},
			{Key: "beginner", Label: wuwaLoc("新手", "新手", "Beginner"), Pity: wuwaPity{}},
			{Key: "beginner_choice", Label: wuwaLoc("新手自選", "新手自选", "Beginner Choice"), Pity: wuwaPity{}},
			{Key: "other", Label: wuwaLoc("感恩定向", "感恩定向", "Other"), Pity: wuwaPity{}},
		},
		PullPrice: 160, Currency: "NT$", ExpectedPity: 62.5,
	}
}
```
(WuWa single pull ≈ 160 元 placeholder estimate.)

- [ ] **Step 4: run** → PASS. `go vet`, `gofmt -l`.
- [ ] **Step 5: COMMIT DEFERRED.**

---

## Task 3: FetchGacha (POST per pool + synth id + normalize)

**Files:** append to `gacha.go`, `gacha_test.go`.

- [ ] **Step 1: failing test** — append to `gacha_test.go`. Consolidate ALL test imports into one block; the full set the tests use is exactly: `context`, `encoding/json`, `errors`, `net/http`, `net/http/httptest`, `net/url`, `os`, `path/filepath`, `testing`, `omnigate/internal/core`. **Do NOT import `bytes` in the test** (it's only used in production `gacha.go`) — unused import = compile error.
```go
func TestWuwaFetch_NormalizesAndSynthIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["cardPoolType"].(float64) == 1 {
			// newest-first: two records (one 5★)
			w.Write([]byte(`{"code":0,"message":"success","data":[
				{"qualityLevel":5,"resourceType":"角色","name":"Alpha","count":1,"time":"2026-06-01 10:00:00"},
				{"qualityLevel":4,"resourceType":"武器","name":"Beta","count":1,"time":"2026-06-01 09:00:00"}]}`))
			return
		}
		w.Write([]byte(`{"code":0,"message":"success","data":[]}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.recordDelay = 0
	f := url.Values{"svr_id": {"1"}, "player_id": {"800"}, "lang": {"zh-Hant"}, "record_id": {"R"}, "resources_id": {"RS"}}
	res, err := p.fetchWuwa(context.Background(), f)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.UID != "800" || len(res.Pulls) != 2 {
		t.Fatalf("uid=%q pulls=%d", res.UID, len(res.Pulls))
	}
	// oldest gets index 0; Alpha (newest) gets the higher index.
	var alpha *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].Name == "Alpha" {
			alpha = &res.Pulls[i]
		}
	}
	if alpha == nil || alpha.Rank != 5 || alpha.BannerKey != "character" || alpha.ID != "1-00000001" {
		t.Fatalf("alpha wrong: %+v", alpha)
	}
}

func TestWuwaFetch_ErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-1,"message":"record id invalid","data":null}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.recordDelay = 0
	f := url.Values{"player_id": {"800"}, "record_id": {"R"}}
	_, err := p.fetchWuwa(context.Background(), f)
	if !errors.Is(err, core.ErrGachaURLUnavailable) {
		t.Fatalf("err=%v want ErrGachaURLUnavailable", err)
	}
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — append to `gacha.go` (add imports `bytes`, `context`, `encoding/json`, `fmt`, `io`, `net/http`, `time`):
```go
var _ core.GachaProvider = (*Provider)(nil)

type wuwaRecordResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    []struct {
		QualityLevel int    `json:"qualityLevel"`
		ResourceType string `json:"resourceType"`
		Name         string `json:"name"`
		Time         string `json:"time"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider for WuWa.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, installDir, cachedURL string) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	f, err := p.extractConveneParams(installDir)
	if err != nil {
		if cachedURL == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cu, perr := url.Parse(cachedURL)
		if perr != nil {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		frag := cu.Fragment
		if i := strings.Index(frag, "?"); i >= 0 {
			frag = frag[i+1:]
		}
		if f, err = url.ParseQuery(frag); err != nil || f.Get("record_id") == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
	}
	return p.fetchWuwa(ctx, f)
}

func (p *Provider) recordAPI() string {
	if p.recordAPIBase != "" {
		return p.recordAPIBase
	}
	return "https://gmserver-api.aki-game2.net"
}

// fetchWuwa POSTs the record query for each pool type and normalizes results.
func (p *Provider) fetchWuwa(ctx context.Context, f url.Values) (core.GachaFetchResult, error) {
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	lang := f.Get("lang")
	if lang == "" {
		lang = "zh-Hant"
	}
	out := core.GachaFetchResult{UID: f.Get("player_id"), Pulls: []core.GachaPull{}}
	// cache a reconstructable URL (carries record_id; never logged)
	out.URL = "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?" + f.Encode()

	endpoint := p.recordAPI() + "/gacha/record/query"
	for pool := 1; pool <= 7; pool++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		reqBody, _ := json.Marshal(map[string]any{
			"cardPoolId":   f.Get("resources_id"),
			"cardPoolType": pool,
			"languageCode": lang,
			"playerId":     f.Get("player_id"),
			"recordId":     f.Get("record_id"),
			"serverId":     f.Get("svr_id"),
		})
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return out, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			return out, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("wuwa record api status %d", resp.StatusCode)
		}
		var r wuwaRecordResp
		if err := json.Unmarshal(body, &r); err != nil {
			return out, err
		}
		if r.Code != 0 {
			return out, core.ErrGachaURLUnavailable
		}
		// API returns newest-first; reverse to oldest-first for stable indices.
		n := len(r.Data)
		for i := n - 1; i >= 0; i-- {
			e := r.Data[i]
			idx := n - 1 - i // 0 = oldest
			out.Pulls = append(out.Pulls, core.GachaPull{
				ID:        fmt.Sprintf("%d-%08d", pool, idx),
				BannerKey: poolBanner(pool),
				ItemType:  e.ResourceType,
				Rank:      e.QualityLevel,
				Name:      e.Name,
				Time:      e.Time,
			})
		}
		if p.recordDelay > 0 {
			time.Sleep(p.recordDelay)
		}
	}
	return out, nil
}
```
(The `recordDelay` field paces per-pool POSTs against rate-limiting; set to 0 in tests, 400ms in `New` — see Task 1 Step 4. Mirrors hoyoverse `gachaPageDelay`.)

- [ ] **Step 4: run** `go test ./internal/providers/kurogames/...` → PASS (incl. pre-existing). `go vet`, `gofmt -l`, `go build ./...`.
- [ ] **Step 5: COMMIT DEFERRED.**

---

## Task 4: Full verification
- [ ] `go test ./...` (no `-race`) green.
- [ ] `cd frontend && npx vitest run` — 69 green (no FE change).
- [ ] `wails build` — binary builds.
- [ ] `git status` clean except the two new kurogames files + kurogames.go.

---

## Real-machine smoke (USER)
1. Open 喚取紀錄 (convene history) in WuWa → **refresh promptly** (record_id is short-lived).
2. omnigate → 鳴潮 → 抽卡分析 → 重新整理 → records populate; verify 5★/銀/名稱/池子/保底.
3. **Response-shape is the unverified piece** — if a field is empty/wrong report it (likely a json-tag/`resourceType` tweak). Watch rate-limit.

## Notes for executor
- Reviewers `opus`; implement UNCOMMITTED; commit each task only after spec+quality APPROVE.
- Never log the convene URL / record_id (it's in `out.URL`).
- `recordDelay` field paces per-pool POSTs (400ms in New, 0 in tests).
- Synthetic ID `<pool>-<8-digit oldest-index>` is the key design choice (WuWa records have no native id + full-list-no-pagination); it makes `UpsertPulls` dedup idempotent across refreshes. If WuWa ever caps history (old pulls age out), indices would shift — note as a smoke/edge follow-up (acceptable for v1).
- CN host (`aki-game.com` / different gmserver) not supported in v1 (global only); regex tolerates `.com` but the API base is the global `gmserver-api.aki-game2.net`.
