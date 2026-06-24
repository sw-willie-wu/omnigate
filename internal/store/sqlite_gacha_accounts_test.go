package store

import (
	"path/filepath"
	"testing"

	"omnigate/internal/core"
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

func mustOpen(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("mustOpen(%q): %v", path, err)
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

func TestGachaAccountCustomLabel(t *testing.T) {
	s := newTestStore(t)
	g := "hypergryph/endfield"
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a1", Game: g, Label: "暱", Email: "x@y.com", Token: "tok"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetGachaAccountLabel("a1", "我的別名"); err != nil {
		t.Fatalf("setlabel: %v", err)
	}
	got, err := s.GetGachaAccount("a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CustomLabel != "我的別名" {
		t.Errorf("CustomLabel=%q want 我的別名", got.CustomLabel)
	}
	if got.Label != "暱" {
		t.Errorf("Label=%q want 暱 (label must NOT change on rename)", got.Label)
	}
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a1", Game: g, Label: "新暱", Email: "x@y.com", Token: "tok", CustomLabel: "我的別名"}); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got, _ = s.GetGachaAccount("a1")
	if got.Label != "新暱" || got.CustomLabel != "我的別名" {
		t.Errorf("after upsert: Label=%q CustomLabel=%q want 新暱/我的別名", got.Label, got.CustomLabel)
	}
	list, _ := s.ListGachaAccounts(g)
	if len(list) != 1 || list[0].CustomLabel != "我的別名" {
		t.Errorf("list custom_label not carried: %+v", list)
	}
}

func TestMigrateV6AddsCustomLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.UpsertGachaAccount(GachaAccount{ID: "a1", Game: "g", Label: "L", Token: "t"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE gacha_accounts DROP COLUMN custom_label`); err != nil {
		t.Fatalf("drop col: %v", err)
	}
	if err := s.SetMeta("schema_version", "5"); err != nil {
		t.Fatalf("setmeta: %v", err)
	}
	s.Close()
	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.GetGachaAccount("a1")
	if err != nil || got.Label != "L" {
		t.Fatalf("data lost after v6: %+v err=%v", got, err)
	}
	if err := s2.SetGachaAccountLabel("a1", "alias"); err != nil {
		t.Fatalf("setlabel after migrate: %v", err)
	}
}

func TestMigrateV4_GachaCredToAccount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	s := mustOpen(t, path)
	g := "hypergryph/endfield"
	if err := s.PutGachaCred(g, "legacy-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertPulls(g, "4191138757", []core.GachaPull{{ID: "1", BannerKey: "special", Rank: 6, Name: "n", Time: "2026-01-01 00:00:00"}}); err != nil {
		t.Fatal(err)
	}
	// force schema_version back to 3 so reopen runs the v4 migration
	if err := s.SetMeta("schema_version", "3"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2 := mustOpen(t, path) // reopen → migrate() runs v4
	accts, err := s2.ListGachaAccounts(g)
	if err != nil || len(accts) != 1 {
		t.Fatalf("accts=%+v err=%v", accts, err)
	}
	if accts[0].Token != "legacy-token" || accts[0].UID != "4191138757" || !accts[0].Active {
		t.Fatalf("migrated row wrong: %+v", accts[0])
	}
	if c, _, _ := s2.GetGachaCred(g); c != "" {
		t.Errorf("gacha_cred not tombstoned: %q", c)
	}
	// resurrect-safe: delete the account, reopen — must NOT recreate it (migration
	// is gated on schema_version < 4, not on data-presence).
	if err := s2.DeleteGachaAccount(accts[0].ID); err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
	s3 := mustOpen(t, path)
	again, _ := s3.ListGachaAccounts(g)
	if len(again) != 0 {
		t.Errorf("account resurrected: %+v", again)
	}
}
