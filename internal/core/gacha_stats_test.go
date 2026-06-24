package core

import (
	"reflect"
	"strconv"
	"testing"
	"time"
)

// Guards B1. The new WuWa id "w|<pool>|<time>|<ord>" is non-numeric. The bug is
// CROSS-TIME with mixed ordinal width: an EARLIER timestamp whose headline has a
// 2-digit ordinal (id one char LONGER) vs a LATER timestamp's 1-digit-ordinal
// headline. Old numLess sorts length-first, so the shorter (later) id sorts BEFORE
// the longer (earlier) one → pity bound to the wrong pull. Assert Count↔Time.
func TestComputeSummaryPityOrderCrossTimeMixedWidth(t *testing.T) {
	cfg := GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]LocalizedString{5: {"en": "5★"}},
		Banners:      []BannerConfig{{Key: "character", Label: LocalizedString{"en": "C"}, Pity: stdPity{cap: 80}}},
		PullPrice:    160, Currency: "astrite", ExpectedPity: 62.5,
	}
	const tEarly = "2026-06-13 14:20:00" // headline at 2-digit ordinal 10 (id LONGER)
	const tLate = "2026-06-13 14:30:00"  // headline at 1-digit ordinal 0 (id SHORTER)
	var pulls []GachaPull
	for ord := 0; ord <= 10; ord++ { // 11 same-second records; ord 0-9 = 4★, ord 10 = 5★
		rank := 4
		if ord == 10 {
			rank = 5
		}
		pulls = append(pulls, GachaPull{
			ID: "w|1|" + tEarly + "|" + strconv.Itoa(ord), BannerKey: "character", ItemType: "角色", Rank: rank, Time: tEarly,
		})
	}
	pulls = append(pulls, GachaPull{ID: "w|1|" + tLate + "|0", BannerKey: "character", ItemType: "角色", Rank: 5, Name: "late", Time: tLate})

	s := ComputeSummary("u", pulls, cfg)
	byTime := map[string]int{}
	for _, h := range s.RecentHeadline {
		byTime[h.Time] = h.Count
	}
	if byTime[tEarly] != 11 {
		t.Fatalf("early(%s) count=%d want 11 (chronological pity)", tEarly, byTime[tEarly])
	}
	if byTime[tLate] != 1 {
		t.Fatalf("late(%s) count=%d want 1", tLate, byTime[tLate])
	}
}

// stdPity: every pull counts, reset to 0 on headline (HoYoverse-standard-like).
type stdPity struct{ cap int }

func (m stdPity) HardPity() int { return m.cap }
func (m stdPity) Has5050() bool { return false }
func (m stdPity) Walk(sorted []GachaPull, headline int) ([]PityHit, int) {
	hits, pity := []PityHit{}, 0
	for _, p := range sorted {
		pity++
		if p.Rank == headline {
			hits = append(hits, PityHit{Pull: p, Count: pity})
			pity = 0
		}
	}
	return hits, pity
}

func testConfig() GachaConfig {
	return GachaConfig{
		HeadlineRank: 6,
		RankLabels:   map[int]LocalizedString{6: {"en": "6★"}},
		Banners:      []BannerConfig{{Key: "special", Label: LocalizedString{"en": "Limited"}, Pity: stdPity{cap: 80}}},
		PullPrice:    100, Currency: "NT$", ExpectedPity: 60, ExpectedFeaturedWeapon: 50,
	}
}

func TestComputeSummarySpendStones(t *testing.T) {
	// 10 pulls, 1 free → 9 paid × 160 = 1440 currency consumed; Currency is the code.
	cfg := testConfig()
	cfg.PullPrice = 160
	cfg.Currency = "primogem"
	pulls := make([]GachaPull, 10)
	for i := range pulls {
		pulls[i] = GachaPull{ID: string(rune('a' + i)), BannerKey: "special", Rank: 5}
	}
	pulls[0].IsFree = true
	s := ComputeSummary("u1", pulls, cfg)
	if s.SpendEst != 1440 {
		t.Fatalf("spendEst=%d want 1440 (9 paid × 160)", s.SpendEst)
	}
	if s.Currency != "primogem" {
		t.Fatalf("currency=%q want code primogem", s.Currency)
	}
}

func TestComputeSummaryBasics(t *testing.T) {
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", Rank: 5},
		{ID: "2", BannerKey: "special", Rank: 5},
		{ID: "3", BannerKey: "special", Rank: 5},
		{ID: "4", BannerKey: "special", Rank: 6, Name: "Top1"},
		{ID: "5", BannerKey: "special", Rank: 5},
		{ID: "6", BannerKey: "special", Rank: 6, Name: "Top2"},
	}
	s := ComputeSummary("u1", pulls, testConfig())
	if !s.Supported || s.TotalPulls != 6 {
		t.Fatalf("total=%d supported=%v", s.TotalPulls, s.Supported)
	}
	if s.HeadlineCnt != 2 {
		t.Fatalf("headline=%d want 2", s.HeadlineCnt)
	}
	if s.SpendEst != 600 {
		t.Fatalf("spend=%d want 600", s.SpendEst)
	}
	if s.AvgPity != 3 {
		t.Fatalf("avgPity=%v want 3", s.AvgPity)
	}
	if s.ExpectedPity != 60 {
		t.Fatalf("expectedPity=%v want 60 (from cfg)", s.ExpectedPity)
	}
	if s.ExpectedFeaturedWeapon != 50 {
		t.Fatalf("expectedFeaturedWeapon=%v want 50 (from cfg)", s.ExpectedFeaturedWeapon)
	}
	if s.WorstPull != 4 {
		t.Fatalf("worst=%d want 4", s.WorstPull)
	}
	if len(s.Pity) != 1 || s.Pity[0].Current != 0 || s.Pity[0].Cap != 80 {
		t.Fatalf("pity=%+v", s.Pity)
	}
	if len(s.RecentHeadline) != 2 || s.RecentHeadline[0].Name != "Top2" {
		t.Fatalf("recent=%+v", s.RecentHeadline)
	}
	if s.LuckScore <= 50 {
		t.Fatalf("luck=%d want >50", s.LuckScore)
	}
}

// TestComputeSummaryRecentHeadlineByTime guards the WuWa bug: pull IDs are
// per-pool synthesized ("<pool>-<idx>"), NOT globally comparable, so the recent
// list must order by real time across banners — otherwise the higher-pool-number
// banner (weapon=pool 2) always sorts above the character banner (pool 1) and
// recent characters get pushed off the list.
func TestComputeSummaryRecentHeadlineByTime(t *testing.T) {
	cfg := GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]LocalizedString{5: {"en": "5★"}},
		Banners: []BannerConfig{
			{Key: "character", Label: LocalizedString{"en": "Char"}, Pity: stdPity{cap: 80}},
			{Key: "weapon", Label: LocalizedString{"en": "Weapon"}, Pity: stdPity{cap: 80}},
		},
		PullPrice: 160, Currency: "astrite", ExpectedPity: 62.5,
	}
	pulls := []GachaPull{
		{ID: "1-00000431", BannerKey: "character", ItemType: "角色", Rank: 5, Name: "Danjin", Time: "2026-05-21 11:11:19"},
		{ID: "2-00000160", BannerKey: "weapon", ItemType: "武器", Rank: 5, Name: "Frost", Time: "2026-04-30 10:41:33"},
		{ID: "2-00000229", BannerKey: "weapon", ItemType: "武器", Rank: 5, Name: "Dwarf", Time: "2026-05-22 01:43:53"},
	}
	s := ComputeSummary("u1", pulls, cfg)
	want := []string{"Dwarf", "Danjin", "Frost"} // pure time-desc
	if len(s.RecentHeadline) != len(want) {
		t.Fatalf("recent len=%d want %d (%+v)", len(s.RecentHeadline), len(want), s.RecentHeadline)
	}
	for i, w := range want {
		if s.RecentHeadline[i].Name != w {
			t.Fatalf("recent[%d]=%q want %q (full=%v)", i, s.RecentHeadline[i].Name, w, s.RecentHeadline)
		}
	}
}

// Highlights = ALL top-two-rarity pulls, newest-first, each with its rank + the
// per-rank pity distance (top resets on a top pull; second resets on top-or-second).
// 3★ are excluded.
func TestComputeSummaryHighlights(t *testing.T) {
	cfg := GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]LocalizedString{5: {"en": "5★"}, 4: {"en": "4★"}},
		Banners: []BannerConfig{
			{Key: "character", Label: LocalizedString{"en": "Char"}, Pity: stdPity{cap: 90}},
			{Key: "weapon", Label: LocalizedString{"en": "Weapon"}, Pity: stdPity{cap: 80}},
		},
		PullPrice: 160, Currency: "x", ExpectedPity: 62.5,
	}
	pulls := []GachaPull{
		{ID: "1-1", BannerKey: "character", Rank: 3, Name: "c3a", Time: "2026-05-01 10:00:00"},
		{ID: "1-2", BannerKey: "character", Rank: 4, Name: "c4", Time: "2026-05-02 10:00:00"}, // since2=2
		{ID: "1-3", BannerKey: "character", Rank: 3, Name: "c3b", Time: "2026-05-03 10:00:00"},
		{ID: "1-4", BannerKey: "character", Rank: 5, Name: "c5", Time: "2026-05-04 10:00:00"}, // since1=4
		{ID: "2-1", BannerKey: "weapon", Rank: 4, Name: "w4", Time: "2026-05-05 10:00:00"},    // since2=1
		{ID: "2-2", BannerKey: "weapon", Rank: 5, Name: "w5", Time: "2026-05-06 10:00:00"},    // since1=2
	}
	s := ComputeSummary("u1", pulls, cfg)
	want := []struct {
		name        string
		rank, count int
	}{{"w5", 5, 2}, {"w4", 4, 1}, {"c5", 5, 4}, {"c4", 4, 2}} // newest-first
	if len(s.Highlights) != len(want) {
		t.Fatalf("highlights len=%d want %d (%+v)", len(s.Highlights), len(want), s.Highlights)
	}
	for i, w := range want {
		h := s.Highlights[i]
		if h.Name != w.name || h.Rank != w.rank || h.Count != w.count {
			t.Fatalf("highlights[%d]=%+v want name=%s rank=%d count=%d", i, h, w.name, w.rank, w.count)
		}
	}
}

func TestComputeSummaryFreeExcludedFromSpend(t *testing.T) {
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", Rank: 5, IsFree: true},
		{ID: "2", BannerKey: "special", Rank: 5},
	}
	s := ComputeSummary("u1", pulls, testConfig())
	if s.SpendEst != 100 {
		t.Fatalf("spend=%d want 100", s.SpendEst)
	}
}

func TestComputeSummaryEmpty(t *testing.T) {
	s := ComputeSummary("", nil, testConfig())
	if !s.Supported || s.TotalPulls != 0 || len(s.RecentHeadline) != 0 {
		t.Fatalf("empty summary wrong: %+v", s)
	}
}

func TestComputeSummaryOff(t *testing.T) {
	winStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := time.Date(2024, 1, 21, 23, 59, 59, 0, time.UTC)
	cfg := GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]LocalizedString{5: {"en": "5★"}, 4: {"en": "4★"}},
		Banners: []BannerConfig{
			{Key: "character", Label: LocalizedString{"en": "Char"}, Pity: stdPity{cap: 90}, Limited: true},
			{Key: "standard", Label: LocalizedString{"en": "Std"}, Pity: stdPity{cap: 90}, Limited: false},
		},
		StandardPool: map[string]bool{"Qiqi": true, "七七": true, "Tighnari": true},
		DualCitizens: []DualCitizen{{Names: []string{"Tighnari"}, Start: winStart, End: winEnd}},
		PullPrice:    160, Currency: "x", ExpectedPity: 62.5,
	}
	pulls := []GachaPull{
		{ID: "1-1", BannerKey: "character", Rank: 5, Name: "Qiqi", Time: "2026-02-01 10:00:00"},     // a) limited+std → off
		{ID: "1-2", BannerKey: "character", Rank: 5, Name: "Hutao", Time: "2026-02-02 10:00:00"},    // b) limited+limited → not
		{ID: "2-1", BannerKey: "standard", Rank: 5, Name: "Qiqi", Time: "2026-02-03 10:00:00"},      // c) standard banner → not
		{ID: "1-3", BannerKey: "character", Rank: 5, Name: "Tighnari", Time: "2024-01-10 10:00:00"}, // d) dual-citizen IN window → not
		{ID: "1-4", BannerKey: "character", Rank: 5, Name: "Tighnari", Time: "2026-02-05 10:00:00"}, // d) dual-citizen OUT window → off
		{ID: "1-5", BannerKey: "character", Rank: 4, Name: "Amber", Time: "2026-02-06 10:00:00"},    // e) second-rank, not in pool → not
		{ID: "1-6", BannerKey: "character", Rank: 5, Name: "七七", Time: "2026-02-07 10:00:00"},      // f) cross-language (zh) → off
		{ID: "1-7", BannerKey: "character", Rank: 5, Name: "Tighnari", Time: "not-a-time"},          // g) dual-citizen, unparseable time → fail-safe suppresses off
	}
	s := ComputeSummary("u", pulls, cfg)
	want := map[string]bool{ // name|time → off
		"Qiqi|2026-02-01 10:00:00":     true,
		"Hutao|2026-02-02 10:00:00":    false,
		"Qiqi|2026-02-03 10:00:00":     false,
		"Tighnari|2024-01-10 10:00:00": false,
		"Tighnari|2026-02-05 10:00:00": true,
		"Amber|2026-02-06 10:00:00":    false,
		"七七|2026-02-07 10:00:00":       true,
		"Tighnari|not-a-time":          false, // g) fail-safe: unparseable time suppresses 歪
	}
	got := map[string]bool{}
	for _, h := range s.Highlights {
		got[h.Name+"|"+h.Time] = h.Off
	}
	for k, w := range want {
		v, ok := got[k]
		if !ok {
			t.Errorf("Highlights missing %s", k)
			continue
		}
		if v != w {
			t.Errorf("Highlights Off[%s] = %v; want %v", k, v, w)
		}
	}
	// RecentHeadline uses the same offFor wiring as Highlights; assert it too so a
	// dropped call site at the RecentHeadline append is caught (guards both sites).
	gotRecent := map[string]bool{}
	for _, h := range s.RecentHeadline {
		gotRecent[h.Name+"|"+h.Time] = h.Off
	}
	for k, w := range want {
		if v, ok := gotRecent[k]; ok && v != w {
			t.Errorf("RecentHeadline Off[%s] = %v; want %v", k, v, w)
		}
	}
}

// perPoolConfig: a PerPool "special" limited banner (mirrors Endfield 特許尋訪) plus a
// non-PerPool "standard" banner, sharing a StandardPool for 歪 detection.
func perPoolConfig() GachaConfig {
	return GachaConfig{
		HeadlineRank: 6,
		RankLabels:   map[int]LocalizedString{6: {"zh-TW": "6★"}, 5: {"zh-TW": "5★"}},
		Banners: []BannerConfig{
			{Key: "special", Label: LocalizedString{"zh-TW": "特許尋訪"}, Pity: stdPity{cap: 80}, Limited: true, PerPool: true},
			{Key: "standard", Label: LocalizedString{"zh-TW": "常駐"}, Pity: stdPity{cap: 80}, Limited: false},
		},
		StandardPool: map[string]bool{"Std6": true},
		PullPrice:    100, Currency: "NT$", ExpectedPity: 60,
	}
}

// findPity returns the BannerPity with the given key, or nil.
func findPity(s GachaSummary, key string) *BannerPity {
	for i := range s.Pity {
		if s.Pity[i].Key == key {
			return &s.Pity[i]
		}
	}
	return nil
}

// Tests 1+2+3: independent per-期 pity, newest-期-first ordering, and composite-key
// consistency across PerBanner / Pity.Key / HeadlineEntry.BannerKey.
func TestComputeSummaryPerPoolIndependentPity(t *testing.T) {
	cfg := perPoolConfig()
	// Two 期 (A older, B newer), interleaved in input order. Each期 has its own 6★.
	// A: 3 pulls, 6★ at the 2nd pull, then 1 trailing → A.Current = 1.
	// B: 4 pulls, 6★ at the 3rd pull, then 1 trailing → B.Current = 1.
	pulls := []GachaPull{
		{ID: "10", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a1", Time: "2026-01-01 10:00:00"},
		{ID: "11", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "A6", Time: "2026-01-01 10:00:01"},
		{ID: "20", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b1", Time: "2026-03-01 10:00:00"},
		{ID: "12", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a3", Time: "2026-01-01 10:00:02"},
		{ID: "21", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b2", Time: "2026-03-01 10:00:01"},
		{ID: "22", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 6, Name: "B6", Time: "2026-03-01 10:00:02"},
		{ID: "23", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b4", Time: "2026-03-01 10:00:03"},
	}
	s := ComputeSummary("u", pulls, cfg)

	// Test 1: TWO independent Pity entries keyed special:A and special:B.
	pa := findPity(s, "special:A")
	pb := findPity(s, "special:B")
	if pa == nil || pb == nil {
		t.Fatalf("want pity keys special:A and special:B, got %+v", s.Pity)
	}
	// Independent trailing: B's 6★ did NOT touch A and vice-versa.
	if pa.Current != 1 {
		t.Errorf("special:A Current=%d want 1 (own trailing only)", pa.Current)
	}
	if pb.Current != 1 {
		t.Errorf("special:B Current=%d want 1 (own trailing only)", pb.Current)
	}
	// Labels via composeLabel.
	if pa.Label["zh-TW"] != "特許尋訪 - 期A" {
		t.Errorf("special:A label=%q want %q", pa.Label["zh-TW"], "特許尋訪 - 期A")
	}
	if pb.Label["zh-TW"] != "特許尋訪 - 期B" {
		t.Errorf("special:B label=%q want %q", pb.Label["zh-TW"], "特許尋訪 - 期B")
	}

	// Test 2: newer 期 B appears BEFORE older 期 A in s.Pity.
	idxA, idxB := -1, -1
	for i, p := range s.Pity {
		if p.Key == "special:A" {
			idxA = i
		}
		if p.Key == "special:B" {
			idxB = i
		}
	}
	if !(idxB < idxA) {
		t.Errorf("want 期B (idx %d) before 期A (idx %d) — newest-first", idxB, idxA)
	}

	// Test 3: composite keys consistent across PerBanner.
	if s.PerBanner["special:A"] != 3 || s.PerBanner["special:B"] != 4 {
		t.Errorf("PerBanner composite=%+v want special:A=3 special:B=4", s.PerBanner)
	}
	if _, bare := s.PerBanner["special"]; bare {
		t.Errorf("PerBanner has bare 'special' key; want only composites: %+v", s.PerBanner)
	}
	// HeadlineEntry.BannerKey for the 6★ pulls == matching composite, Limited still true.
	wantKey := map[string]string{"A6": "special:A", "B6": "special:B"}
	for _, h := range s.Highlights {
		if h.Rank != 6 {
			continue
		}
		if wk, ok := wantKey[h.Name]; ok {
			if h.BannerKey != wk {
				t.Errorf("Highlights[%s].BannerKey=%q want %q", h.Name, h.BannerKey, wk)
			}
			if !h.Limited {
				t.Errorf("Highlights[%s].Limited=false want true", h.Name)
			}
		}
	}
	for _, h := range s.RecentHeadline {
		if wk, ok := wantKey[h.Name]; ok && h.BannerKey != wk {
			t.Errorf("RecentHeadline[%s].BannerKey=%q want %q", h.Name, h.BannerKey, wk)
		}
	}
}

// Test 4: 歪 preserved — a StandardPool name on the PerPool special banner is Off==true
// even though the entry's BannerKey is composite (offFor uses the RAW key).
func TestComputeSummaryPerPoolOffPreserved(t *testing.T) {
	cfg := perPoolConfig()
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "Std6", Time: "2026-01-01 10:00:00"}, // standard on limited → 歪
		{ID: "2", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "Lim6", Time: "2026-01-01 10:00:01"}, // featured → not 歪
	}
	s := ComputeSummary("u", pulls, cfg)
	got := map[string]HeadlineEntry{}
	for _, h := range s.Highlights {
		got[h.Name] = h
	}
	if !got["Std6"].Off {
		t.Errorf("Std6 Off=false want true (歪 on composite banner)")
	}
	if got["Std6"].BannerKey != "special:A" {
		t.Errorf("Std6 BannerKey=%q want special:A", got["Std6"].BannerKey)
	}
	if got["Lim6"].Off {
		t.Errorf("Lim6 Off=true want false")
	}
}

// Test 5: empty-PoolID pulls collapse to ONE 'special' Pity entry and sort LAST.
func TestComputeSummaryPerPoolEmptyFallback(t *testing.T) {
	cfg := perPoolConfig()
	pulls := []GachaPull{
		// real 期 B (newer)
		{ID: "20", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b1", Time: "2026-03-01 10:00:00"},
		{ID: "21", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 6, Name: "B6", Time: "2026-03-01 10:00:01"},
		// empty PoolID (un-backfilled) — newer in time but must sort LAST
		{ID: "30", BannerKey: "special", PoolID: "", Rank: 5, Name: "e1", Time: "2026-05-01 10:00:00"},
		{ID: "31", BannerKey: "special", PoolID: "", Rank: 6, Name: "E6", Time: "2026-05-01 10:00:01"},
	}
	s := ComputeSummary("u", pulls, cfg)
	pe := findPity(s, "special")
	if pe == nil {
		t.Fatalf("want fallback pity key 'special', got %+v", s.Pity)
	}
	if pe.Label["zh-TW"] != "特許尋訪" {
		t.Errorf("fallback label=%q want plain 特許尋訪", pe.Label["zh-TW"])
	}
	// fallback sorts LAST among special subs.
	idxB, idxFallback := -1, -1
	for i, p := range s.Pity {
		if p.Key == "special:B" {
			idxB = i
		}
		if p.Key == "special" {
			idxFallback = i
		}
	}
	if !(idxB >= 0 && idxFallback > idxB) {
		t.Errorf("fallback 'special' (idx %d) must sort after special:B (idx %d)", idxFallback, idxB)
	}
	// empty-PoolID headline keeps the bare key.
	for _, h := range s.Highlights {
		if h.Name == "E6" && h.BannerKey != "special" {
			t.Errorf("E6 BannerKey=%q want bare 'special'", h.BannerKey)
		}
	}
}

func TestComputeSummaryLimitedFlag(t *testing.T) {
	cfg := GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]LocalizedString{5: {"en": "5★"}, 4: {"en": "4★"}},
		Banners: []BannerConfig{
			{Key: "character", Label: LocalizedString{"en": "Char"}, Pity: stdPity{cap: 90}, Limited: true},
			{Key: "standard", Label: LocalizedString{"en": "Std"}, Pity: stdPity{cap: 90}, Limited: false},
		},
		StandardPool: map[string]bool{},
		PullPrice:    160, Currency: "x", ExpectedPity: 62.5,
	}
	pulls := []GachaPull{
		{ID: "1-1", BannerKey: "character", Rank: 5, Name: "A", Time: "2026-02-01 10:00:00"},
		{ID: "2-1", BannerKey: "standard", Rank: 5, Name: "B", Time: "2026-02-02 10:00:00"},
	}
	s := ComputeSummary("u", pulls, cfg)
	want := map[string]bool{"A": true, "B": false}
	if len(s.Highlights) != 2 {
		t.Fatalf("want 2 highlights, got %d", len(s.Highlights))
	}
	for _, h := range s.Highlights {
		if h.Limited != want[h.Name] {
			t.Errorf("Highlights Limited[%s] = %v; want %v", h.Name, h.Limited, want[h.Name])
		}
	}
	// Guard the RecentHeadline call site too (mirrors TestComputeSummaryOff): a dropped
	// limited arg on that append would otherwise go uncaught.
	gotRecent := map[string]bool{}
	for _, h := range s.RecentHeadline {
		gotRecent[h.Name] = h.Limited
	}
	for name, w := range want {
		if v, ok := gotRecent[name]; ok && v != w {
			t.Errorf("RecentHeadline Limited[%s] = %v; want %v", name, v, w)
		}
	}
}

// crossPoolConfig: perPoolConfig + special.CrossPoolBar=true + a non-CrossPoolBar
// PerPool "weapon" banner (cap 40, mirrors 武庫申領).
func crossPoolConfig() GachaConfig {
	cfg := perPoolConfig()
	cfg.Banners[0].CrossPoolBar = true // special
	cfg.Banners = append(cfg.Banners, BannerConfig{
		Key: "weapon", Label: LocalizedString{"zh-TW": "武庫申領"},
		Pity: stdPity{cap: 40}, Limited: true, PerPool: true, CrossPoolBar: false,
	})
	return cfg
}

// Aggregate top bar: bare "special" reflects cross-pool trailing, distinct from per-期 subs.
func TestComputeSummaryCrossPoolAggregateBar(t *testing.T) {
	cfg := crossPoolConfig()
	// 期A(older): a1,A6(6★),a3 → A trailing=1. 期B(newer): b1..b4 no 6★ → B trailing=4.
	// Cross-pool chronological: a1,A6,a3,b1,b2,b3,b4 → trailing after A6 = 5.
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a1", Time: "2026-01-01 10:00:00"},
		{ID: "2", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "A6", Time: "2026-01-01 10:00:01"},
		{ID: "3", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a3", Time: "2026-01-01 10:00:02"},
		{ID: "4", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b1", Time: "2026-03-01 10:00:00"},
		{ID: "5", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b2", Time: "2026-03-01 10:00:01"},
		{ID: "6", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b3", Time: "2026-03-01 10:00:02"},
		{ID: "7", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b4", Time: "2026-03-01 10:00:03"},
	}
	s := ComputeSummary("u", pulls, cfg)
	bare := findPity(s, "special")
	if bare == nil {
		t.Fatalf("want aggregate bare 'special' pity, got %+v", s.Pity)
	}
	if bare.Current != 5 || bare.Cap != 80 {
		t.Errorf("aggregate special Current/Cap = %d/%d want 5/80", bare.Current, bare.Cap)
	}
	if pa := findPity(s, "special:A"); pa == nil || pa.Current != 1 {
		t.Errorf("special:A = %+v want Current 1", pa)
	}
	if pb := findPity(s, "special:B"); pb == nil || pb.Current != 4 {
		t.Errorf("special:B = %+v want Current 4", pb)
	}
	idxBare, idxA, idxB := -1, -1, -1
	for i, p := range s.Pity {
		switch p.Key {
		case "special":
			idxBare = i
		case "special:A":
			idxA = i
		case "special:B":
			idxB = i
		}
	}
	if !(idxBare >= 0 && idxBare < idxA && idxBare < idxB) {
		t.Errorf("bare aggregate (idx %d) must precede subs A(%d)/B(%d)", idxBare, idxA, idxB)
	}
}

// Aggregate is display-only: turning CrossPoolBar on must NOT change any allHits-derived
// stat, nor the highlight/recent lists.
func TestComputeSummaryCrossPoolBarDoesNotChangeStats(t *testing.T) {
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a1", Time: "2026-01-01 10:00:00"},
		{ID: "2", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "A6", Time: "2026-01-01 10:00:01"},
		{ID: "3", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b1", Time: "2026-03-01 10:00:00"},
		{ID: "4", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 6, Name: "B6", Time: "2026-03-01 10:00:01"},
	}
	base := perPoolConfig()
	cross := perPoolConfig()
	cross.Banners[0].CrossPoolBar = true
	s1 := ComputeSummary("u", pulls, base)
	s2 := ComputeSummary("u", pulls, cross)
	if s1.AvgPity != s2.AvgPity || s1.WorstPull != s2.WorstPull || s1.HeadlineCnt != s2.HeadlineCnt {
		t.Errorf("stats changed: avg %v/%v worst %d/%d cnt %d/%d", s1.AvgPity, s2.AvgPity, s1.WorstPull, s2.WorstPull, s1.HeadlineCnt, s2.HeadlineCnt)
	}
	if !reflect.DeepEqual(s1.Distribution, s2.Distribution) {
		t.Errorf("distribution changed: %v vs %v", s1.Distribution, s2.Distribution)
	}
	if len(s1.Highlights) != len(s2.Highlights) || len(s1.RecentHeadline) != len(s2.RecentHeadline) {
		t.Errorf("highlight counts changed: hl %d/%d recent %d/%d", len(s1.Highlights), len(s2.Highlights), len(s1.RecentHeadline), len(s2.RecentHeadline))
	}
}

// weapon is PerPool but NOT CrossPoolBar: per-期 subs at cap 40, no bare aggregate.
func TestComputeSummaryWeaponPerPoolCap40NoBare(t *testing.T) {
	cfg := crossPoolConfig()
	pulls := []GachaPull{
		{ID: "w1", BannerKey: "weapon", PoolID: "X", PoolName: "期X申領", ItemType: "weapon", Rank: 5, Name: "wx1", Time: "2026-01-01 10:00:00"},
		{ID: "w2", BannerKey: "weapon", PoolID: "X", PoolName: "期X申領", ItemType: "weapon", Rank: 6, Name: "WX6", Time: "2026-01-01 10:00:01"},
		{ID: "w3", BannerKey: "weapon", PoolID: "Y", PoolName: "期Y申領", ItemType: "weapon", Rank: 5, Name: "wy1", Time: "2026-03-01 10:00:00"},
	}
	s := ComputeSummary("u", pulls, cfg)
	wx := findPity(s, "weapon:X")
	wy := findPity(s, "weapon:Y")
	if wx == nil || wx.Cap != 40 || wx.Current != 0 {
		t.Errorf("weapon:X = %+v want Cap 40 Current 0", wx)
	}
	if wy == nil || wy.Cap != 40 || wy.Current != 1 {
		t.Errorf("weapon:Y = %+v want Cap 40 Current 1", wy)
	}
	if findPity(s, "weapon") != nil {
		t.Errorf("weapon must have NO bare aggregate (CrossPoolBar=false): %+v", s.Pity)
	}
}

// Under CrossPoolBar, an empty-poolId fallback is folded into the aggregate: exactly ONE
// bare "special" pity row, and empty-poolId records still appear in Highlights.
func TestComputeSummaryCrossPoolFallbackFolded(t *testing.T) {
	cfg := crossPoolConfig()
	pulls := []GachaPull{
		{ID: "20", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 5, Name: "b1", Time: "2026-03-01 10:00:00"},
		{ID: "21", BannerKey: "special", PoolID: "B", PoolName: "期B", Rank: 6, Name: "B6", Time: "2026-03-01 10:00:01"},
		{ID: "30", BannerKey: "special", PoolID: "", Rank: 5, Name: "e1", Time: "2026-05-01 10:00:00"},
		{ID: "31", BannerKey: "special", PoolID: "", Rank: 6, Name: "E6", Time: "2026-05-01 10:00:01"},
	}
	s := ComputeSummary("u", pulls, cfg)
	bareCount := 0
	for _, p := range s.Pity {
		if p.Key == "special" {
			bareCount++
		}
	}
	if bareCount != 1 {
		t.Errorf("want exactly ONE bare 'special' pity (aggregate, fallback folded), got %d: %+v", bareCount, s.Pity)
	}
	found := false
	for _, h := range s.Highlights {
		if h.Name == "E6" {
			found = true
		}
	}
	if !found {
		t.Errorf("E6 (empty-poolId) record missing from Highlights")
	}
}

// Cross-pool aggregate ignores free pulls entirely: a free 6★ does NOT reset the top bar.
func TestComputeSummaryCrossPoolAggregateExcludesFree(t *testing.T) {
	cfg := crossPoolConfig()
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a1", Time: "2026-01-01 10:00:00"},
		{ID: "2", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a2", Time: "2026-01-01 10:00:01"},
		{ID: "3", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a3", Time: "2026-01-01 10:00:02"},
		{ID: "4", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "伊馮", IsFree: true, Time: "2026-01-01 10:00:03"},
		{ID: "5", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a5", Time: "2026-01-01 10:00:04"},
		{ID: "6", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a6", Time: "2026-01-01 10:00:05"},
	}
	s := ComputeSummary("u", pulls, cfg)
	bare := findPity(s, "special")
	if bare == nil || bare.Current != 5 {
		t.Fatalf("aggregate special = %+v want Current 5 (free 6★ filtered → doesn't reset cross-pool)", bare)
	}
}

// Per-record "count" excludes free pulls: a free 6★ records the paid-only pity before it.
func TestComputeSummaryFreePullRecordCountExcludesFree(t *testing.T) {
	cfg := crossPoolConfig()
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a1", Time: "2026-01-01 10:00:00"},
		{ID: "2", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a2", Time: "2026-01-01 10:00:01"},
		{ID: "3", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "a3", Time: "2026-01-01 10:00:02"},
		{ID: "4", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 5, Name: "f1", IsFree: true, Time: "2026-01-01 10:00:03"},
		{ID: "5", BannerKey: "special", PoolID: "A", PoolName: "期A", Rank: 6, Name: "伊馮", IsFree: true, Time: "2026-01-01 10:00:04"},
	}
	s := ComputeSummary("u", pulls, cfg)
	var yvon *HeadlineEntry
	for i := range s.Highlights {
		if s.Highlights[i].Name == "伊馮" {
			yvon = &s.Highlights[i]
		}
	}
	if yvon == nil || yvon.Count != 3 {
		t.Fatalf("伊馮 highlight = %+v want Count 3 (3 paid; 1 free non-6★ skipped)", yvon)
	}
}
