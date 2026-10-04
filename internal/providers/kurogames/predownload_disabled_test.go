package kurogames

import (
	"context"
	"errors"
	"testing"

	"omnigate/internal/core"
)

func TestPredownloadDisabled(t *testing.T) {
	p := New(Settings{}, nil)
	if p.SupportsPredownload(gidWuwa) {
		t.Fatal("SupportsPredownload must be false")
	}
	if _, err := p.CheckForPredownload(context.Background(), gidWuwa, nil); !errors.Is(err, core.ErrPredownloadUnsupported) {
		t.Fatalf("err=%v", err)
	}
}
