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

func TestLocalizedString_GetZhCN_PrefersExplicit(t *testing.T) {
	ls := LocalizedString{
		"zh-CN": "明日方舟：终末地",
		"zh-TW": "明日方舟：終末地",
		"en":    "Arknights: Endfield",
	}
	if got := ls.Get("zh-CN"); got != "明日方舟：终末地" {
		t.Errorf("Get(zh-CN) = %q, want explicit zh-CN", got)
	}
}

func TestLocalizedString_GetZhCN_FallsBackToEn_NotZhTW(t *testing.T) {
	// HoYoverse games only ship zh-TW + en. zh-CN must NOT fall through to zh-TW
	// (would mix simplified/traditional in one sidebar). Should fall to en.
	ls := LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"}
	if got := ls.Get("zh-CN"); got != "Genshin Impact" {
		t.Errorf("Get(zh-CN) = %q, want en fallback Genshin Impact", got)
	}
}

func TestLocalizedString_GetZhCN_FallsToZhTW_WhenNoEn(t *testing.T) {
	// Edge case: only zh-TW available. zh-CN → en (missing) → zh-TW.
	ls := LocalizedString{"zh-TW": "原神"}
	if got := ls.Get("zh-CN"); got != "原神" {
		t.Errorf("Get(zh-CN) with only zh-TW = %q, want 原神", got)
	}
}

func TestLocalizedString_GetZhTW_NoFallthroughToZhCN(t *testing.T) {
	// User on zh-TW must not see zh-CN content.
	ls := LocalizedString{"zh-CN": "崩坏：星穹铁道", "en": "Honkai: Star Rail"}
	if got := ls.Get("zh-TW"); got != "Honkai: Star Rail" {
		t.Errorf("Get(zh-TW) with only zh-CN+en = %q, want en fallback", got)
	}
}
