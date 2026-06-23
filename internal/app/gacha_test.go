package app

import (
	"context"
	"errors"
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
	sum, err := a.RefreshGacha("hypergryph/endfield", "")
	if err != nil || !sum.Supported || sum.TotalPulls != 1 {
		t.Fatalf("refresh sum=%+v err=%v", sum, err)
	}
	got, err := a.GetGachaSummary("hypergryph/endfield", "")
	if err != nil || got.TotalPulls != 1 || got.UID != "u1" {
		t.Fatalf("get sum=%+v err=%v", got, err)
	}
}

func TestRefreshURLUnavailableSurfacesCode(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{err: core.ErrGachaURLUnavailable})
	_, err := a.RefreshGacha("hypergryph/endfield", "")
	if core.ErrorCode(err) != "gacha_url" {
		t.Fatalf("code=%q want gacha_url", core.ErrorCode(err))
	}
}

func TestGachaUnsupportedProvider(t *testing.T) {
	a := newTestAppWithGacha(t, nil)
	sum, err := a.GetGachaSummary("hypergryph/endfield", "")
	if err != nil || sum.Supported {
		t.Fatalf("want unsupported summary, got %+v err=%v", sum, err)
	}
}

// fakeSwitcherGachaProvider implements BOTH GachaProvider and AccountSwitcher.
type fakeSwitcherGachaProvider struct {
	fakeProvider
	res        core.GachaFetchResult
	err        error
	accounts   []core.GameAccount
	fetchCalls int
	swCalls    int
	swErr      error
	listErr    error
}

func (f *fakeSwitcherGachaProvider) FetchGacha(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	f.fetchCalls++
	return f.res, f.err
}
func (f *fakeSwitcherGachaProvider) GachaConfig(_ core.GameID) core.GachaConfig {
	return core.GachaConfig{HeadlineRank: 5, Banners: []core.BannerConfig{}, Currency: "astrite", ExpectedPity: 62.5}
}
func (f *fakeSwitcherGachaProvider) ListAccounts(_ context.Context, _ core.GameID) ([]core.GameAccount, error) {
	return f.accounts, f.listErr
}
func (f *fakeSwitcherGachaProvider) SwitchAccount(_ context.Context, _ core.GameID, _ string) error {
	f.swCalls++
	return f.swErr
}

func newTestAppWithSwitcherGacha(t *testing.T, gp *fakeSwitcherGachaProvider) *App {
	t.Helper()
	gid := core.GameID("kurogames/wutheringwaves")
	base := fakeProvider{id: "kurogames", games: []core.GameDescriptor{{ID: gid, Backend: "kurogames"}}}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.ctx = context.Background()
	a.resolved = map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}}
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a.gachaStore = st
	gp.fakeProvider = base
	a.providers = []core.Provider{gp}
	return a
}

func TestGetSummary_UsesActiveAccountUID(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{accounts: []core.GameAccount{
		{ID: "cuidX", UID: "uidX", Active: true},
		{ID: "cuidY", UID: "uidY", Active: false},
	}}
	a := newTestAppWithSwitcherGacha(t, gp)
	game := "kurogames/wutheringwaves"
	a.gachaStore.UpsertPulls(game, "uidX", []core.GachaPull{{ID: "1", BannerKey: "character", Rank: 5, Name: "X1"}})
	// uidY written last → would be LatestUID; the active path must ignore it.
	a.gachaStore.UpsertPulls(game, "uidY", []core.GachaPull{
		{ID: "1", BannerKey: "character", Rank: 5, Name: "Y1"},
		{ID: "2", BannerKey: "character", Rank: 5, Name: "Y2"},
	})
	sum, err := a.GetGachaSummary(game, "")
	if err != nil || sum.UID != "uidX" || sum.TotalPulls != 1 {
		t.Fatalf("sum=%+v err=%v (want active uidX, 1 pull)", sum, err)
	}
}

func TestGetSummary_ActiveUnknown(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{accounts: []core.GameAccount{{ID: "cuidX", UID: "", Active: true}}}
	a := newTestAppWithSwitcherGacha(t, gp)
	sum, err := a.GetGachaSummary("kurogames/wutheringwaves", "")
	if err != nil || !sum.Supported || !sum.ActiveUnknown || sum.TotalPulls != 0 {
		t.Fatalf("sum=%+v err=%v (want Supported+ActiveUnknown, 0 pulls)", sum, err)
	}
}

func TestGetSummary_NonSwitcherUsesLatestUID(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{})
	game := "hypergryph/endfield"
	a.gachaStore.UpsertPulls(game, "uidLatest", []core.GachaPull{{ID: "1", Rank: 6}})
	sum, err := a.GetGachaSummary(game, "")
	if err != nil || sum.UID != "uidLatest" || sum.ActiveUnknown {
		t.Fatalf("non-switcher should use LatestUID without ActiveUnknown, got %+v err=%v", sum, err)
	}
}

func TestRefresh_WrongAccount(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{
		accounts: []core.GameAccount{{ID: "cuidX", UID: "uidX", Active: true}},
		res:      core.GachaFetchResult{UID: "uidY", Pulls: []core.GachaPull{{ID: "1", Rank: 5}}},
	}
	a := newTestAppWithSwitcherGacha(t, gp)
	game := "kurogames/wutheringwaves"
	_, err := a.RefreshGacha(game, "")
	if !errors.Is(err, core.ErrGachaWrongAccount) {
		t.Fatalf("want ErrGachaWrongAccount, got %v", err)
	}
	all, _ := a.gachaStore.AllPulls(game, "uidY")
	if len(all) != 0 {
		t.Fatalf("must not upsert on mismatch, got %d pulls", len(all))
	}
}

func TestRefresh_MatchUpserts(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{
		accounts: []core.GameAccount{{ID: "cuidX", UID: "uidX", Active: true}},
		res: core.GachaFetchResult{
			UID: "uidX", URL: "https://x#/record?record_id=r&player_id=uidX",
			Pulls: []core.GachaPull{{ID: "1", BannerKey: "character", Rank: 5, Name: "A"}},
		},
	}
	a := newTestAppWithSwitcherGacha(t, gp)
	sum, err := a.RefreshGacha("kurogames/wutheringwaves", "")
	if err != nil || sum.UID != "uidX" || sum.TotalPulls != 1 {
		t.Fatalf("sum=%+v err=%v (want uidX, 1 pull)", sum, err)
	}
}

func TestRefresh_ActiveUnknownNoFetch(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{accounts: []core.GameAccount{{ID: "cuidX", UID: "", Active: true}}}
	a := newTestAppWithSwitcherGacha(t, gp)
	_, err := a.RefreshGacha("kurogames/wutheringwaves", "")
	if !errors.Is(err, core.ErrGachaActiveUnknown) {
		t.Fatalf("want ErrGachaActiveUnknown, got %v", err)
	}
	if gp.fetchCalls != 0 {
		t.Fatalf("must not fetch when active uid unknown, fetchCalls=%d", gp.fetchCalls)
	}
}

func TestGetSummary_ExplicitNonActiveAccount(t *testing.T) {
	gp := &fakeSwitcherGachaProvider{accounts: []core.GameAccount{
		{ID: "cuidX", UID: "uidX", Active: true},
		{ID: "cuidY", UID: "uidY", Active: false},
	}}
	a := newTestAppWithSwitcherGacha(t, gp)
	game := "kurogames/wutheringwaves"
	a.gachaStore.UpsertPulls(game, "uidX", []core.GachaPull{{ID: "1", BannerKey: "character", Rank: 5, Name: "X1"}})
	a.gachaStore.UpsertPulls(game, "uidY", []core.GachaPull{
		{ID: "1", BannerKey: "character", Rank: 5, Name: "Y1"},
		{ID: "2", BannerKey: "character", Rank: 5, Name: "Y2"},
	})
	// Explicitly select the NON-active account Y → its uid + 2 pulls.
	sum, err := a.GetGachaSummary(game, "cuidY")
	if err != nil || sum.UID != "uidY" || sum.TotalPulls != 2 {
		t.Fatalf("sum=%+v err=%v (want uidY, 2 pulls)", sum, err)
	}
}

// ── Credential-path tests (Task 5) ──

// fakeCredProvider implements both GachaProvider (via embedded *fakeGachaProvider)
// and GachaCredentialProvider, allowing tests of the Endfield credential branch.
type fakeCredProvider struct {
	*fakeGachaProvider // embeds GachaConfig + a no-op FetchGacha
	res                core.GachaFetchResult
	err                error
	gotLang            string
}

func (f *fakeCredProvider) FetchGachaWithCredential(_ context.Context, _ core.GameID, cred, lang string) (core.GachaFetchResult, error) {
	f.gotLang = lang
	if f.err != nil {
		return core.GachaFetchResult{}, f.err
	}
	return f.res, nil
}

// newTestAppWithCredProvider builds a test App with prov registered for gameID.
// CRITICAL: a.ctx is left nil so a.emit() is a safe no-op. With a non-nil
// context.Background(), wruntime.EventsEmit hits log.Fatalf → os.Exit(1) and
// kills the whole go test binary. RefreshGacha nil-guards base := a.ctx, so a
// nil ctx is safe for the 120 s timeout path.
func newTestAppWithCredProvider(t *testing.T, gameID string, prov *fakeCredProvider) *App {
	t.Helper()
	gid := core.GameID(gameID)
	backendID, _, _ := core.ParseGameID(gid)
	base := fakeProvider{id: backendID, games: []core.GameDescriptor{{ID: gid, Backend: backendID}}}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	// Do NOT set a.ctx — see critical note above.
	a.resolved = map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}}
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a.gachaStore = st
	prov.fakeGachaProvider.fakeProvider = base
	a.providers = []core.Provider{prov}
	return a
}

func TestRefreshGacha_CredentialPath(t *testing.T) {
	prov := &fakeCredProvider{
		fakeGachaProvider: &fakeGachaProvider{},
		res: core.GachaFetchResult{
			UID:   "R2",
			Pulls: []core.GachaPull{{ID: "1", BannerKey: "standard", ItemType: "char", Rank: 6, Name: "X", Time: "2026-01-01 00:00:00"}},
		},
	}
	a := newTestAppWithCredProvider(t, "hypergryph/endfield", prov)
	_ = a.gachaStore.UpsertGachaAccount(store.GachaAccount{ID: "ga_x", Game: "hypergryph/endfield", Token: "acct-TOK", Active: true})

	sum, err := a.RefreshGacha("hypergryph/endfield", "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalPulls != 1 {
		t.Fatalf("total = %d; want 1", sum.TotalPulls)
	}
	if prov.gotLang == "" {
		t.Error("lang not passed to provider")
	}
}

func TestRefreshGacha_NoCredentialRequired(t *testing.T) {
	prov := &fakeCredProvider{fakeGachaProvider: &fakeGachaProvider{}}
	a := newTestAppWithCredProvider(t, "hypergryph/endfield", prov)
	if _, err := a.RefreshGacha("hypergryph/endfield", ""); !errors.Is(err, core.ErrGachaCredentialRequired) {
		t.Fatalf("err = %v; want ErrGachaCredentialRequired", err)
	}
}

func TestGetGachaSummary_UnlinkedRequired(t *testing.T) {
	prov := &fakeCredProvider{fakeGachaProvider: &fakeGachaProvider{}}
	a := newTestAppWithCredProvider(t, "hypergryph/endfield", prov)
	if _, err := a.GetGachaSummary("hypergryph/endfield", ""); !errors.Is(err, core.ErrGachaCredentialRequired) {
		t.Fatalf("err = %v; want ErrGachaCredentialRequired", err)
	}
}

func TestMapEndfieldLang(t *testing.T) {
	cases := map[string]string{"zh-TW": "zh-tw", "zh-CN": "zh-cn", "en": "en-us", "": "en-us"}
	for in, want := range cases {
		if got := mapEndfieldLang(in); got != want {
			t.Errorf("mapEndfieldLang(%q) = %q; want %q", in, got, want)
		}
	}
}
