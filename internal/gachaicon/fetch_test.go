package gachaicon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
