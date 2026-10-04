package kurogames

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

type Settings struct {
	TempDir string // test-only temp-root fallback; production always injects via SetTempRootFn
}

type Provider struct {
	settings       Settings
	logger         *slog.Logger
	httpClient     *http.Client // manifest/version/gacha/news JSON fetches (overall timeout OK)
	downloadClient *http.Client // game-file downloads; NO overall timeout — stall watchdog governs
	clock          RetryClock   // for download retry backoff (test-only injection)
	resolvedPaths  map[core.GameID]string
	recordAPIBase  string
	convLogPathsFn func(installDir string) []string
	recordDelay    time.Duration
	tempRootFn     func(core.GameID) string
	kv             KV // config store (bundle catalog / active bundle / launch opts); memKV until SetKV

	krsdkCachePathFn     func() (string, error)         // locate KRSDKUserCache.json (injectable)
	localStorageDBPathFn func(installDir string) string // locate LocalStorage.db (injectable)
	procRunningFn        func(names []string) bool      // process gate (injectable)
}

func New(settings Settings, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		settings: settings,
		logger:   logger,
		// httpClient serves only small JSON (index/indexFile/gacha/news) where
		// an overall timeout is the right semantic. Game-file downloads must
		// NOT go through it: Client.Timeout caps the whole body read, which
		// made any >Timeout transfer fail deterministically (24 GiB pak vs
		// the old 5-minute cap).
		httpClient: &http.Client{Timeout: 60 * time.Second},
		downloadClient: &http.Client{
			// No overall timeout — the downloader's stall watchdog aborts
			// dead streams; connection setup is bounded by the Transport.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		clock: realRetryClock{},
		kv:    newMemKV(),
	}
	p.recordAPIBase = "https://gmserver-api.aki-game2.net"
	p.convLogPathsFn = defaultConvLogPaths
	p.recordDelay = 400 * time.Millisecond
	p.krsdkCachePathFn = defaultKRSDKCachePath
	p.localStorageDBPathFn = defaultLocalStorageDBPath
	p.procRunningFn = anyProcessRunning
	return p
}

// tempRoot resolves the temp/sidecar root. Production injects tempRootFn via
// SetTempRootFn (App.tempDirFor); the settings.TempDir and os-default rungs are
// test-only fallbacks for when SetTempRootFn was not called.
func (p *Provider) tempRoot(gid core.GameID) string {
	if p.tempRootFn != nil {
		return p.tempRootFn(gid)
	}
	if p.settings.TempDir != "" {
		return p.settings.TempDir
	}
	return filepath.Join(os.TempDir(), "omnigate")
}

// SetTempRootFn wires the app-provided temp resolver. Called by App.constructProviders.
func (p *Provider) SetTempRootFn(fn func(core.GameID) string) { p.tempRootFn = fn }

// dlClient returns the download client, falling back to httpClient so tests
// that only override httpClient (pre-existing pattern) still work.
func (p *Provider) dlClient() *http.Client {
	if p.downloadClient != nil {
		return p.downloadClient
	}
	return p.httpClient
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
	return []core.SettingField{}
}

func (p *Provider) DetectInstall(_ context.Context) ([]core.InstalledGame, error) {
	out := []core.InstalledGame{}
	for gid, dir := range p.resolvedPaths {
		if dir == "" {
			continue
		}
		if st, statErr := os.Stat(dir); statErr == nil && st.IsDir() {
			out = append(out, core.InstalledGame{GameID: gid, InstallPath: dir})
		}
	}
	return out, nil
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
	installPath, err := p.gameDir(ctx, gid)
	if err != nil {
		return core.VersionInfo{}, err
	}
	vi, err := fetchVersion(ctx, installPath, gid)
	if err != nil || vi.Current == "" {
		return vi, err
	}
	// Best-effort: fetch the v3 game index so vi.Latest reflects what the
	// server ships (common pack version) and refresh the bundle catalog.
	// Network blip → local-only (fetchVersion already set Latest = Current).
	// 10s budget keeps Refresh responsive. Predownload is not offered for
	// the v3 protocol (spec N1), so vi.Predownload stays nil.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if idx, ferr := fetchGameIndexV3(fetchCtx, p.httpClient); ferr == nil {
		vi.Latest = idx.ResourcePacks["common"].Version
		p.saveCatalog(gid, catalogFromIndex(idx, time.Now()))
	}
	return vi, nil
}

func (p *Provider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	installPath, err := p.gameDir(ctx, gid)
	if err != nil {
		return 0, err
	}
	opts.ExtraArgs = p.launchArgsFor(ctx, gid, installPath)
	return Launch(ctx, installPath, gid, opts)
}

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
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	installPath, err := p.gameDir(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	st, err := readInstallState(filepath.Join(installPath, installStateFile))
	if err != nil {
		return core.UpdatePlan{}, err
	}
	known := st.installedKnown()
	if len(known) == 0 {
		return core.UpdatePlan{}, &core.UpdateError{Code: "install_record_missing", Retryable: false}
	}
	idx, err := fetchGameIndexV3(ctx, p.httpClient)
	if err != nil {
		p.logger.Warn("kurogames CheckForUpdate: fetch v3 index failed", "game", gid, "err", err)
		return core.UpdatePlan{}, err
	}
	// v3: common + every installed (usable, known) bundle's pack, each
	// patched from its own local version (spec §5).
	packs := []string{"common"}
	for _, n := range known {
		packs = append(packs, packOf(n))
	}
	plan, err := p.buildPlanV3(ctx, gid, installPath, idx, packs, st.packVersion, onProgress)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	p.logger.Info("CheckForUpdate complete", "game", gid, "local_version", st.packVersion("common"), "target_version", plan.Version, "packs", packs, "files_to_update", len(plan.Files), "patch_groups", len(plan.PatchGroups), "bytes", plan.TotalBytes)
	return plan, nil
}

// SupportsPredownload implements core.PredownloadChecker. Predownload is
// disabled for the v3 resource-pack protocol (spec N1): the v3 index's
// predownload shape is not modelled, so never offer it.
func (p *Provider) SupportsPredownload(gid core.GameID) bool {
	return false
}

// CheckForPredownload implements core.PredownloadChecker; always
// ErrPredownloadUnsupported (see SupportsPredownload). RunUpdate still
// understands PlanPredownload plans (staged-bytes adoption) for a
// pre-existing predl_ready.json left by an older build.
func (p *Provider) CheckForPredownload(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	return core.UpdatePlan{}, core.ErrPredownloadUnsupported
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
	installPath, err := p.gameDir(ctx, plan.GameID)
	if err != nil {
		return err
	}

	// 2nd game-running guard (spec §2.7)
	if isProcessRunning(g.ExeName) {
		return &core.UpdateError{
			Code:      "process_blocked",
			Retryable: true,
			Params:    map[string]string{"kind": "process_running", "game": string(plan.GameID)},
		}
	}

	// Re-verify at entry (spec §2.1): recompute planToken over the plan's
	// pack set against the live v3 index. A fetch/validate failure skips the
	// check (best-effort, same as the v2 ETag re-check).
	if idx, err := fetchGameIndexV3(ctx, p.httpClient); err == nil {
		if tok := planToken(idx, p.planTargets(plan, installPath)); tok != plan.ManifestETag {
			return &core.UpdateError{Code: "manifest_changed", Retryable: true, Params: map[string]string{"old_etag": plan.ManifestETag, "new_etag": tok}}
		}
	}

	// Determine TempDir — root only; newProgressStore.dir() appends gameID/version.
	tempDir := p.tempRoot(plan.GameID)

	progress := newProgressStore(tempDir, string(plan.GameID), plan.Version)

	// Staged-bytes adoption (spec §2.5, R3-B1) — MUST run BEFORE progress.Init.
	// A successfully-staged predl leaves ONLY predl_ready.json in the version
	// dir (RenameToPredlReady moved progress.json away); this adopting run's
	// Init(newETag) therefore finds no progress.json and — were it called
	// first — would take Init's "no match" branch, which calls
	// removeStaleParts() and wipes EVERY *.part under the version dir before
	// Consume ever runs. removeStaleParts is a blunt, version-dir-wide sweep
	// (not scoped to the files Consume is about to restore), so that delete
	// is real and NOT self-healed by Consume's restore running afterward —
	// unlike progress.json's *entries*, which Consume's later write would
	// simply overwrite either way, the deleted chunked-resume .part bytes on
	// disk are gone for good. Running Consume first means progress.json
	// already exists (stamped with plan.ManifestETag) by the time Init runs,
	// so Init takes the "same ETag → preserve" branch and never touches
	// removeStaleParts at all. Consume-before-Init is therefore load-bearing
	// for on-disk .part survival, not (only) a ledger-content concern — do
	// not reorder. Pinned by TestRunUpdate_ConsumeBeforeInit_PreservesParts.
	wantHash := map[string]string{}
	ephemeral := map[string]bool{}
	for _, f := range plan.Files {
		wantHash[f.Path] = f.Hash
		ephemeral[f.Path] = f.Ephemeral
	}
	adopted, stagedEphemeralBytes, consumeErr := progress.ConsumePredlStaged(plan.ManifestETag, wantHash, ephemeral)
	if adopted {
		// stagedEphemeralBytes is informational only. It must NOT be used for
		// disk-space math here: the App-layer preflight (preflightChecks /
		// measureStagedBytes) already measured staged bytes on disk and ran
		// its own disk-need calculation BEFORE RunUpdate was ever called.
		// Subtracting it again here would double-deduct the same bytes.
		p.logger.Info("predl staged bytes adopted", "game", plan.GameID, "staged_ephemeral_bytes", stagedEphemeralBytes)
	} else if consumeErr != nil {
		// Non-fatal: no staged predl to adopt is the common case (nil err,
		// adopted=false) and must not abort the run. A non-nil err here means
		// adoption itself failed (e.g. write error) — log and fall through to
		// a normal full download rather than failing the whole update.
		p.logger.Warn("predl staged adoption failed; full re-download", "game", plan.GameID, "err", consumeErr)
	}

	if err := progress.Init(plan.ManifestETag); err != nil {
		return &core.UpdateError{Code: "internal", Params: map[string]string{"reason": err.Error()}}
	}

	// Download phase
	d := &downloader{
		client:       p.dlClient(),
		logger:       p.logger,
		tempRoot:     tempDir,
		progress:     progress,
		plan:         &plan,
		onEvent:      onEvent,
		clock:        p.clock,
		stallTimeout: defaultStallTimeout,
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
		logger:      p.logger,
		tempRoot:    tempDir,
		gameDir:     installPath,
		progress:    progress,
		plan:        &plan,
		wasPredl:    false,
		onEvent:     onEvent,
		lock:        newApplyLock(),
		exeName:     g.ExeName,
		procRunning: isProcessRunning,
	}
	return a.runApply(ctx)
}

// isProcessRunning checks if the given exe name appears in the process list.
// Uses Windows toolhelp snapshot (kurogames is Windows-only). Stub for
// non-Windows builds always returns false.
//
// Package-level VAR (not a plain func) so tests can stub process detection
// end-to-end through the real RunUpdate path — this is what makes the
// exeName/procRunning injection into RunUpdate's applier literal (spec §4-2)
// independently regression-tested: a test that only constructs *applier
// directly can pin the guard's behavior, but cannot catch a dropped
// injection at the RunUpdate call site (2026-08 review IMPORTANT-1 —
// see TestRunUpdate_ProcessGuardBlocksDuringPatchPhase). Reassigning this
// var affects both the RunUpdate entry guard (spec §2.7) and the
// applier's patch-phase re-guard (spec §4-2), since both read it at call
// time via a func value, not a fixed reference.
var isProcessRunning = func(exeName string) bool {
	return platformIsProcessRunning(exeName)
}

// gameDir returns the App-injected resolved install folder for gid, or
// ErrGameNotInstalled when the game is unresolved.
func (p *Provider) gameDir(_ context.Context, gid core.GameID) (string, error) {
	if dir, ok := p.resolvedPaths[gid]; ok && dir != "" {
		return dir, nil
	}
	return "", fmt.Errorf("%w: %s", core.ErrGameNotInstalled, gid)
}

// SetResolvedPaths injects the App-resolved per-game install folders. The
// provider's DetectInstall + per-game operations then use these instead of
// scanning a single root.
func (p *Provider) SetResolvedPaths(paths map[core.GameID]string) {
	p.resolvedPaths = paths
}

// compile-time interface compliance (EDIT 3 — deviation: removed AssetServer)
var (
	_ core.Provider               = (*Provider)(nil)
	_ core.ExeNamer               = (*Provider)(nil)
	_ core.Updater                = (*Provider)(nil) // M3.A: implements update interface
	_ core.CheckForUpdateProgress = (*Provider)(nil) // verify-local progress for BottomBar
	_ core.ProcessChecker         = (*Provider)(nil)
	_ core.PredownloadChecker     = (*Provider)(nil) // predl: disabled for v3 (always unsupported)
)
