package core

import "testing"

func TestParseGameID_Valid(t *testing.T) {
	b, suffix, err := ParseGameID("hoyoverse/genshin")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if b != "hoyoverse" {
		t.Errorf("backend = %q, want hoyoverse", b)
	}
	if suffix != "genshin" {
		t.Errorf("suffix = %q, want genshin", suffix)
	}
}

func TestParseGameID_Invalid(t *testing.T) {
	cases := []GameID{"", "no-slash", "/leading", "trailing/", "hoyoverse//"}
	for _, c := range cases {
		if _, _, err := ParseGameID(c); err == nil {
			t.Errorf("ParseGameID(%q) succeeded; want error", c)
		}
	}
}

func TestParseGameID_SuffixWithSlash(t *testing.T) {
	// suffix may itself contain slashes — split is on FIRST slash only
	b, suffix, err := ParseGameID("hoyoverse/sub/game")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if b != "hoyoverse" || suffix != "sub/game" {
		t.Errorf("got (%q, %q), want (hoyoverse, sub/game)", b, suffix)
	}
}
