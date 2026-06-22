package gachaicon

import (
	"os"
	"testing"

	"omnigate/internal/core"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildAmber_TopRarityOnly_AllLangKeys(t *testing.T) {
	idx := NewIndex()
	if err := buildFromAmber(idx, "char", 5,
		map[string][]byte{
			"cht": readFixture(t, "amber_avatar.json"),
			"chs": readFixture(t, "amber_avatar.json"),
			"en":  readFixture(t, "amber_avatar.json"),
		}); err != nil {
		t.Fatal(err)
	}
	if e, ok := idx.Resolve(core.GameID("hoyoverse/genshin"), "神里綾華", false); !ok || e.ID != "10000002" || e.IconRef != "UI_AvatarIcon_Ayaka" {
		t.Errorf("resolve 神里綾華 = %+v ok=%v", e, ok)
	}
	if _, ok := idx.Resolve(core.GameID("hoyoverse/genshin"), "砂糖", false); ok {
		t.Errorf("rank-4 砂糖 should be excluded")
	}
}

func TestResolve_T2SBridge_ForHakushGames(t *testing.T) {
	idx := NewIndex()
	idx.put(core.GameID("kurogames/wutheringwaves"), "今汐", Entry{ID: "1404", IconRef: "T_IconRoleHead256_1404", Kind: "char"})
	if e, ok := idx.Resolve(core.GameID("kurogames/wutheringwaves"), "今汐", false); !ok || e.ID != "1404" {
		t.Errorf("simplified direct = %+v ok=%v", e, ok)
	}
	idx.put(core.GameID("kurogames/wutheringwaves"), "维里奈", Entry{ID: "1102", IconRef: "x", Kind: "char"})
	if e, ok := idx.Resolve(core.GameID("kurogames/wutheringwaves"), "維里奈", false); !ok || e.ID != "1102" {
		t.Errorf("traditional via t2s = %+v ok=%v", e, ok)
	}
}

func TestResolve_EquipDisambiguates(t *testing.T) {
	idx := NewIndex()
	gid := core.GameID("hoyoverse/genshin")
	idx.put(gid, "同名", Entry{ID: "C", IconRef: "c", Kind: "char"})
	idx.putEquip(gid, "同名", Entry{ID: "W", IconRef: "w", Kind: "weapon"})
	if e, _ := idx.Resolve(gid, "同名", false); e.ID != "C" {
		t.Errorf("char side = %+v", e)
	}
	if e, _ := idx.Resolve(gid, "同名", true); e.ID != "W" {
		t.Errorf("weapon side = %+v", e)
	}
}

func TestBuildFromAmber_WeaponPath(t *testing.T) {
	idx := NewIndex()
	if err := buildFromAmber(idx, "weapon", 5,
		map[string][]byte{"cht": readFixture(t, "amber_weapon.json")}); err != nil {
		t.Fatal(err)
	}
	if e, ok := idx.Resolve(core.GameID("hoyoverse/genshin"), "霧切之回光", true); !ok ||
		e.ID != "11509" || e.IconRef != "UI_EquipIcon_Sword_Narukami" || e.Kind != "weapon" {
		t.Errorf("resolve 霧切之回光 = %+v ok=%v", e, ok)
	}
}

func TestBuildFromYatta_CharAndEquip(t *testing.T) {
	idx := NewIndex()
	if err := buildFromYatta(idx, "char", 5,
		map[string][]byte{"cht": readFixture(t, "yatta_avatar.json")}); err != nil {
		t.Fatal(err)
	}
	if err := buildFromYatta(idx, "weapon", 5,
		map[string][]byte{"cht": readFixture(t, "yatta_equipment.json")}); err != nil {
		t.Fatal(err)
	}
	gid := core.GameID("hoyoverse/starrail")
	if e, ok := idx.Resolve(gid, "刃", false); !ok || e.ID != "1212" {
		t.Errorf("resolve 刃 = %+v ok=%v", e, ok)
	}
	if _, ok := idx.Resolve(gid, "三月七", false); ok {
		t.Errorf("rank-4 三月七 should be excluded")
	}
	if e, ok := idx.Resolve(gid, "鋒鏑", true); !ok || e.ID != "23000" || e.Kind != "weapon" {
		t.Errorf("resolve 鋒鏑 = %+v ok=%v", e, ok)
	}
}

func TestEntryByID(t *testing.T) {
	idx := NewIndex()
	if err := buildFromAmber(idx, "char", 5,
		map[string][]byte{"cht": readFixture(t, "amber_avatar.json")}); err != nil {
		t.Fatal(err)
	}
	gid := core.GameID("hoyoverse/genshin")
	if e, ok := idx.EntryByID(gid, "10000002"); !ok || e.IconRef != "UI_AvatarIcon_Ayaka" {
		t.Errorf("EntryByID known = %+v ok=%v", e, ok)
	}
	if _, ok := idx.EntryByID(gid, "nope"); ok {
		t.Errorf("EntryByID unknown should be false")
	}
}
