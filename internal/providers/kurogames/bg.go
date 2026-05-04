package kurogames

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

// Background source: A-hybrid (hard-coded URL + WebView2 cache rescan).
//
// Per spec §2.8 / Task 8.11 research (2026-05-04). The official KRLauncher
// fetches its banner via:
//
//   GET https://prod-alicdn-gamestarter.kurogame.com/launcher/{accountID}/{gameID}/background/{configHash}/{lang}.json
//   → JSON.firstFrameImage = https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/<hash>.webp
//
// `accountID` and `configHash` are per-machine + rotated by Kuro, so we
// cannot construct the URL without their values. As an M2 trade-off this
// file ships:
//
//   1. A hard-coded `defaultBgURL` (current as of research date).
//   2. `findCachedBgURL` — best-effort regex scan of the launcher's WebView2
//      disk cache (data_0..data_3). When the user has opened the official
//      launcher recently, it surfaces the most-recently-cached firstFrameImage
//      so the banner auto-tracks Kuro's events.
//
// Failures in the cache scan are non-fatal: the caller silently falls back
// to (1). M3 will likely replace this with a fully runtime-derived URL once
// the launcher's bootstrap protocol is reverse-engineered.

const defaultBgURL = "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/8P8Q67P6OPHZHJFK.webp"

// firstFrameRe matches the JSON literal inside the cached bg-config response
// body. Verified during research that data_1 of the cache contains JSON
// fragments with this exact key.
var firstFrameRe = regexp.MustCompile(`"firstFrameImage"\s*:\s*"(https://hw-pcdownload-[a-z]+\.aki-game\.net/launcher/clientUpload/[A-Za-z0-9_./-]+\.webp)"`)

// CurrentBgURL returns the best-known WuWa banner URL — the most recent
// entry in the launcher's WebView2 cache if available, else the hard-coded
// `defaultBgURL`. logger may be nil.
func CurrentBgURL(logger *slog.Logger) string {
	if u := findCachedBgURL(logger); u != "" {
		return u
	}
	return defaultBgURL
}

// findCachedBgURL scans the official launcher's WebView2 disk cache for the
// latest `firstFrameImage` JSON pair. Returns "" on any failure — never an
// error to the caller; this is best-effort.
func findCachedBgURL(logger *slog.Logger) string {
	roaming, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	base := filepath.Join(roaming,
		"KRLauncher", "G153", "C50004",
		"KRWebViewUserData", "EBWebView", "Default", "Cache", "Cache_Data",
	)

	// Cap each file read at 64 MiB to bound memory.
	const maxRead = 64 * 1024 * 1024

	var (
		bestURL   string
		bestMtime int64
	)

	for i := 0; i < 4; i++ {
		path := filepath.Join(base, "data_"+string(rune('0'+i)))
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
		matches := firstFrameRe.FindAllSubmatch(b, -1)
		if len(matches) == 0 {
			continue
		}
		// Last match in the file is heuristically the most recently written
		// entry; combined with file mtime we pick across the four data files.
		last := matches[len(matches)-1][1]
		mtime := info.ModTime().Unix()
		if mtime > bestMtime {
			bestURL = string(last)
			bestMtime = mtime
		}
	}

	if bestURL != "" && logger != nil {
		logger.Debug("kurogames bg URL resolved from WebView2 cache", "url", bestURL)
	}
	return bestURL
}
