package core

import (
	"strconv"
	"testing"
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
