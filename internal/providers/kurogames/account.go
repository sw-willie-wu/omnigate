package kurogames

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	_ "modernc.org/sqlite"
	"omnigate/internal/core"
)

// krsdkCache is the on-disk KRSDKUserCache.json shape. Only the fields we need
// are mapped; token is intentionally NOT mapped so it never enters memory.
type krsdkCache struct {
	AccountList []struct {
		Cuid     json.Number `json:"cuid"`
		Email    string      `json:"email"`
		Username string      `json:"username"`
	} `json:"account_list"`
	LastLoginCuid string `json:"last_login_cuid"` // a quoted string in the file
}

// lastLoginRe matches only the quoted last_login_cuid value, never the cuid
// fields inside account_list. Capture groups preserve the key + quotes.
var lastLoginRe = regexp.MustCompile(`("last_login_cuid"\s*:\s*")\d+(")`)

var digitsRe = regexp.MustCompile(`^\d+$`)

// rewriteLastLoginCuid returns data with last_login_cuid set to cuid, leaving
// every other byte (tokens, account_list cuids) untouched. accountID must be all
// digits; the field must already exist. Output is BOM-free UTF-8.
func rewriteLastLoginCuid(data []byte, cuid string) ([]byte, error) {
	if !digitsRe.MatchString(cuid) {
		return nil, fmt.Errorf("invalid account id %q (must be digits)", cuid)
	}
	if !lastLoginRe.Match(data) {
		return nil, fmt.Errorf("last_login_cuid not found in KRSDK cache")
	}
	return lastLoginRe.ReplaceAll(data, []byte("${1}"+cuid+"${2}")), nil
}

// readRecentlyLoginUID returns the active in-game UID from a WuWa LocalStorage.db
// (read-only). "" with nil error when the key is absent.
func readRecentlyLoginUID(dbPath string) (string, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer db.Close()
	var uid string
	err = db.QueryRow(`SELECT value FROM LocalStorage WHERE key='RecentlyLoginUID'`).Scan(&uid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return uid, err
}

// activeUIDTrustable reports whether LocalStorage.db's RecentlyLoginUID provably
// belongs to the current last_login_cuid: true only when the game wrote the DB
// AFTER the last login-pointer change (db mtime newer than cache mtime).
func activeUIDTrustable(cachePath, dbPath string) bool {
	cs, err1 := os.Stat(cachePath)
	ds, err2 := os.Stat(dbPath)
	if err1 != nil || err2 != nil {
		return false
	}
	return ds.ModTime().After(cs.ModTime())
}

// parseKRSDKAccounts maps the KRSDK cache JSON to []core.GameAccount. The active
// account (cuid == last_login_cuid) is flagged. UID is left "" (enriched later).
func parseKRSDKAccounts(data []byte) ([]core.GameAccount, error) {
	var c krsdkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse KRSDK cache: %w", err)
	}
	out := make([]core.GameAccount, 0, len(c.AccountList))
	for _, a := range c.AccountList {
		id := a.Cuid.String()
		out = append(out, core.GameAccount{
			ID:       id,
			Email:    a.Email,
			Username: a.Username,
			Active:   id == c.LastLoginCuid,
		})
	}
	return out, nil
}
