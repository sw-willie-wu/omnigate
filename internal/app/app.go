package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/hoyoverse"
	"launcher-collection-tmp/internal/providers/hypergryph"
	"launcher-collection-tmp/internal/providers/kurogames"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type detectEntry struct {
	games []core.InstalledGame
	at    time.Time
	err   error
}

type App struct {
	ctx            context.Context
	settings       Settings
	settingsP      string
	providers      []core.Provider
	detect         map[core.BackendID]detectEntry
	detectMu       sync.Mutex
	logger         *slog.Logger
	updateRegistry *UpdateStateRegistry
}

// New returns an App. settingsPath may be "" → default to alongside the binary.
// logger may be nil → uses slog.Default().
func New(settingsPath string, logger *slog.Logger) *App {
	if settingsPath == "" {
		settingsPath = "settings.toml"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s, err := LoadSettings(settingsPath)
	if err != nil {
		logger.Error("settings load failed; using defaults", "err", err, "path", settingsPath)
	}
	a := &App{
		settings:  s,
		settingsP: settingsPath,
		detect:    map[core.BackendID]detectEntry{},
		logger:    logger,
	}
	if err := a.constructProviders(); err != nil {
		logger.Error("provider construction failed", "err", err)
	}

	// Construct update state registry; emitter writes to Wails event bus.
	emit := func(name string, args ...any) {
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, name, args...)
		}
	}
	a.updateRegistry = NewUpdateStateRegistry(emit, realClock{})

	// Spec §2.3: walk <TempDir>/<gameID-flat>/<version>/ for sidecars left
	// behind by an interrupted prior run.
	a.scanForRecovery()

	return a
}

// constructProviders builds the list of providers from current settings. M1
// hoyoverse always present; M2 adds kurogames + hypergryph (constructed in a
// later task). Re-called by UpdateSettings.
func (a *App) constructProviders() error {
	a.providers = nil
	hoyo := hoyoverse.New(
		hoyoverse.Settings{
			Path:   a.settings.Backends.Hoyoverse.Path,
			Region: a.settings.Backends.Hoyoverse.Region,
		},
		a.logger.With("backend", "hoyoverse"),
	)
	if err := a.registerProvider(hoyo); err != nil {
		return err
	}
	kuro := kurogames.New(
		kurogames.Settings{
			Path:    a.settings.Backends.Kurogames.Path,
			TempDir: a.settings.Backends.Kurogames.TempDir,
		},
		a.logger.With("backend", "kurogames"),
	)
	if err := a.registerProvider(kuro); err != nil {
		return err
	}
	gryph := hypergryph.New(
		hypergryph.Settings{Path: a.settings.Backends.Hypergryph.Path},
		a.logger.With("backend", "hypergryph"),
	)
	if err := a.registerProvider(gryph); err != nil {
		return err
	}
	return nil
}

// registerProvider adds a provider to the registry after validating that
// every GameID it emits has the provider's BackendID as the prefix.
func (a *App) registerProvider(p core.Provider) error {
	for _, g := range p.Games() {
		b, _, err := core.ParseGameID(g.ID)
		if err != nil {
			return fmt.Errorf("provider %q emitted invalid game id %q: %w", p.ID(), g.ID, err)
		}
		if b != p.ID() {
			return fmt.Errorf("provider %q emitted game id %q with mismatched backend prefix %q",
				p.ID(), g.ID, b)
		}
	}
	a.providers = append(a.providers, p)
	return nil
}

// provider returns the registered Provider for a given GameID, or
// core.ErrUnknownGame if no match.
func (a *App) provider(gid core.GameID) (core.Provider, error) {
	backendID, _, err := core.ParseGameID(gid)
	if err != nil {
		return nil, err
	}
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}

// byID returns the registered Provider for a backend, or nil if none.
func (a *App) byID(backendID core.BackendID) core.Provider {
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p
		}
	}
	return nil
}

// cachedDetect returns the detection result for a provider, caching it
// across calls. Cache is invalidated by UpdateSettings or Refresh.
func (a *App) cachedDetect(ctx context.Context, p core.Provider) ([]core.InstalledGame, error) {
	a.detectMu.Lock()
	if e, ok := a.detect[p.ID()]; ok && e.err == nil {
		out := e.games
		a.detectMu.Unlock()
		return out, nil
	}
	a.detectMu.Unlock()

	games, err := p.DetectInstall(ctx)

	a.detectMu.Lock()
	a.detect[p.ID()] = detectEntry{games: games, at: time.Now(), err: err}
	a.detectMu.Unlock()

	return games, err
}

// invalidateDetect clears the entire detection cache. Called by UpdateSettings
// (provider settings may have changed paths) and the manual Refresh command.
func (a *App) invalidateDetect() {
	a.detectMu.Lock()
	a.detect = map[core.BackendID]detectEntry{}
	a.detectMu.Unlock()
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// ─── Wails-bound commands (return values must be JSON-serializable) ───

type GameRow struct {
	ID             string               `json:"id"`
	Backend        string               `json:"backend"`
	DisplayName    core.LocalizedString `json:"display_name"`
	Installed      bool                 `json:"installed"`
	InstallPath    string               `json:"install_path,omitempty"`
	Current        string               `json:"current_version,omitempty"`
	Latest         string               `json:"latest_version,omitempty"`
	HasPredownload bool                 `json:"has_predownload"`
	IconURL        string               `json:"icon_url,omitempty"`
}

// BackendStatus is one entry from ListBackends.
type BackendStatus struct {
	BackendID   string               `json:"backend_id"`
	DisplayName core.LocalizedString `json:"display_name"`
	Status      string               `json:"status"` // ok | path_unset | launcher_missing | empty | error
	Detail      string               `json:"detail,omitempty"`
}

func (a *App) ListGames() ([]GameRow, error) {
	out := []GameRow{}
	for _, p := range a.providers {
		installed, err := a.cachedDetect(a.ctx, p)
		if err != nil {
			a.logger.Warn("DetectInstall failed", "backend", p.ID(), "err", err)
			continue
		}
		seen := map[core.GameID]core.InstalledGame{}
		for _, ig := range installed {
			seen[ig.GameID] = ig
		}
		for _, g := range p.Games() {
			row := GameRow{
				ID:          string(g.ID),
				Backend:     string(g.Backend),
				DisplayName: g.DisplayName,
			}
			if ig, ok := seen[g.ID]; ok {
				row.Installed = true
				row.InstallPath = ig.InstallPath
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func (a *App) ListBackends() []BackendStatus {
	out := make([]BackendStatus, 0, len(a.providers))
	for _, p := range a.providers {
		bs := BackendStatus{
			BackendID:   string(p.ID()),
			DisplayName: p.DisplayName(),
		}
		// Path-based status derivation
		var path string
		if pp, ok := p.(core.PathProvider); ok {
			path = pp.PrimaryPath()
		}
		switch {
		case path == "":
			bs.Status = "path_unset"
		default:
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					bs.Status = "launcher_missing"
					bs.Detail = path
				} else {
					bs.Status = "error"
					bs.Detail = err.Error()
				}
			} else {
				games, err := a.cachedDetect(a.ctx, p)
				switch {
				case err != nil:
					bs.Status = "error"
					bs.Detail = err.Error()
				case len(games) == 0:
					bs.Status = "empty"
				default:
					bs.Status = "ok"
				}
			}
		}
		out = append(out, bs)
	}
	return out
}

func (a *App) RefreshVersion(gameID string) (core.VersionInfo, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return core.VersionInfo{}, err
	}
	vi, err := p.CheckVersion(a.ctx, gid)
	if err != nil {
		return vi, err
	}

	// Spec §2.4 phantom-predl: PredlReady becomes invalid when the install
	// version equals the predl version (user reinstalled at that version, or
	// KRLauncher applied externally). Silently delete sidecar + clear PredlReady.
	if a.updateRegistry != nil {
		state := a.updateRegistry.Get(gid)
		state.mu.RLock()
		predl := state.PredlReady
		state.mu.RUnlock()
		if predl != nil && vi.Current == predl.Version {
			tempDir := a.tempDirFor(kurogames.BackendID, gid)
			gameIDFlat := strings.ReplaceAll(string(gid), "/", "-")
			versionDir := filepath.Join(tempDir, gameIDFlat, predl.Version)
			_ = removeAll(versionDir)
			state.mu.Lock()
			state.PredlReady = nil
			state.mu.Unlock()
			a.updateRegistry.EmitTerminal(gid)
		}
	}
	return vi, err
}

func (a *App) GetIcon(gameID string) (string, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return "", err
	}
	return p.GetIcon(a.ctx, core.GameID(gameID))
}

func (a *App) GetBackgrounds(gameID string) ([]core.Background, error) {
	p, err := a.provider(core.GameID(gameID))
	if err != nil {
		return nil, err
	}
	return p.GetBackgrounds(a.ctx, core.GameID(gameID))
}

func (a *App) Launch(gameID string) (int, error) {
	gid := core.GameID(gameID)

	// M3.A: refuse if apply phase is in flight (spec §2.7)
	if a.updateRegistry != nil {
		state := a.updateRegistry.Get(gid)
		state.mu.RLock()
		blocked := state.InFlight != nil && state.InFlight.Phase == core.PhaseApply
		state.mu.RUnlock()
		if blocked {
			return 0, fmt.Errorf("game %s: apply in progress; please wait", gameID)
		}
	}

	p, err := a.provider(gid)
	if err != nil {
		return 0, err
	}
	return p.Launch(a.ctx, gid, core.LaunchOptions{})
}

func (a *App) GetSettings() Settings { return a.settings }

func (a *App) UpdateSettings(s Settings) error {
	if err := SaveSettings(a.settingsP, s); err != nil {
		return err
	}
	a.settings = s
	a.invalidateDetect()
	return a.constructProviders()
}

// Refresh clears the detection cache. Wails-bound; the frontend's manual
// refresh button calls this.
func (a *App) Refresh() {
	a.invalidateDetect()
}

// scanForRecovery is defined in update_handler.go — moved out of app.go
// (the stub previously here was incorrect; see commit fix below).

// ErrorCode exposes the core.ErrorCode mapping to the frontend.
func (a *App) ErrorCode(s string) string {
	if s == "" {
		return "internal"
	}
	// Frontend passes the err.message string back; match against the sentinels'
	// .Error() values (works because we wrap with %w and the wrapped chain
	// carries the sentinel).
	for _, sentinel := range []error{
		core.ErrUnknownGame, core.ErrGameNotInstalled, core.ErrBackendNotConfigured,
		core.ErrLauncherMissing, core.ErrAssetNotAvailable,
	} {
		if filepath.Clean(s) == sentinel.Error() || strContains(s, sentinel.Error()) {
			return core.ErrorCode(fmt.Errorf("wrap: %w", sentinel))
		}
	}
	return "internal"
}

// ErrorMessage returns a localized human string for the given JSON code.
// M2 ships with English messages only; M3 can route through vue-i18n.
func (a *App) ErrorMessage(code string) string {
	switch code {
	case "unknown_game":
		return "Unknown game."
	case "not_installed":
		return "Game is not installed."
	case "not_configured":
		return "Backend not configured. Set the launcher path in settings.toml."
	case "launcher_missing":
		return "Launcher folder not found at the configured path."
	case "asset_unavailable":
		return "Asset is not available."
	default:
		return "Internal error."
	}
}

// resolveSettingsPath returns ./settings.toml relative to the binary.
func resolveSettingsPath() string { return filepath.Join(".", "settings.toml") }

func strContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// tempDirFor resolves the per-backend temp root for sidecar/staging files.
// In v0.3.1 only kurogames has a configurable TempDir; hoyoverse / hypergryph
// cases will be added in M3.B / M3.C alongside their respective settings
// fields. The default branch is currently unreachable in production (no
// non-kurogames caller exists yet) but exists so future cases can be added
// without modifying call sites.
//
// Bit-exact preservation for kurogames: returns the same value as the legacy
// kurogamesTempDir helper — settings-override OR <TEMP>/launcher-collection
// (no backend/gid suffix; per-game flattening happens inside progressStore).
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	switch backend {
	case kurogames.BackendID:
		if td := a.settings.Backends.Kurogames.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "launcher-collection")
	}
	// Default for backends without a settings TempDir field: per-backend subdir
	// to avoid collisions. Unreachable in v0.3.1.
	return filepath.Join(osTempDir(), "launcher-collection", string(backend))
}
