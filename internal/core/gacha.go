package core

import "context"

// GachaPull is one normalized pull record (backend-agnostic).
// ID is the provider's unique id (HoYoverse `id`, Endfield `seqId`) and doubles
// as the dedup key AND the per-banner pity sort key. It is monotonic WITHIN a
// banner but NOT necessarily comparable across banners (WuWa synthesizes
// "<pool>-<idx>"), so cross-banner recency (recent-headline) is ordered by the
// parsed Time string instead — see ComputeSummary.
type GachaPull struct {
	ID        string `json:"id"`
	BannerKey string `json:"bannerKey"` // per-game banner key (matches GachaConfig.Banners[].Key)
	ItemType  string `json:"itemType"`  // normalized display type (角色/武器…)
	Rank      int    `json:"rank"`      // star rank; range is per-game (Endfield 4/5/6)
	Name      string `json:"name"`
	Time      string `json:"time"`   // source's localized time string, display-only
	IsFree    bool   `json:"isFree"` // free pull (Endfield); excluded from pity & spend
}

// GachaFetchResult is one refresh's outcome.
type GachaFetchResult struct {
	UID   string
	Pulls []GachaPull
	URL   string // the history URL used/obtained this round (store caches it)
}

// PityHit records a headline-rank pull and how many pulls it cost.
type PityHit struct {
	Pull  GachaPull
	Count int
}

// PityModel expresses one banner's pity semantics. Implementations are pure
// (no IO). HoYoverse hard-pity+50/50 and Endfield carryover/isFree both fit.
type PityModel interface {
	HardPity() int // hard-pity cap for the progress bar
	Has5050() bool // does this banner have a small/large guarantee (50/50)?
	// Walk takes same-banner pulls sorted ASCENDING by ID and returns each
	// headline-rank hit (with its pull-cost) plus the trailing not-yet-hit pity.
	Walk(sortedAscByID []GachaPull, headlineRank int) (hits []PityHit, trailingPity int)
}

// BannerConfig describes one pool for UI + stats.
type BannerConfig struct {
	Key   string
	Label LocalizedString
	Pity  PityModel
}

// GachaConfig is a game's rarity/banner/pricing config for the stats engine.
type GachaConfig struct {
	HeadlineRank int                     // top rarity (Endfield 6)
	RankLabels   map[int]LocalizedString // display labels per rank
	Banners      []BannerConfig
	PullPrice    int     // estimated price per (non-free) pull
	Currency     string  // e.g. "NT$"
	ExpectedPity float64 // theoretical avg pulls-per-headline (for the luck score)
}

// BannerOf returns the BannerConfig for key, or nil.
func (c GachaConfig) BannerOf(key string) *BannerConfig {
	for i := range c.Banners {
		if c.Banners[i].Key == key {
			return &c.Banners[i]
		}
	}
	return nil
}

// GachaProvider is an optional Provider capability: fetch a game's gacha history
// with no password. URL source is per-game (text-log regex vs webCaches scan);
// FetchGacha does real file IO + network (unlike LastPlayedProbe's pure paths).
type GachaProvider interface {
	// FetchGacha obtains the history URL (from the local log/cache, or reuses
	// cachedURL if still valid), calls the record API with cursor pagination,
	// and returns normalized pulls + uid. Returns ErrGachaURLUnavailable when no
	// usable URL is found (the App turns this into a "re-open in game" prompt).
	FetchGacha(ctx context.Context, gid GameID, installDir, cachedURL string) (GachaFetchResult, error)
	GachaConfig(gid GameID) GachaConfig
}

// GachaCredentialProvider is an optional capability for providers that
// authenticate with a durable, account-level credential (Endfield's Gryphline
// account_token) rather than a per-uid history URL. When a provider implements
// it, App.RefreshGacha reads the stored credential and calls this instead of the
// URL-based FetchGacha. `lang` is the already-mapped record-API language (App
// resolves it from settings; the provider has no other source — mirrors GetNews).
type GachaCredentialProvider interface {
	FetchGachaWithCredential(ctx context.Context, gid GameID, credential, lang string) (GachaFetchResult, error)
}
