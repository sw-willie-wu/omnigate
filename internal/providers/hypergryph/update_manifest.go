package hypergryph

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

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
		TotalSize    string `json:"total_size"`
		FilePath     string `json:"file_path"`      // per-file CDN base — Option A consumes this
		GameFilesMD5 string `json:"game_files_md5"` // aggregate MD5 (informational)
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

// manifestNode is one line of the decrypted game_files manifest
// (Collapse HgManifestNode). Path uses forward slashes.
type manifestNode struct {
	Path string `json:"path"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}

// gameFilesURL is the per-file CDN manifest endpoint: <pkg.file_path>/game_files.
func gameFilesURL(filePath string) string {
	return strings.TrimRight(filePath, "/") + "/game_files"
}

// fileURL builds a per-file download URL: <pkg.file_path>/<relPath> with spaces
// percent-encoded. relPath keeps forward slashes (URL path), so use string concat
// rather than filepath.Join (which would backslash on Windows).
func fileURL(filePath, relPath string) string {
	return strings.TrimRight(filePath, "/") + "/" + strings.ReplaceAll(relPath, " ", "%20")
}

// fetchGameFilesManifest GETs {filePath}/game_files, AES-decrypts it (same key/IV
// as config.ini), and parses the JSON-lines manifest. Status → core.UpdateError.
func fetchGameFilesManifest(ctx context.Context, client *http.Client, filePath string) ([]manifestNode, error) {
	urlStr := gameFilesURL(filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "omnigate/0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": err.Error()}}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &core.UpdateError{Code: "auth_failed", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode/100 == 5:
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "status": strconv.Itoa(resp.StatusCode)}}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.UpdateError{Code: "network", Retryable: true, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": err.Error()}}
	}
	plain, derr := decryptAESCBC(body)
	if derr != nil {
		return nil, &core.UpdateError{Code: "corrupt", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr), "reason": "game_files decrypt failed"}}
	}
	return parseGameFilesManifest(plain)
}

// parseGameFilesManifest parses decrypted JSON-lines into manifestNode slice.
// Blank lines + unparseable lines are skipped (mirrors HgGameRepairer). The
// config.ini entry is skipped — its version is written separately (spec §5/§6).
func parseGameFilesManifest(plain []byte) ([]manifestNode, error) {
	var out []manifestNode
	sc := bufio.NewScanner(bytes.NewReader(plain))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // tolerate long lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var n manifestNode
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			continue
		}
		if n.Path == "" || strings.EqualFold(n.Path, "config.ini") {
			continue
		}
		out = append(out, n)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("game_files scan: %w", err)
	}
	return out, nil
}

// verifyWorkers controls parallelism in filterChangedFiles (mirror kuro).
const verifyWorkers = 4

// filterChangedFiles drops manifest nodes whose on-disk file matches size+MD5.
// Mirrors kuro update_manifest.go filterChangedFiles, over manifestNode. Output
// is sorted by Path. onProgress(done, total) fires after each file (may be nil).
// ctx is checked per file so cancel mid-verify takes effect at the next boundary.
func filterChangedFiles(ctx context.Context, installDir, filePath string, nodes []manifestNode, logger *slog.Logger, onProgress func(done, total int)) []core.FileTask {
	total := len(nodes)
	if total == 0 {
		return nil
	}
	results := make([]*core.FileTask, total)
	jobs := make(chan int, total)
	for i := range nodes {
		jobs <- i
	}
	close(jobs)

	var (
		progressMu sync.Mutex
		done       int
	)
	emit := func() {
		progressMu.Lock()
		done++
		d := done
		progressMu.Unlock()
		if onProgress != nil {
			onProgress(d, total)
		}
	}

	mk := func(n manifestNode) *core.FileTask {
		return &core.FileTask{Path: n.Path, Hash: n.MD5, Size: n.Size, URL: fileURL(filePath, n.Path)}
	}

	var wg sync.WaitGroup
	wg.Add(verifyWorkers)
	for w := 0; w < verifyWorkers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				n := nodes[i]
				full := filepath.Join(installDir, filepath.FromSlash(n.Path))
				fi, err := os.Stat(full)
				if err != nil || fi.IsDir() || fi.Size() != n.Size {
					results[i] = mk(n)
					emit()
					continue
				}
				h, herr := md5File(full)
				if herr != nil {
					if logger != nil {
						logger.Debug("md5 check failed; will re-download", "path", n.Path, "err", herr)
					}
					results[i] = mk(n)
					emit()
					continue
				}
				if h != n.MD5 {
					results[i] = mk(n)
				}
				emit()
			}
		}()
	}
	wg.Wait()

	out := make([]core.FileTask, 0, total)
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// md5File returns the lowercase-hex MD5 of a file's contents.
func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
