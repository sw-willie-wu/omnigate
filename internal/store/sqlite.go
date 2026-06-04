package store

import (
	"database/sql"
	"sort"
	"time"

	"omnigate/internal/core"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type SQLiteStore struct {
	db *sql.DB
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
	s := &SQLiteStore{db: db}
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES('schema_version', ?)`, schemaVersion)
	return err
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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
