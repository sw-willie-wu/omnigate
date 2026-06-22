package gachaicon

import "testing"

func TestT2S(t *testing.T) {
	cases := map[string]string{
		"神里綾華": "神里绫华",
		"鋒鏑":   "锋镝",
		"11號":  "11号",
		"Lucy": "Lucy",
		"丽娜":   "丽娜",
		"乾":    "干", // multi-candidate line (乾→干 乾): default = first token
		"麼":    "么", // single-candidate sanity
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
	// Yatta embeds markup (HSR 銀狼LV.<unbreak>999</unbreak>); the record API returns
	// the clean name. Both must canonicalize to the same key.
	const clean = "銀狼LV.999"
	if got := Canonicalize("銀狼LV.<unbreak>999</unbreak>"); got != clean {
		t.Errorf("Canonicalize tagged = %q, want %q", got, clean)
	}
	if got := Canonicalize(clean); got != clean {
		t.Errorf("Canonicalize clean = %q, want %q", got, clean)
	}
}
