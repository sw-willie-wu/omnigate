package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"omnigate/internal/core"
)

// AppCred is the hardcoded `appId_appKey` for WuWa Global / live channel.
// Verified per .claude/research/m3a-kuro-update-protocol.md (2026-05-05);
// identical on every install (NOT per-machine, NOT extracted from cache).
const AppCred = "50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c"

// accountIDRe matches Kuro accountID format `50004_<alnum>` for sanitization.
var accountIDRe = regexp.MustCompile(`50004_[a-zA-Z0-9]+`)

// deviceIDRe matches 32-char lowercase hex, the device ID format observed
// during M2 BG research.
var deviceIDRe = regexp.MustCompile(`[a-f0-9]{32}`)

// sanitizeURL redacts PII from a URL before logging or embedding in
// UpdateError.Params.
func sanitizeURL(s string) string {
	s = accountIDRe.ReplaceAllString(s, "<ACCOUNT_ID>")
	s = deviceIDRe.ReplaceAllString(s, "<DEVICE_ID>")
	return s
}

// indexJSONURL is the entrypoint for kurogames update protocol — the catalog
// of CDNs + the per-version indexFile.json pointer. WuWa Global / live channel.
// A var (not func) so tests can point the manifest fetch at an httptest server.
var indexJSONURL = func() string {
	return "https://prod-alicdn-gamestarter.kurogame.com/launcher/game/G153/" + AppCred + "/index.json"
}

// --- JSON shapes (two-step manifest per research) ---

// indexRaw is the top-level launcher index.
type indexRaw struct {
	Default struct {
		Version string         `json:"version"`
		CDNList []cdnEntry     `json:"cdnList"`
		Config  indexConfigRaw `json:"config"`
	} `json:"default"`
	Predownload *struct {
		Version string         `json:"version"`
		CDNList []cdnEntry     `json:"cdnList"`
		Config  indexConfigRaw `json:"config"`
	} `json:"predownload,omitempty"` // present only when active predl is published
	PredownloadSwitch int      `json:"predownloadSwitch"`
	KeyFileCheckList  []string `json:"keyFileCheckList"`
}

type cdnEntry struct {
	URL string `json:"url"`
	P   int    `json:"P"`
	K1  int    `json:"K1"`
	K2  int    `json:"K2"`
}

type indexConfigRaw struct {
	Version        string           `json:"version"`
	IndexFile      string           `json:"indexFile"`
	IndexFileMD5   string           `json:"indexFileMd5"`
	BaseURL        string           `json:"baseUrl"`
	Size           int64            `json:"size"`
	UnCompressSize int64            `json:"unCompressSize"`
	PatchType      string           `json:"patchType"`
	PatchConfig    []indexConfigRaw `json:"patchConfig,omitempty"`
}

// indexFileRaw is the per-version file manifest discovered via indexConfigRaw.IndexFile.
type indexFileRaw struct {
	Resource []manifestFileRaw `json:"resource"`
}

// manifestFileRaw is one file entry. URL = <cdn>/<baseUrl OR fromFolder><dest>.
type manifestFileRaw struct {
	Dest       string      `json:"dest"`
	MD5        string      `json:"md5"`
	Size       int64       `json:"size"`
	ChunkInfos []chunkInfo `json:"chunkInfos,omitempty"`
	FromFolder string      `json:"fromFolder,omitempty"`
}

type chunkInfo struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	MD5   string `json:"md5"`
}

// --- HTTP fetchers ---

// fetchIndex GETs index.json and returns parsed body + an ETag-equivalent.
// index.json doesn't ship a real ETag — uses Last-Modified header as the
// drift token (falls back to body-MD5 if absent).
func fetchIndex(ctx context.Context, client *http.Client, url string) (*indexRaw, string, error) {
	body, etag, err := fetchJSON(ctx, client, url)
	if err != nil {
		return nil, "", err
	}
	var idx indexRaw
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, "", fmt.Errorf("index json parse: %w", err)
	}
	return &idx, etag, nil
}

// fetchIndexFile GETs the indexFile.json discovered from index.json.
// Response's ETag header IS the indexFileMd5 (per research) — caller can
// validate against indexConfigRaw.IndexFileMD5 from the parent index.
func fetchIndexFile(ctx context.Context, client *http.Client, url string) (*indexFileRaw, string, error) {
	body, etag, err := fetchJSON(ctx, client, url)
	if err != nil {
		return nil, "", err
	}
	var idxFile indexFileRaw
	if err := json.Unmarshal(body, &idxFile); err != nil {
		return nil, "", fmt.Errorf("indexFile json parse: %w", err)
	}
	return &idxFile, etag, nil
}

// fetchJSON shares the status-code → core.UpdateError mapping between
// fetchIndex and fetchIndexFile. Returns (body, etagOrLastModified, err).
// 4xx: manifest_not_found / auth_failed; 5xx: network (retryable).
func fetchJSON(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("manifest fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", &core.UpdateError{
			Code:      "manifest_not_found",
			Retryable: false,
			Params:    map[string]string{"url": sanitizeURL(url)},
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "", &core.UpdateError{
			Code:      "auth_failed",
			Retryable: false,
			Params:    map[string]string{"url": sanitizeURL(url), "status": fmt.Sprint(resp.StatusCode)},
		}
	}
	if resp.StatusCode/100 == 5 {
		return nil, "", &core.UpdateError{
			Code:      "network",
			Retryable: true,
			Params:    map[string]string{"url": sanitizeURL(url), "status": fmt.Sprint(resp.StatusCode), "reason": "server error"},
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		etag = resp.Header.Get("Last-Modified")
	}
	if etag == "" {
		// Fallback: hash the body so we can still detect drift.
		h := md5.Sum(body)
		etag = "body-md5:" + hex.EncodeToString(h[:])
	}
	return body, etag, nil
}

// --- CDN + version helpers ---

// pickCDN selects the lowest-priority (best) CDN. Stable ordering for tests:
// ties broken by url string sort.
func pickCDN(list []cdnEntry) string {
	if len(list) == 0 {
		return "https://hw-pcdownload-qcloud.aki-game.net/" // safe default per research
	}
	best := list[0]
	for _, c := range list[1:] {
		if c.P < best.P || (c.P == best.P && c.URL < best.URL) {
			best = c
		}
	}
	return best.URL
}

// pickIndexFileForVersion returns the indexConfigRaw matching the current
// install version (patch path), or default.config (full install) if no
// patchConfig entry matches. Returns (config, isPatch).
func pickIndexFileForVersion(idx *indexRaw, currentVersion string) (indexConfigRaw, bool) {
	if idx.Default.Config.PatchType == "patch" && currentVersion != "" {
		for _, p := range idx.Default.Config.PatchConfig {
			if p.Version == currentVersion {
				return p, true
			}
		}
	}
	return idx.Default.Config, false
}

// pickPredownloadIndexFile returns the predl indexConfig matching the current
// install version (patch path), or the predl default config (full) if no
// patchConfig entry matches. Predl mirror of pickIndexFileForVersion; takes the
// predl config directly (idx.Predownload.Config).
func pickPredownloadIndexFile(predlCfg indexConfigRaw, currentVersion string) indexConfigRaw {
	if predlCfg.PatchType == "patch" && currentVersion != "" {
		for _, p := range predlCfg.PatchConfig {
			if p.Version == currentVersion {
				return p
			}
		}
	}
	return predlCfg
}

// fileURL constructs the download URL for an entry. Per-entry FromFolder
// (patch indexFiles) overrides parent baseUrl. Spaces in dest get %-encoded.
func fileURL(cdn string, parentBaseURL string, entry manifestFileRaw) string {
	folder := entry.FromFolder
	if folder == "" {
		folder = parentBaseURL
	}
	dest := strings.ReplaceAll(entry.Dest, " ", "%20")
	return cdn + folder + dest
}

// toChunks converts manifest chunkInfos into core.Chunks (end stays
// inclusive in both representations). Nil in → nil out, so chunk-less
// entries keep the whole-file download path.
func toChunks(infos []chunkInfo) []core.Chunk {
	if len(infos) == 0 {
		return nil
	}
	out := make([]core.Chunk, len(infos))
	for i, c := range infos {
		out[i] = core.Chunk{Start: c.Start, End: c.End, Hash: c.MD5}
	}
	return out
}

// newFileTask builds the FileTask for a manifest entry that needs
// downloading — single construction point so every branch of
// filterChangedFiles carries the same fields (incl. Chunks).
func newFileTask(cdn, parentBaseURL string, f manifestFileRaw) *core.FileTask {
	return &core.FileTask{
		Path:   f.Dest,
		Hash:   f.MD5,
		Size:   f.Size,
		URL:    fileURL(cdn, parentBaseURL, f),
		Chunks: toChunks(f.ChunkInfos),
	}
}

// --- File filter + MD5 helper ---

// verifyWorkers controls parallelism in filterChangedFiles. SSD random
// reads top out around 4-8 in-flight; CPU MD5 is single-thread per file at
// ~600 MiB/s so going wider than ~8 doesn't help on commodity NVMe.
const verifyWorkers = 4

// filterChangedFiles drops manifest entries whose MD5 matches the
// already-installed file. URL for each surviving entry is constructed
// via fileURL(cdn, parentBaseURL, entry).
//
// MD5 hashing runs in a worker pool (size verifyWorkers) so 195 GB-class
// .pak files don't take 5+ minutes single-threaded. Output order is
// preserved by indexing the input array.
//
// onProgress is called after each file finishes hashing with (done, total).
// May be nil. Callbacks should be cheap and non-blocking; the App layer's
// throttled emitter handles UI rate-limiting.
//
// ctx is checked at the top of each worker iteration so cancel during
// verify takes effect at the next file boundary (worst case ~30s for the
// largest .pak). Returns whatever has been computed so far when ctx done.
func filterChangedFiles(ctx context.Context, installDir, cdn, parentBaseURL string, files []manifestFileRaw, logger *slog.Logger, onProgress func(done, total int)) []core.FileTask {
	total := len(files)
	if total == 0 {
		return nil
	}

	// results[i] is non-nil iff files[i] needs to be downloaded.
	results := make([]*core.FileTask, total)

	// Job dispatch
	jobs := make(chan int, total)
	for i := range files {
		jobs <- i
	}
	close(jobs)

	// Progress is reported under a mutex so done counter stays monotonic
	// even as workers complete out of order.
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

	var wg sync.WaitGroup
	wg.Add(verifyWorkers)
	for w := 0; w < verifyWorkers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				f := files[i]
				full := filepath.Join(installDir, f.Dest)
				fi, err := os.Stat(full)
				if err != nil || fi.IsDir() {
					results[i] = newFileTask(cdn, parentBaseURL, f)
					emit()
					continue
				}
				if fi.Size() != f.Size {
					results[i] = newFileTask(cdn, parentBaseURL, f)
					emit()
					continue
				}
				h, err := md5File(full)
				if err != nil {
					if logger != nil {
						logger.Debug("md5 check failed; will re-download", "path", f.Dest, "err", err)
					}
					results[i] = newFileTask(cdn, parentBaseURL, f)
					emit()
					continue
				}
				if h != f.MD5 {
					results[i] = newFileTask(cdn, parentBaseURL, f)
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
