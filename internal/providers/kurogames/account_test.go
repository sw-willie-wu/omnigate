package kurogames

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

func TestActiveUIDTrustable_MtimeGuard(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "KRSDKUserCache.json")
	db := filepath.Join(dir, "LocalStorage.db")
	os.WriteFile(cache, []byte("{}"), 0o644)
	os.WriteFile(db, []byte("x"), 0o644)

	base := time.Now()
	os.Chtimes(cache, base, base)
	os.Chtimes(db, base.Add(time.Minute), base.Add(time.Minute))
	if !activeUIDTrustable(cache, db) {
		t.Error("db newer than cache should be trustable")
	}
	os.Chtimes(cache, base.Add(time.Minute), base.Add(time.Minute))
	os.Chtimes(db, base, base)
	if activeUIDTrustable(cache, db) {
		t.Error("cache newer than db must NOT be trustable")
	}
}
