package kurogames

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"omnigate/internal/core"
)

const krsdkCacheFixture = `{"account_list":[` +
	`{"cuid":537195734,"email":"a@example.com","username":"U547195734A","token":"TOKEN_A_aaaaaaaa","loginType":13,"thirdNickName":""},` +
	`{"cuid":535788351,"email":"b@example.com","username":"U545788351A","token":"TOKEN_B_bbbbbbbb","loginType":13,"thirdNickName":""}` +
	`],"last_login_cuid":"535788351"}`

func TestParseKRSDKAccounts(t *testing.T) {
	got, err := parseKRSDKAccounts([]byte(krsdkCacheFixture))
	if err != nil {
		t.Fatalf("parseKRSDKAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
	if got[0].ID != "537195734" || got[0].Email != "a@example.com" || got[0].Username != "U547195734A" {
		t.Errorf("account[0] = %+v", got[0])
	}
	if got[0].Active {
		t.Errorf("account[0] should not be active")
	}
	if !got[1].Active || got[1].ID != "535788351" {
		t.Errorf("account[1] should be the active one, got %+v", got[1])
	}
	if got[0].UID != "" || got[1].UID != "" {
		t.Errorf("UID must be empty at parse time")
	}
}

func TestParseKRSDKAccounts_Malformed(t *testing.T) {
	if _, err := parseKRSDKAccounts([]byte("not json")); err == nil {
		t.Fatal("expected error on malformed json")
	}
}

func TestRewriteLastLoginCuid(t *testing.T) {
	out, err := rewriteLastLoginCuid([]byte(krsdkCacheFixture), "537195734")
	if err != nil {
		t.Fatalf("rewriteLastLoginCuid: %v", err)
	}
	if !strings.Contains(string(out), `"last_login_cuid":"537195734"`) {
		t.Errorf("pointer not flipped: %s", out)
	}
	for _, must := range []string{"TOKEN_A_aaaaaaaa", "TOKEN_B_bbbbbbbb", `"cuid":537195734`, `"cuid":535788351`} {
		if !strings.Contains(string(out), must) {
			t.Errorf("missing %q after rewrite", must)
		}
	}
	if len(out) > 0 && out[0] == 0xEF {
		t.Errorf("output has a UTF-8 BOM")
	}
}

func TestRewriteLastLoginCuid_RejectsNonDigit(t *testing.T) {
	if _, err := rewriteLastLoginCuid([]byte(krsdkCacheFixture), "abc"); err == nil {
		t.Fatal("expected error on non-digit accountID")
	}
}

func TestRewriteLastLoginCuid_FieldMissing(t *testing.T) {
	if _, err := rewriteLastLoginCuid([]byte(`{"account_list":[]}`), "1"); err == nil {
		t.Fatal("expected error when last_login_cuid is absent")
	}
}

func writeLocalStorageDB(t *testing.T, path, uid string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE LocalStorage(key TEXT, value TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO LocalStorage(key,value) VALUES('RecentlyLoginUID',?)`, uid); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// setLoginTime inserts a LoginTime_<uid> row with a raw string value (so both
// numeric epochs and malformed values can be tested).
func setLoginTime(t *testing.T, path, uid, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO LocalStorage(key,value) VALUES(?,?)`, "LoginTime_"+uid, value); err != nil {
		t.Fatalf("insert LoginTime: %v", err)
	}
}

func TestReadActiveLoginState(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181")
	setLoginTime(t, db, "700001181", "1781413062.0577722")
	uid, lt, err := readActiveLoginState(db)
	if err != nil || uid != "700001181" {
		t.Fatalf("uid=%q err=%v", uid, err)
	}
	// Assert sub-second precision: the fractional epoch must be preserved (this
	// matters when LoginTime and the cache mtime fall in the same whole second).
	if d := lt.Sub(time.Unix(1781413062, 57772200)); d < -time.Millisecond || d > time.Millisecond {
		t.Errorf("loginTime=%v, want ~1781413062.0577722 (off by %v)", lt, d)
	}
}

func TestReadActiveLoginState_NoLoginTimeRow(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181") // no LoginTime row
	uid, lt, err := readActiveLoginState(db)
	if err != nil || uid != "700001181" {
		t.Fatalf("uid=%q err=%v", uid, err)
	}
	if !lt.IsZero() {
		t.Errorf("loginTime should be zero when no LoginTime row, got %v", lt)
	}
}

func TestReadActiveLoginState_MalformedLoginTime(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181")
	setLoginTime(t, db, "700001181", "not-a-number")
	uid, lt, err := readActiveLoginState(db)
	if err != nil || uid != "700001181" {
		t.Fatalf("uid=%q err=%v", uid, err)
	}
	if !lt.IsZero() {
		t.Errorf("malformed LoginTime must parse to zero time, got %v", lt)
	}
}

func TestReadRecentlyLoginUID(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181")
	got, err := readRecentlyLoginUID(db)
	if err != nil {
		t.Fatalf("readRecentlyLoginUID: %v", err)
	}
	if got != "700001181" {
		t.Errorf("got %q, want 700001181", got)
	}
}

func TestActiveUIDTrustable_LoginTimeGuard(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	os.WriteFile(cache, []byte("{}"), 0o644)
	base := time.Now()
	os.Chtimes(cache, base, base)

	if !activeUIDTrustable(cache, base.Add(time.Minute)) {
		t.Error("loginTime after cache mtime should be trustable")
	}
	if activeUIDTrustable(cache, base.Add(-time.Minute)) {
		t.Error("loginTime before cache mtime must NOT be trustable")
	}
	if activeUIDTrustable(cache, time.Time{}) {
		t.Error("zero loginTime must NOT be trustable")
	}
}

func TestListAccounts_TransitionalNotTrusted(t *testing.T) {
	// The db file is newer than the cache (a launch/login-screen touch) but the
	// active uid's LoginTime predates the cache mtime → old guard wrongly
	// trusted; the hardened guard must NOT fill the active UID.
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	os.WriteFile(cache, []byte(krsdkCacheFixture), 0o644)
	install := t.TempDir()
	dbDir := filepath.Join(install, "Client", "Saved", "LocalStorage")
	os.MkdirAll(dbDir, 0o755)
	db := filepath.Join(dbDir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181")
	base := time.Now()
	os.Chtimes(cache, base, base)
	setLoginTime(t, db, "700001181", strconv.FormatInt(base.Add(-time.Hour).Unix(), 10)) // stale
	os.Chtimes(db, base.Add(time.Minute), base.Add(time.Minute))                          // launch touch

	p := newTestProvider()
	p.krsdkCachePathFn = func() (string, error) { return cache, nil }
	p.SetResolvedPaths(map[core.GameID]string{wuwaGID(): install})

	got, err := p.ListAccounts(context.Background(), wuwaGID())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	for _, a := range got {
		if a.Active && a.UID != "" {
			t.Errorf("transitional state must not fill active UID, got %q", a.UID)
		}
	}
}

func wuwaGID() core.GameID { return core.GameID("kurogames/wutheringwaves") }

func newTestProvider() *Provider { return New(Settings{}, nil) }

func TestListAccounts_FromFixtures(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	os.WriteFile(cache, []byte(krsdkCacheFixture), 0o644)
	install := t.TempDir()
	dbDir := filepath.Join(install, "Client", "Saved", "LocalStorage")
	os.MkdirAll(dbDir, 0o755)
	db := filepath.Join(dbDir, "LocalStorage.db")
	writeLocalStorageDB(t, db, "700001181")
	base := time.Now()
	os.Chtimes(cache, base, base)
	// LoginTime for the active uid is AFTER the cache mtime → trustable.
	setLoginTime(t, db, "700001181", strconv.FormatInt(base.Add(time.Minute).Unix(), 10))

	p := newTestProvider()
	p.krsdkCachePathFn = func() (string, error) { return cache, nil }
	p.SetResolvedPaths(map[core.GameID]string{wuwaGID(): install})

	got, err := p.ListAccounts(context.Background(), wuwaGID())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(got))
	}
	var active core.GameAccount
	for _, a := range got {
		if a.Active {
			active = a
		}
	}
	if active.ID != "535788351" || active.UID != "700001181" {
		t.Errorf("active = %+v, want ID 535788351 UID 700001181", active)
	}
}

func TestSwitchAccount_GameRunningBlocks(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	os.WriteFile(cache, []byte(krsdkCacheFixture), 0o644)
	p := newTestProvider()
	p.krsdkCachePathFn = func() (string, error) { return cache, nil }
	p.procRunningFn = func([]string) bool { return true }

	err := p.SwitchAccount(context.Background(), wuwaGID(), "537195734")
	if err == nil || err != core.ErrGameRunning {
		t.Fatalf("want ErrGameRunning, got %v", err)
	}
	b, _ := os.ReadFile(cache)
	if !strings.Contains(string(b), `"last_login_cuid":"535788351"`) {
		t.Errorf("cache was modified despite running game")
	}
}

func TestSwitchAccount_FlipsAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	os.WriteFile(cache, []byte(krsdkCacheFixture), 0o644)
	p := newTestProvider()
	p.krsdkCachePathFn = func() (string, error) { return cache, nil }
	p.procRunningFn = func([]string) bool { return false }

	if err := p.SwitchAccount(context.Background(), wuwaGID(), "537195734"); err != nil {
		t.Fatalf("SwitchAccount: %v", err)
	}
	b, _ := os.ReadFile(cache)
	if !strings.Contains(string(b), `"last_login_cuid":"537195734"`) {
		t.Errorf("not flipped: %s", b)
	}
	if _, err := os.Stat(cache + ".omnigate-bak"); err != nil {
		t.Errorf("backup not created: %v", err)
	}
}
