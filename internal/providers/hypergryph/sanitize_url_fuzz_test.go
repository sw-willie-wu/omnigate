package hypergryph

import (
	"strings"
	"testing"
)

func FuzzSanitizeURL(f *testing.F) {
	f.Add("https://beyond.hg-cdn.com/X/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/x.zip.001")
	f.Add("")
	f.Add("not a url")
	f.Fuzz(func(t *testing.T, s string) {
		out := sanitizeURL(s)
		if sanitizeURL(out) != out {
			t.Fatalf("sanitizeURL not idempotent: %q -> %q -> %q", s, out, sanitizeURL(out))
		}
		_ = strings.TrimSpace(out)
	})
}
