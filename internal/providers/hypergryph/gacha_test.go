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
