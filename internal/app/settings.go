package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse"
	"omnigate/internal/providers/hypergryph"
	"omnigate/internal/providers/kurogames"
)

type Settings struct {
	Version  int                     `toml:"version"`
	App      AppSettings             `toml:"app"`
	Backends BackendSettings         `toml:"backends"`
	Games    map[string]GameSettings `toml:"games"`
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
type HypergryphSettings struct {
	Path    string `toml:"path"`
	TempDir string `toml:"temp_dir,omitempty"` // empty → runtime default os.TempDir()/omnigate/hypergryph
}

type GameSettings struct {
	Path string `toml:"path,omitempty"`
}

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
	Games map[string]GameSettings `toml:"games"`
}

func defaultSettings() Settings {
	return Settings{
		Version: 2,
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
		Games: map[string]GameSettings{},
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
	if raw.Backends.Hypergryph.TempDir != "" {
		out.Backends.Hypergryph.TempDir = raw.Backends.Hypergryph.TempDir
	}

	// games (per-game overrides)
	out.Games = raw.Games
	if out.Games == nil {
		out.Games = map[string]GameSettings{}
	}

	// v1→v2 migration: derive per-game overrides from old per-backend roots so
	// no currently-installed game is lost when install locations move from
	// per-backend roots to per-game override folders. Runs for any old file
	// (raw.Version < 2). A default-root user keeps NO override (game stays
	// auto-detected and re-detectable after a move); a custom-root user gets a
	// seeded override. Never stats — preserves a custom root on an offline drive.
	if raw.Version < 2 {
		migrateV1ToV2(&out)
	}

	// On any successful load (including post-migration), bump version to 2.
	out.Version = 2

	return out, nil
}

// migrateBackend describes one backend's inputs to the v1→v2 migration.
type migrateBackend struct {
	root        string                 // already-v0-projected install root
	defaultRoot string                 // provider DefaultRoot
	hasSeg      bool                   // provider HasGamesSegment
	folders     map[core.GameID]string // provider FolderNames()
}

// migrateV1ToV2 seeds out.Games with per-game overrides derived from each
// backend's old root. See LoadSettings for the migration policy.
func migrateV1ToV2(out *Settings) {
	backends := []migrateBackend{
		{out.Backends.Hoyoverse.Path, hoyoverse.DefaultRoot, hoyoverse.HasGamesSegment, hoyoverse.FolderNames()},
		{out.Backends.Kurogames.Path, kurogames.DefaultRoot, kurogames.HasGamesSegment, kurogames.FolderNames()},
		{out.Backends.Hypergryph.Path, hypergryph.DefaultRoot, hypergryph.HasGamesSegment, hypergryph.FolderNames()},
	}
	for _, b := range backends {
		if b.root == "" || b.root == b.defaultRoot {
			continue
		}
		for gid, folder := range b.folders {
			key := string(gid)
			if _, exists := out.Games[key]; exists {
				continue // don't clobber an explicit games entry
			}
			var candidate string
			if b.hasSeg {
				candidate = filepath.Join(b.root, "games", folder)
			} else {
				candidate = filepath.Join(b.root, folder)
			}
			out.Games[key] = GameSettings{Path: candidate}
		}
	}
}

// SaveSettings writes the canonical schema. Always includes version = 2; never
// emits hoyoplay_path.
func SaveSettings(path string, s Settings) error {
	s.Version = 2 // canonicalize
	b, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
