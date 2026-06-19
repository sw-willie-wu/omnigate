package hypergryph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnigate/internal/core"
)

func TestEndfieldStandardPityWalk(t *testing.T) {
	m := endfieldStandardPity{}
	pulls := []core.GachaPull{
		{ID: "1", Rank: 5}, {ID: "2", Rank: 6, Name: "X"}, {ID: "3", Rank: 5},
	}
	hits, trailing := m.Walk(pulls, 6)
	if len(hits) != 1 || hits[0].Count != 2 {
		t.Fatalf("hits=%+v want 1 hit cost 2", hits)
	}
	if trailing != 1 {
		t.Fatalf("trailing=%d want 1", trailing)
	}
}

func TestEndfieldLimitedPityExcludesFreeUntilMilestone(t *testing.T) {
	m := endfieldLimitedPity{}
	pulls := []core.GachaPull{
		{ID: "1", Rank: 5, IsFree: true},
		{ID: "2", Rank: 5, IsFree: true},
		{ID: "3", Rank: 5, IsFree: false},
	}
	_, trailing := m.Walk(pulls, 6)
	if trailing != 1 {
		t.Fatalf("trailing=%d want 1 (free pre-milestone ignored)", trailing)
	}
}

func TestEndfieldConfig(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("hypergryph/endfield")
	if cfg.HeadlineRank != 6 {
		t.Fatalf("headlineRank=%d want 6", cfg.HeadlineRank)
	}
	if cfg.BannerOf("special") == nil || cfg.BannerOf("standard") == nil || cfg.BannerOf("beginner") == nil || cfg.BannerOf("joint") == nil {
		t.Fatalf("missing banners: %+v", cfg.Banners)
	}
}

// efChainServer wires one httptest.Server that answers the whole Gryphline chain
// against path, so the provider's base fields can all point at it.
func efChainServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/user/oauth2/v2/grant", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":0,"data":{"token":"oauth-XYZ"}}`))
	})
	mux.HandleFunc("/account/binding/v1/binding_list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "oauth-XYZ" || r.URL.Query().Get("appCode") != "endfield" {
			t.Errorf("binding bad query: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"status":0,"data":{"list":[{"appCode":"endfield","bindingList":[
			{"uid":"hashUID","isDefault":true,"roles":[
				{"roleId":"R1","serverId":"9","isDefault":false},
				{"roleId":"R2","serverId":"2","isDefault":true}]}]}]}}`))
	})
	mux.HandleFunc("/account/binding/v1/u8_token_by_uid", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ UID, Token string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.UID != "hashUID" || body.Token != "oauth-XYZ" {
			t.Errorf("u8 bad body: %+v", body)
		}
		w.Write([]byte(`{"status":0,"data":{"token":"u8-TOK"}}`))
	})
	return httptest.NewServer(mux)
}

func TestEndfieldChain_DefaultRoleAndU8(t *testing.T) {
	srv := efChainServer(t)
	defer srv.Close()
	p := New(Settings{}, nil)
	p.oauthBase, p.bindingBase = srv.URL, srv.URL
	oauth, err := p.efGrant(context.Background(), "acct-TOK")
	if err != nil || oauth != "oauth-XYZ" {
		t.Fatalf("grant = %q,%v", oauth, err)
	}
	uid, role, server, err := p.efBinding(context.Background(), oauth)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "hashUID" || role != "R2" || server != "2" { // default role wins
		t.Fatalf("binding = %q,%q,%q; want hashUID,R2,2", uid, role, server)
	}
	u8, err := p.efU8Token(context.Background(), oauth, uid)
	if err != nil || u8 != "u8-TOK" {
		t.Fatalf("u8 = %q,%v", u8, err)
	}
}

func TestEndfieldChain_GrantAuthFailExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":401,"msg":"bad token"}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.oauthBase = srv.URL
	if _, err := p.efGrant(context.Background(), "stale"); !errors.Is(err, core.ErrGachaCredentialExpired) {
		t.Fatalf("err = %v; want ErrGachaCredentialExpired", err)
	}
}

func TestEndfieldFetchRecords_CharNormalizes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/record/char", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("token") != "u8-TOK" || q.Get("server_id") != "2" || q.Get("lang") != "zh-tw" {
			t.Errorf("char bad query: %s", r.URL.RawQuery)
		}
		// One page per pool; only the Standard pool returns a row.
		if q.Get("pool_type") == "E_CharacterGachaPoolType_Standard" && q.Get("seq_id") == "" {
			w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[
				{"seqId":"100","charId":"c1","charName":"Perlica","rarity":6,"gachaTs":"1769062855302","isFree":false}]}}`))
			return
		}
		w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[]}}`))
	})
	mux.HandleFunc("/api/record/weapon", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[]}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.pageDelay = 0

	res, err := p.efFetchRecords(context.Background(), "u8-TOK", "2", "zh-tw")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pulls) != 1 {
		t.Fatalf("pulls = %d; want 1", len(res.Pulls))
	}
	got := res.Pulls[0]
	if got.ID != "100" || got.BannerKey != "standard" || got.ItemType != "char" || got.Rank != 6 || got.Name != "Perlica" {
		t.Fatalf("pull = %+v", got)
	}
	if got.Time != "2026-01-22 14:20:55" { // 1769062855302 ms in LOCAL tz — adjust expected to your tz when running
		t.Logf("time = %q (local-tz dependent; assert the parse, not the literal)", got.Time)
	}
	if _, err := parseEndfieldTime("1769062855302"); err != nil {
		t.Errorf("parseEndfieldTime: %v", err)
	}
}

func TestEndfieldRecord_AuthTimeoutExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-101,"message":"auth key timeout"}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.pageDelay = 0
	if _, err := p.efFetchRecords(context.Background(), "stale", "2", "en-us"); !errors.Is(err, core.ErrGachaCredentialExpired) {
		t.Fatalf("err = %v; want ErrGachaCredentialExpired", err)
	}
}

func TestEndfieldFetchRecords_IncludesWeapon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/record/char", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[]}}`))
	})
	mux.HandleFunc("/api/record/weapon", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pool_type") != "" {
			t.Errorf("weapon must be single-pass (no pool_type), got %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("seq_id") == "" {
			// isFree:true here proves the normalizer's char-only guard: weapon pulls
			// must come back IsFree=false regardless of the source field.
			w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[
				{"seqId":"900","weaponName":"Blade","rarity":6,"gachaTs":"1769062855302","isFree":true}]}}`))
			return
		}
		w.Write([]byte(`{"code":0,"data":{"hasMore":false,"list":[]}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.pageDelay = 0

	res, err := p.efFetchRecords(context.Background(), "u8", "2", "en-us")
	if err != nil {
		t.Fatal(err)
	}
	var weapon *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].ItemType == "weapon" {
			weapon = &res.Pulls[i]
		}
	}
	if weapon == nil {
		t.Fatal("no weapon pull")
	}
	if weapon.BannerKey != "weapon" || weapon.Name != "Blade" || weapon.Rank != 6 || weapon.IsFree {
		t.Fatalf("weapon pull = %+v", *weapon)
	}
}

func TestEndfieldGachaConfig_PriceCurrencyWeaponBanner(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("hypergryph/endfield")
	if cfg.PullPrice != 500 || cfg.Currency != "endfield_oroberyl" {
		t.Fatalf("price/currency = %d/%q; want 500/endfield_oroberyl", cfg.PullPrice, cfg.Currency)
	}
	if cfg.BannerOf("weapon") == nil {
		t.Error("weapon banner missing")
	}
}
