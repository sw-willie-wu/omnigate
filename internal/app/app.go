package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"omnigate/internal/core"
	"omnigate/internal/gachaicon"
	"omnigate/internal/providers/hoyoverse"
	"omnigate/internal/providers/hypergryph"
	"omnigate/internal/providers/kurogames"
	"omnigate/internal/store"
)

type detectEntry struct {
	games []core.InstalledGame
	at    time.Time
	err   error
}

type App struct {
	ctx            context.Context
	settings       Settings
	dataDir        string
	store          store.StateStore
	providers      []core.Provider
	detect         map[core.BackendID]detectEntry
	resolved       map[core.GameID]resolvedEntry
	detectMu       sync.Mutex
	settingsMu     sync.RWMutex // guards a.settings + a.providers (spec §2.5)
	logger         *slog.Logger
	updateRegistry *UpdateStateRegistry
	playState      *playState
	gachaStore     store.GachaStore
	gachaIcons     *gachaicon.Manager
	uidCache       *uidCache
	pendingElevate string // gid from --elevate-update; consumed once by PendingElevatedGame
}

// New returns an App. dataDir is the directory holding omnigate.db (plus the log
// and the WebView2 .cache). logger may be nil → uses slog.Default().
func New(dataDir string, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	pendingBak, _ := promoteGachaDB(dataDir, logger) // crash-safe gacha.db → omnigate.db
	dbPath := filepath.Join(dataDir, "omnigate.db")
	a := &App{
		dataDir:  dataDir,
		detect:   map[core.BackendID]detectEntry{},
		resolved: map[core.GameID]resolvedEntry{},
		logger:   logger,
	}
	if st, gerr := store.OpenSQLite(dbPath); gerr != nil {
		logger.Error("omnigate.db open failed; running with in-memory defaults", "err", gerr, "path", dbPath)
		a.settings = defaultSettings()
		a.playState = loadPlayState(nil)
		a.uidCache = loadUIDCache(nil)
	} else {
		a.store = st
		a.gachaStore = st
		if pendingBak != "" { // promotion succeeded AND DB opened → retire the source
			_ = os.Rename(pendingBak, pendingBak+".bak")
		}
		importLegacyFiles(dataDir, st, logger) // BEFORE the loads below (spec §6.4)
		if s, lerr := loadSettingsFromDB(st); lerr == nil {
			a.settings = s
		} else {
			logger.Error("settings load failed; using defaults", "err", lerr)
			a.settings = defaultSettings()
		}
		a.playState = loadPlayState(st)
		a.uidCache = loadUIDCache(st)
	}
	if err := a.constructProviders(); err != nil {
		logger.Error("provider construction failed", "err", err)
	}

	// Gacha icon manager: resolves headline records → /_asset icon URLs and warms
	// per-game indices in the background. Its onWarm callback notifies the frontend.
	a.gachaIcons = gachaicon.NewManager(dataDir, logger.With("comp", "gachaicon"))
	a.gachaIcons.SetOnWarm(func(gid core.GameID) { a.emit("gacha:icons", string(gid)) })

	// Construct update state registry; emitter writes to Wails event bus.
	a.updateRegistry = NewUpdateStateRegistry(a.emit, realClock{})

	// Spec §2.3: walk <TempDir>/<gameID-flat>/<version>/ for sidecars left
	// behind by an interrupted prior run.
	a.scanForRecovery()

	// Capture the --elevate-update <gid> arg passed by an elevated relaunch so
	// the frontend can auto-select + auto-start that game's update (as admin).
	a.pendingElevate = parseElevateArg(os.Args)

	return a
}

// emit writes an event to the Wails event bus (no-op before the runtime is
// ready / in tests where a.ctx is nil). Shared by the update registry and gacha.
func (a *App) emit(name string, args ...any) {
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, name, args...)
	}
}

// constructProviders builds the provider list from current settings.
//
// LOCKING (spec §2.5): this is LOCK-FREE and MUST NOT acquire settingsMu. It is
// called only from New (pre-concurrency) and from UpdateSettings while UpdateSettings
// already holds the settingsMu WRITE lock. Its inline a.settings reads are covered by
// that write lock. The SetTempRootFn closure it installs is only INVOKED later (from
// update operations), where tempDirFor takes a fresh RLock — never during construction.
func (a *App) constructProviders() error {
	a.providers = nil
	hoyo := hoyoverse.New(
		hoyoverse.Settings{Region: a.settings.Backends.Hoyoverse.Region},
		a.logger.With("backend", "hoyoverse"),
	)
	hoyo.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(hoyoverse.BackendID, gid) })
	if err := a.registerProvider(hoyo); err != nil {
		return err
	}

	kuro := kurogames.New(kurogames.Settings{}, a.logger.With("backend", "kurogames"))
	kuro.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(kurogames.BackendID, gid) })
	if err := a.registerProvider(kuro); err != nil {
		return err
	}

	gryph := hypergryph.New(hypergryph.Settings{}, a.logger.With("backend", "hypergryph"))
	gryph.SetTempRootFn(func(gid core.GameID) string { return a.tempDirFor(hypergryph.BackendID, gid) })
	if err := a.registerProvider(gryph); err != nil {
		return err
	}

	// Resolve + inject per-game install folders. a.ctx is nil at New time (Wails
	// sets it in Startup); provider DefaultScan→DetectInstall selects on
	// ctx.Done(), so substitute a non-nil ctx to avoid a nil-deref panic.
	a.resolveAll(a.resolveCtx())
	return nil
}

// resolveAll resolves every provider's games and injects the resolved folders.
// MUST be called with settingsMu held for write (or pre-concurrency from New) —
// it reads a.settings.Games WITHOUT locking to avoid RWMutex self-deadlock.
func (a *App) resolveAll(ctx context.Context) {
	if a.resolved == nil {
		a.resolved = map[core.GameID]resolvedEntry{}
	}
	for _, p := range a.providers {
		a.resolveProviderLocked(ctx, p)
	}
}

// resolveProviderLocked resolves one provider's games, writes the results into
// a.resolved, and injects the resolved folders via SetResolvedPaths.
//
// LOCKING: same contract as resolveAll — MUST be called with settingsMu held for
// write (or pre-concurrency from New). It reads a.settings.Games and writes
// a.resolved WITHOUT internal locking to avoid RWMutex self-deadlock.
func (a *App) resolveProviderLocked(ctx context.Context, p core.Provider) {
	if a.resolved == nil {
		a.resolved = map[core.GameID]resolvedEntry{}
	}
	sc, ok := p.(backendScanner)
	if !ok {
		return
	}
	var gids []core.GameID
	for _, g := range p.Games() {
		gids = append(gids, g.ID)
	}
	entries := resolveBackendLocked(ctx, sc, gids, a.settings.Games)
	inj := make(map[core.GameID]string, len(entries))
	for gid, e := range entries {
		a.resolved[gid] = e
		inj[gid] = e.Path // inject literal path; provider DetectInstall stat-gates existence
	}
	if rp, ok := p.(core.ResolvedPathSetter); ok {
		rp.SetResolvedPaths(inj)
	}
}

// resolveCtx returns a.ctx, substituting context.Background() when Wails has not
// yet called Startup (a.ctx nil) to avoid a nil-deref in provider scans.
func (a *App) resolveCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
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
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	for _, p := range a.providers {
		if p.ID() == backendID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
}

// byID returns the registered Provider for a backend, or nil if none.
func (a *App) byID(backendID core.BackendID) core.Provider {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
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

// invalidateDetectFor clears the detection cache entry for a single backend.
// Used by the per-game override RPCs so serveIcon's cachedDetect re-runs for the
// affected backend without dropping every other backend's cache.
func (a *App) invalidateDetectFor(id core.BackendID) {
	a.detectMu.Lock()
	delete(a.detect, id)
	a.detectMu.Unlock()
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// Close releases App-held resources (gacha DB). Safe to call once.
func (a *App) Close() {
	if a.gachaStore != nil {
		a.gachaStore.Close()
	}
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
	ResolvedPath   string               `json:"resolved_path,omitempty"`
	PathSource     string               `json:"path_source"`
	OverridePath   string               `json:"override_path,omitempty"`
	LastPlayed     string               `json:"last_played,omitempty"`
}

// BackendStatus is one entry from ListBackends.
type BackendStatus struct {
	BackendID   string               `json:"backend_id"`
	DisplayName core.LocalizedString `json:"display_name"`
	Status      string               `json:"status"` // ok | empty
	Detail      string               `json:"detail,omitempty"`
}

func (a *App) ListGames() ([]GameRow, error) {
	// a.resolved is the source of truth for install paths; read it (and the
	// override map) under the same RLock that snapshots providers.
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	out := []GameRow{}
	for _, p := range a.providers {
		for _, g := range p.Games() {
			out = append(out, a.gameRowLocked(p, g))
		}
	}
	return out, nil
}

// statModTime returns a path's mtime, or (zero,false) if it cannot be stat'd.
// Package var so tests can stub it (mirrors the osTempDir/osRemoveAll seams).
var statModTime = func(p string) (time.Time, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// lastPlayedLocked returns the effective last-played time for gid: the later of
// the recorded playstate timestamp and the mtime of any LastPlayedProbe file
// (which reflects play outside omnigate). Caller holds settingsMu (R or W) —
// same lock discipline as gameRowLocked. playState may be nil (test helpers).
func (a *App) lastPlayedLocked(p core.Provider, gid core.GameID, installDir string) time.Time {
	var ts time.Time
	if a.playState != nil {
		ts = a.playState.Get(string(gid))
	}
	if probe, ok := p.(core.LastPlayedProbe); ok {
		for _, f := range probe.LastPlayedFiles(gid, installDir) {
			if mt, ok := statModTime(f); ok && mt.After(ts) {
				ts = mt
			}
		}
	}
	return ts
}

// gameRowLocked builds a single GameRow from a.resolved + a.settings.Games.
//
// LOCKING: the caller MUST hold settingsMu for read (or write); this reads both
// maps WITHOUT locking.
func (a *App) gameRowLocked(p core.Provider, g core.GameDescriptor) GameRow {
	e := a.resolved[g.ID]
	row := GameRow{
		ID:           string(g.ID),
		Backend:      string(g.Backend),
		DisplayName:  g.DisplayName,
		PathSource:   string(e.Source),
		ResolvedPath: e.Path,
		InstallPath:  e.Path,
		Installed:    e.Source != core.SourceUnresolved && statDir(e.Path),
		OverridePath: a.settings.Games[string(g.ID)].Path,
	}
	if ts := a.lastPlayedLocked(p, g.ID, e.Path); !ts.IsZero() {
		row.LastPlayed = ts.Format(time.RFC3339)
	}
	return row
}

// SetGameOverride sets an explicit install-folder override for one game,
// persists settings, re-resolves the owning provider, and returns the updated
// row. Spec §6.1 / plan CR-4.
func (a *App) SetGameOverride(gameID, path string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return GameRow{}, err
	}
	a.settingsMu.Lock()
	if a.settings.Games == nil {
		a.settings.Games = map[string]GameSettings{}
	}
	// Read-modify-write so a custom BackgroundPath on this game survives a path change.
	g := a.settings.Games[gameID]
	g.Path = path
	a.settings.Games[gameID] = g
	if a.store != nil {
		if err := saveSettingsToDB(a.store, a.settings); err != nil {
			a.settingsMu.Unlock()
			return GameRow{}, err
		}
	}
	a.resolveProviderLocked(a.resolveCtx(), p) // re-resolve+inject under the write lock
	a.settingsMu.Unlock()
	a.invalidateDetectFor(p.ID()) // so serveIcon's cachedDetect re-runs
	return a.gameRow(gid, p)
}

// ClearGameOverride removes any install-folder override for one game, persists
// settings, re-resolves the owning provider (reverting to launcher/default
// detection), and returns the updated row. Spec §6.1 / plan CR-4.
func (a *App) ClearGameOverride(gameID string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return GameRow{}, err
	}
	a.settingsMu.Lock()
	// Preserve a custom BackgroundPath when clearing only the path override; drop
	// the whole entry only if there's nothing else to keep.
	if g, ok := a.settings.Games[gameID]; ok && g.BackgroundPath != "" {
		g.Path = ""
		a.settings.Games[gameID] = g
	} else {
		delete(a.settings.Games, gameID)
	}
	if a.store != nil {
		if err := saveSettingsToDB(a.store, a.settings); err != nil {
			a.settingsMu.Unlock()
			return GameRow{}, err
		}
	}
	a.resolveProviderLocked(a.resolveCtx(), p)
	a.settingsMu.Unlock()
	a.invalidateDetectFor(p.ID())
	return a.gameRow(gid, p)
}

// RefreshGame re-resolves the owning provider for one game WITHOUT changing
// settings, then returns the updated row. Used to pick up filesystem changes
// (e.g. a game installed/removed out-of-band). Spec §6.1 / plan CR-4.
func (a *App) RefreshGame(gameID string) (GameRow, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return GameRow{}, err
	}
	a.settingsMu.Lock()
	a.resolveProviderLocked(a.resolveCtx(), p)
	a.settingsMu.Unlock()
	a.invalidateDetectFor(p.ID())
	return a.gameRow(gid, p)
}

// gameRow returns the single updated GameRow for gid from provider p. It takes
// settingsMu for read; callers MUST have released any write lock first.
func (a *App) gameRow(gid core.GameID, p core.Provider) (GameRow, error) {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	for _, g := range p.Games() {
		if g.ID == gid {
			return a.gameRowLocked(p, g), nil
		}
	}
	return GameRow{}, fmt.Errorf("unknown game %s", gid)
}

// GetNews returns the public news feed for gameID in the given UI language
// (en/zh-TW/zh-CN), or an empty slice if the game's provider does not implement
// NewsProvider or the fetch fails. lang is passed explicitly by the frontend
// (the current i18n locale) so the result is deterministic and does not race
// the async App.SetLanguage persistence. Best-effort: a fetch error is logged
// and surfaced (the frontend shows empty/error state), never fatal.
func (a *App) GetNews(gameID string, lang string) ([]core.NewsItem, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return nil, err
	}
	np, ok := p.(core.NewsProvider)
	if !ok {
		return []core.NewsItem{}, nil
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	items, err := np.GetNews(ctx, gid, lang)
	if err != nil {
		a.logger.Warn("GetNews failed", "gid", gameID, "err", err)
		return nil, err
	}
	// Simplified-Chinese fallback: WuWa and Endfield only publish zh-TW / en news
	// on their global feeds (zh-CN source is 404 / empty list). Rather than show a
	// zh-CN user a blank panel, fall back to the Traditional-Chinese feed.
	if len(items) == 0 && lang == "zh-CN" {
		if alt, aerr := np.GetNews(ctx, gid, "zh-TW"); aerr == nil && len(alt) > 0 {
			items = alt
		}
	}
	if items == nil {
		items = []core.NewsItem{}
	}
	return items, nil
}

// OpenExternalURL opens rawURL in the user's default browser. Only http/https
// are allowed (reject file://, javascript:, etc. to avoid arbitrary-scheme
// launch). No-op if the Wails ctx is not yet set.
func (a *App) OpenExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing to open non-http(s) url scheme %q", u.Scheme)
	}
	if a.ctx == nil {
		return nil
	}
	wruntime.BrowserOpenURL(a.ctx, rawURL)
	return nil
}

func (a *App) ListBackends() []BackendStatus {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	out := make([]BackendStatus, 0, len(a.providers))
	for _, p := range a.providers {
		bs := BackendStatus{
			BackendID:   string(p.ID()),
			DisplayName: p.DisplayName(),
			Status:      "empty",
		}
		// Status is derived from a.resolved: "ok" if any of the backend's games
		// resolves to a stat-valid directory, else "empty".
		for _, g := range p.Games() {
			e := a.resolved[g.ID]
			if e.Source != core.SourceUnresolved && statDir(e.Path) {
				bs.Status = "ok"
				break
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
	// Single source of truth for "update available" — same predicate
	// CheckForUpdate uses (numeric, not string inequality: a rollover-lag
	// window where local is ahead of the API must not read as an update).
	// The sidebar/UI consumes this flag verbatim.
	vi.UpdateAvailable = updateAvailable(vi)

	// Spec §2.4 phantom-predl: PredlReady becomes invalid when the install
	// version equals the predl version (user reinstalled at that version, or
	// KRLauncher applied externally). Silently delete sidecar + clear PredlReady.
	if a.updateRegistry != nil {
		state := a.updateRegistry.Get(gid)
		state.mu.RLock()
		predl := state.PredlReady
		state.mu.RUnlock()
		if predl != nil && vi.Current == predl.Version {
			tempDir := a.tempDirFor(p.ID(), gid)
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

// accountIsActive reports whether accountID is the currently-written active one.
func accountIsActive(accts []core.GameAccount, accountID string) bool {
	for _, ac := range accts {
		if ac.ID == accountID {
			return ac.Active
		}
	}
	return false
}

func (a *App) Launch(gameID, accountID string) (int, error) {
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

	// Commit the selected account before launching (switcher games only). "" or
	// the already-active account → no write. SwitchAccount's own game-running
	// gate is the safety net against a stale click.
	if sw, ok := p.(core.AccountSwitcher); ok && accountID != "" {
		accts, lerr := sw.ListAccounts(a.ctx, gid)
		if lerr != nil {
			return 0, lerr // can't verify the target → don't silently launch the wrong account
		}
		if !accountIsActive(accts, accountID) {
			if serr := sw.SwitchAccount(a.ctx, gid, accountID); serr != nil {
				return 0, serr // e.g. core.ErrGameRunning
			}
		}
	}

	pid, err := p.Launch(a.ctx, gid, core.LaunchOptions{})
	if err == nil && a.playState != nil {
		a.playState.Record(string(gid))
	}
	return pid, err
}

// IsGameRunning reports whether the game's process is currently running, via the
// provider's optional ProcessChecker. Providers without the capability → false.
func (a *App) IsGameRunning(gameID string) (bool, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return false, err
	}
	pc, ok := p.(core.ProcessChecker)
	if !ok {
		return false, nil
	}
	return pc.IsGameRunning(gid)
}

func (a *App) GetSettings() Settings {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings
}

func (a *App) UpdateSettings(s Settings) error {
	// Disk write first; it touches neither a.settings nor a.providers.
	if a.store != nil {
		if err := saveSettingsToDB(a.store, s); err != nil {
			return err
		}
	}
	a.settingsMu.Lock()
	a.settings = s
	err := a.constructProviders() // lock-free; runs under this write lock
	a.settingsMu.Unlock()
	a.invalidateDetect()
	return err
}

// SetLanguage persists the UI language preference. Unlike UpdateSettings it
// touches only App.Language and skips the provider rebuild + detection-cache
// invalidation, so the Topbar language toggle stays cheap (no game re-probe).
func (a *App) SetLanguage(lang string) error {
	switch lang {
	case "zh-TW", "zh-CN", "en":
	default:
		return fmt.Errorf("unsupported language %q", lang)
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	s := a.settings
	s.App.Language = lang
	if a.store != nil {
		if err := saveSettingsToDB(a.store, s); err != nil {
			return err
		}
	}
	a.settings = s
	return nil
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
		return "Backend not configured. Set the launcher path in Settings."
	case "launcher_missing":
		return "Launcher folder not found at the configured path."
	case "asset_unavailable":
		return "Asset is not available."
	default:
		return "Internal error."
	}
}

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

// imageMIME maps a lowercased file extension to its image MIME type. We do NOT
// use mime.TypeByExtension — webp/bmp are unreliable on Windows registries.
func imageMIME(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	}
	return "", false
}

// GetCustomBackground returns the per-game custom background as a base64 data
// URL, or "" (nil error) when no custom path is set. Read errors / unknown
// extensions return a non-nil error so the frontend falls back to official art.
func (a *App) GetCustomBackground(gameID string) (string, error) {
	a.settingsMu.RLock()
	path := a.settings.Games[gameID].BackgroundPath
	a.settingsMu.RUnlock()
	if path == "" {
		return "", nil
	}
	mimeType, ok := imageMIME(filepath.Ext(path))
	if !ok {
		return "", fmt.Errorf("unsupported image extension: %s", filepath.Ext(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// DefaultTempRoot returns the effective temp root used when App.TempDir is empty
// (<os.TempDir>/omnigate). Surfaced to the settings UI so the user can see where
// downloads / predownloads are actually staged by default. Bound as a Wails RPC.
func (a *App) DefaultTempRoot() string {
	return filepath.Join(osTempDir(), "omnigate")
}

// tempDirFor resolves the per-backend temp root for sidecar/staging files.
//
// App.TempDir is the single source of truth. When empty it falls back to
// <os.TempDir>/omnigate so existing defaults are preserved bit-exactly:
//
//	kurogames  → root (flat, legacy bit-exact)
//	hoyoverse  → root/hoyoverse
//	hypergryph → root/hypergryph
//	other      → root/<backend>
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	root := a.settings.App.TempDir
	if root == "" {
		root = filepath.Join(osTempDir(), "omnigate")
	}
	switch backend {
	case kurogames.BackendID:
		return root // flat (legacy bit-exact)
	case hoyoverse.BackendID:
		return filepath.Join(root, "hoyoverse")
	case hypergryph.BackendID:
		return filepath.Join(root, "hypergryph")
	}
	return filepath.Join(root, string(backend))
}
