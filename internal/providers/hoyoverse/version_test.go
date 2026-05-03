package hoyoverse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckVersion_ParsesMainAndPredownload(t *testing.T) {
	body := `{"retcode":0,"data":{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"},"patches":[]},"pre_download":{"major":{"version":"5.6.0"},"patches":[]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchVersion(context.Background(), "gopR6Cufr3", "5.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != "5.5.0" {
		t.Errorf("Latest = %s, want 5.5.0", got.Latest)
	}
	if got.Current != "5.5.0" {
		t.Errorf("Current = %s, want 5.5.0", got.Current)
	}
	if got.Predownload == nil || got.Predownload.TargetVersion != "5.6.0" {
		t.Errorf("Predownload = %+v, want target 5.6.0", got.Predownload)
	}
}

func TestCheckVersion_NoPredownload(t *testing.T) {
	body := `{"retcode":0,"data":{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"}}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchVersion(context.Background(), "gopR6Cufr3", "5.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Predownload != nil {
		t.Errorf("Predownload = %+v, want nil", got.Predownload)
	}
}
