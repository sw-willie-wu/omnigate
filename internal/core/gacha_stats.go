package core

import (
	"math"
	"sort"
)

// BannerPity is one banner's pity progress for the dashboard.
type BannerPity struct {
	Key      string          `json:"key"`
	Label    LocalizedString `json:"label"`
	Current  int             `json:"current"`
	Cap      int             `json:"cap"`
	NearPity bool            `json:"nearPity"` // >80% of cap
}

// HeadlineEntry is one headline-rank pull for the recent list / timeline.
type HeadlineEntry struct {
	Name      string `json:"name"`
	ItemType  string `json:"itemType"`
	BannerKey string `json:"bannerKey"`
	Time      string `json:"time"`
	Count     int    `json:"count"` // pulls spent to land this one
}

// GachaSummary is the full dashboard payload.
type GachaSummary struct {
	Supported      bool            `json:"supported"`
	UID            string          `json:"uid"`
	TotalPulls     int             `json:"totalPulls"`
	PerBanner      map[string]int  `json:"perBanner"`
	SpendEst       int             `json:"spendEst"`
	Currency       string          `json:"currency"`
	HeadlineCnt    int             `json:"headlineCnt"`
	HeadlineByType map[string]int  `json:"headlineByType"`
	AvgPity        float64         `json:"avgPity"`
	ExpectedPity   float64         `json:"expectedPity"`
	LuckScore      int             `json:"luckScore"`
	WinRate5050    *float64        `json:"winRate5050"`
	WorstPull      int             `json:"worstPull"`
	Pity           []BannerPity    `json:"pity"`
	Distribution   []int           `json:"distribution"`
	RecentHeadline []HeadlineEntry `json:"recentHeadline"`
}

// ComputeSummary builds the dashboard from one (uid)'s pulls + config. Pure.
// Ordering uses the monotonic ID (string numeric compare), never parsed Time.
func ComputeSummary(uid string, pulls []GachaPull, cfg GachaConfig) GachaSummary {
	s := GachaSummary{
		Supported: true, UID: uid, Currency: cfg.Currency,
		PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
		Pity: []BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []HeadlineEntry{},
	}
	s.TotalPulls = len(pulls)
	nonFree := 0
	byBanner := map[string][]GachaPull{}
	for _, p := range pulls {
		s.PerBanner[p.BannerKey]++
		if !p.IsFree {
			nonFree++
		}
		if p.Rank == cfg.HeadlineRank {
			s.HeadlineCnt++
			s.HeadlineByType[p.ItemType]++
		}
		byBanner[p.BannerKey] = append(byBanner[p.BannerKey], p)
	}
	s.SpendEst = nonFree * cfg.PullPrice

	var allHits []PityHit
	for _, b := range cfg.Banners {
		group := byBanner[b.Key]
		sortByID(group)
		hits, trailing := b.Pity.Walk(group, cfg.HeadlineRank)
		allHits = append(allHits, hits...)
		near := b.Pity.HardPity() > 0 && trailing*100 >= b.Pity.HardPity()*80
		s.Pity = append(s.Pity, BannerPity{
			Key: b.Key, Label: b.Label, Current: trailing, Cap: b.Pity.HardPity(), NearPity: near,
		})
	}

	sumCount := 0
	for _, h := range allHits {
		sumCount += h.Count
		if h.Count > s.WorstPull {
			s.WorstPull = h.Count
		}
		s.Distribution[bucket(h.Count)]++
	}
	if len(allHits) > 0 {
		s.AvgPity = float64(sumCount) / float64(len(allHits))
	}
	s.ExpectedPity = cfg.ExpectedPity
	s.LuckScore = luckScore(s.AvgPity, cfg.ExpectedPity, len(allHits))

	sort.Slice(allHits, func(i, j int) bool { return numLess(allHits[j].Pull.ID, allHits[i].Pull.ID) })
	for i, h := range allHits {
		if i >= 8 {
			break
		}
		s.RecentHeadline = append(s.RecentHeadline, HeadlineEntry{
			Name: h.Pull.Name, ItemType: h.Pull.ItemType, BannerKey: h.Pull.BannerKey,
			Time: h.Pull.Time, Count: h.Count,
		})
	}
	return s
}

// bucket maps a pull-count to a histogram index: 0=1-9,1=10-19,...,7=70-79,8=80+.
func bucket(count int) int {
	if count >= 80 {
		return 8
	}
	b := count / 10
	if b > 8 {
		b = 8
	}
	return b
}

// luckScore maps avg-pity vs expected to 0-100 (lower avg = luckier = higher).
// Returns 50 (neutral) when there is no data.
func luckScore(avg, expected float64, n int) int {
	if n == 0 || expected <= 0 {
		return 50
	}
	score := 50 + (expected-avg)/expected*100
	return int(math.Max(0, math.Min(100, math.Round(score))))
}

func sortByID(g []GachaPull) {
	sort.Slice(g, func(i, j int) bool { return numLess(g[i].ID, g[j].ID) })
}

// numLess compares numeric-string ids by (length, lexicographic) so longer
// (bigger) numbers sort higher without overflowing int64 on huge snowflake ids.
func numLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
