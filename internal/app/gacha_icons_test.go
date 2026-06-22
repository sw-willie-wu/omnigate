package app

import (
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/gachaicon"
)

func TestDecorateGachaIcons_SetsURLOnMatch(t *testing.T) {
	a := newAppForTest(t)
	a.gachaIcons = gachaicon.NewManager(t.TempDir(), nil)
	idx := gachaicon.NewIndex()
	gid := core.GameID("hoyoverse/genshin")
	idx.PutForTest(gid, "綾華", gachaicon.Entry{ID: "10000002", IconRef: "X", Kind: "char"})
	a.gachaIcons.SwapForTest(gid, idx)
	sum := core.GachaSummary{Highlights: []core.HeadlineEntry{
		{Name: "綾華", BannerKey: "character", Rank: 5},
		{Name: "未知角色", BannerKey: "character", Rank: 5},
		{Name: "某武器", BannerKey: "weapon", Rank: 5},
	}}
	a.decorateGachaIcons(gid, &sum)
	if sum.Highlights[0].Icon != "/_asset/hoyoverse/gachaicon/genshin.char.10000002" {
		t.Errorf("icon[0] = %q", sum.Highlights[0].Icon)
	}
	if sum.Highlights[1].Icon != "" {
		t.Errorf("unmatched should be empty, got %q", sum.Highlights[1].Icon)
	}
	// Weapon-banner record with no equip-side entry must also stay empty.
	if sum.Highlights[2].Icon != "" {
		t.Errorf("unresolved weapon should be empty, got %q", sum.Highlights[2].Icon)
	}
}

func TestDecorateGachaIcons_WeaponUsesEquipSide(t *testing.T) {
	a := newAppForTest(t)
	a.gachaIcons = gachaicon.NewManager(t.TempDir(), nil)
	gid := core.GameID("hoyoverse/genshin")
	idx := gachaicon.NewIndex()
	// Weapon-side (equip) entry: a "weapon"-banner record must resolve to it.
	idx.PutEquipForTest(gid, "霧切之回光", gachaicon.Entry{ID: "15502", IconRef: "W", Kind: "weapon"})
	a.gachaIcons.SwapForTest(gid, idx)
	sum := core.GachaSummary{Highlights: []core.HeadlineEntry{
		{Name: "霧切之回光", BannerKey: "weapon", Rank: 5},
	}}
	a.decorateGachaIcons(gid, &sum)
	if sum.Highlights[0].Icon != "/_asset/hoyoverse/gachaicon/genshin.weapon.15502" {
		t.Errorf("weapon icon = %q", sum.Highlights[0].Icon)
	}
}

func TestDecorateGachaIcons_NilManagerNoOp(t *testing.T) {
	a := newAppForTest(t) // gachaIcons nil
	sum := core.GachaSummary{Highlights: []core.HeadlineEntry{{Name: "x", Rank: 5}}}
	a.decorateGachaIcons("hoyoverse/genshin", &sum) // must not panic
	if sum.Highlights[0].Icon != "" {
		t.Errorf("nil manager must leave icon empty")
	}
}
