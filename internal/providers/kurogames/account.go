package kurogames

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// readActiveLoginState reads RecentlyLoginUID plus the per-uid LoginTime_<uid>
// from a WuWa LocalStorage.db (read-only). loginTime is the zero Time when the
// active uid has no LoginTime row or its value can't be parsed (the account
// never actually entered the game). "" uid with nil error when absent.
func readActiveLoginState(dbPath string) (uid string, loginTime time.Time, err error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return "", time.Time{}, err
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT value FROM LocalStorage WHERE key='RecentlyLoginUID'`).Scan(&uid); err != nil {
		if err == sql.ErrNoRows {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, err
	}
	if uid == "" {
		return "", time.Time{}, nil
	}
	var lt string
	if err := db.QueryRow(`SELECT value FROM LocalStorage WHERE key=?`, "LoginTime_"+uid).Scan(&lt); err != nil {
		// No LoginTime row (never entered the game) → zero time, not an error.
		return uid, time.Time{}, nil
	}
	return uid, parseEpochTime(lt), nil
}

// readRecentlyLoginUID returns just the active uid (LoginTime ignored). Retained
// as a thin wrapper for the focused reader test; ListAccounts uses
// readActiveLoginState so it can apply the per-uid trust guard.
func readRecentlyLoginUID(dbPath string) (string, error) {
	uid, _, err := readActiveLoginState(dbPath)
	return uid, err
}

// parseEpochTime parses a WuWa LoginTime value ("<epoch_seconds>.<frac>") into a
// time.Time. Unparseable → zero Time (the guard then rejects, degrading safe).
func parseEpochTime(s string) time.Time {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return time.Time{}
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec)
}

// activeUIDTrustable reports whether the active uid's in-game LoginTime is newer
// than the KRSDK cache (login-pointer) mtime — i.e. the game actually entered
// with that account AFTER the last pointer change. The whole-db-file mtime is
// deliberately NOT used: it advances on every launch (login screen, account
// picker, logging into another account) even when no account actually logged in,
// which falsely trusted a stale RecentlyLoginUID.
func activeUIDTrustable(cachePath string, loginTime time.Time) bool {
	cs, err := os.Stat(cachePath)
	if err != nil {
		return false
	}
	return loginTime.After(cs.ModTime())
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

// wuwaProcNames are the WuWa processes whose presence blocks a switch.
var wuwaProcNames = []string{"Wuthering Waves.exe", "Client-Win64-Shipping.exe", "KRSDKExternal.exe"}

// defaultKRSDKCachePath globs %APPDATA%\KR_G153\*\KRSDKUserCache.json and returns
// the newest match. The channel dir (e.g. A1730) is not hardcoded.
func defaultKRSDKCachePath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA not set")
	}
	matches, _ := filepath.Glob(filepath.Join(appData, "KR_G153", "*", "KRSDKUserCache.json"))
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	newest, newestT := matches[0], time.Time{}
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().After(newestT) {
			newest, newestT = m, st.ModTime()
		}
	}
	return newest, nil
}

// defaultLocalStorageDBPath builds the WuWa LocalStorage.db path from the
// resolved install dir (same Client\Saved base the gacha provider uses).
func defaultLocalStorageDBPath(installDir string) string {
	return filepath.Join(installDir, "Client", "Saved", "LocalStorage", "LocalStorage.db")
}

// ListAccounts implements core.AccountSwitcher. Reads the KRSDK cache for the
// account list and fills the active account's UID from LocalStorage.db when the
// mtime guard says it is trustworthy. Never reads tokens.
func (p *Provider) ListAccounts(ctx context.Context, gid core.GameID) ([]core.GameAccount, error) {
	cachePath, err := p.krsdkCachePathFn()
	if err != nil {
		if os.IsNotExist(err) {
			return []core.GameAccount{}, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []core.GameAccount{}, nil
		}
		return nil, err
	}
	accts, err := parseKRSDKAccounts(data)
	if err != nil {
		return nil, err
	}
	if installDir, derr := p.gameDir(ctx, gid); derr == nil {
		dbPath := p.localStorageDBPathFn(installDir)
		if uid, lt, uerr := readActiveLoginState(dbPath); uerr == nil && uid != "" && activeUIDTrustable(cachePath, lt) {
			for i := range accts {
				if accts[i].Active {
					accts[i].UID = uid
				}
			}
		}
	}
	return accts, nil
}

// SwitchAccount implements core.AccountSwitcher. Blocks while the game runs, then
// atomically flips last_login_cuid (one-time .omnigate-bak kept).
func (p *Provider) SwitchAccount(ctx context.Context, gid core.GameID, accountID string) error {
	if p.procRunningFn(wuwaProcNames) {
		return core.ErrGameRunning
	}
	cachePath, err := p.krsdkCachePathFn()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return err
	}
	out, err := rewriteLastLoginCuid(data, accountID)
	if err != nil {
		return err
	}
	bak := cachePath + ".omnigate-bak"
	if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
		_ = os.WriteFile(bak, data, 0o644)
	}
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, cachePath)
}

var _ core.AccountSwitcher = (*Provider)(nil)
