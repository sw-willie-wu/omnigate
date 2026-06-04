package hoyoverse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// ── Task 1: webCache auth-query extraction ────────────────────────────────────

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
