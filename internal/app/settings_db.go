package app

import (
	"strconv"

	"omnigate/internal/store"
)

// config-table keys (dotted, mirroring the TOML structure in settings.go).
const (
	ckSettingsVersion = "settings_version"
	ckLanguage        = "app.language"
	ckTempDir         = "app.temp_dir"
	ckBannerPref      = "app.banner_animation_pref"
	ckShowTech        = "app.show_technical_info"
	ckHoyoPath        = "backends.hoyoverse.path"
	ckHoyoRegion      = "backends.hoyoverse.region"
	ckKuroPath        = "backends.kurogames.path"
	ckGryphPath       = "backends.hypergryph.path"
)

// saveSettingsToDB writes the canonical settings into config + game_settings.
// ShowTechnicalInfo is stored as an explicit "true"/"false"; game_settings is
// replaced wholesale so removed overrides are deleted.
//
// Not wrapped in a single transaction (spec §6 MINOR-2, accepted deviation): a
// mid-write failure leaves partial rows, but every write is an idempotent
// INSERT OR REPLACE / ReplaceGameSettings, so a retry self-heals.
func saveSettingsToDB(st store.StateStore, s Settings) error {
	kv := map[string]string{
		ckSettingsVersion: "3",
		ckLanguage:        s.App.Language,
		ckTempDir:         s.App.TempDir,
		ckBannerPref:      s.App.BannerAnimationPref,
		ckShowTech:        strconv.FormatBool(s.App.ShowTechnicalInfo),
		ckHoyoPath:        s.Backends.Hoyoverse.Path,
		ckHoyoRegion:      s.Backends.Hoyoverse.Region,
		ckKuroPath:        s.Backends.Kurogames.Path,
		ckGryphPath:       s.Backends.Hypergryph.Path,
	}
	for k, v := range kv {
		if err := st.SetConfig(k, v); err != nil {
			return err
		}
	}
	games := map[string]store.GameOverride{}
	for id, g := range s.Games {
		games[id] = store.GameOverride{Path: g.Path, BackgroundPath: g.BackgroundPath}
	}
	return st.ReplaceGameSettings(games)
}

// loadSettingsFromDB reconstructs Settings, starting from defaultSettings() and
// overlaying DB rows. Empty path/region rows fall back to the non-empty default
// (parity with LoadSettings' TOML default-merge); ShowTechnicalInfo is applied
// unconditionally from its explicit bool row.
func loadSettingsFromDB(st store.StateStore) (Settings, error) {
	out := defaultSettings()
	cfg, err := st.AllConfig()
	if err != nil {
		return out, err
	}

	overlayNonEmpty := func(dst *string, key string) {
		if v, ok := cfg[key]; ok && v != "" {
			*dst = v
		}
	}
	overlayNonEmpty(&out.App.Language, ckLanguage)
	overlayNonEmpty(&out.App.BannerAnimationPref, ckBannerPref)
	overlayNonEmpty(&out.Backends.Hoyoverse.Path, ckHoyoPath)
	overlayNonEmpty(&out.Backends.Hoyoverse.Region, ckHoyoRegion)
	overlayNonEmpty(&out.Backends.Kurogames.Path, ckKuroPath)
	overlayNonEmpty(&out.Backends.Hypergryph.Path, ckGryphPath)
	// TempDir: empty is a valid value (→ default temp root); take the row as-is.
	if v, ok := cfg[ckTempDir]; ok {
		out.App.TempDir = v
	}
	// ShowTechnicalInfo: explicit bool, applied unconditionally when present.
	if v, ok := cfg[ckShowTech]; ok {
		out.App.ShowTechnicalInfo, _ = strconv.ParseBool(v)
	}

	gs, err := st.AllGameSettings()
	if err != nil {
		return out, err
	}
	out.Games = map[string]GameSettings{} // always non-nil
	for id, g := range gs {
		out.Games[id] = GameSettings{Path: g.Path, BackgroundPath: g.BackgroundPath}
	}
	out.Version = 3
	return out, nil
}
