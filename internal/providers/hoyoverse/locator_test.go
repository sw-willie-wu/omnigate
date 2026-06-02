package hoyoverse

import (
	"context"
	"testing"

	"omnigate/internal/core"
)

func TestHoYoPlayLocator_ParsesFixture(t *testing.T) {
	reader := func(biz string) (string, bool) {
		m := map[string]string{"hk4e_global": `D:\Games\Genshin`, "hkrpg_global": `D:\Games\StarRail`}
		v, ok := m[biz]
		return v, ok
	}
	got, err := hoyoplayLocator{read: reader}.LocateInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["hoyoverse/genshin"] != `D:\Games\Genshin` {
		t.Errorf("genshin: %+v", got)
	}
	if got["hoyoverse/starrail"] != `D:\Games\StarRail` {
		t.Errorf("starrail: %+v", got)
	}
	if _, ok := got["hoyoverse/zzz"]; ok {
		t.Errorf("zzz should be absent: %+v", got)
	}
}

func TestHoyoverseImplementsInstallLocator(t *testing.T) {
	var _ core.InstallLocator = New(Settings{}, nil)
}
