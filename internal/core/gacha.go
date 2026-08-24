package core

import (
	"context"
	"time"
)

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
	// PoolID and PoolName identify the specific banner期 within a pool
	// (Endfield's rotating 特許尋訪 has e.g. poolId="special_1_3_1");
	// empty for providers/pulls that don't set them.
	PoolID   string `json:"poolId"`
	PoolName string `json:"poolName"`
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
	Key     string
	Label   LocalizedString
	Pity    PityModel
	Limited bool // a featured/event banner where losing the 50/50 (歪) can occur
	// PerPool splits this banner into independent-pity sub-banners by pull PoolID
	// (Endfield's rotating 特許尋訪); default false = unchanged.
	PerPool bool `json:"perPool,omitempty"`
	// CrossPoolBar, when PerPool, makes ComputeSummary ALSO emit one aggregate pity row
	// over ALL the banner's pulls under the bare key (the true cross-pool pity, a
	// display-only top bar). Its hits are discarded — they never enter the global pity
	// stats. The empty-poolId fallback sub is folded into this aggregate. Endfield 特許尋訪.
	CrossPoolBar bool `json:"crossPoolBar"`
}

// DualCitizen is a standard-pool unit that also had a single featured debut; a pull
// of it inside [Start,End] is the debut win (not 歪), outside it is a 50/50 loss.
type DualCitizen struct {
	Names      []string // all stored language forms of the name
	Start, End time.Time
}

// GachaConfig is a game's rarity/banner/pricing config for the stats engine.
type GachaConfig struct {
	HeadlineRank           int                     // top rarity (Endfield 6)
	RankLabels             map[int]LocalizedString // display labels per rank
	Banners                []BannerConfig
	PullPrice              int             // estimated price per (non-free) pull
	Currency               string          // e.g. "NT$"
	ExpectedPity           float64         // theoretical avg pulls-per-headline (出金 expectation; luck score + card note)
	ExpectedFeaturedWeapon float64         // theoretical avg pulls per featured weapon (出限定武器期望); 0 = unknown
	StandardPool           map[string]bool // top-rarity standard/permanent item names (all stored langs); a limited-banner pull of one = 歪
	DualCitizens           []DualCitizen   // standard-pool units that were also featured once (Genshin); 歪 only OUTSIDE the debut window
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
	// known is the set of pull ids (seqIds) already stored for this account, so the
	// provider can stop paginating once it reaches them (incremental sync). Pass nil
	// for a full fetch (first sync).
	FetchGachaWithCredential(ctx context.Context, gid GameID, credential, lang string, known map[string]bool) (GachaFetchResult, error)
}

// GachaImportProvider is an optional capability: parse a third-party gacha
// export file (offline import) into normalized pulls under the export's own
// uid. `existing` returns the already-stored pull ids for a uid (nil-safe);
// providers use it to keep synthesized ids consistent with their live-fetch
// path (WuWa infers the server UTC offset from id overlap). The App upserts
// the returned pulls; URL stays empty (an import carries no history URL).
type GachaImportProvider interface {
	ParseGachaImport(gid GameID, data []byte, existing func(uid string) map[string]bool) (GachaFetchResult, error)
}
