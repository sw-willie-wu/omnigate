package kurogames

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"launcher-collection-tmp/internal/core"
)

type Settings struct {
	Path    string // launcher install root
	TempDir string // optional override; empty → app layer's kurogamesTempDir() default
}

type Provider struct {
	settings   Settings
	logger     *slog.Logger
	httpClient *http.Client     // for manifest + downloads; injected from app layer
	clock      RetryClock       // for download retry backoff (test-only injection)
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		settings:   settings,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		clock:      realRetryClock{},
	}
}

func (p *Provider) ID() core.BackendID { return BackendID }

func (p *Provider) DisplayName() core.LocalizedString {
	return core.LocalizedString{"zh-TW": "庫洛", "zh-CN": "库洛", "en": "Kuro Games"}
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
				"zh-TW": "鳴潮 launcher 安裝資料夾",
				"zh-CN": "鸣潮 launcher 安装文件夹",
				"en":    "Wuthering Waves launcher folder",
			}},
	}
}

func (p *Provider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return DetectInstall(ctx, p.settings.Path)
}

// GetIcon returns the canonical asset URL; the AssetServer middleware does
// the actual PE extraction via iconext on demand.
func (p *Provider) GetIcon(_ context.Context, gid core.GameID) (string, error) {
	g := findByID(gid)
	if g == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_, suffix, _ := core.ParseGameID(gid)
	return fmt.Sprintf("/_asset/%s/icon/%s", p.ID(), suffix), nil
}

// GetBackgrounds returns the external URL directly (EDIT 1 — deviation from plan).
// The caller (WebView2) fetches the image directly from the CDN without middleware.
func (p *Provider) GetBackgrounds(_ context.Context, gid core.GameID) ([]core.Background, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	_ = g // future: per-game URL routing
	img, vid := CurrentBg(p.logger)
	return []core.Background{
		{
			ImageURL: img,
			VideoURL: vid,
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

// PrimaryPath implements core.PathProvider.
func (p *Provider) PrimaryPath() string { return p.settings.Path }

// ExeName implements core.ExeNamer (used by the AssetServer middleware for
// kind=icon to locate the .exe).
func (p *Provider) ExeName(gid core.GameID) (string, bool) {
	g := findByID(gid)
	if g == nil {
		return "", false
	}
	return g.ExeName, true
}

// CheckForUpdate fetches the manifest, filters out files identical to
// the current install, returns a populated UpdatePlan. M3.A only.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}

	// Find install path
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	var installPath string
	for _, ig := range installs {
		if ig.GameID == gid {
			installPath = ig.InstallPath
			break
		}
	}
	if installPath == "" {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
	}

	// AppCred is hardcoded (per research markdown 2026-05-05); no extraction.
	// Read current local version from launcherDownloadConfig.json.
	localVersion, _ := readLauncherDownloadConfigVersion(filepath.Join(installPath, "launcherDownloadConfig.json"))

	// Two-step manifest fetch:
	// 1. GET index.json → discover CDN list + per-version indexFile URL
	idx, idxETag, err := fetchIndex(ctx, p.httpClient, indexJSONURL())
	if err != nil {
		return core.UpdatePlan{}, err
	}
	cfg, _ := pickIndexFileForVersion(idx, localVersion)
	cdn := pickCDN(idx.Default.CDNList)
	indexFileURL := cdn + cfg.IndexFile

	// 2. GET indexFile.json → discover file list with MD5 + size
	idxFile, _, err := fetchIndexFile(ctx, p.httpClient, indexFileURL)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// Filter to changed files only
	files := filterChangedFiles(installPath, cdn, cfg.BaseURL, idxFile.Resource, p.logger)
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}

	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanUpdate,
		ManifestETag: idxETag,
		Version:      cfg.Version,
		Files:        files,
		TotalBytes:   totalBytes,
	}
	p.logger.Info("CheckForUpdate complete",
		"game", gid,
		"local_version", localVersion,
		"target_version", cfg.Version,
		"files_to_update", len(files),
		"bytes", totalBytes,
	)
	return plan, nil
}

// RunUpdate executes a previously-checked plan. Re-verifies ETag at entry,
// dispatches download phase, then apply phase (skipped for PlanPredownload).
// Panic recovery + structured error per spec §6.4.
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("RunUpdate panic", "game", plan.GameID, "panic", r)
			err = &core.UpdateError{
				Code:      "internal",
				Retryable: true,
				Params:    map[string]string{"detail": fmt.Sprint(r)},
			}
		}
	}()

	if ctx.Err() != nil {
		return ctx.Err() // cancel-before-start
	}

	g := findByID(plan.GameID)
	if g == nil {
		return fmt.Errorf("%w: %s", core.ErrUnknownGame, plan.GameID)
	}

	// Find install path
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return err
	}
	var installPath string
	for _, ig := range installs {
		if ig.GameID == plan.GameID {
			installPath = ig.InstallPath
			break
		}
	}
	if installPath == "" {
		return fmt.Errorf("%w: %s", core.ErrGameNotInstalled, plan.GameID)
	}

	// 2nd game-running guard (spec §2.7)
	if isProcessRunning(g.ExeName) {
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "process_running", "game": string(plan.GameID)},
		}
	}

	// Re-verify ETag at entry: re-fetch index.json (~17 KiB gzipped).
	if currentIdx, currentETag, err := fetchIndex(ctx, p.httpClient, indexJSONURL()); err == nil && currentETag != "" && currentETag != plan.ManifestETag {
		_ = currentIdx
		return &core.UpdateError{
			Code:      "manifest_changed",
			Retryable: true,
			Params:    map[string]string{"old_etag": plan.ManifestETag, "new_etag": currentETag},
		}
	}

	// Determine TempDir — root only; newProgressStore.dir() appends gameID/version.
	tempDir := p.settings.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "launcher-collection")
	}

	progress := newProgressStore(tempDir, string(plan.GameID), plan.Version)
	if err := progress.Init(plan.ManifestETag); err != nil {
		return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
	}

	// Download phase
	d := &downloader{
		client:   p.httpClient,
		logger:   p.logger,
		tempRoot: tempDir,
		progress: progress,
		plan:     &plan,
		onEvent:  onEvent,
		clock:    p.clock,
	}
	if err := d.runDownload(ctx); err != nil {
		return err
	}

	// Predl: rename progress.json → predl_ready.json and stop
	if plan.Kind == core.PlanPredownload {
		if err := progress.RenameToPredlReady(); err != nil {
			return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
		}
		return nil
	}

	// Apply phase
	a := &applier{
		logger:   p.logger,
		tempRoot: tempDir,
		gameDir:  installPath,
		progress: progress,
		plan:     &plan,
		wasPredl: false,
		onEvent:  onEvent,
		lock:     newApplyLock(),
	}
	return a.runApply(ctx)
}

// isProcessRunning checks if the given exe name appears in the process list.
// Uses Windows toolhelp snapshot (kurogames is Windows-only). Stub for
// non-Windows builds always returns false.
func isProcessRunning(exeName string) bool {
	return platformIsProcessRunning(exeName)
}

// IsProcessRunning is exported so app layer can do the 1st-point game-running
// guard at RPC entry without re-implementing process enumeration.
func IsProcessRunning(exeName string) bool {
	return platformIsProcessRunning(exeName)
}

// compile-time interface compliance (EDIT 3 — deviation: removed AssetServer)
var (
	_ core.Provider     = (*Provider)(nil)
	_ core.PathProvider = (*Provider)(nil)
	_ core.ExeNamer     = (*Provider)(nil)
	_ core.Updater      = (*Provider)(nil) // M3.A: implements update interface
)
