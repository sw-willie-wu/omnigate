package hoyoverse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"omnigate/internal/core"
)

type rawGamePackages struct {
	GamePackages []struct {
		Game struct {
			ID  string `json:"id"`
			Biz string `json:"biz"`
		} `json:"game"`
		Main struct {
			Major struct{ Version string `json:"version"` } `json:"major"`
		} `json:"main"`
		PreDownload *struct {
			Major struct{ Version string `json:"version"` } `json:"major"`
		} `json:"pre_download,omitempty"`
	} `json:"game_packages"`
}

// fetchVersion returns version info for one game (looked up by API game id).
// currentLocal is the caller-side detected version (or "" if unknown).
func (c *apiClient) fetchVersion(ctx context.Context, apiGameID, currentLocal string) (core.VersionInfo, error) {
	// HoYoverse expects literal "game_ids[]=<id>" — url.Values.Encode()
	// percent-encodes the brackets which the API rejects. The value must be
	// the API game id (e.g. "gopR6Cufr3"), NOT the biz code.
	qs := "launcher_id=" + url.QueryEscape(LauncherID) + "&game_ids[]=" + url.QueryEscape(apiGameID)
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/getGamePackages?"+qs, nil)
	if err != nil {
		return core.VersionInfo{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return core.VersionInfo{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return core.VersionInfo{}, err
	}
	var raw rawGamePackages
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return core.VersionInfo{}, err
	}
	for _, p := range raw.GamePackages {
		if p.Game.ID != apiGameID {
			continue
		}
		info := core.VersionInfo{
			Current: currentLocal,
			Latest:  p.Main.Major.Version,
		}
		if info.Current == "" {
			info.Current = info.Latest
		}
		if p.PreDownload != nil && p.PreDownload.Major.Version != "" {
			info.Predownload = &core.PredownloadInfo{TargetVersion: p.PreDownload.Major.Version}
		}
		return info, nil
	}
	return core.VersionInfo{}, fmt.Errorf("game id %q not in response", apiGameID)
}
