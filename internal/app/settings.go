package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	Version  int             `toml:"version"`
	App      AppSettings     `toml:"app"`
	Backends BackendSettings `toml:"backends"`
}

type AppSettings struct {
	Language            string `toml:"language"`
	BannerAnimationPref string `toml:"banner_animation_pref"`
	ShowTechnicalInfo   bool   `toml:"show_technical_info"`
}

type BackendSettings struct {
	Hoyoverse  HoyoverseSettings  `toml:"hoyoverse"`
	Kurogames  KurogamesSettings  `toml:"kurogames"`
	Hypergryph HypergryphSettings `toml:"hypergryph"`
}

type HoyoverseSettings struct {
	Path    string `toml:"path"`
	Region  string `toml:"region"`
	TempDir string `toml:"temp_dir,omitempty"` // M3.B: empty → runtime default <TEMP>/omnigate/hoyoverse/
}

type KurogamesSettings struct {
	Path    string `toml:"path"`
	TempDir string `toml:"temp_dir,omitempty"` // empty → runtime default os.TempDir()/omnigate/<gameID>
}
type HypergryphSettings struct{ Path string `toml:"path"` }

// hoyoverseRawTOML is used for the M1 → M2 migration: M1 wrote
// `hoyoplay_path` under [backends.hoyoverse]. On Load, if Path is empty and
// HoYoplayPath is non-empty, project HoYoplayPath into Path and warn.
type hoyoverseRawTOML struct {
	Path         string `toml:"path"`
	HoYoplayPath string `toml:"hoyoplay_path"`
	Region       string `toml:"region"`
}

type rawTOML struct {
	Version  int         `toml:"version"`
	App      AppSettings `toml:"app"`
	Backends struct {
		Hoyoverse  hoyoverseRawTOML   `toml:"hoyoverse"`
		Kurogames  KurogamesSettings  `toml:"kurogames"`
		Hypergryph HypergryphSettings `toml:"hypergryph"`
	} `toml:"backends"`
}

func defaultSettings() Settings {
	return Settings{
		Version: 1,
		App: AppSettings{
			Language:            "zh-TW",
			BannerAnimationPref: "video-when-available",
			ShowTechnicalInfo:   false,
		},
		Backends: BackendSettings{
			Hoyoverse:  HoyoverseSettings{Path: `C:\Program Files\HoYoPlay`, Region: "global"},
			Kurogames:  KurogamesSettings{Path: `C:\Program Files\Wuthering Waves`},
			Hypergryph: HypergryphSettings{Path: `C:\Program Files\GRYPHLINK`},
		},
	}
}

// LoadSettings reads path. Returns defaultSettings() on missing file with
// nil error. On parse failure, returns defaultSettings() with the parse
// error so callers can log and continue (M1 ate this silently).
func LoadSettings(path string) (Settings, error) {
	defaults := defaultSettings()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var raw rawTOML
	if err := toml.Unmarshal(b, &raw); err != nil {
		return defaults, fmt.Errorf("settings TOML parse: %w", err)
	}

	out := defaults
	// version
	if raw.Version != 0 {
		out.Version = raw.Version
	} // else stays 1 from defaults; we treat absent as v0=M1
	// app
	if raw.App.Language != "" {
		out.App.Language = raw.App.Language
	}
	if raw.App.BannerAnimationPref != "" {
		out.App.BannerAnimationPref = raw.App.BannerAnimationPref
	}
	out.App.ShowTechnicalInfo = raw.App.ShowTechnicalInfo

	// hoyoverse — migrate hoyoplay_path → path
	hov := raw.Backends.Hoyoverse
	if hov.Path != "" {
		out.Backends.Hoyoverse.Path = hov.Path
	} else if hov.HoYoplayPath != "" && raw.Version == 0 {
		// M1-format file — migrate
		out.Backends.Hoyoverse.Path = hov.HoYoplayPath
		slog.Default().Warn("settings: migrated legacy [backends.hoyoverse].hoyoplay_path → path",
			"old_value", hov.HoYoplayPath)
	}
	if hov.Region != "" {
		out.Backends.Hoyoverse.Region = hov.Region
	}

	// kurogames / hypergryph (no migration; M2 introduces them)
	if raw.Backends.Kurogames.Path != "" {
		out.Backends.Kurogames.Path = raw.Backends.Kurogames.Path
	}
	if raw.Backends.Kurogames.TempDir != "" {
		out.Backends.Kurogames.TempDir = raw.Backends.Kurogames.TempDir
	}
	if raw.Backends.Hypergryph.Path != "" {
		out.Backends.Hypergryph.Path = raw.Backends.Hypergryph.Path
	}

	// On any successful load (including post-migration), bump version to 1.
	out.Version = 1

	return out, nil
}

// SaveSettings writes the canonical schema. Always includes version = 1; never
// emits hoyoplay_path.
func SaveSettings(path string, s Settings) error {
	s.Version = 1 // canonicalize
	b, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
