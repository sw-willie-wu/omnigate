package store

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestMigrateV2_RekeysWuwaPreservesHistory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gacha.db")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := [][]any{
		{"kurogames/wutheringwaves", "u1", "1-00000000", "character", "角色", 4, "old0", "2026-01-01 10:00:00", 0},
		{"kurogames/wutheringwaves", "u1", "1-00000001", "character", "角色", 5, "old1", "2026-01-01 10:00:00", 0},
		{"kurogames/wutheringwaves", "u1", "2-00000000", "weapon", "武器", 5, "w0", "2026-02-01 12:00:00", 0},
		{"hoyoverse/genshin", "h1", "1780000000000000001", "char", "角色", 5, "hoyo", "2026-03-01 00:00:00", 0},
	}
	for _, r := range legacy {
		if _, err := s.db.Exec(`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free) VALUES(?,?,?,?,?,?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','1')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("reopen/migrate: %v", err)
	}
	defer s2.Close()

	if _, err := os.Stat(dbPath + ".bak-v2"); err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	wuwa, _ := s2.AllPulls("kurogames/wutheringwaves", "u1")
	if len(wuwa) != 3 {
		t.Fatalf("wuwa count=%d want 3", len(wuwa))
	}
	for _, p := range wuwa {
		if p.ID[:2] != "w|" {
			t.Fatalf("not re-keyed: %s", p.ID)
		}
	}
	ids := map[string]string{}
	for _, p := range wuwa {
		ids[p.Name] = p.ID
	}
	if ids["old0"] != "w|1|2026-01-01 10:00:00|0" || ids["old1"] != "w|1|2026-01-01 10:00:00|1" {
		t.Fatalf("ordinals wrong: %v", ids)
	}
	hoyo, _ := s2.AllPulls("hoyoverse/genshin", "h1")
	if len(hoyo) != 1 || hoyo[0].ID != "1780000000000000001" {
		t.Fatalf("hoyoverse must be untouched: %+v", hoyo)
	}
	s2.Close()
	s3, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("third open: %v", err)
	}
	defer s3.Close()
	w2, _ := s3.AllPulls("kurogames/wutheringwaves", "u1")
	if len(w2) != 3 {
		t.Fatalf("idempotency broke count: %d", len(w2))
	}
}

func openTemp(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertDedupAndAll(t *testing.T) {
	s := openTemp(t)
	pulls := []core.GachaPull{
		{ID: "100", BannerKey: "special", Rank: 6, Name: "A", Time: "t1"},
		{ID: "101", BannerKey: "special", Rank: 5, Name: "B", Time: "t2"},
	}
	added, err := s.UpsertPulls("hypergryph/endfield", "u1", pulls)
	if err != nil || added != 2 {
		t.Fatalf("first upsert added=%d err=%v want 2", added, err)
	}
	added2, err := s.UpsertPulls("hypergryph/endfield", "u1", append(pulls,
		core.GachaPull{ID: "102", BannerKey: "special", Rank: 5, Name: "C", Time: "t3"}))
	if err != nil || added2 != 1 {
		t.Fatalf("second upsert added=%d err=%v want 1", added2, err)
	}
	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 3 {
		t.Fatalf("AllPulls len=%d err=%v want 3", len(all), err)
	}
}

func TestUIDIsolationAndLatest(t *testing.T) {
	s := openTemp(t)
	s.UpsertPulls("hypergryph/endfield", "u1", []core.GachaPull{{ID: "1", Rank: 6}})
	s.UpsertPulls("hypergryph/endfield", "u2", []core.GachaPull{{ID: "1", Rank: 5}})
	if got, _ := s.AllPulls("hypergryph/endfield", "u2"); len(got) != 1 || got[0].Rank != 5 {
		t.Fatalf("uid isolation broken: %+v", got)
	}
	uids, _ := s.KnownUIDs("hypergryph/endfield")
	if len(uids) != 2 {
		t.Fatalf("KnownUIDs=%v want 2", uids)
	}
	if latest, _ := s.LatestUID("hypergryph/endfield"); latest != "u2" {
		t.Fatalf("LatestUID=%q want u2", latest)
	}
}

func TestURLCacheRoundTrip(t *testing.T) {
	s := openTemp(t)
	if err := s.PutURLCache("hypergryph/endfield", "u1", "https://x/page/gacha_y?token=z"); err != nil {
		t.Fatalf("PutURLCache: %v", err)
	}
	url, _, err := s.GetURLCache("hypergryph/endfield", "u1")
	if err != nil || url == "" {
		t.Fatalf("GetURLCache url=%q err=%v", url, err)
	}
}

func TestGachaCred_RoundTripAndClear(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	game := "hypergryph/endfield"

	if cred, _, err := s.GetGachaCred(game); err != nil || cred != "" {
		t.Fatalf("empty get = %q,%v; want \"\",nil", cred, err)
	}
	if err := s.PutGachaCred(game, "tok-A"); err != nil {
		t.Fatal(err)
	}
	cred, ts, err := s.GetGachaCred(game)
	if err != nil || cred != "tok-A" {
		t.Fatalf("get = %q,%v; want tok-A", cred, err)
	}
	if ts.IsZero() {
		t.Error("updated_at should be set")
	}
	if err := s.PutGachaCred(game, "tok-B"); err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := s.GetGachaCred(game); cred != "tok-B" {
		t.Fatalf("overwrite get = %q; want tok-B", cred)
	}
	if err := s.PutGachaCred(game, ""); err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := s.GetGachaCred(game); cred != "" {
		t.Fatalf("after clear = %q; want empty", cred)
	}
}

func TestMigrate_FreshDB_SchemaVersion3AndTables(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "omnigate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "4" {
		t.Fatalf("schema_version = %q, want 4", v)
	}
	for _, tbl := range []string{"config", "game_settings", "playstate", "account_uid"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
}

func TestMigrate_ExistingV2DB_BumpedTo4(t *testing.T) {
	p := filepath.Join(t.TempDir(), "omnigate.db")
	s, err := OpenSQLite(p) // fresh → 4; force back to 2 to simulate a pre-upgrade DB
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','2')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := OpenSQLite(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var v string
	if err := s2.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "4" {
		t.Fatalf("schema_version = %q, want 4 after re-open", v)
	}
}
