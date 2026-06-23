package app

import (
	"testing"
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
// pasted JSON (or raw), creates a per-account row, sets it active, and refreshes
// to write back the roleId uid. Assert the row exists with the extracted token.
func TestSetGachaCredential_CreatesAccount(t *testing.T) {
	a := newTestAppWithEndfield(t)
	if err := a.SetGachaCredential("hypergryph/endfield", `  {"data":{"content":"pasted-tok"}}  `); err != nil {
		t.Fatal(err)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 || accts[0].Token != "pasted-tok" {
		t.Fatalf("accts = %+v, want one row token=pasted-tok", accts)
	}
	// write-back populated the roleId uid from the fake fetch (ROLE42).
	if accts[0].UID != "ROLE42" {
		t.Fatalf("uid = %q, want ROLE42 (write-back)", accts[0].UID)
	}
}

func TestSetGachaCredential_AcceptsRawToken(t *testing.T) {
	a := newTestAppWithEndfield(t)
	if err := a.SetGachaCredential("hypergryph/endfield", "  pasted-TOK  "); err != nil {
		t.Fatal(err)
	}
	accts, _ := a.gachaStore.ListGachaAccounts("hypergryph/endfield")
	if len(accts) != 1 || accts[0].Token != "pasted-TOK" {
		t.Fatalf("accts = %+v, want one row token=pasted-TOK (trimmed)", accts)
	}
}
