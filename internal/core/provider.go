package core

import (
	"context"
	"fmt"
	"strings"
)

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
	// Launch starts the game by executing its main exe. Returns (0, nil) on
	// successful spawn — the spawned process is intentionally NOT tracked
	// by the launcher (anti-cheat may flag a polling parent). Implementations
	// MUST NOT call cmd.Wait() or otherwise observe the child after spawn.
	//
	// ctx may short-circuit pre-spawn work (UTF-16 conversions, cache lookup)
	// via ctx.Err() but does NOT bind to the spawned process lifetime.
	Launch(ctx context.Context, gid GameID, opts LaunchOptions) (pid int, err error)
}

// ParseGameID splits a GameID of the form "<backend>/<suffix>" into its
// components. Returns an error if the format is invalid (missing slash,
// empty backend, or empty suffix).
//
// The format is part of the contract: front-end stores, App routing, and
// asset URLs all depend on it.
func ParseGameID(s GameID) (BackendID, string, error) {
	parts := strings.SplitN(string(s), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], "/") {
		return "", "", fmt.Errorf("invalid game id %q (want <backend>/<suffix>)", s)
	}
	return BackendID(parts[0]), parts[1], nil
}
