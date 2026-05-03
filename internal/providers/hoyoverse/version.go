package hoyoverse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"launcher-collection-tmp/internal/core"
)

type rawGamePackages struct {
	GamePackages []struct {
		Game struct{ Biz string `json:"biz"` } `json:"game"`
		Main struct {
			Major struct{ Version string `json:"version"` } `json:"major"`
		} `json:"main"`
		PreDownload *struct {
			Major struct{ Version string `json:"version"` } `json:"major"`
		} `json:"pre_download,omitempty"`
	} `json:"game_packages"`
}

// fetchVersion returns version info for one biz id. currentLocal is the
// caller-side detected version (or "" if unknown).
func (c *apiClient) fetchVersion(ctx context.Context, biz, currentLocal string) (core.VersionInfo, error) {
	q := url.Values{}
	q.Set("launcher_id", LauncherID)
	q.Add("game_ids[]", "")
	// hoyoverse expects game_ids[]= for each; we use single
	// (NOTE: param key is biz id mapped via gameMeta — caller passes biz)
	qs := q.Encode() + "&game_ids[]=" + biz
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
		if p.Game.Biz != biz {
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
	return core.VersionInfo{}, fmt.Errorf("biz %q not in response", biz)
}
