package kurogames

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// newsTestServer serves a 2-article ArticleMenu plus per-article detail JSON
// (one 公告 with an inline image, one 活動 without), mimicking the WuWa CMS.
func newsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	menu := `[
	  {"articleId":758,"articleTitle":"Convene Details","startTime":"2024-05-23 10:00:00","top":1,"sortingMark":1},
	  {"articleId":900,"articleTitle":"Spring Event","startTime":"2024-06-01 09:00:00","top":0,"sortingMark":2}
	]`
	detail := map[int]string{
		758: `{"articleTypeName":"公告","articleContent":"<div><img style=\"display:block\" src=\"https://c/cover758.jpg\"/><p>hi</p></div>"}`,
		900: `{"articleTypeName":"活動","articleContent":"<div><p>no image here</p></div>"}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ArticleMenu.json"):
			if !strings.Contains(r.URL.Path, "/en/") {
				t.Errorf("menu path = %q, want .../en/ArticleMenu.json", r.URL.Path)
			}
			w.Write([]byte(menu))
		case strings.Contains(r.URL.Path, "/article/758.json"):
			w.Write([]byte(detail[758]))
		case strings.Contains(r.URL.Path, "/article/900.json"):
			w.Write([]byte(detail[900]))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestNews_Kurogames_TwoStage(t *testing.T) {
	srv := newsTestServer(t)
	defer srv.Close()

	p := &Provider{}
	wuwaNewsBase = srv.URL
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()

	items, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2, got %d: %v", len(items), items)
	}
	byTitle := map[string]core.NewsItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	// 公告 → announce, with the inline cover image as thumbnail.
	c := byTitle["Convene Details"]
	if c.Category != core.NewsAnnounce {
		t.Errorf("公告 should map to announce, got %q", c.Category)
	}
	if c.Thumbnail != "https://c/cover758.jpg" {
		t.Errorf("thumbnail = %q want https://c/cover758.jpg", c.Thumbnail)
	}
	if c.Date != "2024-05-23" {
		t.Errorf("date = %q want 2024-05-23", c.Date)
	}
	if !strings.HasSuffix(c.URL, "/en/main/news/detail/758") {
		t.Errorf("URL = %q", c.URL)
	}
	// 活動 → activity, no inline image → empty thumbnail.
	e := byTitle["Spring Event"]
	if e.Category != core.NewsActivity {
		t.Errorf("活動 should map to activity, got %q", e.Category)
	}
	if e.Thumbnail != "" {
		t.Errorf("thumbnail = %q want empty (no inline img)", e.Thumbnail)
	}
}

func TestNews_Kurogames_DetailFailureKeepsItem(t *testing.T) {
	// Menu OK but all detail fetches 404 → items kept with default info/no-thumb.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ArticleMenu.json") {
			w.Write([]byte(`[{"articleId":1,"articleTitle":"X","startTime":"2024-01-01 00:00:00","top":0,"sortingMark":0}]`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	p := &Provider{}
	wuwaNewsBase = srv.URL
	defer func() { wuwaNewsBase = wuwaNewsBaseDefault }()
	items, err := p.GetNews(context.Background(), "kurogames/wutheringwaves", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item kept, got %d", len(items))
	}
	if items[0].Category != core.NewsInfo || items[0].Thumbnail != "" {
		t.Errorf("detail-failed item should default to info/no-thumb, got %+v", items[0])
	}
}

func TestNews_Kurogames_MenuBadJSONDegrades(t *testing.T) {
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
