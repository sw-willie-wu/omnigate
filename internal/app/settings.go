package app

import (
	"errors"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	App      AppSettings     `toml:"app"`
	Backends BackendSettings `toml:"backends"`
}

type AppSettings struct {
	Language            string `toml:"language"`
	BannerAnimationPref string `toml:"banner_animation_pref"`
	ShowTechnicalInfo   bool   `toml:"show_technical_info"`
}

type BackendSettings struct {
	Hoyoverse HoyoverseSettings `toml:"hoyoverse"`
}

type HoyoverseSettings struct {
	HoYoplayPath string `toml:"hoyoplay_path"`
	Region       string `toml:"region"`
}

func defaultSettings() Settings {
	return Settings{
		App: AppSettings{
			Language:            "zh-TW",
			BannerAnimationPref: "video-when-available",
			ShowTechnicalInfo:   false,
		},
		Backends: BackendSettings{
			Hoyoverse: HoyoverseSettings{
				HoYoplayPath: `C:\Program Files\HoYoPlay`,
				Region:       "global",
			},
		},
	}
}

func LoadSettings(path string) (Settings, error) {
	s := defaultSettings()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := toml.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, nil
}

func SaveSettings(path string, s Settings) error {
	b, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
