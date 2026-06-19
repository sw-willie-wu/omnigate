package hypergryph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"omnigate/internal/core"
)

const endfieldHardPity = 80       // official: 6★ hard pity (char)
const endfieldExpectedPity = 62.0 // theoretical avg pulls/6★ for the luck score
const endfieldMilestone = 60      // free-pull carryover milestone (ref repo)
const endfieldPullPrice = 160     // placeholder: real Endfield per-pull cost TBD (currency code "endfield_pull")
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
		PullPrice: endfieldPullPrice, Currency: "endfield_pull", ExpectedPity: endfieldExpectedPity,
	}
}

var _ core.GachaProvider = (*Provider)(nil)

const (
	endfieldGrantCode = "3dacefa138426cfe" // GLOBAL endfield OAuth grant appCode (distinct from news endfieldAppCode)
	endfieldUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.6422.112 Safari/537.36"
)

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
		return "", "", "", core.ErrGachaCredentialExpired
	}
	bind := app.BindingList[0]
	for i := range app.BindingList {
		if app.BindingList[i].IsDefault {
			bind = app.BindingList[i]
			break
		}
	}
	if len(bind.Roles) == 0 {
		return "", "", "", core.ErrGachaCredentialExpired
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
