package hypergryph

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestProgressStore_InitAndMarkComplete(t *testing.T) {
	root := t.TempDir()
	ps := newProgressStore(root, "hypergryph/endfield", "1.2.6")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := ps.MarkComplete("data/a.bundle", time.Now(), 123); err != nil {
		t.Fatalf("mark: %v", err)
	}
	pf, err := core.LoadProgressFromPath(filepath.Join(ps.dir(), "progress.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e, ok := pf.Entries["data/a.bundle"]
	if !ok || e.Size != 123 {
		t.Errorf("entry = %+v, ok=%v", e, ok)
	}
	if pf.ETag != "etag-1" {
		t.Errorf("etag = %q", pf.ETag)
	}
}
