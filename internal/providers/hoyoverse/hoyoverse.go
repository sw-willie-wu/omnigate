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
			})
		}
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

	gp := p.manifestCache.get(gid)
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

func (p *Provider) freeSpaceProbe() freeSpaceProbe {
	return defaultFreeSpaceProbe{}
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
	_ core.Provider      = (*Provider)(nil)
	_ core.PathProvider  = (*Provider)(nil)
	_ core.Updater       = (*Provider)(nil)
	_ core.ProcessChecker = (*Provider)(nil)
)
