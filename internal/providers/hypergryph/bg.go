package hypergryph

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Background source: A-hybrid (hard-coded URL + GRYPHLINK launcher cache rescan + size filter).
//
// Per spec §2.8 / Task 9.11 research (2026-05-04). The official GRYPHLINK
// launcher fetches its banner via an authenticated batch RPC:
//
//   POST https://launcher.gryphline.com/api/proxy/web/batch_proxy
//   request:  {proxy_reqs:[{kind:"get_main_bg_image",get_main_bg_image_req:<...>},...]}
//   response: {proxy_rsps:[{kind:"get_main_bg_image",
//                           get_main_bg_image_rsp:{main_bg_image:{url:"...",video_url:"..."}}}]}
//
// During research the request body shape that yields a non-empty response
// could not be reverse-engineered from offline sources (the endpoint
// recognized the kind but always returned an empty body without the right
// app/session context). Reproducing the launcher's bootstrap is M3 work.
//
// The launcher's Chromium cache compresses response bodies with gzip, so we
// CANNOT see the JSON labels (`main_bg_image` vs `banners[]`) that would tell
// us which URL is the main bg vs a carousel banner. URLs themselves appear
// as plain-text cache index keys, but they all share the same hashed path
// pattern with no type discriminator.
//
// As an M2 trade-off this file ships:
//
//   1. A hard-coded `defaultBgURL` (current Endfield main bg, verified
//      ~3.9 MiB on the public CDN host gl-utils-public.hg-cdn.com).
//   2. `pickCurrentBgURL` — best-effort scan of the launcher's
//      Chromium simple_cache disk files at
//      `%LOCALAPPDATA%\Games\<hash>\cache\Cache\data_{0..3}` for `.webp`
//      URLs in Endfield's per-game CDN folder (`YDUTE5gscDZ229CW`),
//      followed by HTTP HEAD on each candidate to filter by
//      `Content-Length >= minBgBytes` and pick the one with the most
//      recent `Last-Modified`. This auto-tracks Hypergryph events
//      whenever the user has opened the official launcher recently. The
//      HEAD pass excludes small assets (carousel cards, UI elements)
//      that would otherwise pollute the candidate set.
//
// Failures in the cache scan / HEAD probe are non-fatal: caller silently
// falls back to (1).

const (
	// endfieldGameFolder is the per-game folder hash on Hypergryph's CDN.
	// Stable across users (server-side identifier, not per-account).
	// Discovered during Task 9.11 by visually verifying which folder's
	// banners depict Endfield vs. POPUCOM's `FtQqkyFLX4Z0bg8G`.
	endfieldGameFolder = "YDUTE5gscDZ229CW"

	defaultBgURL = "https://gl-utils-public.hg-cdn.com/hg-utils/prod/eppcsuwqpaueijqk/" +
		endfieldGameFolder + "/40/a6/40a66790b5ca8bab6d65fa0087f1df74.webp"

	// minBgBytes is the lower bound for "main bg" candidates. Verified during
	// research that Endfield main bgs are 2.7-3.9 MiB; carousel cards and UI
	// elements are <500 KiB. 1 MiB is a safe split point.
	minBgBytes = 1 << 20

	// headProbeTimeout caps each HEAD round-trip; 8 sequential probes at
	// 5s each = 40s worst case, but in practice each completes in ~50-100ms.
	headProbeTimeout = 5 * time.Second
)

// endfieldBgRe matches Endfield bg-candidate URLs cached by the GRYPHLINK
// launcher. Restricted to `.webp` because Hypergryph's main bg is always
// served as WebP — `.jpg`/`.png` URLs in the same cache are carousel cards
// or UI icons and would dilute the candidate set.
var endfieldBgRe = regexp.MustCompile(
	`https://gl-utils-public\.hg-cdn\.com/hg-utils/prod/[A-Za-z0-9]+/` +
		regexp.QuoteMeta(endfieldGameFolder) +
		`/[a-f0-9]{2}/[a-f0-9]{2}/[a-f0-9]{32}\.webp`,
)

// CurrentBgURL returns the best-known Endfield banner URL — the most recent
// large-enough entry in the launcher's Chromium cache if available, else the
// hard-coded `defaultBgURL`. logger may be nil.
func CurrentBgURL(ctx context.Context, logger *slog.Logger) string {
	urls := scanCachedWebpURLs()
	if len(urls) == 0 {
		return defaultBgURL
	}
	if u := pickLargestRecent(ctx, urls, logger); u != "" {
		return u
	}
	return defaultBgURL
}

// scanCachedWebpURLs walks GRYPHLINK's Chromium simple_cache and returns
// all unique `.webp` URLs found in Endfield's per-game CDN folder. Returns
// an empty slice on any failure — best-effort.
func scanCachedWebpURLs() []string {
	local, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	gamesRoot := filepath.Join(local, "Games")
	entries, err := os.ReadDir(gamesRoot)
	if err != nil {
		return nil
	}

	// Cap each file read at 64 MiB to bound memory.
	const maxRead = 64 * 1024 * 1024

	seen := map[string]struct{}{}
	var out []string

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cacheDir := filepath.Join(gamesRoot, e.Name(), "cache", "Cache")
		for i := 0; i < 4; i++ {
			path := filepath.Join(cacheDir, "data_"+string(rune('0'+i)))
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, maxRead))
			_ = f.Close()
			if err != nil {
				continue
			}
			for _, m := range endfieldBgRe.FindAll(b, -1) {
				u := string(m)
				if _, ok := seen[u]; ok {
					continue
				}
				seen[u] = struct{}{}
				out = append(out, u)
			}
		}
	}

	return out
}

// pickLargestRecent does an HTTP HEAD on each URL, filters by
// Content-Length >= minBgBytes, then picks the candidate with the most
// recent Last-Modified. Returns "" if none pass.
func pickLargestRecent(ctx context.Context, urls []string, logger *slog.Logger) string {
	type candidate struct {
		url     string
		lastMod time.Time
	}

	client := &http.Client{Timeout: headProbeTimeout}
	var winners []candidate

	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 || resp.ContentLength < minBgBytes {
			continue
		}
		// Last-Modified is informational; zero value sorts last and that's fine.
		lm, _ := time.Parse(http.TimeFormat, resp.Header.Get("Last-Modified"))
		winners = append(winners, candidate{url: u, lastMod: lm})
	}

	if len(winners) == 0 {
		return ""
	}
	sort.SliceStable(winners, func(i, j int) bool {
		return winners[i].lastMod.After(winners[j].lastMod)
	})

	if logger != nil {
		logger.Debug("hypergryph bg URL resolved from launcher cache",
			"url", winners[0].url,
			"candidates", len(winners),
			"last_modified", winners[0].lastMod.Format(time.RFC3339),
		)
	}
	return winners[0].url
}
