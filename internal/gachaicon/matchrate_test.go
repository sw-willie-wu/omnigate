//go:build matchrate
// +build matchrate

package gachaicon

// Run: go test ./internal/gachaicon/ -tags matchrate -run MatchRate -v
//
// NOTE: api.hakush.in shut down (2026-02-14) and is now NXDOMAIN. Data is served
// by the revived nanoka.cc mirror under a VERSIONED layout, so we read the latest
// ww version from the manifest and build the data URLs from it.
import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s -> %d", url, resp.StatusCode)
	}
	return b
}

// wuwaLatestVersion reads .ww.latest from the nanoka manifest.
func wuwaLatestVersion(t *testing.T) string {
	t.Helper()
	var m map[string]struct {
		Latest string `json:"latest"`
	}
	if err := json.Unmarshal(fetch(t, "https://static.nanoka.cc/manifest.json"), &m); err != nil {
		t.Fatal(err)
	}
	if m["ww"].Latest == "" {
		t.Fatal("no ww.latest in manifest")
	}
	return m["ww"].Latest
}

func TestMatchRate_WuWa(t *testing.T) {
	raw, err := os.ReadFile("testdata/wuwa_real_names.txt")
	if err != nil {
		t.Skip("no real-names fixture")
	}
	var names []string
	for _, l := range strings.Split(string(raw), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			names = append(names, s)
		}
	}
	v := wuwaLatestVersion(t)
	gid := core.GameID("kurogames/wutheringwaves")
	idx := NewIndex()
	if err := buildFromHakush(idx, gid, "char", 5, fetch(t, "https://static.nanoka.cc/ww/"+v+"/character.json")); err != nil {
		t.Fatal(err)
	}
	if err := buildFromHakush(idx, gid, "weapon", 5, fetch(t, "https://static.nanoka.cc/ww/"+v+"/weapon.json")); err != nil {
		t.Fatal(err)
	}
	applyOverrides(idx)
	var miss []string
	for _, n := range names {
		if _, ok := idx.Resolve(gid, n, false); !ok {
			miss = append(miss, n)
		}
	}
	rate := float64(len(names)-len(miss)) / float64(len(names)) * 100
	t.Logf("WuWa(ww %s) match rate: %.1f%% (%d/%d). MISSES (%d): %s", v, rate, len(names)-len(miss), len(names), len(miss), strings.Join(miss, ", "))
	// NOTE: do NOT fail on a threshold — the human reviews the miss list and decides overrides.
}
