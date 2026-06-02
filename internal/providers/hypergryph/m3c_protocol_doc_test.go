package hypergryph

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestProtocolDocPackURLRegex asserts the TEST_ANCHOR regex in the research
// doc compiles and matches a representative pack URL — detects drift between
// the documented protocol and the code's expectations.
func TestProtocolDocPackURLRegex(t *testing.T) {
	docPath := "../../../docs/superpowers/research/m3c-endfield-update-protocol.md"
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("research markdown missing: %v", err)
	}
	re := regexp.MustCompile(`(?s)<!-- TEST_ANCHOR: pack_url_regex -->\s*\n(.*?)<!-- END_ANCHOR: pack_url_regex -->`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		t.Fatal("pack_url_regex TEST_ANCHOR not found")
	}
	pat := strings.TrimSpace(m[1])
	if pat == "" {
		t.Fatal("pack_url_regex TEST_ANCHOR block is empty")
	}
	compiled, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("doc regex does not compile: %v", err)
	}
	sample := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/Beyond_Release.zip.001"
	if !compiled.MatchString(sample) {
		t.Fatalf("doc pack_url_regex does not match sample pack URL")
	}
}
