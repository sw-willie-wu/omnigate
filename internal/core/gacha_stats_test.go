package core

import "testing"

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
