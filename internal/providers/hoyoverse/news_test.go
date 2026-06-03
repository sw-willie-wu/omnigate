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
		if it.Date != "2026-05-20" { // created_at 1779247719 → 2026-05-20 (UTC)
			t.Errorf("date = %q want 2026-05-20 for %q", it.Date, it.Title)
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
