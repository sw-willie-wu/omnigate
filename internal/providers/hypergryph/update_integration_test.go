package hypergryph

import "testing"

// Live get_latest E2E is exercised by the Phase A smoke against the real
// GRYPHLINK CDN. The SetAPIBaseURL seam (update_manifest.go) lets Phase B run
// download/apply against an httptest server; placeholder kept for parity.
func TestIntegration_LiveGetLatest(t *testing.T) {
	t.Skip("live E2E covered by Phase A smoke; see m3c-endfield-update-protocol.md")
}
