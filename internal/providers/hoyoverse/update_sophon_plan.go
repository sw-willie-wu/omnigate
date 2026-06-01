package hoyoverse

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// mapFoldersToMatchingFields translates DetectInstalledLanguages folder names
// ("Chinese", "English(US)", ...) into manifest MatchingField codes
// ("zh-cn", "en-us", ...) using the package-level folderToAudioLang map
// (defined in update_manifest.go). Unknown folders are dropped. Result is
// sorted and always non-nil.
func mapFoldersToMatchingFields(folders []string) []string {
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		if code, ok := folderToAudioLang[f]; ok {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// fetchSophonBuild calls getBuild (isPatch=false) or getPatchBuild
// (isPatch=true) on p.sophonAPIBase for a single branch slot at the given
// tag, returning the parsed envelope. Query per spec §2.1:
//
//	plat_app=&branch=&password=&package_id=&tag=
func fetchSophonBuild(ctx context.Context, p *Provider, slot sophon.BranchSlot, platApp, tag string, isPatch bool) (*sophon.BuildResponse, error) {
	base := p.sophonAPIBase
	if base == "" {
		base = sophonChunkAPIBase
	}
	// getBuild accepts GET; getPatchBuild requires POST (live API returns 405
	// "Allow: OPTIONS, POST" for GET). Params stay in the query string for both;
	// the POST body is empty. Verified against sg-public-api 2026-06-01.
	endpoint := "getBuild"
	method := http.MethodGet
	if isPatch {
		endpoint = "getPatchBuild"
		method = http.MethodPost
	}
	q := url.Values{}
	q.Set("plat_app", platApp)
	q.Set("branch", slot.Branch)
	q.Set("password", slot.Password)
	q.Set("package_id", slot.PackageID)
	q.Set("tag", tag)
	urlStr := base + "/" + endpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
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
		return nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s status %d", endpoint, resp.StatusCode)
	}
	var env struct {
		Retcode int             `json:"retcode"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("%s unmarshal envelope: %w", endpoint, err)
	}
	if env.Retcode != 0 {
		return nil, fmt.Errorf("%s retcode=%d msg=%q", endpoint, env.Retcode, env.Message)
	}
	if isPatch {
		return sophon.ParsePatchResponse(env.Data)
	}
	return sophon.ParseBuildResponse(env.Data)
}

// planCategories returns the ordered category matching-field set for a plan:
// "game" first, then the installed audio langs (already sorted by
// mapFoldersToMatchingFields) that are also present in branch.Categories.
func planCategories(slot sophon.BranchSlot, audioLangs []string) []sophon.Category {
	byField := make(map[string]sophon.Category, len(slot.Categories))
	for _, c := range slot.Categories {
		byField[c.MatchingField] = c
	}
	out := make([]sophon.Category, 0, 1+len(audioLangs))
	if c, ok := byField["game"]; ok {
		out = append(out, c)
	}
	for _, lang := range audioLangs {
		if c, ok := byField[lang]; ok {
			out = append(out, c)
		}
	}
	return out
}

// httpClientOrDefault returns p.httpClient or a default 30s-timeout client.
func (p *Provider) httpClientOrDefault() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// buildSophonBuildPlan implements §3.2: fetch getBuild ONCE (§E.2 P9), dedup vs
// an old manifest when present, emit ChunkSources (Local for dedup hits, CDN
// for misses). oldManifests may be nil (flavorSophonFull). It APPENDS into gp.
func buildSophonBuildPlan(
	ctx context.Context,
	p *Provider,
	gp *genshinPlan,
	slot sophon.BranchSlot,
	platApp string,
	cats []sophon.Category,
	oldManifests *appliedSet,
	currentLocal string,
) error {
	// §E.2 P9: getBuild returns ALL categories in one envelope — fetch ONCE.
	build, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, false)
	if err != nil {
		p.logger.Warn("sophon plan: getBuild failed (build flavor)", "tag", slot.Tag, "err", err)
		return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}
	gp.sophonBuildID = build.BuildID
	for _, cat := range cats {
		id, ok := build.ManifestFor(cat.MatchingField)
		if !ok {
			p.logger.Warn("sophon plan: getBuild manifest missing for category", "category", cat.MatchingField)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		// §E.2 P2: capture the raw .pb.zst wire bytes for the dedup cache.
		newManifest, raw, err := sophon.FetchManifestRaw(ctx, p.httpClientOrDefault(), *id)
		if err != nil {
			p.logger.Warn("sophon plan: FetchManifestRaw failed (build)", "category", cat.MatchingField, "url", id.ManifestDownload.URLPrefix+"/"+id.Manifest.ID, "err", err)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		gp.sophonRawManifests[cat.MatchingField] = raw
		var oldManifest *pb.SophonManifestProto
		if oldManifests != nil {
			oldManifest = oldManifests.MatchByVersion(currentLocal, cat.MatchingField)
		}
		useCompress := bool(id.ChunkDownload.Compression)
		chunkPrefix := id.ChunkDownload.URLPrefix
		for _, asset := range newManifest.Assets {
			if asset.AssetType != 0 {
				continue
			}
			oldIdx := sophon.BuildPerAssetMD5Index(oldManifest, asset.AssetName)
			srcs := sophon.BuildChunkSources(asset, oldIdx, chunkPrefix, useCompress)
			gp.sophonChunkSources = append(gp.sophonChunkSources, srcs...)
			gp.sophonAssetMD5[asset.AssetName] = asset.AssetHashMd5 // §E.2 P1
		}
	}
	return nil
}

// buildSophonPatchPlan implements §3.1 (Collapse-faithful patch+main merge).
// It APPENDS Patch/CopyOver records into gp.sophonPatches, main-fall-through
// chunk_assemble into gp.sophonChunkSources, deletes into gp.sophonDeletes,
// and the per-asset main chunk plan into gp.sophonPatchAssetsFromMain for
// every patch record ([DEV-5] / §6.4 demotion).
// Fetches getPatchBuild + getBuild ONCE per branch (§E.2 P9).
func buildSophonPatchPlan(
	ctx context.Context,
	p *Provider,
	gp *genshinPlan,
	slot sophon.BranchSlot,
	platApp string,
	cats []sophon.Category,
	currentLocal string,
	oldMainManifest *pb.SophonManifestProto,
	gameDir string,
) error {
	// §E.2 P9: fetch getPatchBuild + getBuild ONCE per branch.
	patchResp, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, true)
	if err != nil {
		p.logger.Warn("sophon plan: getPatchBuild failed", "tag", slot.Tag, "err", err)
		return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}
	buildResp, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, false)
	if err != nil {
		p.logger.Warn("sophon plan: getBuild failed (patch flavor)", "tag", slot.Tag, "err", err)
		return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}
	gp.sophonBuildID = buildResp.BuildID
	for _, cat := range cats {
		patchID, ok := patchResp.ManifestFor(cat.MatchingField)
		if !ok {
			p.logger.Warn("sophon plan: getPatchBuild manifest missing for category", "category", cat.MatchingField)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		buildID, ok := buildResp.ManifestFor(cat.MatchingField)
		if !ok {
			p.logger.Warn("sophon plan: getBuild manifest missing for category (patch)", "category", cat.MatchingField)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		patchProto, err := sophon.FetchPatchManifest(ctx, p.httpClientOrDefault(), *patchID)
		if err != nil {
			p.logger.Warn("sophon plan: FetchPatchManifest failed", "category", cat.MatchingField, "url", patchID.ManifestDownload.URLPrefix+"/"+patchID.Manifest.ID, "err", err)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		// §E.2 P2: capture the main manifest raw .pb.zst for the dedup cache.
		mainProto, raw, err := sophon.FetchManifestRaw(ctx, p.httpClientOrDefault(), *buildID)
		if err != nil {
			p.logger.Warn("sophon plan: FetchManifestRaw failed (patch)", "category", cat.MatchingField, "url", buildID.ManifestDownload.URLPrefix+"/"+buildID.Manifest.ID, "err", err)
			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
		}
		gp.sophonRawManifests[cat.MatchingField] = raw

		patches, deletes := sophon.BuildPatchInstructions(patchProto, mainProto, currentLocal, patchID.DiffDownload.URLPrefix)
		gp.sophonPatches = append(gp.sophonPatches, patches...)
		gp.sophonDeletes = append(gp.sophonDeletes, deletes...)

		patched := make(map[string]bool, len(patches))
		for _, pi := range patches {
			patched[pi.Asset] = true
		}

		useCompress := bool(buildID.ChunkDownload.Compression)
		chunkPrefix := buildID.ChunkDownload.URLPrefix

		// Split main assets: patched ones get the fast demotion plan (no disk I/O);
		// unpatched ones need an on-disk MD5 skip-guard. The MD5 pass is run in
		// parallel — single-threaded it took ~2.5min hashing ~1100 unchanged files
		// on a real Genshin 6.5→6.6 patch (smoke 2026-06-01), mirroring v1's
		// filterChangedFiles worker pool.
		var fallthroughAssets []*pb.SophonManifestAssetProperty
		for _, ma := range mainProto.Assets {
			if ma.AssetType != 0 {
				continue
			}
			if patched[ma.AssetName] {
				// §6.4 demotion fallback plan; sophonAssetMD5 needed if demoted to chunk_assemble (§E.2 P1).
				oldIdx := sophon.BuildPerAssetMD5Index(oldMainManifest, ma.AssetName)
				gp.sophonPatchAssetsFromMain[ma.AssetName] =
					sophon.BuildChunkSources(ma, oldIdx, chunkPrefix, useCompress)
				gp.sophonAssetMD5[ma.AssetName] = ma.AssetHashMd5
				continue
			}
			fallthroughAssets = append(fallthroughAssets, ma)
		}
		matches := verifyMatchesParallel(ctx, gameDir, fallthroughAssets, sophonVerifyWorkers)
		// Iterate in manifest order for a deterministic plan.
		for _, ma := range fallthroughAssets {
			if matches[ma.AssetName] {
				continue // unchanged on disk → no work needed
			}
			oldIdx := sophon.BuildPerAssetMD5Index(oldMainManifest, ma.AssetName)
			gp.sophonChunkSources = append(gp.sophonChunkSources,
				sophon.BuildChunkSources(ma, oldIdx, chunkPrefix, useCompress)...)
			gp.sophonAssetMD5[ma.AssetName] = ma.AssetHashMd5 // §E.2 P1 (fall-through chunk_assemble)
		}
	}
	return nil
}

// sophonVerifyWorkers bounds the plan-time on-disk MD5 skip-guard pool.
const sophonVerifyWorkers = 8

// verifyMatchesParallel MD5-checks each asset against its on-disk file at
// <gameDir>/<AssetName> using a bounded worker pool, returning assetName→matches.
// It is the parallel form of the buildSophonPatchPlan skip-guard (a single
// asset's md5MatchesOnDisk is unchanged; only the dispatch is parallelized).
// ctx cancellation stops further dispatch; a missing/unmatched asset maps to
// false (→ the caller plans a chunk_assemble re-download, the safe default).
func verifyMatchesParallel(ctx context.Context, gameDir string, assets []*pb.SophonManifestAssetProperty, workers int) map[string]bool {
	out := make(map[string]bool, len(assets))
	if len(assets) == 0 {
		return out
	}
	if workers < 1 {
		workers = 1
	}
	var mu sync.Mutex
	jobs := make(chan *pb.SophonManifestAssetProperty)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				ok := md5MatchesOnDisk(filepath.Join(gameDir, a.AssetName), a.AssetHashMd5)
				mu.Lock()
				out[a.AssetName] = ok
				mu.Unlock()
			}
		}()
	}
	for _, a := range assets {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return out
		case jobs <- a:
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// md5MatchesOnDisk reports whether the file at path exists and its whole-file
// MD5 equals wantMD5. Missing file or read error → false (work needed).
func md5MatchesOnDisk(path, wantMD5 string) bool {
	if wantMD5 == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == wantMD5
}

// buildSophonPlan orchestrates the §3 decision tree for a Sophon game and
// returns the in-memory plan plus predl availability. currentLocal is the
// installed version (non-empty; caller already short-circuited "").
func buildSophonPlan(
	ctx context.Context,
	p *Provider,
	branch *sophon.BranchInfo,
	gid core.GameID,
	currentLocal string,
	audioLangs []string,
	gameDir string,
	tempRoot string,
) (*genshinPlan, bool, error) {
	g := findByID(gid)
	platApp := ""
	if g != nil {
		platApp = g.PlatApp
	}
	mainSlot := branch.Main
	mainSlot.Branch = "main"
	cats := planCategories(mainSlot, audioLangs)

	prev := LoadAppliedManifests(tempRoot, gid) // NEVER nil (Task 17 P3)
	oldMainManifest := prev.MatchByVersion(currentLocal, "game")

	gp := &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanUpdate,
			Version: branch.Main.Tag,
			Reason:  core.ReasonVersionChanged,
		},
		sophonBranch:              branch,
		sophonCategories:          cats,
		sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{},
		sophonAssetMD5:            map[string]string{}, // §E.2 P1
		sophonRawManifests:        map[string][]byte{}, // §E.2 P2
		sourceVersion:             currentLocal,
		audioLanguages:            audioLangs,
	}

	inDiffTags := containsString(branch.Main.DiffTags, currentLocal)
	switch {
	case inDiffTags:
		gp.flavor = flavorSophonPatch
		if err := buildSophonPatchPlan(ctx, p, gp, mainSlot, platApp, cats, currentLocal, oldMainManifest, gameDir); err != nil {
			return nil, false, err
		}
	case oldMainManifest != nil:
		gp.flavor = flavorSophonBuild
		if err := buildSophonBuildPlan(ctx, p, gp, mainSlot, platApp, cats, prev, currentLocal); err != nil {
			return nil, false, err
		}
	default:
		gp.flavor = flavorSophonFull
		if err := buildSophonBuildPlan(ctx, p, gp, mainSlot, platApp, cats, nil, currentLocal); err != nil {
			return nil, false, err
		}
	}

	gp.TotalBytes = sumSophonTotalBytes(gp)

	predlAvail, err := buildSophonPredlPlan(ctx, p, gp, branch, platApp, currentLocal, audioLangs, oldMainManifest, prev, gameDir)
	if err != nil {
		return nil, false, err
	}
	gp.predlAvailable = predlAvail
	return gp, predlAvail, nil
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// sumSophonTotalBytes returns the progress denominator per §5.3: decompressed
// chunk bytes + local read bytes + patch slice lengths (dedup patch blobs by
// PatchName so shared blobs are not double-counted).
func sumSophonTotalBytes(gp *genshinPlan) int64 {
	var total int64
	for _, s := range gp.sophonChunkSources {
		total += s.DecompSize
	}
	seen := map[string]bool{}
	for _, p := range gp.sophonPatches {
		if seen[p.PatchName] {
			continue
		}
		seen[p.PatchName] = true
		total += p.PatchSize
	}
	return total
}

// buildSophonPredlPlan implements §3.3. Returns predlAvail; when true it also
// populates gp.predlPlan with the parallel plan built on branch.PreDownload.
// Full-flavor predl is never offered (§0): if neither DiffTags nor a cached
// old manifest apply, predlAvail is forced false.
func buildSophonPredlPlan(
	ctx context.Context,
	p *Provider,
	gp *genshinPlan,
	branch *sophon.BranchInfo,
	platApp string,
	currentLocal string,
	audioLangs []string,
	oldMainManifest *pb.SophonManifestProto,
	prev *appliedSet,
	gameDir string,
) (bool, error) {
	if branch.PreDownload.IsEmpty() ||
		currentLocal == branch.PreDownload.Tag ||
		currentLocal == "" {
		return false, nil
	}

	predlSlot := branch.PreDownload
	predlSlot.Branch = "predownload"
	cats := planCategories(predlSlot, audioLangs)

	var predlFlavor planFlavor
	switch {
	case containsString(branch.PreDownload.DiffTags, currentLocal):
		predlFlavor = flavorSophonPredlPatch
	case oldMainManifest != nil:
		predlFlavor = flavorSophonPredlBuild
	default:
		// No chunk reuse possible → would be a blind full predl; never offered.
		return false, nil
	}

	scratch := &genshinPlan{
		sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{},
		sophonAssetMD5:            map[string]string{},
		sophonRawManifests:        map[string][]byte{},
	}
	switch predlFlavor {
	case flavorSophonPredlPatch:
		if err := buildSophonPatchPlan(ctx, p, scratch, predlSlot, platApp, cats, currentLocal, oldMainManifest, gameDir); err != nil {
			return false, err
		}
	case flavorSophonPredlBuild:
		if err := buildSophonBuildPlan(ctx, p, scratch, predlSlot, platApp, cats, prev, currentLocal); err != nil {
			return false, err
		}
	}

	gp.predlPlan = &predlPlanCache{
		Flavor:         predlFlavor,
		BuildID:        scratch.sophonBuildID,
		SourceVersion:  currentLocal,
		TargetVersion:  branch.PreDownload.Tag,
		AudioLanguages: audioLangs,
		ChunkSources:   scratch.sophonChunkSources,
		Patches:        scratch.sophonPatches,
		Deletes:        scratch.sophonDeletes,
		Categories:     cats,
	}
	return true, nil
}

// detectPredlConsume implements §3.6. It reads
//
//	versionSidecarDir(tempRoot, gid, mainTag)/predl_ready.json
//
// and returns (true, &file) only when the staged predl is consumable for the
// current update (matches target + source + diff window). On any stale verdict
// it deletes the sidecar AND its staging/predl/<BuildID> tree, then returns
// (false, nil). ENOENT → (false, nil) with no cleanup.
func detectPredlConsume(tempRoot string, gid core.GameID, currentLocal, mainTag string, mainDiffTags []string) (bool, *sophonPredlReadyFile) {
	verDir := versionSidecarDir(tempRoot, gid, mainTag)
	path := filepath.Join(verDir, "predl_ready.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, nil
	}
	var pf sophonPredlReadyFile
	if err := json.Unmarshal(data, &pf); err != nil {
		_ = os.Remove(path)
		return false, nil
	}

	stale := func() (bool, *sophonPredlReadyFile) {
		_ = os.Remove(path)
		_ = os.Remove(filepath.Join(verDir, "sophon_progress.json"))
		if pf.BuildID != "" {
			_ = os.RemoveAll(filepath.Join(verDir, "staging", "predl", pf.BuildID))
		}
		return false, nil
	}

	if pf.Kind != "sophon_patch" && pf.Kind != "sophon_build" {
		return stale()
	}
	if pf.TargetVersion != mainTag {
		slog.Warn("hoyoverse/sophon: predl_stale target mismatch", "target", pf.TargetVersion, "main", mainTag)
		return stale()
	}
	if pf.SourceVersion != currentLocal {
		return stale()
	}
	if pf.Kind == "sophon_patch" && !containsString(mainDiffTags, currentLocal) {
		return stale()
	}
	return true, &pf
}
