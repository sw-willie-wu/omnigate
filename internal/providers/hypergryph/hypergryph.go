package hypergryph

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

type Settings struct {
	Path    string
	TempDir string // optional override; empty → app layer's hypergryph temp default
}

type Provider struct {
	settings Settings
	logger   *slog.Logger
	client   *http.Client
	clock    RetryClock
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		settings: settings,
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Minute}, // download-grade; get_latest also fine
		clock:    realRetryClock{},
	}
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

// DefaultScan returns each known game found under DefaultRoot (layer 3 of
// per-game install-path resolution). Keyed by game ID; empty (non-nil) when
// nothing is installed.
func (p *Provider) DefaultScan(ctx context.Context) (map[core.GameID]string, error) {
	games, err := DetectInstall(ctx, DefaultRoot)
	if err != nil {
		return nil, err
	}
	out := make(map[core.GameID]string, len(games))
	for _, g := range games {
		out[g.GameID] = g.InstallPath
	}
	return out, nil
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
			return fetchVersion(ctx, p.client, ig.InstallPath, gid)
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

// IsGameRunning implements core.ProcessChecker (1st-point game-running guard).
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
	exe, ok := p.ExeName(gid)
	if !ok {
		return false, nil
	}
	return platformIsProcessRunning(exe), nil
}

// CheckForUpdate is a thin wrapper passing a nil verify-progress callback.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	return p.CheckForUpdateWithProgress(ctx, gid, nil)
}

// CheckForUpdateWithProgress resolves the install path then runs the Option-A
// flow. onProgress fires during the local per-file MD5 verify.
func (p *Provider) CheckForUpdateWithProgress(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	installPath, err := p.installPathFor(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	return p.checkForUpdateAt(ctx, gid, installPath, onProgress)
}

// checkForUpdateAt is the core CheckForUpdate logic against a known install path
// (extracted for testability). Spec §3.1 decision tree.
func (p *Provider) checkForUpdateAt(ctx context.Context, gid core.GameID, installPath string, onProgress func(done, total int)) (core.UpdatePlan, error) {
	// 1. Local version (config.ini AES; "" if unreadable — degrade §6).
	curVer, _ := readLocalVersion(installPath)

	// 2. get_latest.
	rsp, err := fetchGetLatest(ctx, p.client, curVer)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// 3. Staleness — version compare authoritative; action==1 only on degrade.
	switch {
	case curVer != "" && curVer == rsp.Version:
		// up-to-date → empty plan.
		return core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: rsp.Version, ManifestETag: rsp.Version, Reason: core.ReasonVersionChanged}, nil
	case curVer == "" && rsp.Action != 1:
		// degrade + no update signal → up-to-date.
		return core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: rsp.Version, ManifestETag: rsp.Version, Reason: core.ReasonVersionChanged}, nil
	}

	// 4. Fetch + parse game_files manifest from the per-file CDN.
	if rsp.Pkg.FilePath == "" {
		return core.UpdatePlan{}, &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"reason": "no pkg.file_path"}}
	}
	nodes, err := fetchGameFilesManifest(ctx, p.client, rsp.Pkg.FilePath)
	if err != nil {
		return core.UpdatePlan{}, err
	}

	// 5. Filter to changed files (heavy local MD5 verify; onProgress per file).
	files := filterChangedFiles(ctx, installPath, rsp.Pkg.FilePath, nodes, p.logger, onProgress)
	if ctx.Err() != nil {
		return core.UpdatePlan{}, ctx.Err()
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}

	plan := core.UpdatePlan{
		GameID:       gid,
		Kind:         core.PlanUpdate,
		ManifestETag: rsp.Version,
		Version:      rsp.Version,
		Files:        files,
		TotalBytes:   totalBytes,
		Reason:       core.ReasonVersionChanged,
	}
	p.logger.Info("hypergryph CheckForUpdate complete", "game", gid, "local_version", curVer, "target_version", rsp.Version, "files_to_update", len(files), "bytes", totalBytes)
	return plan, nil
}

// RunUpdate executes a previously-checked plan: re-verify version → preflight →
// process guard → download → apply. Spec §3.2.
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("RunUpdate panic", "game", plan.GameID, "panic", r)
			err = &core.UpdateError{Code: "internal", Retryable: true, Params: map[string]string{"detail": fmt.Sprint(r)}}
		}
	}()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	g := findByID(plan.GameID)
	if g == nil {
		return fmt.Errorf("%w: %s", core.ErrUnknownGame, plan.GameID)
	}
	installPath, err := p.installPathFor(ctx, plan.GameID)
	if err != nil {
		return err
	}

	// Re-verify version (manifest_changed).
	if cur, ferr := fetchGetLatest(ctx, p.client, ""); ferr == nil && cur.Version != "" && cur.Version != plan.ManifestETag {
		return &core.UpdateError{Code: "manifest_changed", Retryable: true, Params: map[string]string{"old": plan.ManifestETag, "new": cur.Version}}
	}

	// Resolve temp root (matches app.tempDirFor hypergryph default).
	tempDir := p.settings.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "omnigate", "hypergryph")
	}

	// Preflight: same-volume + disk space.
	if perr := preflightSameVolume(tempDir, installPath); perr != nil {
		return perr
	}
	if perr := checkDiskSpace(tempDir, plan.TotalBytes); perr != nil {
		return perr
	}

	// Process guard.
	if platformIsProcessRunning(g.ExeName) {
		return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "process_running", "game": string(plan.GameID)}}
	}

	progress := newProgressStore(tempDir, string(plan.GameID), plan.Version)
	if err := progress.Init(plan.ManifestETag); err != nil {
		return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
	}

	d := &downloader{client: p.client, logger: p.logger, tempRoot: tempDir, progress: progress, plan: &plan, onEvent: onEvent, clock: p.clock}
	if err := d.runDownload(ctx); err != nil {
		return err
	}

	a := &applier{logger: p.logger, tempRoot: tempDir, gameDir: installPath, progress: progress, plan: &plan, wasPredl: false, onEvent: onEvent, lock: newApplyLock()}
	return a.runApply(ctx)
}

// installPathFor resolves the install path for gid via DetectInstall.
func (p *Provider) installPathFor(ctx context.Context, gid core.GameID) (string, error) {
	if findByID(gid) == nil {
		return "", fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	installs, err := DetectInstall(ctx, p.settings.Path)
	if err != nil {
		return "", err
	}
	for _, ig := range installs {
		if ig.GameID == gid {
			return ig.InstallPath, nil
		}
	}
	return "", fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

var (
	_ core.Provider               = (*Provider)(nil)
	_ core.PathProvider           = (*Provider)(nil)
	_ core.ExeNamer               = (*Provider)(nil)
	_ core.Updater                = (*Provider)(nil)
	_ core.CheckForUpdateProgress = (*Provider)(nil)
	_ core.ProcessChecker         = (*Provider)(nil)
)
