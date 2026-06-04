package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"omnigate/internal/core"
)

const endfieldHardPity = 80       // official: 6★ hard pity (char)
const endfieldExpectedPity = 62.0 // theoretical avg pulls/6★ for the luck score
const endfieldMilestone = 60      // free-pull carryover milestone (ref repo)
const endfieldPullPrice = 100     // estimated price per pull (placeholder unit, NT$)
const endfieldMaxPages = 200      // per-pool pagination safety cap (200×~20 ≫ any account)

// endfieldStandardPity: every pull counts; reset to 0 on a headline.
type endfieldStandardPity struct{}

func (endfieldStandardPity) HardPity() int { return endfieldHardPity }
func (endfieldStandardPity) Has5050() bool { return false }
func (endfieldStandardPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
	hits, pity := []core.PityHit{}, 0
	for _, p := range sorted {
		pity++
		if p.Rank == headline {
			hits = append(hits, core.PityHit{Pull: p, Count: pity})
			pity = 0
		}
	}
	return hits, pity
}

// endfieldLimitedPity: only non-free pulls count; free pulls add to carryover
// only after milestone≥60; on a headline, pity resets to the carried-over count.
type endfieldLimitedPity struct{}

func (endfieldLimitedPity) HardPity() int { return endfieldHardPity }
func (endfieldLimitedPity) Has5050() bool { return false }
func (endfieldLimitedPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
	// A single scalar milestone/carry is correct because the stats engine groups
	// pulls by BannerKey and calls Walk once per banner — so `sorted` only ever
	// contains this one limited pool (the reference repo's per-gacha_type
	// milestoneMap collapses to the single-banner case here).
	hits := []core.PityHit{}
	pity, carry, milestone := 0, 0, 0
	for _, p := range sorted {
		if p.Rank == headline {
			hits = append(hits, core.PityHit{Pull: p, Count: pity})
			pity, carry, milestone = carry, 0, 0
			continue
		}
		if !p.IsFree {
			milestone++
			pity++
		} else if milestone >= endfieldMilestone {
			carry++
		}
	}
	return hits, pity
}

// GachaConfig implements part of core.GachaProvider (FetchGacha is below).
func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	return core.GachaConfig{
		HeadlineRank: 6,
		RankLabels: map[int]core.LocalizedString{
			6: {"zh-TW": "六星", "zh-CN": "六星", "en": "6★"},
			5: {"zh-TW": "五星", "zh-CN": "五星", "en": "5★"},
		},
		Banners: []core.BannerConfig{
			{Key: "special", Label: core.LocalizedString{"zh-TW": "特許尋訪", "zh-CN": "特许寻访", "en": "Limited"}, Pity: endfieldLimitedPity{}},
			{Key: "standard", Label: core.LocalizedString{"zh-TW": "基礎尋訪", "zh-CN": "基础寻访", "en": "Standard"}, Pity: endfieldStandardPity{}},
			{Key: "beginner", Label: core.LocalizedString{"zh-TW": "啟程尋訪", "zh-CN": "启程寻访", "en": "Beginner"}, Pity: endfieldStandardPity{}},
			// Joint pool (collab) exists in the live pool_type enum. Its exact pity
			// rule is unverified → standard-pity placeholder so its pulls still count
			// and display; refine if a live record set shows different behaviour.
			{Key: "joint", Label: core.LocalizedString{"zh-TW": "聯動尋訪", "zh-CN": "联动寻访", "en": "Joint"}, Pity: endfieldStandardPity{}},
		},
		PullPrice: endfieldPullPrice, Currency: "NT$", ExpectedPity: endfieldExpectedPity,
	}
}

// ── Task 6: FetchGacha ────────────────────────────────────────────────────────

var _ core.GachaProvider = (*Provider)(nil)

var endfieldGachaURLRe = regexp.MustCompile(`https://ef-webview\.gryphline\.com/page/gacha_[^\s"']*`)

var endfieldPools = []struct{ poolType, bannerKey string }{
	{"E_CharacterGachaPoolType_Special", "special"},
	{"E_CharacterGachaPoolType_Standard", "standard"},
	{"E_CharacterGachaPoolType_Beginner", "beginner"},
	{"E_CharacterGachaPoolType_Joint", "joint"},
}

func defaultEndfieldLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "AppData", "LocalLow", "Gryphline", "Endfield", "sdklogs", "HGWebview.log")
}

// extractEndfieldGachaURL returns the LAST (most recent) gacha page URL in the log.
func extractEndfieldGachaURL(log []byte) string {
	m := endfieldGachaURLRe.FindAll(log, -1)
	if len(m) == 0 {
		return ""
	}
	return string(m[len(m)-1])
}

type endfieldRecordResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data *struct {
		List []struct {
			PoolID   string `json:"poolId"`
			PoolName string `json:"poolName"`
			CharID   string `json:"charId"`
			CharName string `json:"charName"`
			Rarity   int    `json:"rarity"`
			GachaTs  string `json:"gachaTs"`
			SeqID    string `json:"seqId"`
			IsFree   bool   `json:"isFree"`
		} `json:"list"`
		HasMore bool `json:"hasMore"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, _, cachedURL string) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	gachaURL := p.readGachaURL()
	if gachaURL == "" {
		gachaURL = cachedURL
	}
	if gachaURL == "" {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	return p.fetchEndfield(ctx, gachaURL)
}

// readGachaURL reads the local SDK log and extracts the latest gacha URL ("" if none).
func (p *Provider) readGachaURL() string {
	path := p.logPathFn()
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return extractEndfieldGachaURL(b)
}

// fetchEndfield parses the token/server/lang from the gachaURL and paginates
// each pool against /api/record/char.
//
// The live (2026) page URL carries these as `u8_token` and `server` — verified
// against the real endpoint: the record API itself wants them as `token` and
// `server_id` (a token-only probe returned 400 listing token/pool_type/server_id/
// lang as required; supplying them yielded HTTP 200, only the expired token was
// rejected with code 40100). We accept the older `token`/`server_id` query names
// too as a fallback. The /api/record/char endpoint, pool_type enum, and seq_id
// cursor are unchanged; the record RESPONSE shape is still pending live-token
// smoke verification (matches the documented {code,msg,data:{list,hasMore}}).
func (p *Provider) fetchEndfield(ctx context.Context, gachaURL string) (core.GachaFetchResult, error) {
	u, err := url.Parse(gachaURL)
	if err != nil {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	q := u.Query()
	token := firstNonEmpty(q.Get("u8_token"), q.Get("token"))
	serverID := firstNonEmpty(q.Get("server"), q.Get("server_id"))
	lang := q.Get("lang")
	if token == "" {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	if serverID == "" {
		serverID = "2"
	}
	if lang == "" {
		lang = "zh-tw"
	}
	hc := p.client
	if hc == nil {
		hc = http.DefaultClient
	}
	out := core.GachaFetchResult{URL: gachaURL, Pulls: []core.GachaPull{}}
	for _, pool := range endfieldPools {
		seqID := ""
		for page := 0; page < endfieldMaxPages; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			q := url.Values{}
			q.Set("token", token)
			q.Set("pool_type", pool.poolType)
			q.Set("lang", lang)
			q.Set("server_id", serverID)
			if seqID != "" {
				q.Set("seq_id", seqID)
			}
			req, err := http.NewRequestWithContext(ctx, "GET", p.recordAPIBase+"/api/record/char?"+q.Encode(), nil)
			if err != nil {
				return out, err
			}
			req.Header.Set("User-Agent", UserAgent)
			resp, err := hc.Do(req)
			if err != nil {
				return out, err
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				return out, fmt.Errorf("endfield record api status %d", resp.StatusCode)
			}
			var r endfieldRecordResp
			if err := json.Unmarshal(body, &r); err != nil {
				return out, err
			}
			if r.Code != 0 || r.Data == nil {
				return out, core.ErrGachaURLUnavailable
			}
			for _, e := range r.Data.List {
				out.Pulls = append(out.Pulls, core.GachaPull{
					ID:        e.SeqID,
					BannerKey: pool.bannerKey,
					ItemType:  "char",
					Rank:      e.Rarity,
					Name:      e.CharName,
					Time:      e.GachaTs,
					IsFree:    e.IsFree,
				})
			}
			if !r.Data.HasMore || len(r.Data.List) == 0 {
				break
			}
			seqID = r.Data.List[len(r.Data.List)-1].SeqID
			if p.pageDelay > 0 {
				time.Sleep(p.pageDelay)
			}
		}
	}
	if out.UID == "" {
		out.UID = serverID + ":" + token[:minInt(len(token), 8)]
	}
	return out, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// minInt avoids shadowing the Go 1.21 builtin min / any package-level helper.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
