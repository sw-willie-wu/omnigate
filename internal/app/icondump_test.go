package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpFallbackIcons is a guarded developer utility, not an assertion test.
// Run it to (re)generate the bundled fallback game icons the frontend uses
// when the live icon source is unavailable (e.g. /_asset/* 404s under
// `wails dev`, or the HoYoPlay icon API is unreachable):
//
//	DUMP_ICONS=1 go test ./internal/app -run TestDumpFallbackIcons -count=1 -v
//
// It builds a real App from the repo-root settings.toml, then for every
// installed game writes <backend>-<game>.png into frontend/src/assets/gameicons/.
// Sources mirror the live pipeline exactly:
//   - /_asset/<backend>/icon/<key>  → exe extraction via the real asset handler
//     (kurogames/WuWa, hypergryph/Endfield — no clean remote icon exists)
//   - https://…                     → downloaded as-is (HoYoverse getGames API)
func TestDumpFallbackIcons(t *testing.T) {
	if os.Getenv("DUMP_ICONS") == "" {
		t.Skip("guarded utility; set DUMP_ICONS=1 to regenerate fallback icons")
	}

	// Test CWD is the package dir (internal/app); the app's real settings file
	// (with the user's install paths) lives at the repo root.
	settingsPath := filepath.Join("..", "..", "settings.toml")
	outDir := filepath.Join("..", "..", "frontend", "src", "assets", "gameicons")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	a := New(settingsPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.ctx = context.Background() // hoyoverse GetIcon needs a non-nil ctx for its API call
	handler := AssetHandlerForApp(a)

	rows, err := a.ListGames()
	if err != nil {
		t.Fatalf("ListGames: %v", err)
	}

	wrote := 0
	for _, row := range rows {
		if !row.Installed {
			continue
		}
		url, err := a.GetIcon(row.ID)
		if err != nil || url == "" {
			t.Logf("SKIP %s: GetIcon: %v", row.ID, err)
			continue
		}

		var data []byte
		switch {
		case strings.HasPrefix(url, "/_asset/"):
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Logf("SKIP %s: asset handler returned %d for %s", row.ID, rec.Code, url)
				continue
			}
			data = rec.Body.Bytes()
		case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
			resp, err := http.Get(url) //nolint:gosec // launcher-provided icon URL
			if err != nil {
				t.Logf("SKIP %s: GET %s: %v", row.ID, url, err)
				continue
			}
			b, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Logf("SKIP %s: GET %s: status=%d err=%v", row.ID, url, resp.StatusCode, err)
				continue
			}
			data = b
		default:
			t.Logf("SKIP %s: unrecognized icon url %q", row.ID, url)
			continue
		}

		flat := strings.ReplaceAll(row.ID, "/", "-")
		dst := filepath.Join(outDir, flat+".png")
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
		t.Logf("WROTE %s (%d bytes) from %s", dst, len(data), url)
		wrote++
	}

	if wrote == 0 {
		t.Fatalf("no icons written; expected at least the installed games")
	}
	t.Logf("dumped %d fallback icons to %s", wrote, outDir)
}
