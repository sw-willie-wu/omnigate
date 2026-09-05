package hoyoverse

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/patch/hpatchz"
	"omnigate/internal/providers/hoyoverse/sophon"
)

// buildSophonWAL turns a planned genshinPlan into a fresh sophon_apply.wal.
// stagingRoot is the resolved staging dir (main or predl). Records interleave
// chunk_assemble (grouped per asset, in plan append order), hdiff_patch /
// copy_over (from sophonPatches), and delete (from sophonDeletes) per §6.1.
func buildSophonWAL(gp *genshinPlan, gid core.GameID, targetTag, sourceTag, stagingRoot string, wasPredl bool) *sophonApplyWAL {
	wal := &sophonApplyWAL{
		GameID:      string(gid),
		TargetTag:   targetTag,
		BuildID:     gp.sophonBuildID,
		SourceTag:   sourceTag,
		Flavor:      gp.flavor.String(),
		BranchKind:  "main",
		WasPredl:    wasPredl,
		StagingRoot: stagingRoot,
	}

	// chunk_assemble: group chunk sources by owning asset, preserving order.
	type group struct {
		path string
		srcs []sophon.ChunkSource
	}
	order := []string{}
	byAsset := map[string]*group{}
	for _, s := range gp.sophonChunkSources {
		g, ok := byAsset[s.Asset]
		if !ok {
			g = &group{path: s.Asset}
			byAsset[s.Asset] = g
			order = append(order, s.Asset)
		}
		g.srcs = append(g.srcs, s)
	}
	for _, asset := range order {
		g := byAsset[asset]
		walSrcs := make([]walChunkSource, 0, len(g.srcs))
		for _, s := range g.srcs {
			walSrcs = append(walSrcs, toWalChunkSource(s))
		}
		// §E item 5: whole-file MD5 comes from the asset→MD5 map Task 18
		// populated on genshinPlan, NOT from any single chunk's ExpectMD5.
		wal.Records = append(wal.Records, sophonApplyRecord{
			Kind:            "chunk_assemble",
			Path:            g.path,
			State:           "pending",
			AssetMD5:        gp.sophonAssetMD5[g.path],
			AssembleSources: walSrcs,
		})
	}

	// hdiff_patch / copy_over
	for _, pi := range gp.sophonPatches {
		kind := "hdiff_patch"
		if pi.Method == sophon.MethodCopyOver {
			kind = "copy_over"
		}
		wal.Records = append(wal.Records, sophonApplyRecord{
			Kind:            kind,
			Path:            pi.Asset,
			State:           "pending",
			AssetMD5:        pi.ExpectMD5,
			OldPath:         pi.OldFile,
			PatchName:       pi.PatchName,
			PatchOff:        pi.PatchOffset,
			PatchLen:        pi.PatchLength,
			OriginalFileMD5: pi.OriginalFileMD5,
		})
	}

	// delete
	for _, di := range gp.sophonDeletes {
		wal.Records = append(wal.Records, sophonApplyRecord{
			Kind:      "delete",
			Path:      di.Path,
			State:     "pending",
			ExpectMD5: di.ExpectMD5,
		})
	}
	return wal
}

// hpatchzRunOrDefault returns p.hpatchzRun if set (test seam), else hpatchz.Run.
func (p *Provider) hpatchzRunOrDefault() func(ctx context.Context, oldFile, diffFile, newFile string) error {
	if p.hpatchzRun != nil {
		return p.hpatchzRun
	}
	return hpatchz.Run
}

// runSophonApply applies a planned/resumed Sophon update. stagingRoot is the
// resolved staging dir (main or predl); when "" it is derived from the WAL.
func runSophonApply(
	ctx context.Context,
	p *Provider,
	gid core.GameID,
	gp *genshinPlan,
	tempRoot, gameDir, stagingRoot string,
	emit func(stage string, current, total int),
) error {
	versionDir := versionSidecarDir(tempRoot, gid, gp.Version)
	lock := newApplyLock()
	if err := lock.Acquire(versionDir); err != nil {
		return err
	}
	defer lock.Release()

	wal, err := readSophonApplyWAL(versionDir)
	if err != nil {
		return err
	}
	if wal == nil || len(wal.Records) == 0 {
		wal = buildSophonWAL(gp, gid, gp.Version, gp.sourceVersion, stagingRoot, gp.predlConsume)
		if err := writeSophonApplyWAL(versionDir, wal); err != nil {
			return err
		}
	}
	if stagingRoot == "" {
		stagingRoot = wal.StagingRoot
	}

	// Task 16 flusher: batches 50 records / 5s. NOTE: no `defer flusher.Close()`
	// — finalizeSophonApply removes the WAL, and a deferred flush would re-create
	// it (spurious resume on next launch). We flush explicitly on every path.
	flusher := newWalFlusher(versionDir, wal, nil)

	total := len(wal.Records)
	for i := range wal.Records {
		if err := ctx.Err(); err != nil {
			_ = flusher.flush() // persist progress so resume can continue
			return err
		}
		rec := &wal.Records[i]
		if rec.State == "done" {
			emit("applying", i+1, total)
			continue
		}
		if err := applySophonRecord(ctx, p, gp, wal, rec, gameDir, stagingRoot, versionDir, flusher); err != nil {
			_ = flusher.flush() // persist partial progress for resume
			return err
		}
		rec.State = "done"
		if err := flusher.maybeFlush(); err != nil {
			return err
		}
		emit("applying", i+1, total)
	}
	if err := flusher.flush(); err != nil { // ensure all done-states persisted
		return err
	}
	return finalizeSophonApply(p, gid, gp, tempRoot, gameDir, wal)
}

// applySophonRecord dispatches one record by Kind. gp + versionDir + flusher are
// threaded for the §6.4 demotion path (§E.2 P7); phantom helpers
// p.sophonDemoteSources / versionSidecarDirFromWAL are NOT used.
func applySophonRecord(ctx context.Context, p *Provider, gp *genshinPlan, wal *sophonApplyWAL, rec *sophonApplyRecord, gameDir, stagingRoot, versionDir string, flusher *walFlusher) error {
	switch rec.Kind {
	case "chunk_assemble":
		return applyChunkAssemble(ctx, p, rec, gameDir, stagingRoot)
	case "hdiff_patch":
		return applyHDiffPatch(ctx, p, gp, wal, rec, gameDir, stagingRoot, versionDir)
	case "copy_over":
		return applyCopyOver(rec, gameDir, stagingRoot)
	case "delete":
		return applyDelete(rec, gameDir)
	default:
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
}

// applyChunkAssemble implements §6.3.
func applyChunkAssemble(ctx context.Context, p *Provider, rec *sophonApplyRecord, gameDir, stagingRoot string) error {
	srcs := make([]sophon.ChunkSource, 0, len(rec.AssembleSources))
	var totalSize int64
	for _, ws := range rec.AssembleSources {
		srcs = append(srcs, fromWalChunkSource(ws))
		totalSize += ws.DecompSize
	}
	outTmp := filepath.Join(stagingRoot, "assembled", rec.Path+".tmp")
	readChunk := func(src sophon.ChunkSource) ([]byte, error) {
		if src.Kind == sophon.SourceLocal {
			b, err := readLocalChunkBytes(gameDir, src)
			if errors.Is(err, sophon.ErrChunkStale) {
				// §6.3 step 4: demote this chunk to CDN synchronously.
				cdnSrc := src
				cdnSrc.Kind = sophon.SourceCDN
				out := filepath.Join(stagingRoot, "chunks", src.ChunkName)
				if derr := sophon.DownloadChunk(ctx, p.httpClientOrDefault(), cdnSrc, out); derr != nil {
					return nil, derr
				}
				return os.ReadFile(out)
			}
			return b, err
		}
		return os.ReadFile(filepath.Join(stagingRoot, "chunks", src.ChunkName))
	}
	if err := sophon.AssembleFile(outTmp, totalSize, srcs, readChunk); err != nil {
		_ = os.Remove(outTmp)
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	if rec.AssetMD5 != "" && !md5MatchesOnDisk(outTmp, rec.AssetMD5) {
		_ = os.Remove(outTmp)
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	return sophonRenameIntoGame(outTmp, gameDir, rec.Path)
}

// applyHDiffPatch implements §6.4. On OriginalFileMD5 mismatch it demotes to
// chunk_assemble using gp.sophonPatchAssetsFromMain[rec.Path] (§E.2 P7) and the
// threaded versionDir — the phantom p.sophonDemoteSources/versionSidecarDirFromWAL
// are gone. If gp is nil (offline WAL-resume) and a demotion is required, surface
// sophon_apply_failed rather than panic (§6.4 guarantees a pre-crash demotion was
// already persisted as chunk_assemble, so this path is not hit on normal resume).
func applyHDiffPatch(ctx context.Context, p *Provider, gp *genshinPlan, wal *sophonApplyWAL, rec *sophonApplyRecord, gameDir, stagingRoot, versionDir string) error {
	oldPath := filepath.Join(gameDir, rec.OldPath)
	if rec.OriginalFileMD5 != "" && !md5MatchesOnDisk(oldPath, rec.OriginalFileMD5) {
		// Synchronous demotion to chunk_assemble (§6.4).
		var sources []sophon.ChunkSource
		if gp != nil {
			sources = gp.sophonPatchAssetsFromMain[rec.Path]
		}
		if len(sources) == 0 {
			return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
		}
		for _, s := range sources {
			if s.Kind != sophon.SourceCDN {
				continue
			}
			out := filepath.Join(stagingRoot, "chunks", s.ChunkName)
			if md5MatchesOnDisk(out, s.ExpectMD5) {
				continue
			}
			if err := sophon.DownloadChunk(ctx, p.httpClientOrDefault(), s, out); err != nil {
				return &core.UpdateError{Code: "sophon_chunk_verify_failed", Params: map[string]string{"file": s.ChunkName}, Retryable: true}
			}
		}
		walSrcs := make([]walChunkSource, 0, len(sources))
		for _, s := range sources {
			walSrcs = append(walSrcs, toWalChunkSource(s))
		}
		rec.Kind = "chunk_assemble"
		rec.AssembleSources = walSrcs
		rec.OldPath = ""
		rec.PatchName = ""
		rec.OriginalFileMD5 = ""
		// Persist BEFORE executing so a crash resumes as chunk_assemble (§6.4).
		if err := writeSophonApplyWAL(versionDir, wal); err != nil {
			return err
		}
		return applyChunkAssemble(ctx, p, rec, gameDir, stagingRoot)
	}

	// Match path: extract slice → hdiff input → HDiffApply.
	slice, err := readPatchSlice(stagingRoot, rec.PatchName, rec.PatchOff, rec.PatchLen)
	if err != nil {
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	hdiffInput := filepath.Join(stagingRoot, "hdiff_inputs", fmt.Sprintf("%s_%d.bin", rec.PatchName, rec.PatchOff))
	if err := os.MkdirAll(filepath.Dir(hdiffInput), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(hdiffInput, slice, 0o644); err != nil {
		return err
	}
	outTmp := filepath.Join(stagingRoot, "assembled", rec.Path+".tmp")
	if err := os.MkdirAll(filepath.Dir(outTmp), 0o755); err != nil {
		return err
	}
	if err := sophon.HDiffApply(sophon.HDiffOpts{
		Ctx:       ctx,
		Run:       p.hpatchzRunOrDefault(),
		Method:    sophon.MethodPatch,
		OldFile:   oldPath,
		DiffInput: hdiffInput,
		OutTmp:    outTmp,
	}); err != nil {
		_ = os.Remove(outTmp)
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	if rec.AssetMD5 != "" && !md5MatchesOnDisk(outTmp, rec.AssetMD5) {
		_ = os.Remove(outTmp)
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	return sophonRenameIntoGame(outTmp, gameDir, rec.Path)
}

// applyCopyOver implements §6.5.
func applyCopyOver(rec *sophonApplyRecord, gameDir, stagingRoot string) error {
	slice, err := readPatchSlice(stagingRoot, rec.PatchName, rec.PatchOff, rec.PatchLen)
	if err != nil {
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	outTmp := filepath.Join(stagingRoot, "assembled", rec.Path+".tmp")
	if err := os.MkdirAll(filepath.Dir(outTmp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outTmp, slice, 0o644); err != nil {
		return err
	}
	if rec.AssetMD5 != "" && !md5MatchesOnDisk(outTmp, rec.AssetMD5) {
		_ = os.Remove(outTmp)
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	return sophonRenameIntoGame(outTmp, gameDir, rec.Path)
}

// applyDelete implements §6.6.
func applyDelete(rec *sophonApplyRecord, gameDir string) error {
	target := filepath.Join(gameDir, rec.Path)
	if rec.ExpectMD5 != "" && !md5MatchesOnDisk(target, rec.ExpectMD5) {
		slog.Warn("hoyoverse/sophon: delete MD5 mismatch (continuing)", "file", rec.Path)
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
	}
	return nil
}

// readPatchSlice reads <stagingRoot>/patches/<name>[off:off+length].
func readPatchSlice(stagingRoot, name string, off, length int64) ([]byte, error) {
	f, err := os.Open(filepath.Join(stagingRoot, "patches", name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}

// readLocalChunkBytes reads + MD5-verifies a Local chunk slice from gameDir.
func readLocalChunkBytes(gameDir string, src sophon.ChunkSource) ([]byte, error) {
	f, err := os.Open(filepath.Join(gameDir, src.OldFile))
	if err != nil {
		return nil, sophon.ErrChunkStale
	}
	defer f.Close()
	buf := make([]byte, src.DecompSize)
	if _, err := f.ReadAt(buf, src.OldOffset); err != nil {
		return nil, sophon.ErrChunkStale
	}
	h := md5.Sum(buf)
	if hex.EncodeToString(h[:]) != src.ExpectMD5 {
		return nil, sophon.ErrChunkStale
	}
	return buf, nil
}

// sophonRenameIntoGame renames an assembled .tmp to <gameDir>/<rel>, creating
// parent dirs, with cross-device copy+delete fallback ([DEV-2] / §6.8).
func sophonRenameIntoGame(outTmp, gameDir, rel string) error {
	dst := filepath.Join(gameDir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(outTmp, dst); err != nil {
		if isCrossDevice(err) {
			return copyAndRemove(outTmp, dst)
		}
		return err
	}
	return nil
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		in.Close()
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		in.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		in.Close()
		return err
	}
	if err := out.Close(); err != nil {
		in.Close()
		return err
	}
	in.Close()
	return os.Remove(src)
}

// finalizeSophonApply runs §6.2 steps 6–8 after all records are done.
func finalizeSophonApply(p *Provider, gid core.GameID, gp *genshinPlan, tempRoot, gameDir string, wal *sophonApplyWAL) error {
	writebackOK := true
	if err := WriteGameVersion(gameDir, gp.Version); err != nil {
		writebackOK = false
		p.logger.Warn("sophon apply: config.ini writeback failed", "err", err)
	}
	lat := &lastApplyTarget{
		TargetVersion:     gp.Version,
		AudioLanguages:    gp.audioLanguages,
		CompletionTS:      time.Now().UTC(),
		ConfigWritebackOK: writebackOK,
	}
	if err := writeLastApplyTarget(tempRoot, gid, lat); err != nil {
		p.logger.Warn("sophon apply: last_apply_target write failed", "err", err)
	}

	// §E.2 P2: persist the raw applied main manifests for future dedup BEFORE
	// rotating. Empty on a predl-consumed apply (no live getBuild) — acceptable.
	for mf, raw := range gp.sophonRawManifests {
		if err := SaveAppliedManifest(tempRoot, gid, mf, gp.sophonBuildID, gp.Version, raw); err != nil {
			p.logger.Warn("sophon: save applied manifest", "category", mf, "err", err)
		}
	}
	// §E.2 P3: RotateAfterApply takes map[string]string (matchingField → buildID).
	catMap := make(map[string]string, len(gp.sophonCategories))
	for _, c := range gp.sophonCategories {
		catMap[c.MatchingField] = gp.sophonBuildID
	}
	if err := RotateAfterApply(tempRoot, gid, gp.sophonBuildID, gp.Version, catMap); err != nil {
		p.logger.Warn("sophon apply: manifest rotate failed", "err", err)
	}

	versionDir := versionSidecarDir(tempRoot, gid, gp.Version)
	_ = os.RemoveAll(filepath.Join(versionDir, "staging", "main", gp.sophonBuildID))
	if wal.WasPredl {
		predlBuildID := gp.sophonBuildID
		if gp.predlPlan != nil && gp.predlPlan.BuildID != "" {
			predlBuildID = gp.predlPlan.BuildID
		}
		_ = os.RemoveAll(filepath.Join(versionDir, "staging", "predl", predlBuildID))
		_ = os.Remove(filepath.Join(versionDir, "predl_ready.json"))
	}
	_ = os.Remove(filepath.Join(versionDir, "sophon_apply.wal"))
	_ = os.Remove(filepath.Join(versionDir, "sophon_progress.json"))
	return nil
}

// verifyPredlStaging stats + re-verifies every staged CDN chunk and patch
// blob under stagingRoot. Returns discard=true when the staged content is so
// eroded that a fresh download is cheaper than per-item demotion:
// CDN-chunk fail ratio > 0.25 OR patch-blob fail ratio > 0.50 (spec §7.3 step 4).
// discard=false leaves per-item misses to be re-fetched organically by
// downloadAllSophon's skip-if-verified pass. Local chunks are NOT checked here
// (read+verified at apply time per §7.3 step 2).
func verifyPredlStaging(stagingRoot string, sources []sophon.ChunkSource, patches []sophon.PatchInstr) (discard bool, err error) {
	cdnTotal, cdnBad := 0, 0
	for _, s := range sources {
		if s.Kind != sophon.SourceCDN {
			continue
		}
		cdnTotal++
		if !stagedChunkOK(filepath.Join(stagingRoot, "chunks", s.ChunkName), s) {
			cdnBad++
		}
	}
	patchTotal, patchBad := 0, 0
	for _, p := range patches {
		patchTotal++
		if !stagedBlobOK(filepath.Join(stagingRoot, "patches", p.PatchName), p.PatchMD5) {
			patchBad++
		}
	}
	if cdnTotal > 0 && float64(cdnBad)/float64(cdnTotal) > 0.25 {
		return true, nil
	}
	if patchTotal > 0 && float64(patchBad)/float64(patchTotal) > 0.50 {
		return true, nil
	}
	return false, nil
}

// stagedChunkOK reads the staged DECOMPRESSED chunk file and verifies its MD5
// against src.ExpectMD5 (ChunkDecompressedHashMd5). The ChunkName's xxh prefix
// is the COMPRESSED-wire hash and must NOT be applied to the decompressed staged
// bytes (verified against live CDN 2026-06-01).
func stagedChunkOK(path string, src sophon.ChunkSource) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:]) == src.ExpectMD5
}

// stagedBlobOK reads the staged patch blob and verifies its MD5.
func stagedBlobOK(path, wantMD5 string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:]) == wantMD5
}
