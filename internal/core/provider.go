package core

import "context"

type BackendID string
type GameID string
type PlanKind int

const (
	PlanUpdate PlanKind = iota
	PlanPredownload
)

type GameDescriptor struct {
	ID               GameID
	Backend          BackendID
	DisplayName      LocalizedString
	SupportedRegions []string
}

type InstalledGame struct {
	GameID         GameID
	InstallPath    string
	CurrentVersion string
}

type VersionInfo struct {
	Current     string
	Latest      string
	Predownload *PredownloadInfo
}

type PredownloadInfo struct {
	TargetVersion string
	TotalBytes    uint64
}

type BackgroundType int

const (
	BackgroundImage BackgroundType = iota
	BackgroundVideo
)

type Background struct {
	ImageURL string
	VideoURL string
	Type     BackgroundType
}

type SettingFieldKind int

const (
	SettingPath SettingFieldKind = iota
	SettingSelectKind
	SettingBool
)

type SettingField struct {
	Key     string
	Kind    SettingFieldKind
	Label   LocalizedString
	Options []string
}

type LaunchOptions struct {
	ExtraArgs []string
}

// Provider is the integration point for one launcher backend (one publisher).
// Phase 1 = hoyoverse only.
type Provider interface {
	ID() BackendID
	DisplayName() LocalizedString
	Games() []GameDescriptor
	SettingsSchema() []SettingField

	DetectInstall(ctx context.Context) ([]InstalledGame, error)
	GetIcon(ctx context.Context, gid GameID) (string, error)
	GetBackgrounds(ctx context.Context, gid GameID) ([]Background, error)
	CheckVersion(ctx context.Context, gid GameID) (VersionInfo, error)
	Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
}
