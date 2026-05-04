package hypergryph

import (
	"context"
	"fmt"
	"log/slog"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path string
}

type Provider struct {
	settings Settings
	logger   *slog.Logger
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{settings: settings, logger: logger}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "鷹角", "zh-CN": "鹰角", "en": "Hypergryph"}
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
			Label: core.LocalizedString{
				"zh-TW": "GRYPHLINK 安裝資料夾",
				"zh-CN": "GRYPHLINK 安装文件夹",
				"en":    "GRYPHLINK launcher folder",
			}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

func (p *Provider) GetIcon(_ context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return fmt.Sprintf("/_asset/%s/icon/%s", p.ID(), suffix), nil
}

func (p *Provider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_ = g // future: per-game URL routing
	return []core.Background{
		{
			ImageURL: CurrentBgURL(ctx, p.logger),
			VideoURL: "",
			Type:     core.BackgroundImage,
		},
	}, nil
}

func (p *Provider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	installs, err := p.DetectInstall(ctx)
	if err != nil {
		return core.VersionInfo{}, err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return fetchVersion(ctx, ig.InstallPath, gid)
		}
	}
	return core.VersionInfo{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
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

func (p *Provider) PrimaryPath() string { return p.settings.Path }

func (p *Provider) ExeName(gid core.GameID) (string, bool) {
	g := findByID(gid)
	if g == nil {
		return "", false
	}
	return g.ExeName, true
}

var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
	_ core.ExeNamer     = (*Provider)(nil)
)
