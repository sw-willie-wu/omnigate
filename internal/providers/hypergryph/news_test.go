package hypergryph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestNews_Hypergryph_ParsesAndMaps(t *testing.T) {
	body := `{"code":0,"msg":"","data":{"list":[
	  {"cid":"9577","tab":"notices","title":"Notice A","displayTime":1779508800,"cover":"https://c/a.jpg"},
	  {"cid":"9578","tab":"events","title":"Event B","displayTime":1779508800,"cover":""},
	  {"cid":"9579","tab":"news","title":"News C","displayTime":1779508800,"cover":""}
	],"total":3}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "arknights_endfield_official" {
			t.Errorf("code param = %q", r.URL.Query().Get("code"))
		}
		if r.URL.Query().Get("lang") != "zh-tw" {
			t.Errorf("lang = %q want zh-tw", r.URL.Query().Get("lang"))
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &Provider{}
	endfieldNewsBase = srv.URL
	defer func() { endfieldNewsBase = endfieldNewsBaseDefault }()

	items, err := p.GetNews(context.Background(), "hypergryph/endfield", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d", len(items))
	}
	byTitle := map[string]core.NewsItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if byTitle["Notice A"].Category != core.NewsAnnounce {
		t.Errorf("notices→announce")
	}
	if byTitle["Event B"].Category != core.NewsActivity {
		t.Errorf("events→activity")
	}
	if byTitle["News C"].Category != core.NewsInfo {
		t.Errorf("news→info")
	}
	if byTitle["Notice A"].Date != "2026-05-23" {
		t.Errorf("date = %q want 2026-05-23", byTitle["Notice A"].Date)
	}
	if !strings.HasSuffix(byTitle["Notice A"].URL, "/zh-tw/news/9577") {
		t.Errorf("URL = %q", byTitle["Notice A"].URL)
	}
}

func TestNews_Hypergryph_DegradesOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	p := &Provider{}
	endfieldNewsBase = srv.URL
	defer func() { endfieldNewsBase = endfieldNewsBaseDefault }()
	got, err := p.GetNews(context.Background(), "hypergryph/endfield", "en")
	if err != nil {
		t.Fatalf("should degrade, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestNews_Hypergryph_UnknownGID(t *testing.T) {
	p := &Provider{}
	got, err := p.GetNews(context.Background(), "hypergryph/unknown", "en")
	if err != nil || len(got) != 0 {
		t.Errorf("want ([],nil), got %v err=%v", got, err)
	}
}
