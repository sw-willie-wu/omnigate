# Omnigate M3.B v2 — HoYoverse Sophon Protocol Design

**Status:** Draft, post-brainstorm (2026-06-01)
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
| Update path A (HDiff) | `getPatchBuild` when `currentLocal ∈ branch.Main.DiffTags` | Smallest payload; reuses M3.B v1 `hpatchz.go` binary |
| Update path B (chunk-from-disk diff) | `getBuild` + old-manifest sidecar lookup when not in DiffTags but prior manifest cached | Saves 80–95% vs full re-download for 3+ version-behind users |
| Update path C (full) | `getBuild` fresh download when no prior manifest | Used for safe-mode fallback; rare in practice |
| Fresh install | NOT supported — return `sophon_no_install` error | Omnigate guides user to open HoYoPlay for initial install; we only handle updates |
| Audio packs | Subset by `DetectInstalledLanguages` (v1 reuses) | Avoid pulling 30+ GB of unused VO |
| Persistence | Atomic JSON sidecar files (write-temp → rename), same pattern as M3.B v1 | SQLite deferred to post-v2 milestone (gacha records + cross-game state) |
| Manifest cache retention | Keep latest + previous 1 build per game | Supports 2-step chunk-dedup chain (e.g. 6.4 → 6.5 → 6.6) |
| HDiff binary | Reuse M3.B v1 `hpatchz.go` + embedded `hpatchz.exe` verbatim | Sophon HDiff patches are the same format |
| Encryption | None — `password` from `getGameBranches` is a CDN URL signing token, NOT manifest encryption key | Confirmed via Collapse source (`EncryptionPassword` field declared but never used) |
| Manifest format | zstd-compressed Protocol Buffers | `google.golang.org/protobuf` + `klauspost/compress/zstd` |
| Chunk hash algorithm | xxh64 (first 16 hex chars of `ChunkName`) primary, MD5 (`ChunkDecompressedHashMd5`) fallback | Matches Collapse's verification strategy |
| `plat_app` | Hardcoded per-game in `meta.go` (Genshin global = `ddxf6vlr1reo`) | 1:1 with biz code, stable; same approach as `APIGameID` |
| Sophon CDN host | Separate API base `https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api` (distinct from M3.B v1's `sg-hyp-api.hoyoverse.com`) | Real protocol fact |
| Concurrent updates | Allowed (per-game `InFlightOp`) | M3.B v1 frontend wiring already supports this |
| Disk-space pre-flight | Hard block (sum of decompressed chunk sizes × 1.1) | Mirror M3.B v1 |
| `config.ini` writeback failure | Warn-log, treat apply as successful, retry via `maybeSelfHeal` | Mirror M3.B v1 Genshin admin caveat |
| Option A short-circuit removal | Replace `sophon_not_supported` error path with real plan | The whole point of v2 |

---

## §1. Package layout & file responsibilities

### `internal/providers/hoyoverse/` — files **extended** from v1

| File | Change | Responsibility |
|---|---|---|
| `meta.go` | extended | Add per-game `PlatApp string` field (Genshin global = `"ddxf6vlr1reo"`). `UsesSophon` flag already exists. |
| `api.go` | extended | Add `BranchInfo` struct + `fetchBranchInfo(ctx, apiGameID) (*BranchInfo, error)` returning full `{Main, PreDownload}` with `{PackageID, Password, Tag, DiffTags, Categories}`. `fetchBranchTag` becomes a thin wrapper. |
| `hoyoverse.go` | extended | `CheckForUpdate`: Sophon path dispatched on `UsesSophon=true` (replaces `sophon_not_supported` short-circuit). `RunUpdate`: Sophon flavors dispatched via new `runSophon*` helpers. `manifestCache` already holds `*genshinPlan`. |
| `plan_internal.go` | extended | Add 5 new `planFlavor` constants (`flavorSophonPatch`, `flavorSophonBuild`, `flavorSophonFull`, `flavorSophonPredlPatch`, `flavorSophonPredlBuild`). Extend `genshinPlan` with `sophonBranch *branchInfo`, `sophonBuildID string`, `sophonCategories []sophonCategory`, `sophonChunkSources []chunkSource`, `sophonPatches []sophonPatchInstr`. |
| `sidecar_paths.go` | extended | Add `sophonSidecarDir(tempRoot, gid)`, `sophonManifestsDir`, `sophonChunksIndexDir`, `sophonStagingDir(tempRoot, gid, buildID)`. |
| `update_progress.go` | extended | `progressStore` schema extended with optional `SophonStage` (`fetching_manifest`, `downloading_chunks`, `assembling`, `patching_hdiff`). Backward-compat: M3.B v1 sidecars without the field deserialize fine. |
| `update_apply.go` | extended | WAL extended with three record types: `chunk_assemble`, `hdiff_patch`, `copy_over`. Existing v1 records (`full_rename`, `patched_file`) untouched. Apply dispatcher inspects WAL record `Kind` field. |

### `internal/providers/hoyoverse/sophon/` — **new sub-package**

Keeping Sophon-specific code in its own directory keeps the hoyoverse package focused. Public API: a handful of types + functions consumed by the parent package.

| File | Lines (est.) | Responsibility |
|---|---|---|
| `proto/sophon_manifest.proto` | ~30 | Verbatim from Collapse `Hi3Helper.Sophon/Protos/`. |
| `proto/sophon_patch.proto` | ~30 | Same. |
| `proto/sophon_manifest.pb.go` | generated | `protoc-gen-go` output. Checked in (we don't want runtime codegen). |
| `proto/sophon_patch.pb.go` | generated | Same. |
| `proto/tools.go` | ~10 | `//go:build tools` + import `protoc-gen-go` so `go install` picks it up. |
| `proto/gen.go` | ~5 | `//go:generate protoc --go_out=. *.proto` for regeneration. |
| `branches.go` | ~120 | `BranchInfo`, `BranchCategory` types + JSON shapes matching extended `getGameBranches` response. |
| `infos.go` | ~150 | `SophonBuildResponse`, `SophonPatchResponse`, `ManifestInfo`, `ChunkDownloadInfo` types matching `getBuild` / `getPatchBuild` JSON envelopes. |
| `infos_test.go` | ~80 | JSON parse round-trips against fixture. |
| `manifest_fetch.go` | ~120 | `FetchBuildManifest(ctx, http, branch, category, target_tag) (*SophonManifestProto, error)`. GET `manifest_download.url_prefix + '/' + id` → zstd decompress → `proto.Unmarshal`. Sister `FetchPatchManifest`. |
| `manifest_fetch_test.go` | ~80 | Against canned zstd-protobuf bytes. |
| `chunks_index.go` | ~150 | `ChunksIndex` type = `map[xxh64uint64][]ChunkRef{file, offset, size}`. `BuildFromManifest(proto)`. `Save(path)` / `Load(path)` atomic JSON. |
| `chunks_index_test.go` | ~80 | Build, save, load round-trip; 25K-entry bench. |
| `decision.go` | ~150 | `DecidePath(branch, currentLocal, oldIndex) (flavor, reason)`. `BuildChunkSources(newManifest, oldIndex, gameDir) []ChunkSource`. `BuildPatchInstructions(patchProto, currentLocal) []PatchInstr`. |
| `decision_test.go` | ~120 | Decision-tree exhaustive cases. |
| `chunk_download.go` | ~180 | Single-chunk fetch: GET `chunk_download.url_prefix + '/' + chunkName` → zstd decompress (if `IsUseCompression`) → write to staging → verify xxh64/MD5. Retry budget (3× exponential). |
| `chunk_download_test.go` | ~150 | httptest CDN; compressed + raw; verify-fail-redownload. |
| `local_chunk_read.go` | ~80 | Read chunk from on-disk file at offset, verify xxh64; on mismatch, signal upstream to mark stale + fall back to CDN. |
| `local_chunk_read_test.go` | ~60 | Stale fall-through. |
| `file_assemble.go` | ~180 | Given `AssetProperty + []ChunkSource`, build target file: open tmp, `SetLength(asset.size)`, per chunk `WriteAt(decompressed_bytes, on_file_offset)`, fsync, atomic rename. Whole-file MD5 verification against `asset.AssetHashMd5`. |
| `file_assemble_test.go` | ~120 | 3-chunk file; chunk-out-of-order; partial reconstruction resume. |
| `hdiff_apply.go` | ~120 | Given `PatchInstr{Patch | CopyOver | DownloadOver}` + patch blob slice, invoke `hpatchz.Run` (from parent package) or copy. |
| `hdiff_apply_test.go` | ~100 | Patch / CopyOver / DownloadOver branches. |

### `internal/providers/hoyoverse/` — **new files**

| File | Lines (est.) | Responsibility |
|---|---|---|
| `update_sophon_plan.go` | ~250 | `buildSophonPlan(ctx, http, branch, gid, currentLocal, audioLangs, gameDir, tempRoot, oldIndex) (*genshinPlan, predlAvail bool, error)`. Orchestrates `sophon.DecidePath` + manifest fetches + `BuildChunkSources` / `BuildPatchInstructions` per category. |
| `update_sophon_plan_test.go` | ~200 | Decision wiring; httptest end-to-end. |
| `update_sophon_download.go` | ~200 | Sophon-aware 4-worker pool: each job = chunk download (CDN or local read). Uses `sophon.ChunkDownload` / `sophon.LocalChunkRead`. Decompressed-byte progress accounting. |
| `update_sophon_download_test.go` | ~150 | Concurrency, cancel, progress accounting. |
| `update_sophon_apply.go` | ~250 | Per-category, per-file dispatch on flavor: `flavorSophonPatch` → `sophon.ApplyHDiff`; `flavorSophonBuild | flavorSophonFull` → `sophon.AssembleFile`. WAL append per file. Apply lock + ordering: `game` category first, then audio langs. |
| `update_sophon_apply_test.go` | ~200 | Per-flavor flows; WAL resume; cross-device errno path. |
| `testdata/sophon/branches.json` | — | Sanitized `getGameBranches` response with both `main` and `pre_download`. |
| `testdata/sophon/build.json` | — | Sanitized `getBuild` envelope. |
| `testdata/sophon/patch.json` | — | Sanitized `getPatchBuild` envelope. |
| `testdata/sophon/sample.manifest.pb.zst` | — | ~5 file × 3 chunk synthetic manifest. |
| `testdata/sophon/sample.patch.pb.zst` | — | ~3 file × 1 patch synthetic. |
| `testdata/sophon/chunks/*` | — | Synthetic chunk blobs (zstd-compressed and raw variants). |

### v1 files **retired** for Sophon path (kept active for HSR/ZZZ)

| File | v1 use | v2 Sophon path |
|---|---|---|
| `update_manifest.go` | `getGamePackages` → buildPlan zip+hdiff | NOT called for `UsesSophon=true` games. Still called for HSR/ZZZ. |
| `update_download.go` | Zip blob 4-worker pool | NOT called for Sophon. Replaced by `update_sophon_download.go`. HSR/ZZZ unchanged. |
| `update_patch.go` | Zip extract + hdifffiles/hdiffmap parse | NOT called for Sophon. HSR/ZZZ unchanged. |
| `update_apply.go` | Atomic file rename WAL | Sophon adds `chunk_assemble | hdiff_patch | copy_over` record types alongside v1's `full_rename | patched_file`. Common WAL replay loop dispatches on Kind. |

### Sidecar tree (v2 additions per game)

```
tempRoot/hoyoverse/games/<flat_gid>/sophon/
  manifests/
    <build_id>.manifest.pb.zst        # raw wire form, one per category × per kept-build
    latest.json                       # { build_id, version, categories: { "game": <build_id>, "en-us": <build_id>, ... } }
    previous.json                     # same shape; rotated on apply
  chunks_index/
    <build_id>.chunks.json            # { "<xxh64_hex>": [{file, offset, size}, ...], ... }
  staging/
    <target_build_id>/
      chunks/<chunk_xxh64>            # downloaded + verified chunks awaiting assemble
      patches/<patch_blob_md5>        # downloaded patch blobs awaiting HDiff apply
      assembled/<file_relpath>.tmp    # being-assembled output files (renamed to gameDir on apply WAL commit)
  predl-ready.json                    # v1-existing sidecar; Sophon reuses for predl readiness signal
```

`tempRoot` is the same root v1 uses; new `sophon/` subdir added.

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

`plat_app` baked in `meta.go`. `branch`/`password`/`package_id` come from the extended `getGameBranches` parse. `tag` is `branch.Main.Tag` for update or `branch.PreDownload.Tag` for predl.

### §2.2 `getGameBranches` response extension

v1 only parsed `Main.Tag`. v2 parses the full shape:

```go
type branchInfo struct {
    Main       branchSlot
    PreDownload branchSlot // may be empty between releases
}

type branchSlot struct {
    PackageID   string
    Branch      string
    Password    string
    Tag         string
    DiffTags    []string
    Categories  []branchCategory // CATEGORY_TYPE_RESOURCE + CATEGORY_TYPE_AUDIO
}

type branchCategory struct {
    CategoryID    string  // "10016" game, "10017"–"10020" audio langs
    MatchingField string  // "game" | "zh-cn" | "en-us" | "ko-kr" | "ja-jp"
    Type          string  // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
}
```

### §2.3 `getBuild` / `getPatchBuild` response shape (canonical)

```json
{
  "retcode": 0,
  "message": "",
  "data": {
    "build_id": "...",
    "tag": "6.6.0",
    "patch_id": "...",          // patch endpoint only
    "manifests": [
      {
        "category_id": "10016",
        "category_name": "...",
        "matching_field": "game",
        "manifest": { "id": "...", "checksum": "...", "compressed_size": 12345, "uncompressed_size": 67890 },
        "manifest_download": { "url_prefix": "https://...", "url_suffix": "", "password": "<cdn-signing-token>", "encryption": 0, "compression": 1 },
        "chunk_download":    { "url_prefix": "https://...", "url_suffix": "", "password": "<cdn-signing-token>", "encryption": 0, "compression": 1 },
        "diff_download":     { ... },  // patch endpoint only; same shape
        "stats": { ... }
      },
      ...
    ]
  }
}
```

**`compression: 1`** signals zstd on manifest body and chunk bodies. We always treat it as authoritative; no fallback to "guess".

**`encryption`** is parsed and logged if non-zero (defensive), but we expect zero throughout — Collapse confirms `password` is unused for crypto.

### §2.4 Protobuf schemas (verbatim from Collapse)

**`sophon_manifest.proto`:**
```proto
syntax = "proto3";
package sophon;
option go_package = "omnigate/internal/providers/hoyoverse/sophon/proto";

message SophonManifestProto {
  repeated SophonManifestAssetProperty Assets = 1;
}

message SophonManifestAssetProperty {
  string AssetName = 1;
  repeated SophonManifestAssetChunk AssetChunks = 2;
  int32  AssetType = 3;       // non-zero = directory
  int64  AssetSize = 4;
  string AssetHashMd5 = 5;
}

message SophonManifestAssetChunk {
  string ChunkName = 1;                  // CDN filename, first 16 hex = xxh64
  string ChunkDecompressedHashMd5 = 2;   // 16-byte MD5 hex
  int64  ChunkOnFileOffset = 3;          // byte offset into reassembled file
  int64  ChunkSize = 4;                  // on-wire (possibly zstd-compressed)
  int64  ChunkSizeDecompressed = 5;
}
```

**`sophon_patch.proto`:**
```proto
syntax = "proto3";
package sophon;
option go_package = "omnigate/internal/providers/hoyoverse/sophon/proto";

message SophonPatchProto {
  repeated SophonPatchAssetProperty Assets = 1;
}

message SophonPatchAssetProperty {
  string AssetName = 1;
  int64  AssetSize = 2;
  string AssetHashMd5 = 3;
  repeated SophonPatchAssetChunk AssetInfos = 4;
}

message SophonPatchAssetChunk {
  string PatchName = 1;        // CDN filename
  string VersionTag = 2;       // diff source version, e.g. "6.5.0"
  string BuildId = 3;
  int64  PatchSize = 4;
  string PatchMd5 = 5;
  int64  PatchOffset = 6;      // slice into the patch blob
  int64  PatchLength = 7;
  string OriginalFileName = 8; // empty → CopyOver method; non-empty → HDiff
  int64  OriginalFileLength = 9;
  string OriginalFileMd5 = 10;
}
```

`go_package` option puts generated code under `internal/providers/hoyoverse/sophon/proto/`.

### §2.5 Dependencies

New `go.mod` additions:
- `google.golang.org/protobuf` (latest stable)
- `github.com/klauspost/compress` (zstd subpackage; pure Go; CGO_ENABLED=0 friendly)
- `github.com/cespare/xxhash/v2`

Build tools:
- `protoc` (system binary; CI / dev machine prerequisite documented in README)
- `protoc-gen-go` (managed via `proto/tools.go` build tag pattern; `go install` from `tools.go` import)

---

## §3. Decision tree — `CheckForUpdate` for Sophon games

```
g := findByID(gid)
if g == nil || !g.UsesSophon → fall back to v1 legacy flow

branch, err := fetchBranchInfo(ctx, g.APIGameID)
if err → return UpdateError{Code:"sophon_manifest_fetch_failed", Retryable:true}

currentLocal := ReadGameVersion(gameDir)   // best-effort; "" on miss

# Pre-checks
if currentLocal == "":
    return UpdateError{Code:"sophon_no_install", Retryable:false}
if currentLocal == branch.Main.Tag:
    plan.Kind = PlanUpdate, plan.Reason = ReasonNone
    return plan with empty Files (idle; mirrors v1 maybeSelfHeal pattern)

audioLangs := DetectInstalledLanguages(gameDir)  // empty slice OK
oldIndex   := loadChunksIndex(tempRoot, gid, currentLocal)  // nil OK

# Decision
case currentLocal ∈ branch.Main.DiffTags:
    flavor = flavorSophonPatch
    plan = buildPatchPlan(branch.Main, audioLangs, currentLocal, tempRoot, gid)
case oldIndex != nil:
    flavor = flavorSophonBuild
    plan = buildBuildPlan(branch.Main, audioLangs, oldIndex, gameDir, tempRoot, gid)
default:
    flavor = flavorSophonFull
    plan = buildBuildPlan(branch.Main, audioLangs, nil, gameDir, tempRoot, gid)

# Predl detection (parallel decision tree on PreDownload branch)
predlAvail := branch.PreDownload != empty && currentLocal != branch.PreDownload.Tag
```

`buildPatchPlan` orchestrates: per `audioLangs ∪ {"game"}`, fetch `getPatchBuild`'s `SophonPatchProto` for that category, run `sophon.BuildPatchInstructions(proto, currentLocal)` → `[]sophonPatchInstr`. Total bytes = Σ `PatchLength` (deduplicated by patch blob name; multiple files can share one blob).

`buildBuildPlan` orchestrates: per `audioLangs ∪ {"game"}`, fetch `getBuild`'s `SophonManifestProto` for that category, run `sophon.BuildChunkSources(proto, oldIndex, gameDir)` → `[]chunkSource`. Total bytes = Σ `ChunkSize` (compressed) where `chunkSource.kind == kindCDN`.

`reason` enum (extending v1's `core.UpdatePlan.Reason`):
- `ReasonVersionChanged` (currentLocal != branch.Tag)
- `ReasonResumeInterrupted` (apply.wal or extract_progress.json present)
- `ReasonAudioPackChanged` (DetectInstalledLanguages diff vs prior — rare; M3.B v1 covers)
- `ReasonSophonChunkVerifyFailed` (mid-flight verification retry-after-resume)

---

## §4. Manifest cache + reuse layer

### §4.1 Persistence atomic JSON

All sidecars use the M3.B v1 pattern: write to `.tmp`, fsync, atomic rename. No partial-write states observable.

`chunks_index/<build_id>.chunks.json`:
```json
{
  "build_id": "...",
  "version": "6.5.0",
  "generated_at": "2026-06-01T12:00:00Z",
  "entries": {
    "<xxh64_hex>": [
      { "file": "GenshinImpact_Data/StreamingAssets/...", "offset": 0, "size": 10485760 },
      { "file": "GenshinImpact_Data/StreamingAssets/other.pak", "offset": 524288, "size": 10485760 }
    ]
  }
}
```

`latest.json`:
```json
{ "build_id": "...", "version": "6.5.0", "applied_at": "2026-06-01T12:00:00Z",
  "categories": { "game": "<build_id>", "en-us": "<build_id>" } }
```

### §4.2 Cache rotation

After `apply` succeeds for build B (target version V):
1. Read `latest.json` → was build P (version U)
2. Write new `latest.json` for B
3. Move old `latest.json` content to `previous.json`
4. If a `previous.json` already existed pointing at build E, delete `manifests/E.*` + `chunks_index/E.chunks.json`

Result: always ≤ 2 prior builds on disk. Storage bound: ~10–20 MB total.

### §4.3 Cache hit/miss semantics

`loadChunksIndex(tempRoot, gid, version)`:
- Read `latest.json`; if `version == latest.version`, load `chunks_index/<latest.build_id>.chunks.json`
- Else read `previous.json`; if `version == previous.version`, load that index
- Else return nil (cache miss → flavor degrades from `Build` to `Full`)

### §4.4 Stale entry recovery

When `local_chunk_read.go` reads at `{file, offset}` and xxh64 differs from manifest's expected hash:
1. Mark the chunk source as "fall back to CDN"
2. Log warning with `file`, `offset`, expected vs actual hash
3. Schedule a background invalidation: clear that `xxh64` entry from in-memory `chunks_index` for the current update (no disk write — index regenerates from new manifest at apply time)

This handles the case where user manually edits a `.pak` file or game integrity got partially restored elsewhere.

---

## §5. Download layer

### §5.1 Worker pool

Same 4-worker pool shape as M3.B v1 `update_download.go`. Job queue items:

```go
type sophonJob struct {
    kind    sophonJobKind  // jobChunkCDN | jobChunkLocal | jobPatchBlob
    category string         // "game" | "en-us" | ...
    cdn     *chunkSourceCDN
    local   *chunkSourceLocal
    patch   *patchBlobJob   // for HDiff
    out     string          // staging path
    onDone  func(err error)
}
```

Worker dispatches on `kind`:
- `jobChunkCDN` → `sophon.ChunkDownload` (HTTP GET + zstd stream + xxh64 verify + atomic rename to `out`)
- `jobChunkLocal` → `sophon.LocalChunkRead` (open `local.file`, seek `local.offset`, read `local.size`, xxh64 verify, write to `out`; on mismatch, requeue as `jobChunkCDN`)
- `jobPatchBlob` → HTTP GET full patch blob → MD5 verify whole blob → atomic rename

### §5.2 Per-chunk retry

3× exponential backoff (1s / 4s / 16s). After 3 failures, surface `UpdateError{Code:"sophon_chunk_verify_failed"}` — non-retryable from this run, but user can re-trigger update; next pass redownloads from scratch (Sophon offers no byte-range).

### §5.3 Progress accounting

`UpdateEvent.Current` = decompressed bytes written across all workers (atomic counter incremented on each `WriteAt` / local copy). `UpdateEvent.Total` = sum of `ChunkSizeDecompressed` for CDN jobs + `local.size` for local jobs + `PatchLength` for patch blobs. Local-chunk reads contribute to progress so the bar advances even on disk-only operations.

### §5.4 Cancel

Each worker checks `ctx.Done()` between job dequeues AND inside the chunk write loop (every ~64 KiB). Mirrors v1.

---

## §6. Apply layer

### §6.1 WAL extensions

Existing v1 `apply.wal` records:
```go
type walRecord struct {
    Kind     string  // "full_rename" | "patched_file"
    Path     string  // relative to gameDir
    State    string  // "pending" | "done"
    SourceTmp string // staging path
}
```

v2 adds three Sophon record kinds:
```go
// Kind: "chunk_assemble"
//   Path:      target file (relative to gameDir)
//   State:     pending | done
//   SourceTmp: staging assembled/<path>.tmp (already MD5-verified)
//
// Kind: "hdiff_patch"
//   Path:      target file
//   State:     pending | done
//   OldPath:   relative to gameDir; HDiff source
//   PatchTmp:  staging patches/<blob_md5> path
//   PatchOff:  byte offset into PatchTmp
//   PatchLen:  byte length
//
// Kind: "copy_over"
//   Path:      target file
//   State:     pending | done
//   SourceTmp: staging chunks/<chunk_xxh64> (whole-file copy)
```

JSON encoding has optional fields with `omitempty` so v1 sidecars round-trip unchanged.

### §6.2 Apply ordering

1. Acquire `applyLock` (v1 pattern, version-scoped)
2. Order categories: `"game"` always first; audio langs in alphabetical order after
3. Per category, per file in manifest order: build WAL record, write to log, execute, fsync, rename, mark done in log
4. After all categories applied: `WriteGameVersion(gameDir, target_tag)` (warn-log on permission error per `maybeSelfHeal` pattern)
5. Rotate `latest.json` / `previous.json` / chunks_index
6. Delete staging dir contents, release `applyLock`

### §6.3 HDiff path

Reuses M3.B v1 `hpatchz.go` verbatim. For each `hdiff_patch` WAL record:
1. Read patch slice `PatchTmp[PatchOff:PatchOff+PatchLen]` → write to temp file `staging/hdiff_input/<patch_md5>_<offset>.bin`
2. Invoke `hpatchz.Run(ctx, oldPath, hdiff_input, output_tmp)`
3. Verify output MD5 against `asset.AssetHashMd5`
4. Atomic rename to gameDir target; mark WAL done

### §6.4 Chunk assemble path

For each `chunk_assemble` WAL record:
1. `out, _ := os.OpenFile(out_tmp, RDWR|CREATE|TRUNC, 0o644)`
2. `out.Truncate(asset.AssetSize)`
3. For each chunk in `AssetChunks`: read staging `chunks/<chunk_xxh64>` (which we know is already xxh64-verified and decompressed by the download phase) → `out.WriteAt(bytes, chunk.ChunkOnFileOffset)`
4. `out.Sync()`, `out.Close()`
5. Whole-file MD5 verification against `asset.AssetHashMd5`. Mismatch → delete out_tmp, surface error
6. Atomic rename to gameDir; mark WAL done

### §6.5 CopyOver path

For each `copy_over` WAL record: simple `os.Rename(staging chunk, gameDir target)`. If cross-device errno detected (v1 pattern), fall back to copy+delete.

### §6.6 Resume from crash

On next `RunUpdate` call:
- `walExists(versionDir)` → readApplyWAL → resume from first non-done record
- `extractProgressExists(versionDir)` → v1 path, used for legacy HSR/ZZZ
- Neither → fresh run from `manifestCache.get(gid)`

Records with `State: done` are skipped during resume; the rename has already happened, the staging file may or may not still exist (don't care).

---

## §7. Predownload

### §7.1 Flow

Mirror the update flow on `branch.PreDownload`:
1. `CheckForUpdate` sees `branch.PreDownload != empty && currentLocal != branch.PreDownload.Tag` → set `predlAvailable = true` on the cached `genshinPlan`
2. App.GetPredownloadAvailable returns true → UI shows predl button (M3.B v1 wiring untouched)
3. User clicks predl → App.StartPredownload → Provider.RunUpdate with `plan.Kind = PlanPredownload`
4. RunUpdate dispatches Sophon flavor as `flavorSophonPredlPatch` or `flavorSophonPredlBuild` (no `Full` predl — predl always implies an installed game)
5. Download phase runs identically; staging tree at `staging/predl/<predl_build_id>/`
6. **Apply phase is SKIPPED**. Instead, write `predl-ready.json` with metadata:
   ```json
   {
     "kind": "sophon_patch" | "sophon_build",
     "build_id": "...", "version": "...", "audio_languages": [...],
     "staged_at": "..."
   }
   ```
7. When the live update unlocks (currentLocal == predl source version), `CheckForUpdate` detects `predl-ready.json`, reuses staged content, skips re-download, jumps to apply phase

### §7.2 Stale predl handling

If `branch.PreDownload.Tag` changes after predl completed (rare; usually means HoYoverse re-released the patch), `predl-ready.json` becomes stale. Detection: on next `CheckForUpdate`, compare `predl-ready.version` vs `branch.PreDownload.Tag`. Mismatch → emit `predl_stale` UpdateError, delete staging tree, fall through to regular update flow.

---

## §8. Frontend

### §8.1 Removed i18n key

`update.error.sophon_not_supported` (v1's Option A error) → deleted from all 3 locales.

### §8.2 New i18n keys (all 3 locales)

| Key | zh-TW | en | zh-CN |
|---|---|---|---|
| `update.error.sophon_no_install` | 「請先用 HoYoPlay 完成首次安裝」 | "Use HoYoPlay for initial install" | 「请先用 HoYoPlay 完成首次安装」 |
| `update.error.sophon_manifest_fetch_failed` | 「無法取得 Sophon 更新資訊」 | "Failed to fetch Sophon manifest" | 「无法获取 Sophon 更新信息」 |
| `update.error.sophon_chunk_verify_failed` | 「下載的檔案區塊驗證失敗」 | "Chunk verification failed" | 「下载的文件区块验证失败」 |
| `update.error.sophon_apply_failed` | 「更新套用失敗」 | "Apply failed" | 「更新应用失败」 |
| `update.error.predl_stale` | 「預下載已過期，將重新下載」 | "Predownload outdated; restarting" | 「预下载已过期，将重新下载」 |

### §8.3 Component changes

**Zero**. BottomBar, SidebarRow, Topbar, ConfirmDialog, ToastHost, updates store all keep current behavior. The `updates_store` already serializes `last_error.code` as a string; new codes flow through without code changes. Bell drawer entries map error codes via i18n.

### §8.4 Vitest

`i18n_parity.test.ts` will auto-cover the new keys via its existing parity walk. Add 1 case to `BottomBar.test.ts` covering the `sophon_no_install` error rendering (just confirms the i18n lookup path; not a snapshot).

---

## §9. Testing strategy

### §9.1 Unit tests per file (red-green TDD per plan task)

Already itemized in §1. Aggregate:
- ~12 new `_test.go` files in `sophon/` sub-package
- ~3 new `update_sophon_*_test.go` files in `hoyoverse/`
- ~50 unit test functions total

### §9.2 Integration tests

Extend `internal/providers/hoyoverse/integration_test.go` (currently 9 `t.Skip` placeholders).

`fakeSophonServer` (httptest):
- GET `/getGameBranches` → canned `testdata/sophon/branches.json` (with both `main` + `pre_download`)
- GET `/getBuild` (route on path) → canned per-category manifest URLs pointing back at the same test server
- GET `/getPatchBuild` → similarly
- GET manifest CDN paths → return raw bytes from `testdata/sophon/sample.manifest.pb.zst` / `sample.patch.pb.zst`
- GET chunk CDN paths → return raw bytes from `testdata/sophon/chunks/`

Integration scenarios:
1. `TestSophonFlavorPatch_EndToEnd` — currentLocal in DiffTags → HDiff path → final files match expected MD5
2. `TestSophonFlavorBuild_EndToEnd` — oldIndex available → some chunks read from disk, some from CDN
3. `TestSophonFlavorFull_EndToEnd` — no cache → all chunks from CDN
4. `TestSophonPredl_StagingThenApply` — predl run → stage → second run applies without redownload
5. `TestSophonResume_ApplyWALMidFlight` — kill mid-apply, restart, resume completes
6. `TestSophonChunkVerifyFail_Redownload` — first GET returns corrupt body, second OK
7. `TestSophonLocalChunkStale_FallbackCDN` — disk source mismatch → CDN redownload
8. `TestSophonPredlStale_RestartUpdate` — `predl-ready.json` version doesn't match current branch
9. `TestSophonNoInstall_Error` — `config.ini` missing → `sophon_no_install` error
10. `TestSophonHSRZZZ_LegacyPathUnchanged` — regression: non-Sophon games still go through v1 flow

### §9.3 Fuzz

- `FuzzSophonManifestProto_Parse` — random zstd-protobuf bytes → no panic
- `FuzzSophonPatchProto_Parse` — same
- `FuzzChunksIndex_Serde` — random JSON → no panic on Load
- `FuzzPatchBlobSlice` — random PatchOffset/PatchLength against random blobs

### §9.4 Bench

- `BenchmarkChunksIndex_Build_25KEntries` — verify < 100 ms
- `BenchmarkChunksIndex_Lookup_1MOps` — verify > 5 M lookups/sec
- `BenchmarkChunkAssemble_3GBFile` — disk-bound; sanity check (≥ disk throughput minus 10%)

---

## §10. Risks, known limitations, future scope

### Known limitations (accepted in v2)

1. **Fresh install not supported** — Omnigate's Sophon path requires an existing install detected via `config.ini`. Initial install routes through HoYoPlay.
2. **`plat_app` hardcoded** — If HoYoverse rotates per-game `plat_app` values, Sophon API calls 4xx until we ship a fix. Mitigation: extract `plat_app` from HoYoPlay's local metadata at runtime (deferred to v2.1 if rotation becomes a problem).
3. **No HTTP byte-range for chunks** — Per Collapse source, chunks are atomic units. A mid-chunk network drop costs the chunk's worth of bytes on retry. Chunks are typically 10 MB so the loss is bounded.
4. **3+ versions behind** — Triggers full download via chunk-from-disk dedup path. Real savings depend on how much actually changed. Worst case approaches a full re-install.
5. **CN region not supported** — `meta.go` still only registers `global`. CN deferred to a future M-something.
6. **Manifest decryption code not written** — `password` field treated as inert; if HoYoverse turns on real encryption later, our parser will produce garbage and surface an error. Acceptable risk per Collapse's current state.

### Spec deviations from v1 (carried forward)

1. v1 `core.UpdatePlan.Reason` field extended with `ReasonSophonChunkVerifyFailed`. (`Reason` was added in v1 Task 2.)
2. v1 `planFlavor` enum gets 5 new constants; existing 6 v1 flavors untouched.
3. v1 `genshinPlan` struct gets 5 new Sophon-only fields. Existing v1 fields untouched.
4. v1 `apply.wal` format gets 3 new `Kind` values. v1's 2 kinds untouched; JSON-decode of new sidecars against old code would surface "unknown kind" → bail with retryable error. Forward-compat only.
5. Sidecar tree gets a `sophon/` subdir at `<tempRoot>/hoyoverse/games/<flat_gid>/`. v1's other sidecars (`progress.json`, `last_apply_target.json`, `predl-ready.json`, `apply.wal`, `extract_progress.json`) stay at their existing paths.

### Future scope (NOT in v2)

1. SQLite-backed cross-game state + gacha record tracking (separate milestone; see [`memory/project_future_sqlite.md`](../../../../.claude/projects/C--Users-willie-Repos-omnigate/memory/project_future_sqlite.md))
2. HSR / ZZZ Sophon migration (flip `UsesSophon` flag when HoYoverse migrates them; verify `plat_app` discovery)
3. Per-game `plat_app` runtime discovery (HoYoPlay local metadata scan)
4. CN region support
5. Fresh-install flow (would require deeper HoYoPlay metadata reverse-engineering)
6. Settings UI for audio language management (current behavior: locked to installed langs detected on disk)

---

## §11. Open questions for plan-writing phase

These don't block spec approval but should be answered during plan-writing:

1. **`proto/` generated files**: check `.pb.go` into git, or regenerate at build time via `make generate`? Recommendation: check in (simpler dev loop; `protoc` not a runtime dep).
2. **`tools.go` pattern**: traditional `//go:build tools` + import-only `tools.go` at `proto/`? Or use `Makefile` instead? Recommendation: `tools.go` (Go-native, doesn't add Makefile).
3. **Manifest fetch authentication**: are `manifest_download.password` and `chunk_download.password` query-string-appended URL signing tokens, or HTTP basic auth, or X-headers? Collapse appears to embed them in the URL path. Plan-writing should verify against a sample wire fetch with `curl --trace`.
4. **`getGameBranches` `pre_download` exact JSON shape**: M3.B v1 captured only `main`. Plan-writing reads from Collapse one more time and codifies — the rough assumption is "same shape as `main`" but the exact JSON key (`pre_download` vs `preDownload` vs `predl`) needs verification.

---

## §12. Authorization model

NOT YET CONFIRMED. Plan-writing phase should ask user:
- Batch autonomous (subagent-driven loop through Tasks 1–N, like M3.B v1)?
- Per-task user checkpoint?

If batch: ledger commits to project memory at start, subagent loop runs from Task 1 → final pre-smoke task without user intervention. User smoke is always the terminal manual task.
