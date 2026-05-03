package app

import (
	"context"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/hoyoverse"
)

type App struct {
	ctx       context.Context
	settings  Settings
	settingsP string
	hoyo      *hoyoverse.Provider
}

// New returns an App. settingsPath may be "" → default to alongside the binary.
func New(settingsPath string) *App {
	if settingsPath == "" {
		settingsPath = "settings.toml"
	}
	s, _ := LoadSettings(settingsPath)
	return &App{
		settings:  s,
		settingsP: settingsPath,
		hoyo:      hoyoverse.New(hoyoverse.Settings{HoYoplayPath: s.Backends.Hoyoverse.HoYoplayPath, Region: s.Backends.Hoyoverse.Region}),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// ─── Wails-bound commands (return values must be JSON-serializable) ───

type GameRow struct {
	ID            string               `json:"id"`
	Backend       string               `json:"backend"`
	DisplayName   core.LocalizedString `json:"display_name"`
	Installed     bool                 `json:"installed"`
	InstallPath   string               `json:"install_path,omitempty"`
	Current       string               `json:"current_version,omitempty"`
	Latest        string               `json:"latest_version,omitempty"`
	HasPredownload bool                `json:"has_predownload"`
	IconURL       string               `json:"icon_url,omitempty"`
}

// ListGames returns one GameRow per known game (installed or not).
func (a *App) ListGames() ([]GameRow, error) {
	installed, err := a.hoyo.DetectInstall(a.ctx)
	if err != nil {
		return nil, err
	}
	seen := map[core.GameID]core.InstalledGame{}
	for _, ig := range installed {
		seen[ig.GameID] = ig
	}
	out := []GameRow{}
	for _, g := range a.hoyo.Games() {
		row := GameRow{
			ID: string(g.ID), Backend: string(g.Backend), DisplayName: g.DisplayName,
		}
		if ig, ok := seen[g.ID]; ok {
			row.Installed = true
			row.InstallPath = ig.InstallPath
		}
		out = append(out, row)
	}
	return out, nil
}

func (a *App) RefreshVersion(gameID string) (core.VersionInfo, error) {
	return a.hoyo.CheckVersion(a.ctx, core.GameID(gameID))
}

func (a *App) GetIcon(gameID string) (string, error) {
	return a.hoyo.GetIcon(a.ctx, core.GameID(gameID))
}

func (a *App) GetBackgrounds(gameID string) ([]core.Background, error) {
	return a.hoyo.GetBackgrounds(a.ctx, core.GameID(gameID))
}

func (a *App) Launch(gameID string) (int, error) {
	return a.hoyo.Launch(a.ctx, core.GameID(gameID), core.LaunchOptions{})
}

func (a *App) GetSettings() Settings { return a.settings }

func (a *App) UpdateSettings(s Settings) error {
	if err := SaveSettings(a.settingsP, s); err != nil {
		return err
	}
	a.settings = s
	a.hoyo = hoyoverse.New(hoyoverse.Settings{HoYoplayPath: s.Backends.Hoyoverse.HoYoplayPath, Region: s.Backends.Hoyoverse.Region})
	return nil
}

// resolveSettingsPath returns ./settings.toml relative to the binary.
func resolveSettingsPath() string { return filepath.Join(".", "settings.toml") }
