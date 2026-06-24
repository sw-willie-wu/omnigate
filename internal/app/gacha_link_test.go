package app

import (
	"testing"

	"omnigate/internal/core"
)

func TestExtractAccountToken(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"raw", "abc123", "abc123"},
		{"raw spaced", "  abc123  ", "abc123"},
		{"nested json", `{"code":0,"data":{"content":"abc123"}}`, "abc123"},
		{"nested json spaced", "  {\"data\":{\"content\":\"abc123\"}}  ", "abc123"},
		{"top-level content", `{"content":"xyz"}`, "xyz"},
		{"not json", "{not json", "{not json"},
		{"json without content", `{"data":{}}`, `{"data":{}}`},
	}
	for _, c := range cases {
		if got := extractAccountToken(c.in); got != c.want {
			t.Errorf("%s: extractAccountToken(%q) = %q; want %q", c.name, c.in, got, c.want)
		}
	}
}

// SetGachaCredential is the manual-paste fallback: it extracts the token from the
// pasted JSON (or raw), creates a per-account row, and sets it active — then returns
// IMMEDIATELY (uid still ""), exactly like AddGachaAccountByLogin. It must NOT block on
// the slow record fetch; the gacha board drives the refresh-with-progress for the
// uid-empty account (GachaBoard.loadForSelection). Assert the row exists, token set, uid "".
func TestSetGachaCredential_CreatesAccount(t *testing.T) {
	a := newTestAppWithEndfield(t)
	acc, err := a.SetGachaCredential("hypergryph/endfield", `  {"data":{"content":"pasted-tok"}}  `)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Token != "pasted-tok" {
		t.Errorf("returned acc.Token=%q, want pasted-tok", acc.Token)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 || accts[0].Token != "pasted-tok" {
		t.Fatalf("accts = %+v, want one row token=pasted-tok", accts)
	}
	// Returns before the slow fetch: uid is empty (the board writes it back on refresh),
	// so the login modal closes immediately instead of hanging ~80s.
	if acc.UID != "" {
		t.Fatalf("returned acc.UID = %q, want \"\" (no synchronous refresh — board drives it)", acc.UID)
	}
	if accts[0].UID != "" {
		t.Fatalf("stored uid = %q, want \"\" (no synchronous refresh)", accts[0].UID)
	}
}

func TestSetGachaCredential_AcceptsRawToken(t *testing.T) {
	a := newTestAppWithEndfield(t)
	acc, err := a.SetGachaCredential("hypergryph/endfield", "  pasted-TOK  ")
	if err != nil {
		t.Fatal(err)
	}
	if acc.Token != "pasted-TOK" {
		t.Errorf("returned acc.Token=%q, want pasted-TOK (trimmed)", acc.Token)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 || accts[0].Token != "pasted-TOK" {
		t.Fatalf("accts = %+v, want one row token=pasted-TOK (trimmed)", accts)
	}
}

func TestSetGachaCredential_UserInfoErrStillCreatesAccount(t *testing.T) {
	a := newTestAppWithEndfield(t)
	prov := a.providers[0].(*fakeLoginCredProvider)
	prov.userInfoErr = core.ErrGachaCredentialExpired
	acc, err := a.SetGachaCredential("hypergryph/endfield", "raw-token")
	if err != nil {
		t.Fatalf("SetGachaCredential should succeed even when FetchUserInfo fails: %v", err)
	}
	if acc.Token != "raw-token" {
		t.Errorf("Token=%q want raw-token", acc.Token)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 {
		t.Fatalf("want 1 account after credential paste with user/info err, got %d", len(accts))
	}
}
