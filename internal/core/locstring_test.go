package core

import "testing"

func TestLocalizedString_Get(t *testing.T) {
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("zh-TW"); got != "原神" {
		t.Errorf("Get(zh-TW) = %q, want 原神", got)
	}
	if got := ls.Get("en"); got != "Genshin Impact" {
		t.Errorf("Get(en) = %q, want Genshin Impact", got)
	}
}

func TestLocalizedString_GetMissingFallsBackToEn(t *testing.T) {
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("ja"); got != "Genshin Impact" {
		t.Errorf("Get(ja) fallback = %q, want Genshin Impact", got)
	}
}

func TestLocalizedString_GetEmptyReturnsEmpty(t *testing.T) {
	ls := LocalizedString{}
	if got := ls.Get("zh-TW"); got != "" {
		t.Errorf("Get on empty = %q, want empty", got)
	}
}
