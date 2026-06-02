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

func TestParseGameID_RejectsMultiSlashSuffix(t *testing.T) {
	cases := []struct {
		gid     GameID
		wantErr bool
	}{
		{"hoyoverse/genshin", false},                   // valid
		{"hoyoverse/genshin/cn", true},                 // multi-slash → reject
		{"hoyoverse", true},                            // no slash → reject
		{"hoyoverse/", true},                           // empty suffix → reject
		{"/genshin", true},                             // empty backend → reject
		{"hoyoverse/genshin-impact-cn-rev1", false},    // dashes OK
	}
	for _, c := range cases {
		t.Run(string(c.gid), func(t *testing.T) {
			_, _, err := ParseGameID(c.gid)
			gotErr := err != nil
			if gotErr != c.wantErr {
				t.Errorf("ParseGameID(%q): err=%v wantErr=%v", c.gid, err, c.wantErr)
			}
		})
	}
}
