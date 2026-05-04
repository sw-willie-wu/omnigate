package hypergryph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCurrentBgURL_FallsBackToDefaultWhenNoCache(t *testing.T) {
	// On Windows os.UserCacheDir reads %LOCALAPPDATA%; redirect it.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	got := CurrentBgURL(context.Background(), nil)
	if got != defaultBgURL {
		t.Errorf("CurrentBgURL on empty FS = %q, want defaultBgURL", got)
	}
}

func TestScanCachedWebpURLs_PicksWebpFromEndfieldFolder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	base := filepath.Join(tmp, "Games", "deadbeef", "cache", "Cache")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mix: 1 Endfield .webp (should match); 1 Endfield .jpg (rejected by webp-only regex);
	// 1 POPUCOM .webp (rejected by game-folder regex).
	body := `prefix ` +
		`https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/YDUTE5gscDZ229CW/aa/bb/00000000000000000000000000000001.webp tail1 ` +
		`https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/YDUTE5gscDZ229CW/cc/dd/00000000000000000000000000000002.jpg tail2 ` +
		`https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/FtQqkyFLX4Z0bg8G/ee/ff/00000000000000000000000000000003.webp tail3`
	if err := os.WriteFile(filepath.Join(base, "data_1"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := scanCachedWebpURLs()
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %q", len(got), got)
	}
	want := "https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/YDUTE5gscDZ229CW/aa/bb/00000000000000000000000000000001.webp"
	if got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func TestPickLargestRecent_FiltersBySizeAndPrefersNewerLastModified(t *testing.T) {
	mux := http.NewServeMux()
	addEntry := func(path string, contentLength int, lastMod time.Time) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(contentLength))
			w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
		})
	}
	addEntry("/small", 500_000, time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC))     // <1MiB → rejected
	addEntry("/old-big", 3_000_000, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) // passes size, older
	addEntry("/new-big", 3_500_000, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) // winner

	srv := httptest.NewServer(mux)
	defer srv.Close()

	urls := []string{srv.URL + "/small", srv.URL + "/old-big", srv.URL + "/new-big"}
	got := pickLargestRecent(context.Background(), urls, nil)
	want := srv.URL + "/new-big"
	if got != want {
		t.Errorf("pickLargestRecent = %q, want %q", got, want)
	}
}

func TestPickLargestRecent_ReturnsEmptyWhenAllUndersize(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tiny", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := pickLargestRecent(context.Background(), []string{srv.URL + "/tiny"}, nil)
	if got != "" {
		t.Errorf("pickLargestRecent = %q, want empty", got)
	}
}
