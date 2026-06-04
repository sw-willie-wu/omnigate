package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
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
