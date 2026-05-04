package hoyoverse

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path   string // launcher install root, e.g. C:\Program Files\HoYoPlay
	Region string // "global" or "cn" — only "global" supported in M2
}

type Provider struct {
	api      *apiClient
	settings Settings
	logger   *slog.Logger
}

// New returns a new HoYoverse Provider. logger may be nil; falls back to
// slog.Default().
func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		api:      newAPIClient(APIBase, &http.Client{Timeout: 30 * time.Second}),
		settings: settings,
		logger:   logger,
	}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "米哈遊", "en": "HoYoverse"}
}

func (p *Provider) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, 0, len(games))
	for _, g := range games {
		out = append(out, core.GameDescriptor{
			ID:               g.ID,
			Backend:          BackendID,
			DisplayName:      g.Display,
			SupportedRegions: []string{"global"},
		})
	}
	return out
}

func (p *Provider) SettingsSchema() []core.SettingField {
	return []core.SettingField{
		{Key: "path", Kind: core.SettingPath,
			Label: core.LocalizedString{"zh-TW": "HoYoPlay 安裝資料夾", "en": "HoYoPlay install folder"}},
		{Key: "region", Kind: core.SettingSelectKind,
			Label:   core.LocalizedString{"zh-TW": "區域", "en": "Region"},
			Options: []string{"global"}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

func (p *Provider) GetIcon(ctx context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchGameIcon(ctx, g.Biz, "zh-tw")
}

func (p *Provider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchBasicInfo(ctx, g.APIGameID, "zh-tw")
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	g := findByID(gid)
	if g == nil {
		return core.VersionInfo{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	return p.api.fetchVersion(ctx, g.APIGameID, "")
}

func (p *Provider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return 0, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return Launch(ctx, ig.InstallPath, gid, opts)
		}
	}
	return 0, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

// PrimaryPath implements core.PathProvider.
func (p *Provider) PrimaryPath() string { return p.settings.Path }

// compile-time check
var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
)
