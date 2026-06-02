package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"omnigate/internal/core"
)

// Endfield Global (region "os") launcher API constants. Verified against
// daydreamer-json/ak-endfield-api-archive — see the research doc.
const (
	gameAppCode        = "YDUTE5gscDZ229CW"
	launcherAppCode    = "TiaytKBUIEdoEwRT"
	apiChannel         = "6"
	apiSubChannel      = "6"
	apiLauncherSubChan = "6"
	defaultAPIBase     = "https://launcher.gryphline.com/api"
)

// apiBase is overridable in tests via SetAPIBaseURL. NOTE: this is a
// PACKAGE-LEVEL var + package-level func, intentionally NOT hoyoverse's
// method form (`func (p *Provider) SetAPIBaseURL`). The package-level form is
// chosen so package tests can override without constructing a Provider. Do NOT
// copy hoyoverse's method signature — the tests call SetAPIBaseURL(...) as a
// bare function.
var apiBase = defaultAPIBase

// SetAPIBaseURL overrides the launcher API base for integration tests. Pass ""
// to reset to production.
func SetAPIBaseURL(base string) {
	if base == "" {
		apiBase = defaultAPIBase
		return
	}
	apiBase = base
}

// randSegRe matches the per-build "<ver>_<rand>" path segment for redaction.
var randSegRe = regexp.MustCompile(`(/[0-9.]+_)[A-Za-z0-9]{8,}(/)`)

func sanitizeURL(s string) string {
	return randSegRe.ReplaceAllString(s, "${1}<RAND>${2}")
}

// getLatestResponse is the get_latest body (flat — no rsp wrapper; see research
// doc). Phase A uses only Version; Phase B adds pkg/pack consumption.
type getLatestResponse struct {
	Action         int    `json:"action"`
	State          int    `json:"state"`
	LauncherAction int    `json:"launcher_action"`
	Version        string `json:"version"`
	ClientVersion  string `json:"client_version"`
	RequestVersion string `json:"request_version"`
	Pkg            struct {
		Packs []struct {
			URL         string `json:"url"`
			MD5         string `json:"md5"`
			PackageSize string `json:"package_size"`
		} `json:"packs"`
		TotalSize string `json:"total_size"`
	} `json:"pkg"`
}

func decodeGetLatest(body []byte) (*getLatestResponse, error) {
	var out getLatestResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("get_latest parse: %w", err)
	}
	return &out, nil
}

func getLatestURL(version string) string {
	q := url.Values{}
	q.Set("appcode", gameAppCode)
	q.Set("launcher_appcode", launcherAppCode)
	q.Set("channel", apiChannel)
	q.Set("sub_channel", apiSubChannel)
	q.Set("launcher_sub_channel", apiLauncherSubChan)
	if version != "" {
		q.Set("version", version)
	}
	return apiBase + "/game/get_latest?" + q.Encode()
}

// fetchGetLatest GETs get_latest and parses it. Status → core.UpdateError
// (404 manifest_not_found; 401/403 auth_failed; 5xx network).
func fetchGetLatest(ctx context.Context, client *http.Client, version string) (*getLatestResponse, error) {
	urlStr := getLatestURL(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "omnigate/0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get_latest fetch: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &core.UpdateError{Code: "auth_failed", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode/100 == 5:
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode), "reason": "server error"}}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return decodeGetLatest(body)
}
