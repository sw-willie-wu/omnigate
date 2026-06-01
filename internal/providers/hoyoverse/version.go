package hoyoverse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"omnigate/internal/core"
)

// HypPackageData represents one zip blob in either game_pkgs[] or audio_pkgs[].
// Field types follow Collapse's HypPackageData mapping. Note: HoYoverse API
// returns size + decompressed_size as JSON strings (not numbers); we decode
// to string first and convert in parseGamePackagesResponse.
type HypPackageData struct {
	Language             string `json:"language,omitempty"`         // audio_pkgs only
	URL                  string `json:"url"`                        // package download URL
	MD5                  string `json:"md5"`
	Size                 int64  `json:"-"`                          // populated post-decode
	DecompressedSize     int64  `json:"-"`
	SizeRaw              string `json:"size"`
	DecompressedSizeRaw  string `json:"decompressed_size"`
}

// HypPackageInfo is one entry in main.major / main.patches[i] / pre_download.major.
type HypPackageInfo struct {
	Version    string           `json:"version"`
	GamePkgs   []HypPackageData `json:"game_pkgs"`
	AudioPkgs  []HypPackageData `json:"audio_pkgs"`
	ResListURL string           `json:"res_list_url,omitempty"`
}

// HypGamePackagesMain wraps the {major, patches[]} pair.
type HypGamePackagesMain struct {
	Major   HypPackageInfo   `json:"major"`
	Patches []HypPackageInfo `json:"patches"`
}

// HypGameEntry corresponds to one `data.game_packages[i]`.
type HypGameEntry struct {
	Game struct {
		ID  string `json:"id"`
		Biz string `json:"biz"`
	} `json:"game"`
	Main        HypGamePackagesMain  `json:"main"`
	PreDownload *HypGamePackagesMain `json:"pre_download,omitempty"`
}

// HypGetGamePackagesResponse is the top-level shape of the
// `getGamePackages` response.
type HypGetGamePackagesResponse struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    struct {
		GamePackages []HypGameEntry `json:"game_packages"`
	} `json:"data"`
	// ManifestETag is populated by the caller from HTTP ETag header (not part
	// of the JSON wire format).
	ManifestETag string `json:"-"`
}

// parseGamePackagesResponse decodes JSON + post-converts string-typed
// numerics to int64. Used by tests; production fetcher calls this with
// the env.Data (the "data" field extracted from the apiEnvelope).
func parseGamePackagesResponse(body []byte) (*HypGetGamePackagesResponse, error) {
	var data struct {
		GamePackages []HypGameEntry `json:"game_packages"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("unmarshal getGamePackages: %w", err)
	}
	resp := &HypGetGamePackagesResponse{
		Data: data,
	}
	convert := func(p *HypPackageData) error {
		if p.SizeRaw != "" {
			n, err := strconv.ParseInt(p.SizeRaw, 10, 64)
			if err != nil {
				return fmt.Errorf("size %q: %w", p.SizeRaw, err)
			}
			p.Size = n
		}
		if p.DecompressedSizeRaw != "" {
			n, err := strconv.ParseInt(p.DecompressedSizeRaw, 10, 64)
			if err != nil {
				return fmt.Errorf("decompressed_size %q: %w", p.DecompressedSizeRaw, err)
			}
			p.DecompressedSize = n
		}
		return nil
	}
	convertInfo := func(pi *HypPackageInfo) error {
		for i := range pi.GamePkgs {
			if err := convert(&pi.GamePkgs[i]); err != nil {
				return err
			}
		}
		for i := range pi.AudioPkgs {
			if err := convert(&pi.AudioPkgs[i]); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range resp.Data.GamePackages {
		entry := &resp.Data.GamePackages[i]
		if err := convertInfo(&entry.Main.Major); err != nil {
			return nil, fmt.Errorf("entry %d main.major: %w", i, err)
		}
		for j := range entry.Main.Patches {
			if err := convertInfo(&entry.Main.Patches[j]); err != nil {
				return nil, fmt.Errorf("entry %d patches[%d]: %w", i, j, err)
			}
		}
		if entry.PreDownload != nil {
			if err := convertInfo(&entry.PreDownload.Major); err != nil {
				return nil, fmt.Errorf("entry %d pre_download.major: %w", i, err)
			}
			for j := range entry.PreDownload.Patches {
				if err := convertInfo(&entry.PreDownload.Patches[j]); err != nil {
					return nil, fmt.Errorf("entry %d pre_download.patches[%d]: %w", i, j, err)
				}
			}
		}
	}
	return resp, nil
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
	parsed, err := parseGamePackagesResponse(env.Data)
	if err != nil {
		return core.VersionInfo{}, err
	}
	for _, p := range parsed.Data.GamePackages {
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
