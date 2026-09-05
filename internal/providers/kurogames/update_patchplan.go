package kurogames

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omnigate/internal/core"
)

// buildFileAndPatchPlan classifies a fetched patch indexFile into download
// tasks + patch groups per spec §2 (.claude/specs/2026-08-24-wuwa-krpdiff-patch-update-design.md).
//
// cdn/baseURL address the PATCH indexFile's own CDN — used for general
// resource files (which carry FromFolder, or fall back to baseURL) and for
// krpdiff diff downloads (Ephemeral tasks; krpdiff files live under the
// patch config's own directory tree).
//
// fullCDN/fullBaseURL address the FULL (fresh-install) config's CDN —
// required for every group-level-fallback dst FileTask (multi-file groups,
// and 1:1 groups that degrade to a full download): dstFiles metadata never
// carries FromFolder, and the patch config's baseUrl directory does not
// serve full-file content (2026-08-24 real-CDN HEAD: patchBase+dest→404,
// fullBase(zip/)+dest→200 — fix round 1 finding C1). Callers bind both
// cdn/baseURL and fullCDN/fullBaseURL from the SAME config mkFetchFull uses
// (CheckForUpdateWithProgress → idx.Default; CheckForPredownload →
// idx.Predownload) — never hardcode idx.Default here, or the predownload
// path's fallback would mix in live-version files (plan gate B1).
//
// fetchFull lazily fetches the FULL indexFile for WHOLE-PLAN fallback (step
// 0 unknown applyTypes, or a multi-file group with an uncovered src-only
// gap) — a different, heavier escalation than the per-group dst fallback
// above; it re-derives its own (fullCDN, fullBaseURL) from the full
// manifest fetch, which by construction matches the fullCDN/fullBaseURL
// params (same source config).
func (p *Provider) buildFileAndPatchPlan(
	ctx context.Context,
	installDir, cdn, baseURL, fullCDN, fullBaseURL string,
	idxFile *indexFileRaw,
	fetchFull func(ctx context.Context) (idx *indexFileRaw, fullCDN, fullBaseURL string, err error),
	onProgress func(done, total int),
) (files []core.FileTask, groups []core.PatchGroup, deleteFiles []string, peak int64, err error) {
	// step 0: unknown applyTypes → whole-plan full fallback. Patch manifest
	// (incl. deleteFiles) is not trusted at all in this case.
	for _, t := range idxFile.ApplyTypes {
		if t != "group" {
			p.logger.Warn("unknown applyTypes; whole-plan full fallback", "applyTypes", idxFile.ApplyTypes)
			return p.fullFallback(ctx, installDir, idxFile.DeleteFiles, fetchFull, false, onProgress)
		}
	}

	// step 1: no groupInfos → legacy full-manifest flow, unchanged (defensive
	// — kurogames.go's wiring already routes this case straight to
	// filterChangedFiles without calling this builder at all).
	if len(idxFile.GroupInfos) == 0 {
		files = filterChangedFiles(ctx, installDir, cdn, baseURL, idxFile.Resource, p.logger, onProgress)
		if ctx.Err() != nil {
			return nil, nil, nil, 0, ctx.Err()
		}
		return files, nil, nil, 0, nil
	}

	resourceByDest := make(map[string]manifestFileRaw, len(idxFile.Resource))
	groupDestSet := make(map[string]bool, len(idxFile.GroupInfos))
	for _, r := range idxFile.Resource {
		resourceByDest[r.Dest] = r
	}
	for _, g := range idxFile.GroupInfos {
		groupDestSet[g.Dest] = true
	}

	// step 2: classify groups. 1:1 same-path groups become candidates for
	// dst-first hash verification; everything else (multi-file /異路徑) is a
	// group-level fallback — its dstFiles become ordinary full-download
	// FileTasks built straight from groupInfos metadata (no dependency on the
	// full indexFile for METADATA — only the URL prefix comes from the full
	// config, per C1 above). A src-only gap not covered by dstFiles or
	// deleteFiles means that fallback would silently leave the old file in
	// place → escalate to whole-plan fallback instead.
	var candidates []groupInfoRaw
	var groupDstFallback []manifestFileRaw // dst files needing a full download, URL via fullCDN/fullBaseURL
	for _, g := range idxFile.GroupInfos {
		if len(g.SrcFiles) != 1 || len(g.DstFiles) != 1 || g.SrcFiles[0].Dest != g.DstFiles[0].Dest {
			if hasUncoveredSrcOnly(g, idxFile.DeleteFiles) {
				return p.fullFallback(ctx, installDir, idxFile.DeleteFiles, fetchFull, true, onProgress)
			}
			groupDstFallback = append(groupDstFallback, g.DstFiles...)
			continue
		}
		candidates = append(candidates, g)
	}

	// General resource files: entries not referenced as any group's Dest go
	// through the existing filterChangedFiles semantics (URL via the PATCH
	// cdn/baseURL — these carry FromFolder in practice), folded into the
	// same merged hash batch.
	//
	// Defense-in-depth (fix round 1 finding C2, 2026-08-20 incident replay
	// guard): a resource entry named *.krpdiff that isn't consumed as any
	// group's own diff is manifest drift (orphan diff) — it must NEVER
	// become a plain (non-Ephemeral) download task, since apply would then
	// try to rename raw diff bytes into the game dir. Excluded entirely
	// (not staged at all) rather than guessed at, with a Warn log. This
	// check is independent of the groupDestSet membership check above, so
	// even if that check regresses, a *.krpdiff dest can still never reach
	// generalEntries.
	var generalEntries []manifestFileRaw
	for _, r := range idxFile.Resource {
		if groupDestSet[r.Dest] {
			continue // consumed as a group's own diff; handled via resourceByDest lookup below
		}
		if strings.HasSuffix(r.Dest, ".krpdiff") {
			p.logger.Warn("kurogames: orphan krpdiff resource entry not referenced by any group; excluding from plan (manifest drift)", "dest", r.Dest)
			continue
		}
		generalEntries = append(generalEntries, r)
	}

	// step 3(i): cheap local-size pre-check on 1:1 candidates. A local size
	// matching neither src nor dst can't resolve to either hash → classify
	// as group-level fallback immediately without paying for a hash.
	var stillCandidates []groupInfoRaw
	for _, c := range candidates {
		full := filepath.Join(installDir, c.SrcFiles[0].Dest)
		fi, statErr := os.Stat(full)
		if statErr != nil || fi.IsDir() {
			groupDstFallback = append(groupDstFallback, c.DstFiles[0])
			continue
		}
		sz := fi.Size()
		if sz != c.SrcFiles[0].Size && sz != c.DstFiles[0].Size {
			groupDstFallback = append(groupDstFallback, c.DstFiles[0])
			continue
		}
		stillCandidates = append(stillCandidates, c)
	}

	// step 3(ii)/(iii): single merged hash batch — general + group-fallback
	// entries (size-gated: mismatch skips hashing, same as
	// filterChangedFiles) then surviving candidates (always hashed; we need
	// the value to know dst/src/neither). One shared progressTotal so the
	// progress callback never resets mid-scan.
	n := len(generalEntries) + len(groupDstFallback) + len(stillCandidates)
	rels := make([]string, 0, n)
	sizes := make([]int64, 0, n)
	for _, e := range generalEntries {
		rels = append(rels, e.Dest)
		sizes = append(sizes, e.Size)
	}
	for _, e := range groupDstFallback {
		rels = append(rels, e.Dest)
		sizes = append(sizes, e.Size)
	}
	for _, c := range stillCandidates {
		rels = append(rels, c.SrcFiles[0].Dest)
		sizes = append(sizes, -1)
	}
	md5s := localFileMD5s(ctx, installDir, rels, sizes, 0, n, onProgress)
	if ctx.Err() != nil {
		return nil, nil, nil, 0, ctx.Err()
	}

	var outFiles []core.FileTask
	idx := 0
	for _, e := range generalEntries {
		h := md5s[idx]
		idx++
		if h != e.MD5 {
			outFiles = append(outFiles, *newFileTask(cdn, baseURL, e))
		}
	}
	for _, e := range groupDstFallback {
		h := md5s[idx]
		idx++
		if h != e.MD5 {
			outFiles = append(outFiles, *newFileTask(fullCDN, fullBaseURL, e))
		}
	}

	type groupWithDiff struct {
		group    core.PatchGroup
		diffSize int64
	}
	var gwd []groupWithDiff
	var alreadyAtDst, ephemeralDiffs int
	for _, c := range stillCandidates {
		h := md5s[idx]
		idx++
		src := c.SrcFiles[0]
		dst := c.DstFiles[0]
		switch {
		case h == "":
			// Missing/unreadable local file. Guarded as its own case (fix
			// round 1 finding M10) so an empty manifest MD5 (shouldn't
			// happen, but never assume) can't spuriously "match" here.
			outFiles = append(outFiles, *newFileTask(fullCDN, fullBaseURL, dst))
		case h == dst.MD5:
			// Already at target content — group complete, nothing to do.
			alreadyAtDst++
		case h == src.MD5:
			// step 4: krpdiff Ephemeral task. Manifest inconsistency (no
			// matching resource entry) degrades to dst full download rather
			// than aborting.
			entry, ok := resourceByDest[c.Dest]
			if !ok {
				outFiles = append(outFiles, *newFileTask(fullCDN, fullBaseURL, dst))
				continue
			}
			diffTask := newFileTask(cdn, baseURL, entry)
			diffTask.Ephemeral = true
			outFiles = append(outFiles, *diffTask)
			ephemeralDiffs++
			gwd = append(gwd, groupWithDiff{
				group: core.PatchGroup{
					DiffPath: entry.Dest,
					Src:      core.PatchFile{Path: src.Dest, Hash: src.MD5, Size: src.Size},
					Dst:      core.PatchFile{Path: dst.Dest, Hash: dst.MD5, Size: dst.Size},
				},
				diffSize: entry.Size,
			})
		default:
			outFiles = append(outFiles, *newFileTask(fullCDN, fullBaseURL, dst))
		}
	}

	// step 5: sort groups by Dst.Size ascending (apply-order requirement;
	// PeakTempBytes below assumes this order) and compute the temp-usage
	// peak: at each step, bytes still owed for not-yet-consumed diffs plus
	// the dst currently being produced.
	sort.Slice(gwd, func(i, j int) bool { return gwd[i].group.Dst.Size < gwd[j].group.Dst.Size })
	var remaining int64
	for _, g := range gwd {
		remaining += g.diffSize
	}
	for _, g := range gwd {
		if v := remaining + g.group.Dst.Size; v > peak {
			peak = v
		}
		remaining -= g.diffSize
	}
	groups = make([]core.PatchGroup, len(gwd))
	for i, g := range gwd {
		groups[i] = g.group
	}

	sort.Slice(outFiles, func(i, j int) bool { return outFiles[i].Path < outFiles[j].Path })

	// Classification observability (final-fix #3): a real-CDN e2e run can't
	// tell "healthy krpdiff patch run" apart from "silently degraded to a
	// full ~86 GiB fallback" without reading this. patch_groups/
	// ephemeral_diffs should both be >0 and roughly equal on a healthy run;
	// group_fallback_files/general_files being unexpectedly large relative
	// to patch_groups is the signal that classification quietly fell back.
	p.logger.Info("kurogames: patch plan classified",
		"patch_groups", len(groups),
		"group_fallback_files", len(groupDstFallback),
		"already_at_dst", alreadyAtDst,
		"general_files", len(generalEntries),
		"ephemeral_diffs", ephemeralDiffs,
		"peak_temp_bytes", peak,
	)
	return outFiles, groups, idxFile.DeleteFiles, peak, nil
}

// hasUncoveredSrcOnly reports whether a group's srcFiles contains an entry
// that is neither in the group's own dstFiles nor in the manifest's
// deleteFiles — i.e. a file the group-level dst-only fallback would
// silently leave stale (spec §2 src-only 防護).
func hasUncoveredSrcOnly(g groupInfoRaw, deleteFiles []string) bool {
	dstSet := make(map[string]bool, len(g.DstFiles))
	for _, d := range g.DstFiles {
		dstSet[d.Dest] = true
	}
	delSet := make(map[string]bool, len(deleteFiles))
	for _, d := range deleteFiles {
		delSet[d] = true
	}
	for _, s := range g.SrcFiles {
		if !dstSet[s.Dest] && !delSet[s.Dest] {
			return true
		}
	}
	return false
}

// fullFallback discards the patch indexFile in favor of the FULL manifest
// (fetched lazily via fetchFull) and runs it through the existing
// filterChangedFiles flow — the official launcher's Type:"fix" semantics.
// keepDeleteFiles controls whether the patch manifest's DeleteFiles is
// carried through: true for the src-only-gap escalation (the patch manifest
// itself is still trusted), false for unknown-applyTypes (nothing about the
// manifest is trusted, so no delete list either — spec §2).
func (p *Provider) fullFallback(
	ctx context.Context,
	installDir string,
	patchDeleteFiles []string,
	fetchFull func(ctx context.Context) (*indexFileRaw, string, string, error),
	keepDeleteFiles bool,
	onProgress func(done, total int),
) (files []core.FileTask, groups []core.PatchGroup, deleteFiles []string, peak int64, err error) {
	if fetchFull == nil {
		return nil, nil, nil, 0, fmt.Errorf("kurogames: whole-plan fallback needed but no full-manifest fetcher available")
	}
	full, fullCDN, fullBaseURL, ferr := fetchFull(ctx)
	if ferr != nil {
		return nil, nil, nil, 0, ferr
	}
	files = filterChangedFiles(ctx, installDir, fullCDN, fullBaseURL, full.Resource, p.logger, onProgress)
	if ctx.Err() != nil {
		return nil, nil, nil, 0, ctx.Err()
	}
	if keepDeleteFiles {
		deleteFiles = patchDeleteFiles
	}
	return files, nil, deleteFiles, 0, nil
}

// mkFetchFull binds the FULL (fresh-install) indexFile source for one entry
// point. CheckForUpdateWithProgress passes (idx.Default.Config,
// pickCDN(idx.Default.CDNList)); CheckForPredownload passes
// (idx.Predownload.Config, pickCDN(idx.Predownload.CDNList)) — each entry
// point must bind its OWN config+CDN so a predownload's whole-plan fallback
// never fetches the live (Default) manifest (plan gate B1). Callers also
// pass this SAME (fullCfg.BaseURL, fullCDN) pair directly to
// buildFileAndPatchPlan's fullCDN/fullBaseURL params (per-group dst
// fallback URLs; fix round 1 finding C1).
func (p *Provider) mkFetchFull(fullCfg indexConfigRaw, fullCDN string) func(ctx context.Context) (*indexFileRaw, string, string, error) {
	return func(ctx context.Context) (*indexFileRaw, string, string, error) {
		f, _, err := fetchIndexFile(ctx, p.httpClient, fullCDN+fullCfg.IndexFile)
		return f, fullCDN, fullCfg.BaseURL, err
	}
}
