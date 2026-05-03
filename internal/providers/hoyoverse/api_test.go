package hoyoverse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestFetchBasicInfo_ParsesBackgrounds(t *testing.T) {
	body := `{"retcode":0,"message":"OK","data":{"game_info_list":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"backgrounds":[{"id":"a","background":{"url":"https://cdn/img1.webp"},"video":{"url":""},"type":"BACKGROUND_TYPE_UNSPECIFIED"},{"id":"b","background":{"url":"https://cdn/img2.webp"},"video":{"url":"https://cdn/v.webm"},"type":"BACKGROUND_TYPE_VIDEO"}]}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/getAllGameBasicInfo" {
			t.Errorf("path = %s, want /getAllGameBasicInfo", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchBasicInfo(context.Background(), "gopR6Cufr3", "zh-tw")
	if err != nil {
		t.Fatalf("fetchBasicInfo: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d backgrounds, want 2", len(got))
	}
	if got[0].ImageURL != "https://cdn/img1.webp" {
		t.Errorf("bg[0].ImageURL = %s", got[0].ImageURL)
	}
	if got[1].VideoURL != "https://cdn/v.webm" {
		t.Errorf("bg[1].VideoURL = %s", got[1].VideoURL)
	}
	if got[1].Type != core.BackgroundVideo {
		t.Errorf("bg[1].Type = %v, want video", got[1].Type)
	}
	_ = json.Marshal // silence unused import if test grows
}

func TestFetchGameIcon_FindsByBiz(t *testing.T) {
	body := `{"retcode":0,"message":"OK","data":{"games":[{"biz":"hk4e_global","display":{"name":"Genshin Impact","icon":{"url":"https://cdn/icon-genshin.png"}}},{"biz":"hkrpg_global","display":{"name":"Star Rail","icon":{"url":"https://cdn/icon-rail.png"}}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchGameIcon(context.Background(), "hk4e_global", "zh-tw")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn/icon-genshin.png" {
		t.Errorf("icon = %s", got)
	}
}

func TestFetchGameIcon_NotFound(t *testing.T) {
	body := `{"retcode":0,"data":{"games":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, http.DefaultClient)
	_, err := c.fetchGameIcon(context.Background(), "missing_biz", "en")
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}
