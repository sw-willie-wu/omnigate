package hypergryph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"omnigate/internal/core"
)

const endfieldHardPity = 80       // official: 6★ hard pity (char)
const endfieldExpectedPity = 62.0 // theoretical avg pulls/6★ for the luck score
const endfieldMilestone = 60      // free-pull carryover milestone (ref repo)
const endfieldPullPrice = 500     // Endfield per-pull cost: 500 Oroberyl (嵌晶玉)
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

const endfieldWeaponHardPity = 40 // 武器 6★ 保底（per-期，每抽都算）

// endfieldWeaponPity: like endfieldStandardPity but capped at 40. Weapon records have
// no isFree (always false), so every pull counts; reset to 0 on a 6★ headline.
type endfieldWeaponPity struct{}

func (endfieldWeaponPity) HardPity() int { return endfieldWeaponHardPity }
func (endfieldWeaponPity) Has5050() bool { return false }
func (endfieldWeaponPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
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

// endfieldStandardPool contains the standard (permanent) 6★ pool for Endfield:
// 5 standard operators + 29 standard 6★ weapons (the biligame 6★ weapon atlas minus
// the 7 featured). Stored zh-TW (Traditional) + zh-CN (Simplified); operators also en.
// The record API returns names in the REQUESTED lang, so a zh-TW client
// (mapEndfieldLang→"zh-tw") gets Traditional — the Traditional forms are load-bearing
// (verified against the user's stored pulls). 6★ ONLY (a 5★ name would mis-flag
// second-rank Highlights). Approximate: misses losses into past-featured items.
var endfieldStandardPool = map[string]bool{
	// standard 6★ operators (zh-TW / zh-CN / en)
	"艾爾黛拉": true, "艾尔黛拉": true, "Ardelia": true,
	"駿衛": true, "骏卫": true, "Pogranichnik": true,
	"別禮": true, "别礼": true, "Last Rite": true,
	"黎風": true, "黎风": true, "Lifeng": true,
	"餘燼": true, "余烬": true, "Ember": true,
	// standard 6★ weapons (zh-TW / zh-CN)
	"同類相食": true, "同类相食": true,
	"望鄉": true, "望乡": true,
	"顯赫聲名": true, "显赫声名": true,
	"爆破單元": true, "爆破单元": true,
	"不知歸": true, "不知归": true,
	"典範": true, "典范": true,
	"J.E.T.": true,
	"光榮記憶": true, "光荣记忆": true,
	"白夜新星": true,
	"昔日精品": true,
	"遺忘": true, "遗忘": true,
	"鍍紅祝福": true, "镀红祝福": true,
	"破碎君王": true,
	"騎士精神": true, "骑士精神": true,
	"黯色火炬": true,
	"燈火使命": true, "灯火使命": true,
	"領航者": true, "领航者": true,
	"作品：蝕跡": true, "作品：蚀迹": true,
	"扶搖": true, "扶摇": true,
	"幻想苦痛": true,
	"大雷斑": true,
	"負山": true, "负山": true,
	"楔子": true,
	"熱熔切割器": true, "热熔切割器": true,
	"滄溟星夢": true, "沧溟星梦": true,
	"宏願": true, "宏愿": true,
	"赫拉芬格": true,
	"驍勇": true, "骁勇": true,
	"霧中微光": true, "雾中微光": true,
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
			{Key: "special", Label: core.LocalizedString{"zh-TW": "特許尋訪", "zh-CN": "特许寻访", "en": "Limited"}, Pity: endfieldLimitedPity{}, Limited: true, PerPool: true},
			{Key: "standard", Label: core.LocalizedString{"zh-TW": "基礎尋訪", "zh-CN": "基础寻访", "en": "Standard"}, Pity: endfieldStandardPity{}},
			{Key: "beginner", Label: core.LocalizedString{"zh-TW": "啟程尋訪", "zh-CN": "启程寻访", "en": "Beginner"}, Pity: endfieldStandardPity{}},
			// Joint pool (collab) exists in the live pool_type enum. Its exact pity
			// rule is unverified → standard-pity placeholder so its pulls still count
			// and display; refine if a live record set shows different behaviour.
			{Key: "joint", Label: core.LocalizedString{"zh-TW": "特殊尋訪", "zh-CN": "特殊寻访", "en": "Special"}, Pity: endfieldStandardPity{}, Limited: true},
			// Weapon pity = standard placeholder per spec §12.1 (weapons are 4/5/6★ so
			// headline=6 resets correctly; exact weapon guarantee rule unverified).
			{Key: "weapon", Label: core.LocalizedString{"zh-TW": "武庫申領", "zh-CN": "武库申领", "en": "Armory"}, Pity: endfieldStandardPity{}, Limited: true},
		},
		StandardPool: endfieldStandardPool,
		PullPrice:    endfieldPullPrice, Currency: "endfield_oroberyl", ExpectedPity: endfieldExpectedPity,
	}
}

var _ core.GachaProvider = (*Provider)(nil)
var _ core.GachaCredentialProvider = (*Provider)(nil)
var _ core.GachaLoginProvider = (*Provider)(nil)

const (
	endfieldGrantCode    = "3dacefa138426cfe"                 // GLOBAL endfield OAuth grant appCode (distinct from news endfieldAppCode)
	endfieldUA           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.6422.112 Safari/537.36"
	endfieldLoginAppCode = "6eb76d4e13aa36e6"                 // skport/passport login appCode (distinct from endfieldGrantCode)
	endfieldDeviceID     = "4ee4cbe1502437b081d2d8cd7d0d3338" // synthesized stable device id
)

// LoginByEmailPassword exchanges plaintext email+password for a durable passport
// token (the value FetchGachaWithCredential consumes). The password is used only
// for this request and never stored. status!=0 → ErrGachaLoginFailed.
func (p *Provider) LoginByEmailPassword(ctx context.Context, email, password string) (core.GachaLoginResult, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(ctx, "POST", p.oauthBase+"/user/auth/v1/token_by_email_password", bytes.NewReader(body))
	if err != nil {
		return core.GachaLoginResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", endfieldUA)
	req.Header.Set("X-AppCode", endfieldLoginAppCode)
	req.Header.Set("X-DeviceId", endfieldDeviceID)
	req.Header.Set("X-DeviceType", "7")
	req.Header.Set("X-DeviceModel", "Edge")
	req.Header.Set("X-OSVer", "Windows")
	req.Header.Set("X-Language", "zh-tw")
	req.Header.Set("Origin", "https://www.skport.com")
	req.Header.Set("Referer", "https://www.skport.com/")
	p.logger.Info("ef login: requesting")
	loginStart := time.Now()
	resp, err := p.httpClient().Do(req)
	if err != nil {
		p.logger.Warn("ef login: request error", "err", err, "dur_ms", time.Since(loginStart).Milliseconds())
		return core.GachaLoginResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		p.logger.Warn("ef login: non-200", "http", resp.StatusCode, "dur_ms", time.Since(loginStart).Milliseconds())
		return core.GachaLoginResult{}, core.ErrGachaLoginFailed
	}
	var r struct {
		Status int `json:"status"`
		Data   *struct {
			Token string `json:"token"`
			HgID  string `json:"hgId"`
			Email string `json:"email"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		p.logger.Warn("ef login: decode error", "err", err, "dur_ms", time.Since(loginStart).Milliseconds())
		return core.GachaLoginResult{}, err
	}
	p.logger.Info("ef login: response", "http", resp.StatusCode, "api_status", r.Status,
		"has_data", r.Data != nil, "has_token", r.Data != nil && r.Data.Token != "",
		"dur_ms", time.Since(loginStart).Milliseconds())
	if r.Status != 0 || r.Data == nil || r.Data.Token == "" {
		return core.GachaLoginResult{}, core.ErrGachaLoginFailed
	}
	return core.GachaLoginResult{Token: r.Data.Token, HgID: r.Data.HgID, Email: r.Data.Email}, nil
}

// FetchGacha (URL path) is unused for Endfield — auth is credential-based. Kept to
// satisfy core.GachaProvider; App branches to FetchGachaWithCredential first.
func (p *Provider) FetchGacha(_ context.Context, gid core.GameID, _, _ string) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	return core.GachaFetchResult{}, core.ErrGachaCredentialRequired
}

// flexStr unmarshals a JSON value that may be sent as either a string or a number
// (the Gryphline APIs are untyped JS; ids/server/rarity-ish fields vary).
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		*f = flexStr(strings.Trim(s, `"`))
		return nil
	}
	*f = flexStr(s) // bare number → its text
	return nil
}

func (p *Provider) httpClient() *http.Client {
	if p.client != nil {
		return p.client
	}
	return http.DefaultClient
}

// efPostJSON POSTs body to url and decodes the JSON response into out.
func (p *Provider) efPostJSON(ctx context.Context, rawURL string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", endfieldUA)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("endfield POST %s status %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// efGrant exchanges the durable account_token for a short-lived OAuth token.
func (p *Provider) efGrant(ctx context.Context, accountToken string) (string, error) {
	body, _ := json.Marshal(map[string]any{"token": accountToken, "appCode": endfieldGrantCode, "type": 1})
	var r struct {
		Status int `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := p.efPostJSON(ctx, p.oauthBase+"/user/oauth2/v2/grant", body, &r); err != nil {
		return "", err
	}
	if r.Status != 0 || r.Data.Token == "" {
		return "", core.ErrGachaCredentialExpired
	}
	return r.Data.Token, nil
}

// Named binding types (cleaner than an inline anon struct, and reused by the
// app-pick helper).
type bindingResp struct {
	Status int    `json:"status"`
	Msg    string `json:"msg"`
	Data   struct {
		List []bindingApp `json:"list"`
	} `json:"data"`
}
type bindingApp struct {
	AppCode     string        `json:"appCode"`
	BindingList []bindingAcct `json:"bindingList"`
}
type bindingAcct struct {
	UID       flexStr       `json:"uid"`
	IsDefault bool          `json:"isDefault"`
	Roles     []bindingRole `json:"roles"`
}
type bindingRole struct {
	RoleID    flexStr `json:"roleId"`
	ServerID  flexStr `json:"serverId"`
	IsDefault bool    `json:"isDefault"`
}

// efBindingGet does one binding_list GET with the given token param name.
func (p *Provider) efBindingGet(ctx context.Context, oauth, tokenParam string, out *bindingResp) error {
	q := url.Values{}
	q.Set(tokenParam, oauth)
	q.Set("appCode", "endfield")
	req, err := http.NewRequestWithContext(ctx, "GET", p.bindingBase+"/account/binding/v1/binding_list?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", endfieldUA)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("endfield binding_list status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// efBinding returns the default binding's hashed uid + default role's roleId +
// serverId. Replicates the AiverAiva quirk: if lowercase "token" fails with a
// base64 msg, retry once with capitalized "Token" (spec §5.2).
func (p *Provider) efBinding(ctx context.Context, oauth string) (uid, roleID, serverID string, err error) {
	for _, param := range []string{"token", "Token"} {
		var r bindingResp
		if err = p.efBindingGet(ctx, oauth, param, &r); err != nil {
			return "", "", "", err
		}
		if r.Status != 0 {
			if param == "token" && strings.Contains(strings.ToLower(r.Msg), "base64") {
				continue // retry with capitalized Token
			}
			return "", "", "", core.ErrGachaCredentialExpired
		}
		return pickDefaultRole(r)
	}
	return "", "", "", core.ErrGachaCredentialExpired
}

// pickDefaultRole selects the endfield app (else first non-empty), its default
// binding (else first), and that binding's default role (else first).
func pickDefaultRole(r bindingResp) (uid, roleID, serverID string, err error) {
	var app *bindingApp
	for i := range r.Data.List {
		if strings.Contains(strings.ToLower(r.Data.List[i].AppCode), "endfield") {
			app = &r.Data.List[i]
			break
		}
		if app == nil && len(r.Data.List[i].BindingList) > 0 {
			app = &r.Data.List[i]
		}
	}
	if app == nil || len(app.BindingList) == 0 {
		return "", "", "", core.ErrGachaNoGameRole
	}
	bind := app.BindingList[0]
	for i := range app.BindingList {
		if app.BindingList[i].IsDefault {
			bind = app.BindingList[i]
			break
		}
	}
	if len(bind.Roles) == 0 {
		return "", "", "", core.ErrGachaNoGameRole
	}
	role := bind.Roles[0]
	for i := range bind.Roles {
		if bind.Roles[i].IsDefault {
			role = bind.Roles[i]
			break
		}
	}
	return string(bind.UID), string(role.RoleID), string(role.ServerID), nil
}

// efU8Token mints the short-lived u8_token used for all record-API calls.
func (p *Provider) efU8Token(ctx context.Context, oauth, uid string) (string, error) {
	body, _ := json.Marshal(map[string]string{"uid": uid, "token": oauth})
	var r struct {
		Status int `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := p.efPostJSON(ctx, p.bindingBase+"/account/binding/v1/u8_token_by_uid", body, &r); err != nil {
		return "", err
	}
	if r.Status != 0 || r.Data.Token == "" {
		return "", core.ErrGachaCredentialExpired
	}
	return r.Data.Token, nil
}

// endfieldPool is one record stream to paginate.
type endfieldPool struct {
	endpoint  string // "/api/record/char" or "/api/record/weapon"
	poolType  string // char pool_type enum; "" for weapon (single pass)
	bannerKey string
	itemType  string // "char" | "weapon"
}

// endfieldCharPools are the 4 character pools.
var endfieldCharPools = []endfieldPool{
	{"/api/record/char", "E_CharacterGachaPoolType_Special", "special", "char"},
	{"/api/record/char", "E_CharacterGachaPoolType_Standard", "standard", "char"},
	{"/api/record/char", "E_CharacterGachaPoolType_Beginner", "beginner", "char"},
	{"/api/record/char", "E_CharacterGachaPoolType_Joint", "joint", "char"},
}

// endfieldAllPools = the 4 char pools + the single weapon pass (no pool_type).
var endfieldAllPools = append(append([]endfieldPool{}, endfieldCharPools...),
	endfieldPool{"/api/record/weapon", "", "weapon", "weapon"})

type endfieldRecordResp struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Message string `json:"message"` // some endpoints use "message"
	Data    *struct {
		List []struct {
			SeqID      flexStr `json:"seqId"`
			Rarity     int     `json:"rarity"`
			GachaTs    flexStr `json:"gachaTs"`
			IsFree     bool    `json:"isFree"`
			CharName   string  `json:"charName"`
			WeaponName string  `json:"weaponName"`
			PoolID     flexStr `json:"poolId"`
			PoolName   string  `json:"poolName"`
		} `json:"list"`
		HasMore bool `json:"hasMore"`
	} `json:"data"`
}

// parseEndfieldTime converts a ms-epoch string to the shared canonical layout.
func parseEndfieldTime(ms string) (string, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(ms), 10, 64)
	if err != nil {
		return "", err
	}
	return time.UnixMilli(n).Format("2006-01-02 15:04:05"), nil
}

// efFetchRecords paginates the given pools and returns normalized pulls (UID set
// by the caller to the roleId).
func (p *Provider) efFetchRecords(ctx context.Context, u8, serverID, lang string, known map[string]bool) (core.GachaFetchResult, error) {
	return p.efFetchPools(ctx, u8, serverID, lang, endfieldAllPools, known)
}

// efFetchPools paginates each pool newest-first. known is the set of seqIds already
// stored for this account; a pool stops as soon as it reaches a known record
// (incremental sync — only new pulls are fetched). A nil/empty known set fetches
// the full history (first sync).
func (p *Provider) efFetchPools(ctx context.Context, u8, serverID, lang string, pools []endfieldPool, known map[string]bool) (core.GachaFetchResult, error) {
	out := core.GachaFetchResult{Pulls: []core.GachaPull{}}
	for i, pool := range pools {
		p.logger.Info("ef records: pool", "index", i+1, "total", len(pools), "banner", pool.bannerKey)
		seqID := ""
		for page := 0; page < endfieldMaxPages; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			core.ReportGachaProgress(ctx, core.GachaProgress{
				BannerKey: pool.bannerKey, Page: page + 1, PoolIndex: i + 1, PoolTotal: len(pools),
			})
			q := url.Values{}
			q.Set("token", u8)
			q.Set("lang", lang)
			q.Set("server_id", serverID)
			if pool.poolType != "" {
				q.Set("pool_type", pool.poolType)
			}
			if seqID != "" {
				q.Set("seq_id", seqID)
			}
			req, err := http.NewRequestWithContext(ctx, "GET", p.recordAPIBase+pool.endpoint+"?"+q.Encode(), nil)
			if err != nil {
				return out, err
			}
			req.Header.Set("User-Agent", endfieldUA)
			p.logger.Debug("ef records: page requesting", "pool", i+1, "page", page+1, "has_cursor", seqID != "")
			pageStart := time.Now()
			resp, err := p.httpClient().Do(req)
			if err != nil {
				p.logger.Warn("ef records: page http error", "pool", i+1, "page", page+1, "err", err, "dur_ms", time.Since(pageStart).Milliseconds())
				return out, err
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				return out, fmt.Errorf("endfield %s status %d", pool.endpoint, resp.StatusCode)
			}
			var r endfieldRecordResp
			if err := json.Unmarshal(body, &r); err != nil {
				return out, err
			}
			p.logger.Debug("ef records: page done", "pool", i+1, "page", page+1, "code", r.Code,
				"n", func() int { if r.Data != nil { return len(r.Data.List) }; return -1 }(),
				"has_more", r.Data != nil && r.Data.HasMore, "dur_ms", time.Since(pageStart).Milliseconds())
			if r.Code == -101 || r.Code == 40100 { // bad/expired u8_token
				return out, core.ErrGachaCredentialExpired
			}
			if r.Code != 0 || r.Data == nil {
				return out, core.ErrGachaURLUnavailable
			}
			reachedKnown := false
			for _, e := range r.Data.List {
				id := string(e.SeqID)
				if known[id] { // nil map → always false (full first sync)
					reachedKnown = true
					break
				}
				name := e.CharName
				if pool.itemType == "weapon" {
					name = e.WeaponName
				}
				ts, _ := parseEndfieldTime(string(e.GachaTs))
				out.Pulls = append(out.Pulls, core.GachaPull{
					ID:        id,
					BannerKey: pool.bannerKey,
					ItemType:  pool.itemType,
					Rank:      e.Rarity,
					Name:      name,
					Time:      ts,
					IsFree:    pool.itemType == "char" && e.IsFree,
					PoolID:    string(e.PoolID),
					PoolName:  e.PoolName,
				})
			}
			// Incremental sync: records are newest-first, so once we reach a seqId we
			// already have, everything older is known too — stop paginating this pool.
			if reachedKnown {
				break
			}
			if !r.Data.HasMore || len(r.Data.List) == 0 {
				break
			}
			seqID = string(r.Data.List[len(r.Data.List)-1].SeqID)
			if p.pageDelay > 0 {
				// politeness jitter (~100–500ms) between pages to avoid tripping rate
				// limiting on the record API.
				time.Sleep(p.pageDelay + time.Duration(rand.Intn(400))*time.Millisecond)
			}
		}
	}
	return out, nil
}

// FetchGachaWithCredential runs the full chain and returns pulls + roleId uid.
// known is the set of seqIds already stored for this account; pools stop early
// once they reach a known record (incremental sync). Pass nil for a full fetch.
func (p *Provider) FetchGachaWithCredential(ctx context.Context, gid core.GameID, credential, lang string, known map[string]bool) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	if credential == "" {
		return core.GachaFetchResult{}, core.ErrGachaCredentialRequired
	}
	if lang == "" {
		lang = "en-us"
	}
	p.logger.Info("ef fetch: start", "lang", lang)
	t := time.Now()
	oauth, err := p.efGrant(ctx, credential)
	if err != nil {
		p.logger.Warn("ef fetch: efGrant failed", "err", err, "dur_ms", time.Since(t).Milliseconds())
		return core.GachaFetchResult{}, err
	}
	p.logger.Info("ef fetch: efGrant ok", "dur_ms", time.Since(t).Milliseconds())

	t = time.Now()
	uid, roleID, serverID, err := p.efBinding(ctx, oauth)
	if err != nil {
		p.logger.Warn("ef fetch: efBinding failed", "err", err, "dur_ms", time.Since(t).Milliseconds())
		return core.GachaFetchResult{}, err
	}
	p.logger.Info("ef fetch: efBinding ok", "uid_present", uid != "", "role_present", roleID != "", "server", serverID, "dur_ms", time.Since(t).Milliseconds())

	t = time.Now()
	u8, err := p.efU8Token(ctx, oauth, uid)
	if err != nil {
		p.logger.Warn("ef fetch: efU8Token failed", "err", err, "dur_ms", time.Since(t).Milliseconds())
		return core.GachaFetchResult{}, err
	}
	p.logger.Info("ef fetch: efU8Token ok", "dur_ms", time.Since(t).Milliseconds())

	t = time.Now()
	res, err := p.efFetchRecords(ctx, u8, serverID, lang, known)
	if err != nil {
		p.logger.Warn("ef fetch: records failed", "err", err, "dur_ms", time.Since(t).Milliseconds())
		return core.GachaFetchResult{}, err
	}
	p.logger.Info("ef fetch: records ok", "pulls", len(res.Pulls), "dur_ms", time.Since(t).Milliseconds())
	res.UID = roleID
	return res, nil
}
