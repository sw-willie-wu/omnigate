package store

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"omnigate/internal/core"

	_ "modernc.org/sqlite"
)

const schemaVersion = 4

type SQLiteStore struct {
	db   *sql.DB
	path string
}

var _ GachaStore = (*SQLiteStore)(nil)

// OpenSQLite opens (creating if needed) the gacha DB at path and migrates schema.
func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Single connection avoids SQLITE_BUSY on the file; writes are short txns.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE IF NOT EXISTS pulls (
  game TEXT NOT NULL, uid TEXT NOT NULL, id TEXT NOT NULL,
  banner_key TEXT, item_type TEXT, rank INTEGER, name TEXT, time TEXT, is_free INTEGER,
  PRIMARY KEY (game, uid, id)
)`,
		`CREATE INDEX IF NOT EXISTS idx_pulls_game_uid ON pulls(game, uid)`,
		`CREATE TABLE IF NOT EXISTS url_cache (
  game TEXT NOT NULL, uid TEXT NOT NULL, url TEXT, fetched_at INTEGER,
  PRIMARY KEY (game, uid)
)`,
		`CREATE TABLE IF NOT EXISTS gacha_cred (
  game TEXT PRIMARY KEY, cred TEXT NOT NULL, updated_at INTEGER NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS config (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE IF NOT EXISTS game_settings (
  game_id TEXT PRIMARY KEY, path TEXT, background_path TEXT
)`,
		`CREATE TABLE IF NOT EXISTS playstate (
  game TEXT PRIMARY KEY, last_played_unix INTEGER
)`,
		`CREATE TABLE IF NOT EXISTS account_uid (
  cuid TEXT PRIMARY KEY, uid TEXT, label TEXT
)`,
		`CREATE TABLE IF NOT EXISTS gacha_accounts (
  account_id TEXT PRIMARY KEY, game TEXT NOT NULL, hg_id TEXT, uid TEXT,
  label TEXT, email TEXT, token TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_gacha_accounts_game ON gacha_accounts(game)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// For a brand-new DB this records the current schema version; an existing DB
	// keeps its stored value (the v2 re-key below upgrades a v1 DB).
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES('schema_version', ?)`, schemaVersion); err != nil {
		return err
	}
	var verStr string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&verStr); err != nil {
		return err
	}
	ver, _ := strconv.Atoi(verStr)
	if ver < 2 {
		if err := s.migrateV2RekeyWuwa(); err != nil {
			return err
		}
	}
	// schema_version writeback: the const bump alone never reaches existing DBs —
	// the only writes are INSERT OR IGNORE (new DBs only) and the '2' inside
	// migrateV2RekeyWuwa. Gate on the original `ver` read above; ordered AFTER the
	// rekey so it overwrites the rekey's '2'.
	if ver < 3 {
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','3')`); err != nil {
			return err
		}
	}
	if ver < 4 {
		if err := s.migrateV4GachaCredToAccount(); err != nil {
			return err
		}
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','4')`); err != nil {
			return err
		}
	}
	return nil
}

// migrateV2RekeyWuwa re-keys legacy WuWa pulls ("<pool>-<idx>", an unstable
// position-from-oldest index) to the stable "w|<pool>|<time>|<ord>" scheme, so the
// sliding-window record API no longer drops new pulls on dedup. Idempotent;
// WuWa-only; backs up the DB first.
func (s *SQLiteStore) migrateV2RekeyWuwa() error {
	var legacy int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pulls WHERE game LIKE 'kurogames/%' AND id NOT LIKE 'w|%'`).Scan(&legacy); err != nil {
		return err
	}
	if legacy == 0 {
		_, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','2')`)
		return err // fresh/empty or already-migrated → just record the version
	}

	// Back up (only if absent; never clobber a pristine backup) via VACUUM INTO.
	if s.path != "" && s.path != ":memory:" {
		bak := s.path + ".bak-v2"
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			if _, err := s.db.Exec(`VACUUM INTO ?`, bak); err != nil {
				return fmt.Errorf("gacha v2 backup: %w", err)
			}
		}
	}

	// Collect legacy rows (Rows MUST be closed before the write txn — MaxOpenConns(1)).
	type row struct{ game, uid, id, time string }
	rows, err := s.db.Query(`SELECT game,uid,id,time FROM pulls WHERE game LIKE 'kurogames/%' AND id NOT LIKE 'w|%' ORDER BY game,uid,id`)
	if err != nil {
		return err
	}
	var legacyRows []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.game, &r.uid, &r.id, &r.time); err != nil {
			rows.Close()
			return err
		}
		legacyRows = append(legacyRows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// New ids: ordinal = position within (game,uid,pool,time), in id-asc order (the
	// ORDER BY id above = oldest-first within (pool,time), matching fetchWuwa).
	type rekey struct{ game, uid, oldID, newID string }
	var ups []rekey
	ordinals := map[string]int{}
	for _, r := range legacyRows {
		dash := strings.IndexByte(r.id, '-')
		if dash <= 0 {
			continue // not a legacy "<pool>-<idx>" id; skip defensively
		}
		pool := r.id[:dash]
		key := r.game + "|" + r.uid + "|" + pool + "|" + r.time
		ord := ordinals[key]
		ordinals[key]++
		ups = append(ups, rekey{r.game, r.uid, r.id, fmt.Sprintf("w|%s|%s|%d", pool, r.time, ord)})
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range ups {
		// UPDATE OR IGNORE: on a PK conflict (new id already present = a true dup),
		// the update is skipped (0 rows) → delete the legacy duplicate instead.
		res, err := tx.Exec(`UPDATE OR IGNORE pulls SET id=? WHERE game=? AND uid=? AND id=?`, u.newID, u.game, u.uid, u.oldID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(`DELETE FROM pulls WHERE game=? AND uid=? AND id=?`, u.game, u.uid, u.oldID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version','2')`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV4GachaCredToAccount converts each legacy per-game gacha_cred row into a
// single active gacha_accounts row, seeding uid from latest_uid (== roleId,
// offline) so existing analysis stays visible, then deletes the legacy row.
// Gated on ver < 4; safe to call on an empty gacha_cred table (no-op loop).
func (s *SQLiteStore) migrateV4GachaCredToAccount() error {
	rows, err := s.db.Query(`SELECT game, cred FROM gacha_cred`)
	if err != nil {
		return err
	}
	type credRow struct{ game, token string }
	var creds []credRow
	for rows.Next() {
		var c credRow
		if err := rows.Scan(&c.game, &c.token); err != nil {
			rows.Close()
			return err
		}
		creds = append(creds, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range creds {
		var uid string
		_ = s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, "latest_uid:"+c.game).Scan(&uid)
		id := "mig-" + c.game // deterministic; one legacy cred per game
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO gacha_accounts(account_id,game,hg_id,uid,label,email,token,active,updated_at)
VALUES(?,?,?,?,?,?,?,1,strftime('%s','now'))`, id, c.game, "", uid, "", "", c.token); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM gacha_cred WHERE game=?`, c.game); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) UpsertPulls(game, uid string, pulls []core.GachaPull) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO pulls
		(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	added := 0
	for _, p := range pulls {
		res, err := stmt.Exec(game, uid, p.ID, p.BannerKey, p.ItemType, p.Rank, p.Name, p.Time, boolInt(p.IsFree))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if added > 0 {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES(?, ?)`, "latest_uid:"+game, uid); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

func (s *SQLiteStore) AllPulls(game, uid string) ([]core.GachaPull, error) {
	rows, err := s.db.Query(`SELECT id,banner_key,item_type,rank,name,time,is_free
		FROM pulls WHERE game=? AND uid=?`, game, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.GachaPull
	for rows.Next() {
		var p core.GachaPull
		var isFree int
		if err := rows.Scan(&p.ID, &p.BannerKey, &p.ItemType, &p.Rank, &p.Name, &p.Time, &isFree); err != nil {
			return nil, err
		}
		p.IsFree = isFree != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) KnownUIDs(game string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT uid FROM pulls WHERE game=?`, game)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.Strings(out)
	return out, rows.Err()
}

func (s *SQLiteStore) LatestUID(game string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, "latest_uid:"+game).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *SQLiteStore) GetURLCache(game, uid string) (string, time.Time, error) {
	var url string
	var ts int64
	err := s.db.QueryRow(`SELECT url,fetched_at FROM url_cache WHERE game=? AND uid=?`, game, uid).Scan(&url, &ts)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	return url, time.Unix(ts, 0), err
}

func (s *SQLiteStore) PutURLCache(game, uid, url string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO url_cache(game,uid,url,fetched_at) VALUES(?,?,?,?)`,
		game, uid, url, time.Now().Unix())
	return err
}

func (s *SQLiteStore) GetGachaCred(game string) (string, time.Time, error) {
	var cred string
	var ts int64
	err := s.db.QueryRow(`SELECT cred,updated_at FROM gacha_cred WHERE game=?`, game).Scan(&cred, &ts)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	return cred, time.Unix(ts, 0), err
}

func (s *SQLiteStore) PutGachaCred(game, cred string) error {
	if cred == "" {
		_, err := s.db.Exec(`DELETE FROM gacha_cred WHERE game=?`, game)
		return err
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO gacha_cred(game,cred,updated_at) VALUES(?,?,?)`,
		game, cred, time.Now().Unix())
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *SQLiteStore) ListGachaAccounts(game string) ([]GachaAccount, error) {
	rows, err := s.db.Query(`SELECT account_id,game,hg_id,uid,label,email,token,active FROM gacha_accounts WHERE game=? ORDER BY updated_at, account_id`, game)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GachaAccount{}
	for rows.Next() {
		var a GachaAccount
		var active int
		if err := rows.Scan(&a.ID, &a.Game, &a.HgID, &a.UID, &a.Label, &a.Email, &a.Token, &active); err != nil {
			return nil, err
		}
		a.Active = active == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetGachaAccount(id string) (GachaAccount, error) {
	var a GachaAccount
	var active int
	err := s.db.QueryRow(`SELECT account_id,game,hg_id,uid,label,email,token,active FROM gacha_accounts WHERE account_id=?`, id).
		Scan(&a.ID, &a.Game, &a.HgID, &a.UID, &a.Label, &a.Email, &a.Token, &active)
	a.Active = active == 1
	return a, err
}

func (s *SQLiteStore) UpsertGachaAccount(a GachaAccount) error {
	_, err := s.db.Exec(`INSERT INTO gacha_accounts(account_id,game,hg_id,uid,label,email,token,active,updated_at)
VALUES(?,?,?,?,?,?,?,?,strftime('%s','now'))
ON CONFLICT(account_id) DO UPDATE SET game=excluded.game,hg_id=excluded.hg_id,uid=excluded.uid,
  label=excluded.label,email=excluded.email,token=excluded.token,updated_at=excluded.updated_at`,
		a.ID, a.Game, a.HgID, a.UID, a.Label, a.Email, a.Token, boolInt(a.Active))
	return err
}

func (s *SQLiteStore) SetGachaAccountLabel(id, label string) error {
	_, err := s.db.Exec(`UPDATE gacha_accounts SET label=?, updated_at=strftime('%s','now') WHERE account_id=?`, label, id)
	return err
}

func (s *SQLiteStore) DeleteGachaAccount(id string) error {
	_, err := s.db.Exec(`DELETE FROM gacha_accounts WHERE account_id=?`, id)
	return err
}

// SetActiveGachaAccount flips active to exactly the given id within its game.
func (s *SQLiteStore) SetActiveGachaAccount(game, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`UPDATE gacha_accounts SET active=0 WHERE game=?`, game); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE gacha_accounts SET active=1 WHERE account_id=? AND game=?`, id, game); err != nil {
		return err
	}
	return tx.Commit()
}
