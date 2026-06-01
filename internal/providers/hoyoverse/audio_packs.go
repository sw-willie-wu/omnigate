package hoyoverse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// audioAssetsRel is the path (relative to gameDir) where Genshin keeps voice
// pack subfolders. One subfolder per installed language. The literal subfolder
// names (e.g. "Chinese", "English(US)") are observed at runtime; M3.B does
// NOT lock specific names — DetectInstalledLanguages returns whatever the
// filesystem shows. Mapping to manifest audio_pkgs[].language values lives
// in update_manifest.go::audioLanguageIntersect.
const audioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

// DetectInstalledLanguages enumerates installed voice pack folders under
// <gameDir>/<audioAssetsRel>/. Returns sorted slice of subfolder names
// (used by spec §2 last_apply_target.json drift detection + audio_pkg
// intersect). Empty slice if the parent dir doesn't exist (no voice
// packs installed) — NOT an error.
func DetectInstalledLanguages(gameDir string) ([]string, error) {
	root := filepath.Join(gameDir, audioAssetsRel)
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
