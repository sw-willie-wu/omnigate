package hoyoverse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

type Settings struct {
	Path    string // launcher install root, e.g. C:\Program Files\HoYoPlay
	Region  string // "global" or "cn" — only "global" supported in M2
	TempDir string // override for temp/sidecar root (tests + settings.toml)
}

type Provider struct {
	api           *apiClient
	settings      Settings
	logger        *slog.Logger
	tempRootFn    func(core.GameID) string
	httpClient    *http.Client
	manifestCache *manifestCache
	apiBaseURL    string
	branchAPIBase string // default APIBase; getGameBranches ([DEV-3])
	sophonAPIBase string // default sophonChunkAPIBase; getBuild/getPatchBuild ([DEV-3])
	// hpatchzRun is a test seam for hpatchz invocation (T20-E). nil → hpatchz.Run.
	hpatchzRun func(ctx context.Context, oldFile, diffFile, newFile string) error
	// gameDirFn is a test seam to bypass DetectInstall. nil → use DetectInstall.
	gameDirFn func(core.GameID) (string, error)
}

// New returns a new HoYoverse Provider. logger may be nil; falls back to
// slog.Default().
func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		api:      newAPIClient(APIBase, &http.Client{Timeout: 30 * time.Second}),
		settings: settings,
		logger:   logger,
	}
	p.manifestCache = newManifestCache()
	p.branchAPIBase = APIBase
	p.sophonAPIBase = sophonChunkAPIBase
	return p
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
	// Read real local version from <gameDir>/config.ini so App.CheckForUpdate
	// (Topbar Refresh) can detect server > local and light the [更新] button.
	// Best-effort: if gameDir lookup or config.ini read fails, currentLocal
	// stays "" and fetchVersion falls back to Current=Latest (M2 behavior —
	// no update displayed). This preserves M2 wiring for non-Genshin titles
	// (HSR/ZZZ) until their per-game config.ini readers land.
	currentLocal := ""
	if gameDir, err := p.gameDir(gid); err == nil {
		if ver, err := ReadGameVersion(gameDir); err == nil {
			currentLocal = ver
		}
	}

	// Sophon-migrated games (Genshin 6.0+): the legacy /getGamePackages
	// endpoint reports a frozen old version (5.5.0 for Genshin global).
	// Use /getGameBranches.main.tag for the real latest so the UI displays
	// the correct number. The actual update flow is gated separately in
	// CheckForUpdate (returns sophon_not_supported until M3.B v2 lands).
	if g.UsesSophon {
		tag, err := p.fetchBranchTag(ctx, g.APIGameID)
		if err != nil {
			return core.VersionInfo{}, err
		}
		info := core.VersionInfo{Current: currentLocal, Latest: tag}
		if info.Current == "" {
			info.Current = info.Latest
		}
		return info, nil
	}

	return p.api.fetchVersion(ctx, g.APIGameID, currentLocal)
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

// IsGameRunning implements core.ProcessChecker.
func (p *Provider) IsGameRunning(gid core.GameID) (bool, error) {
	switch gid {
	case core.GameID("hoyoverse/genshin"):
		return platformIsProcessRunning("GenshinImpact.exe"), nil
	case core.GameID("hoyoverse/starrail"):
		return platformIsProcessRunning("StarRail.exe"), nil
	case core.GameID("hoyoverse/zzz"):
		return platformIsProcessRunning("ZenlessZoneZero.exe"), nil
	}
	return false, nil
}

// CheckForUpdate implements core.Updater.
func (p *Provider) CheckForUpdate(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	return p.checkForUpdate(ctx, gid, nil)
}

// CheckForUpdateWithProgress implements core.CheckForUpdateProgress: it surfaces
// the heavy local-file MD5 verification progress (Sophon patch flavor) so the UI
// renders "驗證本地檔案 X / Y" instead of a static, seemingly-frozen label.
func (p *Provider) CheckForUpdateWithProgress(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	return p.checkForUpdate(ctx, gid, onProgress)
}

// checkForUpdate is the shared CheckForUpdate body; onProgress (may be nil) is
// threaded into the Sophon patch-plan local-file verification.
func (p *Provider) checkForUpdate(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	// Sophon-migrated games (Genshin 6.0+): route to the Sophon decision tree
	// (§3). gameDir is resolved the v1 way; tempRoot via p.tempRoot(gid).
	if g := findByID(gid); g != nil && g.UsesSophon {
		gameDir, err := p.gameDir(gid)
		if err != nil {
			return core.UpdatePlan{}, err
		}
		return p.checkForUpdateSophon(ctx, gid, gameDir, p.tempRoot(gid), onProgress)
	}

	resp, err := p.fetchGetGamePackages(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	gameDir, err := p.gameDir(gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	tempRoot := p.tempRoot(gid)
	currentVer, _ := ReadGameVersion(gameDir)

	if healed, healErr := p.maybeSelfHeal(resp, gid, currentVer, tempRoot, gameDir); healed {
		gp := &genshinPlan{
			UpdatePlan: core.UpdatePlan{
				GameID:       gid,
				Kind:         core.PlanUpdate,
				Version:      resp.Data.GamePackages[0].Main.Major.Version,
				ManifestETag: resp.ManifestETag,
			},
			flavor: flavorNone,
		}
		p.manifestCache.put(gid, gp)
		return gp.UpdatePlan, nil
	} else if healErr != nil {
		p.logger.Warn("self-heal failed; falling through", "err", healErr)
	}

	gp, predlAvail, err := buildPlan(ctx, resp, gid, currentVer, tempRoot, gameDir, p.freeSpaceProbe())
	if err != nil {
		return core.UpdatePlan{}, err
	}
	gp.predlAvailable = predlAvail
	p.manifestCache.put(gid, gp)
	return gp.UpdatePlan, nil
}

// GetPredownloadAvailable returns whether a predownload is advertised by the
// last CheckForUpdate call for gid.
func (p *Provider) GetPredownloadAvailable(gid core.GameID) bool {
	gp := p.manifestCache.get(gid)
	if gp == nil {
		return false
	}
	return gp.predlAvailable
}

// LastApplyTarget is the App-exposed view of the persistent sidecar.
type LastApplyTarget struct {
	TargetVersion     string
	ConfigWritebackOK bool
}

// GetLastApplyTarget returns the persisted last_apply_target.json for gid,
// or nil if not found.
func (p *Provider) GetLastApplyTarget(gid core.GameID) *LastApplyTarget {
	tempRoot := p.tempRoot(gid)
	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
	internal, _ := loadJSONSidecar[lastApplyTarget](latPath)
	if internal == nil {
		return nil
	}
	return &LastApplyTarget{
		TargetVersion:     internal.TargetVersion,
		ConfigWritebackOK: internal.ConfigWritebackOK,
	}
}

// RunUpdate implements core.Updater.
func (p *Provider) RunUpdate(ctx context.Context, plan core.UpdatePlan, onEvent func(core.UpdateEvent)) error {
	gid := plan.GameID
	tempRoot := p.tempRoot(gid)
	gameDir, err := p.gameDir(gid)
	if err != nil {
		return err
	}
	versionDir := versionSidecarDir(tempRoot, gid, plan.Version)

	emit := func(stage string, current, total int) {
		if onEvent != nil {
			onEvent(core.UpdateEvent{
				Phase:   resolvePhase(stage),
				Current: int64(current),
				Total:   int64(total),
				Stage:   stage,
			})
		}
	}

	// Sophon dispatch: routes Sophon flavors to runUpdateSophon; v1 games fall
	// through unchanged. Check both cached flavor AND on-disk sidecar presence
	// so a resume after a restart (cold cache) also routes correctly.
	gp := p.manifestCache.get(gid)
	if (gp != nil && isSophonFlavor(gp.flavor)) || sophonSidecarsExist(versionDir) {
		return p.runUpdateSophon(ctx, plan, gameDir, tempRoot, versionDir, emit)
	}

	if walExists(versionDir) {
		wal, err := readApplyWAL(versionDir)
		if err != nil {
			return fmt.Errorf("read apply.wal: %w", err)
		}
		if wal != nil {
			ver := wal.Version
			etag := wal.ManifestETag
			if ver == "" {
				ver = plan.Version
			}
			if etag == "" {
				etag = plan.ManifestETag
			}
			return runApplyPlanPatch(ctx, tempRoot, gameDir, gid, ver, wal.WasPredl, etag, emit)
		}
	}
	if extractProgressExists(versionDir) {
		ep, err := readExtractProgress(versionDir)
		if err != nil {
			return fmt.Errorf("read extract_progress: %w", err)
		}
		if ep != nil {
			etag := ep.ManifestETag
			if etag == "" {
				etag = plan.ManifestETag
			}
			return runApplyPlanFull(ctx, tempRoot, gameDir, gid, plan.Version, etag, plan.Files, emit)
		}
	}

	if gp == nil {
		return fmt.Errorf("RunUpdate called without prior CheckForUpdate; manifestCache miss")
	}

	ps, err := newProgressStore(tempRoot, gid, plan.Version, plan.ManifestETag)
	if err != nil {
		return err
	}

	if err := downloadAll(ctx, ps, plan.Files, 4, func(bytes int64) {
		if onEvent != nil {
			onEvent(core.UpdateEvent{Phase: core.PhaseDownload, Current: bytes, Total: plan.TotalBytes})
		}
	}); err != nil {
		return err
	}

	if plan.Kind == core.PlanPredownload {
		snap := planSnapshot{
			SourceVersion:  gp.sourceVersion,
			TargetVersion:  plan.Version,
			Files:          plan.Files,
			AudioLanguages: gp.audioLanguages,
			ManifestETag:   plan.ManifestETag,
		}
		return ps.RenameToPredlReady(snap)
	}

	switch gp.flavor {
	case flavorPatch, flavorAudioOnly:
		stagingDir := filepath.Join(versionDir, "staging")
		_ = os.RemoveAll(stagingDir)
		for _, blob := range plan.Files {
			zipPath := filepath.Join(versionDir, blob.Path)
			if err := applyPatchZip(ctx, zipPath, gameDir, stagingDir, emit); err != nil {
				return err
			}
		}
		return runApplyPlanPatch(ctx, tempRoot, gameDir, gid, plan.Version, false, plan.ManifestETag, emit)
	case flavorFull:
		return runApplyPlanFull(ctx, tempRoot, gameDir, gid, plan.Version, plan.ManifestETag, plan.Files, emit)
	default:
		return fmt.Errorf("unsupported flavor: %v", gp.flavor)
	}
}

func resolvePhase(stage string) core.Phase {
	switch stage {
	case "applying", "applying_full":
		return core.PhaseApply
	}
	return core.PhaseDownload
}

func walExists(versionDir string) bool {
	_, err := os.Stat(filepath.Join(versionDir, "apply.wal"))
	return err == nil
}

func extractProgressExists(versionDir string) bool {
	_, err := os.Stat(filepath.Join(versionDir, "extract_progress.json"))
	return err == nil
}

// fetchGetGamePackages calls the HoYoverse getGamePackages API for the given
// game and returns the parsed response.
func (p *Provider) fetchGetGamePackages(ctx context.Context, gid core.GameID) (*HypGetGamePackagesResponse, error) {
	g := findByID(gid)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	apiID := g.APIGameID
	base := p.apiBaseURL
	if base == "" {
		base = APIBase
	}
	urlStr := fmt.Sprintf("%s/getGamePackages?launcher_id=%s&game_ids[]=%s", base, LauncherID, apiID)
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	hc := p.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getGamePackages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getGamePackages status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Parse outer apiEnvelope to extract the inner data field.
	var env struct {
		Retcode int             `json:"retcode"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("getGamePackages unmarshal envelope: %w", err)
	}
	parsed, err := parseGamePackagesResponse(env.Data)
	if err != nil {
		return nil, err
	}
	parsed.ManifestETag = resp.Header.Get("ETag")
	return parsed, nil
}

func (p *Provider) gameDir(gid core.GameID) (string, error) {
	if p.gameDirFn != nil {
		return p.gameDirFn(gid)
	}
	games, err := p.DetectInstall(context.Background())
	if err != nil {
		return "", err
	}
	for _, g := range games {
		if g.GameID == gid {
			return g.InstallPath, nil
		}
	}
	return "", fmt.Errorf("gameDir: %w (gid=%s)", core.ErrUnknownGame, gid)
}

func (p *Provider) tempRoot(gid core.GameID) string {
	if p.tempRootFn != nil {
		return p.tempRootFn(gid)
	}
	return filepath.Join(os.TempDir(), "omnigate", "hoyoverse")
}

// SetTempRootFn wires the app-provided temp directory resolver into this
// provider. Called by App.constructProviders immediately after New.
func (p *Provider) SetTempRootFn(fn func(core.GameID) string) {
	p.tempRootFn = fn
}

// SetAPIBaseURL overrides the default HoYoverse API base URL. Used by integration
// tests to point at httptest servers.
func (p *Provider) SetAPIBaseURL(url string) {
	p.apiBaseURL = url
}

// SetBranchAPIBaseURL overrides the getGameBranches base URL. Test seam
// ([DEV-3]); defaults to APIBase.
func (p *Provider) SetBranchAPIBaseURL(u string) { p.branchAPIBase = u }

// SetGameDirFn wires a test-only game-directory resolver so integration tests
// can point the provider at a temp gameDir without going through DetectInstall.
func (p *Provider) SetGameDirFn(fn func(core.GameID) (string, error)) {
	p.gameDirFn = fn
}

// SetHpatchzRun overrides the hpatchz invocation. Used by integration tests
// to avoid requiring a valid hpatchz diff blob in the fixture.
func (p *Provider) SetHpatchzRun(fn func(ctx context.Context, oldFile, diffFile, newFile string) error) {
	p.hpatchzRun = fn
}

// SetSophonAPIBaseURL overrides the getBuild/getPatchBuild base URL. Test seam
// ([DEV-3]); defaults to sophonChunkAPIBase.
func (p *Provider) SetSophonAPIBaseURL(u string) { p.sophonAPIBase = u }

func (p *Provider) freeSpaceProbe() freeSpaceProbe {
	return defaultFreeSpaceProbe{}
}

// maybeSelfHealSophon mirrors maybeSelfHeal (incl the clock-skew handling at
// hoyoverse.go:470-483) for Sophon games: if an apply completed but config.ini
// writeback failed, retry the writeback (24h budget). Returns true iff the
// writeback now succeeds. §3.5.
func (p *Provider) maybeSelfHealSophon(currentLocal, mainTag, gameDir, tempRoot string, gid core.GameID) bool {
	if currentLocal == mainTag {
		return false // already healed
	}
	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
	lat, err := loadJSONSidecar[lastApplyTarget](latPath)
	if err != nil || lat == nil {
		return false
	}
	if lat.TargetVersion != mainTag {
		return false
	}

	now := time.Now().UTC()
	if !lat.LastWritebackRetryTS.IsZero() {
		delta := now.Sub(lat.LastWritebackRetryTS)
		if delta >= 0 && delta < 24*time.Hour {
			return false
		}
		if delta < 0 && -delta <= 24*time.Hour {
			return false
		}
		if delta < 0 && -delta > 24*time.Hour {
			_ = os.Remove(latPath)
			return false
		}
	}

	writeErr := WriteGameVersion(gameDir, mainTag)
	lat.LastWritebackRetryTS = now
	lat.ConfigWritebackOK = (writeErr == nil)
	if persistErr := writeLastApplyTarget(tempRoot, gid, lat); persistErr != nil {
		p.logger.Warn("sophon self-heal: failed to update last_apply_target", "err", persistErr)
	}
	return writeErr == nil
}

// checkForUpdateSophon implements the §3 decision tree for Sophon games.
// Takes gameDir and tempRoot explicitly so unit tests can call it without
// going through DetectInstall (INTEGRATOR-NOTE T21-A).
func (p *Provider) checkForUpdateSophon(ctx context.Context, gid core.GameID, gameDir, tempRoot string, onProgress func(done, total int)) (core.UpdatePlan, error) {
	g := findByID(gid)
	branch, err := p.fetchBranchInfo(ctx, g.APIGameID)
	if err != nil {
		if ctx.Err() != nil {
			return core.UpdatePlan{}, ctx.Err() // user canceled — surface as ctx error so the app idles silently (no error toast)
		}
		p.logger.Warn("sophon CheckForUpdate: fetchBranchInfo (getGameBranches) failed", "apiGameID", g.APIGameID, "err", err)
		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}
	if branch.Main.IsEmpty() || len(branch.Main.Categories) == 0 {
		p.logger.Warn("sophon CheckForUpdate: main branch empty or no categories", "mainEmpty", branch.Main.IsEmpty(), "cats", len(branch.Main.Categories))
		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}

	currentLocal, _ := ReadGameVersion(gameDir)
	p.logger.Debug("sophon CheckForUpdate: branch+local resolved", "mainTag", branch.Main.Tag, "currentLocal", currentLocal, "diffTags", branch.Main.DiffTags, "predlEmpty", branch.PreDownload.IsEmpty())
	if currentLocal == "" {
		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_no_install", Retryable: false}
	}

	allowedTargets := []string{branch.Main.Tag}
	if !branch.PreDownload.IsEmpty() {
		allowedTargets = append(allowedTargets, branch.PreDownload.Tag)
	}

	// Self-heal (§3.5).
	if p.maybeSelfHealSophon(currentLocal, branch.Main.Tag, gameDir, tempRoot, gid) {
		cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
		gp := &genshinPlan{
			UpdatePlan: core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonUnspecified},
			flavor:     flavorNone,
		}
		p.manifestCache.put(gid, gp)
		return gp.UpdatePlan, nil
	}

	// Idle short-circuit.
	if currentLocal == branch.Main.Tag {
		cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
		gp := &genshinPlan{
			UpdatePlan: core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonUnspecified},
			flavor:     flavorNone,
		}
		p.manifestCache.put(gid, gp)
		return gp.UpdatePlan, nil
	}

	// Predl-consume short-circuit (§3.6).
	if consume, predl := detectPredlConsume(tempRoot, gid, currentLocal, branch.Main.Tag, branch.Main.DiffTags); consume {
		flavor := flavorSophonPatch
		if predl.Kind == "sophon_build" {
			flavor = flavorSophonBuild
		}
		snap := predl.PlanSnapshot
		gp := &genshinPlan{
			UpdatePlan:    core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonResumeInterrupted},
			flavor:        flavor,
			predlConsume:  true,
			predlSnapshot: &snap,
			sophonBranch:  branch,
			sophonBuildID: predl.BuildID,
			sourceVersion: currentLocal,
		}
		p.manifestCache.put(gid, gp)
		return gp.UpdatePlan, nil
	}

	// Normal plan build.
	audioFolders, _ := DetectInstalledLanguages(gameDir)
	audioLangs := mapFoldersToMatchingFields(audioFolders)
	gp, predlAvail, err := buildSophonPlan(ctx, p, branch, gid, currentLocal, audioLangs, gameDir, tempRoot, onProgress)
	if err != nil {
		if ctx.Err() != nil {
			return core.UpdatePlan{}, ctx.Err() // user canceled mid-plan — silent idle, not a fetch error
		}
		return core.UpdatePlan{}, err
	}
	gp.predlAvailable = predlAvail
	p.manifestCache.put(gid, gp)
	return gp.UpdatePlan, nil
}

// isSophonFlavor reports whether f is one of the Sophon plan flavors.
func isSophonFlavor(f planFlavor) bool {
	switch f {
	case flavorSophonPatch, flavorSophonBuild, flavorSophonFull, flavorSophonPredlPatch, flavorSophonPredlBuild:
		return true
	}
	return false
}

// sophonSidecarsExist reports whether versionDir contains any Sophon-specific
// resume markers (sophon_apply.wal or sophon_progress.json).
func sophonSidecarsExist(versionDir string) bool {
	if _, err := os.Stat(filepath.Join(versionDir, "sophon_apply.wal")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(versionDir, "sophon_progress.json")); err == nil {
		return true
	}
	return false
}

// runUpdateSophon dispatches the Sophon resume ladder (§6.9) + fresh runs.
func (p *Provider) runUpdateSophon(ctx context.Context, plan core.UpdatePlan, gameDir, tempRoot, versionDir string, emit func(stage string, current, total int)) error {
	gid := plan.GameID

	// §6.9 path 1: sophon_apply.wal present → apply-phase resume (offline-safe).
	if wal, _ := readSophonApplyWAL(versionDir); wal != nil && len(wal.Records) > 0 {
		gp := p.manifestCache.get(gid)
		if gp == nil {
			gp = &genshinPlan{
				UpdatePlan:    core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: plan.Version},
				flavor:        planFlavorFromString(wal.Flavor),
				sophonBuildID: wal.BuildID,
				sourceVersion: wal.SourceTag,
			}
		}
		return runSophonApply(ctx, p, gid, gp, tempRoot, gameDir, wal.StagingRoot, emit)
	}

	gp := p.manifestCache.get(gid)
	if gp == nil {
		// §6.9 path 3: sophon_progress.json without WAL → rebuild plan via CheckForUpdate.
		if _, err := os.Stat(filepath.Join(versionDir, "sophon_progress.json")); err == nil {
			if _, cerr := p.CheckForUpdate(ctx, gid); cerr != nil {
				return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
			}
			gp = p.manifestCache.get(gid)
		}
		if gp == nil {
			return fmt.Errorf("RunUpdate(sophon) without prior CheckForUpdate; manifestCache miss")
		}
	}

	// §7.3 predl handoff: hydrate from snapshot when consuming.
	branchKind := "main"
	stagingBuildID := gp.sophonBuildID
	if plan.Kind == core.PlanUpdate && gp.predlConsume && gp.predlSnapshot != nil {
		gp.sophonChunkSources = gp.predlSnapshot.SophonChunkSources
		gp.sophonPatches = gp.predlSnapshot.SophonPatches
		gp.sophonDeletes = gp.predlSnapshot.SophonDeletes
		gp.sophonCategories = gp.predlSnapshot.Categories

		// OVERRIDE 3: verify predl staging before reusing; discard if too eroded.
		predlStagingRoot := sophonStagingDir(tempRoot, gid, gp.Version, "predl", gp.sophonBuildID)
		if discard, _ := verifyPredlStaging(predlStagingRoot, gp.sophonChunkSources, gp.sophonPatches); discard {
			p.logger.Warn("sophon: predl staging too eroded; discarding for fresh download", "gid", gid)
			_ = os.Remove(filepath.Join(versionSidecarDir(tempRoot, gid, gp.Version), "predl_ready.json"))
			_ = os.RemoveAll(predlStagingRoot)
			gp.predlConsume = false // fall through to fresh staging/main download+apply below
		}
	}

	// Re-derive branchKind and stagingBuildID after possible predl discard.
	if gp.predlConsume {
		branchKind = "predl"
		stagingBuildID = gp.sophonBuildID
	} else {
		branchKind = "main"
		stagingBuildID = gp.sophonBuildID
	}

	// Predownload: stage to predl, write predl_ready, SKIP apply (§7.1).
	if plan.Kind == core.PlanPredownload {
		if gp.predlPlan == nil {
			return fmt.Errorf("RunUpdate(sophon predl) without predlPlan")
		}
		return p.runSophonPredownload(ctx, gid, gp, tempRoot, versionDir, gameDir, emit)
	}

	stagingRoot := sophonStagingDir(tempRoot, gid, plan.Version, branchKind, stagingBuildID)
	store, err := newSophonProgressStore(tempRoot, gid, plan.Version, branchKind, stagingBuildID)
	if err != nil {
		return err
	}
	exec := defaultSophonExecutors(p.httpClientOrDefault())
	if err := downloadAllSophon(ctx, store, gameDir, stagingRoot, gp.sophonChunkSources, gp.sophonPatches, 4, exec, func(b int64) {
		emit("download", int(b), int(gp.TotalBytes))
	}); err != nil {
		return err
	}
	return runSophonApply(ctx, p, gid, gp, tempRoot, gameDir, stagingRoot, emit)
}

// runSophonPredownload stages predl content and writes predl_ready.json
// WITHOUT applying (§7.1).
func (p *Provider) runSophonPredownload(ctx context.Context, gid core.GameID, gp *genshinPlan, tempRoot, versionDir, gameDir string, emit func(stage string, current, total int)) error {
	pp := gp.predlPlan
	stagingRoot := sophonStagingDir(tempRoot, gid, pp.TargetVersion, "predl", pp.BuildID)
	store, err := newSophonProgressStore(tempRoot, gid, pp.TargetVersion, "predl", pp.BuildID)
	if err != nil {
		return err
	}
	exec := defaultSophonExecutors(p.httpClientOrDefault())
	if err := downloadAllSophon(ctx, store, gameDir, stagingRoot, pp.ChunkSources, pp.Patches, 4, exec, func(b int64) {
		emit("download", int(b), int(gp.TotalBytes))
	}); err != nil {
		return err
	}
	kind := "sophon_patch"
	if pp.Flavor == flavorSophonPredlBuild {
		kind = "sophon_build"
	}
	ready := &sophonPredlReadyFile{
		Kind:           kind,
		BuildID:        pp.BuildID,
		SourceVersion:  pp.SourceVersion,
		TargetVersion:  pp.TargetVersion,
		AudioLanguages: pp.AudioLanguages,
		StagedAt:       time.Now().UTC().Format(time.RFC3339),
		PlanSnapshot: sophonPlanSnapshot{
			SophonChunkSources: pp.ChunkSources,
			SophonPatches:      pp.Patches,
			SophonDeletes:      pp.Deletes,
			Categories:         pp.Categories,
		},
	}
	dir := versionSidecarDir(tempRoot, gid, pp.TargetVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ready, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "predl_ready.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// predl_ready.json is now the authoritative resume marker; drop the
	// download-phase progress sidecar so ScanRecovery classifies this dir as
	// PredlAwaiting (not DownloadResume) on the next launch. (Mirrors v1
	// RenameToPredlReady dropping progress.json.)
	_ = os.Remove(filepath.Join(dir, "sophon_progress.json"))
	return nil
}

func (p *Provider) maybeSelfHeal(
	resp *HypGetGamePackagesResponse,
	gid core.GameID,
	currentVer string,
	tempRoot string,
	gameDir string,
) (bool, error) {
	if len(resp.Data.GamePackages) == 0 {
		return false, nil
	}
	mainMajor := resp.Data.GamePackages[0].Main.Major
	if currentVer == mainMajor.Version {
		return false, nil
	}
	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
	lat, err := loadJSONSidecar[lastApplyTarget](latPath)
	if err != nil {
		return false, err
	}
	if lat == nil || lat.TargetVersion != mainMajor.Version {
		return false, nil
	}

	now := time.Now().UTC()
	if !lat.LastWritebackRetryTS.IsZero() {
		delta := now.Sub(lat.LastWritebackRetryTS)
		if delta >= 0 && delta < 24*time.Hour {
			return false, nil
		}
		if delta < 0 && -delta <= 24*time.Hour {
			return false, nil
		}
		if delta < 0 && -delta > 24*time.Hour {
			_ = os.Remove(latPath)
			return false, nil
		}
	}

	writeErr := WriteGameVersion(gameDir, mainMajor.Version)
	lat.LastWritebackRetryTS = now
	if writeErr == nil {
		lat.ConfigWritebackOK = true
	} else {
		lat.ConfigWritebackOK = false
	}
	if persistErr := writeLastApplyTarget(tempRoot, gid, lat); persistErr != nil {
		p.logger.Warn("self-heal: failed to update last_apply_target", "err", persistErr)
	}
	if writeErr == nil {
		return true, nil
	}
	return false, nil
}

type defaultFreeSpaceProbe struct{}

func (defaultFreeSpaceProbe) FreeBytes(path string) (uint64, error) {
	return windowsFreeBytes(path)
}

// compile-time check
var (
	_ core.Provider       = (*Provider)(nil)
	_ core.PathProvider   = (*Provider)(nil)
	_ core.Updater        = (*Provider)(nil)
	_ core.ProcessChecker = (*Provider)(nil)
)
