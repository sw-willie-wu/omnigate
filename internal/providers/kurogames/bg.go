package kurogames

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

// Background source: A-hybrid (hard-coded URLs + WebView2 cache rescan).
//
// Per spec §2.8 / Task 8.11 research (2026-05-04). The official KRLauncher
// fetches its banner via:
//
//   GET https://prod-alicdn-gamestarter.kurogame.com/launcher/{accountID}/{gameID}/background/{configHash}/{lang}.json
//   → JSON.backgroundFile     = https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/<hash>.mp4    (looping video)
//   → JSON.firstFrameImage    = https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/<hash>.webp   (still preview)
//
// `accountID` and `configHash` are per-machine + rotated by Kuro, so we
// cannot construct the URL without their values. As an M2 trade-off this
// file ships:
//
//   1. Hard-coded `defaultBgURL` + `defaultBgVideoURL` (current as of
//      research date; verified to be a matched pair).
//   2. `findCachedBgPair` — best-effort regex scan of the launcher's
//      WebView2 disk cache (data_0..data_3). When the user has opened the
//      official launcher recently, it surfaces the most-recently-cached
//      pair so the banner auto-tracks Kuro's events. Field order in the
//      cached JSON response is stable: `backgroundFile`, then
//      `backgroundFileType`, then `firstFrameImage`. A single regex
//      captures both URLs in one shot.
//
// Failures in the cache scan are non-fatal: the caller silently falls back
// to (1). M3 will likely replace this with a fully runtime-derived URL
// once the launcher's bootstrap protocol is reverse-engineered.

const (
	defaultBgURL      = "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/8P8Q67P6OPHZHJFK.webp"
	defaultBgVideoURL = "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/LA6F54614JP6ELEF.mp4"
)

// pairRe matches the JSON literal pair inside the cached bg-config response.
// Captures: [1] = video URL (.mp4/.webm), [2] = still image URL.
var pairRe = regexp.MustCompile(
	`"backgroundFile"\s*:\s*"(https://hw-pcdownload-[a-z]+\.aki-game\.net/launcher/clientUpload/[A-Za-z0-9_./-]+\.(?:mp4|webm))"\s*,\s*"backgroundFileType":\d+\s*,\s*"firstFrameImage"\s*:\s*"(https://hw-pcdownload-[a-z]+\.aki-game\.net/launcher/clientUpload/[A-Za-z0-9_./-]+\.(?:webp|png|jpe?g))"`,
)

// CurrentBg returns the best-known WuWa banner pair: still image + looping
// video. Both are non-empty when the cache scan succeeds; falls back to
// hard-coded defaults otherwise. logger may be nil.
func CurrentBg(logger *slog.Logger) (imageURL, videoURL string) {
	if img, vid := findCachedBgPair(logger); img != "" {
		return img, vid
	}
	return defaultBgURL, defaultBgVideoURL
}

// findCachedBgPair scans the launcher's WebView2 disk cache for the latest
// (firstFrameImage, backgroundFile) pair. Returns ("", "") on any failure —
// best-effort, never an error.
func findCachedBgPair(logger *slog.Logger) (imageURL, videoURL string) {
	roaming, err := os.UserConfigDir()
	if err != nil {
		return "", ""
	}
	base := filepath.Join(roaming,
		"KRLauncher", "G153", "C50004",
		"KRWebViewUserData", "EBWebView", "Default", "Cache", "Cache_Data",
	)

	// Cap each file read at 64 MiB to bound memory.
	const maxRead = 64 * 1024 * 1024

	var (
		bestImg   string
		bestVid   string
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
		matches := pairRe.FindAllSubmatch(b, -1)
		if len(matches) == 0 {
			continue
		}
		// Last match in the file is heuristically the most recently written
		// entry; combined with file mtime we pick across the four data files.
		last := matches[len(matches)-1]
		mtime := info.ModTime().Unix()
		if mtime > bestMtime {
			bestVid = string(last[1])
			bestImg = string(last[2])
			bestMtime = mtime
		}
	}

	if bestImg != "" && logger != nil {
		logger.Debug("kurogames bg pair resolved from WebView2 cache", "image", bestImg, "video", bestVid)
	}
	return bestImg, bestVid
}
