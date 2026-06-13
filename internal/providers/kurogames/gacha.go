package kurogames

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"omnigate/internal/core"
)

var conveneURLRe = regexp.MustCompile(`https://aki-gm-resources(?:-oversea|-back)?\.aki-game\.(?:net|com)/aki/gacha/index\.html#/record[^\s"]*`)

// defaultConvLogPaths returns the candidate log files (relative to the WuWa game dir)
// that may carry the convene-history URL.
func defaultConvLogPaths(installDir string) []string {
	return []string{
		filepath.Join(installDir, "Client", "Saved", "Logs", "Client.log"),
		filepath.Join(installDir, "Client", "Binaries", "Win64", "ThirdParty", "KrPcSdk_Global", "KRSDKRes", "KRSDKWebView", "debug.log"),
	}
}

// extractConveneParams reads the WuWa logs, finds the most recent convene URL,
// and returns its fragment params (svr_id/player_id/lang/record_id/resources_id…).
func (p *Provider) extractConveneParams(installDir string) (url.Values, error) {
	paths := p.convLogPathsFn(installDir)
	var last string
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		m := conveneURLRe.FindAll(b, -1)
		if len(m) == 0 {
			// recent builds XOR-obfuscate Client.log; decrypt and retry.
			m = conveneURLRe.FindAll(xorDecryptClientLog(b), -1)
		}
		if len(m) > 0 {
			last = string(m[len(m)-1]) // most recent in this file
		}
	}
	if last == "" {
		return nil, core.ErrGachaURLUnavailable
	}
	// params are in the fragment after "#/record?"
	frag := last
	if i := strings.Index(frag, "?"); i >= 0 {
		frag = frag[i+1:]
	}
	q, err := url.ParseQuery(frag)
	if err != nil || q.Get("record_id") == "" || q.Get("player_id") == "" {
		return nil, core.ErrGachaURLUnavailable
	}
	return q, nil
}

// xorDecryptClientLog de-obfuscates a WuWa Client.log. Recent builds XOR each
// byte: low-nibble-odd bytes with 0xA5, the rest with 0xEF. Plaintext logs
// (older builds, debug.log) are matched on the raw bytes first; this is only
// applied as a fallback when the raw regex finds nothing. The op is symmetric,
// so decrypting already-plaintext bytes just yields garbage that won't match.
func xorDecryptClientLog(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if (c&0x0F)%2 == 1 {
			out[i] = c ^ 0xA5
		} else {
			out[i] = c ^ 0xEF
		}
	}
	return out
}

// wuwaPity: every pull counts, reset on a headline; WuWa has no 50/50.
type wuwaPity struct{}

func (wuwaPity) HardPity() int { return 80 }
func (wuwaPity) Has5050() bool { return false }
func (wuwaPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
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

var wuwaPoolBanner = map[int]string{
	1: "character", 2: "weapon", 3: "standard_char", 4: "standard_weapon",
	5: "beginner", 6: "beginner_choice", 7: "other",
}

func poolBanner(t int) string {
	if b, ok := wuwaPoolBanner[t]; ok {
		return b
	}
	return "other"
}

func wuwaLoc(zhTW, zhCN, en string) core.LocalizedString {
	return core.LocalizedString{"zh-TW": zhTW, "zh-CN": zhCN, "en": en}
}

func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	return core.GachaConfig{
		HeadlineRank: 5,
		RankLabels:   map[int]core.LocalizedString{5: wuwaLoc("五星", "五星", "5★"), 4: wuwaLoc("四星", "四星", "4★")},
		Banners: []core.BannerConfig{
			{Key: "character", Label: wuwaLoc("限定共鳴者", "限定共鸣者", "Featured Resonator"), Pity: wuwaPity{}},
			{Key: "weapon", Label: wuwaLoc("限定武器", "限定武器", "Featured Weapon"), Pity: wuwaPity{}},
			{Key: "standard_char", Label: wuwaLoc("常駐共鳴者", "常驻共鸣者", "Standard Resonator"), Pity: wuwaPity{}},
			{Key: "standard_weapon", Label: wuwaLoc("常駐武器", "常驻武器", "Standard Weapon"), Pity: wuwaPity{}},
			{Key: "beginner", Label: wuwaLoc("新手", "新手", "Beginner"), Pity: wuwaPity{}},
			{Key: "beginner_choice", Label: wuwaLoc("新手自選", "新手自选", "Beginner Choice"), Pity: wuwaPity{}},
			{Key: "other", Label: wuwaLoc("感恩定向", "感恩定向", "Other"), Pity: wuwaPity{}},
		},
		PullPrice: 160, Currency: "astrite", ExpectedPity: 62.5,
	}
}

var _ core.GachaProvider = (*Provider)(nil)

type wuwaRecordResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    []struct {
		QualityLevel int    `json:"qualityLevel"`
		ResourceType string `json:"resourceType"`
		Name         string `json:"name"`
		Time         string `json:"time"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider for WuWa.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, installDir, cachedURL string) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	f, err := p.extractConveneParams(installDir)
	if err != nil {
		if cachedURL == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		cu, perr := url.Parse(cachedURL)
		if perr != nil {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
		frag := cu.Fragment
		if i := strings.Index(frag, "?"); i >= 0 {
			frag = frag[i+1:]
		}
		if f, err = url.ParseQuery(frag); err != nil || f.Get("record_id") == "" {
			return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
		}
	}
	return p.fetchWuwa(ctx, f)
}

func (p *Provider) recordAPI() string {
	if p.recordAPIBase != "" {
		return p.recordAPIBase
	}
	return "https://gmserver-api.aki-game2.net"
}

// fetchWuwa POSTs the record query for each pool type and normalizes results.
func (p *Provider) fetchWuwa(ctx context.Context, f url.Values) (core.GachaFetchResult, error) {
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	lang := f.Get("lang")
	if lang == "" {
		lang = "zh-Hant"
	}
	out := core.GachaFetchResult{UID: f.Get("player_id"), Pulls: []core.GachaPull{}}
	// cache a reconstructable URL (carries record_id; never logged)
	out.URL = "https://aki-gm-resources-oversea.aki-game.net/aki/gacha/index.html#/record?" + f.Encode()

	endpoint := p.recordAPI() + "/gacha/record/query"
	for pool := 1; pool <= 7; pool++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		core.ReportGachaProgress(ctx, core.GachaProgress{
			BannerKey: poolBanner(pool), Page: 1, PoolIndex: pool, PoolTotal: 7,
		})
		reqBody, _ := json.Marshal(map[string]any{
			"cardPoolId":   f.Get("resources_id"),
			"cardPoolType": pool,
			"languageCode": lang,
			"playerId":     f.Get("player_id"),
			"recordId":     f.Get("record_id"),
			"serverId":     f.Get("svr_id"),
		})
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return out, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			return out, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("wuwa record api status %d", resp.StatusCode)
		}
		var r wuwaRecordResp
		if err := json.Unmarshal(body, &r); err != nil {
			return out, err
		}
		if r.Code != 0 {
			return out, core.ErrGachaURLUnavailable
		}
		// API returns newest-first; reverse to oldest-first for stable indices.
		n := len(r.Data)
		for i := n - 1; i >= 0; i-- {
			e := r.Data[i]
			idx := n - 1 - i // 0 = oldest
			out.Pulls = append(out.Pulls, core.GachaPull{
				ID:        fmt.Sprintf("%d-%08d", pool, idx),
				BannerKey: poolBanner(pool),
				ItemType:  e.ResourceType,
				Rank:      e.QualityLevel,
				Name:      e.Name,
				Time:      e.Time,
			})
		}
		if p.recordDelay > 0 {
			time.Sleep(p.recordDelay)
		}
	}
	return out, nil
}
