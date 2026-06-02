package hypergryph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestParseGetLatest_FixtureHasVersion(t *testing.T) {
	body, err := os.ReadFile("testdata/get_latest-sample.json")
	if err != nil {
		t.Skipf("fixture missing (Task A1 spike): %v", err)
	}
	resp, err := decodeGetLatest(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version == "" {
		t.Fatal("expected non-empty target version")
	}
}

func TestFetchGetLatest_HitsServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appcode") != gameAppCode {
			t.Errorf("missing appcode: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": 1, "version": "1.2.5",
			"pkg": map[string]any{"packs": []any{}},
		})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	resp, err := fetchGetLatest(context.Background(), srv.Client(), "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if resp.Version != "1.2.5" {
		t.Fatalf("version: got %q", resp.Version)
	}
}

func TestSanitizeURL_RedactsRandSegment(t *testing.T) {
	in := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/x.zip.001"
	want := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_<RAND>/packs/x.zip.001"
	if got := sanitizeURL(in); got != want {
		t.Fatalf("sanitizeURL:\n got %q\nwant %q", got, want)
	}
}
