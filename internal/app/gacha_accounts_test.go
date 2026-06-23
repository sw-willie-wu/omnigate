package app

import (
	"context"
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

func (f *fakeLoginCredProvider) FetchGachaWithCredential(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	return f.fetchRes, f.fetchErr
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

func TestAddGachaAccountByLogin_WritesBackRoleIdUID(t *testing.T) {
	a := newTestAppWithEndfield(t) // fake: Login→{tok,hg,e@x}, Fetch→res.UID="ROLE42", 1 pull
	acc, err := a.AddGachaAccountByLogin("hypergryph/endfield", "e@x", "pw")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if acc.UID != "ROLE42" {
		t.Fatalf("uid = %q, want ROLE42 (roleId via write-back, NOT binding hash)", acc.UID)
	}
	if acc.Email != "e@x" || acc.Label != "e@x" {
		t.Fatalf("acc = %+v (label defaults to email)", acc)
	}
	// records landed under the roleId partition (assert via the STORE directly —
	// GetGachaSummary per-account resolution is wired in Task 6, NOT here)
	all, err := a.gachaStore.AllPulls("hypergryph/endfield", acc.UID)
	if err != nil || len(all) == 0 {
		t.Fatalf("no pulls under roleId partition: %d err=%v", len(all), err)
	}
}

func TestGameAccountKind(t *testing.T) {
	a := newTestAppWithEndfield(t)
	if k := a.GameAccountKind("hypergryph/endfield"); k != "credential" {
		t.Errorf("endfield kind = %q, want credential", k)
	}
}
