package app

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGachaLinkListener_CaptureAndNonce(t *testing.T) {
	game := "hypergryph/endfield"
	a := newTestAppWithCredProvider(t, game, &fakeCredProvider{fakeGachaProvider: &fakeGachaProvider{}})

	linked := make(chan string, 1)
	port, nonce, err := a.startGachaLinkListener(game, func(g string) { linked <- g })
	if err != nil {
		t.Fatal(err)
	}
	defer a.stopGachaLink()
	base := "http://127.0.0.1:" + strconv.Itoa(port) + "/cb"

	// OPTIONS preflight FIRST — the good-nonce capture is single-use and closes the
	// listener (go a.stopGachaLink()), so assert the PNA header before that.
	req, _ := http.NewRequest("OPTIONS", base, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatalf("OPTIONS: %v", err)
	} else if resp.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Error("missing PNA header on OPTIONS")
	}

	// Bad nonce → 403, nothing stored.
	if resp, err := http.Post(base+"?n=wrong", "text/plain", strings.NewReader("tokX")); err != nil {
		t.Fatal(err)
	} else if resp.StatusCode != 403 {
		t.Fatalf("bad-nonce status = %d; want 403", resp.StatusCode)
	}
	if cred, _, _ := a.gachaStore.GetGachaCred(game); cred != "" {
		t.Fatalf("bad nonce stored a cred: %q", cred)
	}

	// Good nonce → 200, cred stored (trimmed), onLinked fired.
	if resp, err := http.Post(base+"?n="+url.QueryEscape(nonce), "text/plain", strings.NewReader("  acct-TOK  ")); err != nil {
		t.Fatal(err)
	} else if resp.StatusCode != 200 {
		t.Fatalf("good status = %d; want 200", resp.StatusCode)
	}
	select {
	case g := <-linked:
		if g != game {
			t.Fatalf("linked game = %q", g)
		}
	case <-time.After(2 * time.Second): // blocking, not a non-blocking default → not flaky
		t.Fatal("onLinked not fired within 2s")
	}
	if cred, _, _ := a.gachaStore.GetGachaCred(game); cred != "acct-TOK" {
		t.Fatalf("stored cred = %q; want acct-TOK (trimmed)", cred)
	}
}

func TestBuildGachaBookmarklet_BothDomains(t *testing.T) {
	bm := buildGachaBookmarklet(54321, "deadbeefnonce")
	if !strings.HasPrefix(bm, "javascript:") {
		t.Fatalf("bookmarklet must start with javascript:, got %.20q", bm)
	}
	for _, want := range []string{
		"web-api.gryphline.com/cookie_store/account_token",
		"web-api.skport.com/cookie_store/account_token",
		"127.0.0.1:54321/cb?n=deadbeefnonce",
	} {
		if !strings.Contains(bm, want) {
			t.Errorf("bookmarklet missing %q\n got: %s", want, bm)
		}
	}
}

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

func TestSetGachaCredential_AcceptsWholeJSON(t *testing.T) {
	game := "hypergryph/endfield"
	a := newTestAppWithCredProvider(t, game, &fakeCredProvider{fakeGachaProvider: &fakeGachaProvider{}})
	if err := a.SetGachaCredential(game, `  {"data":{"content":"acct-XYZ"}}  `); err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := a.gachaStore.GetGachaCred(game); cred != "acct-XYZ" {
		t.Fatalf("cred = %q; want acct-XYZ (extracted from pasted JSON)", cred)
	}
}

func TestSetGachaCredential_Stores(t *testing.T) {
	game := "hypergryph/endfield"
	// a.ctx is nil in this harness, so SetGachaCredential's a.emit("gacha:linked")
	// is a safe no-op (a non-nil Background ctx would log.Fatalf via EventsEmit).
	a := newTestAppWithCredProvider(t, game, &fakeCredProvider{fakeGachaProvider: &fakeGachaProvider{}})
	if err := a.SetGachaCredential(game, "  pasted-TOK  "); err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := a.gachaStore.GetGachaCred(game); cred != "pasted-TOK" {
		t.Fatalf("cred = %q; want pasted-TOK (trimmed)", cred)
	}
}
