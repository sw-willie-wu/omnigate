package hoyoverse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnigate/internal/core"
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

func TestFetchBranchInfo_ParsesMainAndPredl(t *testing.T) {
	body := `{"retcode":0,"message":"OK","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkgMain","branch":"main","password":"pw-main","tag":"6.6.0","diff_tags":["6.5.0","6.4.0"],"categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"},{"category_id":"10017","matching_field":"en-us","type":"CATEGORY_TYPE_AUDIO"}]},"pre_download":{"package_id":"pkgPredl","branch":"predownload","password":"pw-predl","tag":"6.7.0","diff_tags":["6.6.0"],"categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/getGameBranches" {
			t.Errorf("path = %s, want /getGameBranches", got)
		}
		if got := r.URL.Query().Get("launcher_id"); got != LauncherID {
			t.Errorf("launcher_id = %q, want %q", got, LauncherID)
		}
		if got := r.URL.Query().Get("game_ids[]"); got != "gopR6Cufr3" {
			t.Errorf("game_ids[] = %q, want gopR6Cufr3", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	bi, err := p.fetchBranchInfo(context.Background(), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("fetchBranchInfo: %v", err)
	}
	if bi.Main.Tag != "6.6.0" {
		t.Errorf("Main.Tag = %q, want 6.6.0", bi.Main.Tag)
	}
	if bi.Main.PackageID != "pkgMain" || bi.Main.Password != "pw-main" {
		t.Errorf("Main package/password = %q/%q", bi.Main.PackageID, bi.Main.Password)
	}
	if len(bi.Main.DiffTags) != 2 || bi.Main.DiffTags[0] != "6.5.0" {
		t.Errorf("Main.DiffTags = %v", bi.Main.DiffTags)
	}
	if len(bi.Main.Categories) != 2 {
		t.Fatalf("Main.Categories len = %d, want 2", len(bi.Main.Categories))
	}
	if bi.PreDownload.IsEmpty() {
		t.Error("PreDownload unexpectedly empty")
	}
	if bi.PreDownload.Tag != "6.7.0" {
		t.Errorf("PreDownload.Tag = %q, want 6.7.0", bi.PreDownload.Tag)
	}
}

func TestFetchBranchTag_DelegatesToFetchBranchInfo(t *testing.T) {
	body := `{"retcode":0,"data":{"game_branches":[{"game":{"id":"gopR6Cufr3"},"main":{"package_id":"pkg","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tag, err := p.fetchBranchTag(context.Background(), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("fetchBranchTag: %v", err)
	}
	if tag != "6.6.0" {
		t.Errorf("tag = %q, want 6.6.0", tag)
	}
}
