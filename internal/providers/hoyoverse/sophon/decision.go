// internal/providers/hoyoverse/sophon/decision.go
package sophon

import (
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// Flavor is the decision-tree outcome for a Sophon CheckForUpdate. It is local
// to the sophon package; the hoyoverse layer maps each value onto a planFlavor
// (part-0 §A.5).
type Flavor int

const (
	DecisionNoInstall Flavor = iota // currentLocal == "" → no install to update
	DecisionIdle                    // currentLocal == branch.Main.Tag → up to date
	DecisionPatch                   // currentLocal ∈ branch.Main.DiffTags → HDiff path
	DecisionBuild                   // not in DiffTags but a prior manifest is cached → chunk-from-disk
	DecisionFull                    // no prior manifest → full download
)

// DecidePath implements the spec §3 case ladder (top-to-bottom). The caller
// (hoyoverse §3) is responsible for fetching the branch and rejecting an empty
// branch.Main BEFORE calling this; a nil branch is treated defensively as
// DecisionNoInstall.
//
// oldMainAvailable reports whether a prior-version "game"-category manifest is
// cached on disk (LoadAppliedManifests().MatchByVersion(currentLocal,"game")
// != nil), which gates Build vs Full.
func DecidePath(branch *BranchInfo, currentLocal string, oldMainAvailable bool) (Flavor, string) {
	if branch == nil {
		return DecisionNoInstall, "nil branch info"
	}
	if currentLocal == "" {
		return DecisionNoInstall, "no local install detected (currentLocal empty)"
	}
	if currentLocal == branch.Main.Tag {
		return DecisionIdle, "local version matches branch.Main.Tag"
	}
	for _, dt := range branch.Main.DiffTags {
		if dt == currentLocal {
			return DecisionPatch, "currentLocal is in branch.Main.DiffTags (HDiff patch path)"
		}
	}
	if oldMainAvailable {
		return DecisionBuild, "prior-version manifest cached (chunk-from-disk dedup path)"
	}
	return DecisionFull, "no prior manifest cached (full download path)"
}

// BuildChunkSources builds the per-asset chunk plan for one new-manifest asset
// (spec §3.2 step 3). For each chunk it looks up its decompressed MD5 in oldIdx:
// a hit emits a SourceLocal entry (read from <gameDir>/<OldFile> at OldOffset),
// a miss emits a SourceCDN entry. All sizes/offsets come from the NEW manifest
// chunk (guaranteed equal to the old chunk by virtue of the MD5 match).
//
// Local entries ALSO carry ChunkName/URLPrefix/CompressedSz/UseCompress so the
// apply phase can fall back to a CDN download if the on-disk old chunk turns
// out stale at apply time (spec §6.3 step 4).
func BuildChunkSources(newAsset *pb.SophonManifestAssetProperty, oldIdx map[string]ChunkRef, chunkURLPrefix string, useCompress bool) []ChunkSource {
	if newAsset == nil {
		return nil
	}
	out := make([]ChunkSource, 0, len(newAsset.AssetChunks))
	for _, chunk := range newAsset.AssetChunks {
		if chunk == nil {
			continue
		}
		src := ChunkSource{
			Asset:        newAsset.AssetName,
			ChunkName:    chunk.ChunkName,
			URLPrefix:    chunkURLPrefix,
			CompressedSz: chunk.ChunkSize,
			UseCompress:  useCompress,
			DecompSize:   chunk.ChunkSizeDecompressed,
			FileOffset:   chunk.ChunkOnFileOffset,
			ExpectMD5:    chunk.ChunkDecompressedHashMd5,
		}
		if ref, ok := oldIdx[chunk.ChunkDecompressedHashMd5]; ok {
			src.Kind = SourceLocal
			src.OldFile = ref.OldFilePath
			src.OldOffset = ref.OldOffset
		} else {
			src.Kind = SourceCDN
		}
		out = append(out, src)
	}
	return out
}

// BuildPatchInstructions performs the patch-package join for flavorSophonPatch
// (spec §3.1 steps 3, 4, 6) and emits ONLY the patch/copyover instructions and
// the UnusedAssets deletes. Per [DEV-5], the main-fall-through chunk_assemble
// for files NOT covered by the patch is assembled by the hoyoverse layer
// (buildSophonPatchPlan), not here — this keeps the sophon function free of
// gameDir / dedup-index concerns. The caller decides which main assets fall
// through to chunk_assemble.
//
// patchURLPrefix is diff_download.url_prefix and is stamped onto every emitted
// PatchInstr.URLPrefix. ExpectMD5 is taken from the MAIN asset's AssetHashMd5
// (the post-apply whole-file hash).
func BuildPatchInstructions(patch *pb.SophonPatchProto, main *pb.SophonManifestProto, currentLocal, patchURLPrefix string) (patches []PatchInstr, deletes []DeleteInstr) {
	if patch == nil || main == nil {
		return nil, nil
	}

	// Step 3: patchDict keyed by AssetName → the AssetInfo whose VersionTag
	// matches currentLocal (skip patch assets with no matching VersionTag).
	patchDict := make(map[string]*pb.SophonPatchAssetInfo, len(patch.PatchAssets))
	for _, pa := range patch.PatchAssets {
		if pa == nil {
			continue
		}
		for _, info := range pa.AssetInfos {
			if info == nil || info.Chunk == nil {
				continue
			}
			if info.VersionTag == currentLocal {
				patchDict[pa.AssetName] = info
				break
			}
		}
	}

	// Step 4: iterate main.Assets (source of truth for "what exists in the new
	// build"); emit a PatchInstr for each asset that has a matching patch entry.
	for _, ma := range main.Assets {
		if ma == nil {
			continue
		}
		info, ok := patchDict[ma.AssetName]
		if !ok {
			continue // caller's fall-through ([DEV-5]); not emitted here.
		}
		ck := info.Chunk
		instr := PatchInstr{
			Asset:       ma.AssetName,
			PatchName:   ck.PatchName,
			URLPrefix:   patchURLPrefix,
			PatchSize:   ck.PatchSize,
			PatchMD5:    ck.PatchMd5,
			PatchOffset: ck.PatchOffset,
			PatchLength: ck.PatchLength,
			ExpectMD5:   ma.AssetHashMd5,
		}
		if ck.OriginalFileName == "" {
			instr.Method = MethodCopyOver
		} else {
			instr.Method = MethodPatch
			instr.OldFile = ck.OriginalFileName
			instr.OriginalFileMD5 = ck.OriginalFileMd5
		}
		patches = append(patches, instr)
	}

	// Step 6: UnusedAssets deletes whose VersionTag matches currentLocal.
	for _, ua := range patch.UnusedAssets {
		if ua == nil || ua.VersionTag != currentLocal {
			continue
		}
		for _, ai := range ua.AssetInfos {
			if ai == nil {
				continue
			}
			for _, f := range ai.Assets {
				if f == nil {
					continue
				}
				deletes = append(deletes, DeleteInstr{
					Path:      f.FileName,
					ExpectMD5: f.FileMd5,
				})
			}
		}
	}

	return patches, deletes
}
