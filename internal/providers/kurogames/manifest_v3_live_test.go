//go:build live

package kurogames

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestLiveFetchGameIndexV3(t *testing.T) {
	c := &http.Client{Timeout: 30 * time.Second}
	idx, err := fetchGameIndexV3(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	cdn := pickCDN(idx.CDNList)
	for pack, cfg := range idx.ResourcePacks {
		f, err := fetchPackIndexFile(context.Background(), c, cdn, cfg)
		if err != nil {
			t.Fatalf("%s: %v", pack, err)
		}
		if err := validatePackIndexFile(idx, pack, f); err != nil {
			t.Fatalf("%s: %v", pack, err)
		}
		t.Logf("%s %s files=%d", pack, cfg.Version, len(f.Resource))
	}
}
