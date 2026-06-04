package hypergryph

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestExtractEndfieldGachaURL(t *testing.T) {
	log := "noise\nopening https://ef-webview.gryphline.com/page/gacha_index?token=AAA&server_id=2&lang=zh-tw foo\nlater https://ef-webview.gryphline.com/page/gacha_index?token=BBB&server_id=2&lang=zh-tw bar\n"
	got := extractEndfieldGachaURL([]byte(log))
	if got == "" || !strings.Contains(got, "token=BBB") {
		t.Fatalf("url=%q want last (BBB)", got)
	}
}

func TestFetchEndfieldPaginatesAndNormalizes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/record/char" {
			w.WriteHeader(404)
			return
		}
		calls++
		if r.URL.Query().Get("pool_type") != "E_CharacterGachaPoolType_Special" {
			w.Write([]byte(`{"code":0,"msg":"","data":{"list":[],"hasMore":false}}`))
			return
		}
		if r.URL.Query().Get("seq_id") == "" {
			w.Write([]byte(`{"code":0,"msg":"","data":{"list":[
				{"poolId":"sp","poolName":"特許尋訪","charId":"c1","charName":"Alpha","rarity":6,"gachaTs":"2025-01-01 10:00:00","seqId":"200","isFree":false},
				{"poolId":"sp","poolName":"特許尋訪","charId":"c2","charName":"Beta","rarity":5,"gachaTs":"2025-01-01 09:00:00","seqId":"199","isFree":true}
			],"hasMore":true}}`))
			return
		}
		w.Write([]byte(`{"code":0,"msg":"","data":{"list":[
			{"poolId":"sp","poolName":"特許尋訪","charId":"c3","charName":"Gamma","rarity":5,"gachaTs":"2025-01-01 08:00:00","seqId":"198","isFree":false}
		],"hasMore":false}}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.pageDelay = 0

	// Live (2026) page URL carries the token as u8_token and the server as server.
	url := srv.URL + "/page/gacha_char?u8_token=T&server=2&lang=zh-tw"
	res, err := p.fetchEndfield(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchEndfield: %v", err)
	}
	if len(res.Pulls) != 3 {
		t.Fatalf("pulls=%d want 3", len(res.Pulls))
	}
	var top *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].ID == "200" {
			top = &res.Pulls[i]
		}
	}
	if top == nil || top.Rank != 6 || top.Name != "Alpha" || top.BannerKey != "special" || top.IsFree {
		t.Fatalf("normalize wrong: %+v", top)
	}
	// 5 record-API calls: special page1 + special page2, then standard + beginner
	// + joint (1 empty page each).
	if calls != 5 {
		t.Fatalf("calls=%d want 5 (pagination + 4 pools)", calls)
	}
}

func TestFetchGachaMissingLogReturnsSentinel(t *testing.T) {
	p := New(Settings{}, nil)
	p.logPathFn = func() string { return filepath.Join(t.TempDir(), "nope.log") }
	_, err := p.FetchGacha(context.Background(), "hypergryph/endfield", "", "")
	if err == nil || !errors.Is(err, core.ErrGachaURLUnavailable) {
		t.Fatalf("err=%v want ErrGachaURLUnavailable", err)
	}
}

// Ensure os is used (avoids import error if test file uses it indirectly via filepath).
var _ = os.DevNull
