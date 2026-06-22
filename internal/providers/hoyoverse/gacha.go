package hoyoverse

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
	"sort"
	"strconv"
	"strings"
	"time"

	"omnigate/internal/core"
)

// ── Task 1: webCache auth-query extraction ────────────────────────────────────

// hoyoAuthURLRe matches a cached gacha-log URL carrying an authkey. The
// getGachaLog anchor (applied after the match) is what makes a URL a candidate:
// other authkey-bearing URLs (gacha-page init, unrelated APIs) authenticate but
// return an empty/wrong-scope list, so they must be excluded.
var hoyoAuthURLRe = regexp.MustCompile(`https://[^\s"\x00]*authkey=[^\s"\x00]*`)

// extractHoyoAuthQuery scans the newest webCaches data_1/2/3 for getGachaLog URLs
// carrying an authkey, and returns the deduped candidate queries ordered freshest
// (largest timestamp) first; URLs without a timestamp sort last. The full query of
// each candidate is preserved (callers forward every param). installDir is the
// App-resolved game dir; dataDir is "<Game>_Data".
func extractHoyoAuthQuery(installDir, dataDir string) ([]url.Values, error) {
	root := filepath.Join(installDir, dataDir, "webCaches")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, core.ErrGachaURLUnavailable
	}
	vers := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			vers = append(vers, e.Name())
		}
	}
	if len(vers) == 0 {
		return nil, core.ErrGachaURLUnavailable
	}
	sort.Strings(vers)
	newest := vers[len(vers)-1]

	type candidate struct {
		q    url.Values
		ts   int64
		seen int // encounter order, for stable tie-breaking
	}
	var cands []candidate
	dedup := map[string]bool{}
	order := 0
	for _, n := range []string{"data_1", "data_2", "data_3"} {
		b, err := os.ReadFile(filepath.Join(root, newest, "Cache", "Cache_Data", n))
		if err != nil {
			continue
		}
		for _, m := range hoyoAuthURLRe.FindAll(b, -1) {
			s := string(m)
			if !strings.Contains(s, "getGachaLog") { // getGachaLog anchor
				continue
			}
			u, e := url.Parse(s)
			if e != nil {
				continue
			}
			q := u.Query()
			if q.Get("authkey") == "" {
				continue
			}
			key := q.Encode()
			if dedup[key] {
				continue
			}
			dedup[key] = true
			ts, _ := strconv.ParseInt(q.Get("timestamp"), 10, 64)
			cands = append(cands, candidate{q: q, ts: ts, seen: order})
			order++
		}
	}
	if len(cands) == 0 {
		return nil, core.ErrGachaURLUnavailable
	}
	// freshest timestamp first; missing/zero ts last; stable on ties.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].ts != cands[j].ts {
			return cands[i].ts > cands[j].ts
		}
		return cands[i].seen < cands[j].seen
	})
	out := make([]url.Values, len(cands))
	for i, c := range cands {
		out[i] = c.q
	}
	return out, nil
}

// cloneValues deep-copies a url.Values so per-request Set() never mutates the
// shared candidate/auth map (url.Values is map[string][]string).
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// dataDirFor derives "<Game>_Data" from the game's exe name (e.g.
// GenshinImpact.exe → GenshinImpact_Data).
func dataDirFor(m *gameMeta) string {
	base := m.ExeName
	if i := len(base) - len(".exe"); i > 0 && base[i:] == ".exe" {
		base = base[:i]
	}
	return base + "_Data"
}

// ── Task 2: Per-game GachaConfig + pity models ───────────────────────────────

// hoyoPity: HoYoverse standard pity — every pull counts, reset to 0 on a headline.
type hoyoPity struct {
	cap   int
	fifty bool
}

func (m hoyoPity) HardPity() int { return m.cap }
func (m hoyoPity) Has5050() bool { return m.fifty }
func (m hoyoPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
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

// gachaTypeBanner maps a per-game gacha_type code → bannerKey. Genshin 301 & 400
// both fold into "character" so the limited-character pity merges across them.
var gachaTypeBanner = map[core.GameID]map[string]string{
	"hoyoverse/genshin":  {"301": "character", "400": "character", "302": "weapon", "200": "standard", "100": "beginner", "500": "chronicled"},
	"hoyoverse/starrail": {"11": "character", "12": "lightcone", "1": "standard", "2": "beginner"},
	"hoyoverse/zzz":      {"2": "character", "3": "wengine", "1": "standard", "5": "bangboo"},
}

func bannerForGachaType(gid core.GameID, gachaType string) string {
	if m, ok := gachaTypeBanner[gid]; ok {
		if b, ok := m[gachaType]; ok {
			return b
		}
	}
	return "standard"
}

// gachaTypesToQuery lists the gacha_type codes to fetch per game (one banner page
// loop each). Genshin queries 301 AND 400 (character spans both) plus the rest.
var gachaTypesToQuery = map[core.GameID][]string{
	"hoyoverse/genshin":  {"301", "400", "302", "200", "100", "500"},
	"hoyoverse/starrail": {"11", "12", "1", "2"},
	"hoyoverse/zzz":      {"2", "3", "1", "5"},
}

func loc(zhTW, zhCN, en string) core.LocalizedString {
	return core.LocalizedString{"zh-TW": zhTW, "zh-CN": zhCN, "en": en}
}

// genshinStandardPool lists every top-rarity permanent item name in all stored
// language forms. A limited-banner pull of any of these is a 50/50 loss (歪),
// EXCEPT dual-citizens within their debut window (see genshinDualCitizens).
var genshinStandardPool = map[string]bool{
	// Characters
	"Jean": true, "琴": true,
	"Diluc": true, "迪盧克": true, "迪卢克": true,
	"Mona": true, "莫娜": true,
	"Qiqi": true, "七七": true,
	"Keqing": true, "刻晴": true,
	"Tighnari": true, "提納里": true, "提纳里": true,
	"Dehya": true, "迪希雅": true,
	"Yumemizuki Mizuki": true, "夢見月瑞希": true, "梦见月瑞希": true,
	// Weapons
	"Amos' Bow": true, "阿莫斯之弓": true,
	"Skyward Harp": true, "天空之翼": true,
	"Lost Prayer to the Sacred Winds": true, "四風原典": true, "四风原典": true,
	"Skyward Atlas": true, "天空之卷": true,
	"Wolf's Gravestone": true, "狼的末路": true,
	"Skyward Pride": true, "天空之傲": true,
	"Primordial Jade Winged-Spear": true, "和璞鳶": true, "和璞鸢": true,
	"Skyward Spine": true, "天空之脊": true,
	"Aquila Favonia": true, "風鷹劍": true, "风鹰剑": true,
	"Skyward Blade": true, "天空之刃": true,
}

// starrailStandardPool lists every top-rarity permanent item name in all stored
// language forms. A limited-banner pull of any of these is a 50/50 loss (歪).
// Star Rail has no dual-citizens (standard-pool units were never re-run on limited).
var starrailStandardPool = map[string]bool{
	// Characters
	"Bailu": true, "白露": true,
	"Bronya": true, "布洛妮婭": true, "布洛妮娅": true,
	"Clara": true, "克拉拉": true,
	"Gepard": true, "傑帕德": true, "杰帕德": true,
	"Himeko": true, "姬子": true,
	"Welt": true, "瓦爾特": true, "瓦尔特": true,
	"Yanqing": true, "彥卿": true, "彦卿": true,
	// Light Cones
	"Night on the Milky Way": true, "銀河鐵道之夜": true, "银河铁道之夜": true,
	"Something Irreplaceable": true, "無可取代的東西": true, "无可取代的东西": true,
	"But the Battle Isn't Over": true, "但戰鬥還未結束": true, "但战斗还未结束": true,
	"In the Name of the World": true, "以世界之名": true,
	"Moment of Victory": true, "制勝的瞬間": true, "制胜的瞬间": true,
	"Sleep Like the Dead": true, "如泥酣眠": true,
	"Time Waits for No One": true, "時節不居": true, "时节不居": true,
}

// zzzStandardPool lists every top-rarity permanent item name in all stored
// language forms. A limited-banner pull of any of these is a 50/50 loss (歪).
// ZZZ has no dual-citizens (standard-pool agents were never re-run on limited).
// Soldier 11 is stored UNBRACKETED — the form the API returns ("11號"/"11号"),
// NOT "[11]號" — so the lookup must use the same unbracketed string.
var zzzStandardPool = map[string]bool{
	// Agents
	"Nekomata": true, "貓又": true, "猫又": true,
	"Soldier 11": true, "11號": true, "11号": true,
	"Koleda": true, "珂蕾妲": true,
	"Lycaon": true, "萊卡恩": true, "莱卡恩": true,
	"Grace Howard": true, "格莉絲": true, "格莉丝": true,
	"Rina": true, "麗娜": true, "丽娜": true,
	// W-Engines
	"Steel Cushion": true, "鋼鐵肉墊": true, "钢铁肉垫": true,
	"The Brimstone": true, "硫磺石": true,
	"Hellfire Gears": true, "燃獄齒輪": true, "燃狱齿轮": true,
	"The Restrained": true, "拘縛者": true, "拘缚者": true,
	"Fusion Compiler": true, "嵌合編譯器": true, "嵌合编译器": true,
	"Weeping Cradle": true, "啜泣搖籃": true, "啜泣摇篮": true,
}

// genshinDualCitizens lists standard-pool 5★ units that were ALSO featured on a
// limited Event Wish exactly once (their debut). A pull inside the debut window is
// that win (not 歪); outside it is a 50/50 loss. Windows are padded ±1 day from
// the banner's server-local calendar to absorb cross-server/timezone skew.
// These units appear on a limited banner ONLY at debut, so padding cannot suppress
// any later real 歪.  (Dates web-verified vs. wiki/game8.)
var genshinDualCitizens = []core.DualCitizen{
	{
		Names: []string{"Tighnari", "提納里", "提纳里"},
		Start: time.Date(2022, 8, 23, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2022, 9, 10, 23, 59, 59, 0, time.UTC),
	},
	{
		Names: []string{"Dehya", "迪希雅"},
		Start: time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2023, 3, 22, 23, 59, 59, 0, time.UTC),
	},
	{
		Names: []string{"Yumemizuki Mizuki", "夢見月瑞希", "梦见月瑞希"},
		Start: time.Date(2025, 2, 11, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 3, 5, 23, 59, 59, 0, time.UTC),
	},
}

func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	rank := map[int]core.LocalizedString{5: loc("五星", "五星", "5★"), 4: loc("四星", "四星", "4★")}
	// PullPrice 160 = premium currency consumed per pull (all HoYoverse games);
	// Currency is a per-game stone code resolved to a localized name in the UI.
	cfg := core.GachaConfig{HeadlineRank: 5, RankLabels: rank, PullPrice: 160, ExpectedPity: 62.5}
	switch gid {
	case "hoyoverse/genshin":
		cfg.Currency = "primogem"
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}, Limited: true},
			{Key: "weapon", Label: loc("武器", "武器", "Weapon"), Pity: hoyoPity{80, true}, Limited: true},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
			{Key: "chronicled", Label: loc("集錄", "集录", "Chronicled"), Pity: hoyoPity{90, false}},
		}
		cfg.StandardPool = genshinStandardPool
		cfg.DualCitizens = genshinDualCitizens
	case "hoyoverse/starrail":
		cfg.Currency = "stellar_jade"
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}, Limited: true},
			{Key: "lightcone", Label: loc("光錐", "光锥", "Light Cone"), Pity: hoyoPity{80, true}, Limited: true},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
		}
		cfg.StandardPool = starrailStandardPool
	case "hoyoverse/zzz":
		cfg.Currency = "polychrome"
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定代理人", "限定代理人", "Character"), Pity: hoyoPity{90, true}, Limited: true},
			{Key: "wengine", Label: loc("音擎", "音擎", "W-Engine"), Pity: hoyoPity{80, true}, Limited: true},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "bangboo", Label: loc("邦布", "邦布", "Bangboo"), Pity: hoyoPity{80, false}},
		}
		cfg.StandardPool = zzzStandardPool
	}
	return cfg
}

// ── Task 3: FetchGacha (getGachaLog pagination + normalize) ──────────────────

var _ core.GachaProvider = (*Provider)(nil)

func hoyoGetGachaLogEndpoint(gid core.GameID) string {
	switch gid {
	case "hoyoverse/genshin":
		return "https://public-operation-hk4e-sg.hoyoverse.com/gacha_info/api/getGachaLog"
	case "hoyoverse/starrail":
		return "https://public-operation-hkrpg-sg.hoyoverse.com/common/hkrpg_gacha_record/api/getGachaLog"
	case "hoyoverse/zzz":
		return "https://public-operation-common-sg.hoyoverse.com/common/gacha_record/api/getGachaLog"
	}
	return ""
}

type hoyoGachaLogResp struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    *struct {
		List []struct {
			ID        string `json:"id"`
			GachaType string `json:"gacha_type"`
			RankType  string `json:"rank_type"`
			ItemType  string `json:"item_type"`
			Name      string `json:"name"`
			Time      string `json:"time"`
			UID       string `json:"uid"`
		} `json:"list"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider. installDir is the resolved game dir.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, installDir, cachedURL string) (core.GachaFetchResult, error) {
	m := findByID(gid)
	if m == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	cands, err := extractHoyoAuthQuery(installDir, dataDirFor(m))
	if err != nil {
		// No fresh candidates → fall back to a stored auth-query URL if present.
		// The cachedURL is a getGachaLog query (endpoint+"?"+query); accept it as a
		// single candidate as long as it carries authkey (anchor-exempt: R4).
		if cachedURL == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cu, perr := url.Parse(cachedURL)
		if perr != nil || cu.Query().Get("authkey") == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cands = []url.Values{cu.Query()}
	}
	auth, err := p.selectAuthCandidate(ctx, gid, cands)
	if err != nil {
		return core.GachaFetchResult{}, err
	}
	return p.fetchHoyoGacha(ctx, gid, auth)
}

// selectAuthCandidate probes each candidate once (first banner gacha_type, page 1)
// against getGachaLog and returns the chosen full query, using a two-tier rule:
//   - tier 1: the first candidate (in order) with retcode==0 and a non-empty list;
//   - tier 2: else the first candidate with retcode==0 and non-nil data;
//   - else ErrGachaURLUnavailable.
//
// A probe that errors, returns non-200, fails to parse, has retcode!=0, or nil
// data is SKIPPED (never fatal) so a single bad/expired candidate can't abort
// selection while a working one remains.
func (p *Provider) selectAuthCandidate(ctx context.Context, gid core.GameID, cands []url.Values) (url.Values, error) {
	endpoint := p.endpointFor(gid)
	if endpoint == "" {
		return nil, core.ErrUnknownGame
	}
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	gts := gachaTypesToQuery[gid]
	if len(gts) == 0 {
		return nil, core.ErrUnknownGame
	}
	probeType := gts[0]

	var tier2 url.Values
	for i, c := range cands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Space out probes for rate limiting — applies to every probe after the
		// first (incl. failing/expired candidates, the real rate-limit risk).
		if i > 0 && p.gachaPageDelay > 0 {
			time.Sleep(p.gachaPageDelay)
		}
		q := cloneValues(c)
		q.Set("gacha_type", probeType)
		q.Set("size", "20")
		q.Set("page", "1")
		q.Set("end_id", "0")
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var r hoyoGachaLogResp
		if err := json.Unmarshal(body, &r); err != nil {
			continue
		}
		if r.Retcode != 0 || r.Data == nil {
			continue
		}
		if len(r.Data.List) > 0 {
			return c, nil // tier 1: first non-empty wins
		}
		if tier2 == nil {
			tier2 = c // remember first retcode0+empty
		}
	}
	if tier2 != nil {
		return tier2, nil
	}
	return nil, core.ErrGachaURLUnavailable
}

func (p *Provider) endpointFor(gid core.GameID) string {
	if p.gachaEndpoint != nil {
		return p.gachaEndpoint(gid)
	}
	return hoyoGetGachaLogEndpoint(gid)
}

// fetchHoyoGacha replays the auth query against getGachaLog for every banner
// gacha_type, paginating by end_id. Caches the auth query (as URL) in the result.
func (p *Provider) fetchHoyoGacha(ctx context.Context, gid core.GameID, auth url.Values) (core.GachaFetchResult, error) {
	endpoint := p.endpointFor(gid)
	if endpoint == "" {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	out := core.GachaFetchResult{Pulls: []core.GachaPull{}}
	// cache the auth query as a URL string for the store (token-bearing; never logged)
	out.URL = endpoint + "?" + auth.Encode()

	gts := gachaTypesToQuery[gid]
	for i, gt := range gts {
		endID := "0"
		for page := 1; page <= 100; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			core.ReportGachaProgress(ctx, core.GachaProgress{
				BannerKey: bannerForGachaType(gid, gt), Page: page, PoolIndex: i + 1, PoolTotal: len(gts),
			})
			// Forward the full chosen query verbatim; override only the
			// per-request pagination keys (some games, e.g. ZZZ, need the
			// extra params the cached URL carries — see fix #1 spec).
			q := cloneValues(auth)
			q.Set("gacha_type", gt)
			q.Set("size", "20")
			q.Set("page", strconv.Itoa(page))
			q.Set("end_id", endID)
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
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
				return out, fmt.Errorf("getGachaLog status %d", resp.StatusCode)
			}
			var r hoyoGachaLogResp
			if err := json.Unmarshal(body, &r); err != nil {
				return out, err
			}
			if r.Retcode != 0 || r.Data == nil {
				// -100/-101/-111 etc → expired/invalid authkey → re-open guidance
				return out, core.ErrGachaURLUnavailable
			}
			for _, e := range r.Data.List {
				rank, _ := strconv.Atoi(e.RankType)
				if out.UID == "" {
					out.UID = e.UID
				}
				out.Pulls = append(out.Pulls, core.GachaPull{
					ID:        e.ID,
					BannerKey: bannerForGachaType(gid, e.GachaType),
					ItemType:  e.ItemType,
					Rank:      rank,
					Name:      e.Name,
					Time:      e.Time,
				})
			}
			if len(r.Data.List) < 20 {
				break
			}
			endID = r.Data.List[len(r.Data.List)-1].ID
			if p.gachaPageDelay > 0 {
				time.Sleep(p.gachaPageDelay)
			}
		}
	}
	return out, nil
}
