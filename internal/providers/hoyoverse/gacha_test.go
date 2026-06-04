package hoyoverse

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// ── Task 1: webCache auth-query extraction ────────────────────────────────────

// gachaLogURL builds a getGachaLog-endpoint URL fixture carrying the given
// authkey/timestamp plus the usual params (so it passes the getGachaLog anchor).
func gachaLogURL(authkey, ts string) string {
	return "https://public-operation-hk4e-sg.hoyoverse.com/gacha_info/api/getGachaLog" +
		"?authkey=" + authkey + "&authkey_ver=1&sign_type=2&game_biz=hk4e_global" +
		"&lang=zh-tw&region=os_asia&gacha_id=ABC&timestamp=" + ts
}

func TestExtractAuthQuery_PicksFreshestByTimestamp(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "GenshinImpact_Data", "webCaches", "2.51.0.0", "Cache", "Cache_Data")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// two getGachaLog URLs; the larger timestamp must sort first.
	old := gachaLogURL("OLD", "1000")
	newer := gachaLogURL("NEW", "2000")
	blob := "garbage\x00" + old + "\x00noise " + newer + "\x00tail"
	if err := os.WriteFile(filepath.Join(cache, "data_2"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	cands, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("len(cands)=%d want 2", len(cands))
	}
	if cands[0].Get("authkey") != "NEW" {
		t.Fatalf("cands[0].authkey=%q want NEW (freshest by timestamp first)", cands[0].Get("authkey"))
	}
	// full query preserved (extra param carried through).
	if cands[0].Get("game_biz") != "hk4e_global" || cands[0].Get("gacha_id") != "ABC" {
		t.Fatalf("full query not preserved: %v", cands[0])
	}
}

func TestExtractAuthQuery_NoneFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data"); err == nil {
		t.Fatalf("want error when no webCache/authkey present")
	}
}

func TestExtractAuthQuery_ExcludesNonGachaLog(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "GenshinImpact_Data", "webCaches", "2.51.0.0", "Cache", "Cache_Data")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// an authkey URL that is NOT a getGachaLog endpoint must be excluded.
	noise := `https://gs.hoyoverse.com/genshin/event/e/index.html?authkey=BADINIT&game_biz=hk4e_global&timestamp=9999`
	good := gachaLogURL("REAL", "1000")
	blob := noise + "\x00" + good + "\x00"
	if err := os.WriteFile(filepath.Join(cache, "data_1"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	cands, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(cands) != 1 || cands[0].Get("authkey") != "REAL" {
		t.Fatalf("want only the getGachaLog candidate REAL, got %v", cands)
	}
}

func TestExtractAuthQuery_DedupAndOrder(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "GenshinImpact_Data", "webCaches", "2.51.0.0", "Cache", "Cache_Data")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	dup := gachaLogURL("DUP", "5000")
	noTS := "https://public-operation-hk4e-sg.hoyoverse.com/gacha_info/api/getGachaLog" +
		"?authkey=NOTS&authkey_ver=1&sign_type=2&game_biz=hk4e_global&lang=zh-tw&region=os_asia"
	blob := dup + "\x00" + dup + "\x00" + noTS + "\x00" + dup + "\x00"
	if err := os.WriteFile(filepath.Join(cache, "data_3"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	cands, err := extractHoyoAuthQuery(dir, "GenshinImpact_Data")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("len(cands)=%d want 2 (deduped)", len(cands))
	}
	if cands[0].Get("authkey") != "DUP" || cands[1].Get("authkey") != "NOTS" {
		t.Fatalf("order wrong (timestamped first, missing-ts last): %v", cands)
	}
}

// ── Task 2: Per-game GachaConfig + pity models ───────────────────────────────

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
		gid    core.GameID
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

// ── Task 3: FetchGacha (getGachaLog pagination + normalize) ──────────────────

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

func TestFetchGachaReportsProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"retcode":0,"message":"OK","data":{"list":[]}}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	var got []core.GachaProgress
	ctx := core.WithGachaProgress(context.Background(), func(pr core.GachaProgress) { got = append(got, pr) })
	q := url.Values{"authkey": {"K"}, "game_biz": {"hkrpg_global"}}
	if _, err := p.fetchHoyoGacha(ctx, "hoyoverse/starrail", q); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("no progress reported")
	}
	if got[0].BannerKey == "" || got[0].Page < 1 || got[0].PoolTotal < 1 {
		t.Fatalf("bad progress %+v", got[0])
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

// ── Fix #1: multi-candidate probe/fallback selection ─────────────────────────

func cand(authkey, ts string) url.Values {
	return url.Values{
		"authkey": {authkey}, "authkey_ver": {"1"}, "sign_type": {"2"},
		"game_biz": {"hkrpg_global"}, "lang": {"zh-tw"}, "region": {"prod"},
		"timestamp": {ts},
	}
}

// list1 / empty / timeout response bodies for the starrail character banner (gt=11).
const respList1 = `{"retcode":0,"message":"OK","data":{"list":[{"id":"1","gacha_type":"11","rank_type":"5","item_type":"角色","name":"A","time":"2026-06-01 10:00:00","uid":"800"}]}}`
const respEmpty = `{"retcode":0,"message":"OK","data":{"list":[]}}`
const respTimeout = `{"retcode":-101,"message":"authkey timeout","data":null}`

func TestSelectAuthCandidate_FallbackOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("authkey") == "GOOD" {
			w.Write([]byte(respList1))
			return
		}
		w.Write([]byte(respTimeout))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	// EXPIRED ordered first (freshest ts) but times out → must fall back to GOOD.
	chosen, err := p.selectAuthCandidate(context.Background(), "hoyoverse/starrail",
		[]url.Values{cand("EXPIRED", "2000"), cand("GOOD", "1000")})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if chosen.Get("authkey") != "GOOD" {
		t.Fatalf("chosen authkey=%q want GOOD", chosen.Get("authkey"))
	}
}

func TestSelectAuthCandidate_PrefersNonEmptyOverEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("authkey") == "REAL" {
			w.Write([]byte(respList1))
			return
		}
		w.Write([]byte(respEmpty)) // WRONG → retcode 0 but empty
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	// WRONG (empty) ordered FIRST; two-tier must still pick REAL (non-empty).
	chosen, err := p.selectAuthCandidate(context.Background(), "hoyoverse/starrail",
		[]url.Values{cand("WRONG", "2000"), cand("REAL", "1000")})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if chosen.Get("authkey") != "REAL" {
		t.Fatalf("chosen authkey=%q want REAL (non-empty preferred over earlier empty)", chosen.Get("authkey"))
	}
}

func TestSelectAuthCandidate_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(respTimeout))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	_, err := p.selectAuthCandidate(context.Background(), "hoyoverse/starrail",
		[]url.Values{cand("A", "2000"), cand("B", "1000")})
	if !errors.Is(err, core.ErrGachaURLUnavailable) {
		t.Fatalf("err=%v want ErrGachaURLUnavailable when all candidates fail", err)
	}
}

func TestSelectAuthCandidate_Tier2WhenAllEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(respEmpty))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	chosen, err := p.selectAuthCandidate(context.Background(), "hoyoverse/starrail",
		[]url.Values{cand("FIRST", "2000"), cand("SECOND", "1000")})
	if err != nil {
		t.Fatalf("select (tier2): %v", err)
	}
	if chosen.Get("authkey") != "FIRST" {
		t.Fatalf("chosen authkey=%q want FIRST (first retcode0+nonnil data)", chosen.Get("authkey"))
	}
}

func TestFetchGachaForwardsFullParams(t *testing.T) {
	var page1Seen, page2Seen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("gacha_type") != "11" {
			w.Write([]byte(respEmpty))
			return
		}
		if q.Get("gacha_id") != "XYZ" {
			t.Errorf("missing forwarded gacha_id on request end_id=%q: %v", q.Get("end_id"), q.Encode())
		}
		if q.Get("end_id") == "0" {
			page1Seen = true
			// 20 items (distinct ids) → loop continues to page 2.
			var b strings.Builder
			b.WriteString(`{"retcode":0,"message":"OK","data":{"list":[`)
			for i := 0; i < 20; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				id := string(rune('A'+i)) // distinct-ish; id used only as cursor
				b.WriteString(`{"id":"` + id + `","gacha_type":"11","rank_type":"4","item_type":"x","name":"n","time":"t","uid":"800"}`)
			}
			b.WriteString(`]}}`)
			w.Write([]byte(b.String()))
			return
		}
		page2Seen = true
		w.Write([]byte(respEmpty))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	auth := url.Values{"authkey": {"K"}, "game_biz": {"hkrpg_global"}, "gacha_id": {"XYZ"}}
	if _, err := p.fetchHoyoGacha(context.Background(), "hoyoverse/starrail", auth); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !page1Seen || !page2Seen {
		t.Fatalf("page1Seen=%v page2Seen=%v (expected end_id pagination)", page1Seen, page2Seen)
	}
}

func TestFetchGachaNoAuthkeyInLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(respEmpty))
	}))
	defer srv.Close()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := New(Settings{}, logger)
	p.gachaEndpoint = func(core.GameID) string { return srv.URL }
	p.gachaPageDelay = 0
	auth := url.Values{"authkey": {"SECRET123"}, "game_biz": {"hkrpg_global"}}
	if _, err := p.fetchHoyoGacha(context.Background(), "hoyoverse/starrail", auth); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if s := buf.String(); strings.Contains(s, "authkey=") || strings.Contains(s, "SECRET123") {
		t.Fatalf("authkey leaked into logs: %q", s)
	}
}
