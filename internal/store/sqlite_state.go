package store

import (
	"database/sql"
	"errors"
)

var _ StateStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) getKV(table, keyCol, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM `+table+` WHERE `+keyCol+`=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) { // wrapped-error-safe variant of sqlite.go's no-rows checks
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *SQLiteStore) GetMeta(k string) (string, bool, error) { return s.getKV("meta", "key", k) }

func (s *SQLiteStore) SetMeta(k, v string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES(?,?)`, k, v)
	return err
}

func (s *SQLiteStore) GetConfig(k string) (string, bool, error) { return s.getKV("config", "key", k) }

func (s *SQLiteStore) SetConfig(k, v string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO config(key,value) VALUES(?,?)`, k, v)
	return err
}

func (s *SQLiteStore) AllConfig() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key,value FROM config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *SQLiteStore) AllGameSettings() (map[string]GameOverride, error) {
	rows, err := s.db.Query(`SELECT game_id,path,background_path FROM game_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]GameOverride{}
	for rows.Next() {
		var id, p, bg string
		if err := rows.Scan(&id, &p, &bg); err != nil {
			return nil, err
		}
		out[id] = GameOverride{Path: p, BackgroundPath: bg}
	}
	return out, rows.Err()
}

// ReplaceGameSettings makes game_settings exactly match m (rows absent from m are
// deleted), in one transaction.
func (s *SQLiteStore) ReplaceGameSettings(m map[string]GameOverride) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM game_settings`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO game_settings(game_id,path,background_path) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, o := range m {
		if _, err := stmt.Exec(id, o.Path, o.BackgroundPath); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) AllPlaystate() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT game,last_played_unix FROM playstate`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var g string
		var u int64
		if err := rows.Scan(&g, &u); err != nil {
			return nil, err
		}
		out[g] = u
	}
	return out, rows.Err()
}

func (s *SQLiteStore) SetPlaystate(game string, unix int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO playstate(game,last_played_unix) VALUES(?,?)`, game, unix)
	return err
}

func (s *SQLiteStore) AllAccountUID() (map[string]AccountUID, error) {
	rows, err := s.db.Query(`SELECT cuid,uid,label FROM account_uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AccountUID{}
	for rows.Next() {
		var c, u, l string
		if err := rows.Scan(&c, &u, &l); err != nil {
			return nil, err
		}
		out[c] = AccountUID{UID: u, Label: l}
	}
	return out, rows.Err()
}

func (s *SQLiteStore) SetAccountUID(cuid, uid, label string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO account_uid(cuid,uid,label) VALUES(?,?,?)`, cuid, uid, label)
	return err
}
