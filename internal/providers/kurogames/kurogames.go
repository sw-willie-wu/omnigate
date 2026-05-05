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
			vi, err := fetchVersion(ctx, ig.InstallPath, gid)
			if err != nil {
				return vi, err
			}
			// Best-effort: fetch index.json (~17 KiB) so vi.Latest reflects
			// what the server is actually shipping. Network blip → fall back
			// to local-only (fetchVersion already set Latest = Current).
			// 10s budget keeps Refresh responsive even on slow connections.
			fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if idx, _, ferr := fetchIndex(fetchCtx, p.httpClient, indexJSONURL()); ferr == nil {
				if idx.Default.Version != "" {
					vi.Latest = idx.Default.Version
				}
				if idx.Predownload != nil && idx.Predownload.Version != "" && idx.Predownload.Version != vi.Current {
					vi.Predownload = &core.PredownloadInfo{TargetVersion: idx.Predownload.Version}
				}
			}
			return vi, nil
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

// IsGameRunning implements core.ProcessChecker — used by App layer's update
// flow as the 1st-point game-running guard. Composes ExeName (the existing
// core.ExeNamer impl) with the build-tag-gated platformIsProcessRunning.
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
	exe, ok := p.ExeName(gid)
	if !ok {
		return false, nil
	}
	return platformIsProcessRunning(exe), nil
}

// CheckForUpdate fetches the manifest, filters out files identical to
// the current install, returns a populated UpdatePlan. M3.A only.
//
// Implemented as a thin wrapper around CheckForUpdateWithProgress so the
// progress-reporting path is the canonical implementation; this method just
// passes a nil callback for callers that don't care about verify progress.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	return p.CheckForUpdateWithProgress(ctx, gid, nil)
}

// CheckForUpdateWithProgress is the progress-reporting variant. onProgress
// fires after each local file is examined during filterChangedFiles
// (`done` files of `total` total examined). Implements
// core.CheckForUpdateProgress.
func (p *Provider) CheckForUpdateWithProgress(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	p.logger.Debug("kurogames CheckForUpdate: enter", "game", gid)
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}

	// Find install path
	p.logger.Debug("kurogames CheckForUpdate: DetectInstall", "game", gid, "settings_path", p.settings.Path)
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		p.logger.Warn("kurogames CheckForUpdate: DetectInstall failed", "game", gid, "err", err)
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
		p.logger.Warn("kurogames CheckForUpdate: install path empty", "game", gid, "installs_count", len(installs))
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
	}
	p.logger.Debug("kurogames CheckForUpdate: install path resolved", "game", gid, "install_path", installPath)

	// AppCred is hardcoded (per research markdown 2026-05-05); no extraction.
	// Read current local version from launcherDownloadConfig.json.
	localVersion, _ := readLauncherDownloadConfigVersion(filepath.Join(installPath, "launcherDownloadConfig.json"))
	p.logger.Debug("kurogames CheckForUpdate: localVersion read", "game", gid, "local_version", localVersion)

	// Two-step manifest fetch:
	// 1. GET index.json → discover CDN list + per-version indexFile URL
	p.logger.Debug("kurogames CheckForUpdate: fetchIndex start", "game", gid, "url", indexJSONURL())
	idx, idxETag, err := fetchIndex(ctx, p.httpClient, indexJSONURL())
	if err != nil {
		p.logger.Warn("kurogames CheckForUpdate: fetchIndex failed", "game", gid, "err", err)
		return core.UpdatePlan{}, err
	}
	p.logger.Debug("kurogames CheckForUpdate: fetchIndex done", "game", gid, "default_version", idx.Default.Version, "etag", idxETag)
	cfg, _ := pickIndexFileForVersion(idx, localVersion)
	cdn := pickCDN(idx.Default.CDNList)
	indexFileURL := cdn + cfg.IndexFile
	p.logger.Debug("kurogames CheckForUpdate: fetchIndexFile start", "game", gid, "url", indexFileURL)

	// 2. GET indexFile.json → discover file list with MD5 + size
	idxFile, _, err := fetchIndexFile(ctx, p.httpClient, indexFileURL)
	if err != nil {
		p.logger.Warn("kurogames CheckForUpdate: fetchIndexFile failed", "game", gid, "err", err)
		return core.UpdatePlan{}, err
	}
	p.logger.Debug("kurogames CheckForUpdate: fetchIndexFile done", "game", gid, "resource_count", len(idxFile.Resource))

	// Filter to changed files only — onProgress fires after each file.
	// Workers honor ctx.Done() between files so cancel mid-verify takes
	// effect within ~1 file's worth of MD5 (worst case ~30s for biggest .pak).
	files := filterChangedFiles(ctx, installPath, cdn, cfg.BaseURL, idxFile.Resource, p.logger, onProgress)
	if ctx.Err() != nil {
		return core.UpdatePlan{}, ctx.Err()
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}

	// Plan.Version must be the TARGET (latest) version, not cfg.Version.
	// In patch mode cfg.Version is the FROM version (the patchConfig is keyed
	// by current install version), so writing cfg.Version back to
	// launcherDownloadConfig.json after apply would leave it at the
	// pre-update value → AvailableUpdate re-flags on next Refresh.
	targetVersion := idx.Default.Version
	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanUpdate,
		ManifestETag: idxETag,
		Version:      targetVersion,
		Files:        files,
		TotalBytes:   totalBytes,
	}
	p.logger.Info("CheckForUpdate complete",
		"game", gid,
		"local_version", localVersion,
		"target_version", targetVersion,
		"patch_from", cfg.Version,
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

// compile-time interface compliance (EDIT 3 — deviation: removed AssetServer)
var (
	_ core.Provider                = (*Provider)(nil)
	_ core.PathProvider            = (*Provider)(nil)
	_ core.ExeNamer                = (*Provider)(nil)
	_ core.Updater                 = (*Provider)(nil) // M3.A: implements update interface
	_ core.CheckForUpdateProgress  = (*Provider)(nil) // verify-local progress for BottomBar
	_ core.ProcessChecker          = (*Provider)(nil)
)
