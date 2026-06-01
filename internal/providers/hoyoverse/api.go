package hoyoverse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string, hc *http.Client) *apiClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &apiClient{base: base, http: hc}
}

type apiEnvelope struct {
	Retcode int             `json:"retcode"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type rawBasicInfo struct {
	GameInfoList []struct {
		Game struct {
			ID  string `json:"id"`
			Biz string `json:"biz"`
		} `json:"game"`
		Backgrounds []struct {
			ID         string `json:"id"`
			Background struct{ URL string `json:"url"` } `json:"background"`
			Video      struct{ URL string `json:"url"` } `json:"video"`
			Type       string `json:"type"`
		} `json:"backgrounds"`
	} `json:"game_info_list"`
}

// fetchBasicInfo calls /getAllGameBasicInfo and returns the backgrounds for one game.
func (c *apiClient) fetchBasicInfo(ctx context.Context, apiGameID, lang string) ([]core.Background, error) {
	q := url.Values{}
	q.Set("launcher_id", LauncherID)
	q.Set("language", lang)
	q.Set("game_id", apiGameID)
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getAllGameBasicInfo?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("api %s: http %d", "getAllGameBasicInfo", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if env.Retcode != 0 {
		return nil, fmt.Errorf("api retcode=%d msg=%q", env.Retcode, env.Message)
	}
	var raw rawBasicInfo
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil, err
	}
	if len(raw.GameInfoList) == 0 {
		return nil, errors.New("api returned empty game_info_list")
	}
	out := make([]core.Background, 0, len(raw.GameInfoList[0].Backgrounds))
	for _, b := range raw.GameInfoList[0].Backgrounds {
		bg := core.Background{ImageURL: b.Background.URL, VideoURL: b.Video.URL}
		if b.Type == "BACKGROUND_TYPE_VIDEO" {
			bg.Type = core.BackgroundVideo
		} else {
			bg.Type = core.BackgroundImage
		}
		out = append(out, bg)
	}
	return out, nil
}

type rawGames struct {
	Games []struct {
		Biz     string `json:"biz"`
		Display struct {
			Name string `json:"name"`
			Icon struct{ URL string `json:"url"` } `json:"icon"`
		} `json:"display"`
	} `json:"games"`
}

// rawGameBranches mirrors the /getGameBranches response shape. The main
// branch's `tag` field is the real current latest version for Sophon-migrated
// games (Genshin 6.0+); the legacy /getGamePackages endpoint is frozen there.
type rawGameBranches struct {
	GameBranches []struct {
		Game struct {
			ID  string `json:"id"`
			Biz string `json:"biz"`
		} `json:"game"`
		Main struct {
			PackageID string   `json:"package_id"`
			Branch    string   `json:"branch"`
			Tag       string   `json:"tag"`
			DiffTags  []string `json:"diff_tags"`
		} `json:"main"`
	} `json:"game_branches"`
}

// fetchBranchInfo calls /getGameBranches on p.branchAPIBase and returns the
// full {Main, PreDownload} branch info via sophon.ParseBranches. It is a
// *Provider method (not *apiClient) so SetBranchAPIBaseURL can redirect it to
// an httptest server independently of SetAPIBaseURL ([DEV-3]). Uses
// p.httpClient with the same nil-fallback as fetchGetGamePackages.
func (p *Provider) fetchBranchInfo(ctx context.Context, apiGameID string) (*sophon.BranchInfo, error) {
	base := p.branchAPIBase
	if base == "" {
		base = APIBase
	}
	qs := "launcher_id=" + url.QueryEscape(LauncherID) + "&game_ids[]=" + url.QueryEscape(apiGameID)
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/getGameBranches?"+qs, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getGameBranches: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if env.Retcode != 0 {
		return nil, fmt.Errorf("getGameBranches retcode=%d msg=%q", env.Retcode, env.Message)
	}
	return sophon.ParseBranches(env.Data, apiGameID)
}

// fetchBranchTag returns the main branch's tag for apiGameID. Delegates to
// fetchBranchInfo so CheckVersion keeps working ([DEV-3]).
func (p *Provider) fetchBranchTag(ctx context.Context, apiGameID string) (string, error) {
	bi, err := p.fetchBranchInfo(ctx, apiGameID)
	if err != nil {
		return "", err
	}
	if bi.Main.IsEmpty() {
		return "", fmt.Errorf("getGameBranches: game id %q has empty main branch", apiGameID)
	}
	return bi.Main.Tag, nil
}

func (c *apiClient) fetchGameIcon(ctx context.Context, biz, lang string) (string, error) {
	q := url.Values{}
	q.Set("launcher_id", LauncherID)
	q.Set("language", lang)
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getGames?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", err
	}
	var raw rawGames
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return "", err
	}
	for _, g := range raw.Games {
		if g.Biz == biz {
			return g.Display.Icon.URL, nil
		}
	}
	return "", fmt.Errorf("biz %q not in /getGames response", biz)
}
