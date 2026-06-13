# WuWa Account Switcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user switch between KRSDK-remembered WuWa (鳴潮) accounts from the omnigate home screen by flipping `last_login_cuid` in the KRSDK user-cache, storing no credentials.

**Architecture:** A new optional `core.AccountSwitcher` capability, implemented only by the `kurogames` provider in v1 (so only WuWa shows the chip). The provider reads the KRSDK cache JSON (`%APPDATA%\KR_G153\*\KRSDKUserCache.json`) for the account list and writes one field to switch; the App layer owns a non-sensitive `wuwa_uid_cache.json` (cuid→game-UID) and exposes two bound methods; the frontend adds an account chip to the right of `NavStrip`.

**Tech Stack:** Go (no CGO → `modernc.org/sqlite`), Wails, Vue 3 + Pinia + vue-i18n. Spec: `docs/superpowers/specs/2026-06-13-omnigate-wuwa-account-switcher-design.md`.

**Branch:** already on `feat/wuwa-account-switcher`. Commit convention: no `Co-Authored-By` trailer. Tests run CGO-free, so **never pass `-race`**.

---

## File map

- `internal/core/provider.go` — add `GameAccount` + `AccountSwitcher` (Task 1)
- `internal/core/errors.go` — add `ErrGameRunning`, `ErrAccountSwitchUnsupported` + `ErrorCode` cases (Task 1)
- `internal/core/errors_test.go` — new, ErrorCode mapping (Task 1)
- `internal/providers/kurogames/account.go` — new: parse, rewrite, locate, UID read, `ListAccounts`/`SwitchAccount` (Tasks 2–5)
- `internal/providers/kurogames/account_test.go` — new (Tasks 2–5)
- `internal/providers/kurogames/process_check_windows.go` + `_other.go` — add `anyProcessRunning` (Task 5)
- `internal/app/account_handler.go` — new: `ListGameAccounts`/`SwitchGameAccount` + uid-cache sidecar (Task 6)
- `internal/app/account_handler_test.go` — new (Task 6)
- `frontend/src/components/AccountChip.vue` — new (Task 7)
- `frontend/src/components/NavStrip.vue` — mount the chip (Task 7)
- `frontend/src/locales/{zh-TW,zh-CN,en}.json` — chip strings (Task 7)
- `frontend/src/components/__tests__/AccountChip.spec.ts` — new (Task 7)

---

## Task 1: Core capability + sentinel errors

**Files:**
- Modify: `internal/core/provider.go` (append near the other optional capabilities, after `NewsProvider`)
- Modify: `internal/core/errors.go`
- Test: `internal/core/errors_test.go` (create)

- [ ] **Step 1: Write the failing test** — `internal/core/errors_test.go`

```go
package core

import (
	"fmt"
	"testing"
)

func TestErrorCode_AccountSwitcherSentinels(t *testing.T) {
	cases := map[error]string{
		ErrGameRunning:              "game_running",
		ErrAccountSwitchUnsupported: "account_switch_unsupported",
	}
	for sentinel, want := range cases {
		if got := ErrorCode(fmt.Errorf("wrap: %w", sentinel)); got != want {
			t.Errorf("ErrorCode(%v) = %q, want %q", sentinel, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestErrorCode_AccountSwitcherSentinels`
Expected: FAIL — `undefined: ErrGameRunning` / `ErrAccountSwitchUnsupported`.

- [ ] **Step 3: Add the sentinels + codes** — `internal/core/errors.go`

Add two vars to the `var (...)` block:

```go
	ErrGameRunning              = errors.New("game is running")
	ErrAccountSwitchUnsupported = errors.New("account switching not supported for this game")
```

Add two cases to the `ErrorCode` switch (before `default:`):

```go
	case errors.Is(err, ErrGameRunning):
		return "game_running"
	case errors.Is(err, ErrAccountSwitchUnsupported):
		return "account_switch_unsupported"
```

- [ ] **Step 4: Add the capability types** — append to `internal/core/provider.go`

```go
// GameAccount is one launcher-remembered account for a game. The provider never
// exposes credentials; ID is a provider-defined opaque key (for kurogames: the
// KRSDK cuid). UID is the in-game UID, "" when not yet known.
type GameAccount struct {
	ID       string `json:"id"`
	UID      string `json:"uid"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

// AccountSwitcher is an optional Provider capability: list the launcher-
// remembered accounts for a game and switch which one logs in next. Switching
// mutates only the publisher's own login-pointer state and requires the game to
// be closed. Implementations store no credentials.
type AccountSwitcher interface {
	ListAccounts(ctx context.Context, gid GameID) ([]GameAccount, error)
	SwitchAccount(ctx context.Context, gid GameID, accountID string) error // accountID = GameAccount.ID
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/core/`
Expected: PASS, no build errors.

- [ ] **Step 6: Commit**

```bash
git add internal/core/provider.go internal/core/errors.go internal/core/errors_test.go
git commit -m "feat(wuwa-switcher): core AccountSwitcher capability + sentinel errors"
```

---

## Imports note (Tasks 2–5 build two files incrementally)

Each task below shows `import (...)` snippets, but `account.go` and
`account_test.go` each keep **one** import block that grows across tasks. Treat
every snippet's imports as "ensure these are present" (merge, never duplicate).
The complete final import sets are:

**`account.go`:**
```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	_ "modernc.org/sqlite"
	"omnigate/internal/core"
)
```

**`account_test.go`:**
```go
import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"omnigate/internal/core"
)
```

---

## Task 2: kurogames — parse KRSDK account cache (pure)

**Files:**
- Create: `internal/providers/kurogames/account.go`
- Test: `internal/providers/kurogames/account_test.go` (create)

- [ ] **Step 1: Write the failing test** — `internal/providers/kurogames/account_test.go`

```go
package kurogames

import "testing"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kurogames/ -run TestParseKRSDKAccounts`
Expected: FAIL — `undefined: parseKRSDKAccounts`.

- [ ] **Step 3: Write the parser** — `internal/providers/kurogames/account.go`

```go
package kurogames

import (
	"encoding/json"
	"fmt"

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
```

> Note: `json.Number` makes `cuid` (a JSON number) stringify without float
> rounding; `537195734` → `"537195734"`. No `strconv` needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/kurogames/ -run TestParseKRSDKAccounts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/kurogames/account.go internal/providers/kurogames/account_test.go
git commit -m "feat(wuwa-switcher): parse KRSDK account cache (no token in memory)"
```

---

## Task 3: kurogames — flip `last_login_cuid` (pure, token-safe)

**Files:**
- Modify: `internal/providers/kurogames/account.go`
- Test: `internal/providers/kurogames/account_test.go`

- [ ] **Step 1: Write the failing test** — append to `account_test.go` (imports per the consolidated block)

```go
func TestRewriteLastLoginCuid(t *testing.T) {
	out, err := rewriteLastLoginCuid([]byte(krsdkCacheFixture), "537195734")
	if err != nil {
		t.Fatalf("rewriteLastLoginCuid: %v", err)
	}
	if !strings.Contains(string(out), `"last_login_cuid":"537195734"`) {
		t.Errorf("pointer not flipped: %s", out)
	}
	// tokens and account_list cuids must survive verbatim
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kurogames/ -run TestRewriteLastLoginCuid`
Expected: FAIL — `undefined: rewriteLastLoginCuid`.

- [ ] **Step 3: Implement the rewrite** — append to `account.go` (and add `"regexp"` to the import block):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/kurogames/ -run TestRewriteLastLoginCuid`
Expected: PASS. Also run `go test ./internal/providers/kurogames/ -run TestParseKRSDKAccounts` to confirm Task 2 still green.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/kurogames/account.go internal/providers/kurogames/account_test.go
git commit -m "feat(wuwa-switcher): token-safe last_login_cuid rewrite"
```

---

## Task 4: kurogames — active UID from LocalStorage.db + mtime guard

**Files:**
- Modify: `internal/providers/kurogames/account.go`
- Test: `internal/providers/kurogames/account_test.go`

- [ ] **Step 1: Write the failing test** — append to `account_test.go` (imports per the consolidated block)

```go
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
	// db newer than cache → trustable
	os.Chtimes(cache, base, base)
	os.Chtimes(db, base.Add(time.Minute), base.Add(time.Minute))
	if !activeUIDTrustable(cache, db) {
		t.Error("db newer than cache should be trustable")
	}
	// cache newer than db (just switched, game not launched) → not trustable
	os.Chtimes(cache, base.Add(time.Minute), base.Add(time.Minute))
	os.Chtimes(db, base, base)
	if activeUIDTrustable(cache, db) {
		t.Error("cache newer than db must NOT be trustable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kurogames/ -run 'TestReadRecentlyLoginUID|TestActiveUIDTrustable'`
Expected: FAIL — `undefined: readRecentlyLoginUID` / `activeUIDTrustable`.

- [ ] **Step 3: Implement** — append to `account.go` (imports per the consolidated `account.go` block)

```go
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
```

(`database/sql`, `os`, and the blank `modernc.org/sqlite` are now present via the
consolidated `account.go` import block.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/kurogames/ -run 'TestReadRecentlyLoginUID|TestActiveUIDTrustable'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/kurogames/account.go internal/providers/kurogames/account_test.go
git commit -m "feat(wuwa-switcher): active UID read + mtime trust guard"
```

---

## Task 5: kurogames — locate cache, process gate, ListAccounts/SwitchAccount

**Files:**
- Modify: `internal/providers/kurogames/account.go`
- Modify: `internal/providers/kurogames/kurogames.go` (add seam fields + defaults)
- Modify: `internal/providers/kurogames/process_check_windows.go`
- Create: `internal/providers/kurogames/process_check_other.go` only if no non-windows stub exists (check first: `ls internal/providers/kurogames/process_check_*`)
- Test: `internal/providers/kurogames/account_test.go`

- [ ] **Step 1: Add seam fields + defaults** — `kurogames.go`

Add to the `Provider` struct (after `tempRootFn`):

```go
	krsdkCachePathFn     func() (string, error)        // locate KRSDKUserCache.json (injectable)
	localStorageDBPathFn func(installDir string) string // locate LocalStorage.db (injectable)
	procRunningFn        func(names []string) bool      // process gate (injectable)
```

In `New`, after `p.recordDelay = ...`:

```go
	p.krsdkCachePathFn = defaultKRSDKCachePath
	p.localStorageDBPathFn = defaultLocalStorageDBPath
	p.procRunningFn = anyProcessRunning
```

- [ ] **Step 2: Add the multi-name process helper** — `process_check_windows.go`

Append:

```go
// anyProcessRunning reports whether ANY of the named exes is running.
func anyProcessRunning(names []string) bool {
	for _, n := range names {
		if platformIsProcessRunning(n) {
			return true
		}
	}
	return false
}
```

If `process_check_other.go` (non-windows stub) exists, add the same
`anyProcessRunning` there too (its `platformIsProcessRunning` stub returns
false). If it does NOT exist, create `process_check_other.go`:

```go
//go:build !windows

package kurogames

func platformIsProcessRunning(string) bool { return false }
func anyProcessRunning([]string) bool       { return false }
```

(Only add the `platformIsProcessRunning` stub line if the file had to be created;
if a stub already defines it elsewhere, keep just `anyProcessRunning`.)

- [ ] **Step 3: Write the failing test** — append to `account_test.go`

```go
// (account_test.go imports are the consolidated block above; wuwaProcNames is
// defined in account.go — do NOT redeclare it here.)

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
	// make db newer than cache so the active UID is trusted
	base := time.Now()
	os.Chtimes(cache, base, base)
	os.Chtimes(db, base.Add(time.Minute), base.Add(time.Minute))

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
	// active account is cuid 535788351 → its UID is filled from the db
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
	p.procRunningFn = func([]string) bool { return true } // pretend running

	err := p.SwitchAccount(context.Background(), wuwaGID(), "537195734")
	if err == nil || err != core.ErrGameRunning {
		t.Fatalf("want ErrGameRunning, got %v", err)
	}
	// file must be untouched
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
```

> `wuwaGID()` is a tiny test helper added in Step 5. The real WuWa game id is
> `core.GameID("kurogames/wutheringwaves")` (from `meta.go`'s `games` table).

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/providers/kurogames/ -run 'TestListAccounts_FromFixtures|TestSwitchAccount'`
Expected: FAIL — `undefined: ListAccounts` / `SwitchAccount` / `wuwaGID` / `defaultKRSDKCachePath` / `defaultLocalStorageDBPath`.

- [ ] **Step 5: Implement** — append to `account.go` (imports per the consolidated `account.go` block — `context`, `path/filepath`, `time` are all in it)

```go
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
			return []core.GameAccount{}, nil // never logged in → empty, no chip
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
	// Best-effort active-UID enrichment (failures leave UID "").
	if installDir, derr := p.gameDir(ctx, gid); derr == nil {
		dbPath := p.localStorageDBPathFn(installDir)
		if activeUIDTrustable(cachePath, dbPath) {
			if uid, uerr := readRecentlyLoginUID(dbPath); uerr == nil && uid != "" {
				for i := range accts {
					if accts[i].Active {
						accts[i].UID = uid
					}
				}
			}
		}
	}
	return accts, nil
}

// SwitchAccount implements core.AccountSwitcher. Blocks while the game runs,
// then atomically flips last_login_cuid (one-time .omnigate-bak kept). accountID
// must be a known cuid; validation is enforced by rewriteLastLoginCuid.
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
	// one-time backup (this file holds tokens; a bad write means full re-login)
	bak := cachePath + ".omnigate-bak"
	if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
		_ = os.WriteFile(bak, data, 0o644)
	}
	// atomic write: temp + rename (os.WriteFile emits BOM-free UTF-8)
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, cachePath)
}
```

Then add the test helper to `account_test.go`:

```go
func wuwaGID() core.GameID { return core.GameID("kurogames/wutheringwaves") }
```

Finally add the compile-time assertion at the bottom of `account.go`:

```go
var _ core.AccountSwitcher = (*Provider)(nil)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/providers/kurogames/`
Expected: PASS (all account tests + existing kurogames tests). If `wuwaGID()`'s
suffix is wrong, `ListAccounts` returns `ErrGameNotInstalled` via `gameDir`; fix
the literal to match `meta.go`.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/kurogames/
git commit -m "feat(wuwa-switcher): kurogames ListAccounts/SwitchAccount + process gate"
```

---

## Task 6: App layer — bound methods + cuid→UID sidecar

**Files:**
- Create: `internal/app/account_handler.go`
- Test: `internal/app/account_handler_test.go` (create)

- [ ] **Step 1: Write the failing test** — `internal/app/account_handler_test.go`

```go
package app

import (
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestUIDCache_RecordBackfill(t *testing.T) {
	dir := t.TempDir()
	c := loadUIDCache(filepath.Join(dir, "wuwa_uid_cache.json"))
	c.Record("535788351", "700001181")

	accts := []core.GameAccount{
		{ID: "537195734", Active: false},
		{ID: "535788351", UID: "700001181", Active: true},
	}
	c.Backfill(accts)
	if accts[1].UID != "700001181" {
		t.Errorf("active UID should remain: %+v", accts[1])
	}
	// reload from disk → mapping persisted, backfills the non-active one once seen
	c2 := loadUIDCache(filepath.Join(dir, "wuwa_uid_cache.json"))
	again := []core.GameAccount{{ID: "535788351"}}
	c2.Backfill(again)
	if again[0].UID != "700001181" {
		t.Errorf("persisted cache should backfill UID, got %+v", again[0])
	}
}

// fakeProvider satisfies core.Provider (embed the interface; unused methods are
// never called) but does NOT implement core.AccountSwitcher.
type fakeProvider struct{ core.Provider }

func (fakeProvider) ID() core.BackendID { return "fake" }
func (fakeProvider) Games() []core.GameDescriptor {
	return []core.GameDescriptor{{ID: "fake/g", Backend: "fake"}}
}

func TestListGameAccounts_Unsupported(t *testing.T) {
	a := &App{}
	if err := a.registerProvider(fakeProvider{}); err != nil {
		t.Fatalf("registerProvider: %v", err)
	}
	_, err := a.ListGameAccounts("fake/g")
	if err != core.ErrAccountSwitchUnsupported {
		t.Fatalf("want ErrAccountSwitchUnsupported, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestUIDCache_RecordBackfill`
Expected: FAIL — `undefined: loadUIDCache`.

- [ ] **Step 3: Implement** — `internal/app/account_handler.go`

```go
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"omnigate/internal/core"
)

// uidCachePathFor puts wuwa_uid_cache.json beside settings (mirrors
// playStatePathFor / gachaDBPathFor).
func uidCachePathFor(settingsPath string) string {
	dir := filepath.Dir(settingsPath)
	if dir == "." || dir == "" {
		return "wuwa_uid_cache.json"
	}
	return filepath.Join(dir, "wuwa_uid_cache.json")
}

// uidCache is the App-owned, non-sensitive cuid→game-UID map (numbers only).
type uidCache struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

func loadUIDCache(path string) *uidCache {
	c := &uidCache{path: path, m: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m) // corrupt → empty, best-effort
	}
	return c
}

// Record stores cuid→uid and persists atomically (best-effort).
func (c *uidCache) Record(cuid, uid string) {
	if cuid == "" || uid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[cuid] == uid {
		return
	}
	c.m[cuid] = uid
	b, _ := json.MarshalIndent(c.m, "", "  ")
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// Backfill fills every account's empty UID from the cache; also records any
// account that already carries a UID (the trusted active one).
func (c *uidCache) Backfill(accts []core.GameAccount) {
	for i := range accts {
		if accts[i].UID != "" {
			c.Record(accts[i].ID, accts[i].UID)
			continue
		}
		c.mu.Lock()
		if uid, ok := c.m[accts[i].ID]; ok {
			accts[i].UID = uid
		}
		c.mu.Unlock()
	}
}

// ListGameAccounts returns the switchable accounts for a game, UID-enriched from
// the App-owned cache. Backends without the capability yield
// ErrAccountSwitchUnsupported (frontend hides the chip).
func (a *App) ListGameAccounts(gameID string) ([]core.GameAccount, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return nil, err
	}
	sw, ok := p.(core.AccountSwitcher)
	if !ok {
		return nil, core.ErrAccountSwitchUnsupported
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	accts, err := sw.ListAccounts(ctx, gid)
	if err != nil {
		return nil, err
	}
	a.uidCache.Backfill(accts)
	return accts, nil
}

// SwitchGameAccount switches the active account for a game (game must be closed).
func (a *App) SwitchGameAccount(gameID, accountID string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	sw, ok := p.(core.AccountSwitcher)
	if !ok {
		return core.ErrAccountSwitchUnsupported
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return sw.SwitchAccount(ctx, gid, accountID)
}
```

- [ ] **Step 4: Wire the cache into the App** — `internal/app/app.go`

Add to the `App` struct (after `gachaStore`):

```go
	uidCache       *uidCache
```

In `New`, after the `a.playState = loadPlayState(...)` line:

```go
	a.uidCache = loadUIDCache(uidCachePathFor(settingsPath))
```

> **Intentionally NOT extended:** `App.ErrorCode`/`App.ErrorMessage` are left as-is.
> Nothing in `frontend/src` consumes them (verified: no callers), and the chip
> owns its own error detection (matching the Go error text "game is running") and
> its own localized toast. Adding the two sentinels to the `App.ErrorCode` loop
> would be dead code, so per spec §5's reality-check we skip it. (The core-level
> `core.ErrorCode` cases from Task 1 stay, for consistency with the other
> sentinels.)

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/app/ -run 'TestUIDCache_RecordBackfill|TestListGameAccounts_Unsupported'`
Expected: PASS. Then `go build ./...` to confirm the App struct/`New` wiring compiles.

- [ ] **Step 6: Commit**

```bash
git add internal/app/account_handler.go internal/app/account_handler_test.go internal/app/app.go
git commit -m "feat(wuwa-switcher): App ListGameAccounts/SwitchGameAccount + uid cache"
```

---

## Task 7: Frontend — AccountChip in NavStrip

**Files:**
- Regenerate: `frontend/wailsjs/go/app/App.{d.ts,js}` (run `wails generate module` or `wails dev` once; if unavailable, hand-add `ListGameAccounts`/`SwitchGameAccount` to the bindings mirroring `RefreshGacha`)
- Create: `frontend/src/components/AccountChip.vue`
- Modify: `frontend/src/components/NavStrip.vue`
- Modify: `frontend/src/locales/zh-TW.json`, `zh-CN.json`, `en.json`
- Test: `frontend/src/components/__tests__/AccountChip.spec.ts` (create)

- [ ] **Step 1: Regenerate bindings**

Run from `frontend/`: `wails generate module` (or build once). Verify
`ListGameAccounts` and `SwitchGameAccount` appear in `wailsjs/go/app/App.d.ts`.
If the toolchain is unavailable, add by hand to `App.d.ts`:

```ts
export function ListGameAccounts(arg1:string):Promise<Array<core.GameAccount>>;
export function SwitchGameAccount(arg1:string,arg2:string):Promise<void>;
```

and to `App.js` mirroring an existing two-arg/one-arg method.

- [ ] **Step 2: Write the failing component test** — `frontend/src/components/__tests__/AccountChip.spec.ts`

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';

const list = vi.fn();
const switchAcc = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SwitchGameAccount: (...a: unknown[]) => switchAcc(...a),
}));

import AccountChip from '../AccountChip.vue';

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {
  account: {
    switchHint: 'Switch account', addInGame: 'Log in a new account in-game',
    switchedToast: 'Switched to {name}; takes effect on next launch',
    gameRunning: 'Close the game before switching accounts',
    switchFailed: 'Account switch failed',
  } } } });

function mountChip() {
  return mount(AccountChip, { props: { gameId: 'kurogames/wutheringwaves' }, global: { plugins: [i18n] } });
}

describe('AccountChip', () => {
  beforeEach(() => { list.mockReset(); switchAcc.mockReset(); });

  it('renders UID primary + email secondary for the active account', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    const w = mountChip();
    await flushPromises();
    expect(w.text()).toContain('700727240');
    expect(w.text()).toContain('a@example.com');
  });

  it('hides itself when the backend lacks the capability', async () => {
    list.mockRejectedValue(new Error('account switching not supported for this game'));
    const w = mountChip();
    await flushPromises();
    expect(w.find('[data-test="account-chip"]').exists()).toBe(false);
  });

  it('calls SwitchGameAccount when picking another account', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    switchAcc.mockResolvedValue(undefined);
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click'); // open dropdown
    await w.find('[data-test="account-opt-535788351"]').trigger('click');
    expect(switchAcc).toHaveBeenCalledWith('kurogames/wutheringwaves', '535788351');
    await flushPromises();
    expect(w.find('[data-test="account-toast"]').text()).toContain('Switched to');
  });

  it('shows the game-running toast when the switch is blocked', async () => {
    list.mockResolvedValue([
      { id: '537195734', uid: '700727240', email: 'a@example.com', username: 'U547195734A', active: true },
      { id: '535788351', uid: '', email: 'b@example.com', username: 'U545788351A', active: false },
    ]);
    switchAcc.mockRejectedValue(new Error('game is running'));
    const w = mountChip();
    await flushPromises();
    await w.find('[data-test="account-chip"]').trigger('click');
    await w.find('[data-test="account-opt-535788351"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-test="account-toast"]').text()).toContain('Close the game');
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run from `frontend/`: `npx vitest run src/components/__tests__/AccountChip.spec.ts`
Expected: FAIL — cannot resolve `../AccountChip.vue`.

- [ ] **Step 4: Implement the component** — `frontend/src/components/AccountChip.vue`

```vue
<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { ListGameAccounts, SwitchGameAccount } from '../../wailsjs/go/app/App';

type Account = { id: string; uid: string; email: string; username: string; active: boolean };

const props = defineProps<{ gameId: string }>();
const { t } = useI18n();

const accounts = ref<Account[]>([]);
const supported = ref(false);
const open = ref(false);
const toast = ref('');
let toastTimer: ReturnType<typeof setTimeout> | undefined;

const active = computed(() => accounts.value.find((a) => a.active));
function primary(a: Account): string { return a.uid || a.email || a.username; }

function showToast(msg: string) {
  toast.value = msg;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.value = ''; }, 4000);
}

async function load() {
  try {
    accounts.value = await ListGameAccounts(props.gameId);
    supported.value = accounts.value.length > 0;
  } catch {
    accounts.value = []; supported.value = false; // ErrAccountSwitchUnsupported / empty → hide
  }
}

async function pick(a: Account) {
  open.value = false;
  if (a.active) return;
  try {
    await SwitchGameAccount(props.gameId, a.id);
    await load();
    // chip owns its own localized toast (no global error→toast pipeline exists)
    showToast(t('account.switchedToast', { name: primary(a) }));
  } catch (e) {
    // core.ErrGameRunning surfaces as the Go error text "game is running"
    const msg = String((e as Error)?.message ?? e);
    showToast(msg.includes('game is running') ? t('account.gameRunning') : t('account.switchFailed'));
  }
}

onMounted(load);
watch(() => props.gameId, load);
</script>

<template>
  <div v-if="supported" class="account-chip" data-test="account-chip" @click="open = !open">
    <span class="avatar">{{ (active?.username || '?').slice(0, 1) }}</span>
    <span class="ident">
      <span class="primary">{{ active ? primary(active) : '' }}</span>
      <span class="secondary">{{ active?.email }}</span>
    </span>
    <span class="chev">▾</span>

    <div v-if="open" class="account-menu" @click.stop>
      <button
        v-for="a in accounts"
        :key="a.id"
        class="account-opt"
        :data-test="`account-opt-${a.id}`"
        @click="pick(a)"
      >
        <span class="tick">{{ a.active ? '✓' : '' }}</span>
        <span class="opt-ident">
          <span class="primary">{{ primary(a) }}</span>
          <span class="secondary">{{ a.email }}</span>
        </span>
      </button>
      <div class="account-hint">＋ {{ t('account.addInGame') }}</div>
    </div>

    <div v-if="toast" class="account-toast" data-test="account-toast" @click.stop>{{ toast }}</div>
  </div>
</template>

<style scoped>
.account-chip { display: flex; align-items: center; gap: 8px; margin-left: auto; cursor: pointer;
  padding: 4px 10px; border-radius: 999px; background: rgba(255,255,255,0.04); position: relative; }
.avatar { width: 24px; height: 24px; border-radius: 50%; display: grid; place-items: center;
  background: #1f6f4a; color: #d8ffe9; font-size: 12px; }
.ident { display: flex; flex-direction: column; line-height: 1.1; }
.ident .primary { font-size: 13px; }
.ident .secondary { font-size: 10px; opacity: 0.6; }
.chev { opacity: 0.6; }
.account-menu { position: absolute; top: calc(100% + 6px); right: 0; min-width: 220px; z-index: 20;
  background: #14161c; border: 1px solid rgba(255,255,255,0.08); border-radius: 10px; padding: 4px; }
.account-opt { display: flex; align-items: center; gap: 8px; width: 100%; background: none; border: none;
  color: inherit; text-align: left; padding: 8px; border-radius: 6px; cursor: pointer; }
.account-opt:hover { background: rgba(255,255,255,0.06); }
.tick { width: 12px; }
.opt-ident { display: flex; flex-direction: column; line-height: 1.15; }
.opt-ident .primary { font-size: 13px; }
.opt-ident .secondary { font-size: 11px; opacity: 0.6; }
.account-hint { padding: 8px; font-size: 12px; opacity: 0.6; }
.account-toast { position: absolute; top: calc(100% + 6px); right: 0; max-width: 280px; z-index: 21;
  background: #14161c; border: 1px solid rgba(255,255,255,0.12); border-radius: 8px;
  padding: 8px 12px; font-size: 12px; }
</style>
```

- [ ] **Step 5: Mount in NavStrip + add locale strings**

`NavStrip.vue` — import the games store + chip, render the chip after the tabs:

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import AccountChip from './AccountChip.vue';

const { t } = useI18n();
const view = useViewStore();
const games = useGamesStore();
</script>
```

In the template, inside `<div class="nav-strip">`, after the gacha `<button>`:

```vue
    <AccountChip v-if="games.selectedID" :key="games.selectedID" :game-id="games.selectedID" />
```

No style change needed for the strip: `.nav-strip` lives in
`frontend/src/styles/theme.css` (not a `<style>` block in `NavStrip.vue`) and is
already `display:flex; align-items:center`, so the chip's `margin-left:auto`
pushes it to the right as-is. (Verify this is still true before relying on it.)

Add to each locale file (`en.json` shown; translate for zh-TW / zh-CN) under a
new `"account"` key:

```json
"account": {
  "switchHint": "Switch account",
  "addInGame": "Log in a new account in-game",
  "switchedToast": "Switched to {name}; takes effect on next launch",
  "gameRunning": "Close the game before switching accounts",
  "switchFailed": "Account switch failed"
}
```

zh-TW: `"切換帳號"`, `"在遊戲登入新帳號"`, `"已切到 {name}，啟動遊戲生效"`, `"請先關閉遊戲再切換"`, `"帳號切換失敗"`.
zh-CN: `"切换账号"`, `"在游戏登录新账号"`, `"已切到 {name}，启动游戏生效"`, `"请先关闭游戏再切换"`, `"账号切换失败"`.

- [ ] **Step 6: Run tests to verify they pass**

Run from `frontend/`: `npx vitest run src/components/__tests__/AccountChip.spec.ts`
Expected: PASS. Then `npx vitest run` to confirm no regressions, and
`go build ./...` for the backend.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/AccountChip.vue frontend/src/components/NavStrip.vue frontend/src/components/__tests__/AccountChip.spec.ts frontend/src/locales/ frontend/wailsjs/
git commit -m "feat(wuwa-switcher): AccountChip in NavStrip (UID primary, email secondary)"
```

---

## Final verification

- [ ] `go test ./...` (no `-race`) — all green.
- [ ] `go build ./...` — clean.
- [ ] `cd frontend && npx vitest run` — all green.
- [ ] Manual smoke (real machine, WuWa closed): chip lists both accounts, picking the other one flips `last_login_cuid`; launching WuWa lands in the picked account; switching while WuWa runs is blocked with the toast. (This mirrors the reverse-engineering already verified by hand.)

## Spec coverage check

- §4.1 capability + types + accountID validation → Tasks 1, 3, 5.
- §4.2 locate/parse/flip/atomic-bak/BOM-free + active-UID + mtime guard + process gate seam → Tasks 2–5.
- §4.3 App methods + App-owned uid cache + capability gate + auto-Bind → Task 6.
- §4.4 chip placement, UID-primary/email-secondary, capability-gated, dropdown + hint → Task 7.
- §5 typed errors + codes (chip owns its own toast) → Tasks 1, 7.
- §7 injectable seams (procRunningFn, path fns) + fixture units → Tasks 4–6.
- §8 out-of-scope (nickname API, HoYo/Endfield) → intentionally not implemented.
