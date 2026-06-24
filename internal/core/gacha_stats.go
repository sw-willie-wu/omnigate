package core

import (
	"math"
	"sort"
	"time"
)

// BannerPity is one banner's pity progress for the dashboard.
type BannerPity struct {
	Key      string          `json:"key"`
	Label    LocalizedString `json:"label"`
	Current  int             `json:"current"`
	Cap      int             `json:"cap"`
	NearPity bool            `json:"nearPity"` // >80% of cap
}

// HeadlineEntry is one high-rarity pull for the recent/headline lists. Count is
// the per-rank pity distance (pulls since the previous pull of this rank-or-higher
// for the top rank; since the previous top-or-second-rank pull for the second).
type HeadlineEntry struct {
	Name      string `json:"name"`
	ItemType  string `json:"itemType"`
	BannerKey string `json:"bannerKey"`
	Time      string `json:"time"`
	Count     int    `json:"count"`   // pulls spent to land this one
	Rank      int    `json:"rank"`    // the pull's rarity (e.g. 4 or 5; Endfield 5 or 6)
	Off       bool   `json:"off"`     // lost the 50/50 (歪): a standard-pool item on a limited banner
	Limited   bool   `json:"limited"` // pulled on a Limited (featured/collab) banner
	Icon      string `json:"icon"`    // /_asset/... icon URL; set by the app-layer decorator (core stays pure → ""), "" when unresolved
}

// GachaSummary is the full dashboard payload.
type GachaSummary struct {
	Supported              bool            `json:"supported"`
	UID                    string          `json:"uid"`
	ActiveUnknown          bool            `json:"activeUnknown"` // switcher-only: active account uid not yet known (play-first)
	TotalPulls             int             `json:"totalPulls"`
	PerBanner              map[string]int  `json:"perBanner"`
	SpendEst               int             `json:"spendEst"`
	Currency               string          `json:"currency"`
	HeadlineCnt            int             `json:"headlineCnt"`
	HeadlineByType         map[string]int  `json:"headlineByType"`
	AvgPity                float64         `json:"avgPity"`
	ExpectedPity           float64         `json:"expectedPity"`
	ExpectedFeaturedWeapon float64         `json:"expectedFeaturedWeapon"` // 出限定武器期望; 0 = unknown (no card-8 note)
	LuckScore              int             `json:"luckScore"`
	WinRate5050            *float64        `json:"winRate5050"`
	WorstPull              int             `json:"worstPull"`
	Pity                   []BannerPity    `json:"pity"`
	Distribution           []int           `json:"distribution"`
	RecentHeadline         []HeadlineEntry `json:"recentHeadline"` // recent top-rank only (cap 8)
	Highlights             []HeadlineEntry `json:"highlights"`     // ALL top-two-rarity pulls, newest-first
}

// ComputeSummary builds the dashboard from one (uid)'s pulls + config. Pure.
// Per-banner pity ordering is chronological (parsed Time, id tiebreak); the
// cross-banner recent-headline list also uses parsed Time. IDs are not globally
// comparable across banners/providers (WuWa synthesizes them) so Time is the
// source of truth — see the sorts below.
func ComputeSummary(uid string, pulls []GachaPull, cfg GachaConfig) GachaSummary {
	s := GachaSummary{
		Supported: true, UID: uid, Currency: cfg.Currency,
		PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
		Pity: []BannerPity{}, Distribution: make([]int, 9),
		RecentHeadline: []HeadlineEntry{}, Highlights: []HeadlineEntry{},
	}
	s.TotalPulls = len(pulls)
	nonFree := 0
	byBanner := map[string][]GachaPull{}
	for _, p := range pulls {
		s.PerBanner[outBannerKey(cfg, p)]++
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
		// byBanner is keyed by the RAW bannerKey; the per-期 split (independent pity
		// for a PerPool banner) happens inside bannerSubGroups. Each sub sorts itself.
		for _, sub := range bannerSubGroups(b, byBanner[b.Key]) {
			hits, trailing := b.Pity.Walk(sub.pulls, cfg.HeadlineRank)
			allHits = append(allHits, hits...)
			near := b.Pity.HardPity() > 0 && trailing*100 >= b.Pity.HardPity()*80
			s.Pity = append(s.Pity, BannerPity{
				Key: sub.key, Label: sub.label, Current: trailing, Cap: b.Pity.HardPity(), NearPity: near,
			})
		}
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
	s.ExpectedFeaturedWeapon = cfg.ExpectedFeaturedWeapon
	s.LuckScore = luckScore(s.AvgPity, cfg.ExpectedPity, len(allHits))

	// limited maps banner-key → Limited, the per-banner lookup for the 歪 (50/50-loss)
	// marker (offFor); built once here and reused by both headline loops below.
	limited := make(map[string]bool, len(cfg.Banners))
	for _, b := range cfg.Banners {
		limited[b.Key] = b.Limited
	}

	// Recent-headline ordering is CROSS-banner, so it must use real time, not the
	// per-banner ID. HoYoverse IDs are a global increasing sequence (ID order ==
	// time order), but WuWa/kurogames synthesizes IDs as "w|<pool>|<time>|<ord>"
	// which are not comparable across banners — sorting those by ID surfaces whole
	// pools at a time, hiding recent characters. Sort by parsed Time desc; fall back
	// to the ID compare when times tie or don't parse (preserves HoYoverse
	// same-second 10-pull order and any odd format).
	sort.SliceStable(allHits, func(i, j int) bool { return headlineNewer(allHits[i], allHits[j]) })
	for i, h := range allHits {
		if i >= 8 {
			break
		}
		s.RecentHeadline = append(s.RecentHeadline, headlineEntry(cfg, h, offFor(cfg, limited, h.Pull), limited[h.Pull.BannerKey]))
	}

	// Highlights: ALL top-two-rarity pulls (top = HeadlineRank, second = one below)
	// with per-rank pity counts, newest-first — feeds the full high-star board where
	// the user toggles which ranks to show. since1 (top) resets only on a top pull;
	// since2 (second) resets on a top OR second pull (a top pull also satisfies the
	// second-rank guarantee). Pulls below the second rank are skipped.
	r1, r2 := cfg.HeadlineRank, cfg.HeadlineRank-1
	var hlHits []PityHit
	for _, b := range cfg.Banners {
		// Per-期 independent: since1/since2 reset within each sub (the sub owns its sort).
		for _, sub := range bannerSubGroups(b, byBanner[b.Key]) {
			since1, since2 := 0, 0
			for _, p := range sub.pulls {
				since1++
				since2++
				switch {
				case p.Rank >= r1:
					hlHits = append(hlHits, PityHit{Pull: p, Count: since1})
					since1, since2 = 0, 0
				case p.Rank >= r2:
					hlHits = append(hlHits, PityHit{Pull: p, Count: since2})
					since2 = 0
				}
			}
		}
	}
	sort.SliceStable(hlHits, func(i, j int) bool { return headlineNewer(hlHits[i], hlHits[j]) })
	for _, h := range hlHits {
		s.Highlights = append(s.Highlights, headlineEntry(cfg, h, offFor(cfg, limited, h.Pull), limited[h.Pull.BannerKey]))
	}
	return s
}

// headlineNewer reports whether hit a is more recent than b: by parsed Time desc,
// falling back to ID compare when times tie or don't parse (HoYo IDs are a global
// increasing sequence; WuWa synthesizes per-pool IDs, so Time is the source of truth).
func headlineNewer(a, b PityHit) bool {
	ti, oki := parseGachaTime(a.Pull.Time)
	tj, okj := parseGachaTime(b.Pull.Time)
	if oki && okj && !ti.Equal(tj) {
		return ti.After(tj)
	}
	return numLess(b.Pull.ID, a.Pull.ID)
}

func headlineEntry(cfg GachaConfig, h PityHit, off bool, lim bool) HeadlineEntry {
	return HeadlineEntry{
		Name: h.Pull.Name, ItemType: h.Pull.ItemType, BannerKey: outBannerKey(cfg, h.Pull),
		Time: h.Pull.Time, Count: h.Count, Rank: h.Pull.Rank, Off: off, Limited: lim,
	}
}

// composeLabel prefixes each locale of base with the 期 name: "特許尋訪" + " - " + poolName.
func composeLabel(base LocalizedString, poolName string) LocalizedString {
	out := make(LocalizedString, len(base))
	for loc, v := range base {
		out[loc] = v + " - " + poolName
	}
	return out
}

// outBannerKey is the dashboard output key for a pull: the composite
// "<bannerKey>:<poolId>" when its banner is PerPool and it has a poolId, else the
// raw BannerKey. Kept consistent across PerBanner / Pity[].Key / HeadlineEntry.BannerKey
// so the frontend groups records under the matching pity entry.
func outBannerKey(cfg GachaConfig, p GachaPull) string {
	if b := cfg.BannerOf(p.BannerKey); b != nil && b.PerPool && p.PoolID != "" {
		return p.BannerKey + ":" + p.PoolID
	}
	return p.BannerKey
}

// bannerSub is one independent-pity sub-group of a banner.
type bannerSub struct {
	key   string
	label LocalizedString
	pulls []GachaPull // sorted chronological (oldest-first)
}

// bannerSubGroups splits a banner's pulls into independent-pity sub-groups: one per
// poolId (newest-期 first) when b.PerPool, else the single whole group. Each sub is
// sorted chronological internally. Pulls with empty PoolID collapse into a single
// fallback sub keyed by the raw b.Key (shown under plain 特許尋訪 until backfilled),
// sorted LAST.
func bannerSubGroups(b BannerConfig, group []GachaPull) []bannerSub {
	if !b.PerPool {
		g := append([]GachaPull(nil), group...)
		sortChronological(g)
		return []bannerSub{{key: b.Key, label: b.Label, pulls: g}}
	}
	// Bucket by poolId; empty poolId collapses into a single fallback bucket.
	type bucketT struct {
		poolID, poolName string
		pulls            []GachaPull
	}
	order := []string{}            // poolIDs in first-seen order, to stabilize before sort
	buckets := map[string]*bucketT{}
	for _, p := range group {
		bk, ok := buckets[p.PoolID]
		if !ok {
			bk = &bucketT{poolID: p.PoolID, poolName: p.PoolName}
			buckets[p.PoolID] = bk
			order = append(order, p.PoolID)
		}
		bk.pulls = append(bk.pulls, p)
	}
	var nonEmpty []*bucketT
	var fallback *bucketT
	for _, id := range order {
		bk := buckets[id]
		sortChronological(bk.pulls)
		if id == "" {
			fallback = bk
		} else {
			nonEmpty = append(nonEmpty, bk)
		}
	}
	// Non-empty buckets newest-期 first: compare by most-recent pull (already sorted
	// chronological → last element), parsed Time desc with numLess(id) tiebreak.
	sort.SliceStable(nonEmpty, func(i, j int) bool {
		ai := nonEmpty[i].pulls[len(nonEmpty[i].pulls)-1]
		aj := nonEmpty[j].pulls[len(nonEmpty[j].pulls)-1]
		return headlineNewer(PityHit{Pull: ai}, PityHit{Pull: aj})
	})
	subs := make([]bannerSub, 0, len(nonEmpty)+1)
	for _, bk := range nonEmpty {
		subs = append(subs, bannerSub{
			key:   b.Key + ":" + bk.poolID,
			label: composeLabel(b.Label, bk.poolName),
			pulls: bk.pulls,
		})
	}
	if fallback != nil {
		subs = append(subs, bannerSub{key: b.Key, label: b.Label, pulls: fallback.pulls})
	}
	return subs
}

// offFor reports whether a pull lost the 50/50: a standard-pool item on a limited
// banner, excluding a dual-citizen pulled inside its debut up-window.
func offFor(cfg GachaConfig, limited map[string]bool, p GachaPull) bool {
	return limited[p.BannerKey] && cfg.StandardPool[p.Name] && !inDebutWindow(cfg, p)
}

// inDebutWindow: true iff p is a dual-citizen pulled inside its debut window. On a
// time-parse failure it returns true (fail-safe: suppress 歪 on a likely debut win).
func inDebutWindow(cfg GachaConfig, p GachaPull) bool {
	for _, d := range cfg.DualCitizens {
		match := false
		for _, n := range d.Names {
			if n == p.Name {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		t, ok := parseGachaTime(p.Time)
		if !ok {
			return true
		}
		return !t.Before(d.Start) && !t.After(d.End)
	}
	return false
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

// parseGachaTime parses the providers' shared "YYYY-MM-DD HH:MM:SS" local time
// string (HoYoverse, WuWa, Endfield all use it). Returns ok=false on any other
// format so callers fall back to ID ordering. Location is fixed (UTC) — only the
// relative order matters, and every record in one game shares the same format.
func parseGachaTime(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// sortChronological orders a banner's pulls oldest-first for pity walking: parsed
// Time ascending, with numLess(id) as the tiebreak for same-second records and a
// fallback when Time is empty/unparseable. WuWa's stable ids ("w|pool|time|ord")
// are NOT numeric, so a pure id sort would mis-order; Time is the real chronology.
// No-op for HoYoverse/Endfield (native ids are increasing == chronological).
func sortChronological(g []GachaPull) {
	sort.SliceStable(g, func(i, j int) bool {
		ti, oki := parseGachaTime(g[i].Time)
		tj, okj := parseGachaTime(g[j].Time)
		if oki && okj && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return numLess(g[i].ID, g[j].ID)
	})
}

// numLess compares numeric-string ids by (length, lexicographic) so longer
// (bigger) numbers sort higher without overflowing int64 on huge snowflake ids.
func numLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
