package kurogames

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"omnigate/internal/core"
)

// packSource is one resource pack's resolved indexFile for a plan (spec §5).
type packSource struct {
	Pack    string
	Cfg     indexConfigRaw // the indexFile actually fetched (patch or full)
	Full    indexConfigRaw // the pack's full config (fallback target)
	IdxFile *indexFileRaw
	IsPatch bool
	Est     int // estimated hash count, for the merged progress total
}

// resolvePackSource picks the pack's patchConfig matching localVer (patch
// path) or its full config, fetches that indexFile (md5-verified) and checks
// the pack path rules (spec §2.3).
func (p *Provider) resolvePackSource(ctx context.Context, idx *gameIndexV3, cdn, pack, localVer string) (packSource, error) {
	full := idx.ResourcePacks[pack]
	src := packSource{Pack: pack, Cfg: full, Full: full}
	if localVer != "" && localVer != full.Version {
		for _, pc := range full.PatchConfig {
			if pc.Version == localVer {
				src.Cfg, src.IsPatch = pc, true
				break
			}
		}
	}
	f, err := fetchPackIndexFile(ctx, p.httpClient, cdn, src.Cfg)
	if err != nil {
		return src, err
	}
	if err := validatePackIndexFile(idx, pack, f); err != nil {
		return src, err
	}
	src.IdxFile = f
	src.Est = estHash(f, src.IsPatch)
	return src, nil
}

// estHash mirrors buildFileAndPatchPlan's hash batch: general (non-krpdiff)
// resources + multi-file group dsts + 1:1 group srcs. Full → len(resource).
// It is an upper-bound estimate used only to size the merged progress total;
// the per-pack callback clamps to it.
func estHash(f *indexFileRaw, isPatch bool) int {
	if !isPatch || (len(f.GroupInfos) == 0 && len(f.ApplyTypes) == 0) {
		return len(f.Resource)
	}
	diffs := map[string]bool{}
	for _, g := range f.GroupInfos {
		diffs[g.Dest] = true
	}
	n := 0
	for _, r := range f.Resource {
		if !diffs[r.Dest] {
			n++
		}
	}
	for _, g := range f.GroupInfos {
		if len(g.SrcFiles) == 1 && len(g.DstFiles) == 1 && g.SrcFiles[0].Dest == g.DstFiles[0].Dest {
			n++
		} else {
			n += len(g.DstFiles)
		}
	}
	return n
}

type packPlan struct {
	Files       []core.FileTask
	PatchGroups []core.PatchGroup
	DeleteFiles []string
	PeakTemp    int64
}

// mkFetchFullPack is spec §5.3's fallback fetcher: unlike the removed v2
// full-manifest fetcher it verifies indexFileMd5 and the pack path rules (spec
// §2.1/§2.3), so a whole-plan fallback can never build from a stale or
// foreign indexFile.
func (p *Provider) mkFetchFullPack(idx *gameIndexV3, pack, cdn string) func(ctx context.Context) (*indexFileRaw, string, string, error) {
	full := idx.ResourcePacks[pack]
	return func(ctx context.Context) (*indexFileRaw, string, string, error) {
		f, err := fetchPackIndexFile(ctx, p.httpClient, cdn, full)
		if err != nil {
			return nil, "", "", err
		}
		if err := validatePackIndexFile(idx, pack, f); err != nil {
			return nil, "", "", err
		}
		return f, cdn, full.BaseURL, nil
	}
}

// buildPackPlan classifies one pack's indexFile against the install dir.
// Patch indexFiles with groupInfos/applyTypes go through the krpdiff-aware
// classifier (fallback fetcher bound to THIS pack's full config); everything
// else is a plain md5 filter.
func (p *Provider) buildPackPlan(ctx context.Context, idx *gameIndexV3, installDir, cdn string, src packSource, onProgress func(done, total int)) (packPlan, error) {
	var pp packPlan
	f := src.IdxFile
	if src.IsPatch && (len(f.GroupInfos) > 0 || len(f.ApplyTypes) > 0) {
		fetchFull := p.mkFetchFullPack(idx, src.Pack, cdn)
		files, groups, del, peak, err := p.buildFileAndPatchPlan(ctx, installDir, cdn, src.Cfg.BaseURL, cdn, src.Full.BaseURL, f, fetchFull, onProgress)
		if err != nil {
			return pp, err
		}
		return packPlan{Files: files, PatchGroups: groups, DeleteFiles: del, PeakTemp: peak}, nil
	}
	pp.Files = filterChangedFiles(ctx, installDir, cdn, src.Cfg.BaseURL, f.Resource, p.logger, onProgress)
	if ctx.Err() != nil {
		return pp, ctx.Err()
	}
	if src.IsPatch {
		pp.DeleteFiles = f.DeleteFiles
	}
	return pp, nil
}

// buildPlanV3 builds one UpdatePlan over the given packs (spec §5.4). Shared
// by the regular update (common + every installed bundle pack) and bundle
// install (a single bundle pack). Progress is one merged total across packs.
func (p *Provider) buildPlanV3(ctx context.Context, gid core.GameID, installDir string, idx *gameIndexV3, packs []string, localVer func(string) string, onProgress func(done, total int)) (core.UpdatePlan, error) {
	cdn := pickCDN(idx.CDNList)
	srcs := make([]packSource, 0, len(packs))
	total := 0
	for _, pk := range packs {
		s, err := p.resolvePackSource(ctx, idx, cdn, pk, localVer(pk))
		if err != nil {
			return core.UpdatePlan{}, err
		}
		srcs = append(srcs, s)
		total += s.Est
	}
	emit := func(d int) {
		if onProgress != nil {
			onProgress(d, total)
		}
	}
	plan := core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: idx.ResourcePacks["common"].Version, Reason: core.ReasonVersionChanged, ManifestETag: planToken(idx, packs)}
	base := 0
	for _, s := range srcs {
		b, est := base, s.Est
		sub := func(done, _ int) {
			if done > est {
				done = est
			}
			emit(b + done)
		}
		pp, err := p.buildPackPlan(ctx, idx, installDir, cdn, s, sub)
		if err != nil {
			return core.UpdatePlan{}, err
		}
		plan.Files = append(plan.Files, pp.Files...)
		plan.PatchGroups = append(plan.PatchGroups, pp.PatchGroups...)
		plan.DeleteFiles = append(plan.DeleteFiles, pp.DeleteFiles...)
		plan.PeakTempBytes += pp.PeakTemp
		base += est
		emit(base)
	}
	emit(total)
	// runPatchGroups requires Dst.Size ascending; per-pack lists are sorted
	// but their concatenation is not.
	sort.SliceStable(plan.PatchGroups, func(i, j int) bool { return plan.PatchGroups[i].Dst.Size < plan.PatchGroups[j].Dst.Size })
	for _, f := range plan.Files {
		plan.TotalBytes += f.Size
	}
	p.saveCatalog(gid, catalogFromIndex(idx, time.Now()))
	return plan, nil
}

// planTargets reconstructs the pack set a plan was built for (spec §2.1), so
// RunUpdate can recompute planToken against the live index.
func (p *Provider) planTargets(plan core.UpdatePlan, installDir string) []string {
	if plan.Bundle != "" {
		return []string{packOf(plan.Bundle)}
	}
	s, _ := readInstallState(filepath.Join(installDir, installStateFile))
	out := []string{"common"}
	for _, n := range s.installedKnown() {
		out = append(out, packOf(n))
	}
	return out
}
