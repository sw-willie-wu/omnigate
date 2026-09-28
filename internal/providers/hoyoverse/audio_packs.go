package hoyoverse

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omnigate/internal/core"
)

// audioAssetsRel is the Genshin voice-pack root (relative to gameDir). The
// per-game value now lives in gameMeta.AudioAssetsRel; this constant is kept
// for the legacy update_manifest tests that build Genshin fixtures with it.
const audioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

// DetectInstalledLanguages returns the voice-pack folder names installed for
// gid under <gameDir>/<meta.AudioAssetsRel>/, sorted. Games without audio
// meta (ZZZ, unknown ids) get an empty, non-nil slice — NOT an error.
//
// Source of truth, in order:
//  1. meta.AudioRecordRel (Star Rail's HoYoPlay-written
//     AudioLaucherRecord.txt) when it names at least one known language.
//     Folder presence is not a reliable signal for Star Rail: the game
//     drops partial on-demand audio into the same root, and only the
//     record distinguishes launcher-installed packs from those. A recorded
//     language whose folder is missing is kept (it will be re-downloaded);
//     if the record looks stale (names a language with no folder) the
//     installed known folders are unioned in so an installed pack is never
//     left at the old version.
//  2. Otherwise the folder scan (Genshin; Star Rail without a record).
//
// Whenever the result differs from the folder scan a warning is logged so a
// surprising plan can be diagnosed from omnigate.log.
func DetectInstalledLanguages(gid core.GameID, gameDir string) ([]string, error) {
	g := findByID(gid)
	if g == nil || g.AudioAssetsRel == "" {
		return []string{}, nil
	}
	scanned, err := scanAudioFolders(filepath.Join(gameDir, g.AudioAssetsRel))
	if err != nil {
		return nil, err
	}
	if g.AudioRecordRel == "" {
		return scanned, nil
	}
	data, err := os.ReadFile(filepath.Join(gameDir, g.AudioRecordRel))
	if err != nil {
		return scanned, nil // no/unreadable record → folder scan
	}
	recorded := parseAudioRecord(string(data), g.AudioFolders)
	if len(recorded) == 0 {
		return scanned, nil
	}
	result := recorded
	if !subset(recorded, scanned) {
		known := make(map[string]struct{}, len(g.AudioFolders))
		for _, folder := range g.AudioFolders {
			known[folder] = struct{}{}
		}
		set := make(map[string]struct{}, len(recorded)+len(scanned))
		for _, r := range recorded {
			set[r] = struct{}{}
		}
		for _, s := range scanned {
			if _, ok := known[s]; ok {
				set[s] = struct{}{}
			}
		}
		result = make([]string, 0, len(set))
		for k := range set {
			result = append(result, k)
		}
		sort.Strings(result)
	}
	if !slicesEqual(result, scanned) {
		slog.Warn("hoyoverse: audio record/folder mismatch", "game", gid, "recorded", recorded, "scanned", scanned, "using", result)
	}
	return result, nil
}

// scanAudioFolders lists the subdirectories of root, sorted. A missing root
// yields an empty slice.
func scanAudioFolders(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// parseAudioRecord tokenises a launcher audio record permissively (newline,
// CR, comma or semicolon separated; the multi-language format has not been
// observed in the wild) and keeps only tokens that name a known folder —
// either the folder name itself or its matching_field code, which is mapped
// to the folder. Result is deduplicated and sorted.
func parseAudioRecord(content string, folders map[string]string) []string {
	byFolder := make(map[string]struct{}, len(folders))
	for _, f := range folders {
		byFolder[f] = struct{}{}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 2)
	tokens := strings.FieldsFunc(content, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		folder := ""
		if _, ok := byFolder[tok]; ok {
			folder = tok
		} else if f, ok := folders[tok]; ok {
			folder = f
		}
		if folder == "" {
			continue
		}
		if _, dup := seen[folder]; dup {
			continue
		}
		seen[folder] = struct{}{}
		out = append(out, folder)
	}
	sort.Strings(out)
	return out
}

// subset reports whether every element of a is present in b.
func subset(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	for _, s := range a {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}
