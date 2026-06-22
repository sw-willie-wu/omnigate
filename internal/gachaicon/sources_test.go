package gachaicon

import (
	"strings"
	"testing"
)

func TestIconURL_HakushGames(t *testing.T) {
	// realistic refs from the live fixtures (testdata/hakush_ww_character.json, hakush_zzz_character.json)
	wuwaRef := "/Game/Aki/UI/UIResources/Common/Image/IconRoleHead256/T_IconRoleHead256_14_UI.T_IconRoleHead256_14_UI"
	if got := wuwaIconURL("char", wuwaRef); got == "" || !strings.HasPrefix(got, "https://") {
		t.Errorf("wuwaIconURL=%q", got)
	}
	if got := zzzIconURL("char", "IconRole01"); got == "" || !strings.HasPrefix(got, "https://") {
		t.Errorf("zzzIconURL=%q", got)
	}
}
