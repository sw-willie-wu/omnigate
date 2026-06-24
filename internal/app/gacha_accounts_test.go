package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// fakeLoginCredProvider satisfies core.Provider (via embedded fakeProvider),
// core.GachaLoginProvider, and core.GachaCredentialProvider. Used by
// newTestAppWithEndfield to simulate the Endfield per-account gacha flow.
type fakeLoginCredProvider struct {
	fakeProvider
	loginRes core.GachaLoginResult
	loginErr error
	fetchRes core.GachaFetchResult
	fetchErr error
}

func (f *fakeLoginCredProvider) LoginByEmailPassword(_ context.Context, _, _ string) (core.GachaLoginResult, error) {
	return f.loginRes, f.loginErr
}

func (f *fakeLoginCredProvider) FetchGachaWithCredential(_ context.Context, _ core.GameID, _, _ string, _ map[string]bool) (core.GachaFetchResult, error) {
	return f.fetchRes, f.fetchErr
}

// FetchGacha + GachaConfig satisfy core.GachaProvider so the credential branch of
// GetGachaSummary/RefreshGacha (which does p.(core.GachaProvider) then
// gp.GachaConfig(gid)) compiles and runs. The credential path uses
// FetchGachaWithCredential, not FetchGacha, so this is a no-op.
func (f *fakeLoginCredProvider) FetchGacha(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	return core.GachaFetchResult{}, nil
}

func (f *fakeLoginCredProvider) GachaConfig(_ core.GameID) core.GachaConfig {
	return core.GachaConfig{HeadlineRank: 6, Banners: []core.BannerConfig{}, Currency: "NT$", ExpectedPity: 60}
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
