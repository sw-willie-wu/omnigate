package kurogames

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestNews_Kurogames_ParsesAndMaps(t *testing.T) {
	body := `[
	  {"articleId":758,"articleTitle":"Convene Details","articleType":58,"startTime":"2024-05-23 10:00:00","suggestCover":"https://c/cover.jpg","top":1,"sortingMark":1},
	  {"articleId":900,"articleTitle":"Spring Event","articleType":59,"startTime":"2024-06-01 09:00:00","suggestCover":"","top":0,"sortingMark":2},
	  {"articleId":901,"articleTitle":"Dev Note","articleType":57,"startTime":"2024-06-02 09:00:00","suggestCover":"","top":0,"sortingMark":3}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/en/ArticleMenu.json") {
			t.Errorf("path = %q, want .../en/ArticleMenu.json", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &Provider{}
	wuwaNewsBase = srv.URL // test seam (full base incl. /akiwebsite/...)
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()

	items, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d: %v", len(items), items)
	}
	byTitle := map[string]core.NewsItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if byTitle["Convene Details"].Category != core.NewsAnnounce {
		t.Errorf("58 should map to announce, got %q", byTitle["Convene Details"].Category)
	}
	if byTitle["Spring Event"].Category != core.NewsActivity {
		t.Errorf("59 should map to activity")
	}
	if byTitle["Dev Note"].Category != core.NewsInfo {
		t.Errorf("57 should map to info")
	}
	if byTitle["Convene Details"].Date != "2024-05-23" {
		t.Errorf("date = %q want 2024-05-23", byTitle["Convene Details"].Date)
	}
	if !strings.HasSuffix(byTitle["Convene Details"].URL, "/en/main/news/detail/758") {
		t.Errorf("URL = %q", byTitle["Convene Details"].URL)
	}
	if byTitle["Convene Details"].Thumbnail != "https://c/cover.jpg" {
		t.Errorf("thumb = %q", byTitle["Convene Details"].Thumbnail)
	}
}

func TestNews_Kurogames_BadJSONDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("nope"))
	}))
	defer srv.Close()
	p := &Provider{}
	wuwaNewsBase = srv.URL
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()
	got, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatalf("should degrade, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestNews_Kurogames_UnknownGID(t *testing.T) {
	p := &Provider{}
	got, err := p.GetNews(context.Background(), "kurogames/unknown", "en")
	if err != nil || len(got) != 0 {
		t.Errorf("want ([],nil), got %v err=%v", got, err)
	}
}
