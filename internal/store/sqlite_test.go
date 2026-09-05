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
	if v != "7" {
		t.Fatalf("schema_version = %q, want 7", v)
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
	if v != "7" {
		t.Fatalf("schema_version = %q, want 7 after re-open", v)
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
	if v != "7" {
		t.Fatalf("schema_version=%q want 7 after migration", v)
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

// buildV6DB hand-crafts a v6-shaped DB (old (game,uid,id) PK, schema_version=6)
// plus the given extra statements, mirroring the v4 fixture pattern above.
func buildV6DB(t *testing.T, dbPath string, extra ...string) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE pulls (
			game TEXT NOT NULL, uid TEXT NOT NULL, id TEXT NOT NULL,
			banner_key TEXT, item_type TEXT, rank INTEGER, name TEXT, time TEXT, is_free INTEGER,
			pool_id TEXT, pool_name TEXT,
			PRIMARY KEY (game, uid, id)
		)`,
		`INSERT INTO meta(key,value) VALUES('schema_version','6')`,
	}
	for _, q := range append(stmts, extra...) {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
}

// v6→v7: PK gains banner_key, per-uid endfield repair keys are seeded inside
// the same txn, and a .bak-v7 backup is produced.
func TestMigrateV7PullsPKBanner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v6.db")
	buildV6DB(t, dbPath,
		// collision scenario: char already owns id=400
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		 VALUES('hypergryph/endfield','U1','400','special','char',5,'卡契爾','2026-04-17 15:13:34',0)`,
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		 VALUES('hypergryph/endfield','U2','10','weapon','weapon',4,'w','2026-01-01 00:00:00',0)`,
		// empty-uid row: must not seed a "…endfield:" orphan key
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		 VALUES('hypergryph/endfield','','11','weapon','weapon',4,'w2','2026-01-01 00:00:00',0)`,
		// non-endfield row: must NOT seed a repair key
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		 VALUES('hoyoverse/genshin','G1','1700000000000000001','character','char',5,'x','2026-01-01 00:00:00',0)`,
	)

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if v, _, _ := s.GetMeta("schema_version"); v != "7" {
		t.Fatalf("schema_version = %s", v)
	}
	// new PK: weapon id=400 coexists with char id=400
	if n, err := s.UpsertPulls("hypergryph/endfield", "U1", []core.GachaPull{
		{ID: "400", BannerKey: "weapon", ItemType: "weapon", Rank: 6, Name: "曜夜的首演", Time: "2026-08-15 00:00:00"},
	}); err != nil || n != 1 {
		t.Fatalf("cross-banner insert n=%d err=%v", n, err)
	}
	all, _ := s.AllPulls("hypergryph/endfield", "U1")
	if len(all) != 2 {
		t.Fatalf("want 2 rows for U1, got %d", len(all))
	}
	// repair keys: one per endfield uid, none for genshin or empty uid
	for _, uid := range []string{"U1", "U2"} {
		if _, ok, _ := s.GetMeta("v7_refetch_pending:hypergryph/endfield:" + uid); !ok {
			t.Errorf("repair key missing for %s", uid)
		}
	}
	if _, ok, _ := s.GetMeta("v7_refetch_pending:hypergryph/endfield:G1"); ok {
		t.Error("genshin uid must not get a repair key")
	}
	if _, ok, _ := s.GetMeta("v7_refetch_pending:hypergryph/endfield:"); ok {
		t.Error("empty uid must not get a repair key")
	}
	if _, err := os.Stat(dbPath + ".bak-v7"); err != nil {
		t.Errorf("bak-v7 missing: %v", err)
	}
}

// Crash residue: a leftover pulls_new table from an interrupted run must not
// block the migration.
func TestMigrateV7CrashResidue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v6r.db")
	buildV6DB(t, dbPath, `CREATE TABLE pulls_new (x TEXT)`)
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open with residue: %v", err)
	}
	s.Close()
}

// No endfield rows → no repair keys seeded (a stranger's v6 DB must not be
// forced into a full refetch).
func TestMigrateV7NoEndfieldRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v6e.db")
	buildV6DB(t, dbPath)
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key LIKE 'v7_refetch_pending:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("repair keys on clean DB: n=%d err=%v", n, err)
	}
}

// Convergence: once a repair key is deleted, reopening the DB must not
// resurrect it (guards the R3 failure mode where a mis-ordered version gate
// re-runs migrateV7 on every open).
func TestMigrateV7Convergence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v6c.db")
	buildV6DB(t, dbPath,
		`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		 VALUES('hypergryph/endfield','U1','1','special','char',5,'a','2026-01-01 00:00:00',0)`,
	)
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() // double Close (explicit below) is a no-op; guards Fatal paths
	if _, ok, _ := s.GetMeta("v7_refetch_pending:hypergryph/endfield:U1"); !ok {
		t.Fatal("repair key not seeded")
	}
	if err := s.DeleteMeta("v7_refetch_pending:hypergryph/endfield:U1"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, ok, _ := s2.GetMeta("v7_refetch_pending:hypergryph/endfield:U1"); ok {
		t.Fatal("repair key resurrected on reopen — migration re-ran")
	}
	if v, _, _ := s2.GetMeta("schema_version"); v != "7" {
		t.Fatalf("schema_version after reopen = %s", v)
	}
}

// Pool-column backfill must be scoped to the banner: with banner_key in the
// PK, a char and a weapon row can share an id, and the v5 backfill UPDATE
// must not write one banner's pool columns onto the other's row.
func TestUpsertPulls_BackfillScopedToBanner(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "bf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	game, uid := "hypergryph/endfield", "U1"
	// two rows sharing an id across banners, both with empty pool columns
	// (simulating pre-v5 data)
	for _, bk := range []string{"special", "weapon"} {
		if _, err := s.db.Exec(`INSERT INTO pulls(game,uid,id,banner_key,item_type,rank,name,time,is_free)
			VALUES(?,?,?,?,?,?,?,?,0)`, game, uid, "400", bk, "x", 5, "n", "2026-01-01 00:00:00"); err != nil {
			t.Fatal(err)
		}
	}
	// re-upsert carrying pool info for the weapon row only
	if _, err := s.UpsertPulls(game, uid, []core.GachaPull{
		{ID: "400", BannerKey: "weapon", ItemType: "weapon", Rank: 6, Name: "n",
			Time: "2026-01-01 00:00:00", PoolID: "weponbox_1_4_2", PoolName: "明曜申領"},
	}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.AllPulls(game, uid)
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2", len(all))
	}
	for _, p := range all {
		switch p.BannerKey {
		case "weapon":
			if p.PoolID != "weponbox_1_4_2" {
				t.Errorf("weapon pool_id = %q", p.PoolID)
			}
		case "special":
			if p.PoolID != "" {
				t.Errorf("special pool_id polluted: %q", p.PoolID)
			}
		}
	}
}

// Long-term invariant (spec): banner_key is part of the PK, so the same
// (game,uid,id) under a different banner_key is a distinct row — which means
// every provider's record→bannerKey mapping must stay stable forever, or the
// same server records re-insert as duplicates.
func TestUpsertPulls_BannerKeyIsLoadBearing(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "lb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	game, uid := "hypergryph/endfield", "U1"
	p1 := []core.GachaPull{{ID: "7", BannerKey: "special", ItemType: "char", Rank: 5, Name: "a", Time: "2026-01-01 00:00:00"}}
	if n, err := s.UpsertPulls(game, uid, p1); err != nil || n != 1 {
		t.Fatalf("first insert n=%d err=%v", n, err)
	}
	// same 4-tuple → deduped
	if n, err := s.UpsertPulls(game, uid, p1); err != nil || n != 0 {
		t.Fatalf("dup insert n=%d err=%v", n, err)
	}
	// same id under another banner → a new row (deliberate semantics)
	p2 := []core.GachaPull{{ID: "7", BannerKey: "weapon", ItemType: "weapon", Rank: 4, Name: "b", Time: "2026-01-01 00:00:00"}}
	if n, err := s.UpsertPulls(game, uid, p2); err != nil || n != 1 {
		t.Fatalf("cross-banner insert n=%d err=%v", n, err)
	}
}

// Fresh DB: base CREATE TABLE already has the v7 shape, so no rebuild, no
// repair keys, no backup.
func TestFreshDBNoV7Artifacts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _, _ := s.GetMeta("schema_version"); v != "7" {
		t.Fatalf("fresh schema_version = %s", v)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key LIKE 'v7_refetch_pending:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("fresh DB repair keys: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(dbPath + ".bak-v7"); !os.IsNotExist(err) {
		t.Fatalf("fresh DB must not create bak-v7: %v", err)
	}
}
