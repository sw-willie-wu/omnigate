package hoyoverse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"omnigate/internal/core"
)

type lastApplyTarget struct {
	TargetVersion        string    `json:"target_version"`
	AudioLanguages       []string  `json:"audio_languages"`
	CompletionTS         time.Time `json:"completion_ts"`
	ConfigWritebackOK    bool      `json:"config_writeback_ok"`
	ManifestETag         string    `json:"manifest_etag"`
	LastWritebackRetryTS time.Time `json:"last_writeback_retry_ts,omitempty"`
}

func writeLastApplyTarget(tempRoot string, gid core.GameID, lat *lastApplyTarget) error {
	dir := gameSidecarDir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "last_apply_target.json")
	data, err := json.MarshalIndent(lat, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

var audioLangToFolder = map[string]string{
	"zh-cn": "Chinese",
	"en-us": "English(US)",
	"ja-jp": "Japanese",
	"ko-kr": "Korean",
}

var folderToAudioLang = func() map[string]string {
	m := make(map[string]string, len(audioLangToFolder))
	for k, v := range audioLangToFolder {
		m[v] = k
	}
	return m
}()

func audioLanguageIntersect(info *HypPackageInfo, installedFolders []string) []string {
	installedAPI := make(map[string]struct{}, len(installedFolders))
	for _, f := range installedFolders {
		if api, ok := folderToAudioLang[f]; ok {
			installedAPI[api] = struct{}{}
		}
	}
	out := make([]string, 0)
	for _, p := range info.AudioPkgs {
		if _, want := installedAPI[p.Language]; want {
			out = append(out, p.Language)
		}
	}
	sort.Strings(out)
	return out
}

func buildPlan(
	ctx context.Context,
	resp *HypGetGamePackagesResponse,
	gid core.GameID,
	currentVer string,
	tempRoot string,
	gameDir string,
	probe freeSpaceProbe,
) (*genshinPlan, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(resp.Data.GamePackages) == 0 {
		return nil, false, fmt.Errorf("manifest has no game_packages")
	}
	entry := resp.Data.GamePackages[0]
	mainMajor := entry.Main.Major

	gp := &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:       gid,
			Kind:         core.PlanUpdate,
			ManifestETag: resp.ManifestETag,
			Version:      mainMajor.Version,
		},
		manifestETag:   resp.ManifestETag,
		audioLanguages: nil,
	}

	installedFolders, err := DetectInstalledLanguages(gameDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, fmt.Errorf("detect audio langs: %w", err)
	}
	installedAPI := audioLanguageIntersect(&mainMajor, installedFolders)
	gp.audioLanguages = append([]string{}, installedAPI...)

	predlAvailable := entry.PreDownload != nil

	if currentVer == mainMajor.Version {
		latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
		lat, _ := loadJSONSidecar[lastApplyTarget](latPath)
		if lat == nil {
			gp.flavor = flavorNone
			return gp, predlAvailable, nil
		}
		baseline := append([]string{}, lat.AudioLanguages...)
		sort.Strings(baseline)
		current := append([]string{}, installedFolders...)
		sort.Strings(current)
		if slicesEqual(baseline, current) {
			gp.flavor = flavorNone
			return gp, predlAvailable, nil
		}
		gp.flavor = flavorAudioOnly
		gp.UpdatePlan.Reason = core.ReasonAudioPackAdded
		gp.sourceVersion = currentVer
		baselineSet := make(map[string]struct{}, len(lat.AudioLanguages))
		for _, lang := range lat.AudioLanguages {
			if api, ok := folderToAudioLang[lang]; ok {
				baselineSet[api] = struct{}{}
			}
		}
		for _, p := range mainMajor.AudioPkgs {
			if _, want := baselineSet[p.Language]; want {
				continue
			}
			if !contains(installedAPI, p.Language) {
				continue
			}
			gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{
				URL:  p.URL,
				Hash: p.MD5,
				Size: p.Size,
				Path: filepath.Base(p.URL),
			})
		}
		return runPreflight(gp, tempRoot, gameDir, probe, predlAvailable)
	}

	for _, patch := range entry.Main.Patches {
		if patch.Version == currentVer {
			gp.flavor = flavorPatch
			gp.sourceVersion = currentVer
			gp.UpdatePlan.Reason = core.ReasonVersionChanged
			patchAudio := audioLanguageIntersect(&patch, installedFolders)
			if !slicesEqual(installedAPI, patchAudio) {
				gp.UpdatePlan.Reason = core.ReasonVersionAndAudio
			}
			gp.UpdatePlan.Files = make([]core.FileTask, 0, len(patch.GamePkgs)+len(patch.AudioPkgs))
			for _, p := range patch.GamePkgs {
				gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{
					URL: p.URL, Hash: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
				})
			}
			for _, p := range patch.AudioPkgs {
				if !contains(installedAPI, p.Language) {
					continue
				}
				gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{
					URL: p.URL, Hash: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
				})
			}
			return runPreflight(gp, tempRoot, gameDir, probe, predlAvailable)
		}
	}

	gp.flavor = flavorFull
	gp.UpdatePlan.Reason = core.ReasonVersionChanged
	gp.UpdatePlan.Files = make([]core.FileTask, 0, len(mainMajor.GamePkgs)+len(mainMajor.AudioPkgs))
	for _, p := range mainMajor.GamePkgs {
		gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{
			URL: p.URL, Hash: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
		})
	}
	for _, p := range mainMajor.AudioPkgs {
		if !contains(installedAPI, p.Language) {
			continue
		}
		gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{
			URL: p.URL, Hash: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
		})
	}
	return runPreflight(gp, tempRoot, gameDir, probe, predlAvailable)
}

func runPreflight(gp *genshinPlan, tempRoot, gameDir string, probe freeSpaceProbe, predlAvailable bool) (*genshinPlan, bool, error) {
	var total int64
	for _, f := range gp.UpdatePlan.Files {
		total += f.Size
	}
	gp.UpdatePlan.TotalBytes = total

	if err := CheckDiskSpace(gp.UpdatePlan, tempRoot, gameDir, probe); err != nil {
		return nil, false, err
	}
	return gp, predlAvailable, nil
}

// buildPredlPlan builds a predownload plan from entry.PreDownload (the next
// version), mirroring buildPlan's patch-vs-full decision sourced from PreDownload
// instead of Main. Returns (nil, nil) when no actionable predownload is published
// (no PreDownload, or its version equals the installed version). Flavors are
// flavorPredlPatch / flavorPredlFull; Kind is PlanPredownload.
func buildPredlPlan(
	ctx context.Context,
	resp *HypGetGamePackagesResponse,
	gid core.GameID,
	currentVer string,
	tempRoot string,
	gameDir string,
	probe freeSpaceProbe,
) (*genshinPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(resp.Data.GamePackages) == 0 {
		return nil, fmt.Errorf("manifest has no game_packages")
	}
	entry := resp.Data.GamePackages[0]
	if entry.PreDownload == nil {
		return nil, nil
	}
	predlMajor := entry.PreDownload.Major
	if predlMajor.Version == "" || predlMajor.Version == currentVer {
		return nil, nil
	}

	gp := &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:       gid,
			Kind:         core.PlanPredownload,
			ManifestETag: resp.ManifestETag,
			Version:      predlMajor.Version,
			Reason:       core.ReasonPredownload,
		},
		manifestETag:  resp.ManifestETag,
		sourceVersion: currentVer,
	}

	installedFolders, err := DetectInstalledLanguages(gameDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("detect audio langs: %w", err)
	}
	installedAPI := audioLanguageIntersect(&predlMajor, installedFolders)
	gp.audioLanguages = append([]string{}, installedAPI...)

	appendPkgs := func(info *HypPackageInfo) {
		gp.UpdatePlan.Files = make([]core.FileTask, 0, len(info.GamePkgs)+len(info.AudioPkgs))
		for _, pk := range info.GamePkgs {
			gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{URL: pk.URL, Hash: pk.MD5, Size: pk.Size, Path: filepath.Base(pk.URL)})
		}
		for _, pk := range info.AudioPkgs {
			if !contains(installedAPI, pk.Language) {
				continue
			}
			gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{URL: pk.URL, Hash: pk.MD5, Size: pk.Size, Path: filepath.Base(pk.URL)})
		}
	}

	for i := range entry.PreDownload.Patches {
		patch := entry.PreDownload.Patches[i]
		if patch.Version == currentVer {
			gp.flavor = flavorPredlPatch
			appendPkgs(&patch)
			out, _, perr := runPreflight(gp, tempRoot, gameDir, probe, false)
			return out, perr
		}
	}

	gp.flavor = flavorPredlFull
	appendPkgs(&predlMajor)
	out, _, perr := runPreflight(gp, tempRoot, gameDir, probe, false)
	return out, perr
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
