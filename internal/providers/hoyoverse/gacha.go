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
	"time"

	"omnigate/internal/core"
)

// ── Task 1: webCache auth-query extraction ────────────────────────────────────

var hoyoAuthURLRe = regexp.MustCompile(`https://[^\s"\x00]*authkey=[^\s"\x00]*`)

// extractHoyoAuthQuery scans the game's webCaches data_* files for the freshest
// gacha page URL carrying authkey+game_biz, and returns its query values.
// installDir is the App-resolved game dir; dataDir is "<Game>_Data".
func extractHoyoAuthQuery(installDir, dataDir string) (url.Values, error) {
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

	var best url.Values
	var bestTS int64 = -1
	for _, n := range []string{"data_1", "data_2", "data_3"} {
		b, err := os.ReadFile(filepath.Join(root, newest, "Cache", "Cache_Data", n))
		if err != nil {
			continue
		}
		for _, m := range hoyoAuthURLRe.FindAll(b, -1) {
			u, e := url.Parse(string(m))
			if e != nil {
				continue
			}
			q := u.Query()
			if q.Get("authkey") == "" || q.Get("game_biz") == "" {
				continue
			}
			ts, _ := strconv.ParseInt(q.Get("timestamp"), 10, 64)
			if ts > bestTS {
				bestTS, best = ts, q
			}
		}
	}
	if best == nil {
		return nil, core.ErrGachaURLUnavailable
	}
	return best, nil
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

func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	rank := map[int]core.LocalizedString{5: loc("五星", "五星", "5★"), 4: loc("四星", "四星", "4★")}
	cfg := core.GachaConfig{HeadlineRank: 5, RankLabels: rank, PullPrice: 100, Currency: "NT$", ExpectedPity: 62.5}
	switch gid {
	case "hoyoverse/genshin":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}},
			{Key: "weapon", Label: loc("武器", "武器", "Weapon"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
			{Key: "chronicled", Label: loc("集錄", "集录", "Chronicled"), Pity: hoyoPity{90, false}},
		}
	case "hoyoverse/starrail":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定角色", "限定角色", "Character"), Pity: hoyoPity{90, true}},
			{Key: "lightcone", Label: loc("光錐", "光锥", "Light Cone"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "beginner", Label: loc("新手", "新手", "Beginner"), Pity: hoyoPity{90, false}},
		}
	case "hoyoverse/zzz":
		cfg.Banners = []core.BannerConfig{
			{Key: "character", Label: loc("限定代理人", "限定代理人", "Character"), Pity: hoyoPity{90, true}},
			{Key: "wengine", Label: loc("音擎", "音擎", "W-Engine"), Pity: hoyoPity{80, true}},
			{Key: "standard", Label: loc("常駐", "常驻", "Standard"), Pity: hoyoPity{90, false}},
			{Key: "bangboo", Label: loc("邦布", "邦布", "Bangboo"), Pity: hoyoPity{80, false}},
		}
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
	q, err := extractHoyoAuthQuery(installDir, dataDirFor(m))
	if err != nil {
		// fall back to a cached auth-query string if present
		if cachedURL == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cu, perr := url.Parse(cachedURL)
		if perr != nil || cu.Query().Get("authkey") == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		q = cu.Query()
	}
	return p.fetchHoyoGacha(ctx, gid, q)
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

	for _, gt := range gachaTypesToQuery[gid] {
		endID := "0"
		for page := 1; page <= 100; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			q := url.Values{}
			for _, k := range []string{"authkey", "authkey_ver", "sign_type", "game_biz", "lang", "region"} {
				if v := auth.Get(k); v != "" {
					q.Set(k, v)
				}
			}
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
