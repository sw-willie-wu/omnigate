package gachaicon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkportSign_Golden(t *testing.T) {
	got := skportSign("/web/v1/wiki/item/catalog", "typeMainId=1",
		"06c7c625a2a7129ce8bfc12412de3020", "1782297097")
	if got != "4276bdfb29ac9a4c2c9a6884226f1307" {
		t.Fatalf("sign=%s want 4276bdfb29ac9a4c2c9a6884226f1307", got)
	}
}

func TestSkportGuestToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/web/v1/auth/refresh" {
			w.Write([]byte(`{"code":0,"data":{"token":"TKN123"}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	SetSkportBaseForTest(srv.URL)
	defer SetSkportBaseForTest("https://zonai.skport.com")
	m := NewManager(t.TempDir(), nil)
	tok, err := m.skportGuestToken()
	if err != nil || tok != "TKN123" {
		t.Fatalf("token=%q err=%v want TKN123", tok, err)
	}
}

func TestSkportGet_SignsRequest(t *testing.T) {
	var gotSign, gotTs, gotLang, gotPlatform string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSign = r.Header.Get("sign")
		gotTs = r.Header.Get("timestamp")
		gotLang = r.Header.Get("sk-language")
		gotPlatform = r.Header.Get("platform")
		w.Write([]byte(`{"code":0,"data":{"catalog":[]}}`))
	}))
	defer srv.Close()
	SetSkportBaseForTest(srv.URL)
	defer SetSkportBaseForTest("https://zonai.skport.com")
	m := NewManager(t.TempDir(), nil)
	body, err := m.skportGet("/web/v1/wiki/item/catalog", "typeMainId=1&typeSubId=1", "TKN", "zh_Hant")
	if err != nil {
		t.Fatalf("skportGet err: %v", err)
	}
	if len(gotSign) != 32 || gotTs == "" || gotLang != "zh_Hant" || gotPlatform != "3" {
		t.Fatalf("headers: sign=%q ts=%q lang=%q platform=%q", gotSign, gotTs, gotLang, gotPlatform)
	}
	if want := skportSign("/web/v1/wiki/item/catalog", "typeMainId=1&typeSubId=1", "TKN", gotTs); want != gotSign {
		t.Fatalf("sign mismatch: got %s want %s", gotSign, want)
	}
	if len(body) == 0 {
		t.Fatal("empty body")
	}
}

func TestSkportGet_NoTokenLeakInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	SetSkportBaseForTest(srv.URL)
	defer SetSkportBaseForTest("https://zonai.skport.com")
	m := NewManager(t.TempDir(), nil)
	_, err := m.skportGet("/web/v1/wiki/item/catalog", "typeMainId=1", "SECRET-TOKEN", "zh_Hant")
	if err == nil {
		t.Fatal("want error on 401")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks token: %v", err)
	}
}
