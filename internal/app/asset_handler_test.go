package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/gachaicon"
)

var testImgServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write([]byte("ICONBYTES"))
}))

func TestAssetHandler_GachaIcon_ServesBytes(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "hoyoverse"})
	a.gachaIcons = gachaicon.NewManager(t.TempDir(), nil)
	idx := gachaicon.NewIndex()
	idx.PutForTest(core.GameID("hoyoverse/genshin"), "綾華", gachaicon.Entry{ID: "1", IconRef: "X", Kind: "char"})
	a.gachaIcons.SwapForTest(core.GameID("hoyoverse/genshin"), idx)
	a.gachaIcons.SetURLFnForTest(func(core.GameID, string, string) string { return testImgServer.URL })
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/hoyoverse/gachaicon/genshin.char.1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	if rr.Body.String() != "ICONBYTES" {
		t.Errorf("body %q", rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") == "" {
		t.Errorf("missing Cache-Control")
	}
}

func TestAssetHandler_GachaIcon_404OnMalformedKey(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "hoyoverse"})
	a.gachaIcons = gachaicon.NewManager(t.TempDir(), nil)
	h := newAssetHandler(a)
	for _, key := range []string{"genshin.char.", "genshin", "genshin.char", ".char.1"} {
		req := httptest.NewRequest(http.MethodGet, "/_asset/hoyoverse/gachaicon/"+key, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Errorf("key %q: status %d, want 404", key, rr.Code)
		}
	}
}

func TestAssetHandler_ParseAndDispatch_404OnUnknownBackend(t *testing.T) {
	a := newAppForTest(t /* no providers */)
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/unknown/icon/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestAssetHandler_404OnUnknownKind(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "kurogames"})
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/garbage/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestAssetHandler_RejectsDotDotInKey(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "kurogames"})
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/icon/..\\..\\hack", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 on '..' in key", rr.Code)
	}
}

func TestAssetHandler_BgDelegatesToAssetServer(t *testing.T) {
	served := false
	p := &fakeProviderWithAsset{
		fakeProvider: fakeProvider{id: "kurogames"},
		serve: func(kind, key string) ([]byte, string, error) {
			served = true
			return []byte("PNG-bytes"), "image/png", nil
		},
	}
	a := newAppForTest(t, p)
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/kurogames/bg/wuwa", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "PNG-bytes" {
		t.Errorf("body = %q, want PNG-bytes", rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cache-Control") == "" {
		t.Errorf("missing Cache-Control header")
	}
	if !served {
		t.Errorf("ServeAsset was not called")
	}
}

func TestAssetHandler_BgReturns404WhenProviderLacksAssetServer(t *testing.T) {
	a := newAppForTest(t, &fakeProvider{id: "hoyoverse"}) // no AssetServer impl
	h := newAssetHandler(a)
	req := httptest.NewRequest(http.MethodGet, "/_asset/hoyoverse/bg/genshin", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// fakeProviderWithAsset implements core.AssetServer too.
type fakeProviderWithAsset struct {
	fakeProvider
	serve func(kind, key string) ([]byte, string, error)
}

func (f *fakeProviderWithAsset) ServeAsset(ctx context.Context, kind, key string) ([]byte, string, error) {
	return f.serve(kind, key)
}
