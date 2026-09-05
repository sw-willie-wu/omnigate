package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// testPity is a minimal core.PityModel for use in test GachaConfigs. It records no
// headline hits and reports zero trailing pity — sufficient for tests that only check
// the known-map capture, not the computed summary statistics.
type testPity struct{}

func (testPity) HardPity() int                                        { return 80 }
func (testPity) Has5050() bool                                        { return false }
func (testPity) Walk(_ []core.GachaPull, _ int) ([]core.PityHit, int) { return nil, 0 }

// fakeLoginCredProvider satisfies core.Provider (via embedded fakeProvider),
// core.GachaLoginProvider, core.GachaCredentialProvider, and
// core.GachaUserInfoProvider. Used by newTestAppWithEndfield to simulate the
// Endfield per-account gacha flow.
type fakeLoginCredProvider struct {
	fakeProvider
	loginRes     core.GachaLoginResult
	loginErr     error
	fetchRes     core.GachaFetchResult
	fetchErr     error
	lastKnown    map[string]bool // captures the `known` arg of the most recent FetchGachaWithCredential call
	lastDeadline time.Time       // captures ctx deadline (observes the 120s vs 300s repair window)
	userInfo     core.GachaUserInfo
	userInfoErr  error
}

func (f *fakeLoginCredProvider) LoginByEmailPassword(_ context.Context, _, _ string) (core.GachaLoginResult, error) {
	return f.loginRes, f.loginErr
}

func (f *fakeLoginCredProvider) FetchUserInfo(_ context.Context, _ string) (core.GachaUserInfo, error) {
	return f.userInfo, f.userInfoErr
}

func (f *fakeLoginCredProvider) FetchGachaWithCredential(ctx context.Context, _ core.GameID, _, _ string, known map[string]bool) (core.GachaFetchResult, error) {
	f.lastKnown = known
	if d, ok := ctx.Deadline(); ok {
		f.lastDeadline = d
	}
	return f.fetchRes, f.fetchErr
}

// FetchGacha + GachaConfig satisfy core.GachaProvider so the credential branch of
// GetGachaSummary/RefreshGacha (which does p.(core.GachaProvider) then
// gp.GachaConfig(gid)) compiles and runs. The credential path uses
// FetchGachaWithCredential, not FetchGacha, so this is a no-op.
func (f *fakeLoginCredProvider) FetchGacha(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	return core.GachaFetchResult{}, nil
}

// GachaConfig returns a config that mirrors real Endfield: "special" (特許尋訪) is
// PerPool=true; "standard" is not PerPool. Both banners carry a non-nil testPity so
// ComputeSummary can call Pity.Walk without panicking.
func (f *fakeLoginCredProvider) GachaConfig(_ core.GameID) core.GachaConfig {
	return core.GachaConfig{
		HeadlineRank: 6,
		Banners: []core.BannerConfig{
			{Key: "special", PerPool: true, Pity: testPity{}},
			{Key: "weapon", PerPool: true, Pity: testPity{}},
			{Key: "standard", Pity: testPity{}},
		},
		Currency:     "NT$",
		ExpectedPity: 60,
	}
}

// newTestAppWithEndfield builds a test App with a fake Endfield provider that
// implements both GachaLoginProvider and GachaCredentialProvider.
//
// IMPORTANT: a.ctx is intentionally left nil. With a non-nil context,
// a.emit() calls wruntime.EventsEmit which hits log.Fatalf → os.Exit in tests.
// All methods under test nil-guard a.ctx before use, so nil is safe here.
func newTestAppWithEndfield(t *testing.T) *App {
	t.Helper()
	gid := core.GameID("hypergryph/endfield")
	backendID, _, _ := core.ParseGameID(gid)
	prov := &fakeLoginCredProvider{
		fakeProvider: fakeProvider{
			id:    backendID,
			games: []core.GameDescriptor{{ID: gid, Backend: backendID}},
		},
		loginRes: core.GachaLoginResult{Token: "tok", HgID: "hg", Email: "e@x"},
		fetchRes: core.GachaFetchResult{
			UID: "ROLE42",
			Pulls: []core.GachaPull{
				{ID: "1", BannerKey: "standard", ItemType: "char", Rank: 6, Name: "X", Time: "2026-01-01 00:00:00"},
			},
		},
	}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	// Do NOT set a.ctx — see critical note above.
	a.resolved = map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}}
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a.gachaStore = st
	a.store = st // StateStore view of the same DB (v7 repair-key path)
	a.providers = []core.Provider{prov}
	return a
}

// seedAccount upserts a GachaAccount row for "hypergryph/endfield" with the
// given id+uid (set active) and seeds n pulls under that uid. Reused by Task 6.
func seedAccount(t *testing.T, a *App, id, uid string, n int) {
	t.Helper()
	const game = "hypergryph/endfield"
	acc := store.GachaAccount{
		ID:     id,
		Game:   game,
		UID:    uid,
		Label:  "seed-" + id,
		Active: true,
	}
	if err := a.gachaStore.UpsertGachaAccount(acc); err != nil {
		t.Fatalf("seedAccount UpsertGachaAccount: %v", err)
	}
	if err := a.gachaStore.SetActiveGachaAccount(game, id); err != nil {
		t.Fatalf("seedAccount SetActive: %v", err)
	}
	if n > 0 {
		pulls := make([]core.GachaPull, n)
		for i := range pulls {
			pulls[i] = core.GachaPull{
				ID:        fmt.Sprintf("seed-%s-%d", id, i),
				BannerKey: "standard",
				Rank:      4,
			}
		}
		if _, err := a.gachaStore.UpsertPulls(game, uid, pulls); err != nil {
			t.Fatalf("seedAccount UpsertPulls: %v", err)
		}
	}
}

func TestAddGachaAccountByLogin_SavesAccountThenRefreshWritesBackUID(t *testing.T) {
	a := newTestAppWithEndfield(t) // fake: Login→{tok,hg,e@x}, Fetch→res.UID="ROLE42", 1 pull
	acc, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Add no longer blocks on the (slow) record fetch: the account is saved with an
	// empty uid + email-default label, and is set active. The board drives the
	// refresh-with-progress separately.
	if acc.UID != "" {
		t.Fatalf("uid = %q, want empty pre-refresh (fetch is deferred to the board)", acc.UID)
	}
	if acc.Email != "e@x" || acc.Label != "e@x" {
		t.Fatalf("acc = %+v (label defaults to email)", acc)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 || !accts[0].Active {
		t.Fatalf("want one active saved account, got %+v", accts)
	}

	// A subsequent refresh (what the board triggers) fetches the records and writes
	// back the roleId uid onto the account.
	sum, err := a.RefreshGacha("hypergryph/endfield", acc.ID)
	if err != nil || sum.TotalPulls == 0 {
		t.Fatalf("refresh: sum=%+v err=%v", sum, err)
	}
	got, err := a.gachaStore.GetGachaAccount(acc.ID)
	if err != nil || got.UID != "ROLE42" {
		t.Fatalf("uid not written back: %+v err=%v", got, err)
	}
}

func TestAddGachaAccountByLogin_DedupsByHgID(t *testing.T) {
	a := newTestAppWithEndfield(t) // fake login always returns HgID="hg"
	acc1, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatal(err)
	}
	acc2, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if acc1.ID != acc2.ID {
		t.Errorf("re-login created a new row: %s vs %s (should dedup by HgID)", acc1.ID, acc2.ID)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 {
		t.Fatalf("want 1 account after re-login, got %d", len(accts))
	}
}

func TestGameAccountKind(t *testing.T) {
	a := newTestAppWithEndfield(t)
	if k := a.GameAccountKind("hypergryph/endfield"); k != "credential" {
		t.Errorf("endfield kind = %q, want credential", k)
	}
}

func TestGetGachaSummary_CredentialResolvesSelectedAccount(t *testing.T) {
	a := newTestAppWithEndfield(t)
	seedAccount(t, a, "ga_A", "ROLE_A", 3)
	seedAccount(t, a, "ga_B", "ROLE_B", 5)
	sumA, _ := a.GetGachaSummary("hypergryph/endfield", "ga_A")
	sumB, _ := a.GetGachaSummary("hypergryph/endfield", "ga_B")
	if sumA.TotalPulls != 3 || sumB.TotalPulls != 5 {
		t.Fatalf("A=%d B=%d, want 3/5 (per-account partition)", sumA.TotalPulls, sumB.TotalPulls)
	}
	_ = a.SelectGachaAccount("hypergryph/endfield", "ga_B")
	sumActive, _ := a.GetGachaSummary("hypergryph/endfield", "")
	if sumActive.TotalPulls != 5 {
		t.Fatalf("active resolve = %d, want 5", sumActive.TotalPulls)
	}
}

func TestGetGachaSummary_NoAccounts_RequiresCredential(t *testing.T) {
	a := newTestAppWithEndfield(t)
	if _, err := a.GetGachaSummary("hypergryph/endfield", ""); !errors.Is(err, core.ErrGachaCredentialRequired) {
		t.Fatalf("err = %v, want ErrGachaCredentialRequired", err)
	}
}

// TestBackfillPoolID_ForcesFullFetch verifies that when a PerPool (special) banner
// has stored pulls with empty PoolID, refreshAndWriteBackUID forces a full re-fetch
// by passing known=nil to FetchGachaWithCredential.
func TestBackfillPoolID_ForcesFullFetch(t *testing.T) {
	const game = "hypergryph/endfield"
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)

	// Seed account with a known UID so the existing-pulls block is entered.
	seedAccount(t, a, "ga_backfill", "ROLE42", 0)

	// Upsert a "special" pull with empty PoolID — predates per-pool capture.
	pulls := []core.GachaPull{
		{ID: "sp-1", BannerKey: "special", Rank: 5, Name: "A", Time: "2026-01-01 00:00:00", PoolID: ""},
	}
	if _, err := a.gachaStore.UpsertPulls(game, "ROLE42", pulls); err != nil {
		t.Fatalf("UpsertPulls: %v", err)
	}

	if _, err := a.RefreshGacha(game, "ga_backfill"); err != nil {
		t.Fatalf("RefreshGacha: %v", err)
	}

	// known must be nil → full re-fetch to backfill poolId.
	if prov.lastKnown != nil {
		t.Errorf("lastKnown = %v; want nil (full re-fetch forced by missing PerPool PoolID)", prov.lastKnown)
	}
}

// TestBackfillPoolID_IncrementalWhenPopulated verifies that when all PerPool pulls
// already have a PoolID, refreshAndWriteBackUID passes a non-empty known map
// (incremental sync — no backfill needed).
func TestBackfillPoolID_IncrementalWhenPopulated(t *testing.T) {
	const game = "hypergryph/endfield"
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)

	seedAccount(t, a, "ga_incr", "ROLE42", 0)

	// Upsert a "special" pull WITH a PoolID already set.
	pulls := []core.GachaPull{
		{ID: "sp-2", BannerKey: "special", Rank: 5, Name: "B", Time: "2026-01-02 00:00:00", PoolID: "pool-001"},
	}
	if _, err := a.gachaStore.UpsertPulls(game, "ROLE42", pulls); err != nil {
		t.Fatalf("UpsertPulls: %v", err)
	}

	if _, err := a.RefreshGacha(game, "ga_incr"); err != nil {
		t.Fatalf("RefreshGacha: %v", err)
	}

	// known must be non-nil and non-empty → incremental sync.
	if prov.lastKnown == nil || len(prov.lastKnown) == 0 {
		t.Errorf("lastKnown = %v; want non-empty map (incremental sync)", prov.lastKnown)
	}
}

// TestBackfillPoolID_NonPerPoolEmptyDoesNotForceFullFetch is the gate-review negative
// test: a non-PerPool pull ("standard") with empty PoolID must NOT trigger a full
// re-fetch, because standard/joint/beginner pools may never return a poolId.
func TestBackfillPoolID_NonPerPoolEmptyDoesNotForceFullFetch(t *testing.T) {
	const game = "hypergryph/endfield"
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)

	seedAccount(t, a, "ga_neg", "ROLE42", 0)

	// Standard pull with empty PoolID (non-PerPool — must never force full fetch).
	// Special pull with PoolID populated (PerPool — satisfied, no backfill needed).
	pulls := []core.GachaPull{
		{ID: "std-1", BannerKey: "standard", Rank: 4, Name: "C", Time: "2026-01-03 00:00:00", PoolID: ""},
		{ID: "sp-3", BannerKey: "special", Rank: 5, Name: "D", Time: "2026-01-04 00:00:00", PoolID: "pool-002"},
	}
	if _, err := a.gachaStore.UpsertPulls(game, "ROLE42", pulls); err != nil {
		t.Fatalf("UpsertPulls: %v", err)
	}

	if _, err := a.RefreshGacha(game, "ga_neg"); err != nil {
		t.Fatalf("RefreshGacha: %v", err)
	}

	// known must be non-nil → incremental (the empty standard pull must NOT force full fetch).
	if prov.lastKnown == nil {
		t.Errorf("lastKnown is nil; want non-nil map — empty non-PerPool pull must not force full re-fetch")
	}
}

// Weapon is PerPool: a stored weapon pull that already has a PoolID must NOT force a
// full re-fetch (no perpetual-wedge); refresh stays incremental.
func TestBackfillPoolID_WeaponPopulatedStaysIncremental(t *testing.T) {
	a := newTestAppWithEndfield(t)
	const game = "hypergryph/endfield"
	seedAccount(t, a, "acc-w", "ROLE42", 0)
	if _, err := a.gachaStore.UpsertPulls(game, "ROLE42", []core.GachaPull{
		{ID: "wp-1", BannerKey: "weapon", Rank: 5, Name: "W", Time: "2026-01-01 00:00:00", PoolID: "weponbox_1_1_2"},
	}); err != nil {
		t.Fatalf("UpsertPulls: %v", err)
	}
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.lastKnown = map[string]bool{} // sentinel: must become non-nil (incremental)
	if _, err := a.RefreshGacha(game, "acc-w"); err != nil {
		t.Fatalf("RefreshGacha: %v", err)
	}
	if prov.lastKnown == nil {
		t.Errorf("lastKnown is nil; weapon pull WITH poolId must stay incremental (no wedge)")
	}
}

func TestAddGachaAccountByLogin_LabelFromNickName(t *testing.T) {
	a := newTestAppWithEndfield(t) // login → {tok,hg,e@x}
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.userInfo = core.GachaUserInfo{HgID: "HG", NickName: "暱稱", RealEmail: "r@e.com"}
	acc, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if acc.Label != "暱稱" {
		t.Errorf("Label=%q want 暱稱 (nickName)", acc.Label)
	}
	if acc.Email != "r@e.com" {
		t.Errorf("Email=%q want r@e.com (realEmail)", acc.Email)
	}
}

func TestAddGachaAccountByLogin_UserInfoFailFallsBackToEmail(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.userInfoErr = core.ErrGachaCredentialExpired
	acc, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if acc.Label != acc.Email || acc.Email == "" {
		t.Errorf("on user/info fail Label must fall back to login email; got Label=%q Email=%q", acc.Label, acc.Email)
	}
}

func TestCustomLabelPreservedAcrossReLogin(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.userInfo = core.GachaUserInfo{HgID: "HG", NickName: "暱稱", RealEmail: "r@e.com"}
	acc, _ := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err := a.SetGachaAccountLabel("hypergryph/endfield", acc.ID, "我的別名"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	prov.userInfo = core.GachaUserInfo{HgID: "HG", NickName: "新暱", RealEmail: "r@e.com"}
	acc2, _ := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if acc2.ID != acc.ID {
		t.Fatalf("dedup failed: new id %s vs %s", acc2.ID, acc.ID)
	}
	got, _ := a.gachaStore.GetGachaAccount(acc.ID)
	if got.CustomLabel != "我的別名" {
		t.Errorf("CustomLabel=%q want 我的別名 (preserved)", got.CustomLabel)
	}
	if got.Label != "新暱" {
		t.Errorf("Label=%q want 新暱 (refreshed)", got.Label)
	}
}

// A v7 repair key forces one full refetch (known=nil, 300s window); success
// clears the key and the next refresh is incremental again.
func TestRepairKey_ForcesFullRefetchThenClears(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.fetchRes.UID = "U1" // keep the seeded uid so key bookkeeping is 1:1
	seedAccount(t, a, "ga_x", "U1", 1)
	if err := a.store.SetMeta("v7_refetch_pending:hypergryph/endfield:U1", "1"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RefreshGacha("hypergryph/endfield", ""); err != nil {
		t.Fatal(err)
	}
	if prov.lastKnown != nil {
		t.Errorf("repair refresh: known = %v, want nil (full refetch)", prov.lastKnown)
	}
	if until := time.Until(prov.lastDeadline); until < 200*time.Second {
		t.Errorf("repair window deadline %v away, want ~300s", until.Round(time.Second))
	}
	if _, ok, _ := a.store.GetMeta("v7_refetch_pending:hypergryph/endfield:U1"); ok {
		t.Error("repair key not cleared after success")
	}

	// second refresh: incremental again, normal 120s window
	if _, err := a.RefreshGacha("hypergryph/endfield", ""); err != nil {
		t.Fatal(err)
	}
	if len(prov.lastKnown) == 0 {
		t.Error("second refresh should be incremental (non-empty known)")
	}
	if until := time.Until(prov.lastDeadline); until > 150*time.Second {
		t.Errorf("normal window deadline %v away, want ~120s", until.Round(time.Second))
	}
}

// When the write-back changes the uid (roleId re-bind), BOTH the seed uid's
// key and the new uid's key must be retired — a stale key under either uid
// would wedge every later refresh into a permanent 300s full refetch.
func TestRepairKey_ClearedUnderBothUIDsOnRoleChange(t *testing.T) {
	a := newTestAppWithEndfield(t)
	// fake default fetchRes.UID is "ROLE42" — deliberately different from the
	// seeded uid U1 to exercise the seedUID vs res.UID split.
	seedAccount(t, a, "ga_x", "U1", 1)
	for _, uid := range []string{"U1", "ROLE42"} {
		if err := a.store.SetMeta("v7_refetch_pending:hypergryph/endfield:"+uid, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.RefreshGacha("hypergryph/endfield", ""); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"U1", "ROLE42"} {
		if _, ok, _ := a.store.GetMeta("v7_refetch_pending:hypergryph/endfield:" + uid); ok {
			t.Errorf("repair key for %s not cleared after role-change write-back", uid)
		}
	}
}

// A failed fetch keeps the repair key so the next refresh retries the repair.
func TestRepairKey_KeptOnFailure(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.fetchErr = errors.New("boom")
	seedAccount(t, a, "ga_x", "U1", 1)
	if err := a.store.SetMeta("v7_refetch_pending:hypergryph/endfield:U1", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshGacha("hypergryph/endfield", ""); err == nil {
		t.Fatal("want error from failing fetch")
	}
	if _, ok, _ := a.store.GetMeta("v7_refetch_pending:hypergryph/endfield:U1"); !ok {
		t.Error("repair key must survive a failed refresh")
	}
}

// The known set handed to the provider must use "<bannerKey>|<id>" composites,
// matching what efFetchPools consumes (bare ids are ambiguous across the
// independent char/weapon seqId counters).
func TestKnownKeysAreBannerScoped(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.fetchRes.UID = "U1"
	seedAccount(t, a, "ga_x", "U1", 1) // seeds pull ID "seed-ga_x-0" under banner "standard"

	if _, err := a.RefreshGacha("hypergryph/endfield", ""); err != nil {
		t.Fatal(err)
	}
	if !prov.lastKnown["standard|seed-ga_x-0"] {
		t.Errorf("known missing composite key; got %v", prov.lastKnown)
	}
	if prov.lastKnown["seed-ga_x-0"] {
		t.Error("known must not contain bare ids")
	}
}
