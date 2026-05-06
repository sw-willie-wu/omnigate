package hoyoverse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"omnigate/internal/core"
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
