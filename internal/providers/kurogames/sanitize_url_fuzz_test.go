package kurogames

import (
	"strings"
	"testing"
)

func FuzzSanitizeURL(f *testing.F) {
	f.Add("https://prod.kurogame.com/launcher/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/G153/x")
	f.Add("https://x.com/?token=" + strings.Repeat("a", 32))
	f.Add("not a url")
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		out := sanitizeURL(in)
		if accountIDRe.MatchString(out) {
			t.Errorf("sanitizeURL leaked accountID pattern: %q → %q", in, out)
		}
	})
}
