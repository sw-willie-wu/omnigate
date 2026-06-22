package core

import (
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
		PullPrice:    100, Currency: "NT$", ExpectedPity: 60,
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
