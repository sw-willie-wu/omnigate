package gachaicon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// A single language 404 (e.g. HSR's old "chs" code, which sr.yatta.moe rejects)
// must NOT abort the whole index build — the other languages still resolve names.
func TestFetchAYLangs_SkipsFailedLangKeepsRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject the simplified code, serve the rest — mirrors sr.yatta.moe.
		if strings.Contains(r.URL.Path, "/chs/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":{"items":{}}}`))
	}))
	defer srv.Close()

	m := NewManager(t.TempDir(), nil)
	out, err := m.fetchAYLangs(srv.URL, []string{"cht", "chs", "en"}, "avatar")
	if err != nil {
		t.Fatalf("a single 404 must not error: %v", err)
	}
	if len(out) != 2 || out["cht"] == nil || out["en"] == nil {
		t.Fatalf("want cht+en kept, chs skipped; got keys %v", keysOf(out))
	}
	if out["chs"] != nil {
		t.Errorf("404 lang must be absent, got body for chs")
	}
}

// When EVERY language fails the build can't proceed — surface it as an error so
// the warm is reported failed (not a silent empty index).
func TestFetchAYLangs_AllFailErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := NewManager(t.TempDir(), nil)
	if _, err := m.fetchAYLangs(srv.URL, []string{"cht", "cn", "en"}, "avatar"); err == nil {
		t.Fatal("all-langs-fail must return an error")
	}
}

func keysOf(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestFetchSources_Endfield(t *testing.T) {
	chars := `{"code":0,"data":{"catalog":[{"typeSub":[{"items":[{"itemId":"23","name":"卡契爾","brief":{"cover":"https://static.skport.com/x/aa.png"}}]}]}]}}`
	weps := `{"code":0,"data":{"catalog":[{"typeSub":[{"items":[{"itemId":"733","name":"狼之緋","brief":{"cover":"https://static.skport.com/x/wolf.png"}}]}]}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/web/v1/auth/refresh":
			w.Write([]byte(`{"code":0,"data":{"token":"TKN"}}`))
		case r.URL.Query().Get("typeSubId") == "1":
			w.Write([]byte(chars))
		case r.URL.Query().Get("typeSubId") == "2":
			w.Write([]byte(weps))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	SetSkportBaseForTest(srv.URL)
	defer SetSkportBaseForTest("https://zonai.skport.com")
	m := NewManager(t.TempDir(), nil)
	idx, err := fetchSources(m, core.GameID("hypergryph/endfield"))
	if err != nil {
		t.Fatalf("fetchSources err: %v", err)
	}
	if e, ok := idx.Resolve(core.GameID("hypergryph/endfield"), "卡契爾", false); !ok || e.ID != "23" {
		t.Errorf("char 卡契爾 not built: %+v ok=%v", e, ok)
	}
	if e, ok := idx.Resolve(core.GameID("hypergryph/endfield"), "狼之緋", true); !ok || e.ID != "733" {
		t.Errorf("weapon 狼之緋 not built: %+v ok=%v", e, ok)
	}
}

func TestFetchSources_Endfield_TokenFailReturnsErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	SetSkportBaseForTest(srv.URL)
	defer SetSkportBaseForTest("https://zonai.skport.com")
	m := NewManager(t.TempDir(), nil)
	_, err := fetchSources(m, core.GameID("hypergryph/endfield"))
	if err == nil {
		t.Fatal("total token failure must return err (so next refresh retries; not swallow to empty+nil)")
	}
}
