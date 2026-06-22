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

// Recent WuWa builds XOR-obfuscate Client.log (low-nibble-odd byte ^ 0xA5, else
// ^ 0xEF). The plaintext regex finds nothing; extract must decrypt and retry.
func TestExtractConveneParams_EncryptedClientLog(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "Client", "Saved", "Logs")
	os.MkdirAll(logs, 0o755)
	u := "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?svr_id=9&player_id=ENC&lang=zh-Hant&gacha_id=1&gacha_type=1&svr_area=global&record_id=RX&resources_id=RSX&platform=PC"
	plain := []byte("LogTemp: opening convene record\n" + u + "\ntrailing line\n")
	enc := make([]byte, len(plain))
	for i, c := range plain {
		// inverse of the decrypt rule: even plaintext byte -> ^0xA5 (cipher
		// becomes low-nibble-odd), odd plaintext byte -> ^0xEF.
		if c%2 == 0 {
			enc[i] = c ^ 0xA5
		} else {
			enc[i] = c ^ 0xEF
		}
	}
	os.WriteFile(filepath.Join(logs, "Client.log"), enc, 0o644)

	p := New(Settings{}, nil)
	f, err := p.extractConveneParams(dir)
	if err != nil {
		t.Fatalf("extract from encrypted log: %v", err)
	}
	if f.Get("player_id") != "ENC" || f.Get("record_id") != "RX" {
		t.Fatalf("did not decode encrypted convene url: %v", f)
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
	for _, k := range []string{"character", "weapon", "standard_char", "beginner", "char_exchange", "weapon_exchange", "collab", "collab_weapon"} {
		if cfg.BannerOf(k) == nil {
			t.Fatalf("missing banner %q", k)
		}
		if cfg.BannerOf(k).Pity.Has5050() {
			t.Fatalf("%q must not be 50/50 (WuWa featured is guaranteed)", k)
		}
	}
}

func TestWuwaPoolBanner(t *testing.T) {
	want := map[int]string{
		1: "character", 2: "weapon", 3: "standard_char", 4: "standard_weapon",
		5: "beginner", 6: "beginner_choice", 7: "other",
		8: "char_exchange", 9: "weapon_exchange", 10: "collab", 11: "collab_weapon",
	}
	for pt, w := range want {
		if poolBanner(pt) != w {
			t.Errorf("poolBanner(%d)=%q want %q", pt, poolBanner(pt), w)
		}
	}
	if poolBanner(99) != "other" {
		t.Errorf("unknown pool must fall back to other")
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
	// each distinct-time record is ordinal 0 within its own (pool,time).
	var alpha *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].Name == "Alpha" {
			alpha = &res.Pulls[i]
		}
	}
	if alpha == nil || alpha.Rank != 5 || alpha.BannerKey != "character" || alpha.ID != "w|1|2026-06-01 10:00:00|0" {
		t.Fatalf("alpha wrong: %+v", alpha)
	}
}

func TestWuwaFetch_SameSecondOrdinals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["cardPoolType"].(float64) == 1 {
			// three records, SAME second, newest-first
			w.Write([]byte(`{"code":0,"message":"success","data":[
				{"qualityLevel":3,"resourceType":"武器","name":"C","count":1,"time":"2026-06-01 10:00:00"},
				{"qualityLevel":3,"resourceType":"武器","name":"B","count":1,"time":"2026-06-01 10:00:00"},
				{"qualityLevel":3,"resourceType":"武器","name":"A","count":1,"time":"2026-06-01 10:00:00"}]}`))
			return
		}
		w.Write([]byte(`{"code":0,"message":"success","data":[]}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.recordDelay = 0
	f := url.Values{"player_id": {"800"}, "record_id": {"R"}}
	res, _ := p.fetchWuwa(context.Background(), f)
	// oldest-first ordinals: A(oldest)=0, B=1, C(newest)=2 — all distinct, stable.
	ids := map[string]string{}
	for _, pl := range res.Pulls {
		ids[pl.Name] = pl.ID
	}
	if ids["A"] != "w|1|2026-06-01 10:00:00|0" || ids["B"] != "w|1|2026-06-01 10:00:00|1" || ids["C"] != "w|1|2026-06-01 10:00:00|2" {
		t.Fatalf("ordinals wrong: %v", ids)
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

func TestWuwaStandardPoolAndLimited(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("kurogames/wutheringwaves")
	for _, n := range []string{"Calcharo", "Encore", "Jianxin", "Lingyang", "Verina", "卡卡羅", "凌阳"} {
		if !cfg.StandardPool[n] {
			t.Errorf("StandardPool missing %q", n)
		}
	}
	lim := map[string]bool{}
	for _, b := range cfg.Banners {
		lim[b.Key] = b.Limited
	}
	for _, k := range []string{"character", "weapon", "char_exchange", "weapon_exchange", "collab", "collab_weapon"} {
		if !lim[k] {
			t.Errorf("%q must be Limited", k)
		}
	}
	for _, k := range []string{"standard_char", "standard_weapon", "beginner", "beginner_choice", "other"} {
		if lim[k] {
			t.Errorf("%q must NOT be Limited", k)
		}
	}
}

// All 11 pools are queried; absent pools (code 0 + empty) contribute nothing and
// do NOT abort the fetch. Pool 10 data is normalized as banner "collab".
func TestWuwaFetch_AllPoolsNoAbort(t *testing.T) {
	queried := map[int]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		pt := int(body["cardPoolType"].(float64))
		queried[pt] = true
		if pt == 1 {
			w.Write([]byte(`{"code":0,"message":"success","data":[
				{"qualityLevel":5,"resourceType":"角色","name":"Lingyang","count":1,"time":"2026-06-01 10:00:00"}]}`))
			return
		}
		if pt == 10 {
			w.Write([]byte(`{"code":0,"message":"success","data":[
				{"qualityLevel":5,"resourceType":"角色","name":"Lucy","count":1,"time":"2026-06-08 23:35:44"}]}`))
			return
		}
		w.Write([]byte(`{"code":0,"message":"success","data":[]}`)) // every other pool empty
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.recordDelay = 0
	f := url.Values{"player_id": {"800"}, "record_id": {"R"}}
	res, err := p.fetchWuwa(context.Background(), f)
	if err != nil {
		t.Fatalf("fetch aborted on empty pools: %v", err)
	}
	for pt := 1; pt <= 11; pt++ {
		if !queried[pt] {
			t.Errorf("pool %d not queried", pt)
		}
	}
	if len(res.Pulls) != 2 {
		t.Fatalf("pulls=%d want 2 (Lingyang + Lucy)", len(res.Pulls))
	}
	var lucy *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].Name == "Lucy" {
			lucy = &res.Pulls[i]
		}
	}
	if lucy == nil || lucy.BannerKey != "collab" || lucy.ID != "w|10|2026-06-08 23:35:44|0" {
		t.Fatalf("collab pull wrong: %+v", lucy)
	}
}

// A standard resonator on the collab/char_exchange (limited, 50/50) pool is 歪;
// the collab character (Lucy) is a win; a weapon on a weapon pool is never 歪.
func TestWuwaOffOnCollabAndExchange(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("kurogames/wutheringwaves")
	pulls := []core.GachaPull{
		{ID: "a", BannerKey: "collab", Rank: 5, Name: "Lingyang", Time: "2026-06-08 23:00:00"},                  // standard on collab → 歪
		{ID: "b", BannerKey: "collab", Rank: 5, Name: "Lucy", Time: "2026-06-08 23:10:00"},                      // collab char → win
		{ID: "c", BannerKey: "char_exchange", Rank: 5, Name: "Encore", Time: "2026-06-08 23:20:00"},             // standard on exchange → 歪
		{ID: "d", BannerKey: "collab_weapon", Rank: 5, Name: "Emerald of Genesis", Time: "2026-06-08 23:30:00"}, // weapon on collab-weapon → never 歪
		{ID: "e", BannerKey: "weapon_exchange", Rank: 5, Name: "Stringmaster", Time: "2026-06-08 23:40:00"},     // weapon on new-journey weapon → never 歪
	}
	s := core.ComputeSummary("u", pulls, cfg)
	off := map[string]bool{}
	for _, h := range s.Highlights {
		off[h.Name] = h.Off
	}
	if !off["Lingyang"] || !off["Encore"] {
		t.Errorf("standard resonator on collab/exchange must be 歪: %+v", off)
	}
	if off["Lucy"] {
		t.Errorf("Lucy (collab char) must NOT be 歪")
	}
	if off["Emerald of Genesis"] || off["Stringmaster"] {
		t.Errorf("weapons on weapon pools must NOT be 歪: %+v", off)
	}
}
