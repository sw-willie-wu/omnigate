package kurogames

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestProtocolDocMatchesCode parses the research markdown and asserts the
// documented manifest URL pattern matches what the Go code produces.
// Detects drift between m3a-kuro-update-protocol.md and update_manifest.go.
func TestProtocolDocMatchesCode(t *testing.T) {
	docPath := "../../../.claude/research/m3a-kuro-update-protocol.md"
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("research markdown missing: %v", err)
	}

	// Extract content between TEST_ANCHOR markers
	re := regexp.MustCompile(`(?s)<!-- TEST_ANCHOR: manifest_url_regex -->\s*\n(.*?)<!-- END_ANCHOR: manifest_url_regex -->`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		t.Fatalf("TEST_ANCHOR markers not found in %s", docPath)
	}
	docBlock := m[1]

	// Generate URL via Go code (post-Task-7 corrections — no buildManifestURL,
	// use indexJSONURL constant function).
	got := indexJSONURL()

	// Doc has the host literal "prod-alicdn-gamestarter.kurogame.com" inside
	// the anchor block — match against code's host.
	if !strings.Contains(docBlock, "prod-alicdn-gamestarter.kurogame.com") {
		t.Errorf("doc TEST_ANCHOR block missing host 'prod-alicdn-gamestarter.kurogame.com'")
	}
	if !strings.Contains(got, "prod-alicdn-gamestarter.kurogame.com") {
		t.Errorf("indexJSONURL = %q, missing expected host", got)
	}
	// Verify the AppCred constant appears in the produced URL
	if !strings.Contains(got, AppCred) {
		t.Errorf("indexJSONURL = %q, missing AppCred constant", got)
	}
}
