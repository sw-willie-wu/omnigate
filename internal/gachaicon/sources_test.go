package gachaicon

import (
	"strings"
	"testing"

	"omnigate/internal/core"
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

func TestIconURL_EndfieldPassthrough(t *testing.T) {
	gid := core.GameID("hypergryph/endfield")
	full := "https://static.skport.com/x/aa.png"
	if got := iconURL(gid, "char", full); got != full {
		t.Errorf("iconURL full passthrough = %q want %q", got, full)
	}
	if got := iconURL(gid, "char", "not-a-url"); got != "" {
		t.Errorf("iconURL non-url = %q want empty", got)
	}
}
