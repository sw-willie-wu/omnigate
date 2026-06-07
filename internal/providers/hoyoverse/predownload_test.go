package hoyoverse

import (
	"context"
	"errors"
	"testing"

	"omnigate/internal/core"
)

func TestSupportsPredownload_SophonGamesOnly(t *testing.T) {
	p := New(Settings{}, nil)
	if !p.SupportsPredownload(core.GameID("hoyoverse/genshin")) {
		t.Error("genshin (UsesSophon) must support predownload in Phase 2")
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/starrail")) {
		t.Error("starrail (legacy) must NOT support predownload until Phase 3")
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/zzz")) {
		t.Error("zzz (legacy) must NOT support predownload until Phase 3")
	}
}

func TestCheckForPredownload_LegacyUnsupported(t *testing.T) {
	p := New(Settings{}, nil)
	_, err := p.CheckForPredownload(context.Background(), core.GameID("hoyoverse/starrail"), nil)
	if !errors.Is(err, core.ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported for legacy game", err)
	}
}
