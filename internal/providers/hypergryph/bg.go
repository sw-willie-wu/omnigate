package hypergryph

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

// Background source: A-hybrid (hard-coded URL + GRYPHLINK launcher cache rescan).
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
// As an M2 trade-off this file ships:
//
//   1. A hard-coded `defaultBgURL` (current Endfield banner on the public
//      CDN host gl-utils-public.hg-cdn.com).
//   2. `findCachedBgURL` — best-effort regex scan of the launcher's
//      Chromium simple_cache disk files at
//      `%LOCALAPPDATA%\Games\<hash>\cache\Cache\data_{0..3}`. The
//      launcher is a Chromium-based webview that caches API responses
//      including banner CDN URLs; grep for URLs in Endfield's
//      per-game folder (`YDUTE5gscDZ229CW`) surfaces fresh banners
//      whenever the user has opened the official launcher recently.
//      Multiple `Games\<hash>\` subdirs (per profile) are enumerated; the
//      newest-mtime data_N file's last match wins.
//
// Failures in the cache scan are non-fatal: caller silently falls back
// to (1).

const (
	// endfieldGameFolder is the per-game folder hash on Hypergryph's CDN.
	// Stable across users (server-side identifier, not per-account).
	// Discovered during Task 9.11 by visually verifying which folder's
	// banners depict Endfield vs. POPUCOM's `FtQqkyFLX4Z0bg8G`.
	endfieldGameFolder = "YDUTE5gscDZ229CW"

	defaultBgURL = "https://gl-utils-public.hg-cdn.com/hg-utils/prod/eppcsuwqpaueijqk/" +
		endfieldGameFolder + "/40/a6/40a66790b5ca8bab6d65fa0087f1df74.webp"
)

// endfieldBgRe matches Endfield banners cached by the GRYPHLINK launcher.
// The terminal extension allows webp/png/jpg.
var endfieldBgRe = regexp.MustCompile(
	`https://gl-utils-public\.hg-cdn\.com/hg-utils/prod/[A-Za-z0-9]+/` +
		regexp.QuoteMeta(endfieldGameFolder) +
		`/[a-f0-9]{2}/[a-f0-9]{2}/[a-f0-9]{32}\.(webp|png|jpe?g)`,
)

// CurrentBgURL returns the best-known Endfield banner URL — the most recent
// entry in the launcher's Chromium cache if available, else the hard-coded
// `defaultBgURL`. logger may be nil.
func CurrentBgURL(logger *slog.Logger) string {
	if u := findCachedBgURL(logger); u != "" {
		return u
	}
	return defaultBgURL
}

// findCachedBgURL scans GRYPHLINK's Chromium simple_cache for the latest
// Endfield banner URL. Returns "" on any failure — best-effort.
func findCachedBgURL(logger *slog.Logger) string {
	local, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	gamesRoot := filepath.Join(local, "Games")
	entries, err := os.ReadDir(gamesRoot)
	if err != nil {
		return ""
	}

	// Cap each file read at 64 MiB to bound memory.
	const maxRead = 64 * 1024 * 1024

	var (
		bestURL   string
		bestMtime int64
	)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cacheDir := filepath.Join(gamesRoot, e.Name(), "cache", "Cache")
		for i := 0; i < 4; i++ {
			path := filepath.Join(cacheDir, "data_"+string(rune('0'+i)))
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, maxRead))
			_ = f.Close()
			if err != nil {
				continue
			}
			matches := endfieldBgRe.FindAll(b, -1)
			if len(matches) == 0 {
				continue
			}
			last := matches[len(matches)-1]
			mtime := info.ModTime().Unix()
			if mtime > bestMtime {
				bestURL = string(last)
				bestMtime = mtime
			}
		}
	}

	if bestURL != "" && logger != nil {
		logger.Debug("hypergryph bg URL resolved from launcher cache", "url", bestURL)
	}
	return bestURL
}
