# Omnigate M3.B v2 — HoYoverse Sophon Protocol Design

**Status:** Draft, post-brainstorm + 1st reviewer round (2026-06-01)
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
| Update path B (chunk-from-disk diff) | `getBuild` + prior-version manifest sidecar lookup when not in DiffTags but prior manifest cached | Saves 80–95% vs full re-download for 3+ version-behind users |
| Update path C (full) | `getBuild` fresh download when no prior manifest | Fresh-cache fallback; safe-mode |
| Fresh install | NOT supported — return `sophon_no_install` error | Omnigate guides user to open HoYoPlay for initial install; we only handle updates |
| Patch + Main merge | Patch flow ALWAYS fetches both `getPatchBuild` AND `getBuild`; files-not-in-patch fall through to main manifest | Per `SophonPatch.EnumerateUpdateAsync` in Collapse; otherwise newly-added files are silently missing |
| Audio packs | Subset by `DetectInstalledLanguages` (v1 reuses) | Avoid pulling 30+ GB of unused VO |
| Persistence | Atomic JSON sidecar files (write-temp → fsync → rename), same pattern as M3.B v1 | SQLite deferred to post-v2 milestone (gacha records + cross-game state) |
| Old-manifest retention | Keep latest applied + previous 1 build per game (`<build_id>.manifest.pb.zst` retained for dedup at next update) | Supports 2-step chunk-dedup chain (e.g. 6.4 → 6.5 → 6.6) |
| Chunk-reuse keying | **Per-asset MD5 (`ChunkDecompressedHashMd5`)**, matching Collapse `SophonUpdate.GetChunkOldOffsetFromOld` | The xxh64 prefix of `ChunkName` is a CDN-filename integrity hash, NOT a dedup key |
| Chunk-download integrity | xxh64 of decompressed bytes (if `ChunkName` first 16 hex parse OK) → fallback to MD5 (`ChunkDecompressedHashMd5`) | Matches Collapse's verification strategy |
| HDiff binary | Reuse M3.B v1 `hpatchz.go` + embedded `hpatchz.exe` verbatim | Sophon HDiff patches are the same format |
| Encryption | None — `password` from `getGameBranches` is a CDN URL signing token, NOT manifest encryption key | Confirmed via Collapse: `EncryptionPassword` field declared but never read for crypto |
| Manifest format | zstd-compressed Protocol Buffers | `google.golang.org/protobuf` + `klauspost/compress/zstd` |
| `plat_app` | Hardcoded per-game in `meta.go` (Genshin global = `ddxf6vlr1reo`) | 1:1 with biz code, stable; same approach as `APIGameID` |
| Sophon CDN host | Separate API base `https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api` (distinct from M3.B v1's `sg-hyp-api.hoyoverse.com`) | Confirmed in Collapse `PresetConfig.cs` URL templates |
| `branch.Categories` source | **Live `getGameBranches` API observation (2026-06-01 in memory)**, NOT Collapse — Collapse's `HypGameInfoBranchData` does not include categories | HoYoverse added the field after Collapse last synced |
| Concurrent updates | Allowed (per-game `InFlightOp`) | M3.B v1 frontend wiring already supports this |
| Disk-space pre-flight | Hard block (sum of decompressed chunk sizes × 1.1) | Mirror M3.B v1 |
| `config.ini` writeback failure | Warn-log; surface via `last_apply_target.json`; `maybeSelfHeal` retries on next CheckForUpdate (24h budget) | Mirror M3.B v1 Genshin admin caveat verbatim |
| Sophon apply WAL | New parallel sidecar `sophon_apply.wal` with typed-record schema; v1's `apply.wal` flat-list format untouched (still used by HSR/ZZZ legacy path) | v1's `applyWAL{Pending []string, Done []string}` cannot encode chunk_assemble/hdiff_patch/copy_over without breaking back-compat |
| Option A short-circuit removal | Replace `sophon_not_supported` error path with real plan | The whole point of v2 |

---

## §1. Package layout & file responsibilities

### `internal/providers/hoyoverse/` — files **extended** from v1

| File | Change | Responsibility |
|---|---|---|
| `meta.go` | extended | Add per-game `PlatApp string` field (Genshin global = `"ddxf6vlr1reo"`). `UsesSophon` flag already exists. |
| `api.go` | extended | Add `branchInfo` struct + `fetchBranchInfo(ctx, apiGameID) (*branchInfo, error)` returning full `{Main, PreDownload}` with `{PackageID, Password, Tag, DiffTags, Categories}`. `fetchBranchTag` becomes a thin wrapper. |
| `hoyoverse.go` | extended | `CheckForUpdate`: Sophon path dispatched on `UsesSophon=true` (replaces `sophon_not_supported` short-circuit). `RunUpdate`: Sophon flavors dispatched via new `runSophon*` helpers. `manifestCache` already holds `*genshinPlan`. |
| `plan_internal.go` | extended | Add 5 new `planFlavor` constants (`flavorSophonPatch`, `flavorSophonBuild`, `flavorSophonFull`, `flavorSophonPredlPatch`, `flavorSophonPredlBuild`). Extend `genshinPlan` with `sophonBranch *branchInfo`, `sophonBuildID string`, `sophonCategories []sophonCategory`, `sophonChunkSources []chunkSource`, `sophonPatches []sophonPatchInstr`. |
| `sidecar_paths.go` | extended | Add `sophonSubdir(tempRoot, gid) = filepath.Join(gameSidecarDir(tempRoot, gid), "sophon")` and the four leaf helpers (`sophonManifestsDir`, `sophonStagingDir(tempRoot, gid, buildID)`, `sophonAppliedManifestPath`, `sophonPredlReadyPath`). Layered on top of existing `gameSidecarDir = <tempRoot>/<flat_gid>`. |
| `update_progress.go` | extended | `progressStore` schema gets optional `SophonStage` field (`fetching_manifest`, `downloading_chunks`, `assembling`, `patching_hdiff`); also optional `ChunkProgress map[string]bool` (xxh64 → done) for crash-recovery of the download phase. Backward compat: M3.B v1 sidecars without these fields deserialize cleanly. |

### `internal/providers/hoyoverse/sophon/` — **new sub-package**

Keeping Sophon-specific code in its own directory keeps the parent package focused. Public API: a handful of types + functions consumed by the parent.

| File | Responsibility |
|---|---|
| `proto/sophon_manifest.proto` | Verbatim from Collapse `Hi3Helper.Sophon/Protos/SophonManifestProto.proto`. |
| `proto/sophon_patch.proto` | Verbatim from Collapse `Hi3Helper.Sophon/Protos/SophonPatchProto.proto`. |
| `proto/sophon_manifest.pb.go` | `protoc-gen-go` output. **Committed** (not regenerated at build time). |
| `proto/sophon_patch.pb.go` | Same. |
| `proto/tools.go` | `//go:build tools` + import `google.golang.org/protobuf/cmd/protoc-gen-go` so `go install` picks it up for regen. |
| `proto/gen.go` | `//go:generate protoc --go_out=. *.proto`. Used only during proto file changes. |
| `branches.go` | `branchInfo`, `branchSlot`, `branchCategory` types + JSON shapes matching the extended `getGameBranches` response. |
| `infos.go` | `BuildResponse`, `PatchResponse`, `ManifestIdentity`, `ChunkDownloadInfo`, `ManifestDownloadInfo` types matching `getBuild` / `getPatchBuild` JSON envelopes. Uses a custom `boolish` JSON type that accepts `0|1|"0"|"1"|true|false` (Collapse's BoolConverter quirk). |
| `infos_test.go` | JSON round-trips against fixtures. |
| `manifest_fetch.go` | `FetchBuildManifest(ctx, http, branch, category, target_tag) (*pb.SophonManifestProto, error)`. GET `manifest_download.url_prefix + '/' + id` → if `compression==1` wrap in `zstd.NewReader` → `proto.Unmarshal`. Sister `FetchPatchManifest`. |
| `manifest_fetch_test.go` | Against canned zstd-protobuf bytes. |
| `dedup.go` | `BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto) map[string]map[string]ChunkRef` where outer key = asset name, inner key = decompressed-MD5-hex, value = `{ChunkOnFileOffset, ChunkSizeDecompressed}`. Per-asset scope, MD5-keyed, mirroring Collapse `SophonUpdate.GetChunkOldOffsetFromOld`. |
| `dedup_test.go` | Multi-asset index build; chunk lookup; per-asset isolation (chunk in asset A not visible from asset B query). |
| `decision.go` | `DecidePath(branch, currentLocal, oldManifest) (flavor, reason)`. `BuildChunkSources(newManifest, oldManifest, gameDir) []ChunkSource`. `BuildPatchInstructions(patchProto, mainProto, currentLocal) []PatchInstr` — accepts BOTH proto types and merges (files in patch use Patch/CopyOver; files in main-but-not-patch use DownloadOver). |
| `decision_test.go` | Decision-tree exhaustive cases; patch+main merge cases. |
| `chunk_download.go` | Single-chunk fetch: GET `chunk_download.url_prefix + '/' + chunkName` → wrap body in `ctxReader` for cancel + `zstd.NewReader` (if compressed) → write to staging → verify xxh64/MD5. Retry budget (3× exponential 1s/4s/16s). Staged-chunks-on-disk are reusable across runs (keyed by xxh64). |
| `chunk_download_test.go` | httptest CDN; compressed + raw; verify-fail-redownload; retry budget exhausted. |
| `local_chunk_read.go` | Read chunk from on-disk old-version file at `(file, offset, size)`, verify MD5 against expected; on mismatch, return `ErrChunkStale` so caller falls back to CDN. |
| `local_chunk_read_test.go` | Match path; stale fall-through; ENOENT old-file fallthrough. |
| `file_assemble.go` | Given `AssetProperty + []ChunkSource`, build target file: open `*.tmp`, `Truncate(asset.AssetSize)`, per chunk `WriteAt(decompressed_bytes, ChunkOnFileOffset)`, fsync, whole-file MD5 verify, atomic rename. |
| `file_assemble_test.go` | 3-chunk file; chunk-out-of-order writes; partial reconstruction resume. |
| `hdiff_apply.go` | Given `PatchInstr{Method=Patch \| CopyOver \| DownloadOver}` + patch blob slice path, dispatch: `Patch` invokes `hpatchz.Run` (from parent package) on old file + slice → output `.tmp`; `CopyOver` renames slice file directly to target; `DownloadOver` is a no-op (handled in download phase). |
| `hdiff_apply_test.go` | Three method branches; cross-device errno during rename. |

### `internal/providers/hoyoverse/` — **new files**

| File | Responsibility |
|---|---|
| `update_sophon_plan.go` | `buildSophonPlan(ctx, http, branch, gid, currentLocal, audioLangs, gameDir, tempRoot, oldManifests) (*genshinPlan, predlAvail bool, error)`. Orchestrates `sophon.DecidePath` + manifest fetches + `BuildChunkSources` / `BuildPatchInstructions` per category. |
| `update_sophon_plan_test.go` | Decision wiring; httptest end-to-end. |
| `update_sophon_download.go` | Sophon-aware 4-worker pool: each job = chunk download (CDN or local read) or patch blob download. Uses `sophon.ChunkDownload` / `sophon.LocalChunkRead`. Progress tracked via `progressStore.ChunkProgress[xxh64]=true` per success → enables crash resume. |
| `update_sophon_download_test.go` | Concurrency, cancel, progress accounting, crash resume from partial `ChunkProgress`. |
| `update_sophon_apply.go` | Per-category, per-file dispatch on flavor: `flavorSophonPatch` → `sophon.HDiffApply`; `flavorSophonBuild | flavorSophonFull` → `sophon.FileAssemble`. WAL append per file via new `sophon_apply.wal` sidecar. Apply lock + ordering: `game` category first, then audio langs alphabetical. |
| `update_sophon_apply_test.go` | Per-flavor flows; WAL resume; cross-device errno path. |
| `sophon_apply_wal.go` | New parallel WAL sidecar at `<versionSidecarDir>/sophon_apply.wal` (NOT the v1 `apply.wal`). Schema in §6.1. Reuses v1's atomic write pattern. |
| `sophon_apply_wal_test.go` | Read/write/round-trip; resume-from-mid-flight. |
| `sophon_manifest_cache.go` | On-disk manifest sidecar storage: `SaveAppliedManifest(gid, category, buildID, version, raw_pb_zst_bytes)`, `LoadAppliedManifests(gid) → []appliedManifestRecord`, `RotateAfterApply(gid, newBuildIDs)` — keeps latest + previous, GCs older. Atomic JSON `applied.json` index + raw .pb.zst blob files. |
| `sophon_manifest_cache_test.go` | Save/load round-trip; rotation correctness; concurrent reads safe. |
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
| `update_manifest.go` | `getGamePackages` → buildPlan zip+hdiff | NOT called for `UsesSophon=true` games. Still called for HSR/ZZZ. |
| `update_download.go` | Zip blob 4-worker pool | NOT called for Sophon. HSR/ZZZ unchanged. |
| `update_patch.go` | Zip extract + hdifffiles/hdiffmap parse | NOT called for Sophon. HSR/ZZZ unchanged. |
| `update_apply.go` | Atomic file rename WAL (`apply.wal`) | Still owned by HSR/ZZZ. Sophon uses `sophon_apply.wal` instead. RunUpdate's recovery dispatcher checks BOTH sidecars and routes accordingly. |

### Sidecar tree (v2 additions per game)

Paths layered on top of v1's actual layout: `gameSidecarDir(tempRoot, gid) = filepath.Join(tempRoot, flatGameID(gid))` and `versionSidecarDir(tempRoot, gid, ver) = filepath.Join(tempRoot, flatGameID(gid), ver)`.

```
<tempRoot>/<flat_gid>/                           # gameSidecarDir (v1)
  last_apply_target.json                         # v1, untouched
  sophon/                                        # NEW v2 cross-version sidecars
    manifests/
      <build_id>__<category>.manifest.pb.zst     # raw wire form, one per applied (build_id, category) pair
    applied.json                                 # { latest: {buildID, version, categoryIDs: {...}}, previous: {...} }

<tempRoot>/<flat_gid>/<target_version>/          # versionSidecarDir (v1)
  progress.json                                  # v1; extended with SophonStage + ChunkProgress
  apply.wal                                      # v1; unused for Sophon games
  sophon_apply.wal                               # NEW v2 typed-record WAL for Sophon apply phase
  predl_ready.json                               # v1 filename; v2 extends schema (see §7)
  staging/<target_build_id>/                     # NEW v2
    chunks/<chunk_xxh64>                         # downloaded + verified chunks awaiting assemble
    patches/<patch_blob_md5>                     # downloaded patch blobs awaiting HDiff apply
    hdiff_inputs/<patch_blob_md5>_<offset>.bin   # extracted patch blob slices for hpatchz
    assembled/<file_relpath>.tmp                 # being-assembled output files
```

`tempRoot` defaults to `<TEMP>/omnigate/hoyoverse` (set by `App.constructProviders` via `SetTempRootFn`). v1's tree shape is preserved verbatim; v2 adds `sophon/` at the per-game level and `staging/<build_id>/` + `sophon_apply.wal` at the per-version level.

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

v1 only parsed `main.tag`. v2 parses the full shape based on the live-API capture in `memory/project_m3b_v2_sophon.md` (Collapse's `HypGameInfoBranchData` lacks `categories` — that field was added by HoYoverse after Collapse last synced):

```go
type branchInfo struct {
    Main        branchSlot
    PreDownload branchSlot  // may be empty between releases — caller must check
}

type branchSlot struct {
    PackageID  string
    Branch     string             // "main" or "predownload"
    Password   string             // CDN URL signing token (NOT decryption key)
    Tag        string             // target version, e.g. "6.6.0"
    DiffTags   []string           // diff source versions, e.g. ["6.5.0", "6.4.0"]
    Categories []branchCategory   // CATEGORY_TYPE_RESOURCE + CATEGORY_TYPE_AUDIO
}

type branchCategory struct {
    CategoryID    string  // "10016" game, "10017"–"10020" audio langs
    MatchingField string  // "game" | "zh-cn" | "en-us" | "ko-kr" | "ja-jp"
    Type          string  // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
}

// IsEmpty: PackageID == "" — both branches start empty and we detect populated state by this
func (s branchSlot) IsEmpty() bool { return s.PackageID == "" }
```

If `Categories` is empty when `PackageID != ""` we treat the response as malformed and return `sophon_manifest_fetch_failed`.

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

**`compression` / `encryption` value parsing**: Collapse uses a `BoolConverter` because HoYoverse may serialize these as `0|1|"0"|"1"|true|false`. The `infos.go` Go types use a custom `boolish` type that accepts all forms.

**`encryption`** is treated as defensive telemetry: if observed non-zero (currently always zero per Collapse + live capture), log a warning AND if any `password` value is non-empty also log so we get an early signal of a HoYoverse encryption-rollout.

**`build_id` uniqueness**: assumed unique across (`main`, `pre_download`) branches per the same target version. v2 keys `staging/<target_build_id>/` directories by build_id; if HoYoverse re-uses build_ids across branches we'd get directory collisions. Plan-writing task adds a guard that records `(buildID, branch)` pair and asserts no cross-pair collision.

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
           int32                    AssetType    = 3;   // non-zero = directory
           int64                    AssetSize    = 4;
           string                   AssetHashMd5 = 5;
}

message SophonManifestAssetChunk {
  string ChunkName                = 1;   // CDN filename, first 16 hex = xxh64 of decompressed bytes
  string ChunkDecompressedHashMd5 = 2;
  int64  ChunkOnFileOffset        = 3;
  int64  ChunkSize                = 4;   // on-wire (possibly zstd-compressed)
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
  string                VersionTag = 1;   // diff source version, e.g. "6.5.0"
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
  string OriginalFileName   = 8;   // empty → CopyOver method; non-empty → HDiff
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

`SophonUnusedAssetProperty` lists files to **delete** as part of the patch (game files removed in the new version). Apply phase deletes these after WAL commit.

The patch-asset selection key per file is `SophonPatchAssetInfo.VersionTag` matching the user's `currentLocal`. If no matching `AssetInfos` entry exists, the file must come from the **main manifest** (DownloadOver path) — see §3 patch flow.

`go_package` puts generated code under `internal/providers/hoyoverse/sophon/proto/`.

### §2.5 Dependencies

New `go.mod` additions:
- `google.golang.org/protobuf` (latest stable)
- `github.com/klauspost/compress` (zstd subpackage; pure Go; CGO_ENABLED=0 friendly)
- `github.com/cespare/xxhash/v2`

Build prerequisites:
- `protoc` (system binary) — only required when regenerating `.pb.go` from `.proto` changes
- `protoc-gen-go` — installed via `cd internal/providers/hoyoverse/sophon/proto && go install google.golang.org/protobuf/cmd/protoc-gen-go` (driven by `tools.go` build-tag pattern)
- **Fresh checkout requires no `protoc`** because `.pb.go` files are committed
- README dev-setup section added with these notes

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

currentLocal := ReadGameVersion(gameDir)   // best-effort; "" on miss
if currentLocal == "" → UpdateError{"sophon_no_install", Retryable:false}

# Self-heal: applied chunks but config.ini writeback failed (v1 pattern)
if healed := maybeSelfHealSophon(currentLocal, branch.Main.Tag, gameDir, tempRoot, gid):
    return idle plan (Reason: ReasonNone)

if currentLocal == branch.Main.Tag → idle plan (Reason: ReasonNone, Files: [])

audioLangs := DetectInstalledLanguages(gameDir)              // [] OK; means no VO

# Old-manifest lookup for chunk-from-disk dedup (Path B)
prev := LoadAppliedManifests(tempRoot, gid)
oldMainManifest := prev.MatchByVersion(currentLocal, "game")  // nil if no match

# Decision
case currentLocal ∈ branch.Main.DiffTags:
    flavor = flavorSophonPatch
    plan, predl = buildSophonPatchPlan(branch.Main, audioLangs, currentLocal, tempRoot, gid)
case oldMainManifest != nil:
    flavor = flavorSophonBuild
    plan, predl = buildSophonBuildPlan(branch.Main, audioLangs, prev, gameDir, tempRoot, gid)
default:
    flavor = flavorSophonFull
    plan, predl = buildSophonBuildPlan(branch.Main, audioLangs, nil, gameDir, tempRoot, gid)
```

### §3.1 `buildSophonPatchPlan` — patch + main merge

Per `category ∈ {"game"} ∪ audioLangs`:
1. Fetch `getPatchBuild` → `SophonPatchProto` for category
2. Fetch `getBuild` → `SophonManifestProto` for **same** category (needed for fall-through)
3. For each `pa ∈ patchProto.PatchAssets`:
    - Find `info, ok := pa.AssetInfos[i].VersionTag == currentLocal`
    - If `!ok`: this file has no patch entry for this source version. Look it up in `mainProto.Assets` by name → emit `chunk_assemble` from main manifest (`DownloadOver` semantics; full chunks via Path B/C dedup against `oldMainManifest`)
    - If `ok && info.Chunk.OriginalFileName == ""`: emit `CopyOver{PatchName, PatchOffset, PatchLength, target=AssetName}` → patch blob slice is the entire target file
    - If `ok && info.Chunk.OriginalFileName != ""`: emit `Patch{patch_blob=PatchName[PatchOffset:PatchOffset+PatchLength], oldPath=OriginalFileName, target=AssetName, expected_md5=AssetHashMd5}`
4. For each `pa ∈ patchProto.UnusedAssets[].AssetInfos[].Assets` where outer `VersionTag == currentLocal`: emit `Delete{path=FileName, expected_md5=FileMd5}`
5. For each `ma ∈ mainProto.Assets` NOT covered by any patch entry above: emit `chunk_assemble` (Path B/C semantics)

Total bytes counted: Σ `info.Chunk.PatchLength` (unique by `PatchName` to dedup blob fetches) + Σ download chunks for main-only files.

### §3.2 `buildSophonBuildPlan` — full + chunk-from-disk dedup

Per `category ∈ {"game"} ∪ audioLangs`:
1. Fetch `getBuild` → `SophonManifestProto` for category
2. If `oldManifests != nil`, look up matching old per-category manifest (by `category_id` or `matching_field`); else skip dedup
3. For each `newAsset ∈ newProto.Assets` (`AssetType==0`, i.e. files):
    - Build `oldAssetMD5Idx`: scan `oldManifest.Assets` for matching `AssetName`; build `map[ChunkDecompressedHashMd5] → {OldOffset, OldSize}` from that asset's `AssetChunks`. Empty map if no match.
    - For each `chunk ∈ newAsset.AssetChunks`:
        - Lookup `chunk.ChunkDecompressedHashMd5` in `oldAssetMD5Idx`
        - Hit: emit `ChunkSource{kind=Local, oldPath=newAsset.AssetName, oldOffset=match.OldOffset, size=match.OldSize}`
        - Miss: emit `ChunkSource{kind=CDN, url_prefix=category.chunk_download.url_prefix, chunkName=chunk.ChunkName, decompressed_size=chunk.ChunkSizeDecompressed, compressed_size=chunk.ChunkSize, md5=chunk.ChunkDecompressedHashMd5}`

Total bytes counted: Σ `chunk.ChunkSize` for CDN chunks only (compressed wire bytes; user-perceived download).

### §3.3 Predownload detection

`predlAvail = !branch.PreDownload.IsEmpty() && currentLocal != branch.PreDownload.Tag && currentLocal != ""`. If true, repeat §3.1 or §3.2 on `branch.PreDownload` to produce a parallel predl plan (cached in `genshinPlan.predlPlan *predlPlan`). UI's predl button → `Provider.RunUpdate` with `plan.Kind = PlanPredownload`.

### §3.4 Reason codes

Reuses v1's `core.UpdatePlan.Reason`:
- `ReasonVersionChanged` (currentLocal != branch.Main.Tag and chosen flavor != idle)
- `ReasonResumeInterrupted` (sophon_apply.wal present OR ChunkProgress non-empty)
- `ReasonAudioPackChanged` (audioLangs diff vs prior; v1 covers)

No new `Reason` constants. Runtime errors surface via `core.UpdateError.Code` (see §6.7).

### §3.5 `maybeSelfHealSophon`

Mirrors v1's `maybeSelfHeal` exactly:
- Read `last_apply_target.json` at `gameSidecarDir/last_apply_target.json`
- If `lat.TargetVersion == branch.Main.Tag` AND `currentLocal != branch.Main.Tag` AND 24h since `lat.LastWritebackRetryTS`:
    - Attempt `WriteGameVersion(gameDir, branch.Main.Tag)` again
    - On success: update `lat.ConfigWritebackOK = true`, return healed=true
    - On failure: bump `lat.LastWritebackRetryTS`, return healed=false (real download path resumes)

Same 24h budget, same `last_apply_target.json` semantics. The Sophon apply path writes `last_apply_target.json` at apply-success (§6.2 step 4) — same field shape v1 uses.

---

## §4. Manifest cache + reuse layer

### §4.1 Storage layout

```
<tempRoot>/<flat_gid>/sophon/
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
- For each `(category, build_id)` in latest + previous, lazy-load the corresponding `.manifest.pb.zst` file on demand via `MatchByVersion(version, category)`
- `MatchByVersion` returns a parsed `*pb.SophonManifestProto` or nil if no slot matches version+category

The proto is NOT held in memory across `CheckForUpdate` calls; it's loaded fresh and discarded after planning.

### §4.3 Cache rotation (write-order corrected)

After apply for build B (target version V) succeeds:
1. **Capture** the current `applied.json.previous.build_id` (if any) → `evictedBuildID`
2. **Build** new `applied.json`:
    - `latest` = `{B, V, now, categoryIDs}`
    - `previous` = (current `latest`, if any; else null)
3. Atomic write new `applied.json` (write `applied.json.tmp` → fsync → rename)
4. If `evictedBuildID != ""`: delete `manifests/<evictedBuildID>__*.manifest.pb.zst`

Result: at most 2 prior builds × N categories on disk. Storage bound ~10–40 MB (manifests for 5 categories × 2 builds × ~few MB each).

### §4.4 Stale entry recovery (per-chunk MD5 mismatch)

When `local_chunk_read.go` reads at `(oldFile, oldOffset, size)` and computed MD5 disagrees with manifest's expected MD5:
1. Return `ErrChunkStale` from `LocalChunkRead`
2. Worker pool re-classifies the chunk source as CDN (`Path B's lookup result was wrong because the on-disk file was edited`)
3. Log warning with `file`, `offset`, expected/actual MD5

No persistent state change. The old manifest stays valid for OTHER chunks (most assets ARE untouched). Stale-detection is per-chunk-read.

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

Output paths (under `<versionSidecarDir>/staging/<target_build_id>/`):
- `jobChunkCDN` / `jobChunkLocal` → `chunks/<chunk_xxh64>` (xxh64 of decompressed bytes is deterministic; same chunk requested by different files writes the same file once)
- `jobPatchBlob` → `patches/<patch_blob_md5>` (deterministic from manifest)

Worker dispatches on `kind`:
- `jobChunkCDN` → `sophon.ChunkDownload` (HTTP GET + `ctxReader`-wrapped + zstd decompress if compressed + xxh64 verify on write + atomic rename to `out`). If `out` already exists and verifies: skip download.
- `jobChunkLocal` → `sophon.LocalChunkRead` (open `local.oldFile`, seek `local.oldOffset`, read `local.size`, MD5 verify against expected, write to `out`; `ErrChunkStale` → requeue as `jobChunkCDN`)
- `jobPatchBlob` → HTTP GET full patch blob → MD5 verify → atomic rename to `out`. If `out` exists and verifies: skip.

### §5.2 Per-chunk retry + cross-run resume

Per `jobChunkCDN`: 3× exponential backoff (1s / 4s / 16s). After 3 failures, the chunk is recorded as **not-done** in `progressStore.ChunkProgress` and surfaces as `UpdateError{Code:"sophon_chunk_verify_failed", Retryable:true}` (retryable because user-initiated rerun resumes).

Cross-run resume: successful chunks (staging file exists + xxh64 verifies) stay on disk between runs. On rerun:
1. Iterate plan's chunk sources
2. For each, check if `staging/<build_id>/chunks/<xxh64>` exists and xxh64-verifies
3. If yes: mark `ChunkProgress[xxh64]=true` in `progressStore` and skip download
4. If no: requeue as fresh job

`local_chunk_read.go` reads are NOT cached; they're always re-executed from the old file because cache-hit would just mean another disk read.

### §5.3 Progress accounting

`UpdateEvent.Current` = decompressed bytes written across all workers (atomic counter incremented per chunk-completion).
`UpdateEvent.Total` = Σ `chunk.ChunkSizeDecompressed` for CDN chunks + Σ `local.size` for local chunks + Σ `PatchLength` for patch blobs.

**Implementation note**: the bar represents "bytes assembled into staging", which includes disk-only reads (Path B local chunks). This is intentional UX — the bar advances even when no network traffic happens, mirroring HoYoPlay behavior. The spec consciously trades "network throughput" semantics for "work-completed" semantics.

### §5.4 Cancel

`ctxReader` wraps any `io.Reader` and returns `ctx.Err()` on `Read` if the context is cancelled. Used to wrap both raw HTTP body and zstd decompression streams so cancel propagates through compression. Worker pool also checks `ctx.Done()` between jobs.

---

## §6. Apply layer

### §6.1 `sophon_apply.wal` schema

NEW parallel sidecar at `<versionSidecarDir>/sophon_apply.wal`. v1's `apply.wal` (flat `Pending []string` / `Done []string`) is preserved for HSR/ZZZ; v2 Sophon writes only `sophon_apply.wal`.

```go
type sophonApplyWAL struct {
    GameID       string                `json:"game_id"`
    TargetTag    string                `json:"target_tag"`
    BuildID      string                `json:"build_id"`
    SourceTag    string                `json:"source_tag"`    // "" for flavorSophonFull
    Flavor       string                `json:"flavor"`        // planFlavor.String()
    WasPredl     bool                  `json:"was_predl"`
    Records      []sophonApplyRecord   `json:"records"`
}

type sophonApplyRecord struct {
    Kind      string `json:"kind"`        // "chunk_assemble" | "hdiff_patch" | "copy_over" | "delete"
    Category  string `json:"category"`    // "game" | "en-us" | ...
    Path      string `json:"path"`        // target relative to gameDir
    State     string `json:"state"`       // "pending" | "done"

    // chunk_assemble (Path B/C)
    AssetMD5  string `json:"asset_md5,omitempty"`        // whole-file MD5 to verify pre-rename

    // hdiff_patch (Path A)
    OldPath   string `json:"old_path,omitempty"`         // relative to gameDir
    PatchTmp  string `json:"patch_tmp,omitempty"`        // staging patches/<md5>
    PatchOff  int64  `json:"patch_off,omitempty"`
    PatchLen  int64  `json:"patch_len,omitempty"`

    // copy_over (Path A)
    CopyTmp   string `json:"copy_tmp,omitempty"`         // staging patches/<md5>
    CopyOff   int64  `json:"copy_off,omitempty"`
    CopyLen   int64  `json:"copy_len,omitempty"`

    // delete (UnusedAssets)
    ExpectMD5 string `json:"expect_md5,omitempty"`       // optional pre-delete verification
}
```

`Records` is written in apply order. State transitions happen pessimistically (write `pending` → execute → rewrite WAL marking `done` → continue). The WAL is rewritten in full on each state transition (small file, atomic rename pattern). For 5K records (large patch with many small files), each rewrite is ~500 KB — acceptable.

### §6.2 Apply ordering

1. Acquire `applyLock` (v1 pattern, version-scoped)
2. Read or create `sophon_apply.wal`. If exists with `Records[i].State == "pending"`, resume from first pending; else build records from `genshinPlan.sophonChunkSources + sophonPatches` and write fresh WAL
3. Order categories: `"game"` first; audio langs alphabetical
4. Per category, per file (manifest order): execute record → fsync → atomic rename → mark `done` → rewrite WAL
5. After all records done: `WriteGameVersion(gameDir, branch.Main.Tag)` + write `last_apply_target.json` (warn-log on permission error, set `ConfigWritebackOK=false`)
6. Rotate `sophon/applied.json` per §4.3
7. Delete `<versionSidecarDir>/staging/<build_id>/` contents + `sophon_apply.wal`
8. Release `applyLock`

### §6.3 `chunk_assemble` record execution

For `Kind == "chunk_assemble"`:
1. `out, _ := os.OpenFile(<staging>/assembled/<path>.tmp, RDWR|CREATE|TRUNC, 0o644)`
2. `out.Truncate(<expected_file_size>)` (from manifest `AssetSize`)
3. For each `chunk` in the manifest's `newAsset.AssetChunks`: read its staging file `<staging>/chunks/<chunk_xxh64>` (xxh64-verified in download phase, decompressed bytes) → `out.WriteAt(bytes, chunk.ChunkOnFileOffset)`
4. `out.Sync()`, `out.Close()`
5. Compute whole-file MD5, compare against `AssetMD5`. Mismatch → delete `out`, surface `UpdateError{"sophon_apply_failed", Retryable:false}`
6. Atomic rename `<staging>/assembled/<path>.tmp` → `<gameDir>/<path>`
7. Mark WAL `done`

### §6.4 `hdiff_patch` record execution

For `Kind == "hdiff_patch"`:
1. Write slice `PatchTmp[PatchOff:PatchOff+PatchLen]` to `<staging>/hdiff_inputs/<patchMD5>_<offset>.bin` (if not already there — deterministic path)
2. `hpatchz.Run(ctx, <gameDir>/<OldPath>, <hdiff_input>, <staging>/assembled/<Path>.tmp)`
3. Whole-file MD5 verify against `AssetMD5` (set from `SophonPatchAssetProperty.AssetHashMd5` at plan time)
4. Atomic rename `<staging>/assembled/<Path>.tmp` → `<gameDir>/<Path>`
5. Mark WAL `done`

### §6.5 `copy_over` record execution

For `Kind == "copy_over"`:
1. Write slice `CopyTmp[CopyOff:CopyOff+CopyLen]` to `<staging>/assembled/<Path>.tmp`
2. Whole-file MD5 verify against `AssetMD5`
3. Atomic rename → `<gameDir>/<Path>`
4. Mark WAL `done`

### §6.6 `delete` record execution (UnusedAssets)

For `Kind == "delete"`:
1. Optional: stat `<gameDir>/<Path>`, compute MD5, compare against `ExpectMD5`. Mismatch → log warn (user may have modded file), continue.
2. `os.Remove(<gameDir>/<Path>)` (ignore ENOENT — already deleted)
3. Mark WAL `done`

### §6.7 Runtime error codes (surfaced via `core.UpdateError.Code`)

| Code | Cause | Retryable |
|---|---|---|
| `sophon_no_install` | `currentLocal == ""` | false |
| `sophon_manifest_fetch_failed` | branch/manifest HTTP error | true |
| `sophon_chunk_verify_failed` | 3× retry exhausted on a chunk | true |
| `sophon_apply_failed` | whole-file MD5 mismatch or `hpatchz` non-zero exit | false |
| `predl_stale` | `predl_ready.json` target version doesn't match current branch.PreDownload.Tag | false (deletes staging, falls through to fresh predl) |

### §6.8 Cross-device errno during rename

Reuses v1's `cross_device_windows.go` / `_other.go` errno detection. Sophon assemble + copy_over both go through a `safeAtomicRename` wrapper that falls back to copy+delete on `EXDEV`.

### §6.9 Resume from crash

`RunUpdate`'s recovery dispatcher (in `hoyoverse.go`):
1. If `<versionSidecarDir>/sophon_apply.wal` exists → load → resume from first pending Sophon record (§6.2 step 2 already covers)
2. Else if `<versionSidecarDir>/apply.wal` exists → v1 path (HSR/ZZZ)
3. Else if `<versionSidecarDir>/progress.json` has non-empty `ChunkProgress` → resume download phase (mark already-done chunks as skip)
4. Else: fresh run from `manifestCache.get(gid)` (must have been planned by recent CheckForUpdate)

If `manifestCache` miss AND download progress exists, re-call `CheckForUpdate` to rebuild plan. If both miss, surface `UpdateError{"sophon_manifest_fetch_failed"}` (we cannot proceed safely).

---

## §7. Predownload

### §7.1 Flow

1. `CheckForUpdate` detects `branch.PreDownload != empty && currentLocal != branch.PreDownload.Tag && currentLocal != ""` → sets `predlAvailable = true` on cached `genshinPlan`
2. App.GetPredownloadAvailable returns true → UI shows predl button (v1 wiring untouched)
3. User clicks predl → App.StartPredownload → Provider.RunUpdate with `plan.Kind = PlanPredownload`
4. RunUpdate dispatches Sophon flavor as `flavorSophonPredlPatch` or `flavorSophonPredlBuild` (no `Full` predl — predl implies installed game)
5. Download phase runs identically; staging tree at `staging/<predl_build_id>/`
6. **Apply phase is SKIPPED**. Instead, write `predl_ready.json` (extending v1's `predlReadyFile` with Sophon fields):

```go
type sophonPredlReadyFile struct {
    core.ProgressFile                            // v1 base
    Kind            string `json:"kind"`         // "sophon_patch" | "sophon_build"
    BuildID         string `json:"build_id"`
    SourceVersion   string `json:"source_version"`  // currentLocal at predl-stage time
    TargetVersion   string `json:"target_version"`  // == branch.PreDownload.Tag at predl-stage time
    AudioLanguages  []string `json:"audio_languages"`
    StagedAt        string `json:"staged_at"`    // RFC3339
    PlanSnapshot    sophonPlanSnapshot `json:"plan_snapshot"` // captures sophonChunkSources + sophonPatches
}
```

`sophonPlanSnapshot` is a serializable form of the in-memory `genshinPlan.sophonChunkSources`/`sophonPatches` that's complete enough for apply phase to run without re-fetching manifests. Plan-writing task pins exact fields based on what `update_sophon_apply.go` needs.

7. When the live update unlocks (subsequent CheckForUpdate sees `currentLocal == predl.SourceVersion` AND `branch.Main.Tag == predl.TargetVersion`): if `predl_ready.json` exists at version-dir for `predl.TargetVersion`, set `genshinPlan.predlConsume = true` + reuse `PlanSnapshot` directly in `RunUpdate` → skip download phase entirely → go straight to apply phase

### §7.2 Stale predl detection

On `CheckForUpdate`, after computing `branch.PreDownload`:
- Read `<versionSidecarDir(predl_target)>/predl_ready.json`
- If `predl_ready.TargetVersion != branch.PreDownload.Tag`: stale — emit `UpdateError{"predl_stale", Retryable:false}`. Caller deletes the staging tree, falls through to a fresh predl plan.
- If `predl_ready.SourceVersion != currentLocal`: also stale (user updated via HoYoPlay between predl and now).
- If `predl_ready.Kind == "sophon_patch"` and `currentLocal ∉ branch.PreDownload.DiffTags`: stale (predl was patch-based but the diff window no longer covers user).

### §7.3 Predl→update handoff dispatch

In `RunUpdate`:
1. If `plan.Kind == PlanUpdate` and `genshinPlan.predlConsume == true`: load `predl_ready.json` for current `branch.Main.Tag`, hydrate `genshinPlan.sophonChunkSources` and `genshinPlan.sophonPatches` from `PlanSnapshot` (overrides whatever was built at plan time — they should agree, but predl is authoritative for already-staged content)
2. Verify all expected chunks/patches exist in `staging/<predl_build_id>/` and verify their hashes (xxh64 / MD5). Any failure → fall through to fresh download (don't trust predl content)
3. Skip download phase, go directly to apply phase with WAL build from hydrated plan

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
| `update.error.predl_stale` | 「預下載已過期，將重新下載」 | "Predownload outdated; restarting" | 「预下载已过期，将重新下载」 |

### §8.3 Component changes

**Zero**. BottomBar, SidebarRow, Topbar, ConfirmDialog, ToastHost, updates store all keep current behavior. The `updates_store` already serializes `last_error.code` and `last_error.params` as string + map[string]string; new codes flow through without code changes. Bell drawer entries map error codes via i18n.

### §8.4 Vitest

`i18n_parity.test.ts` auto-covers new keys via its existing parity walk. Add 1 case to `BottomBar.test.ts` covering `sophon_no_install` error rendering (just confirms the i18n lookup path; not a snapshot).

---

## §9. Testing strategy

### §9.1 Unit tests

~50 functions across the new files listed in §1. Highlights:
- `sophon/dedup_test.go`: per-asset isolation (chunk in A invisible from B query); MD5 key collision intra-asset; empty old manifest
- `sophon/decision_test.go`: 6 path cases — Patch-with-DiffTags-hit / Build-with-old-manifest / Full-no-cache / Patch-but-missing-from-patch-falls-to-main / NoInstall / IdleSameVersion
- `sophon/file_assemble_test.go`: chunks out of file order; chunks-not-page-aligned offsets; cross-device errno fallback
- `sophon/chunk_download_test.go`: zstd-compressed path; raw path; xxh64 mismatch retry; xxh64 mismatch after 3 retries → ErrVerifyExhausted; xxh64 parse fail → MD5 fallback
- `sophon/hdiff_apply_test.go`: Patch / CopyOver / DownloadOver branches; cross-device errno

### §9.2 Integration tests

Extend `integration_test.go` (currently 9 `t.Skip` placeholders). Use TWO distinct fixture sets to ensure a bug in one fixture doesn't mask all scenarios:
- `testdata/sophon/sample.*` — 5-file × 3-chunk per category
- `testdata/sophon/tiny.*` — 1-file × 1-chunk minimal

`fakeSophonServer` (httptest) mux:
- GET `/getGameBranches` → canned `branches_*.json`
- GET `/getBuild` + `getPatchBuild` → canned `build_*.json` / `patch_*.json`
- GET CDN manifest paths → raw `.manifest.pb.zst` / `.patch.pb.zst` bytes
- GET CDN chunk paths → raw `chunks/<chunk_xxh64>` bytes

Scenarios (run against both fixture sets where independent):
1. `TestSophonFlavorPatch_EndToEnd` — currentLocal in DiffTags → HDiff path → final files match expected MD5
2. `TestSophonFlavorPatch_FilesNotInPatch_FromMain` — patch covers 3 of 5 files; 2 fall through to main `getBuild` → final state matches
3. `TestSophonFlavorBuild_EndToEnd` — old manifest available → some chunks Local, some CDN
4. `TestSophonFlavorBuild_StaleLocalChunk_FallbackCDN` — disk file modified → MD5 mismatch on local read → CDN fallback succeeds
5. `TestSophonFlavorFull_EndToEnd` — no cache → all chunks from CDN
6. `TestSophonPredl_StagingThenLiveApply` — predl run → next CheckForUpdate finds predl_ready → apply skips download
7. `TestSophonPredlStale_TargetMismatch` — `predl_ready.target` != current branch.PreDownload.Tag → predl_stale error → recover with fresh download
8. `TestSophonPredl_CurrentLocalEqPredlTag` — `currentLocal == branch.PreDownload.Tag` → predl button not shown (predlAvailable=false)
9. `TestSophonResume_ApplyWALMidFlight` — kill mid-apply (after 2 of 5 records done) → restart → resume completes
10. `TestSophonResume_DownloadCrashMidChunks` — kill mid-download (3 of 10 chunks done, marked in ChunkProgress) → restart → 7 remaining chunks re-fetched, 3 reused from staging
11. `TestSophonChunkVerifyFail_RetryThenSucceed` — first GET returns corrupt body, second OK
12. `TestSophonChunkVerifyFail_RetryExhausted` — all 3 retries corrupt → surface `sophon_chunk_verify_failed`
13. `TestSophonNoInstall_Error` — `config.ini` missing → `sophon_no_install`
14. `TestSophonHSRZZZ_LegacyPathUnchanged` — non-Sophon games still go through v1 zip+hdiff
15. `TestSophonMaybeSelfHeal_WritebackRetry` — currentLocal stale, `last_apply_target` matches target, 24h passed → self-heal succeeds → idle plan returned
16. `TestSophonCancelMidChunkDownload` — `ctx.Cancel()` during zstd stream → worker exits cleanly → progressStore reflects partial state
17. `TestSophonCrossDeviceErrno_AssembleRename` — assemble succeeds but rename returns EXDEV → fallback copy+delete succeeds
18. `TestSophonDiffTagsEmpty_FallsToBuildOrFull` — `branch.Main.DiffTags = []` → never hits Patch flavor → Build (if cache) or Full
19. `TestSophonMainCategoriesEmpty_Error` — malformed branch response → `sophon_manifest_fetch_failed`
20. `TestSophonUnusedAssetsDeleted` — `SophonUnusedAssetProperty` entries trigger `delete` WAL records → file removed from gameDir

### §9.3 Fuzz

- `FuzzSophonManifestProto_Parse` — random zstd-protobuf bytes → no panic
- `FuzzSophonPatchProto_Parse` — same
- `FuzzAppliedJSON_Serde` — random JSON → no panic on Load
- `FuzzPatchBlobSlice` — random `PatchOffset`/`PatchLength` against random blobs → no panic / no out-of-bounds

### §9.4 Bench

- `BenchmarkBuildPerAssetMD5Index_LargeManifest` — 25K total chunks across 500 assets → verify < 100ms
- `BenchmarkDedupLookup_1MOps` — verify > 5M lookups/sec
- `BenchmarkChunkAssemble_3GBFile` — disk-bound; sanity check (≥ disk throughput minus 10%)

---

## §10. Risks, known limitations, future scope

### Known limitations (accepted in v2)

1. **Fresh install not supported** — Omnigate's Sophon path requires an existing install detected via `config.ini`. Initial install routes through HoYoPlay.
2. **`plat_app` hardcoded** — If HoYoverse rotates per-game `plat_app` values, Sophon API calls 4xx until we ship a fix. Mitigation: extract from HoYoPlay local metadata at runtime (deferred to v2.1 if rotation becomes a problem).
3. **No HTTP byte-range for chunks** — Collapse does not use byte-range; we follow suit. Whether HoYoverse CDN supports them is untested. A mid-chunk network drop costs the chunk's worth of bytes on retry. Chunks typically ~10 MB.
4. **3+ versions behind without cached old manifest** — Triggers full download via Path C (no chunk-from-disk dedup possible without prior manifest). Real savings depend on how much actually changed.
5. **CN region not supported** — `meta.go` still only registers `global`. CN deferred to a future M-something.
6. **Manifest decryption code not written** — `password` field treated as inert. Defensive telemetry logs non-zero `encryption` flag AND non-empty `password` so we get early signal if HoYoverse flips it on.
7. **ZZZ may migrate to Sophon mid-v2** — Memory's live-API probe (2026-06-01) shows ZZZ still on legacy `getGamePackages`; if HoYoverse migrates ZZZ between now and v2 ship, we flip `UsesSophon=true` on `meta.go` and re-smoke (one-line code change; no design change).

### Spec deviations from v1 (carried forward)

1. `planFlavor` enum gets 5 new constants; existing 6 v1 flavors untouched.
2. `genshinPlan` struct gets 5 new Sophon-only fields. Existing v1 fields untouched.
3. New parallel WAL sidecar `sophon_apply.wal` with typed records; v1's `apply.wal` flat string lists untouched (still used by HSR/ZZZ).
4. `progressStore` schema gets optional `SophonStage` + `ChunkProgress` fields. v1 sidecars without these load fine.
5. Sidecar tree adds `sophon/` subdir at `gameSidecarDir`, and per-version: `sophon_apply.wal` + `staging/<build_id>/`. Existing v1 sidecars at their existing paths.
6. v1's `predlReadyFile` schema is extended for Sophon predl with new fields under the same JSON object (`Kind`, `BuildID`, `SourceVersion`, `TargetVersion`, etc.). v1 legacy predl (HSR/ZZZ) doesn't write these fields; v1 reader code ignores unknown fields per `encoding/json` defaults.

### Future scope (NOT in v2)

1. SQLite-backed cross-game state + gacha record tracking (separate milestone; see `memory/project_future_sqlite.md`)
2. HSR / ZZZ Sophon migration (flip `UsesSophon` flag when HoYoverse migrates them; verify `plat_app` discovery)
3. Per-game `plat_app` runtime discovery (HoYoPlay local metadata scan)
4. CN region support
5. Fresh-install flow (would require deeper HoYoPlay metadata reverse-engineering)
6. Settings UI for audio language management (current behavior: locked to installed langs detected on disk)

---

## §11. Open questions answered during 1st spec review (closed)

| Q | Answer | Source |
|---|---|---|
| Are `.pb.go` files committed or regenerated at build? | **Committed.** Plan-writing pins this. | Spec §2.5 |
| `tools.go` vs Makefile for protoc-gen-go bootstrap? | **`tools.go`** (Go-native). | Spec §1 sophon/proto/tools.go |
| Manifest/chunk URL signing — query token, basic auth, header? | Path-appended chunk name; **no password** appended at fetch time. Collapse `Extension.cs:441-470`. | Spec §5.1 |
| `getGameBranches` `pre_download` key shape? | snake_case `pre_download`, same shape as `main`. | Collapse `HypLauncherSophonBranchesApi.cs` |

---

## §12. Authorization model

**Batch autonomous** (confirmed 2026-06-01).

Subagent-driven-development loop runs Tasks 1–N without per-task user checkpoint. Stop conditions: (a) implementer subagent BLOCKED on the same task twice consecutively, (b) reviewers cannot reconcile, (c) plan-level error discovered, or (d) the final USER smoke task. Per-task reviewer findings against plan-verbatim code are overridden (M3.A / v1 established pattern in `memory/feedback_autonomous_m3a_batch.md` / `feedback_autonomous_m3b_batch.md`).

This grant should be captured in a new memory file `feedback_autonomous_m3b_v2_batch.md` at plan-writing time and marked EXPIRED at v2 ship.
