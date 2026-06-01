# Omnigate M3.B v2 — HoYoverse Sophon Protocol Design

**Status:** Draft, post-brainstorm + 4 reviewer rounds (2026-06-01)
**Target tag:** `v0.4.0-m3b` (combined v1 + v2; tagged at merge into `main`)
**Branch:** `m3b-v2/spec` (branched from `dev`, which has `m3b/spec` merged in via `--no-ff`)
**Scope:** Genshin Impact 6.0+ update / install / predownload via HoYoverse's **Sophon** chunk-level binary delta protocol. HSR / ZZZ stay on legacy `getGamePackages` (M3.B v1) until HoYoverse migrates them.

This spec is the source of truth for the M3.B v2 implementation plan. Protocol facts were extracted from [CollapseLauncher/Hi3Helper.Sophon](https://github.com/CollapseLauncher/Hi3Helper.Sophon) and [CollapseLauncher/Collapse](https://github.com/CollapseLauncher/Collapse) per the standing convention; no live HoYoverse API probing beyond the `getGameBranches` snapshot already captured in M3.B v1.

---

## §0. Locked decisions (do NOT revisit)

| Decision | Choice | Rationale |
|---|---|---|
| Game scope | Genshin Impact only (Sophon-migrated) | HSR / ZZZ stay legacy; `UsesSophon` flag in `meta.go` already discriminates |
| Feature scope | Delta + Full + Predownload | User-confirmed maximum coverage |
| Update path A (HDiff) | `getPatchBuild` when `currentLocal ∈ branch.Main.DiffTags` | Smallest payload; reuses M3.B v1 `hpatchz` binary |
| Update path B (chunk-from-disk diff) | `getBuild` + prior-version manifest sidecar lookup when not in DiffTags but prior manifest cached | Saves 80–95% vs full re-download for 3+ version-behind users |
| Update path C (full) | `getBuild` fresh download when no prior manifest | Fresh-cache fallback; safe-mode |
| Fresh install | NOT supported — return `sophon_no_install` error | Omnigate guides user to open HoYoPlay for initial install; we only handle updates |
| Patch + Main merge | Patch flow ALWAYS fetches both `getPatchBuild` AND `getBuild`; files-not-in-patch fall through to main manifest | Per `SophonPatch.EnumerateUpdateAsync` in Collapse |
| Hybrid record stream | Under flavorSophonPatch, `genshinPlan.sophonPatches` (Patch/CopyOver records) + `genshinPlan.sophonChunkSources` (DownloadOver / main-only-fall-through chunks) BOTH stream into a single `sophon_apply.wal` Records list | Avoids ambiguity about Patch-only vs Build-only apply at flavor boundaries |
| Audio packs | Subset by `DetectInstalledLanguages` (v1 reuses) **translated to `MatchingField` codes via v1's `folderToAudioLang` map** ("Chinese" → "zh-cn", "English(US)" → "en-us", "Japanese" → "ja-jp", "Korean" → "ko-kr"). Categories from `branch.Categories` are compared against the translated codes. | Avoid pulling 30+ GB of unused VO; `MatchingField` is the manifest's native language identifier |
| Persistence | Atomic JSON sidecar files (write-temp → fsync → rename), same pattern as M3.B v1 | SQLite deferred to post-v2 milestone (gacha records + cross-game state) |
| Old-manifest retention | Keep latest applied + previous 1 build per game (`<build_id>__<category>.manifest.pb.zst` retained for dedup at next update) | Supports 2-step chunk-dedup chain (e.g. 6.4 → 6.5 → 6.6) |
| Chunk-reuse keying | **Per-asset MD5 (`ChunkDecompressedHashMd5`)**, matching Collapse `SophonUpdate.GetChunkOldOffsetFromOld` | The xxh64 prefix of `ChunkName` is a CDN-filename integrity hash, NOT a dedup key |
| Chunk-download integrity | xxh64 of decompressed bytes (if `ChunkName` first 16 hex parses) → fallback to MD5 (`ChunkDecompressedHashMd5`). **Deliberate divergence from Collapse** (which uses MD5 by default for main chunks, xxh64 only for patch blobs). | xxh64 is faster and the prefix is manifest-supplied truth. Risk: spec divergence; mitigation by mandatory fallback when parse fails. Logged in §10 risks. |
| HDiff binary | Move M3.B v1 `hpatchz.go` to NEW `internal/providers/hoyoverse/hpatchz/` sub-package so BOTH `hoyoverse` and `hoyoverse/sophon` can import it without cycle | Architecturally cleaner than callback injection |
| Alt-CDN fallback for chunks | **NOT implemented in v2.0; deferred to v2.1.** Collapse retries chunk fetches against `SophonChunksInfoAlt` (the OLD branch's chunk-CDN base) when the new-branch URL returns non-success; we don't. | Risk: rare HoYoverse-side chunk-URL rotation between manifest cache and chunk fetch causes false `sophon_chunk_verify_failed` for Path A (HDiff via patch blobs) and Path C (Full from getBuild). Does NOT affect Path B (LocalChunkRead never touches CDN). Logged in §10. |
| Encryption | `password` field from `getGameBranches` and `chunk_download.password` are PARSED but NEVER READ for crypto. Collapse declares `EncryptionPassword` and never reads it; we mirror that. | Honest description; the exact purpose (CDN URL token? deferred crypto?) is unknown. Defensive telemetry logs any non-empty `password` value as a tripwire. |
| Manifest format | zstd-compressed Protocol Buffers | `google.golang.org/protobuf` + `klauspost/compress/zstd` |
| `plat_app` | Hardcoded per-game in `meta.go` (Genshin global = `ddxf6vlr1reo`) | 1:1 with biz code, stable; same approach as `APIGameID` |
| Sophon CDN host | Separate API base `https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api` | Confirmed in Collapse `PresetConfig.cs` URL templates |
| `branch.Categories` source | **Live `getGameBranches` API observation (2026-06-01 memory)**, NOT Collapse — Collapse's `HypGameInfoBranchData` does not include categories | HoYoverse added the field after Collapse last synced; same applies to `pre_download` categories |
| Concurrent updates | Allowed (per-game `InFlightOp`) | M3.B v1 frontend wiring already supports this |
| Disk-space pre-flight | Hard block (sum of decompressed chunk sizes × 1.1) | Mirror M3.B v1 |
| `config.ini` writeback failure | Warn-log; surface via `last_apply_target.json`; `maybeSelfHealSophon` retries on next CheckForUpdate (24h budget) | Mirror M3.B v1 Genshin admin caveat verbatim |
| Apply WAL | New parallel sidecar `sophon_apply.wal` with typed-record schema; batched rewrite (every 50 records OR every 5s, whichever first) | v1's `apply.wal` flat-list format can't encode chunk_assemble/hdiff_patch/copy_over; per-record rewrite at 50K records ≈ 250 MB I/O |
| Progress sidecar | New parallel sidecar `sophon_progress.json` with chunk-level granularity; v1's `progress.json` (per-file granularity, `core.ProgressFile`) untouched | Avoids contaminating cross-provider `core.ProgressFile` with Sophon-specific fields |
| `core.ScanRecovery` extension | Add `sophon_apply.wal` and `sophon_progress.json` to the existence-check ladder. **Precedence order**: `sophon_apply.wal > apply.wal > sophon_progress.json > progress.json+predl > progress.json > predl_ready.json`. Sophon sidecars supersede v1 sidecars at the same scope. | App-layer bell-drawer wiring already queries `core.ScanRecovery`; without this extension Sophon-crash UI affordance never fires |
| New `core.ReasonCode` constant | Add `ReasonResumeInterrupted ReasonCode = "resume_interrupted"` to `core/updater.go` | Used by v2 only for now; v1's other reason codes (`ReasonVersionChanged`, `ReasonAudioPackAdded`, `ReasonVersionAndAudio`, `ReasonPredownload`) cover everything else |
| Staging directory layout | Split by branch: `staging/main/<build_id>/` vs `staging/predl/<build_id>/` | Eliminates risk of `build_id` collision between `main` and `pre_download` branches |
| Same-chunk worker race | Tolerated. Two workers may concurrently download the same chunk; atomic rename + xxh64 verify make the result byte-identical. No inflight singleflight map needed. | Adds complexity for no benefit; duplicate work is bounded (a chunk appears in at most a handful of files) |
| Full-flavor predownload | **NOT offered.** If §3.3's predl-flavor decision yields `flavorSophonFull` (no prior manifest cached → no chunk reuse possible), `predlAvailable` is forced to false. | Predownloading tens of GB blind (no install state to reuse from) is wasteful; user has not committed to that scope. Patch + Build predl only. |
| Pre-apply oldFile MD5 verify | hdiff_patch records verify `originalFileMD5` against on-disk source BEFORE invoking hpatchz. Mismatch → synchronous demotion: emit chunk-download jobs for the file's main-manifest chunks (from `genshinPlan.sophonPatchAssetsFromMain[path]`); apply phase blocks until those chunks land, then rewrites the WAL record as `chunk_assemble` and executes it. | Modded source files would otherwise produce garbage hpatchz output → terminal `sophon_apply_failed`. Demotion fetches just the chunks actually needed (vs defensively pre-downloading every patch file's main chunks, which would double bandwidth). |
| Chunk staging filename | `staging/<branchKind>/<build_id>/chunks/<ChunkName>` where `ChunkName` is the full CDN filename verbatim from the manifest. `sophon_progress.ChunksDone` keys on `ChunkName` too. xxh64 is verification-only, never filename. | Works uniformly whether ChunkName's first-16-hex parses or not; removes the round-4 ambiguity over MD5-fallback chunk naming. |
| Option A short-circuit removal | Replace `sophon_not_supported` error path with real plan | The whole point of v2 |

---

## §1. Package layout & file responsibilities

### `internal/core/` — files **extended**

| File | Change | Responsibility |
|---|---|---|
| `updater.go` | extended | Add `ReasonResumeInterrupted ReasonCode = "resume_interrupted"` constant. Existing constants untouched. |
| `recovery.go` | extended | `ScanRecovery` learns two new sidecar filenames: `sophon_apply.wal` (classified as `RecoveryPhaseApplyResume` — same prevailing-sidecar precedence as v1 `apply.wal`) and `sophon_progress.json` (classified as `RecoveryPhaseDownloadResume`). Same cleanup-of-stale-companions semantics as v1. |

### `internal/providers/hoyoverse/` — files **extended** from v1

| File | Change | Responsibility |
|---|---|---|
| `meta.go` | extended | Add per-game `PlatApp string` field (Genshin global = `"ddxf6vlr1reo"`). `UsesSophon` flag already exists. |
| `api.go` | extended | Add `branchInfo` struct + `fetchBranchInfo(ctx, apiGameID) (*branchInfo, error)` returning full `{Main, PreDownload}` with `{PackageID, Password, Tag, DiffTags, Categories}`. `fetchBranchTag` becomes a thin wrapper. |
| `hoyoverse.go` | extended | `CheckForUpdate`: Sophon path dispatched on `UsesSophon=true` (replaces `sophon_not_supported` short-circuit). `RunUpdate`: Sophon flavors dispatched via new `runSophon*` helpers. `manifestCache` already holds `*genshinPlan`. |
| `plan_internal.go` | extended | Add 5 new `planFlavor` constants (`flavorSophonPatch`, `flavorSophonBuild`, `flavorSophonFull`, `flavorSophonPredlPatch`, `flavorSophonPredlBuild`). Extend `genshinPlan` with **9 new fields**: `sophonBranch *branchInfo`, `sophonBuildID string`, `sophonCategories []sophonCategory`, `sophonChunkSources []chunkSource`, `sophonPatches []sophonPatchInstr`, `sophonDeletes []sophonDeleteInstr`, `sophonPatchAssetsFromMain map[string][]chunkSource` (path → per-asset chunk plan from main manifest; used by §6.4 demotion fallback), `predlConsume bool`, `predlSnapshot *sophonPlanSnapshot`, `predlPlan *predlPlanCache` (§3.3 — holds the parallel predl plan when `predlAvailable=true` AND user hasn't yet consumed it). |
| `sidecar_paths.go` | extended | Add `sophonSubdir(tempRoot, gid)` → `<gameSidecarDir>/.sophon` (dot-prefix is deliberate: `internal/app/update_handler.go::scanForRecoveryRoot` iterates `IsDir()` entries under `gameSidecarDir` and treats each as a version; a leading `.` keeps it skipped without modifying that walker). Plus `sophonManifestsDir`, `sophonAppliedJSONPath`, `sophonStagingDir(tempRoot, gid, version, branchKind, buildID)` where `branchKind ∈ {"main", "predl"}`. Layered on v1's `gameSidecarDir = <tempRoot>/<flat_gid>` and `versionSidecarDir = <tempRoot>/<flat_gid>/<version>`. |

### `internal/providers/hoyoverse/hpatchz/` — **new sub-package (moved from parent)**

Extracts v1's `hpatchz.go` to break the proposed `sophon → hoyoverse` import cycle. Both `hoyoverse` and `hoyoverse/sophon` import `hpatchz` cleanly.

| File | Responsibility |
|---|---|
| `hpatchz.go` | `package hpatchz`. Verbatim move of v1's `internal/providers/hoyoverse/hpatchz.go`: `go:embed third_party_hpatchz/hpatchz.exe`, `sync.Once`-guarded extract, `Run(ctx, oldFile, hdiffFile, newFile) error`. |
| `hpatchz_test.go` | Verbatim move of v1 test. |
| `third_party_hpatchz/` | Verbatim move of binary + LICENSE + README (already at this path under v1 hoyoverse package). |

Parent `hoyoverse` package's existing callers (`update_patch.go`) switch import from `internal/providers/hoyoverse` self-reference to `internal/providers/hoyoverse/hpatchz`. Plan-writing covers the 3–4 call-site updates.

### `internal/providers/hoyoverse/sophon/` — **new sub-package**

| File | Responsibility |
|---|---|
| `proto/sophon_manifest.proto` | Verbatim from Collapse `Hi3Helper.Sophon/Protos/SophonManifestProto.proto`. |
| `proto/sophon_patch.proto` | Verbatim from Collapse `Hi3Helper.Sophon/Protos/SophonPatchProto.proto`. |
| `proto/sophon_manifest.pb.go` | `protoc-gen-go` output. **Committed** (fresh checkout works without `protoc`). |
| `proto/sophon_patch.pb.go` | Same. |
| `proto/tools.go` | `//go:build tools` + import `google.golang.org/protobuf/cmd/protoc-gen-go` so `go install` picks it up for regen. |
| `proto/gen.go` | `//go:generate protoc --go_out=. *.proto`. Used only during proto file changes. |
| `branches.go` | `branchInfo`, `branchSlot`, `branchCategory` types + JSON shapes matching the extended `getGameBranches` response (snake_case `pre_download`). |
| `infos.go` | `BuildResponse`, `PatchResponse`, `ManifestIdentity`, `ChunkDownloadInfo`, `ManifestDownloadInfo` types matching `getBuild` / `getPatchBuild` JSON envelopes. Custom JSON helpers: `boolish` (accepts `0|1|"0"|"1"|true|false` for `compression`/`encryption`) AND `int64ish` (accepts string-or-number for `compressed_size`/`uncompressed_size`/`chunk_size` — Collapse uses `JsonNumberHandling.AllowReadingFromString` for the same reason). |
| `infos_test.go` | JSON round-trips against fixtures. |
| `manifest_fetch.go` | `FetchBuildManifest(ctx, http, branch, category, target_tag) (*pb.SophonManifestProto, error)`. GET `manifest_download.url_prefix + '/' + id` → if `compression==1` wrap in `zstd.NewReader` → `proto.Unmarshal`. Sister `FetchPatchManifest`. **Manifest `checksum` field is NOT verified** — Collapse parity (Collapse's `ReadProtoFromManifestInfo` likewise omits it). Defensive telemetry: log the manifest's declared `checksum` alongside `proto.Unmarshal` success so debugging can correlate. |
| `manifest_fetch_test.go` | Against canned zstd-protobuf bytes. |
| `dedup.go` | `BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto, assetName string) map[string]ChunkRef` where key = decompressed-MD5-hex, value = `ChunkRef{OldFilePath string; OldOffset int64}`. **NO size field** — chunk size always comes from the *new* manifest's `ChunkSizeDecompressed` (which equals the old chunk's size by virtue of the MD5 match). Per-asset scope, mirroring Collapse `SophonUpdate.GetChunkOldOffsetFromOld`. |
| `dedup_test.go` | Per-asset isolation (chunk in asset A invisible from B query); MD5 key collision intra-asset; empty old manifest. |
| `decision.go` | `DecidePath(branch, currentLocal, oldManifest) (flavor, reason)`. `BuildChunkSources(newManifest, oldManifest, gameDir) []ChunkSource`. `BuildPatchInstructions(patchProto, mainProto, currentLocal) (patches []PatchInstr, fallthrough []ChunkSource, deletes []DeleteInstr)` — accepts BOTH proto types; returns 3 slices for the hybrid Patch+Main case (Patch/CopyOver instructions from patch; main-fall-through chunks for files not in patch; deletes from `UnusedAssets`). |
| `decision_test.go` | Decision-tree exhaustive cases; patch+main merge cases. |
| `chunk_download.go` | Single-chunk fetch: GET `chunk_download.url_prefix + '/' + chunkName` → wrap body in `ctxReader` for cancel + `zstd.NewReader` (if compressed) → write to staging → verify xxh64/MD5. Retry budget (3× exponential 1s/4s/16s). |
| `chunk_download_test.go` | httptest CDN; compressed + raw; verify-fail-redownload; retry budget exhausted. |
| `local_chunk_read.go` | Read chunk from on-disk old-version file at `(file, offset, size)`, verify MD5 against expected; on mismatch, return `ErrChunkStale` so caller falls back to CDN. |
| `local_chunk_read_test.go` | Match path; stale fall-through; ENOENT old-file fallthrough. |
| `file_assemble.go` | Given `AssetProperty + []ChunkSource`, build target file: `MkdirAll(filepath.Dir(out))`, open `*.tmp`, `Truncate(asset.AssetSize)`, per chunk `WriteAt(decompressed_bytes, ChunkOnFileOffset)`, fsync, whole-file MD5 verify, atomic rename. |
| `file_assemble_test.go` | 3-chunk file; chunk-out-of-order writes; partial reconstruction resume; nested-path MkdirAll. |
| `hdiff_apply.go` | `HDiffApply(ctx, opts) error` where `opts.Run hpatchz.RunFunc` is injected by parent (avoids cycle); dispatch `PatchInstr{Method=Patch|CopyOver|DownloadOver}`. `Patch` calls `opts.Run(ctx, oldPath, hdiffInputPath, outPath)`. `CopyOver` renames slice file. `DownloadOver` is a no-op (handled in download phase). All paths do `MkdirAll(filepath.Dir(out))` before assemble. |
| `hdiff_apply_test.go` | Three method branches; cross-device errno during rename; mock Run callback. |

### `internal/providers/hoyoverse/` — **new files**

| File | Responsibility |
|---|---|
| `update_sophon_plan.go` | `buildSophonPlan(ctx, http, branch, gid, currentLocal, audioLangs, gameDir, tempRoot, oldManifests) (*genshinPlan, predlAvail bool, error)`. Orchestrates `sophon.DecidePath` + manifest fetches + `BuildChunkSources` / `BuildPatchInstructions` per category. Patch flow ALSO captures `genshinPlan.sophonPatchAssetsFromMain map[assetPath][]chunkSource` — per-asset chunk source plan from the main manifest, used by apply phase to demote `hdiff_patch` records to `chunk_assemble` on pre-apply OldFile MD5 mismatch (§6.4). Also handles `detectPredlConsume` (see §3.6). |
| `update_sophon_plan_test.go` | Decision wiring; httptest end-to-end; predl-consume detection. |
| `update_sophon_download.go` | Sophon-aware 4-worker pool: each job = chunk download (CDN or local read) or patch blob download. Uses `sophon.ChunkDownload` / `sophon.LocalChunkRead`. Progress tracked via `sophonProgressStore.ChunksDone[xxh64]=true` per success → enables crash resume. Duplicate-chunk requests are tolerated (no inflight map). |
| `update_sophon_download_test.go` | Concurrency, cancel, progress accounting, crash resume from partial ChunksDone, duplicate-chunk race tolerated. |
| `update_sophon_apply.go` | Per-category, per-file dispatch on record `Kind`: `chunk_assemble` → `sophon.FileAssemble`; `hdiff_patch` → `sophon.HDiffApply` (passes `hpatchz.Run` as the injected callback); `copy_over` → atomic rename; `delete` → `os.Remove`. WAL append per record via `sophon_apply.wal`. Apply lock + ordering: `game` first, then audio langs alphabetical. WAL batched-rewrite policy (§6.1). |
| `update_sophon_apply_test.go` | Per-flavor flows; WAL resume; cross-device errno path; batched-rewrite cadence. |
| `sophon_apply_wal.go` | New sidecar at `<versionSidecarDir>/sophon_apply.wal`. Schema in §6.1. Atomic write pattern. |
| `sophon_apply_wal_test.go` | Read/write/round-trip; resume-from-mid-flight; batched rewrite. |
| `sophon_progress.go` | New sidecar `sophonProgressFile` at `<versionSidecarDir>/sophon_progress.json`. Schema: `{GameID, Version, BranchKind, BuildID, Stage, ChunksDone map[ChunkName]bool, PatchesDone map[PatchName]bool}` — both keys are the full CDN filenames from the manifest. v1's `core.ProgressFile` and `progress.json` untouched. |
| `sophon_progress_test.go` | Save/load round-trip; partial ChunksDone resume; corrupt-file recovery. |
| `sophon_manifest_cache.go` | On-disk manifest sidecar storage: `SaveAppliedManifest(gid, category, buildID, version, raw_pb_zst_bytes)`, `LoadAppliedManifests(gid) → *appliedSet`, `RotateAfterApply(gid, newBuildIDs)` — keeps latest + previous, GCs older. Atomic JSON `applied.json` index + raw `.pb.zst` blob files. Plus `cleanupStaleSophonSidecars(tempRoot, gid, currentTag, allowedTargets []string)` — sweeps ALL version-scoped dirs under `gameSidecarDir`: (a) version dirs with an all-`done` `sophon_apply.wal` matching `currentTag` (rotate-vs-cleanup crash window per §3 / §6.2); (b) version dirs for versions NOT in `allowedTargets = [currentTag, branch.PreDownload.Tag]` AND older than 7 days by mtime (orphan staging from abandoned earlier runs). Tens-of-GB-leak safety. |
| `sophon_manifest_cache_test.go` | Save/load round-trip; rotation correctness; cross-build-id GC; concurrent reads safe. |
| `testdata/sophon/branches_main_only.json` | Sanitized `getGameBranches` response, `main` only (no predl). |
| `testdata/sophon/branches_with_predl.json` | Same + `pre_download` populated. |
| `testdata/sophon/build_small.json` | Sanitized `getBuild` envelope, 5 files × 3 chunks per category. |
| `testdata/sophon/build_tiny.json` | Single-file single-chunk fixture for isolation testing. |
| `testdata/sophon/patch_small.json` | Sanitized `getPatchBuild`, same files-shape. |
| `testdata/sophon/sample.manifest.pb.zst` | 5-file × 3-chunk synthetic manifest matching `build_small.json` IDs. |
| `testdata/sophon/sample.patch.pb.zst` | 3-file × 1-patch synthetic matching `patch_small.json`. |
| `testdata/sophon/tiny.manifest.pb.zst` | 1-file × 1-chunk fixture matching `build_tiny.json`. |
| `testdata/sophon/chunks/*` | Synthetic chunk blobs (zstd-compressed + raw variants). |

### v1 files **unchanged but bypassed** for Sophon games (still active for HSR/ZZZ)

| File | v1 use | v2 Sophon path |
|---|---|---|
| `update_manifest.go` | `getGamePackages` → buildPlan zip+hdiff | NOT called for `UsesSophon=true` games. HSR/ZZZ unchanged. |
| `update_download.go` | Zip blob 4-worker pool | NOT called for Sophon. HSR/ZZZ unchanged. |
| `update_patch.go` | Zip extract + hdifffiles/hdiffmap parse | NOT called for Sophon. HSR/ZZZ unchanged. Imports updated to `hpatchz` sub-package. |
| `update_apply.go` | `apply.wal` flat-list | NOT used by Sophon. HSR/ZZZ unchanged. |
| `update_progress.go` | `progress.json` (`core.ProgressFile`, per-file) | NOT used by Sophon (Sophon uses `sophon_progress.json`). HSR/ZZZ unchanged. |

### Sidecar tree (v2 additions per game)

Paths layered on top of v1's actual layout: `gameSidecarDir = filepath.Join(tempRoot, flatGameID(gid))` and `versionSidecarDir = filepath.Join(tempRoot, flatGameID(gid), version)`.

```
<tempRoot>/<flat_gid>/                                # gameSidecarDir (v1)
  last_apply_target.json                              # v1, untouched
  .sophon/                                            # NEW v2 cross-version sidecars (dot-prefix avoids App scanForRecoveryRoot misclassifying as a version dir)
    manifests/
      <build_id>__<category>.manifest.pb.zst          # raw wire form, one per (build_id, category)
    applied.json                                      # { latest: {buildID, version, categories: {...}}, previous: {...} }

<tempRoot>/<flat_gid>/<target_version>/               # versionSidecarDir (v1)
  progress.json                                       # v1, untouched (HSR/ZZZ only)
  apply.wal                                           # v1, untouched (HSR/ZZZ only)
  predl_ready.json                                    # v1 filename; v2 extends schema for Sophon predl (see §7)
  sophon_progress.json                                # NEW v2 chunk-level download progress
  sophon_apply.wal                                    # NEW v2 typed-record WAL for Sophon apply
  staging/
    main/<target_build_id>/                           # NEW v2; branch-split to avoid build_id collisions
      chunks/<ChunkName>                            # verified chunks awaiting assemble
      patches/<PatchName>                        # patch blobs awaiting HDiff apply
      hdiff_inputs/<PatchName>_<offset>.bin      # extracted patch blob slices for hpatchz
      assembled/<file_relpath>.tmp                    # being-assembled output files
    predl/<predl_build_id>/                           # NEW v2; predl staging (no collision risk vs main)
      (same subdirs as main)
```

`tempRoot` defaults to `<TEMP>/omnigate/hoyoverse` (set by `App.constructProviders` via `SetTempRootFn`).

---

## §2. Protocol layer

### §2.1 New endpoints

| Endpoint | Use | Host |
|---|---|---|
| `getBuild` | Full build manifest (one per category) | `sg-public-api.hoyoverse.com/downloader/sophon_chunk/api` |
| `getPatchBuild` | HDiff patch manifest (one per category) | same host |

Query parameters (both):
```
plat_app={per-game}&branch={main|predownload}&password={branch_password}&package_id={branch_package_id}&tag={target_version}
```

`plat_app` baked in `meta.go`. `branch`/`password`/`package_id` come from extended `getGameBranches`. `tag` is `branch.Main.Tag` for update or `branch.PreDownload.Tag` for predl.

### §2.2 `getGameBranches` response extension

v1 only parsed `main.tag`. v2 parses the full shape based on the live-API capture in `memory/project_m3b_v2_sophon.md` (Collapse's `HypGameInfoBranchData` lacks `categories`; HoYoverse added it after Collapse last synced, and the symmetry implies `pre_download` also carries `categories` though that hasn't been live-captured):

```go
type branchInfo struct {
    Main        branchSlot
    PreDownload branchSlot  // may be empty between releases — caller must check
}

type branchSlot struct {
    PackageID  string
    Branch     string             // "main" or "predownload"
    Password   string             // CDN URL signing token (NOT decryption key)
    Tag        string
    DiffTags   []string
    Categories []branchCategory
}

type branchCategory struct {
    CategoryID    string  // "10016" game, "10017"–"10020" audio langs
    MatchingField string  // "game" | "zh-cn" | "en-us" | "ko-kr" | "ja-jp"
    Type          string  // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
}

func (s branchSlot) IsEmpty() bool { return s.PackageID == "" }
```

If `branch.Main.Categories` is empty when `branch.Main.IsEmpty() == false` we treat the response as malformed → `sophon_manifest_fetch_failed`. For `PreDownload`, the same rule **does NOT apply** — `categories` symmetry on pre_download was hypothesized in round 1 but not live-confirmed; if HoYoverse omits `categories` on pre_download, treat it as "game category only, no audio in predl" and continue (audio packs are then updated only when the live update happens). Defensive over strict.

### §2.3 `getBuild` / `getPatchBuild` response shape (canonical)

```json
{
  "retcode": 0,
  "message": "",
  "data": {
    "build_id": "...",
    "tag": "6.6.0",
    "patch_id": "...",
    "manifests": [
      {
        "category_id": "10016",
        "category_name": "...",
        "matching_field": "game",
        "manifest": { "id": "...", "checksum": "...", "compressed_size": 12345, "uncompressed_size": 67890 },
        "manifest_download": { "url_prefix": "https://...", "url_suffix": "", "password": "<cdn-signing-token>", "encryption": 0, "compression": 1 },
        "chunk_download":    { "url_prefix": "https://...", "url_suffix": "", "password": "<cdn-signing-token>", "encryption": 0, "compression": 1 },
        "diff_download":     { ... },
        "stats": { ... }
      }
    ]
  }
}
```

**`compression` / `encryption` value parsing**: Collapse uses a `BoolConverter` because HoYoverse may serialize these as `0|1|"0"|"1"|true|false`. `infos.go` Go types use a custom `boolish` type that accepts all forms.

**`encryption`** is defensive telemetry: if observed non-zero (currently always zero per Collapse + live capture), log a warning AND if any `password` value is non-empty also log so we get an early signal of a HoYoverse encryption-rollout.

**`build_id` collision policy** (LOCKED): staging dirs are split by branch — `staging/main/<build_id>/` vs `staging/predl/<build_id>/` — so collision risk is eliminated structurally regardless of HoYoverse's per-build_id uniqueness guarantees.

### §2.4 Protobuf schemas (verbatim from Collapse `Hi3Helper.Sophon/Protos/`)

**`sophon_manifest.proto`:**
```proto
syntax = "proto3";

package Hi3Helper.Sophon.Protos;
option go_package = "omnigate/internal/providers/hoyoverse/sophon/proto";

message SophonManifestProto {
  repeated SophonManifestAssetProperty Assets = 1;
}

message SophonManifestAssetProperty {
           string                   AssetName    = 1;
  repeated SophonManifestAssetChunk AssetChunks  = 2;
           int32                    AssetType    = 3;
           int64                    AssetSize    = 4;
           string                   AssetHashMd5 = 5;
}

message SophonManifestAssetChunk {
  string ChunkName                = 1;
  string ChunkDecompressedHashMd5 = 2;
  int64  ChunkOnFileOffset        = 3;
  int64  ChunkSize                = 4;
  int64  ChunkSizeDecompressed    = 5;
}
```

**`sophon_patch.proto`:**
```proto
syntax = "proto3";

package Hi3Helper.Sophon.Protos;
option go_package = "omnigate/internal/providers/hoyoverse/sophon/proto";

message SophonPatchProto {
  repeated SophonPatchAssetProperty  PatchAssets  = 1;
  repeated SophonUnusedAssetProperty UnusedAssets = 2;
}

message SophonPatchAssetProperty {
           string               AssetName    = 1;
           int64                AssetSize    = 2;
           string               AssetHashMd5 = 3;
  repeated SophonPatchAssetInfo AssetInfos   = 4;
}

message SophonPatchAssetInfo {
  string                VersionTag = 1;
  SophonPatchAssetChunk Chunk      = 2;
}

message SophonPatchAssetChunk {
  string PatchName          = 1;
  string VersionTag         = 2;
  string BuildId            = 3;
  int64  PatchSize          = 4;
  string PatchMd5           = 5;
  int64  PatchOffset        = 6;
  int64  PatchLength        = 7;
  string OriginalFileName   = 8;
  int64  OriginalFileLength = 9;
  string OriginalFileMd5    = 10;
}

message SophonUnusedAssetProperty {
           string                VersionTag = 1;
  repeated SophonUnusedAssetInfo AssetInfos = 2;
}

message SophonUnusedAssetInfo {
  repeated SophonUnusedAssetFile Assets = 1;
}

message SophonUnusedAssetFile {
  string FileName = 1;
  int64  FileSize = 2;
  string FileMd5  = 3;
}
```

`SophonUnusedAssetProperty` lists files to delete as part of the patch (game files removed in the new version). The apply phase emits `delete` WAL records for these.

### §2.5 Dependencies

New `go.mod` additions:
- `google.golang.org/protobuf` (latest stable)
- `github.com/klauspost/compress` (zstd subpackage; pure Go; CGO_ENABLED=0 friendly)
- `github.com/cespare/xxhash/v2`

Build prerequisites:
- `protoc` (system binary) — only required when regenerating `.pb.go` from `.proto` changes
- `protoc-gen-go` — installed via `cd internal/providers/hoyoverse/sophon/proto && go install google.golang.org/protobuf/cmd/protoc-gen-go` (driven by `tools.go` build-tag pattern)
- **Fresh checkout requires no `protoc`** because `.pb.go` files are committed
- README dev-setup section added with these notes; CI adds a `go generate ./internal/providers/hoyoverse/sophon/proto && git diff --exit-code` check (only when `protoc` available on the runner) to detect `.pb.go` drift from `.proto`

`wails build` does NOT trigger `go generate`; it's a one-time developer action on proto changes.

---

## §3. Decision tree — `CheckForUpdate` for Sophon games

```
g := findByID(gid)
if g == nil || !g.UsesSophon → fall back to v1 legacy flow

branch, err := fetchBranchInfo(ctx, g.APIGameID)
if err → UpdateError{Code:"sophon_manifest_fetch_failed", Retryable:true}
if branch.Main.IsEmpty() → UpdateError{"sophon_manifest_fetch_failed", Retryable:true}
if len(branch.Main.Categories) == 0 → UpdateError{"sophon_manifest_fetch_failed", Retryable:true}

currentLocal := ReadGameVersion(gameDir)
if currentLocal == "" → UpdateError{"sophon_no_install", Retryable:false}

# Self-heal: applied chunks but config.ini writeback failed (v1 pattern)
if healed := maybeSelfHealSophon(currentLocal, branch.Main.Tag, gameDir, tempRoot, gid):
    cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
    plan := idle plan (Reason: ReasonUnspecified, Version: branch.Main.Tag)
    manifestCache.put(gid, &genshinPlan{flavor: flavorNone, ...})
    return plan

if currentLocal == branch.Main.Tag:
    cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
    return idle plan (Reason: ReasonUnspecified, Files: [])

# Predl-consume detection (§3.6). Runs BEFORE main decision so we can short-circuit
# the manifest fetches if predl already staged the same target.
if consume, snap := detectPredlConsume(tempRoot, gid, currentLocal, branch.Main.Tag):
    plan := genshinPlan{
        flavor: flavorFromSnapshot(snap),  // sophon_patch | sophon_build
        predlConsume: true, predlSnapshot: snap,
        sophonBranch: branch, sophonBuildID: snap.BuildID,
        // sophonChunkSources / sophonPatches hydrated by §7.3 step 1 at RunUpdate
    }
    return plan.UpdatePlan with Reason: ReasonResumeInterrupted

audioFolders, _ := DetectInstalledLanguages(gameDir)  // returns folder names ("Chinese", "English(US)", ...)
audioLangs := mapFoldersToMatchingFields(audioFolders)  // reuse v1 folderToAudioLang map → ("zh-cn", "en-us", ...)

# Old-manifest lookup for chunk-from-disk dedup (Path B)
prev := LoadAppliedManifests(tempRoot, gid)
oldMainManifest := prev.MatchByVersion(currentLocal, "game")

case currentLocal ∈ branch.Main.DiffTags:
    flavor = flavorSophonPatch
    plan = buildSophonPatchPlan(branch.Main, audioLangs, currentLocal, prev, gameDir, tempRoot, gid)
case oldMainManifest != nil:
    flavor = flavorSophonBuild
    plan = buildSophonBuildPlan(branch.Main, audioLangs, prev, gameDir, tempRoot, gid)
default:
    flavor = flavorSophonFull
    plan = buildSophonBuildPlan(branch.Main, audioLangs, nil, gameDir, tempRoot, gid)
```

### §3.1 `buildSophonPatchPlan` — patch + main merge (Collapse-faithful iteration)

Per `category ∈ {"game"} ∪ audioLangs`:
1. Fetch `getPatchBuild` → `SophonPatchProto` for category
2. Fetch `getBuild` → `SophonManifestProto` for **same** category (needed for fall-through)
3. Build `patchDict map[string]*SophonPatchAssetInfo` keyed by `pa.AssetName`, value = the `info ∈ pa.AssetInfos` with `info.VersionTag == currentLocal` (skip patch assets that have no matching VersionTag entry — they have no work for this source version)
4. **Iterate `mainProto.Assets`** (`AssetType==0`, files only) — Collapse-faithful order; main manifest is source of truth for "what exists in the new build":
    - If `info, ok := patchDict[ma.AssetName]`:
        - `info.Chunk.OriginalFileName == ""` (CopyOver — full file delivered as patch blob slice): emit `PatchInstr{Method: CopyOver, PatchName, PatchOffset, PatchLength, target: ma.AssetName, expectMD5: ma.AssetHashMd5}` → APPEND to `genshinPlan.sophonPatches`. Also build per-asset chunk source plan from main (`buildAssetChunkSources(ma, oldMainManifest, gameDir)`) and store in `genshinPlan.sophonPatchAssetsFromMain[ma.AssetName]` for §6.4 demotion fallback.
        - `info.Chunk.OriginalFileName != ""` (HDiff): emit `PatchInstr{Method: Patch, PatchName, PatchOffset, PatchLength, oldPath: info.Chunk.OriginalFileName, target: ma.AssetName, expectMD5: ma.AssetHashMd5, originalFileMD5: info.Chunk.OriginalFileMd5}` → APPEND to `genshinPlan.sophonPatches`. Also store `genshinPlan.sophonPatchAssetsFromMain[ma.AssetName]` as above (so pre-apply MD5 mismatch can demote to chunk_assemble via main manifest).
    - Else (no patch entry for this asset — newly added file or unchanged file): emit `chunk_assemble` using `buildAssetChunkSources(ma, oldMainManifest, gameDir)` (DownloadOver semantics with Path B dedup) → APPEND to `genshinPlan.sophonChunkSources`
5. For each `pa ∈ patchProto.PatchAssets` whose `AssetName` is NOT in `mainProto.Assets` name set: log warn `patch references non-main asset {name}; skipping`. (Defensive — should never happen with well-formed HoYoverse manifests.)
6. For each `ua ∈ patchProto.UnusedAssets` where `ua.VersionTag == currentLocal`, for each `file ∈ ua.AssetInfos[].Assets`: emit `DeleteInstr{path: FileName, expectMD5: FileMd5}` → APPEND to `genshinPlan.sophonDeletes`

Total bytes: Σ unique `info.Chunk.PatchLength` (dedup by `PatchName` to avoid double-counting shared patch blobs) + Σ `chunk.ChunkSize` for CDN chunks in main-fall-through. Pre-build a `name→index` map over `mainProto.Assets` to keep step 4 O(N) rather than O(N²); manifest sizes can reach ~50K files.

Implementation note (Collapse divergence): we iterate main and look up patch, where Collapse iterates main and looks up patch via the same join. Order is identical; only the data structure that holds the lookup key is at our discretion.

### §3.2 `buildSophonBuildPlan` — full + chunk-from-disk dedup

Per `category ∈ {"game"} ∪ audioLangs`:
1. Fetch `getBuild` → `SophonManifestProto` for category
2. If `oldManifests != nil`, look up matching old per-category manifest (by `category_id` or `matching_field`); else skip dedup
3. For each `newAsset ∈ newProto.Assets` (`AssetType==0`, files):
    - Build `oldAssetMD5Idx` via `sophon.BuildPerAssetMD5Index(oldManifest, newAsset.AssetName)` — empty map if no matching old asset
    - For each `chunk ∈ newAsset.AssetChunks`:
        - Lookup `chunk.ChunkDecompressedHashMd5` in `oldAssetMD5Idx`
        - Hit: emit `ChunkSource{kind=Local, asset: newAsset.AssetName, oldFile: match.OldFilePath, oldOffset: match.OldOffset, decompressedSize: chunk.ChunkSizeDecompressed, expectMD5: chunk.ChunkDecompressedHashMd5}` → APPEND to `genshinPlan.sophonChunkSources`. Size sourced from *new* manifest (guaranteed equal to old via MD5 match).
        - Miss: emit `ChunkSource{kind=CDN, urlPrefix, chunkName, decompressedSize, compressedSize, expectMD5}` → APPEND to `genshinPlan.sophonChunkSources`

Total bytes: Σ `chunk.ChunkSize` for CDN chunks (compressed wire bytes; user-perceived download).

### §3.3 Predownload detection

```
predlAvail := !branch.PreDownload.IsEmpty()
           && currentLocal != branch.PreDownload.Tag
           && currentLocal != ""

if predlAvail:
    if currentLocal ∈ branch.PreDownload.DiffTags:
        predlFlavor = flavorSophonPredlPatch
    elif oldMainManifest != nil:
        predlFlavor = flavorSophonPredlBuild
    else:
        # No chunk reuse possible — full predl would be tens of GB blind.
        # We never offer this; user committed to update scope, not full-install scope.
        predlAvail = false

if predlAvail:
    build the parallel predl plan (sophonChunkSources / sophonPatches / sophonDeletes)
    on branch.PreDownload, cache on genshinPlan.predlPlan
```

UI's predl button → `Provider.RunUpdate` with `plan.Kind = PlanPredownload`.

### §3.4 Reason codes

Reuses v1's `core.ReasonCode` enum + new `ReasonResumeInterrupted`:
- `ReasonVersionChanged` — currentLocal != branch.Main.Tag, normal flavor chosen
- `ReasonAudioPackAdded` — DetectInstalledLanguages diff vs prior (v1 covers)
- `ReasonVersionAndAudio` — both above
- `ReasonPredownload` — `plan.Kind == PlanPredownload`
- `ReasonResumeInterrupted` (**NEW**) — `predlConsume == true` OR sophon_apply.wal / non-empty sophon_progress.json present
- `ReasonUnspecified` — idle plan, self-heal succeeded

Runtime errors surface via `core.UpdateError.Code` (see §6.7), NOT via `ReasonCode`.

### §3.5 `maybeSelfHealSophon`

Mirrors v1's `maybeSelfHeal` exactly, **including the clock-skew handling** at `hoyoverse.go:470-483` (negative delta within 24h → return false; negative delta > 24h → delete the lat sidecar and return false). Plan-writing task does side-by-side diff against v1 to verify parity. On heal-success, ALSO call `manifestCache.put(gid, &genshinPlan{flavor: flavorNone, UpdatePlan: idle})` so a subsequent App.GetPredownloadAvailable / RunUpdate call doesn't see a stale non-idle plan.

```
lat := loadJSONSidecar[lastApplyTarget](latPath)
if lat == nil → return false
if lat.TargetVersion != branch.Main.Tag → return false
if currentLocal == branch.Main.Tag → return false   // already healed
if !lat.LastWritebackRetryTS.IsZero() && time.Since(lat.LastWritebackRetryTS) < 24h → return false
err := WriteGameVersion(gameDir, branch.Main.Tag)
lat.LastWritebackRetryTS = time.Now().UTC()
lat.ConfigWritebackOK = (err == nil)
writeLastApplyTarget(tempRoot, gid, lat)
return err == nil
```

### §3.6 `detectPredlConsume`

Read `versionSidecarDir(tempRoot, gid, branch.Main.Tag)/predl_ready.json`:
- ENOENT → return `(false, nil)`
- Parse `sophonPredlReadyFile`; if parse fail → delete file, return `(false, nil)`
- If `predl.Kind ∉ {"sophon_patch", "sophon_build"}` (e.g. zero-value from a legacy v1 HSR/ZZZ-shaped `predl_ready.json` that lacks the field, or a future kind we don't know): treat as stale, delete sidecar + staging tree, return `(false, nil)`
- If `predl.TargetVersion != branch.Main.Tag` → mismatch (stale); emit `predl_stale` warn-log, delete sidecar + staging tree at `staging/predl/<predl.BuildID>/`, return `(false, nil)`
- If `predl.SourceVersion != currentLocal` → user updated via HoYoPlay between predl and now; same cleanup, return `(false, nil)`
- If `predl.Kind == "sophon_patch"` and `currentLocal ∉ branch.Main.DiffTags` → predl was patch-based but diff window no longer covers user; same cleanup, return `(false, nil)`
- Otherwise → return `(true, &predl)`. Caller sets `genshinPlan.predlConsume=true`, `genshinPlan.predlSnapshot=&predl.PlanSnapshot`, flavor from `predl.Kind`

Cleanup ownership: `detectPredlConsume` ALWAYS performs sidecar + staging cleanup on stale; `CheckForUpdate` does not need to call it explicitly.

---

## §4. Manifest cache + reuse layer

### §4.1 Storage layout

```
<tempRoot>/<flat_gid>/.sophon/
  manifests/
    <build_id>__game.manifest.pb.zst
    <build_id>__en-us.manifest.pb.zst
    <build_id>__zh-cn.manifest.pb.zst
    ...
  applied.json
```

`applied.json`:
```json
{
  "latest":   { "build_id": "...", "version": "6.6.0", "applied_at": "2026-06-01T12:00:00Z",
                "categories": { "game": "<build_id>", "en-us": "<build_id>" } },
  "previous": { "build_id": "...", "version": "6.5.0", "applied_at": "2026-05-15T11:00:00Z",
                "categories": { "game": "<build_id>", "en-us": "<build_id>" } }
}
```

### §4.2 Lookup semantics

`LoadAppliedManifests(tempRoot, gid) *appliedSet`:
- Read `applied.json`. If absent → return `nil`
- For each `(category, build_id)` in latest + previous, lazy-load `.manifest.pb.zst` on demand via `MatchByVersion(version, category)`
- `MatchByVersion` returns a parsed `*pb.SophonManifestProto` or nil if no slot matches version+category

The proto is NOT held in memory across `CheckForUpdate` calls; loaded fresh, discarded after planning.

### §4.3 Cache rotation (write-order corrected)

After apply for build B (target version V) succeeds:
1. **Capture** `evictedBuildID := applied.previous.build_id` and `evictedCategoryIDs := applied.previous.categories` (or empty if no previous)
2. Build new in-memory `applied`:
    - `latest` = `{B, V, now, newCategoryIDs}`
    - `previous` = (old `latest`, if any; else null)
3. Atomic write new `applied.json` (write `.tmp` → fsync → rename)
4. If `evictedBuildID != ""`: for each `(_, bid) ∈ evictedCategoryIDs`, delete `manifests/<bid>__*.manifest.pb.zst` (any per-category file keyed by that build_id)
5. Additionally, walk `manifests/` and delete any `.manifest.pb.zst` whose build_id is NOT in the new `applied.latest.categories.values()` ∪ `applied.previous.categories.values()` (sweeps audio-only manifests that lingered because their language was no longer installed)

Result: at most 2 prior builds × N categories on disk. Storage bound ~10–40 MB.

### §4.4 Stale entry recovery (per-chunk MD5 mismatch)

When `local_chunk_read.go` reads at `(oldFile, oldOffset, size)` and computed MD5 disagrees with the manifest's expected MD5:
1. Return `ErrChunkStale`
2. Worker pool re-classifies the chunk source as CDN
3. Log warning with `file`, `offset`, expected/actual MD5

No persistent state change. Stale-detection is per-chunk-read; the old manifest stays valid for OTHER chunks.

---

## §5. Download layer

### §5.1 Worker pool

Same 4-worker pool shape as M3.B v1 `update_download.go`. Job queue items:

```go
type sophonJob struct {
    kind     sophonJobKind  // jobChunkCDN | jobChunkLocal | jobPatchBlob
    category string         // "game" | "en-us" | ...
    out      string         // staging path (deterministic, keyed by content hash)
    cdn      *cdnChunk      // jobChunkCDN
    local    *localChunk    // jobChunkLocal
    patch    *patchBlob     // jobPatchBlob
    onDone   func(err error)
}
```

Output paths (under `<versionSidecarDir>/staging/<branchKind>/<target_build_id>/`):
- `jobChunkCDN` / `jobChunkLocal` → `chunks/<ChunkName>` (deterministic; same chunk requested by different files writes the same file once)
- `jobPatchBlob` → `patches/<PatchName>`

Worker dispatches on `kind`:
- `jobChunkCDN` → `sophon.ChunkDownload` (HTTP GET + `ctxReader`-wrapped + zstd decompress if compressed + xxh64 verify on write + atomic rename to `out`). If `out` already exists and verifies: skip.
- `jobChunkLocal` → `sophon.LocalChunkRead` (open `local.oldFile`, seek `local.oldOffset`, read `local.size`, MD5 verify against expected, write to `out`; `ErrChunkStale` → requeue as `jobChunkCDN`)
- `jobPatchBlob` → HTTP GET full patch blob → MD5 verify → atomic rename. If `out` exists and verifies: skip.

**Same-chunk worker race**: when two assets share a chunk and both get scheduled concurrently, both workers may download the same chunk to the same `out` path. This is tolerated: atomic rename + xxh64 verification make the result byte-identical (last-writer-wins on rename, but the content is the same). No inflight singleflight map is needed; the wasted bandwidth is bounded (a chunk appears in at most a handful of assets in practice).

### §5.2 Per-chunk retry + cross-run resume

Per `jobChunkCDN`: 3× exponential backoff (1s / 4s / 16s). After 3 failures, the chunk is left in `sophon_progress.ChunksDone` as absent and surfaces as `UpdateError{Code:"sophon_chunk_verify_failed", Retryable:true}`.

Cross-run resume:
1. `sophon_progress.json.ChunksDone[ChunkName]==true` → skip; the staging chunk file is known good
2. Staging chunk file exists but ChunksDone says absent → re-verify (xxh64 if `ChunkName` first-16-hex parses; else MD5) from disk; if pass, mark done and skip; if fail, delete and re-download
3. Neither → fresh job

Same logic for `PatchesDone[PatchName]`.

`local_chunk_read.go` reads are NOT cached in `sophon_progress`; they're always re-executed because cache-hit would just mean another disk read.

### §5.3 Progress accounting

`UpdateEvent.Current` = decompressed bytes written across all workers (atomic counter incremented per chunk-completion).
`UpdateEvent.Total` = Σ `chunk.ChunkSizeDecompressed` for CDN chunks + Σ `local.size` for local chunks + Σ `PatchLength` for patch blobs.

**Implementation note**: the bar represents "bytes assembled into staging", which includes disk-only reads (Path B local chunks). Intentional UX — bar advances even when no network traffic happens, mirroring HoYoPlay behavior. The spec trades "network throughput" semantics for "work-completed" semantics.

### §5.4 Cancel

`ctxReader` wraps any `io.Reader` and returns `ctx.Err()` on `Read` if the context is cancelled. Used to wrap both raw HTTP body AND zstd decompression streams (since zstd.NewReader takes an io.Reader). Worker pool also checks `ctx.Done()` between jobs.

---

## §6. Apply layer

### §6.1 `sophon_apply.wal` schema + batched rewrite

Sidecar at `<versionSidecarDir>/sophon_apply.wal`.

```go
type sophonApplyWAL struct {
    GameID    string              `json:"game_id"`
    TargetTag string              `json:"target_tag"`
    BuildID   string              `json:"build_id"`
    SourceTag string              `json:"source_tag"`     // "" for flavorSophonFull
    Flavor    string              `json:"flavor"`         // planFlavor.String()
    BranchKind string             `json:"branch_kind"`    // "main" — predl never reaches apply
    WasPredl  bool                `json:"was_predl"`      // true if originated from predl consume
    Records   []sophonApplyRecord `json:"records"`
}

type sophonApplyRecord struct {
    Kind      string `json:"kind"`        // "chunk_assemble" | "hdiff_patch" | "copy_over" | "delete"
    Category  string `json:"category"`
    Path      string `json:"path"`        // target relative to gameDir
    State     string `json:"state"`       // "pending" | "done"
    AssetMD5  string `json:"asset_md5,omitempty"`     // expected post-apply whole-file MD5 (assemble/hdiff/copy)
    OldPath   string `json:"old_path,omitempty"`      // hdiff_patch
    PatchTmp  string `json:"patch_tmp,omitempty"`     // hdiff_patch | copy_over (staging patches/<md5>)
    PatchOff  int64  `json:"patch_off,omitempty"`     // hdiff_patch | copy_over
    PatchLen  int64  `json:"patch_len,omitempty"`     // hdiff_patch | copy_over
    ExpectMD5 string `json:"expect_md5,omitempty"`    // delete (pre-delete sanity check)
}
```

Under flavorSophonPatch, the records list is a single ordered stream that interleaves Patch/CopyOver (from `sophonPatches`), DownloadOver `chunk_assemble` (from main-fall-through chunks in `sophonChunkSources`), and `delete` (from `sophonDeletes`). Under flavorSophonBuild / flavorSophonFull, the list is just `chunk_assemble` records.

**Batched rewrite policy** (controls WAL write cost — at 50K records each rewrite is ~5 MB):
- Per-record state transition updates an in-memory copy
- Rewrite to disk every **50 records** OR every **5 seconds** (whichever first)
- On apply completion or controlled shutdown (ctx cancel), flush
- On crash, lost records are at most the last 50 (or 5 s) of work → resume re-runs them; idempotent because `done` rename is a no-op against the already-renamed target (resume detects via target file's MD5)

### §6.2 Apply ordering

1. Acquire `applyLock`
2. Load `sophon_apply.wal`. If exists with non-empty `Records`, resume from first `pending`; else build records list from `genshinPlan.sophonChunkSources + sophonPatches + sophonDeletes` (interleaved in the same order built by §3.1/§3.2) and write fresh WAL
3. Order categories: `"game"` first; audio langs alphabetical
4. Per category, per record (in records-list order): execute → fsync → atomic rename → mark `done` in memory → maybe-flush WAL per batching policy
5. Final WAL flush
6. After all records done: `WriteGameVersion(gameDir, branch.Main.Tag)` + write `last_apply_target.json` (warn-log on permission error, set `ConfigWritebackOK=false`)
7. Rotate `sophon/applied.json` per §4.3
8. Delete:
    - `<versionSidecarDir>/staging/main/<build_id>/` contents
    - `<versionSidecarDir>/staging/predl/<predl_build_id>/` contents IF `WasPredl == true` (predl-consume happened — the predl staging was the source of truth for this apply, now done)
    - `<versionSidecarDir>/sophon_apply.wal`
    - `<versionSidecarDir>/sophon_progress.json`
    - `<versionSidecarDir>/predl_ready.json` IF `WasPredl == true` (it served its purpose)
9. Release `applyLock`

### §6.3 `chunk_assemble` record execution

For `Kind == "chunk_assemble"`:
1. `os.MkdirAll(filepath.Dir(out_tmp), 0o755)` where `out_tmp = <staging>/main/<build_id>/assembled/<Path>.tmp`
2. `out, _ := os.OpenFile(out_tmp, RDWR|CREATE|TRUNC, 0o644)`
3. `out.Truncate(<expected_file_size>)` (from manifest `AssetSize`)
4. For each chunk in `newAsset.AssetChunks`: read staging `<staging>/main/<build_id>/chunks/<ChunkName>` → `out.WriteAt(bytes, chunk.ChunkOnFileOffset)`
5. `out.Sync()`, `out.Close()`
6. Whole-file MD5 verify against `AssetMD5`. Mismatch → delete `out_tmp`, surface `UpdateError{"sophon_apply_failed", Retryable:false}`
7. `os.MkdirAll(filepath.Dir(<gameDir>/<Path>), 0o755)` (game files into nested dirs)
8. `safeAtomicRename(out_tmp, <gameDir>/<Path>)` (cross-device errno → copy+delete fallback)
9. Mark WAL `done`

### §6.4 `hdiff_patch` record execution

For `Kind == "hdiff_patch"`:
1. **Pre-apply OldFile MD5 verify**: stat `<gameDir>/<OldPath>`, compute MD5, compare against `originalFileMD5` (carried on the record from §3.1).
    - **Match** → proceed to step 2.
    - **Mismatch** → **synchronous demotion**:
        - Look up `sources := genshinPlan.sophonPatchAssetsFromMain[Path]` (built at §3.1 step 4). If not present (defensive — shouldn't happen because §3.1 always populates this for every patch record): surface `UpdateError{"sophon_apply_failed", Retryable:false}`.
        - **Synchronously enqueue chunk-download jobs** for each `chunkSource where kind==CDN` in `sources` that's not already in staging (verify via `ChunkName` filename + xxh64/MD5 re-check). Block this apply-worker until all enqueued jobs land in `staging/main/<build_id>/chunks/<ChunkName>`. Local-kind chunk sources need no pre-fetch; `chunk_assemble` reads them at execution time.
        - Rewrite this WAL record in-memory: `Kind = "chunk_assemble"`, `Path` unchanged, `AssetMD5` unchanged (target file MD5 is identical between patch and main views of the same file), drop hdiff-specific fields. Set a synthetic `assembleSources []chunkSource` reference on the record so step 6.3's executor reads from `sources` rather than re-deriving.
        - Persist WAL (flush per batched-rewrite cadence — demotion is a state transition that forces an early flush).
        - **Restart execution from this record** as `chunk_assemble` (§6.3).
2. `os.MkdirAll(filepath.Dir(hdiff_input), 0o755)` where `hdiff_input = <staging>/main/<build_id>/hdiff_inputs/<patchMD5>_<offset>.bin`
3. Write slice `PatchTmp[PatchOff:PatchOff+PatchLen]` to `hdiff_input`
4. `os.MkdirAll(filepath.Dir(out_tmp), 0o755)`
5. Invoke `sophon.HDiffApply(ctx, opts)` with `opts.Run = hpatchz.Run` injected by parent: `hpatchz.Run(ctx, <gameDir>/<OldPath>, hdiff_input, out_tmp)`
6. Whole-file MD5 verify against `AssetMD5`
7. `safeAtomicRename(out_tmp, <gameDir>/<Path>)`
8. Mark WAL `done`

Demotion atomicity: if process crashes between WAL rewrite (1.d) and chunk_assemble execution, §6.9 resume sees a `chunk_assemble` record whose required chunks may or may not be in staging. Resume-time behavior: §6.3 step 4 attempts to open each `staging/main/<build_id>/chunks/<ChunkName>`. ENOENT → re-enqueue chunk download (same path as live demotion), block, re-attempt. Hash-mismatch on stagged-but-pre-existing chunks → delete + redownload.

### §6.5 `copy_over` record execution

For `Kind == "copy_over"`:
1. `os.MkdirAll(filepath.Dir(out_tmp), 0o755)`
2. Write slice `PatchTmp[PatchOff:PatchOff+PatchLen]` to `out_tmp`
3. Whole-file MD5 verify against `AssetMD5`
4. `safeAtomicRename(out_tmp, <gameDir>/<Path>)`
5. Mark WAL `done`

### §6.6 `delete` record execution (UnusedAssets)

For `Kind == "delete"`:
1. Optional: stat `<gameDir>/<Path>`, compute MD5, compare against `ExpectMD5`. Mismatch → log warn (user may have modded), continue
2. `os.Remove(<gameDir>/<Path>)` (ignore ENOENT)
3. Mark WAL `done`

### §6.7 Runtime error codes (surfaced via `core.UpdateError.Code`)

| Code | Cause | Retryable |
|---|---|---|
| `sophon_no_install` | `currentLocal == ""` | false |
| `sophon_manifest_fetch_failed` | branch/manifest HTTP error | true |
| `sophon_chunk_verify_failed` | 3× retry exhausted on a chunk | true |
| `sophon_apply_failed` | whole-file MD5 mismatch or `hpatchz` non-zero exit | false |
| `predl_stale` | `predl_ready.json` mismatched (handled internally by §3.6 cleanup; surfaced as warn-log, not user-facing — fresh predl plan is built) | n/a |

### §6.8 Cross-device errno during rename

Reuses v1's `cross_device_windows.go` / `_other.go` errno detection. Sophon assemble + copy_over + hdiff_patch all go through `safeAtomicRename` which falls back to copy+delete on `EXDEV`.

### §6.9 Resume from crash

Two layers:

**Provider-internal dispatcher** (in `hoyoverse.go::RunUpdate`):
1. If `<versionSidecarDir>/sophon_apply.wal` exists AND `Records` non-empty → load → resume from first pending Sophon record (§6.2 step 2 covers)
2. Else if `<versionSidecarDir>/apply.wal` exists → v1 path (HSR/ZZZ)
3. Else if `<versionSidecarDir>/sophon_progress.json` exists with non-empty `ChunksDone` → resume download phase (skip chunks already done; re-fetch the rest)
4. Else if `<versionSidecarDir>/progress.json` has non-empty entries → v1 path
5. Else: fresh run from `manifestCache.get(gid)` (must have been planned by recent CheckForUpdate)

If `manifestCache` miss AND any progress sidecar exists, the dispatcher re-calls `CheckForUpdate` to rebuild plan — **but only when the dispatcher landed on path 3 (download-phase resume from `sophon_progress.json` without a WAL)**. For path 1 (sophon_apply.wal present), the WAL itself carries all required per-record state (paths, offsets, MD5s, staged blob references) so apply-phase resume needs no network and no manifestCache rebuild. If both miss, surface `UpdateError{"sophon_manifest_fetch_failed"}`.

**App-layer bell-drawer** (calls `core.ScanRecovery`):
- `core.ScanRecovery` is extended (§1 core/recovery.go) to also look for `sophon_apply.wal` and `sophon_progress.json`. Classification:
    - `sophon_apply.wal` → `RecoveryPhaseApplyResume`, `WasPredl` read from the Sophon WAL header
    - `sophon_progress.json` (no `sophon_apply.wal`) → `RecoveryPhaseDownloadResume`
    - Existing v1 sidecars retain their existing precedence (the dispatcher cleans up stale companions)
- Bell drawer prompt copy unchanged; the existing resume confirmation flow handles Sophon transparently because Provider.RunUpdate dispatches by sidecar

---

## §7. Predownload

### §7.1 Flow

1. `CheckForUpdate` sets `predlAvailable=true` on `genshinPlan` when `branch.PreDownload != empty && currentLocal != branch.PreDownload.Tag`
2. App.GetPredownloadAvailable → true → UI shows predl button
3. User clicks predl → App.StartPredownload → Provider.RunUpdate with `plan.Kind = PlanPredownload`
4. RunUpdate dispatches flavor as `flavorSophonPredlPatch` or `flavorSophonPredlBuild` (no Full predl)
5. Download phase runs identically; staging tree at `staging/predl/<predl_build_id>/`
6. **Apply phase is SKIPPED**. Instead, write `predl_ready.json` (extending v1's `predlReadyFile`):

```go
type sophonPredlReadyFile struct {
    core.ProgressFile                                 // v1 base (Entries map empty for Sophon)
    Kind            string             `json:"kind"`             // "sophon_patch" | "sophon_build"
    BuildID         string             `json:"build_id"`
    SourceVersion   string             `json:"source_version"`   // currentLocal at predl-stage time
    TargetVersion   string             `json:"target_version"`   // == branch.PreDownload.Tag at predl-stage time
    AudioLanguages  []string           `json:"audio_languages"`
    StagedAt        string             `json:"staged_at"`        // RFC3339
    PlanSnapshot    sophonPlanSnapshot `json:"plan_snapshot"`
}

type sophonPlanSnapshot struct {
    SophonChunkSources []chunkSource         `json:"sophon_chunk_sources"`
    SophonPatches      []sophonPatchInstr    `json:"sophon_patches"`
    SophonDeletes      []sophonDeleteInstr   `json:"sophon_deletes"`
    Categories         []sophonCategorySnap  `json:"categories"`  // per-category {ID, MatchingField, BuildID}
}
```

`predl_ready.json` is written at `versionSidecarDir(tempRoot, gid, predl.TargetVersion)/predl_ready.json` — the per-version dir keyed by the predl target, NOT the live update target.

### §7.2 Stale predl detection — performed by `detectPredlConsume` (§3.6)

All stale-predl detection logic, cleanup ownership, and error surfacing is consolidated in `detectPredlConsume` per §3.6. `CheckForUpdate` calls this once and trusts its verdict; no other site evaluates predl staleness.

### §7.3 Predl→update handoff dispatch

In `RunUpdate`:
1. If `plan.Kind == PlanUpdate` AND `genshinPlan.predlConsume == true`: hydrate `genshinPlan.sophonChunkSources / sophonPatches / sophonDeletes` from `predlSnapshot` directly (overrides values from `buildSophonPlan` — predl snapshot is authoritative for ALREADY-STAGED content; live re-planning would lose the staging directory's contents)
2. Verify staging integrity:
    - **CDN chunks**: for each `chunkSource where kind==CDN`, stat `staging/predl/<predlBuildID>/chunks/<xxh64>`. Missing or xxh64-verify-fail → demote to fresh CDN job (downloaded on the spot during apply's pre-flight)
    - **Local chunks**: NOT pre-verified at handoff time. Local chunks are read at apply time from the user's gameDir; if the user updated via HoYoPlay between predl and now, MD5 verification at read time catches it (`ErrChunkStale` → CDN fallback)
    - **Patch blobs**: for each `patchInstr where Method != DownloadOver`, stat `staging/predl/<predlBuildID>/patches/<patch_md5>`. Missing or MD5-verify-fail → demote to fresh patch blob job
3. Re-locate staged files: `RunUpdate` builds the WAL with `PatchTmp` pointing at `staging/predl/...` paths (NOT `staging/main/...`) so apply consumes predl-staged content directly. After apply success (§6.2 step 8 cleanup), `staging/predl/<predlBuildID>/` is removed.
4. If hash-verify fails for **>25% of CDN chunks** OR **>50% of patch blobs** at step 2: discard predl entirely, delete `predl_ready.json` + `staging/predl/`, fall through to fresh download.

   Rationale for thresholds: per-chunk drift (a few corrupt files from disk-level bit-rot, antivirus quarantine of suspicious blobs, etc.) is acceptable to recover via demotion. Above the threshold, the staged content is so eroded that a one-pass fresh download is cheaper than per-chunk re-verify + re-fetch + per-blob re-extract. Numbers chosen pragmatically — patch blobs tolerate more failure because each blob expands to many files (one bad blob = many target files lost) so the cost asymmetry favors a fresh re-fetch sooner.

   Unit test scenario: `TestSophonPredl_PartialStaleStagingThresholdRecover` (24% CDN fail → demote-and-continue; 26% → discard-and-fresh).

---

## §8. Frontend

### §8.1 Removed i18n key

`update.error.sophon_not_supported` (v1's Option A error) → deleted from all 3 locales.

### §8.2 New i18n keys (all 3 locales)

| Key | zh-TW | en | zh-CN |
|---|---|---|---|
| `update.error.sophon_no_install` | 「請先用 HoYoPlay 完成首次安裝」 | "Use HoYoPlay for initial install" | 「请先用 HoYoPlay 完成首次安装」 |
| `update.error.sophon_manifest_fetch_failed` | 「無法取得 Sophon 更新資訊」 | "Failed to fetch Sophon manifest" | 「无法获取 Sophon 更新信息」 |
| `update.error.sophon_chunk_verify_failed` | 「下載的檔案區塊驗證失敗 ({file})」 | "Chunk verification failed ({file})" | 「下载的文件区块验证失败 ({file})」 |
| `update.error.sophon_apply_failed` | 「{file} 套用失敗」 | "Apply failed: {file}" | 「{file} 应用失败」 |

(`predl_stale` is internal-only — not user-facing — so no i18n key needed.)

### §8.3 Component changes

**Zero**. BottomBar, SidebarRow, Topbar, ConfirmDialog, ToastHost, updates store keep current behavior. The `updates_store` already serializes `last_error.code` and `last_error.params`; new codes flow through without code changes. Bell drawer entries map via i18n. `ReasonResumeInterrupted` reuses the existing `update.tooltip.reason.*` i18n key family (add 1 new key).

### §8.4 Vitest

`i18n_parity.test.ts` auto-covers new keys via its existing parity walk. Add 1 case to `BottomBar.test.ts` exercising the `sophon_no_install` error rendering through the real i18n + Pinia path (not a snapshot — actual rendered text assertion).

---

## §9. Testing strategy

### §9.1 Unit tests

~50 functions across the new files in §1. Highlights:
- `sophon/dedup_test.go`: per-asset isolation; MD5 key collision intra-asset; empty old manifest
- `sophon/decision_test.go`: 6+ path cases — Patch-with-DiffTags-hit / Build-with-old-manifest / Full-no-cache / Patch-with-files-falling-to-main / NoInstall / IdleSameVersion / Patch+UnusedAssets-delete
- `sophon/file_assemble_test.go`: chunks out of file order; non-page-aligned offsets; cross-device errno fallback; nested-path MkdirAll
- `sophon/chunk_download_test.go`: zstd path; raw path; xxh64 mismatch retry; retry-exhausted; xxh64 parse fail → MD5 fallback
- `sophon/hdiff_apply_test.go`: Patch / CopyOver / DownloadOver; cross-device errno; injected Run callback receives expected args
- `sophon_apply_wal_test.go`: batched-rewrite cadence (50 records); time-based cadence (5s); WAL flush on cancel
- `sophon_progress_test.go`: ChunksDone partial resume; PatchesDone partial resume; corrupt-file recovery

### §9.2 Integration tests

Extend `integration_test.go` (currently 9 `t.Skip` placeholders). Use TWO distinct fixture sets to ensure a bug in one fixture doesn't mask all scenarios. Each scenario explicitly notes which fixture set it uses, and several run against BOTH for cross-validation:
- `testdata/sophon/sample.*` — 5-file × 3-chunk per category (used by 1, 2, 3, 4, 6, 7, 9, 10, 11, 15, 17, 18, 19, 20)
- `testdata/sophon/tiny.*` — 1-file × 1-chunk minimal (used by 5, 8, 12, 13, 14, 16, AND a cross-check of 1, 3, 9)

`fakeSophonServer` (httptest) mux:
- GET `/getGameBranches` → canned `branches_*.json`
- GET `/getBuild` + `getPatchBuild` → canned `build_*.json` / `patch_*.json`
- GET CDN manifest paths → raw `.manifest.pb.zst` / `.patch.pb.zst` bytes
- GET CDN chunk paths → raw `chunks/<ChunkName>` bytes

Scenarios:
1. `TestSophonFlavorPatch_EndToEnd` — currentLocal in DiffTags → HDiff path
2. `TestSophonFlavorPatch_FilesNotInPatch_FromMain` — patch covers 3 of 5 files; 2 fall through to main `getBuild`
3. `TestSophonFlavorBuild_EndToEnd` — old manifest available → some Local, some CDN
4. `TestSophonFlavorBuild_StaleLocalChunk_FallbackCDN` — disk file modified → MD5 mismatch → CDN fallback
5. `TestSophonFlavorFull_EndToEnd` — no cache → all CDN (tiny fixture)
6. `TestSophonPredl_StagingThenLiveApply` — predl run → next CheckForUpdate finds predl_ready → apply uses predl staging
7. `TestSophonPredlStale_TargetMismatch` — `predl_ready.target` != current branch.PreDownload.Tag → cleanup → fresh download
8. `TestSophonPredl_CurrentLocalEqPredlTag` — `currentLocal == branch.PreDownload.Tag` → predlAvailable=false (tiny fixture)
9. `TestSophonResume_ApplyWALMidFlight` — kill mid-apply (after 2 of 5 records done) → restart → resume completes
10. `TestSophonResume_DownloadCrashMidChunks` — kill mid-download (3 of 10 done in ChunksDone) → restart → reuses 3, fetches 7
11. `TestSophonChunkVerifyFail_RetryThenSucceed` — first GET corrupt, second OK
12. `TestSophonChunkVerifyFail_RetryExhausted` — all 3 retries corrupt → `sophon_chunk_verify_failed` (tiny fixture)
13. `TestSophonNoInstall_Error` — `config.ini` missing → `sophon_no_install` (tiny fixture)
14. `TestSophonHSRZZZ_LegacyPathUnchanged` — non-Sophon games still go through v1 zip+hdiff (tiny fixture; cross-provider regression)
15. `TestSophonMaybeSelfHeal_WritebackRetry` — currentLocal stale, `last_apply_target` matches target, 24h passed → self-heal → idle
16. `TestSophonCancelMidChunkDownload` — `ctx.Cancel()` during zstd stream → worker exits cleanly (tiny fixture)
17. `TestSophonCrossDeviceErrno_AssembleRename` — assemble succeeds but rename returns EXDEV → fallback copy+delete
18. `TestSophonDiffTagsEmpty_FallsToBuildOrFull` — `branch.Main.DiffTags = []` → never hits Patch flavor
19. `TestSophonMainCategoriesEmpty_Error` — malformed → `sophon_manifest_fetch_failed`
20. `TestSophonUnusedAssetsDeleted` — `UnusedAssets` entries trigger `delete` WAL records
21. `TestScanRecovery_SophonApplyWAL` — `core.ScanRecovery` correctly classifies a dir with only `sophon_apply.wal` as `RecoveryPhaseApplyResume`
22. `TestScanRecovery_SophonProgress` — same for `sophon_progress.json` → `RecoveryPhaseDownloadResume`
23. `TestScanRecovery_Precedence_SophonOverV1` — `sophon_apply.wal` AND `apply.wal` both present → `sophon_apply.wal` wins (Sophon supersedes v1 at same scope)
24. `TestSophonImportCycleFree` — meta-test asserting `internal/providers/hoyoverse/sophon/` does not import `internal/providers/hoyoverse/` (uses `go list -deps`)
25. `TestSophonHdiffPatch_OldFileMD5Mismatch_DemoteToChunkAssemble` — pre-apply MD5 fails on a modded source file → record rewritten to `chunk_assemble` from main → apply succeeds
26. `TestSophonChunkVerify_XXh64ParseFail_FallbackMD5` — `ChunkName` first 16 chars don't parse as hex → MD5 used as verification key instead
27. `TestSophonPredl_PartialStaleStagingThresholdRecover` — 24% CDN-chunk verify fail → per-chunk demote-and-continue; 26% → discard entire predl + fresh download
28. `TestSophonIdlePathCleanup_AllDoneWAL` — after a successful apply, simulate a crash between WAL-all-done and staging-cleanup; next CheckForUpdate hits idle short-circuit AND `cleanupStaleSophonSidecars` removes the orphan WAL + staging dir
29. `TestSophonPredl_ZeroValueKind_TreatedAsStale` — legacy v1-shaped `predl_ready.json` with `Kind==""` → §3.6 treats as stale → cleanup → no consume
30. `TestSophonPredl_FullFlavorBlocked` — `branch.PreDownload.IsEmpty() == false` but `currentLocal ∉ DiffTags` AND no cached old manifest → `predlAvailable=false` (Full predl never offered)

### §9.3 Fuzz

- `FuzzSophonManifestProto_Parse` — random zstd-protobuf bytes → no panic
- `FuzzSophonPatchProto_Parse` — same
- `FuzzAppliedJSON_Serde` — random JSON → no panic on Load
- `FuzzPatchBlobSlice` — random `PatchOffset`/`PatchLength` against random blobs → no panic / no out-of-bounds
- `FuzzSophonApplyWAL_Roundtrip` — random records → marshal/unmarshal preserves shape

### §9.4 Bench

- `BenchmarkBuildPerAssetMD5Index_LargeAsset` — single asset with 500 chunks → verify < 5 ms
- `BenchmarkDedupLookup_1MOps` — verify > 5M lookups/sec
- `BenchmarkChunkAssemble_3GBFile` — disk-bound; sanity check (≥ disk throughput minus 10%)
- `BenchmarkSophonApplyWAL_BatchedRewrite_50KRecords` — verify total rewrite I/O is bounded by batching (< 100 MB total for 50K records)

---

## §10. Risks, known limitations, future scope

### Known limitations (accepted in v2)

1. **Fresh install not supported** — Omnigate's Sophon path requires an existing install detected via `config.ini`. Initial install routes through HoYoPlay.
2. **`plat_app` hardcoded** — If HoYoverse rotates per-game `plat_app` values, Sophon API calls 4xx until we ship a fix. Mitigation: extract from HoYoPlay local metadata at runtime (deferred to v2.1).
3. **No HTTP byte-range for chunks** — Collapse does not use byte-range; we follow suit. Whether HoYoverse CDN supports them is untested. Mid-chunk network drop costs chunk's worth of bytes on retry. Chunks typically ~10 MB.
4. **3+ versions behind without cached old manifest** — Triggers full download via Path C. Real savings depend on how much actually changed.
5. **CN region not supported** — `meta.go` registers only `global`.
6. **Manifest decryption code not written** — `password` treated as inert. Defensive telemetry logs non-zero `encryption` flag AND non-empty `password`.
7. **ZZZ may migrate to Sophon mid-v2** — If HoYoverse migrates ZZZ between now and v2 ship, flip `UsesSophon=true` on `meta.go` ZZZ entry and re-smoke (one-line code change; no design change).
8. **Same-chunk duplicate download tolerated** — Bounded waste; explicit non-goal to add inflight singleflight map.
9. **Chunk-verify precedence inverts Collapse** — v2 uses xxh64 (`ChunkName` prefix) primary, MD5 fallback. Collapse uses MD5 primary for main chunks, xxh64 only for patch blobs. Faster on the happy path; risk is a HoYoverse-side chunk-naming convention change that breaks our prefix parse. Mitigation: mandatory MD5 fallback on xxh64 parse failure. Test scenario `TestSophonChunkVerify_XXh64ParseFail_FallbackMD5` in §9.2 covers this.
10. **Resume semantics by sidecar type** — Apply-phase resume from `sophon_apply.wal` is **offline-safe**: the WAL carries every per-record path, offset, length, and MD5 needed to finish the apply without re-fetching manifests. Download-phase resume from `sophon_progress.json` (no WAL) **requires network**: the dispatcher re-runs `CheckForUpdate` to rebuild the chunk-source plan. A future v2.1 could persist the resolved plan to a sidecar so download-phase resume is network-free too.
11. **Alt-CDN fallback not implemented** — see §0 row. Risk: HoYoverse-side chunk URL rotation between the time a manifest was cached and the time we fetch its chunks → CDN GET returns 404 on a new-branch chunk URL that Collapse would have retried against the OLD branch's chunk-CDN base. Affects Path A (HDiff via patch blobs) and Path C (Full from `getBuild`) chunk fetches; does NOT affect Path B (chunk-from-disk LocalChunkRead) which never touches CDN. Mitigation: surface `sophon_chunk_verify_failed`, user retry triggers fresh plan via `CheckForUpdate` which gets new URLs.

### Spec deviations from v1 (carried forward)

1. `planFlavor` enum gets 5 new constants; existing v1 flavors untouched.
2. `genshinPlan` struct gets 9 new Sophon-only fields (`sophonBranch`, `sophonBuildID`, `sophonCategories`, `sophonChunkSources`, `sophonPatches`, `sophonDeletes`, `sophonPatchAssetsFromMain`, `predlConsume`, `predlSnapshot`, `predlPlan`).
3. NEW parallel WAL sidecar `sophon_apply.wal` with typed records and batched rewrite policy; v1's `apply.wal` flat string lists untouched.
4. NEW parallel progress sidecar `sophon_progress.json` (NOT `core.ProgressFile`-shaped); v1's `progress.json` untouched.
5. `core.recovery.go::ScanRecovery` extended to know `sophon_apply.wal` + `sophon_progress.json`. New file precedence rules added but v1 sidecar precedence unchanged.
6. `core.updater.go` gets one new `ReasonResumeInterrupted` constant.
7. `internal/providers/hoyoverse/hpatchz.go` MOVED to `internal/providers/hoyoverse/hpatchz/hpatchz.go` (sub-package extraction) to break the proposed import cycle. v1 callers update import paths.
8. Sidecar tree adds `sophon/` subdir at `gameSidecarDir`, plus per-version: `sophon_apply.wal`, `sophon_progress.json`, `staging/{main,predl}/<build_id>/`.
9. v1's `predlReadyFile` schema extended for Sophon predl (new fields under the same JSON object). v1 legacy predl (HSR/ZZZ) doesn't write the new fields; v1 reader ignores unknown JSON keys per `encoding/json` defaults.

### Future scope (NOT in v2)

1. SQLite-backed cross-game state + gacha record tracking (separate milestone; see `memory/project_future_sqlite.md`)
2. HSR / ZZZ Sophon migration (flip `UsesSophon` flag when HoYoverse migrates them; verify `plat_app` discovery)
3. Per-game `plat_app` runtime discovery
4. CN region support
5. Fresh-install flow
6. Settings UI for audio language management

---

## §11. Open questions answered during 1st spec review (closed)

| Q | Answer | Source |
|---|---|---|
| `.pb.go` committed or regenerated? | **Committed.** | §2.5 |
| `tools.go` vs Makefile bootstrap? | **`tools.go`** (Go-native). | §1 sophon/proto/tools.go |
| Manifest/chunk URL signing token? | **Path-appended chunk name; no password.** | Collapse `Extension.cs:441-470` |
| `getGameBranches` `pre_download` shape? | snake_case `pre_download`, parallel to `main` (extending Collapse with the same `categories` field HoYoverse added live). | §2.2 |

---

## §12. Authorization model

**Batch autonomous** (confirmed 2026-06-01).

Subagent-driven-development loop runs Tasks 1–N without per-task user checkpoint. Stop conditions: (a) implementer subagent BLOCKED on same task twice consecutively, (b) reviewers cannot reconcile, (c) plan-level error discovered, (d) the final USER smoke task. Per-task reviewer findings against plan-verbatim code are overridden (M3.A / v1 established pattern per `memory/feedback_autonomous_m3a_batch.md` / `feedback_autonomous_m3b_batch.md`).

This grant will be captured in a new memory file `feedback_autonomous_m3b_v2_batch.md` at plan-writing time and marked EXPIRED at v2 ship.
