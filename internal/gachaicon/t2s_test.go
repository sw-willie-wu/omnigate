package gachaicon

import "testing"

func TestT2S(t *testing.T) {
	cases := map[string]string{
		"神里綾華": "神里绫华",
		"鋒鏑":    "锋镝",
		"11號":   "11号",
		"Lucy":  "Lucy",
		"丽娜":    "丽娜",
		"乾":     "干", // multi-candidate line (乾→干 乾): default = first token
		"麼":     "么", // single-candidate sanity
	}
	for in, want := range cases {
		if got := t2s(in); got != want {
			t.Errorf("t2s(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	if got := Canonicalize("  神里綾華 "); got != "神里綾華" {
		t.Errorf("Canonicalize trim = %q", got)
	}
}
