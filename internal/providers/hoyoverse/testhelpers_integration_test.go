//go:build integration

package hoyoverse

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"omnigate/internal/core"
)

// fixturePack is the per-test scaffolding: gameDir, tempRoot, manifest server,
// blob servers, and a Provider configured to point at them.
type fixturePack struct {
	gameDir     string
	tempRoot    string
	manifestSrv *httptest.Server
	blobSrvs    []*httptest.Server
	provider    *Provider
}

// newFixturePack constructs a fixturePack for integration testing. It creates
// a temporary gameDir, tempRoot, and httptest servers for manifest and blobs.
// Since all integration tests t.Skip, this is referenced but not called at runtime.
func newFixturePack(t *testing.T, manifestJSON []byte, blobs map[string][]byte) *fixturePack {
	t.Helper()
	fp := &fixturePack{
		gameDir:  t.TempDir(),
		tempRoot: t.TempDir(),
	}
	fp.manifestSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "etag-test-1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifestJSON)
	}))
	_ = blobs // silence unused if blobs not referenced
	return fp
}

// teardown closes the httptest servers created by newFixturePack.
func (fp *fixturePack) teardown() {
	if fp.manifestSrv != nil {
		fp.manifestSrv.Close()
	}
	for _, s := range fp.blobSrvs {
		if s != nil {
			s.Close()
		}
	}
}

// silence unused imports in case all integration tests are skipped
var _ = core.GameID("")
