package store

import (
	"database/sql"
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
	if v != "6" {
		t.Fatalf("schema_version = %q, want 6", v)
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
	if v != "6" {
		t.Fatalf("schema_version = %q, want 6 after re-open", v)
	}
}

// TestUpsertPulls_PoolRoundTrip verifies that pool_id/pool_name are persisted
// and returned unchanged by AllPulls.
func TestUpsertPulls_PoolRoundTrip(t *testing.T) {
	s := openTemp(t)
	pulls := []core.GachaPull{
		{ID: "200", BannerKey: "special", Rank: 6, Name: "X", Time: "t1",
			PoolID: "special_1_3_1", PoolName: "特許尋訪-1-3-1"},
	}
	if _, err := s.UpsertPulls("hypergryph/endfield", "u1", pulls); err != nil {
		t.Fatal(err)
	}
	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 1 {
		t.Fatalf("AllPulls len=%d err=%v want 1", len(all), err)
	}
	if all[0].PoolID != "special_1_3_1" || all[0].PoolName != "特許尋訪-1-3-1" {
		t.Fatalf("pool fields not round-tripped: %+v", all[0])
	}
}

// TestUpsertPulls_PoolBackfill verifies the conditional UPDATE that backfills
// pool_id/pool_name onto an already-stored row (INSERT OR IGNORE no-op path).
// Also asserts added==0 on the second call (no new INSERT happened).
func TestUpsertPulls_PoolBackfill(t *testing.T) {
	s := openTemp(t)

	// First insert — no pool info.
	added, err := s.UpsertPulls("hypergryph/endfield", "u1", []core.GachaPull{
		{ID: "300", BannerKey: "special", Rank: 6, Name: "Y", Time: "t1"},
	})
	if err != nil || added != 1 {
		t.Fatalf("first upsert added=%d err=%v want 1", added, err)
	}

	// Re-upsert same (game,uid,id) WITH pool info — INSERT OR IGNORE is a no-op;
	// the conditional UPDATE should backfill.
	added2, err := s.UpsertPulls("hypergryph/endfield", "u1", []core.GachaPull{
		{ID: "300", BannerKey: "special", Rank: 6, Name: "Y", Time: "t1",
			PoolID: "special_1_3_1", PoolName: "特許尋訪-1-3-1"},
	})
	if err != nil || added2 != 0 {
		t.Fatalf("second upsert added=%d err=%v want 0", added2, err)
	}

	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 1 {
		t.Fatalf("AllPulls len=%d err=%v want 1", len(all), err)
	}
	if all[0].PoolID != "special_1_3_1" || all[0].PoolName != "特許尋訪-1-3-1" {
		t.Fatalf("pool backfill failed: %+v", all[0])
	}
}

// TestMigrateV5_AddsPoolColumns builds a v4-style DB on disk (pulls table
// lacking pool_id/pool_name), then verifies:
//  1. OpenSQLite migrates cleanly to v5 (ALTER TABLE succeeds, AllPulls works).
//  2. A second open does NOT error — column-exists guard prevents "duplicate
//     column name" on re-run.
func TestMigrateV5_AddsPoolColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gacha_v4sim.db")

	// Build a minimal v4 DB by hand (no pool columns).
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO meta(key,value) VALUES('schema_version','4')`,
		`CREATE TABLE pulls (
			game TEXT NOT NULL, uid TEXT NOT NULL, id TEXT NOT NULL,
			banner_key TEXT, item_type TEXT, rank INTEGER, name TEXT, time TEXT, is_free INTEGER,
			PRIMARY KEY (game, uid, id))`,
		`CREATE TABLE url_cache (game TEXT NOT NULL, uid TEXT NOT NULL, url TEXT, fetched_at INTEGER, PRIMARY KEY (game, uid))`,
		`CREATE TABLE gacha_cred (game TEXT PRIMARY KEY, cred TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE game_settings (game_id TEXT PRIMARY KEY, path TEXT, background_path TEXT)`,
		`CREATE TABLE playstate (game TEXT PRIMARY KEY, last_played_unix INTEGER)`,
		`CREATE TABLE account_uid (cuid TEXT PRIMARY KEY, uid TEXT, label TEXT)`,
		`CREATE TABLE gacha_accounts (
			account_id TEXT PRIMARY KEY, game TEXT NOT NULL, hg_id TEXT, uid TEXT,
			label TEXT, email TEXT, token TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL)`,
		// Insert a pull without pool columns — simulates a row stored before v5.
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,name,rank,time,is_free)
			VALUES('hypergryph/endfield','u1','400','special','角色','Z',6,'t1',0)`,
	} {
		if _, err := rawDB.Exec(stmt); err != nil {
			t.Fatalf("setup rawDB: %v", err)
		}
	}
	rawDB.Close()

	// First open → should migrate from v4 to v5.
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("migrate v4→v5: %v", err)
	}
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "6" {
		t.Fatalf("schema_version=%q want 6 after migration", v)
	}
	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 1 {
		t.Fatalf("AllPulls after migrate: %v, len=%d", err, len(all))
	}
	// Old row has no pool info — COALESCE must yield empty strings (not NULL scan error).
	if all[0].PoolID != "" || all[0].PoolName != "" {
		t.Fatalf("old row should have empty pool fields: %+v", all[0])
	}
	s.Close()

	// Second open → must NOT error (column-exists guard prevents duplicate ALTER).
	s2, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("second open (idempotency): %v", err)
	}
	defer s2.Close()
	all2, err := s2.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all2) != 1 {
		t.Fatalf("second open AllPulls: %v, len=%d", err, len(all2))
	}
}
