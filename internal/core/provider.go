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

// MarshalJSON emits the string form ("update"/"predownload") so the frontend
// can compare with === to literal strings. Default int marshalling broke
// BottomBar's `inFlight.kind === 'update'` check.
func (k PlanKind) MarshalJSON() ([]byte, error) {
	switch k {
	case PlanUpdate:
		return []byte(`"update"`), nil
	case PlanPredownload:
		return []byte(`"predownload"`), nil
	}
	return []byte(`""`), nil
}

// UnmarshalJSON accepts either the new string form or the legacy int form
// so existing on-disk sidecars (predl_ready.json / apply.wal) keep loading.
func (k *PlanKind) UnmarshalJSON(data []byte) error {
	s := string(data)
	switch s {
	case `"update"`, `0`:
		*k = PlanUpdate
		return nil
	case `"predownload"`, `1`:
		*k = PlanPredownload
		return nil
	}
	return fmt.Errorf("unknown PlanKind JSON: %s", s)
}

// Phase identifies which sub-phase of RunUpdate is currently active.
// PhaseDownload progress is reported in bytes; PhaseApply in file count.
type Phase int

const (
	PhaseDownload Phase = iota
	PhaseApply
)

func (p Phase) MarshalJSON() ([]byte, error) {
	switch p {
	case PhaseDownload:
		return []byte(`"download"`), nil
	case PhaseApply:
		return []byte(`"apply"`), nil
	}
	return []byte(`""`), nil
}

func (p *Phase) UnmarshalJSON(data []byte) error {
	s := string(data)
	switch s {
	case `"download"`, `0`:
		*p = PhaseDownload
		return nil
	case `"apply"`, `1`:
		*p = PhaseApply
		return nil
	}
	return fmt.Errorf("unknown Phase JSON: %s", s)
}

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

// InstallSource is how a game's resolved install folder was determined.
type InstallSource string

const (
	SourceOverride   InstallSource = "override"   // user-set Settings.Games[id].Path
	SourceLauncher   InstallSource = "launcher"   // read from the launcher's own records
	SourceDefault    InstallSource = "default"    // found under the backend DefaultRoot
	SourceUnresolved InstallSource = "unresolved" // not found anywhere
)

// InstallLocator is an optional Provider capability: read the launcher's own
// record of where each installed game lives (registry / AppData), independent
// of any configured root. Best-effort — partial/empty results and errors are
// acceptable; the App stat-validates every returned path before trusting it.
type InstallLocator interface {
	LocateInstalls(ctx context.Context) (map[GameID]string, error)
}

// ResolvedPathSetter is an optional Provider capability: accept the App-resolved
// per-game install folders so the provider's launch/version/update operations
// use them instead of re-deriving from a single root.
type ResolvedPathSetter interface {
	SetResolvedPaths(paths map[GameID]string)
}

// LastPlayedProbe is an optional Provider capability. Given a game and its
// resolved install dir, it returns filesystem paths whose mtime indicates the
// game was launched — including launches outside omnigate (the game engine's
// player log, rewritten on each launch). The App stats each path and takes the
// most recent mtime, then maxes it against the recorded playstate timestamp.
//
// Implementations MUST be pure path construction: no filesystem IO, no errors.
// Non-existent paths are filtered by the App's stat step. An empty/nil return
// means "no extra signal" (the App falls back to the playstate timestamp).
type LastPlayedProbe interface {
	LastPlayedFiles(gid GameID, installDir string) []string
}

// NewsCategory groups a news item for the NewsPanel filter (全部/公告/活動).
type NewsCategory string

const (
	NewsAnnounce NewsCategory = "announce" // 公告
	NewsActivity NewsCategory = "activity" // 活動
	NewsInfo     NewsCategory = "info"     // 資訊（前端只在「全部」顯示）
)

// NewsItem is one entry in a game's public news feed.
type NewsItem struct {
	Title     string       `json:"title"`
	Category  NewsCategory `json:"category"`
	Date      string       `json:"date"`                // display string (provider formats epoch → YYYY-MM-DD)
	URL       string       `json:"url"`                 // click-through: external browser
	Thumbnail string       `json:"thumbnail,omitempty"` // may be empty → frontend placeholder
}

// NewsProvider is an optional Provider capability: fetch a game's public news
// feed (no auth). lang is the app UI language (en/zh-TW/zh-CN); the provider
// maps it to its own source language code. A fetch/parse failure should return
// an empty slice (best-effort) or an error; the App treats both as "no news".
type NewsProvider interface {
	GetNews(ctx context.Context, gid GameID, lang string) ([]NewsItem, error)
}

// GameAccount is one launcher-remembered account for a game. The provider never
// exposes credentials; ID is a provider-defined opaque key (for kurogames: the
// KRSDK cuid). UID is the in-game UID, "" when not yet known.
type GameAccount struct {
	ID       string `json:"id"`
	UID      string `json:"uid"`
	Label    string `json:"label"` // App-owned user label; providers leave this empty
	Email    string `json:"email"`
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

// AccountSwitcher is an optional Provider capability: list the launcher-
// remembered accounts for a game and switch which one logs in next. Switching
// mutates only the publisher's own login-pointer state and requires the game to
// be closed. Implementations store no credentials.
type AccountSwitcher interface {
	ListAccounts(ctx context.Context, gid GameID) ([]GameAccount, error)
	SwitchAccount(ctx context.Context, gid GameID, accountID string) error // accountID = GameAccount.ID
}

// GachaLoginResult is the outcome of an email/password gacha login: a durable
// passport token (the value the credential chain consumes) plus identity. No uid
// — the account's uid (roleId) is resolved later by writing back the first
// refresh's res.UID (binding's hashed uid is NOT the record-partition key).
type GachaLoginResult struct {
	Token string
	HgID  string
	Email string
}

// GachaLoginProvider is an optional capability: exchange email+password for a
// durable gacha token without persisting the password. Implemented by Endfield.
type GachaLoginProvider interface {
	LoginByEmailPassword(ctx context.Context, email, password string) (GachaLoginResult, error)
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
// empty backend, empty suffix, OR suffix contains '/' — gids must be
// exactly two segments separated by exactly one slash).
//
// The format is part of the contract: front-end stores, App routing,
// asset URLs, and scanForRecovery's flatten/unflatten logic all depend
// on it.
func ParseGameID(s GameID) (BackendID, string, error) {
	parts := strings.SplitN(string(s), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid game id %q (want <backend>/<suffix>)", s)
	}
	if strings.ContainsRune(parts[1], '/') {
		return "", "", fmt.Errorf("invalid game id %q (suffix must not contain '/')", s)
	}
	return BackendID(parts[0]), parts[1], nil
}
