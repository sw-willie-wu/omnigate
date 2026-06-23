package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestGachaAccounts_CRUDAndActive(t *testing.T) {
	s := newTestStore(t)
	g := "hypergryph/endfield"
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a1", Game: g, Label: "x@y.com", Email: "x@y.com", Token: "tok1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a2", Game: g, Label: "p@q.com", Email: "p@q.com", Token: "tok2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveGachaAccount(g, "a2"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListGachaAccounts(g)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v err=%v", got, err)
	}
	active := 0
	for _, a := range got {
		if a.Active {
			active++
			if a.ID != "a2" {
				t.Errorf("active = %s, want a2", a.ID)
			}
		}
	}
	if active != 1 {
		t.Errorf("active count = %d, want 1", active)
	}
	if err := s.SetGachaAccountLabel("a1", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a1", Game: g, Label: "main", Email: "x@y.com", Token: "tok1", UID: "4191138757"}); err != nil {
		t.Fatal(err)
	}
	a1, err := s.GetGachaAccount("a1")
	if err != nil || a1.UID != "4191138757" || a1.Label != "main" {
		t.Fatalf("a1 = %+v err=%v", a1, err)
	}
	if err := s.DeleteGachaAccount("a2"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListGachaAccounts(g)
	if len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("after delete = %+v", got)
	}
}
