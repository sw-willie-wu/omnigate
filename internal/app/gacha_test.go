package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

func TestGachaProgressPayload(t *testing.T) {
	cfg := core.GachaConfig{Banners: []core.BannerConfig{
		{Key: "character", Label: core.LocalizedString{"en": "Character", "zh-TW": "限定角色"}},
	}}
	p := gachaProgressPayload(cfg, core.GachaProgress{BannerKey: "character", Page: 2, PoolIndex: 1, PoolTotal: 4})
	label, _ := p["banner"].(core.LocalizedString)
	if label["en"] != "Character" || p["page"].(int) != 2 || p["poolTotal"].(int) != 4 {
		t.Fatalf("payload=%+v", p)
	}
	// unknown banner key → fallback label = the raw key.
	p2 := gachaProgressPayload(cfg, core.GachaProgress{BannerKey: "mystery", Page: 1})
	label2, _ := p2["banner"].(core.LocalizedString)
	if label2["en"] != "mystery" {
		t.Fatalf("fallback label=%+v", label2)
	}
}

// fakeGachaProvider embeds fakeProvider BY VALUE (like fakeNewsProvider in
// app_test.go) so ID()/Games() are satisfied without a nil-pointer panic.
type fakeGachaProvider struct {
	fakeProvider
	res core.GachaFetchResult
	err error
}

func (f *fakeGachaProvider) FetchGacha(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	return f.res, f.err
}
func (f *fakeGachaProvider) GachaConfig(_ core.GameID) core.GachaConfig {
	return core.GachaConfig{HeadlineRank: 6, Banners: []core.BannerConfig{}, Currency: "NT$", ExpectedPity: 60}
}

func newTestAppWithGacha(t *testing.T, gp *fakeGachaProvider) *App {
	t.Helper()
	gid := core.GameID("hypergryph/endfield")
	base := fakeProvider{id: "hypergryph", games: []core.GameDescriptor{{ID: gid, Backend: "hypergryph"}}}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.ctx = context.Background()
	a.resolved = map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}}
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a.gachaStore = st
	if gp == nil {
		a.providers = []core.Provider{&base}
	} else {
		gp.fakeProvider = base
		a.providers = []core.Provider{gp}
	}
	return a
}

func TestRefreshThenGetSummary(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{res: core.GachaFetchResult{
		UID: "u1", URL: "https://x/page/gacha_y?token=z",
		Pulls: []core.GachaPull{{ID: "1", BannerKey: "special", Rank: 6, Name: "A"}},
	}})
	sum, err := a.RefreshGacha("hypergryph/endfield")
	if err != nil || !sum.Supported || sum.TotalPulls != 1 {
		t.Fatalf("refresh sum=%+v err=%v", sum, err)
	}
	got, err := a.GetGachaSummary("hypergryph/endfield")
	if err != nil || got.TotalPulls != 1 || got.UID != "u1" {
		t.Fatalf("get sum=%+v err=%v", got, err)
	}
}

func TestRefreshURLUnavailableSurfacesCode(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{err: core.ErrGachaURLUnavailable})
	_, err := a.RefreshGacha("hypergryph/endfield")
	if core.ErrorCode(err) != "gacha_url" {
		t.Fatalf("code=%q want gacha_url", core.ErrorCode(err))
	}
}

func TestGachaUnsupportedProvider(t *testing.T) {
	a := newTestAppWithGacha(t, nil)
	sum, err := a.GetGachaSummary("hypergryph/endfield")
	if err != nil || sum.Supported {
		t.Fatalf("want unsupported summary, got %+v err=%v", sum, err)
	}
}
