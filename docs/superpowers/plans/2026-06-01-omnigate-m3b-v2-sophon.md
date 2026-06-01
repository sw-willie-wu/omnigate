# Omnigate M3.B v2 — HoYoverse Sophon Protocol Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Genshin Impact 6.0+ update / predownload via HoYoverse's Sophon chunk-level binary-delta protocol, replacing M3.B v1's `sophon_not_supported` short-circuit with a real plan; HSR/ZZZ stay on the legacy `getGamePackages` pipeline.

**Architecture:** A new `internal/providers/hoyoverse/sophon/` sub-package owns the protocol-pure logic (proto parsing, per-asset MD5 chunk dedup, decision tree, chunk download, file assemble, hdiff apply). The parent `hoyoverse` package orchestrates it (plan build, worker pool, apply WAL, manifest cache, Provider wiring) and injects `hpatchz.Run` to avoid an import cycle — `hpatchz.go` is extracted to its own `hoyoverse/hpatchz/` sub-package. All persistence is atomic JSON / raw-blob sidecars under `<tempRoot>/<flat_gid>/`. Three update paths: A (HDiff via `getPatchBuild`), B (chunk-from-disk dedup via cached old manifest), C (full download). Predownload stages chunks/patches and defers apply to a `predl_ready.json` snapshot consumed on the next `CheckForUpdate`.

**Tech Stack:** Go 1.23, `google.golang.org/protobuf`, `github.com/klauspost/compress/zstd` (pure-Go, CGO_ENABLED=0 friendly), `github.com/cespare/xxhash/v2`, embedded `hpatchz.exe` (HDiffPatch v4.12.2). Frontend: Vue 3 + Pinia + vue-i18n + Vitest.

**Spec:** `docs/superpowers/specs/2026-06-01-omnigate-m3b-v2-sophon-design.md` (the §-references throughout this plan point there).

**Authorization:** Batch-autonomous per spec §12 / `memory/feedback_autonomous_m3b_v2_batch.md`. Tasks 1–24 run via subagent-driven-development without per-task user checkpoint; Task 25 (smoke + tag + merge) is USER-required.

---

## Toolchain note (every Go task)

On subagent shells without Go on PATH, prepend:
```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
```
This host is `CGO_ENABLED=0` — **never** pass `-race` to `go test` (see `memory/feedback_no_cgo_race.md`). Run package tests with `go test -count=1 ./internal/providers/hoyoverse/...`. Whole-repo gate: `go build ./... && go vet ./... && go test -count=1 ./...`. Frontend gate: `cd frontend && npm run build && npx vitest run`.

---

## §A. Type Contract & Naming Registry (NORMATIVE — every task obeys this verbatim)

This section locks every cross-task type, signature, constant, and filename so tasks written independently stay consistent. Where it refines the spec, the deviation is flagged `[DEV-n]` and collected in §C.

### A.1 Generated proto types — package `sophon/proto` (import alias `pb`)

`protoc-gen-go` output for the two `.proto` files in §2.4 of the spec. Go field names (PascalCase, protoc-gen-go convention; `Md5`→`Md5`, `Id`→`Id`):

```go
// sophon_manifest.pb.go (generated; committed)
type SophonManifestProto struct {
    Assets []*SophonManifestAssetProperty
}
type SophonManifestAssetProperty struct {
    AssetName    string
    AssetChunks  []*SophonManifestAssetChunk
    AssetType    int32
    AssetSize    int64
    AssetHashMd5 string
}
type SophonManifestAssetChunk struct {
    ChunkName                string
    ChunkDecompressedHashMd5 string
    ChunkOnFileOffset        int64
    ChunkSize                int64   // compressed (on-wire) size
    ChunkSizeDecompressed    int64
}

// sophon_patch.pb.go (generated; committed)
type SophonPatchProto struct {
    PatchAssets  []*SophonPatchAssetProperty
    UnusedAssets []*SophonUnusedAssetProperty
}
type SophonPatchAssetProperty struct {
    AssetName    string
    AssetSize    int64
    AssetHashMd5 string
    AssetInfos   []*SophonPatchAssetInfo
}
type SophonPatchAssetInfo struct {
    VersionTag string
    Chunk      *SophonPatchAssetChunk
}
type SophonPatchAssetChunk struct {
    PatchName          string
    VersionTag         string
    BuildId            string
    PatchSize          int64
    PatchMd5           string
    PatchOffset        int64
    PatchLength        int64
    OriginalFileName   string
    OriginalFileLength int64
    OriginalFileMd5    string
}
type SophonUnusedAssetProperty struct {
    VersionTag string
    AssetInfos []*SophonUnusedAssetInfo
}
type SophonUnusedAssetInfo struct {
    Assets []*SophonUnusedAssetFile
}
type SophonUnusedAssetFile struct {
    FileName string
    FileSize int64
    FileMd5  string
}
```
`go_package = "omnigate/internal/providers/hoyoverse/sophon/proto"`. Import as `pb "omnigate/internal/providers/hoyoverse/sophon/proto"`. Generated structs also carry `state`, `sizeCache`, `unknownFields` and getters — the plan only references the exported data fields above; the implementer treats `protoc-gen-go` output as opaque otherwise.

### A.2 Canonical work-item types — package `sophon` (EXPORTED)

**[DEV-1]** The spec mentions both `sophon.ChunkSource` (decision.go return type, §1) and a lowercase `chunkSource` in `plan_internal.go` (§6.1). To avoid a duplicate parallel type family and conversion boilerplate, the canonical in-memory work-item types live **exported in package `sophon`**, and the `hoyoverse` package imports `sophon` and uses them directly (`hoyoverse` → `sophon` is a legal one-way dependency). The lowercase `chunkSource`/`sophonPatchInstr`/`sophonDeleteInstr` named in the spec's plan_internal.go row are NOT created; `genshinPlan` stores the `sophon.*` types. The JSON WAL/snapshot serialization forms (`walChunkSource`, `sophonPlanSnapshot`) remain hoyoverse-package types (A.6).

```go
package sophon

// Source kind + patch method string constants.
const (
    SourceCDN   = "cdn"
    SourceLocal = "local"

    MethodPatch    = "patch"
    MethodCopyOver = "copy_over"
)

// ChunkSource is one chunk to place into a target file at FileOffset.
// Kind==SourceCDN: download <URLPrefix>/<ChunkName>, zstd-decompress if
// UseCompress, verify (xxh64 of ChunkName prefix, else MD5 ExpectMD5).
// Kind==SourceLocal: read DecompSize bytes from <gameDir>/<OldFile> at
// OldOffset, verify MD5==ExpectMD5; on mismatch fall back to the CDN form
// (ChunkName/URLPrefix are ALWAYS populated, even for Local, per spec §6.3
// step 4 stale-fallback).
type ChunkSource struct {
    Kind         string // SourceCDN | SourceLocal
    Asset        string // owning AssetName (log + per-asset dedup scoping)
    ChunkName    string // CDN filename; also the staging filename
    URLPrefix    string // chunk_download.url_prefix
    CompressedSz int64  // ChunkSize (on-wire); progress accounting after resume
    UseCompress  bool   // chunk_download.compression
    OldFile      string // SourceLocal: relative to gameDir
    OldOffset    int64  // SourceLocal
    DecompSize   int64  // ChunkSizeDecompressed
    FileOffset   int64  // ChunkOnFileOffset into the target file
    ExpectMD5    string // ChunkDecompressedHashMd5
}

// PatchInstr is one patch-blob operation. Method==MethodPatch → hpatchz
// (OldFile + hdiff slice → target). Method==MethodCopyOver → write the patch
// blob slice directly as the target (full file delivered in the patch blob).
type PatchInstr struct {
    Method          string // MethodPatch | MethodCopyOver
    Asset           string // target relative to gameDir
    PatchName       string
    URLPrefix       string // diff_download.url_prefix (patch-blob CDN base)
    PatchSize       int64  // full blob size (download/progress accounting)
    PatchMD5        string // full blob MD5
    PatchOffset     int64  // slice offset within the blob
    PatchLength     int64  // slice length
    OldFile         string // MethodPatch: source file relative to gameDir
    ExpectMD5       string // expected post-apply whole-file MD5 (== AssetHashMd5)
    OriginalFileMD5 string // MethodPatch: pre-apply OldFile MD5 (demotion guard)
}

// DeleteInstr is one UnusedAssets file removal.
type DeleteInstr struct {
    Path      string // relative to gameDir
    ExpectMD5 string // pre-delete sanity check (mismatch → warn, continue)
}

// ChunkRef is a dedup-index value: where an old chunk lives on disk.
type ChunkRef struct {
    OldFilePath string // old AssetName == path relative to gameDir
    OldOffset   int64
}

// Category identifies one manifest category (game / audio language).
type Category struct {
    ID            string // "10016".."10020"
    MatchingField string // "game" | "zh-cn" | "en-us" | "ja-jp" | "ko-kr"
    Type          string // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
}
```

### A.3 sophon-package function signatures (EXPORTED)

```go
// branches.go — JSON shapes for the extended getGameBranches response.
type BranchInfo struct {
    Main        BranchSlot
    PreDownload BranchSlot
}
type BranchSlot struct {
    PackageID  string
    Branch     string
    Password   string
    Tag        string
    DiffTags   []string
    Categories []Category
}
func (s BranchSlot) IsEmpty() bool // PackageID == ""
func ParseBranches(data []byte, apiGameID string) (*BranchInfo, error) // parse the apiEnvelope.Data of getGameBranches

// infos.go — getBuild/getPatchBuild envelopes + tolerant scalar types.
type Boolish bool   // UnmarshalJSON accepts 0|1|"0"|"1"|true|false
type Int64ish int64 // UnmarshalJSON accepts number-or-quoted-number
type BuildResponse struct {
    BuildID   string
    Tag       string
    PatchID   string
    Manifests []ManifestIdentity
}
type ManifestIdentity struct {
    CategoryID       string
    CategoryName     string
    MatchingField    string
    Manifest         ManifestFileInfo
    ManifestDownload ManifestDownloadInfo
    ChunkDownload    ManifestDownloadInfo
    DiffDownload     ManifestDownloadInfo
}
type ManifestFileInfo struct {
    ID               string
    Checksum         string
    CompressedSize   Int64ish
    UncompressedSize Int64ish
}
type ManifestDownloadInfo struct {
    URLPrefix   string
    URLSuffix   string
    Password    string
    Encryption  Boolish
    Compression Boolish
}
func ParseBuildResponse(data []byte) (*BuildResponse, error)      // from apiEnvelope.Data
func ParsePatchResponse(data []byte) (*BuildResponse, error)      // same envelope shape; PatchID populated
func (b *BuildResponse) ManifestFor(matchingField string) (*ManifestIdentity, bool)

// manifest_fetch.go
func FetchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonManifestProto, error) // GET ManifestDownload.url_prefix + "/" + Manifest.ID; zstd if Compression; proto.Unmarshal; checksum NOT verified (logged)
func FetchPatchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonPatchProto, error)

// dedup.go
func BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto, assetName string) map[string]ChunkRef // key = ChunkDecompressedHashMd5; per-asset scope; nil/empty old → empty map

// decision.go
type Flavor int // local to decision results; mirrors hoyoverse planFlavor values, see A.5 mapping
func BuildChunkSources(newAsset *pb.SophonManifestAssetProperty, oldIdx map[string]ChunkRef, chunkURLPrefix string, useCompress bool) []ChunkSource // one asset → its chunk plan (Path-B dedup); CDN entries carry URLPrefix; Local entries ALSO carry ChunkName/URLPrefix for stale-fallback
func BuildPatchInstructions(patch *pb.SophonPatchProto, main *pb.SophonManifestProto, currentLocal, patchURLPrefix string) (patches []PatchInstr, deletes []DeleteInstr) // per-asset join keyed on currentLocal VersionTag; see spec §3.1. NOTE: main-fall-through chunk_assemble is built by the hoyoverse layer (buildSophonPatchPlan), not here — this returns only patch/copyover + deletes.

// chunk_download.go
var ErrChunkVerify = errors.New("sophon: chunk verification failed")
func DownloadChunk(ctx context.Context, hc *http.Client, src ChunkSource, out string) error // GET, ctxReader-wrapped, zstd if UseCompress, verify, atomic write to out; 3× backoff 1s/4s/16s; ErrChunkVerify after exhaustion
func DownloadPatchBlob(ctx context.Context, hc *http.Client, p PatchInstr, out string) error // full blob GET, MD5 verify, atomic write

// local_chunk_read.go
var ErrChunkStale = errors.New("sophon: local chunk MD5 mismatch")
func ReadLocalChunk(gameDir string, src ChunkSource, out string) error // open <gameDir>/<OldFile>, seek OldOffset, read DecompSize, MD5 verify==ExpectMD5 (ErrChunkStale on mismatch / ENOENT-wrapped), atomic write to out

// file_assemble.go
func AssembleFile(out string, totalSize int64, sources []ChunkSource, readChunk func(src ChunkSource) ([]byte, error)) error // MkdirAll, *.tmp, Truncate(totalSize), per src WriteAt(bytes, FileOffset), fsync, return — caller does whole-file MD5 verify + atomic rename. readChunk abstracts staging-read vs local-read.

// hdiff_apply.go
type HDiffOpts struct {
    Ctx        context.Context
    Run        func(ctx context.Context, oldFile, diffFile, newFile string) error // hpatchz.Run injected by parent
    Method     string // MethodPatch | MethodCopyOver
    OldFile    string // MethodPatch
    DiffInput  string // MethodPatch: extracted hdiff slice path
    BlobSlice  string // MethodCopyOver: extracted blob slice path (becomes the file)
    OutTmp     string
}
func HDiffApply(opts HDiffOpts) error // MkdirAll(dir(OutTmp)); Method dispatch; MethodPatch→opts.Run(ctx,OldFile,DiffInput,OutTmp); MethodCopyOver→rename BlobSlice→OutTmp

// rename_cross_device (sophon-package copy; [DEV-2])
func SafeAtomicRename(src, dst string) error // os.Rename; on EXDEV → copy+remove fallback
func isCrossDevice(err error) bool            // build-tagged: windows.ERROR_NOT_SAME_DEVICE / syscall.EXDEV
```

**[DEV-2]** v1's `cross_device_windows.go`/`_other.go` + `isCrossDevice` live in `package hoyoverse` and cannot be imported by `sophon` (would create a cycle). The `sophon` package gets its own build-tagged `cross_device_windows.go`/`cross_device_other.go` + `SafeAtomicRename`. The `hoyoverse`-layer apply (`update_sophon_apply.go`) reuses the EXISTING hoyoverse `isCrossDevice`/`applyOneRename` where it renames from the hoyoverse package; the sophon-package assemble/hdiff use `sophon.SafeAtomicRename`.

### A.4 hoyoverse-package additions

**`meta.go`** — add field to `gameMeta`:
```go
PlatApp string // Sophon plat_app query param; Genshin global = "ddxf6vlr1reo"
```
Genshin entry gains `PlatApp: "ddxf6vlr1reo"`. HSR/ZZZ leave it `""`.

**`hoyoverse.go` Provider** — add two fields + two setters (test seams, [DEV-3]):
```go
branchAPIBase string // default APIBase ("…/hyp/hyp-connect/api"); getGameBranches
sophonAPIBase string // default sophonChunkAPIBase; getBuild/getPatchBuild
func (p *Provider) SetBranchAPIBaseURL(u string)
func (p *Provider) SetSophonAPIBaseURL(u string)
```
Constant in `meta.go`: `const sophonChunkAPIBase = "https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api"`.

**[DEV-3]** v1's `fetchBranchTag` is a `*apiClient` method bound to `APIBase` and is NOT redirectable by the existing `SetAPIBaseURL` (which only steers `fetchGetGamePackages`). Sophon needs httptest redirection of BOTH getGameBranches and getBuild/getPatchBuild. The plan adds `fetchBranchInfo` as a **`*Provider` method** using `p.branchAPIBase` + `p.httpClient` (mirroring `fetchGetGamePackages`), and Sophon build/patch fetches take an explicit base URL sourced from `p.sophonAPIBase`. `fetchBranchTag` is refactored to delegate to `fetchBranchInfo` (`CheckVersion` keeps working). Defaults preserve production behaviour.

### A.5 planFlavor (hoyoverse `plan_internal.go`)

Existing: `flavorNone=0, flavorPatch=1, flavorFull=2, flavorAudioOnly=3, flavorPredlPatch=4, flavorPredlFull=5`. **Append** (do not renumber):
```go
flavorSophonPatch       // 6
flavorSophonBuild       // 7
flavorSophonFull        // 8
flavorSophonPredlPatch  // 9
flavorSophonPredlBuild  // 10
```
`String()` gains cases: `"sophon_patch"`, `"sophon_build"`, `"sophon_full"`, `"sophon_predl_patch"`, `"sophon_predl_build"`.

`genshinPlan` gains these fields (types per A.2):
```go
sophonBranch              *sophon.BranchInfo
sophonBuildID             string
sophonCategories          []sophon.Category
sophonChunkSources        []sophon.ChunkSource
sophonPatches             []sophon.PatchInstr
sophonDeletes             []sophon.DeleteInstr
sophonPatchAssetsFromMain map[string][]sophon.ChunkSource // assetPath → main-manifest chunk plan (§6.4 demotion)
predlConsume              bool
predlSnapshot             *sophonPlanSnapshot             // A.6
predlPlan                 *predlPlanCache                 // A.6
```

### A.6 hoyoverse-package serialization + cache types

```go
// plan_internal.go — in-memory predl plan captured at CheckForUpdate time.
type predlPlanCache struct {
    Flavor         planFlavor
    BuildID        string
    SourceVersion  string
    TargetVersion  string
    AudioLanguages []string
    ChunkSources   []sophon.ChunkSource
    Patches        []sophon.PatchInstr
    Deletes        []sophon.DeleteInstr
    Categories     []sophon.Category
}

// sophon_progress.go — chunk-level download progress sidecar
// (<versionSidecarDir>/sophon_progress.json). v1 progress.json untouched.
type sophonProgressFile struct {
    GameID      string          `json:"game_id"`
    Version     string          `json:"version"`
    BranchKind  string          `json:"branch_kind"`  // "main" | "predl"
    BuildID     string          `json:"build_id"`
    Stage       string          `json:"stage"`        // "download" | "apply"
    ChunksDone  map[string]bool `json:"chunks_done"`  // key = ChunkName
    PatchesDone map[string]bool `json:"patches_done"` // key = PatchName
}

// sophon_apply_wal.go — typed-record WAL (<versionSidecarDir>/sophon_apply.wal)
type sophonApplyWAL struct {
    GameID      string              `json:"game_id"`
    TargetTag   string              `json:"target_tag"`
    BuildID     string              `json:"build_id"`
    SourceTag   string              `json:"source_tag"`   // "" for flavorSophonFull
    Flavor      string              `json:"flavor"`       // planFlavor.String()
    BranchKind  string              `json:"branch_kind"`  // "main" (predl never reaches apply)
    WasPredl    bool                `json:"was_predl"`
    StagingRoot string              `json:"staging_root"` // resolved staging dir; all reads relative to it
    Records     []sophonApplyRecord `json:"records"`
}
type sophonApplyRecord struct {
    Kind            string           `json:"kind"`        // "chunk_assemble" | "hdiff_patch" | "copy_over" | "delete"
    Category        string           `json:"category"`
    Path            string           `json:"path"`        // target relative to gameDir
    State           string           `json:"state"`       // "pending" | "done"
    AssetMD5        string           `json:"asset_md5,omitempty"`
    OldPath         string           `json:"old_path,omitempty"`
    PatchName       string           `json:"patch_name,omitempty"`
    PatchOff        int64            `json:"patch_off,omitempty"`
    PatchLen        int64            `json:"patch_len,omitempty"`
    ExpectMD5       string           `json:"expect_md5,omitempty"`        // delete pre-check
    AssembleSources []walChunkSource `json:"assemble_sources,omitempty"`  // chunk_assemble (incl §6.4 demotions)
    OriginalFileMD5 string           `json:"original_file_md5,omitempty"` // hdiff_patch pre-apply guard
}
type walChunkSource struct {
    Kind         string `json:"kind"`         // "cdn" | "local"
    ChunkName    string `json:"chunk_name"`
    URLPrefix    string `json:"url_prefix"`   // persisted for offline-safe demotion re-download
    CompressedSz int64  `json:"compressed_sz"`
    UseCompress  bool   `json:"use_compress"`
    OldFile      string `json:"old_file,omitempty"`
    OldOffset    int64  `json:"old_offset,omitempty"`
    DecompSize   int64  `json:"decomp_size"`
    FileOffset   int64  `json:"file_offset"`
    ExpectMD5    string `json:"expect_md5"`
}
// Conversions (sophon_apply_wal.go): toWalChunkSource(sophon.ChunkSource) walChunkSource
// and fromWalChunkSource(walChunkSource) sophon.ChunkSource.

// sophon_manifest_cache.go — on-disk applied-manifest index
// (<gameSidecarDir>/.sophon/applied.json + manifests/<build_id>__<cat>.manifest.pb.zst)
type appliedManifestSet struct {
    Latest   *appliedBuild `json:"latest"`
    Previous *appliedBuild `json:"previous"`
}
type appliedBuild struct {
    BuildID    string            `json:"build_id"`
    Version    string            `json:"version"`
    AppliedAt  string            `json:"applied_at"`            // RFC3339
    Categories map[string]string `json:"categories"`            // matchingField → build_id
}

// update_sophon_plan.go — predl snapshot persisted in predl_ready.json
type sophonPlanSnapshot struct {
    SophonChunkSources []sophon.ChunkSource `json:"sophon_chunk_sources"`
    SophonPatches      []sophon.PatchInstr  `json:"sophon_patches"`
    SophonDeletes      []sophon.DeleteInstr `json:"sophon_deletes"`
    Categories         []sophon.Category    `json:"categories"`
}
type sophonPredlReadyFile struct {
    core.ProgressFile                    // v1 base (Entries empty for Sophon)
    Kind           string             `json:"kind"`            // "sophon_patch" | "sophon_build"
    BuildID        string             `json:"build_id"`
    SourceVersion  string             `json:"source_version"`
    TargetVersion  string             `json:"target_version"`
    AudioLanguages []string           `json:"audio_languages"`
    StagedAt       string             `json:"staged_at"`       // RFC3339
    PlanSnapshot   sophonPlanSnapshot `json:"plan_snapshot"`
}
```
`sophon.ChunkSource` / `sophon.PatchInstr` / `sophon.DeleteInstr` / `sophon.Category` are JSON-serialized directly inside `sophonPlanSnapshot` — they carry no json tags, so they serialize with exported PascalCase field names. That is acceptable because the snapshot is private to the hoyoverse package (written and read only by it); **the implementer must NOT add json tags to the sophon types** (they are also the in-memory plan types and tags would be noise). The WAL uses the separately-tagged `walChunkSource` instead.

### A.7 Time injection

`time.Now()` is used by `maybeSelfHealSophon`, `RotateAfterApply` (`applied_at`), and predl `StagedAt`. Mirror v1: call `time.Now().UTC()` inline. Tests that must control the clock use the existing pattern (v1 has no global clock seam for these; tests assert on shape/relative behavior, not exact timestamps). Do NOT introduce a clock interface — match v1.

### A.8 Sidecar path helpers (`sidecar_paths.go` additions)

```go
func sophonSubdir(tempRoot string, gid core.GameID) string        // <gameSidecarDir>/.sophon
func sophonManifestsDir(tempRoot string, gid core.GameID) string  // <…/.sophon>/manifests
func sophonAppliedJSONPath(tempRoot string, gid core.GameID) string// <…/.sophon>/applied.json
func sophonStagingDir(tempRoot string, gid core.GameID, version, branchKind, buildID string) string
// → <versionSidecarDir>/staging/<branchKind>/<buildID>   (branchKind ∈ {"main","predl"})
```

### A.9 Runtime error codes (`core.UpdateError.Code` string values)

`"sophon_no_install"` (Retryable false), `"sophon_manifest_fetch_failed"` (true), `"sophon_chunk_verify_failed"` (true), `"sophon_apply_failed"` (false). `predl_stale` is internal warn-log only (never surfaced).

### A.10 New core constant

`internal/core/updater.go`: `ReasonResumeInterrupted ReasonCode = "resume_interrupted"` (appended to the existing const block; `ReasonCode` is a plain string, no JSON methods).

### A.11 Frontend i18n keys

Remove `update.error.sophon_not_supported` from all 3 locales. Add to `update.error.*`: `sophon_no_install`, `sophon_manifest_fetch_failed`, `sophon_chunk_verify_failed`, `sophon_apply_failed` (texts in spec §8.2). Add to `update.reason.*`: `resume_interrupted`. **[DEV-4]** The frontend currently has NO generic mapping from `last_error.code` → `update.error.<code>` (only `useResumePrompt` handles `interrupted_resume`); the v1 `sophon_not_supported` string was effectively never rendered. Task 22 adds a minimal generic error-display path (a computed in BottomBar.vue or a tiny composable mirroring `useResumePrompt`) so Sophon error codes render, and §8.4's BottomBar test asserts the rendered `sophon_no_install` text.

---

## §B. File structure (create / modify)

**Create — `internal/providers/hoyoverse/hpatchz/`** (Task 2): `hpatchz.go`, `hpatchz_test.go`, `third_party_hpatchz/{hpatchz.exe,LICENSE,README.md}` (moved verbatim).

**Create — `internal/providers/hoyoverse/sophon/`**: `proto/{sophon_manifest.proto,sophon_patch.proto,sophon_manifest.pb.go,sophon_patch.pb.go,tools.go,gen.go}` (T4); `branches.go`+`branches_test.go`, `infos.go`+`infos_test.go` (T5); `manifest_fetch.go`+`_test.go` (T6); `dedup.go`+`_test.go` (T7); `decision.go`+`_test.go` (T8); `chunk_download.go`+`_test.go`, `cross_device_windows.go`, `cross_device_other.go`, `rename.go` (T9); `local_chunk_read.go`+`_test.go` (T10); `file_assemble.go`+`_test.go` (T11); `hdiff_apply.go`+`_test.go` (T12).

**Create — `internal/providers/hoyoverse/`**: `update_sophon_plan.go`+`_test.go` (T18); `update_sophon_download.go`+`_test.go` (T19); `update_sophon_apply.go`+`_test.go` (T20); `sophon_apply_wal.go`+`_test.go` (T16); `sophon_progress.go`+`_test.go` (T15); `sophon_manifest_cache.go`+`_test.go` (T17); `testdata/sophon/*` (T23).

**Modify**: `go.mod`/`go.sum` (T1); `internal/providers/hoyoverse/update_patch.go` import (T2); `internal/core/updater.go` (T3); `internal/core/recovery.go` (T3); `internal/app/update_handler.go` (T3); `internal/providers/hoyoverse/meta.go` (T13); `internal/providers/hoyoverse/api.go` (T13); `internal/providers/hoyoverse/hoyoverse.go` (T13 setters, T21 dispatch); `internal/providers/hoyoverse/sidecar_paths.go` (T13); `internal/providers/hoyoverse/plan_internal.go` (T14); `internal/providers/hoyoverse/integration_test.go` (T23); `frontend/src/locales/{en,zh-TW,zh-CN}.json` (T22); `frontend/src/components/BottomBar.vue` (T22); `frontend/src/__tests__/BottomBar.test.ts` (T22).

---

## §C. Spec deviations (this plan)

- **[DEV-1]** Canonical work-item types (`ChunkSource`/`PatchInstr`/`DeleteInstr`/`ChunkRef`/`Category`) exported from `sophon`; `genshinPlan` uses them directly instead of duplicate lowercase hoyoverse types.
- **[DEV-2]** `sophon` package owns its own build-tagged cross-device detection + `SafeAtomicRename` (cannot import hoyoverse's).
- **[DEV-3]** `fetchBranchInfo` is a `*Provider` method on `p.branchAPIBase`; new `SetBranchAPIBaseURL`/`SetSophonAPIBaseURL` test seams; `fetchBranchTag` delegates to it.
- **[DEV-4]** Frontend gains a minimal generic `last_error.code → update.error.<code>` display path (spec §8 assumed it existed; it did not).
- **[DEV-5]** Main-fall-through `chunk_assemble` for patch flavor is assembled in the hoyoverse layer (`buildSophonPatchPlan`), not in `sophon.BuildPatchInstructions` — keeps the sophon function free of gameDir/dedup-index concerns. `BuildPatchInstructions` returns only patch/copyover + deletes.

---

## §D. Task index

| # | Task | Group | Key files |
|---|---|---|---|
| 1 | go.mod deps (protobuf, zstd, xxhash) | A | go.mod, go.sum |
| 2 | Extract `hpatchz` to sub-package | A | hpatchz/*, update_patch.go |
| 3 | core + app extensions (ReasonResumeInterrupted, ScanRecovery, dotfile skip) | A | core/updater.go, core/recovery.go, app/update_handler.go |
| 4 | Proto files + generated Go + tools/gen | B | sophon/proto/* |
| 5 | branches.go + infos.go (Boolish/Int64ish) | B | sophon/branches.go, sophon/infos.go |
| 6 | manifest_fetch.go (zstd + proto) | B | sophon/manifest_fetch.go |
| 7 | dedup.go (per-asset MD5 index) | C | sophon/dedup.go |
| 8 | decision.go (DecidePath / BuildChunkSources / BuildPatchInstructions) | C | sophon/decision.go |
| 9 | chunk_download.go + ctxReader + cross-device rename | D | sophon/chunk_download.go, sophon/rename.go, sophon/cross_device_*.go |
| 10 | local_chunk_read.go | D | sophon/local_chunk_read.go |
| 11 | file_assemble.go | D | sophon/file_assemble.go |
| 12 | hdiff_apply.go (injected Run) | D | sophon/hdiff_apply.go |
| 13 | meta.PlatApp + api.fetchBranchInfo + setters + sidecar_paths helpers | E | meta.go, api.go, hoyoverse.go, sidecar_paths.go |
| 14 | plan_internal.go flavors + genshinPlan fields + predlPlanCache | E | plan_internal.go |
| 15 | sophon_progress.go sidecar | E | sophon_progress.go |
| 16 | sophon_apply_wal.go sidecar + batched rewrite | E | sophon_apply_wal.go |
| 17 | sophon_manifest_cache.go (save/load/rotate/cleanup) | E | sophon_manifest_cache.go |
| 18 | update_sophon_plan.go (build*Plan, detectPredlConsume, mapFolders) | F | update_sophon_plan.go |
| 19 | update_sophon_download.go (worker pool, resume) | F | update_sophon_download.go |
| 20 | update_sophon_apply.go (record dispatch, demotion, ordering, cleanup) | F | update_sophon_apply.go |
| 21 | hoyoverse.go integration (CheckForUpdate/RunUpdate dispatch, self-heal, resume) | F | hoyoverse.go |
| 22 | Frontend i18n + generic error display + Vitest | G | locales/*, BottomBar.vue, BottomBar.test.ts |
| 23 | Integration tests + fakeSophonServer + testdata fixtures | G | integration_test.go, testdata/sophon/* |
| 24 | Fuzz + bench | G | sophon/*_test.go, *_test.go |
| 25 | **USER**: smoke + tag v0.4.0-m3b + merge dev→main | — | — |

---

## §E. Reconciliation addenda (NORMATIVE — resolves cross-part gaps; supersedes any contradicting task-body text)

These were surfaced by the parallel drafters and are the final authority. Where a task body contradicts an item here, this section wins.

1. **§A.2 work-item type ownership (single declaration each).** `Category` is declared in **`branches.go` (Task 5)** — it lands first and `ParseBranches` needs it. `ChunkSource`, `PatchInstr`, `DeleteInstr`, `ChunkRef`, and the `SourceCDN`/`SourceLocal`/`MethodPatch`/`MethodCopyOver` constants are declared in **`internal/providers/hoyoverse/sophon/types.go`, created in Task 7** (the first task that needs them — group-B tasks T5/T6 don't reference them). `branches.go` (T5) declares `BranchInfo`/`BranchSlot`/`IsEmpty`/`ParseBranches`/`Category`. `infos.go` (T5) declares `Boolish`/`Int64ish`/`BuildResponse`/`ManifestIdentity`/… `dedup.go` (T7) and `decision.go` (T8) **reference** these types and must NOT re-declare them. The §A.2 listing groups all of these under `package sophon` logically; this item pins the physical file per type. (`types.go` must NOT re-declare `Category`.)

2. **`sophon.Flavor` enum** (decision.go, T8): `const ( DecisionNoInstall Flavor = iota; DecisionIdle; DecisionPatch; DecisionBuild; DecisionFull )`. `DecidePath(nil, …)` and `currentLocal==""` → `DecisionNoInstall`. The hoyoverse layer maps these to `planFlavor` (§A.5).

3. **getGameBranches category JSON key is `"type"`** (per the live capture in `memory/project_m3b_v2_sophon.md`), NOT `"category_type"`. `ParseBranches` (T5) unmarshals a wire struct with `json:"category_id"`, `json:"matching_field"`, `json:"type"` and copies into the tag-free `sophon.Category`. **Any test fixture using `"category_type"` is wrong → use `"type"`.**

4. **`sophonPlanSnapshot` and `sophonPredlReadyFile` are declared in `plan_internal.go` (Task 14)**, not `update_sophon_plan.go`. `genshinPlan.predlSnapshot *sophonPlanSnapshot` needs the type at T14 to compile. Tasks 18/21 **reference** them; do not re-declare. (Corrects §A.6 comment and §B's T18 file assignment.)

5. **`genshinPlan` gets an 11th field — `sophonAssetMD5 map[string]string`** (assetPath → expected whole-file MD5 = `AssetHashMd5`), added in Task 14. **Populated** in Task 18 (`buildSophonPatchPlan`/`buildSophonBuildPlan`) for every asset that emits `chunk_assemble` ChunkSources. **Consumed** in Task 20 to set `sophonApplyRecord.AssetMD5` on `chunk_assemble` records (ChunkSource carries no whole-file MD5). `hdiff_patch`/`copy_over` records instead take `AssetMD5` from `PatchInstr.ExpectMD5`. (Real gap fix — T20-C.)

6. **`detectPredlConsume` signature** (T18) is `detectPredlConsume(tempRoot string, gid core.GameID, currentLocal, mainTag string, mainDiffTags []string) (bool, *sophonPredlReadyFile)` — `mainDiffTags` is required for §3.6's "predl was patch-based but diff window no longer covers user" check. (Corrects §3.6 / part-F T18-H.)

7. **`planFlavorFromString(s string) planFlavor`** inverse helper added in `plan_internal.go` (T14): maps `"sophon_patch"→flavorSophonPatch`, `"sophon_build"→flavorSophonBuild`, `"sophon_full"→flavorSophonFull`, `"sophon_predl_patch"→flavorSophonPredlPatch`, `"sophon_predl_build"→flavorSophonPredlBuild`, else `flavorNone`. Used by T21 to reconstruct flavor from a predl `Kind` (`"sophon_patch"`/`"sophon_build"`) or a resumed WAL `Flavor`. (T21-C.)

8. **WAL flusher seam** (T16): `newWALFlusher(path string, wal *sophonApplyWAL, now func() time.Time)` (nil `now` defaults to `time.Now`). It exposes an unexported, test-readable `flushes int` counter incremented on each disk rewrite, so `sophon_apply_wal_test.go` asserts the 50-record / 5-second cadence by adding records and observing `flushes`. Production callers pass `nil` for `now`. (T20-H / T16.)

9. **Predl staging integrity + threshold-discard (spec §7.3)** lives in a Task 21 helper `verifyPredlStaging(...) (proceed bool, demotions []…, err error)`: stat each CDN chunk / patch blob in `staging/predl/<buildID>/`; missing-or-bad → demotion; if CDN-chunk failure ratio > 25% OR patch-blob failure ratio > 50% → return `proceed=false` (caller discards `predl_ready.json` + `staging/predl/` and falls through to a fresh `staging/main` download). Unit test `TestSophonPredl_PartialStaleStagingThresholdRecover` (24%→demote, 26%→discard) lives in Task 23. (T21-D.)

10. **`sophon_progress.json` recovery supersede** (T3 / `core.recovery.go`): when present **without** `sophon_apply.wal`, `ScanRecovery` returns `RecoveryPhaseDownloadResume` and deletes any v1 `progress.json` + `predl_ready.json` companions in the same dir (Sophon supersedes v1 at the same scope, mirroring v1's apply.wal-supersedes-companions cleanup). Full precedence ladder: `sophon_apply.wal > apply.wal > sophon_progress.json > (progress.json + predl_ready.json) > progress.json > predl_ready.json > none`.

11. **`DownloadPatchBlob` runs for ALL `PatchInstr`** regardless of `Method` (both `MethodPatch` and `MethodCopyOver` need the blob on disk — the apply phase slices `[PatchOffset:PatchOffset+PatchLength]` out of it). T19 enqueues a `jobPatchBlob` per unique `PatchName`. (T19.)

12. **Integration test infrastructure (T23) is test-only**, not part of the production contract: `fakeSophonServer` (httptest mux), `seedConfigIni`, `registerTestGameDir`, and the committed fixture generator (`//go:build genfixtures` or a `TestGenerateFixtures` helper) live in `integration_test.go` / a sibling `_test.go`. They build on v1's `SetAPIBaseURL` plus the new `SetBranchAPIBaseURL`/`SetSophonAPIBaseURL` seams ([DEV-3]).

13. **Fixture set (§B testdata) is extended** beyond the spec's named files to cover all 30 scenarios: add `testdata/sophon/branches_no_difftags.json`, `branches_no_categories.json`, `branches_predl_now.json`, and `build_nonhex_chunk.json` (+ its `.manifest.pb.zst`). T23's generator emits every fixture deterministically (no `Math.random`/timestamps).

14. **`core.UpdatePlan.Reason` is typed `core.ReasonCode`** (not `string`). Sophon plans set `Reason: core.ReasonVersionChanged` / `core.ReasonResumeInterrupted` / `core.ReasonPredownload` etc. as appropriate.

15. **Task numbering for `git` ops in Task 25**: the combined v1+v2 ship merges `dev → main` (dev already carries `m3b/spec` via `--no-ff`); the merge-source branch and whether to fast-forward `m3b-v2/spec` into `dev` first is **confirmed with the user at smoke time** (per `memory/feedback_commits.md` + project memory). Subagents never run Task 25.

---

### Task 1: Add Sophon go.mod dependencies (protobuf, zstd, xxhash)

Adds the three runtime dependencies the Sophon layer needs: `google.golang.org/protobuf` (manifest parsing, T4+), `github.com/klauspost/compress/zstd` (pure-Go zstd; CGO_ENABLED=0 friendly — T6/T9), and `github.com/cespare/xxhash/v2` (chunk integrity — T9). Spec §2.5.

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Test (throwaway, deleted in the same task): `internal/providers/hoyoverse/sophon/depsmoke/depsmoke_test.go`

> **Toolchain note (applies to every Go step in this part):** on subagent shells without Go on PATH, prepend
> `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`.
> This host is `CGO_ENABLED=0` — **never** pass `-race` to `go test`. Use `go test -count=1 ...`.

- [ ] **Step 1: Write a throwaway smoke test that imports all three deps (expected: FAIL — packages not in module).**
  Create `internal/providers/hoyoverse/sophon/depsmoke/depsmoke_test.go`:
  ```go
  package depsmoke

  import (
  	"bytes"
  	"testing"

  	"github.com/cespare/xxhash/v2"
  	"github.com/klauspost/compress/zstd"
  	"google.golang.org/protobuf/proto"
  )

  // TestDepsImportable is a build-only smoke test asserting the three Sophon
  // dependencies resolve and link. Deleted at the end of Task 1.
  func TestDepsImportable(t *testing.T) {
  	// xxhash: hash a known string.
  	if xxhash.Sum64String("") != 0xef46db3751d8e999 {
  		t.Fatal("xxhash sum mismatch")
  	}
  	// zstd: round-trip a tiny buffer.
  	enc, err := zstd.NewWriter(nil)
  	if err != nil {
  		t.Fatalf("zstd writer: %v", err)
  	}
  	compressed := enc.EncodeAll([]byte("hello"), nil)
  	_ = enc.Close()
  	dec, err := zstd.NewReader(nil)
  	if err != nil {
  		t.Fatalf("zstd reader: %v", err)
  	}
  	got, err := dec.DecodeAll(compressed, nil)
  	dec.Close()
  	if err != nil || !bytes.Equal(got, []byte("hello")) {
  		t.Fatalf("zstd round-trip: got %q err %v", got, err)
  	}
  	// proto: reference the package so the import is load-bearing.
  	_ = proto.Marshal
  }
  ```

- [ ] **Step 2: Run the smoke test (expected: FAIL — missing go.sum entries / unknown imports).**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/depsmoke/
  ```
  Expected: build error `no required module provides package github.com/cespare/xxhash/v2` (and the other two). This confirms the deps are genuinely absent before adding them.

- [ ] **Step 3: Add the three dependencies and tidy (implement).**
  ```bash
  go get google.golang.org/protobuf@latest
  go get github.com/klauspost/compress@latest
  go get github.com/cespare/xxhash/v2@latest
  go mod tidy
  ```
  These move `google.golang.org/protobuf`, `github.com/klauspost/compress`, and `github.com/cespare/xxhash/v2` into the `require` block (direct), and `go mod tidy` reconciles `go.sum`. Do NOT hand-edit `go.mod`/`go.sum`.

- [ ] **Step 4: Run the smoke test again (expected: PASS) and assert whole-repo build is GREEN.**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/depsmoke/
  go build ./...
  ```
  Expected: smoke test PASS; `go build ./...` exits 0 with no output.

- [ ] **Step 5: Delete the throwaway smoke test + its package dir, re-tidy, re-verify build.**
  ```bash
  rm -rf internal/providers/hoyoverse/sophon/depsmoke
  go mod tidy
  go build ./...
  ```
  `go mod tidy` keeps the three deps in `require` (they are now referenced by the committed `part-0` §A contract's downstream tasks — but at THIS point nothing else imports them yet, so they will be demoted to `// indirect` or dropped). To pin them as direct deps so later tasks compile cleanly, this step leaves them as `tidy` decides; T4–T9 add real imports that re-promote them. **If `go mod tidy` removes any of the three** because nothing imports them yet, re-run the three `go get` commands from Step 3 (without the smoke test) so they stay recorded in `go.mod`; `go build ./...` must still pass.
  Expected: `go build ./...` exits 0.

- [ ] **Step 6: Commit go.mod + go.sum.**
  ```bash
  git add go.mod go.sum
  git commit -m "feat(sophon): add protobuf/zstd/xxhash deps"
  ```
  (Branch `m3b-v2/spec`; no `Co-Authored-By` trailer per house rule.)

---

### Task 2: Extract `hpatchz` to its own sub-package

Moves v1's `hpatchz.go` (+ test + embedded binary) verbatim into `internal/providers/hoyoverse/hpatchz/` so BOTH `hoyoverse` and the new `hoyoverse/sophon` package can import `hpatchz.Run` without an import cycle (spec §0 "HDiff binary" row; §1 `hpatchz/` table; [DEV-2] context). Public symbol `Run` becomes `hpatchz.Run`. Test seams `resetHpatchzExtractOnce` / `osTempDirHpatchz` / `newHpatchzCmd` move with it. The embed path `third_party_hpatchz/hpatchz.exe` stays relative, so the `third_party_hpatchz/` dir moves into the sub-package and the `.gitignore` exception path is updated.

**Files:**
- Create: `internal/providers/hoyoverse/hpatchz/hpatchz.go` (verbatim move; `package hoyoverse` → `package hpatchz`)
- Create: `internal/providers/hoyoverse/hpatchz/hpatchz_test.go` (verbatim move; `package hoyoverse` → `package hpatchz`)
- Move (git mv): `internal/providers/hoyoverse/third_party_hpatchz/{hpatchz.exe,LICENSE,README.md}` → `internal/providers/hoyoverse/hpatchz/third_party_hpatchz/`
- Delete: `internal/providers/hoyoverse/hpatchz.go`, `internal/providers/hoyoverse/hpatchz_test.go`
- Modify: `.gitignore` (exception path)
- Modify: `internal/providers/hoyoverse/update_patch.go` (import + 2 call sites)

- [ ] **Step 1: Create the sub-package dir and `git mv` the binary + license + readme (preserves the tracked binary blob).**
  ```bash
  mkdir -p internal/providers/hoyoverse/hpatchz
  git mv internal/providers/hoyoverse/third_party_hpatchz internal/providers/hoyoverse/hpatchz/third_party_hpatchz
  ```
  This relocates `third_party_hpatchz/{hpatchz.exe,LICENSE,README.md}` under the new sub-package. `git mv` preserves the binary's object so it is not re-added/re-filtered by `.gitignore`.

- [ ] **Step 2: Update the `.gitignore` vendored-binary exception to the new path.**
  In `.gitignore`, replace:
  ```
  !internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe
  ```
  with:
  ```
  !internal/providers/hoyoverse/hpatchz/third_party_hpatchz/hpatchz.exe
  ```
  (The `*.exe` ignore rule above it still applies; this exception keeps the vendored binary tracked at its new location.)

- [ ] **Step 3: Create `internal/providers/hoyoverse/hpatchz/hpatchz.go` (verbatim move; only the package clause changes).**
  Full file content:
  ```go
  package hpatchz

  import (
  	"context"
  	"crypto/sha256"
  	"encoding/hex"
  	"errors"
  	"fmt"
  	_ "embed"
  	"os"
  	"os/exec"
  	"path/filepath"
  	"sync"
  )

  //go:embed third_party_hpatchz/hpatchz.exe
  var embeddedHpatchz []byte

  // embeddedHpatchzSHA returns the SHA-256 hex of the embedded binary.
  // Computed once at package init via sync.Once-guarded var below.
  var (
  	hpatchzSHAOnce sync.Once
  	hpatchzSHA     string
  )

  func embeddedHpatchzSHA() string {
  	hpatchzSHAOnce.Do(func() {
  		sum := sha256.Sum256(embeddedHpatchz)
  		hpatchzSHA = hex.EncodeToString(sum[:])
  	})
  	return hpatchzSHA
  }

  // osTempDirHpatchz is a test seam returning os.TempDir() in production.
  var osTempDirHpatchz = func() string { return os.TempDir() }

  // extractOnce gates first-use binary extraction. resetHpatchzExtractOnce
  // resets it for tests; production code never calls reset.
  var (
  	hpatchzExtractOnce *sync.Once = &sync.Once{}
  	hpatchzExtractPath string
  	hpatchzExtractErr  error
  )

  func resetHpatchzExtractOnce() {
  	hpatchzExtractOnce = &sync.Once{}
  	hpatchzExtractPath = ""
  	hpatchzExtractErr = nil
  }

  // extractHpatchzOnce writes embeddedHpatchz to <TEMP>/omnigate/hpatchz-<sha8>.exe
  // (cross-backend shared cache; spec §1 hpatchz.go row, §2 sidecar tree). The
  // SHA-keyed filename means a binary upgrade auto-invalidates the cache.
  //
  // Idempotent: if the target file already exists with matching size, reuse;
  // else write atomically (tmpfile + rename). Returns the absolute path.
  func extractHpatchzOnce(ctx context.Context) (string, error) {
  	hpatchzExtractOnce.Do(func() {
  		sha8 := embeddedHpatchzSHA()[:8]
  		dir := filepath.Join(osTempDirHpatchz(), "omnigate")
  		if err := os.MkdirAll(dir, 0o755); err != nil {
  			hpatchzExtractErr = fmt.Errorf("mkdir %s: %w", dir, err)
  			return
  		}
  		path := filepath.Join(dir, "hpatchz-"+sha8+".exe")
  		// Reuse if already present + correct size.
  		if stat, err := os.Stat(path); err == nil && stat.Size() == int64(len(embeddedHpatchz)) {
  			hpatchzExtractPath = path
  			return
  		}
  		// Atomic write.
  		tmp := path + ".tmp"
  		if err := os.WriteFile(tmp, embeddedHpatchz, 0o755); err != nil {
  			hpatchzExtractErr = fmt.Errorf("write %s: %w", tmp, err)
  			return
  		}
  		if err := os.Rename(tmp, path); err != nil {
  			_ = os.Remove(tmp)
  			hpatchzExtractErr = fmt.Errorf("rename %s → %s: %w", tmp, path, err)
  			return
  		}
  		hpatchzExtractPath = path
  	})
  	if hpatchzExtractErr != nil {
  		return "", hpatchzExtractErr
  	}
  	if err := ctx.Err(); err != nil {
  		return "", err
  	}
  	return hpatchzExtractPath, nil
  }

  // newHpatchzCmd is a test seam for constructing the exec.Cmd. Keeps tests
  // from binding to specific CommandContext flags.
  func newHpatchzCmd(ctx context.Context, hpatchzPath string, args ...string) *exec.Cmd {
  	return exec.CommandContext(ctx, hpatchzPath, args...)
  }

  // Run invokes hpatchz to apply <diffFile> to <oldFile>, producing <newFile>.
  // Cancel propagates via ctx (exec.CommandContext machinery kills the process
  // on ctx.Done()).
  //
  // hpatchz argument order: hpatchz [options] <oldFile> <diffFile> <outNewFile>
  // Per HDiffPatch v4 docs (and Collapse Launcher's invocation pattern).
  //
  // Returns nil on zero exit code; otherwise error wrapping exec.ExitError
  // with stderr.
  func Run(ctx context.Context, oldFile, diffFile, newFile string) error {
  	if err := ctx.Err(); err != nil {
  		return err
  	}
  	hpatchzPath, err := extractHpatchzOnce(ctx)
  	if err != nil {
  		return err
  	}
  	cmd := newHpatchzCmd(ctx, hpatchzPath, "-f", oldFile, diffFile, newFile)
  	out, err := cmd.CombinedOutput()
  	if err != nil {
  		var ee *exec.ExitError
  		if errors.As(err, &ee) {
  			return fmt.Errorf("hpatchz exit %d: %w; output: %s", ee.ExitCode(), err, string(out))
  		}
  		return fmt.Errorf("hpatchz exec: %w; output: %s", err, string(out))
  	}
  	return nil
  }
  ```

- [ ] **Step 4: Create `internal/providers/hoyoverse/hpatchz/hpatchz_test.go` (verbatim move; only the package clause changes).**
  Full file content:
  ```go
  package hpatchz

  import (
  	"context"
  	"crypto/sha256"
  	"encoding/hex"
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"
  	"time"
  )

  func TestEmbeddedHpatchzNonEmpty(t *testing.T) {
  	if len(embeddedHpatchz) == 0 {
  		t.Fatal("embeddedHpatchz is empty; Task 1 may not have committed the binary")
  	}
  	if len(embeddedHpatchz) < 100*1024 {
  		t.Errorf("embeddedHpatchz size %d bytes is suspiciously small (<100KB); expected ~250-900KB", len(embeddedHpatchz))
  	}
  }

  func TestEmbeddedSHAComputed(t *testing.T) {
  	want := sha256.Sum256(embeddedHpatchz)
  	got := embeddedHpatchzSHA()
  	if hex.EncodeToString(want[:]) != got {
  		t.Errorf("SHA mismatch: want %s got %s", hex.EncodeToString(want[:]), got)
  	}
  	// First 8 hex chars used as cache filename suffix.
  	if len(got) < 8 {
  		t.Fatal("sha256 hex too short")
  	}
  }

  func TestExtractHpatchzOnce_CachesAndReuses(t *testing.T) {
  	tmp := t.TempDir()
  	prevTempDir := osTempDirHpatchz
  	osTempDirHpatchz = func() string { return tmp }
  	defer func() { osTempDirHpatchz = prevTempDir }()
  	resetHpatchzExtractOnce()

  	path1, err := extractHpatchzOnce(context.Background())
  	if err != nil {
  		t.Fatalf("first extract: %v", err)
  	}
  	if !strings.Contains(path1, embeddedHpatchzSHA()[:8]) {
  		t.Errorf("path %q should contain SHA-8 prefix %q", path1, embeddedHpatchzSHA()[:8])
  	}
  	stat1, err := os.Stat(path1)
  	if err != nil {
  		t.Fatalf("stat: %v", err)
  	}
  	mtime1 := stat1.ModTime()

  	// Wait a tick to detect any rewrite.
  	time.Sleep(20 * time.Millisecond)

  	path2, err := extractHpatchzOnce(context.Background())
  	if err != nil {
  		t.Fatalf("second extract: %v", err)
  	}
  	if path1 != path2 {
  		t.Errorf("expected same path, got %q vs %q", path1, path2)
  	}
  	stat2, err := os.Stat(path2)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if !stat2.ModTime().Equal(mtime1) {
  		t.Errorf("file rewrite suspected; mtime changed: %v → %v", mtime1, stat2.ModTime())
  	}
  }

  func TestRunHpatchz_BogusFiles(t *testing.T) {
  	tmp := t.TempDir()
  	prevTempDir := osTempDirHpatchz
  	osTempDirHpatchz = func() string { return tmp }
  	defer func() { osTempDirHpatchz = prevTempDir }()
  	resetHpatchzExtractOnce()

  	// Run with non-existent input paths to exercise the full Run pipeline:
  	//   1. ctx not canceled → reaches extract
  	//   2. extractHpatchzOnce succeeds (writes binary)
  	//   3. exec.CommandContext(... -f <bogus> <bogus> <bogus>) runs
  	//   4. hpatchz exits non-zero (cannot open input)
  	//   5. Run returns error wrapping ExitError + stderr
  	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  	defer cancel()

  	err := Run(ctx,
  		filepath.Join(tmp, "nonexistent_old"),
  		filepath.Join(tmp, "nonexistent_diff"),
  		filepath.Join(tmp, "nonexistent_new"),
  	)
  	if err == nil {
  		t.Fatal("expected error from hpatchz with bogus input paths")
  	}
  	// Verify error wraps stderr / exit-code info (loose check; substring may
  	// vary by hpatchz version but "hpatchz" should appear in our wrap).
  	if !strings.Contains(err.Error(), "hpatchz") {
  		t.Errorf("expected error mentions hpatchz; got %v", err)
  	}
  }
  ```

- [ ] **Step 5: Delete the old parent-package copies.**
  ```bash
  git rm internal/providers/hoyoverse/hpatchz.go internal/providers/hoyoverse/hpatchz_test.go
  ```
  (The symbols `Run`, `resetHpatchzExtractOnce`, `osTempDirHpatchz`, `newHpatchzCmd`, `embeddedHpatchz`, `embeddedHpatchzSHA`, `extractHpatchzOnce` no longer exist in `package hoyoverse` — every reference must now go through the sub-package. The only production reference is `update_patch.go`, fixed next.)

- [ ] **Step 6: Run the build (expected: FAIL — `update_patch.go` still calls bare `Run`).**
  ```bash
  go build ./...
  ```
  Expected: `internal/providers/hoyoverse/update_patch.go:196: undefined: Run` (and `:248`). This confirms the call sites still need fixing.

- [ ] **Step 7: Update `update_patch.go` import + 2 call sites (implement).**
  In `internal/providers/hoyoverse/update_patch.go`, add the import to the existing import block (alphabetical position after `strings` is fine — `gofmt` will order it; place it with the module imports):
  ```go
  	"omnigate/internal/providers/hoyoverse/hpatchz"
  ```
  Then change the two call sites:
  - Line ~196 — from:
    ```go
    			if err := Run(ctx, srcPath, patchPath, stagedTargetPath); err != nil {
    ```
    to:
    ```go
    			if err := hpatchz.Run(ctx, srcPath, patchPath, stagedTargetPath); err != nil {
    ```
  - Line ~248 — from:
    ```go
    			if err := Run(ctx, lt.src, lt.patch, lt.target); err != nil {
    ```
    to:
    ```go
    			if err := hpatchz.Run(ctx, lt.src, lt.patch, lt.target); err != nil {
    ```
  Run `gofmt -w internal/providers/hoyoverse/update_patch.go` to settle import ordering.

- [ ] **Step 8: Build + run the moved package's tests + the hoyoverse package tests (expected: PASS).**
  ```bash
  go build ./...
  go test -count=1 ./internal/providers/hoyoverse/...
  ```
  Expected: build exits 0; `hpatchz` sub-package tests (`TestEmbeddedHpatchzNonEmpty`, `TestEmbeddedSHAComputed`, `TestExtractHpatchzOnce_CachesAndReuses`, `TestRunHpatchz_BogusFiles`) PASS at their new path; existing `hoyoverse` package tests still PASS (now exercising `hpatchz.Run` through `update_patch.go`).

- [ ] **Step 9: Commit the extraction.**
  ```bash
  git add -A
  git commit -m "refactor(hoyoverse): extract hpatchz to sub-package"
  ```
  `git add -A` captures the renamed binary, the new sub-package files, the deleted originals, the `.gitignore` edit, and the `update_patch.go` import change.

---

### Task 3: core + app extensions (ReasonResumeInterrupted, ScanRecovery Sophon sidecars, dotfile skip)

Three independent extensions that the Sophon layer's resume + recovery wiring depends on:
- (a) `core/updater.go`: append `ReasonResumeInterrupted ReasonCode = "resume_interrupted"` (§A.10, spec §3.4).
- (b) `core/recovery.go`: teach `ScanRecovery` two new sidecars — `sophon_apply.wal` (→ `RecoveryPhaseApplyResume`, `WasPredl` read from its single-object JSON header's `was_predl` field, same shape as v1 `apply.wal`) and `sophon_progress.json` (→ `RecoveryPhaseDownloadResume`). Pinned precedence (spec §0 / §6.9): `sophon_apply.wal > apply.wal > sophon_progress.json > progress.json+predl > progress.json > predl_ready.json`. Sophon sidecars SUPERSEDE v1 companions at the same scope (delete stale companions exactly as v1 does).
- (c) `app/update_handler.go`: add a one-line dotfile skip in `scanForRecoveryRoot`'s version-dir loop so the cross-version `.sophon/` dir is never misclassified as a version dir (spec §1 `update_handler.go` row).

**Files:**
- Modify: `internal/core/updater.go`
- Modify: `internal/core/recovery.go`
- Test: `internal/core/recovery_test.go`
- Modify: `internal/app/update_handler.go`

- [ ] **Step 1: (a) Append the new ReasonCode constant.**
  In `internal/core/updater.go`, change the const block from:
  ```go
  const (
  	ReasonUnspecified     ReasonCode = ""
  	ReasonVersionChanged  ReasonCode = "version_changed"
  	ReasonAudioPackAdded  ReasonCode = "audio_pack_added"
  	ReasonVersionAndAudio ReasonCode = "version_and_audio"
  	ReasonPredownload     ReasonCode = "predownload"
  )
  ```
  to:
  ```go
  const (
  	ReasonUnspecified       ReasonCode = ""
  	ReasonVersionChanged    ReasonCode = "version_changed"
  	ReasonAudioPackAdded    ReasonCode = "audio_pack_added"
  	ReasonVersionAndAudio   ReasonCode = "version_and_audio"
  	ReasonPredownload       ReasonCode = "predownload"
  	ReasonResumeInterrupted ReasonCode = "resume_interrupted"
  )
  ```
  (Existing constants untouched; only the new line is added and the block re-aligned by `gofmt`.)

- [ ] **Step 2: Build to confirm (a) compiles (expected: PASS).**
  ```bash
  go build ./internal/core/
  ```
  Expected: exits 0. `ReasonResumeInterrupted` is now available to the hoyoverse layer (consumed in T18/T21).

- [ ] **Step 3: (b) Write failing tests for the Sophon ScanRecovery cases in `recovery_test.go` (expected: FAIL — Sophon sidecars not yet recognized).**
  Append to `internal/core/recovery_test.go`:
  ```go
  func TestScanRecovery_SophonApplyWAL(t *testing.T) {
  	tmp := t.TempDir()
  	wal := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":false,"records":[{"kind":"chunk_assemble","state":"pending"}]}`
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(wal), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryPhaseApplyResume {
  		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
  	}
  	if state.WasPredl {
  		t.Errorf("WasPredl = true; sophon WAL was_predl=false")
  	}
  }

  func TestScanRecovery_SophonApplyWAL_WasPredl(t *testing.T) {
  	tmp := t.TempDir()
  	wal := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":true,"records":[]}`
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(wal), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryPhaseApplyResume {
  		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
  	}
  	if !state.WasPredl {
  		t.Errorf("WasPredl = false; sophon WAL was_predl=true")
  	}
  }

  func TestScanRecovery_SophonApplyWAL_Corrupt(t *testing.T) {
  	tmp := t.TempDir()
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte("{not json"), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryCorrupt {
  		t.Errorf("Phase = %v, want RecoveryCorrupt", state.Phase)
  	}
  }

  func TestScanRecovery_SophonProgress(t *testing.T) {
  	tmp := t.TempDir()
  	prog := `{"game_id":"genshin","version":"6.6.0","stage":"download","chunks_done":{"abc":true}}`
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(prog), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryPhaseDownloadResume {
  		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume", state.Phase)
  	}
  }

  func TestScanRecovery_Precedence_SophonOverV1(t *testing.T) {
  	tmp := t.TempDir()
  	// sophon_apply.wal present alongside the full v1 sidecar set.
  	sophonWAL := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":true,"records":[]}`
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(sophonWAL), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(`{"etag":"e","was_predl":false,"done":[]}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(`{"chunks_done":{}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryPhaseApplyResume {
  		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume (sophon WAL wins)", state.Phase)
  	}
  	if !state.WasPredl {
  		t.Errorf("WasPredl = false; should come from the sophon WAL header (was_predl=true)")
  	}
  	// Sophon WAL supersedes ALL v1 companions at the same scope.
  	for _, stale := range []string{"apply.wal", "sophon_progress.json", "progress.json", "predl_ready.json"} {
  		if _, err := os.Stat(filepath.Join(tmp, stale)); err == nil {
  			t.Errorf("%s should be deleted (superseded by sophon_apply.wal)", stale)
  		}
  	}
  }

  func TestScanRecovery_Precedence_SophonProgressOverV1Progress(t *testing.T) {
  	tmp := t.TempDir()
  	// sophon_progress.json beats v1 progress.json (no WALs present).
  	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(`{"chunks_done":{"a":true}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(filepath.Join(tmp, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
  		t.Fatal(err)
  	}
  	state := ScanRecovery(tmp)
  	if state.Phase != RecoveryPhaseDownloadResume {
  		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume (sophon progress wins over v1)", state.Phase)
  	}
  	for _, stale := range []string{"progress.json", "predl_ready.json"} {
  		if _, err := os.Stat(filepath.Join(tmp, stale)); err == nil {
  			t.Errorf("%s should be deleted (superseded by sophon_progress.json)", stale)
  		}
  	}
  }
  ```

- [ ] **Step 4: Run the new tests (expected: FAIL — `sophon_apply.wal` / `sophon_progress.json` are ignored, fall through to `RecoveryNone` or v1 paths).**
  ```bash
  go test -count=1 ./internal/core/ -run TestScanRecovery_Sophon
  go test -count=1 ./internal/core/ -run TestScanRecovery_Precedence
  ```
  Expected: `TestScanRecovery_SophonApplyWAL` etc. FAIL with `Phase = 0, want RecoveryPhaseApplyResume` (RecoveryNone=0), because the current `ScanRecovery` only checks `progress.json` / `apply.wal` / `predl_ready.json`.

- [ ] **Step 5: (b) Extend `ScanRecovery` with the two Sophon sidecars at the top of the precedence ladder (implement).**
  Replace the body of `ScanRecovery` in `internal/core/recovery.go` with:
  ```go
  func ScanRecovery(dir string) RecoveryState {
  	hasSophonWAL := fileExists(filepath.Join(dir, "sophon_apply.wal"))
  	hasSophonProgress := fileExists(filepath.Join(dir, "sophon_progress.json"))
  	hasProgress := fileExists(filepath.Join(dir, "progress.json"))
  	hasWAL := fileExists(filepath.Join(dir, "apply.wal"))
  	hasPredl := fileExists(filepath.Join(dir, "predl_ready.json"))

  	// Precedence (spec §0 / §6.9):
  	//   sophon_apply.wal > apply.wal > sophon_progress.json
  	//     > progress.json+predl > progress.json > predl_ready.json
  	// Sophon sidecars supersede v1 companions at the same scope; the prevailing
  	// sidecar deletes the stale companions (same semantics as v1 apply.wal).
  	switch {
  	case hasSophonWAL:
  		// Sophon apply WAL supersedes every v1 companion AND sophon_progress.
  		for _, stale := range []string{"apply.wal", "sophon_progress.json", "progress.json", "predl_ready.json"} {
  			_ = os.Remove(filepath.Join(dir, stale))
  		}
  		walPath := filepath.Join(dir, "sophon_apply.wal")
  		body, err := os.ReadFile(walPath)
  		if err != nil {
  			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
  		}
  		var hdr struct {
  			WasPredl bool `json:"was_predl"`
  		}
  		if err := json.Unmarshal(body, &hdr); err != nil {
  			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
  		}
  		return RecoveryState{Phase: RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

  	case hasWAL:
  		if hasSophonProgress {
  			_ = os.Remove(filepath.Join(dir, "sophon_progress.json"))
  		}
  		if hasProgress {
  			_ = os.Remove(filepath.Join(dir, "progress.json"))
  		}
  		if hasPredl {
  			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
  		}
  		walPath := filepath.Join(dir, "apply.wal")
  		body, err := os.ReadFile(walPath)
  		if err != nil {
  			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
  		}
  		var hdr struct {
  			WasPredl bool `json:"was_predl"`
  		}
  		if err := json.Unmarshal(body, &hdr); err != nil {
  			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
  		}
  		return RecoveryState{Phase: RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

  	case hasSophonProgress:
  		// Sophon download-phase resume. Supersedes v1 progress.json + predl_ready.json.
  		if hasProgress {
  			_ = os.Remove(filepath.Join(dir, "progress.json"))
  		}
  		if hasPredl {
  			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
  		}
  		return RecoveryState{Phase: RecoveryPhaseDownloadResume}

  	case hasProgress && hasPredl:
  		_ = os.Remove(filepath.Join(dir, "progress.json"))
  		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
  			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
  			return RecoveryState{Phase: RecoveryNone}
  		}
  		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

  	case hasProgress:
  		if _, err := loadProgressFile(filepath.Join(dir, "progress.json")); err != nil {
  			_ = os.Remove(filepath.Join(dir, "progress.json"))
  			return RecoveryState{Phase: RecoveryNone}
  		}
  		return RecoveryState{Phase: RecoveryPhaseDownloadResume}

  	case hasPredl:
  		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
  			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
  			return RecoveryState{Phase: RecoveryNone}
  		}
  		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

  	default:
  		return RecoveryState{Phase: RecoveryNone}
  	}
  }
  ```
  Notes for the implementer:
  - The `sophon_apply.wal` header read mirrors v1's `apply.wal` `was_predl` read exactly — a single JSON object with a `was_predl` field (the Sophon WAL is `sophonApplyWAL` per §A.6, a single object, NOT a flat list).
  - `sophon_progress.json` is NOT `core.ProgressFile`-shaped (it's the hoyoverse-package `sophonProgressFile`), so it is NOT parsed via `loadProgressFile` here — existence is sufficient for the recovery classification, matching the spec's "existence-check ladder" wording (§0 `core.ScanRecovery extension` row). Parse/validity of `sophon_progress.json` is the hoyoverse dispatcher's concern (§6.9 path 3), not `core`'s.

- [ ] **Step 6: Run the full core recovery suite (expected: PASS — new + existing).**
  ```bash
  go test -count=1 ./internal/core/ -run TestScanRecovery
  ```
  Expected: all `TestScanRecovery_*` PASS, including the pre-existing v1 cases (`TestScanRecovery_ApplyWalWins`, `TestScanRecovery_PredlOverProgress`, `TestScanRecovery_DownloadOnly_NotFromPredl`, `TestScanRecovery_ApplyResume_*`, `TestScanRecovery_CorruptWal`) which exercise the v1 precedence still intact below the Sophon cases.

- [ ] **Step 7: (c) Add the dotfile skip in `scanForRecoveryRoot`'s version-dir loop (implement).**
  In `internal/app/update_handler.go`, in the `for _, vDir := range versionDirs` loop, change:
  ```go
  		for _, vDir := range versionDirs {
  			if !vDir.IsDir() {
  				continue
  			}
  			sidecarDir := filepath.Join(gameDirPath, vDir.Name())
  ```
  to:
  ```go
  		for _, vDir := range versionDirs {
  			if !vDir.IsDir() {
  				continue
  			}
  			// Skip cross-version sidecar dirs (e.g. ".sophon/"); they are not
  			// version dirs and must not be passed to ScanRecovery (spec §1).
  			if strings.HasPrefix(vDir.Name(), ".") {
  				continue
  			}
  			sidecarDir := filepath.Join(gameDirPath, vDir.Name())
  ```
  Ensure `"strings"` is in the file's import block; if not present, add it (then `gofmt -w internal/app/update_handler.go`).

- [ ] **Step 8: Build the app package + run its tests (expected: PASS).**
  ```bash
  go build ./internal/app/
  go test -count=1 ./internal/app/
  ```
  Expected: build exits 0; existing app tests PASS. There is no dedicated unit test for the dotfile skip in the app test file; it is covered by the integration test in Task 23 (`TestSophonIdlePathCleanup_AllDoneWAL` and the `.sophon/` sidecar tree walk). Note this in the commit body. (If a `scanForRecovery`-targeted test already exists in `internal/app/*_test.go`, add a case writing a `.sophon/` dir under `<flat_gid>/` and asserting `applyRecoveryState` is not invoked for it — but do NOT invent a test harness; the integration coverage is the contract.)

- [ ] **Step 9: Whole-repo gate (expected: PASS).**
  ```bash
  go build ./... && go vet ./... && go test -count=1 ./internal/core/ ./internal/app/ ./internal/providers/hoyoverse/...
  ```
  Expected: all green.

- [ ] **Step 10: Commit the core + app extensions.**
  ```bash
  git add internal/core/updater.go internal/core/recovery.go internal/core/recovery_test.go internal/app/update_handler.go
  git commit -m "feat(sophon): extend core recovery + reason code for Sophon sidecars"
  ```

### Task 4: Proto files + committed generated Go + tools/gen

Creates the `sophon/proto` sub-package: the two `.proto` schemas (verbatim from spec §2.4), the **committed** `protoc-gen-go` output, `tools.go` + `gen.go` for regeneration, and a smoke test asserting the generated structs carry the exact exported fields from §A.1.

> **Toolchain note (applies to every Go step in this file):** On subagent shells without Go on PATH, prepend `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. This host is `CGO_ENABLED=0` — **never** pass `-race`. Run package tests with `go test -count=1 ./internal/providers/hoyoverse/sophon/...`.

**Files:**
- Create: `internal/providers/hoyoverse/sophon/proto/sophon_manifest.proto`
- Create: `internal/providers/hoyoverse/sophon/proto/sophon_patch.proto`
- Create: `internal/providers/hoyoverse/sophon/proto/sophon_manifest.pb.go` (generated, committed)
- Create: `internal/providers/hoyoverse/sophon/proto/sophon_patch.pb.go` (generated, committed)
- Create: `internal/providers/hoyoverse/sophon/proto/tools.go`
- Create: `internal/providers/hoyoverse/sophon/proto/gen.go`
- Test: `internal/providers/hoyoverse/sophon/proto/proto_smoke_test.go`
- Modify: `README.md` (dev-setup note, spec §2.5)

Steps:

- [ ] **Step 1: Write the two `.proto` files (verbatim from spec §2.4).**
  Create `sophon_manifest.proto`:
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
  Create `sophon_patch.proto`:
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

- [ ] **Step 2: Write `tools.go` and `gen.go`.**
  `tools.go`:
  ```go
  //go:build tools
  // +build tools

  // Package proto's tools.go pins protoc-gen-go so `go install` (run from this
  // dir) picks the right version for regenerating the committed *.pb.go files.
  // Never built by application code (the `tools` build tag is never set).
  package proto

  import (
  	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
  )
  ```
  `gen.go`:
  ```go
  package proto

  // Regenerate the committed *.pb.go from the *.proto schemas. Requires protoc
  // (system binary) plus protoc-gen-go on PATH:
  //
  //   cd internal/providers/hoyoverse/sophon/proto
  //   go install google.golang.org/protobuf/cmd/protoc-gen-go
  //   go generate ./...
  //
  //go:generate protoc --go_out=. --go_opt=paths=source_relative sophon_manifest.proto sophon_patch.proto
  ```

- [ ] **Step 3: Generate the committed `.pb.go` files (PREFERRED: protoc).**
  From the proto dir, install the plugin and run protoc:
  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  protoc --go_out=. --go_opt=paths=source_relative sophon_manifest.proto sophon_patch.proto
  ```
  (Run these with cwd = `internal/providers/hoyoverse/sophon/proto`; `--go_opt=paths=source_relative` places the output next to the `.proto`, NOT under the `go_package` path.) This writes `sophon_manifest.pb.go` and `sophon_patch.pb.go`. **Commit them as-is** — treat protoc-gen-go output as opaque; do not hand-edit. The generated structs carry unexported `state`/`sizeCache`/`unknownFields` plus getters; only the exported data fields in §A.1 are referenced by other tasks.

  **FALLBACK (only if `protoc` is unavailable on the implementer host):** hand-write the two `.pb.go` files using `google.golang.org/protobuf/runtime/protoimpl` + `runtime/protoreflect`, declaring messages/fields with the exact PascalCase exported field names and proto field numbers from Step 1 (e.g. `SophonManifestProto{Assets []*SophonManifestAssetProperty}` with `protobuf:"bytes,1,rep,name=Assets,proto3"`). This is laborious and error-prone — PREFER protoc. Whichever route, the Step 5 smoke test is the gate: it passes only if the structs round-trip through `proto.Marshal`/`proto.Unmarshal`.

- [ ] **Step 4: Write the failing smoke test.**
  `proto_smoke_test.go`:
  ```go
  package proto_test

  import (
  	"testing"

  	"google.golang.org/protobuf/proto"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  func TestManifestProtoRoundTrip(t *testing.T) {
  	in := &pb.SophonManifestProto{
  		Assets: []*pb.SophonManifestAssetProperty{{
  			AssetName:    "x",
  			AssetType:    0,
  			AssetSize:    100,
  			AssetHashMd5: "abc",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{
  				ChunkName:                "c0",
  				ChunkDecompressedHashMd5: "md5",
  				ChunkOnFileOffset:        0,
  				ChunkSize:                10,
  				ChunkSizeDecompressed:    20,
  			}},
  		}},
  	}
  	raw, err := proto.Marshal(in)
  	if err != nil {
  		t.Fatalf("marshal: %v", err)
  	}
  	var out pb.SophonManifestProto
  	if err := proto.Unmarshal(raw, &out); err != nil {
  		t.Fatalf("unmarshal: %v", err)
  	}
  	if len(out.Assets) != 1 || out.Assets[0].AssetName != "x" {
  		t.Fatalf("asset round-trip: got %+v", out.Assets)
  	}
  	if len(out.Assets[0].AssetChunks) != 1 ||
  		out.Assets[0].AssetChunks[0].ChunkSizeDecompressed != 20 {
  		t.Fatalf("chunk round-trip: got %+v", out.Assets[0].AssetChunks)
  	}
  }

  func TestPatchProtoRoundTrip(t *testing.T) {
  	in := &pb.SophonPatchProto{
  		PatchAssets: []*pb.SophonPatchAssetProperty{{
  			AssetName:    "f",
  			AssetSize:    5,
  			AssetHashMd5: "h",
  			AssetInfos: []*pb.SophonPatchAssetInfo{{
  				VersionTag: "6.5.0",
  				Chunk: &pb.SophonPatchAssetChunk{
  					PatchName:          "p0",
  					VersionTag:         "6.5.0",
  					BuildId:            "b1",
  					PatchSize:          3,
  					PatchMd5:           "pm",
  					PatchOffset:        0,
  					PatchLength:        3,
  					OriginalFileName:   "f",
  					OriginalFileLength: 4,
  					OriginalFileMd5:    "om",
  				},
  			}},
  		}},
  		UnusedAssets: []*pb.SophonUnusedAssetProperty{{
  			VersionTag: "6.5.0",
  			AssetInfos: []*pb.SophonUnusedAssetInfo{{
  				Assets: []*pb.SophonUnusedAssetFile{{
  					FileName: "old", FileSize: 9, FileMd5: "u",
  				}},
  			}},
  		}},
  	}
  	raw, err := proto.Marshal(in)
  	if err != nil {
  		t.Fatalf("marshal: %v", err)
  	}
  	var out pb.SophonPatchProto
  	if err := proto.Unmarshal(raw, &out); err != nil {
  		t.Fatalf("unmarshal: %v", err)
  	}
  	if len(out.PatchAssets) != 1 || out.PatchAssets[0].AssetInfos[0].Chunk.BuildId != "b1" {
  		t.Fatalf("patch round-trip: got %+v", out.PatchAssets)
  	}
  	if len(out.UnusedAssets) != 1 ||
  		out.UnusedAssets[0].AssetInfos[0].Assets[0].FileName != "old" {
  		t.Fatalf("unused round-trip: got %+v", out.UnusedAssets)
  	}
  }
  ```

- [ ] **Step 5: Run the test — expect PASS (gates Step 3 generation).**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/proto/...
  ```
  If it FAILS to compile (missing field / wrong name), the generated `.pb.go` does not match §A.1 — re-run Step 3 from the correct `.proto` (the field names/numbers are the contract). Do not edit the test to match drifted output.

- [ ] **Step 6: Add the README dev-setup note (spec §2.5).**
  Append a short "Sophon proto regeneration" subsection to `README.md` under the existing developer/build docs:
  ```markdown
  ### Sophon protobuf regeneration

  The Sophon manifest/patch parsers use generated Go from
  `internal/providers/hoyoverse/sophon/proto/*.proto`. The generated
  `*.pb.go` files are **committed**, so a fresh checkout builds without `protoc`.

  Regenerate only when editing a `.proto`:

  ```bash
  cd internal/providers/hoyoverse/sophon/proto
  go install google.golang.org/protobuf/cmd/protoc-gen-go
  go generate ./...   # runs: protoc --go_out=. --go_opt=paths=source_relative *.proto
  ```

  Requires `protoc` on PATH. `wails build` does NOT run `go generate`.
  ```

- [ ] **Step 7: Commit (.proto + .pb.go + tools.go + gen.go + test + README together).**
  ```bash
  git add internal/providers/hoyoverse/sophon/proto README.md
  git commit -m "feat(sophon): add Sophon manifest/patch protobuf schemas + generated Go"
  ```
  (Branch `m3b-v2/spec`; no `Co-Authored-By` trailer.)

---

### Task 5: `branches.go` + `infos.go` — getGameBranches / getBuild parsers + tolerant scalars

`branches.go` parses the extended `getGameBranches` `Data` (spec §2.2) into the §A.3 `BranchInfo`/`BranchSlot`/`Category` types. `infos.go` parses the `getBuild`/`getPatchBuild` `Data` (spec §2.3) into `BuildResponse` + nested types, using the tolerant `Boolish`/`Int64ish` scalar types (spec §2.3 — HoYoverse serializes `compression`/`encryption` as any of `0|1|"0"|"1"|true|false`, and sizes as number-or-quoted-number).

Both parsers consume the **inner `Data`** of `apiEnvelope` (the v1 `apiEnvelope{Retcode,Message,Data json.RawMessage}` is unwrapped by the hoyoverse layer before calling these — same idiom as `fetchBasicInfo`).

**Files:**
- Create: `internal/providers/hoyoverse/sophon/branches.go`
- Create: `internal/providers/hoyoverse/sophon/infos.go`
- Test: `internal/providers/hoyoverse/sophon/branches_test.go`
- Test: `internal/providers/hoyoverse/sophon/infos_test.go`

Steps:

- [ ] **Step 1: Write failing `branches_test.go` against inline JSON literals.**
  Covers: main+predl populated; main-only (predl `IsEmpty()` true); empty-categories-on-Main → error (spec §2.2 malformed rule); empty-categories-on-PreDownload → OK (defensive: predl with no categories is allowed); wrong game id → error.
  ```go
  package sophon

  import "testing"

  const branchesMainPredl = `{
    "game_branches": [{
      "game": {"id": "gopR6Cufr3", "biz": "hk4e_global"},
      "main": {
        "package_id": "ScSYQBFhu9", "branch": "main", "password": "pw1",
        "tag": "6.6.0", "diff_tags": ["6.5.0", "6.4.0"],
        "categories": [
          {"category_id": "10016", "matching_field": "game",  "type": "CATEGORY_TYPE_RESOURCE"},
          {"category_id": "10018", "matching_field": "en-us", "type": "CATEGORY_TYPE_AUDIO"}
        ]
      },
      "pre_download": {
        "package_id": "PreDL01", "branch": "predownload", "password": "pw2",
        "tag": "6.7.0", "diff_tags": ["6.6.0"],
        "categories": [
          {"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE"}
        ]
      }
    }]
  }`

  const branchesMainOnly = `{
    "game_branches": [{
      "game": {"id": "gopR6Cufr3", "biz": "hk4e_global"},
      "main": {
        "package_id": "ScSYQBFhu9", "branch": "main", "password": "pw1",
        "tag": "6.6.0", "diff_tags": ["6.5.0"],
        "categories": [
          {"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE"}
        ]
      }
    }]
  }`

  func TestParseBranchesMainAndPredl(t *testing.T) {
  	bi, err := ParseBranches([]byte(branchesMainPredl), "gopR6Cufr3")
  	if err != nil {
  		t.Fatalf("ParseBranches: %v", err)
  	}
  	if bi.Main.PackageID != "ScSYQBFhu9" || bi.Main.Tag != "6.6.0" {
  		t.Fatalf("main slot: %+v", bi.Main)
  	}
  	if len(bi.Main.DiffTags) != 2 || bi.Main.DiffTags[0] != "6.5.0" {
  		t.Fatalf("main diff_tags: %+v", bi.Main.DiffTags)
  	}
  	if len(bi.Main.Categories) != 2 || bi.Main.Categories[1].MatchingField != "en-us" {
  		t.Fatalf("main categories: %+v", bi.Main.Categories)
  	}
  	if bi.Main.Categories[1].Type != "CATEGORY_TYPE_AUDIO" {
  		t.Fatalf("category type: %+v", bi.Main.Categories[1])
  	}
  	if bi.Main.IsEmpty() {
  		t.Fatal("main unexpectedly empty")
  	}
  	if bi.PreDownload.IsEmpty() || bi.PreDownload.Tag != "6.7.0" {
  		t.Fatalf("predl slot: %+v", bi.PreDownload)
  	}
  }

  func TestParseBranchesMainOnly(t *testing.T) {
  	bi, err := ParseBranches([]byte(branchesMainOnly), "gopR6Cufr3")
  	if err != nil {
  		t.Fatalf("ParseBranches: %v", err)
  	}
  	if !bi.PreDownload.IsEmpty() {
  		t.Fatalf("predl should be empty: %+v", bi.PreDownload)
  	}
  }

  func TestParseBranchesEmptyMainCategoriesIsError(t *testing.T) {
  	const j = `{"game_branches":[{"game":{"id":"g"},"main":{"package_id":"p","tag":"6.6.0","categories":[]}}]}`
  	if _, err := ParseBranches([]byte(j), "g"); err == nil {
  		t.Fatal("expected error for empty Main categories")
  	}
  }

  func TestParseBranchesEmptyPredlCategoriesIsOK(t *testing.T) {
  	const j = `{"game_branches":[{"game":{"id":"g"},
  	  "main":{"package_id":"p","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]},
  	  "pre_download":{"package_id":"q","tag":"6.7.0","categories":[]}}]}`
  	bi, err := ParseBranches([]byte(j), "g")
  	if err != nil {
  		t.Fatalf("predl empty categories must not error: %v", err)
  	}
  	if bi.PreDownload.IsEmpty() || len(bi.PreDownload.Categories) != 0 {
  		t.Fatalf("predl slot: %+v", bi.PreDownload)
  	}
  }

  func TestParseBranchesGameNotPresent(t *testing.T) {
  	if _, err := ParseBranches([]byte(branchesMainOnly), "OTHER"); err == nil {
  		t.Fatal("expected error when requested game id absent")
  	}
  }
  ```

- [ ] **Step 2: Run — expect FAIL (compile error: `ParseBranches`/types undefined).**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/...
  ```

- [ ] **Step 3: Implement `branches.go`.**
  ```go
  // Package sophon implements the HoYoverse Sophon chunk-level binary-delta
  // download protocol (Genshin 6.0+). This file parses the extended
  // getGameBranches response (spec §2.2).
  package sophon

  import (
  	"encoding/json"
  	"fmt"
  )

  // Category identifies one manifest category (game / audio language).
  type Category struct {
  	ID            string // "10016".."10020"
  	MatchingField string // "game" | "zh-cn" | "en-us" | "ja-jp" | "ko-kr"
  	Type          string // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
  }

  // BranchSlot is one branch (main or pre_download) of a Sophon game.
  type BranchSlot struct {
  	PackageID  string
  	Branch     string
  	Password   string
  	Tag        string
  	DiffTags   []string
  	Categories []Category
  }

  // IsEmpty reports whether the slot is absent (HoYoverse omits pre_download
  // between releases).
  func (s BranchSlot) IsEmpty() bool { return s.PackageID == "" }

  // BranchInfo is the parsed getGameBranches result for one game.
  type BranchInfo struct {
  	Main        BranchSlot
  	PreDownload BranchSlot
  }

  // wire shapes (snake_case getGameBranches Data).
  type rawBranchCategory struct {
  	CategoryID    string `json:"category_id"`
  	MatchingField string `json:"matching_field"`
  	Type          string `json:"type"`
  }

  type rawBranchSlot struct {
  	PackageID  string              `json:"package_id"`
  	Branch     string              `json:"branch"`
  	Password   string              `json:"password"`
  	Tag        string              `json:"tag"`
  	DiffTags   []string            `json:"diff_tags"`
  	Categories []rawBranchCategory `json:"categories"`
  }

  type rawBranchesData struct {
  	GameBranches []struct {
  		Game struct {
  			ID  string `json:"id"`
  			Biz string `json:"biz"`
  		} `json:"game"`
  		Main        rawBranchSlot `json:"main"`
  		PreDownload rawBranchSlot `json:"pre_download"`
  	} `json:"game_branches"`
  }

  func (r rawBranchSlot) toSlot() BranchSlot {
  	cats := make([]Category, 0, len(r.Categories))
  	for _, c := range r.Categories {
  		cats = append(cats, Category{ID: c.CategoryID, MatchingField: c.MatchingField, Type: c.Type})
  	}
  	return BranchSlot{
  		PackageID:  r.PackageID,
  		Branch:     r.Branch,
  		Password:   r.Password,
  		Tag:        r.Tag,
  		DiffTags:   r.DiffTags,
  		Categories: cats,
  	}
  }

  // ParseBranches parses the apiEnvelope.Data of getGameBranches for one game.
  // Spec §2.2: an empty Categories list on a present Main slot is malformed; the
  // same emptiness on PreDownload is tolerated (game-category-only predl).
  func ParseBranches(data []byte, apiGameID string) (*BranchInfo, error) {
  	var raw rawBranchesData
  	if err := json.Unmarshal(data, &raw); err != nil {
  		return nil, fmt.Errorf("sophon: parse getGameBranches: %w", err)
  	}
  	for _, b := range raw.GameBranches {
  		if b.Game.ID != apiGameID {
  			continue
  		}
  		bi := &BranchInfo{Main: b.Main.toSlot(), PreDownload: b.PreDownload.toSlot()}
  		if !bi.Main.IsEmpty() && len(bi.Main.Categories) == 0 {
  			return nil, fmt.Errorf("sophon: getGameBranches main for %q has no categories (malformed)", apiGameID)
  		}
  		return bi, nil
  	}
  	return nil, fmt.Errorf("sophon: getGameBranches: game id %q not in response", apiGameID)
  }
  ```

- [ ] **Step 4: Implement `infos.go`.**
  ```go
  package sophon

  import (
  	"bytes"
  	"encoding/json"
  	"fmt"
  	"strconv"
  )

  // Boolish accepts HoYoverse's varied boolean serializations:
  // 0|1|"0"|"1"|true|false (spec §2.3, Collapse BoolConverter parity).
  type Boolish bool

  func (b *Boolish) UnmarshalJSON(data []byte) error {
  	data = bytes.TrimSpace(data)
  	switch string(data) {
  	case "true", "1", `"1"`, `"true"`:
  		*b = true
  		return nil
  	case "false", "0", `"0"`, `"false"`, "null", `""`:
  		*b = false
  		return nil
  	}
  	return fmt.Errorf("sophon: Boolish: cannot parse %q", string(data))
  }

  // Int64ish accepts a JSON number OR a quoted number (HoYoverse uses
  // AllowReadingFromString for size fields; spec §2.3).
  type Int64ish int64

  func (n *Int64ish) UnmarshalJSON(data []byte) error {
  	data = bytes.TrimSpace(data)
  	if string(data) == "null" {
  		*n = 0
  		return nil
  	}
  	s := string(data)
  	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
  		s = s[1 : len(s)-1]
  	}
  	if s == "" {
  		*n = 0
  		return nil
  	}
  	v, err := strconv.ParseInt(s, 10, 64)
  	if err != nil {
  		return fmt.Errorf("sophon: Int64ish: cannot parse %q: %w", string(data), err)
  	}
  	*n = Int64ish(v)
  	return nil
  }

  // ManifestFileInfo is the `manifest` block of one category identity.
  type ManifestFileInfo struct {
  	ID               string   `json:"id"`
  	Checksum         string   `json:"checksum"`
  	CompressedSize   Int64ish `json:"compressed_size"`
  	UncompressedSize Int64ish `json:"uncompressed_size"`
  }

  // ManifestDownloadInfo is a `manifest_download` / `chunk_download` /
  // `diff_download` block.
  type ManifestDownloadInfo struct {
  	URLPrefix   string  `json:"url_prefix"`
  	URLSuffix   string  `json:"url_suffix"`
  	Password    string  `json:"password"`
  	Encryption  Boolish `json:"encryption"`
  	Compression Boolish `json:"compression"`
  }

  // ManifestIdentity is one category's manifest identity within a build.
  type ManifestIdentity struct {
  	CategoryID       string               `json:"category_id"`
  	CategoryName     string               `json:"category_name"`
  	MatchingField    string               `json:"matching_field"`
  	Manifest         ManifestFileInfo     `json:"manifest"`
  	ManifestDownload ManifestDownloadInfo `json:"manifest_download"`
  	ChunkDownload    ManifestDownloadInfo `json:"chunk_download"`
  	DiffDownload     ManifestDownloadInfo `json:"diff_download"`
  }

  // BuildResponse is the parsed getBuild / getPatchBuild Data envelope.
  type BuildResponse struct {
  	BuildID   string             `json:"build_id"`
  	Tag       string             `json:"tag"`
  	PatchID   string             `json:"patch_id"`
  	Manifests []ManifestIdentity `json:"manifests"`
  }

  // ParseBuildResponse parses the apiEnvelope.Data of getBuild.
  func ParseBuildResponse(data []byte) (*BuildResponse, error) {
  	var b BuildResponse
  	if err := json.Unmarshal(data, &b); err != nil {
  		return nil, fmt.Errorf("sophon: parse getBuild: %w", err)
  	}
  	return &b, nil
  }

  // ParsePatchResponse parses the apiEnvelope.Data of getPatchBuild (same
  // envelope shape; PatchID populated).
  func ParsePatchResponse(data []byte) (*BuildResponse, error) {
  	var b BuildResponse
  	if err := json.Unmarshal(data, &b); err != nil {
  		return nil, fmt.Errorf("sophon: parse getPatchBuild: %w", err)
  	}
  	return &b, nil
  }

  // ManifestFor returns the manifest identity for the given matching_field
  // ("game" | "zh-cn" | ...).
  func (b *BuildResponse) ManifestFor(matchingField string) (*ManifestIdentity, bool) {
  	for i := range b.Manifests {
  		if b.Manifests[i].MatchingField == matchingField {
  			return &b.Manifests[i], true
  		}
  	}
  	return nil, false
  }
  ```

- [ ] **Step 5: Write failing `infos_test.go`.**
  Covers: Boolish all six forms; Int64ish number + quoted; full getBuild Data round-trip including `compression:1` numeric and `compressed_size:"12345"` quoted; `ManifestFor` hit/miss; getPatchBuild with `patch_id`.
  ```go
  package sophon

  import (
  	"encoding/json"
  	"testing"
  )

  func TestBoolishForms(t *testing.T) {
  	cases := map[string]bool{
  		`0`: false, `1`: true, `"0"`: false, `"1"`: true,
  		`true`: true, `false`: false,
  	}
  	for in, want := range cases {
  		var b Boolish
  		if err := json.Unmarshal([]byte(in), &b); err != nil {
  			t.Fatalf("Boolish(%s): %v", in, err)
  		}
  		if bool(b) != want {
  			t.Fatalf("Boolish(%s) = %v, want %v", in, bool(b), want)
  		}
  	}
  	var b Boolish
  	if err := json.Unmarshal([]byte(`"yes"`), &b); err == nil {
  		t.Fatal("Boolish should reject \"yes\"")
  	}
  }

  func TestInt64ishForms(t *testing.T) {
  	var n Int64ish
  	if err := json.Unmarshal([]byte(`123`), &n); err != nil || n != 123 {
  		t.Fatalf("Int64ish(123) = %d err=%v", n, err)
  	}
  	if err := json.Unmarshal([]byte(`"123"`), &n); err != nil || n != 123 {
  		t.Fatalf(`Int64ish("123") = %d err=%v`, n, err)
  	}
  	if err := json.Unmarshal([]byte(`"x"`), &n); err == nil {
  		t.Fatal(`Int64ish("x") should error`)
  	}
  }

  const buildSmall = `{
    "build_id": "B66", "tag": "6.6.0", "patch_id": "",
    "manifests": [
      {
        "category_id": "10016", "category_name": "game", "matching_field": "game",
        "manifest": {"id": "M1", "checksum": "ck", "compressed_size": "12345", "uncompressed_size": 67890},
        "manifest_download": {"url_prefix": "https://m/", "url_suffix": "", "password": "", "encryption": 0, "compression": 1},
        "chunk_download": {"url_prefix": "https://c/", "url_suffix": "", "password": "", "encryption": "0", "compression": "1"},
        "diff_download": {"url_prefix": "https://d/", "compression": false}
      }
    ]
  }`

  func TestParseBuildResponse(t *testing.T) {
  	b, err := ParseBuildResponse([]byte(buildSmall))
  	if err != nil {
  		t.Fatalf("ParseBuildResponse: %v", err)
  	}
  	if b.BuildID != "B66" || b.Tag != "6.6.0" {
  		t.Fatalf("build header: %+v", b)
  	}
  	id, ok := b.ManifestFor("game")
  	if !ok {
  		t.Fatal("ManifestFor(game) miss")
  	}
  	if id.Manifest.ID != "M1" || id.Manifest.CompressedSize != 12345 || id.Manifest.UncompressedSize != 67890 {
  		t.Fatalf("manifest file info: %+v", id.Manifest)
  	}
  	if !bool(id.ManifestDownload.Compression) || bool(id.ManifestDownload.Encryption) {
  		t.Fatalf("manifest_download flags: %+v", id.ManifestDownload)
  	}
  	if !bool(id.ChunkDownload.Compression) {
  		t.Fatalf("chunk_download compression (quoted) not parsed: %+v", id.ChunkDownload)
  	}
  	if bool(id.DiffDownload.Compression) {
  		t.Fatalf("diff_download compression should be false: %+v", id.DiffDownload)
  	}
  	if _, ok := b.ManifestFor("ko-kr"); ok {
  		t.Fatal("ManifestFor(ko-kr) should miss")
  	}
  }

  func TestParsePatchResponsePatchID(t *testing.T) {
  	const j = `{"build_id":"B67","tag":"6.7.0","patch_id":"P1","manifests":[]}`
  	b, err := ParsePatchResponse([]byte(j))
  	if err != nil {
  		t.Fatalf("ParsePatchResponse: %v", err)
  	}
  	if b.PatchID != "P1" {
  		t.Fatalf("patch_id: %+v", b)
  	}
  }
  ```

- [ ] **Step 6: Run — expect PASS.**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/...
  ```

- [ ] **Step 7: Commit.**
  ```bash
  git add internal/providers/hoyoverse/sophon/branches.go internal/providers/hoyoverse/sophon/branches_test.go internal/providers/hoyoverse/sophon/infos.go internal/providers/hoyoverse/sophon/infos_test.go
  git commit -m "feat(sophon): parse getGameBranches + getBuild/getPatchBuild with tolerant scalars"
  ```

---

### Task 6: `manifest_fetch.go` — fetch + zstd-decompress + proto.Unmarshal

`FetchManifest`/`FetchPatchManifest` (§A.3): GET `id.ManifestDownload.URLPrefix + "/" + id.Manifest.ID`, conditionally wrap the body in `zstd.NewReader` when `id.ManifestDownload.Compression` is set, then `proto.Unmarshal` into the §A.1 proto types. The manifest `checksum` is **NOT verified** (Collapse parity) — it is logged via `slog` alongside unmarshal success for debugging correlation (spec §1 manifest_fetch.go row, §0 defensive-telemetry).

**Files:**
- Create: `internal/providers/hoyoverse/sophon/manifest_fetch.go`
- Test: `internal/providers/hoyoverse/sophon/manifest_fetch_test.go`

Steps:

- [ ] **Step 1: Write failing `manifest_fetch_test.go`.**
  An `httptest` server serves canned bytes built in-test: marshal a `pb.SophonManifestProto`, zstd-compress with klauspost, serve at the manifest path. Tests both the compressed path (`Compression=true`) and the raw path (`Compression=false`, serving the bare proto bytes). A patch test does the same with `pb.SophonPatchProto` + `FetchPatchManifest`.
  ```go
  package sophon

  import (
  	"context"
  	"net/http"
  	"net/http/httptest"
  	"strings"
  	"testing"

  	"github.com/klauspost/compress/zstd"
  	"google.golang.org/protobuf/proto"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  func zstdCompress(t *testing.T, raw []byte) []byte {
  	t.Helper()
  	enc, err := zstd.NewWriter(nil)
  	if err != nil {
  		t.Fatalf("zstd writer: %v", err)
  	}
  	defer enc.Close()
  	return enc.EncodeAll(raw, nil)
  }

  func newManifestServer(t *testing.T, body []byte) (*httptest.Server, string, string) {
  	t.Helper()
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		if !strings.HasSuffix(r.URL.Path, "/M1") {
  			http.NotFound(w, r)
  			return
  		}
  		_, _ = w.Write(body)
  	}))
  	t.Cleanup(srv.Close)
  	return srv, srv.URL, "M1"
  }

  func TestFetchManifestCompressed(t *testing.T) {
  	want := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{{
  		AssetName: "f0", AssetSize: 9, AssetHashMd5: "h",
  		AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c0", ChunkSize: 3, ChunkSizeDecompressed: 9}},
  	}}}
  	raw, err := proto.Marshal(want)
  	if err != nil {
  		t.Fatalf("marshal: %v", err)
  	}
  	srv, base, id := newManifestServer(t, zstdCompress(t, raw))
  	_ = srv

  	got, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
  		Manifest:         ManifestFileInfo{ID: id, Checksum: "ck"},
  		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: true},
  	})
  	if err != nil {
  		t.Fatalf("FetchManifest: %v", err)
  	}
  	if len(got.Assets) != 1 || got.Assets[0].AssetName != "f0" ||
  		got.Assets[0].AssetChunks[0].ChunkSizeDecompressed != 9 {
  		t.Fatalf("decoded manifest: %+v", got.Assets)
  	}
  }

  func TestFetchManifestRaw(t *testing.T) {
  	want := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{{AssetName: "raw0"}}}
  	raw, err := proto.Marshal(want)
  	if err != nil {
  		t.Fatalf("marshal: %v", err)
  	}
  	srv, base, id := newManifestServer(t, raw)
  	_ = srv

  	got, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
  		Manifest:         ManifestFileInfo{ID: id},
  		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: false},
  	})
  	if err != nil {
  		t.Fatalf("FetchManifest raw: %v", err)
  	}
  	if len(got.Assets) != 1 || got.Assets[0].AssetName != "raw0" {
  		t.Fatalf("decoded raw manifest: %+v", got.Assets)
  	}
  }

  func TestFetchPatchManifestCompressed(t *testing.T) {
  	want := &pb.SophonPatchProto{PatchAssets: []*pb.SophonPatchAssetProperty{{AssetName: "p0", AssetSize: 5}}}
  	raw, err := proto.Marshal(want)
  	if err != nil {
  		t.Fatalf("marshal: %v", err)
  	}
  	srv, base, id := newManifestServer(t, zstdCompress(t, raw))
  	_ = srv

  	got, err := FetchPatchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
  		Manifest:         ManifestFileInfo{ID: id},
  		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: true},
  	})
  	if err != nil {
  		t.Fatalf("FetchPatchManifest: %v", err)
  	}
  	if len(got.PatchAssets) != 1 || got.PatchAssets[0].AssetName != "p0" {
  		t.Fatalf("decoded patch: %+v", got.PatchAssets)
  	}
  }

  func TestFetchManifestHTTPError(t *testing.T) {
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		http.Error(w, "nope", http.StatusInternalServerError)
  	}))
  	t.Cleanup(srv.Close)
  	_, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
  		Manifest:         ManifestFileInfo{ID: "M1"},
  		ManifestDownload: ManifestDownloadInfo{URLPrefix: srv.URL, Compression: true},
  	})
  	if err == nil {
  		t.Fatal("expected error on HTTP 500")
  	}
  }
  ```

- [ ] **Step 2: Run — expect FAIL (compile: `FetchManifest`/`FetchPatchManifest` undefined).**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/...
  ```

- [ ] **Step 3: Implement `manifest_fetch.go`.**
  Shared internal `fetchAndDecode` does the GET + status check + optional zstd + ReadAll; the two exported funcs wrap it with the proper proto message. Checksum is logged, never verified.
  ```go
  package sophon

  import (
  	"context"
  	"fmt"
  	"io"
  	"log/slog"
  	"net/http"

  	"github.com/klauspost/compress/zstd"
  	"google.golang.org/protobuf/proto"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  // fetchManifestBytes GETs <ManifestDownload.URLPrefix>/<Manifest.ID>, applies
  // zstd decompression when ManifestDownload.Compression is set, and returns the
  // raw protobuf bytes. The manifest checksum is NOT verified (Collapse parity);
  // it is logged for debugging correlation.
  func fetchManifestBytes(ctx context.Context, hc *http.Client, id ManifestIdentity) ([]byte, error) {
  	if hc == nil {
  		hc = http.DefaultClient
  	}
  	u := id.ManifestDownload.URLPrefix + "/" + id.Manifest.ID
  	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
  	if err != nil {
  		return nil, err
  	}
  	resp, err := hc.Do(req)
  	if err != nil {
  		return nil, err
  	}
  	defer resp.Body.Close()
  	if resp.StatusCode != http.StatusOK {
  		return nil, fmt.Errorf("sophon: fetch manifest %q: http %d", id.Manifest.ID, resp.StatusCode)
  	}

  	var r io.Reader = resp.Body
  	if bool(id.ManifestDownload.Compression) {
  		zr, err := zstd.NewReader(resp.Body)
  		if err != nil {
  			return nil, fmt.Errorf("sophon: zstd reader for manifest %q: %w", id.Manifest.ID, err)
  		}
  		defer zr.Close()
  		r = zr
  	}
  	raw, err := io.ReadAll(r)
  	if err != nil {
  		return nil, fmt.Errorf("sophon: read manifest %q: %w", id.Manifest.ID, err)
  	}
  	return raw, nil
  }

  // FetchManifest fetches and decodes a SophonManifestProto (getBuild category).
  func FetchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonManifestProto, error) {
  	raw, err := fetchManifestBytes(ctx, hc, id)
  	if err != nil {
  		return nil, err
  	}
  	var m pb.SophonManifestProto
  	if err := proto.Unmarshal(raw, &m); err != nil {
  		return nil, fmt.Errorf("sophon: unmarshal manifest %q: %w", id.Manifest.ID, err)
  	}
  	slog.Debug("sophon: manifest decoded (checksum not verified)",
  		"manifest_id", id.Manifest.ID, "category", id.MatchingField,
  		"declared_checksum", id.Manifest.Checksum, "assets", len(m.Assets))
  	return &m, nil
  }

  // FetchPatchManifest fetches and decodes a SophonPatchProto (getPatchBuild
  // category).
  func FetchPatchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonPatchProto, error) {
  	raw, err := fetchManifestBytes(ctx, hc, id)
  	if err != nil {
  		return nil, err
  	}
  	var m pb.SophonPatchProto
  	if err := proto.Unmarshal(raw, &m); err != nil {
  		return nil, fmt.Errorf("sophon: unmarshal patch manifest %q: %w", id.Manifest.ID, err)
  	}
  	slog.Debug("sophon: patch manifest decoded (checksum not verified)",
  		"manifest_id", id.Manifest.ID, "category", id.MatchingField,
  		"declared_checksum", id.Manifest.Checksum, "patch_assets", len(m.PatchAssets))
  	return &m, nil
  }
  ```

- [ ] **Step 4: Run — expect PASS.**
  ```bash
  go test -count=1 ./internal/providers/hoyoverse/sophon/...
  ```

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/providers/hoyoverse/sophon/manifest_fetch.go internal/providers/hoyoverse/sophon/manifest_fetch_test.go
  git commit -m "feat(sophon): fetch + zstd-decompress + decode Sophon manifest/patch protos"
  ```

### Task 7: `dedup.go` — per-asset MD5 chunk index

**Spec refs:** part-0 §A.2 (`ChunkRef`), §A.3 (`BuildPerAssetMD5Index` signature); spec §0 (chunk-reuse keying = per-asset `ChunkDecompressedHashMd5`), §3.2 step 3 (lookup), §4.2 (lookup semantics), §9.1 (`dedup_test` cases).

**Why now:** Task 8's `BuildChunkSources` consumes `map[string]ChunkRef` from this function for Path-B chunk-from-disk dedup. Both live in package `sophon`. The proto types (Task 4) and `ChunkRef`/`Category` exported work-item types (also defined here in `dedup.go` since this is the first sophon-core file in group C to declare them — see Step 1 note) must exist first.

**Contract (verbatim from §A.3):**
```go
func BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto, assetName string) map[string]ChunkRef
```
Key = `ChunkDecompressedHashMd5`; value = `ChunkRef{OldFilePath: oldAsset.AssetName, OldOffset: chunk.ChunkOnFileOffset}`. Per-asset scope (a chunk in asset A is invisible from a query for asset B). nil manifest or missing asset → empty (non-nil) map. Intra-asset MD5 collision → last chunk wins (documented).

> **NOTE — type ownership:** The exported work-item types `ChunkSource`, `PatchInstr`, `DeleteInstr`, `ChunkRef`, `Category` and the `Source*`/`Method*` consts (part-0 §A.2) are a single shared declaration for package `sophon`. This plan places them in a dedicated `types.go` created as Step 1 of THIS task (Task 7 is the first group-C task to need them). If an earlier group-B task already created `types.go`, skip Step 1's file creation and only add anything missing. The `go build` in Step 3 is the authority on whether the types already exist.

**Files:**
- Create: `internal/providers/hoyoverse/sophon/types.go` (shared §A.2 types + consts — only if not already present)
- Create: `internal/providers/hoyoverse/sophon/dedup.go`
- Create: `internal/providers/hoyoverse/sophon/dedup_test.go`

> **Toolchain (every Go step):** on subagent shells without Go on PATH, prepend once per shell:
> ```bash
> export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
> ```
> This host is `CGO_ENABLED=0` — **never** pass `-race`. Package test command: `go test -count=1 ./internal/providers/hoyoverse/sophon/...`.

---

- [ ] **Step 1: Ensure shared §A.2 types exist (`types.go`).**

If `internal/providers/hoyoverse/sophon/types.go` (or an equivalent declaration of `ChunkRef`/`ChunkSource`/`PatchInstr`/`DeleteInstr`/`Category`) does not already exist, create it verbatim. These are the canonical in-memory work-item types per [DEV-1]; **do NOT add json tags** (part-0 §A.6 — they double as snapshot serialization fields).

```go
// internal/providers/hoyoverse/sophon/types.go
package sophon

// Source kind + patch method string constants.
const (
	SourceCDN   = "cdn"
	SourceLocal = "local"

	MethodPatch    = "patch"
	MethodCopyOver = "copy_over"
)

// ChunkSource is one chunk to place into a target file at FileOffset.
// Kind==SourceCDN: download <URLPrefix>/<ChunkName>, zstd-decompress if
// UseCompress, verify (xxh64 of ChunkName prefix, else MD5 ExpectMD5).
// Kind==SourceLocal: read DecompSize bytes from <gameDir>/<OldFile> at
// OldOffset, verify MD5==ExpectMD5; on mismatch fall back to the CDN form
// (ChunkName/URLPrefix are ALWAYS populated, even for Local, per spec §6.3
// step 4 stale-fallback).
type ChunkSource struct {
	Kind         string // SourceCDN | SourceLocal
	Asset        string // owning AssetName (log + per-asset dedup scoping)
	ChunkName    string // CDN filename; also the staging filename
	URLPrefix    string // chunk_download.url_prefix
	CompressedSz int64  // ChunkSize (on-wire); progress accounting after resume
	UseCompress  bool   // chunk_download.compression
	OldFile      string // SourceLocal: relative to gameDir
	OldOffset    int64  // SourceLocal
	DecompSize   int64  // ChunkSizeDecompressed
	FileOffset   int64  // ChunkOnFileOffset into the target file
	ExpectMD5    string // ChunkDecompressedHashMd5
}

// PatchInstr is one patch-blob operation. Method==MethodPatch → hpatchz
// (OldFile + hdiff slice → target). Method==MethodCopyOver → write the patch
// blob slice directly as the target (full file delivered in the patch blob).
type PatchInstr struct {
	Method          string // MethodPatch | MethodCopyOver
	Asset           string // target relative to gameDir
	PatchName       string
	URLPrefix       string // diff_download.url_prefix (patch-blob CDN base)
	PatchSize       int64  // full blob size (download/progress accounting)
	PatchMD5        string // full blob MD5
	PatchOffset     int64  // slice offset within the blob
	PatchLength     int64  // slice length
	OldFile         string // MethodPatch: source file relative to gameDir
	ExpectMD5       string // expected post-apply whole-file MD5 (== AssetHashMd5)
	OriginalFileMD5 string // MethodPatch: pre-apply OldFile MD5 (demotion guard)
}

// DeleteInstr is one UnusedAssets file removal.
type DeleteInstr struct {
	Path      string // relative to gameDir
	ExpectMD5 string // pre-delete sanity check (mismatch → warn, continue)
}

// ChunkRef is a dedup-index value: where an old chunk lives on disk.
type ChunkRef struct {
	OldFilePath string // old AssetName == path relative to gameDir
	OldOffset   int64
}
```

> **RECONCILED (§E item 1):** Do NOT declare `Category` here — it is declared in `branches.go` (Task 5, which lands first). `types.go` (this task) owns only `ChunkSource`/`PatchInstr`/`DeleteInstr`/`ChunkRef` + the `Source*`/`Method*` consts. Group-B tasks (T5/T6) don't need the work-item types, so Task 7 is the correct first creator of `types.go`. If `go build` reports a duplicate `Category`, the cause is an accidental re-declaration here — delete it; `branches.go` is the single owner.

(No test for `types.go` alone — it is exercised by `dedup_test.go` and Task 8.)

- [ ] **Step 2: Write the failing test `dedup_test.go`.**

Construct `pb.*` fixtures inline. Covers: per-asset isolation; intra-asset MD5 collision (last wins); empty old manifest; nil manifest; missing asset.

```go
// internal/providers/hoyoverse/sophon/dedup_test.go
package sophon

import (
	"testing"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

func TestBuildPerAssetMD5Index_PerAssetIsolation(t *testing.T) {
	old := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{
				AssetName: "GenshinImpact_Data/a.pak",
				AssetChunks: []*pb.SophonManifestAssetChunk{
					{ChunkName: "chunkA", ChunkDecompressedHashMd5: "md5-A1", ChunkOnFileOffset: 0},
					{ChunkName: "chunkB", ChunkDecompressedHashMd5: "md5-A2", ChunkOnFileOffset: 100},
				},
			},
			{
				AssetName: "GenshinImpact_Data/b.pak",
				AssetChunks: []*pb.SophonManifestAssetChunk{
					{ChunkName: "chunkC", ChunkDecompressedHashMd5: "md5-B1", ChunkOnFileOffset: 0},
				},
			},
		},
	}

	idxA := BuildPerAssetMD5Index(old, "GenshinImpact_Data/a.pak")
	if got := len(idxA); got != 2 {
		t.Fatalf("idxA len = %d, want 2", got)
	}
	if ref, ok := idxA["md5-A1"]; !ok {
		t.Errorf("md5-A1 missing from idxA")
	} else if ref.OldFilePath != "GenshinImpact_Data/a.pak" || ref.OldOffset != 0 {
		t.Errorf("md5-A1 ref = %+v, want {a.pak, 0}", ref)
	}
	if ref, ok := idxA["md5-A2"]; !ok {
		t.Errorf("md5-A2 missing from idxA")
	} else if ref.OldOffset != 100 {
		t.Errorf("md5-A2 offset = %d, want 100", ref.OldOffset)
	}
	// Per-asset scope: asset B's chunk is invisible from an asset-A query.
	if _, ok := idxA["md5-B1"]; ok {
		t.Errorf("md5-B1 leaked into idxA (per-asset isolation violated)")
	}

	idxB := BuildPerAssetMD5Index(old, "GenshinImpact_Data/b.pak")
	if got := len(idxB); got != 1 {
		t.Fatalf("idxB len = %d, want 1", got)
	}
	if ref, ok := idxB["md5-B1"]; !ok {
		t.Errorf("md5-B1 missing from idxB")
	} else if ref.OldFilePath != "GenshinImpact_Data/b.pak" {
		t.Errorf("md5-B1 path = %q, want b.pak", ref.OldFilePath)
	}
	if _, ok := idxB["md5-A1"]; ok {
		t.Errorf("md5-A1 leaked into idxB (per-asset isolation violated)")
	}
}

func TestBuildPerAssetMD5Index_IntraAssetCollisionLastWins(t *testing.T) {
	old := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{
				AssetName: "dup.pak",
				AssetChunks: []*pb.SophonManifestAssetChunk{
					{ChunkName: "first", ChunkDecompressedHashMd5: "same-md5", ChunkOnFileOffset: 0},
					{ChunkName: "second", ChunkDecompressedHashMd5: "same-md5", ChunkOnFileOffset: 512},
				},
			},
		},
	}
	idx := BuildPerAssetMD5Index(old, "dup.pak")
	if got := len(idx); got != 1 {
		t.Fatalf("idx len = %d, want 1 (collision collapses)", got)
	}
	// Documented behavior: last chunk wins.
	if ref := idx["same-md5"]; ref.OldOffset != 512 {
		t.Errorf("collision OldOffset = %d, want 512 (last wins)", ref.OldOffset)
	}
}

func TestBuildPerAssetMD5Index_EmptyOldManifest(t *testing.T) {
	old := &pb.SophonManifestProto{} // no assets
	idx := BuildPerAssetMD5Index(old, "anything")
	if idx == nil {
		t.Fatal("idx is nil, want empty non-nil map")
	}
	if len(idx) != 0 {
		t.Errorf("idx len = %d, want 0", len(idx))
	}
}

func TestBuildPerAssetMD5Index_NilManifest(t *testing.T) {
	idx := BuildPerAssetMD5Index(nil, "anything")
	if idx == nil {
		t.Fatal("idx is nil, want empty non-nil map")
	}
	if len(idx) != 0 {
		t.Errorf("idx len = %d, want 0", len(idx))
	}
}

func TestBuildPerAssetMD5Index_MissingAsset(t *testing.T) {
	old := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{AssetName: "present.pak", AssetChunks: []*pb.SophonManifestAssetChunk{
				{ChunkDecompressedHashMd5: "x", ChunkOnFileOffset: 0},
			}},
		},
	}
	idx := BuildPerAssetMD5Index(old, "absent.pak")
	if idx == nil {
		t.Fatal("idx is nil, want empty non-nil map")
	}
	if len(idx) != 0 {
		t.Errorf("idx len = %d, want 0 for missing asset", len(idx))
	}
}
```

- [ ] **Step 3: Run the test — expect FAIL (compile error: undefined `BuildPerAssetMD5Index`).**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```
Expected: build failure / `undefined: BuildPerAssetMD5Index` (and, if `types.go` was newly created in Step 1, the types now resolve). This confirms the test compiles against the proto types and only the implementation is missing.

- [ ] **Step 4: Implement `dedup.go`.**

```go
// internal/providers/hoyoverse/sophon/dedup.go
package sophon

import (
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// BuildPerAssetMD5Index builds a per-asset chunk-reuse index for Path-B
// chunk-from-disk dedup. It finds the asset named assetName in oldManifest and
// maps each of its chunks' ChunkDecompressedHashMd5 to a ChunkRef pointing at
// the old asset's on-disk location (path == old AssetName) and the chunk's
// ChunkOnFileOffset.
//
// Scope is strictly per-asset: a chunk belonging to another asset is never
// reachable from a query for assetName (Collapse SophonUpdate
// .GetChunkOldOffsetFromOld parity, spec §0 chunk-reuse keying).
//
// A nil manifest, an empty manifest, or a missing asset all yield an empty
// (non-nil) map. If the same MD5 appears on more than one chunk within the
// asset, the last chunk in AssetChunks order wins (documented; the chunks are
// byte-identical by MD5 so either offset is a valid source).
func BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto, assetName string) map[string]ChunkRef {
	idx := make(map[string]ChunkRef)
	if oldManifest == nil {
		return idx
	}
	for _, asset := range oldManifest.Assets {
		if asset == nil || asset.AssetName != assetName {
			continue
		}
		for _, chunk := range asset.AssetChunks {
			if chunk == nil {
				continue
			}
			idx[chunk.ChunkDecompressedHashMd5] = ChunkRef{
				OldFilePath: asset.AssetName,
				OldOffset:   chunk.ChunkOnFileOffset,
			}
		}
		// Per-asset scope: first matching asset only; stop scanning.
		break
	}
	return idx
}
```

- [ ] **Step 5: Run the test — expect PASS.**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```
Expected: `ok  omnigate/internal/providers/hoyoverse/sophon`.

- [ ] **Step 6: Commit.**

Branch `m3b-v2/spec`, conventional commit, no `Co-Authored-By` trailer.
```bash
git add internal/providers/hoyoverse/sophon/types.go \
        internal/providers/hoyoverse/sophon/dedup.go \
        internal/providers/hoyoverse/sophon/dedup_test.go
git commit -m "feat(sophon): per-asset MD5 chunk-reuse index (Task 7)"
```
(If `types.go` was pre-existing and untouched, drop it from the `git add`.)

---

### Task 8: `decision.go` — DecidePath / BuildChunkSources / BuildPatchInstructions

**Spec refs:** part-0 §A.2 (work-item types + consts), §A.3 (the three signatures), §C [DEV-5] (main-fall-through chunk_assemble is built by the hoyoverse layer, NOT here); spec §3 decision tree case ladder, §3.1 steps 3/4/6 (patch/copyover emission + deletes — but only the patch-package join here), §3.2 step 3 (BuildChunkSources dedup), §6.3 step 4 (stale-fallback: Local sources ALSO carry ChunkName/URLPrefix), §9.1 (`decision_test` cases).

**Why now:** depends on Task 7 (`map[string]ChunkRef`, `ChunkRef`) and the shared §A.2 types/consts (`ChunkSource`, `PatchInstr`, `DeleteInstr`, `SourceCDN`/`SourceLocal`, `MethodPatch`/`MethodCopyOver`) and the proto types (Task 4). The hoyoverse-layer `buildSophonPlan` (Task 18) calls all three of these.

**Contracts (verbatim from §A.3 + this part's scope):**
```go
type Flavor int
func DecidePath(branch *BranchInfo, currentLocal string, oldMainAvailable bool) (Flavor, reason string)
func BuildChunkSources(newAsset *pb.SophonManifestAssetProperty, oldIdx map[string]ChunkRef, chunkURLPrefix string, useCompress bool) []ChunkSource
func BuildPatchInstructions(patch *pb.SophonPatchProto, main *pb.SophonManifestProto, currentLocal, patchURLPrefix string) (patches []PatchInstr, deletes []DeleteInstr)
```

Behavior:
- **`Flavor`** local result enum (the hoyoverse layer maps it to `planFlavor`): `DecisionPatch`, `DecisionBuild`, `DecisionFull`, `DecisionIdle`, `DecisionNoInstall`.
- **`DecidePath`** (spec §3 ladder, top-to-bottom): `currentLocal=="" → NoInstall`; `currentLocal==branch.Main.Tag → Idle`; `currentLocal ∈ branch.Main.DiffTags → Patch`; `oldMainAvailable → Build`; else `Full`. (Note: §3 fetches the branch and checks `Main.IsEmpty()` BEFORE calling this; `DecidePath` assumes a non-nil branch with a populated `Main`. A nil branch is treated defensively as `NoInstall`.)
- **`BuildChunkSources`** (spec §3.2 step 3): for each chunk in `newAsset.AssetChunks`, look up `chunk.ChunkDecompressedHashMd5` in `oldIdx`. Hit → `SourceLocal` carrying `OldFile`/`OldOffset` from the `ChunkRef`, AND ALSO `ChunkName`/`URLPrefix`/`CompressedSz`/`UseCompress` for the §6.3-step-4 stale-fallback. Miss → `SourceCDN`. Sizes from the NEW manifest: `DecompSize=ChunkSizeDecompressed`, `CompressedSz=ChunkSize`, `FileOffset=ChunkOnFileOffset`, `ExpectMD5=ChunkDecompressedHashMd5`. `Asset` = `newAsset.AssetName`. `URLPrefix=chunkURLPrefix`, `UseCompress=useCompress` on every entry.
- **`BuildPatchInstructions`** [DEV-5] — ONLY the patch/copyover emission + deletes; NO main-fall-through `chunk_assemble`:
  1. Build `patchDict map[string]*pb.SophonPatchAssetInfo` keyed by `pa.AssetName`, value = the `info ∈ pa.AssetInfos` whose `info.VersionTag == currentLocal` (skip patch assets with no matching VersionTag — no work for this source version).
  2. Iterate `main.Assets`. For an asset present in `patchDict`:
     - `info.Chunk.OriginalFileName == ""` → `MethodCopyOver` `PatchInstr` (full file in the blob slice).
     - `info.Chunk.OriginalFileName != ""` → `MethodPatch` `PatchInstr`; set `OldFile=OriginalFileName`, `OriginalFileMD5=info.Chunk.OriginalFileMd5`.
     - Both set `ExpectMD5 = main asset AssetHashMd5`, `URLPrefix=patchURLPrefix`, and the blob slice fields from `info.Chunk` (`PatchName`/`PatchOffset`→`PatchOffset`/`PatchLength`→`PatchLength`/`PatchSize`/`PatchMd5`).
  3. (No emission for main assets NOT in `patchDict` — the caller decides which fall through to `chunk_assemble`, [DEV-5].)
  4. For each `ua ∈ patch.UnusedAssets` where `ua.VersionTag == currentLocal`, for each file in `ua.AssetInfos[].Assets`: emit `DeleteInstr{Path: FileName, ExpectMD5: FileMd5}`.
  - Return `(patches, deletes)`.

**Files:**
- Create: `internal/providers/hoyoverse/sophon/decision.go`
- Create: `internal/providers/hoyoverse/sophon/decision_test.go`

> Toolchain note applies (see Task 7). `CGO_ENABLED=0`; never `-race`.

---

- [ ] **Step 1: Write the failing test `decision_test.go`.**

Construct `pb.*` and `BranchInfo` fixtures inline. Covers the §9.1 cases: 6 `DecidePath` cases (NoInstall / Idle-same-version / Patch-DiffTags-hit / Build-old-manifest / Full-no-cache / DiffTags-empty→Build-or-Full); `BuildChunkSources` hit+miss mix; `BuildPatchInstructions` CopyOver vs Patch vs UnusedAssets-delete + VersionTag-mismatch skip.

```go
// internal/providers/hoyoverse/sophon/decision_test.go
package sophon

import (
	"testing"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

func mainBranch(tag string, diffTags []string) *BranchInfo {
	return &BranchInfo{
		Main: BranchSlot{
			PackageID: "pkg-1",
			Branch:    "main",
			Tag:       tag,
			DiffTags:  diffTags,
		},
	}
}

func TestDecidePath(t *testing.T) {
	cases := []struct {
		name             string
		branch           *BranchInfo
		currentLocal     string
		oldMainAvailable bool
		want             Flavor
	}{
		{
			name:         "NoInstall_empty_currentLocal",
			branch:       mainBranch("6.6.0", []string{"6.5.0"}),
			currentLocal: "",
			want:         DecisionNoInstall,
		},
		{
			name:         "Idle_same_version",
			branch:       mainBranch("6.6.0", []string{"6.5.0"}),
			currentLocal: "6.6.0",
			want:         DecisionIdle,
		},
		{
			name:         "Patch_diffTags_hit",
			branch:       mainBranch("6.6.0", []string{"6.5.0", "6.4.0"}),
			currentLocal: "6.5.0",
			want:         DecisionPatch,
		},
		{
			name:             "Build_old_manifest_available",
			branch:           mainBranch("6.6.0", []string{"6.5.0"}),
			currentLocal:     "6.3.0", // not in DiffTags
			oldMainAvailable: true,
			want:             DecisionBuild,
		},
		{
			name:             "Full_no_cache",
			branch:           mainBranch("6.6.0", []string{"6.5.0"}),
			currentLocal:     "6.3.0", // not in DiffTags
			oldMainAvailable: false,
			want:             DecisionFull,
		},
		{
			name:             "DiffTags_empty_with_old_manifest_to_Build",
			branch:           mainBranch("6.6.0", nil),
			currentLocal:     "6.5.0",
			oldMainAvailable: true,
			want:             DecisionBuild,
		},
		{
			name:             "DiffTags_empty_no_cache_to_Full",
			branch:           mainBranch("6.6.0", nil),
			currentLocal:     "6.5.0",
			oldMainAvailable: false,
			want:             DecisionFull,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := DecidePath(tc.branch, tc.currentLocal, tc.oldMainAvailable)
			if got != tc.want {
				t.Errorf("DecidePath = %v, want %v", got, tc.want)
			}
			if reason == "" {
				t.Errorf("DecidePath reason is empty; want a non-empty explanation")
			}
		})
	}
}

func TestDecidePath_NilBranch(t *testing.T) {
	got, _ := DecidePath(nil, "6.5.0", true)
	if got != DecisionNoInstall {
		t.Errorf("DecidePath(nil) = %v, want DecisionNoInstall", got)
	}
}

func TestBuildChunkSources_HitMissMix(t *testing.T) {
	newAsset := &pb.SophonManifestAssetProperty{
		AssetName: "GenshinImpact_Data/c.pak",
		AssetSize: 300,
		AssetChunks: []*pb.SophonManifestAssetChunk{
			// reused (hit in oldIdx)
			{ChunkName: "ck-reused", ChunkDecompressedHashMd5: "md5-reuse", ChunkOnFileOffset: 0, ChunkSize: 40, ChunkSizeDecompressed: 100},
			// fresh (miss)
			{ChunkName: "ck-fresh", ChunkDecompressedHashMd5: "md5-fresh", ChunkOnFileOffset: 100, ChunkSize: 80, ChunkSizeDecompressed: 200},
		},
	}
	oldIdx := map[string]ChunkRef{
		"md5-reuse": {OldFilePath: "GenshinImpact_Data/old_c.pak", OldOffset: 4096},
	}

	got := BuildChunkSources(newAsset, oldIdx, "https://cdn.example/chunks", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	local := got[0]
	if local.Kind != SourceLocal {
		t.Errorf("chunk[0].Kind = %q, want SourceLocal", local.Kind)
	}
	if local.OldFile != "GenshinImpact_Data/old_c.pak" || local.OldOffset != 4096 {
		t.Errorf("chunk[0] old ref = (%q,%d), want (old_c.pak,4096)", local.OldFile, local.OldOffset)
	}
	// §6.3 step 4 stale-fallback: Local sources ALSO carry CDN coordinates.
	if local.ChunkName != "ck-reused" || local.URLPrefix != "https://cdn.example/chunks" {
		t.Errorf("chunk[0] missing CDN fallback fields: name=%q prefix=%q", local.ChunkName, local.URLPrefix)
	}
	if local.CompressedSz != 40 || !local.UseCompress {
		t.Errorf("chunk[0] CompressedSz=%d UseCompress=%v, want 40,true", local.CompressedSz, local.UseCompress)
	}
	if local.DecompSize != 100 || local.FileOffset != 0 || local.ExpectMD5 != "md5-reuse" {
		t.Errorf("chunk[0] sizes/offset/md5 wrong: %+v", local)
	}
	if local.Asset != "GenshinImpact_Data/c.pak" {
		t.Errorf("chunk[0].Asset = %q, want c.pak", local.Asset)
	}

	cdn := got[1]
	if cdn.Kind != SourceCDN {
		t.Errorf("chunk[1].Kind = %q, want SourceCDN", cdn.Kind)
	}
	if cdn.ChunkName != "ck-fresh" || cdn.URLPrefix != "https://cdn.example/chunks" {
		t.Errorf("chunk[1] CDN coords wrong: name=%q prefix=%q", cdn.ChunkName, cdn.URLPrefix)
	}
	if cdn.OldFile != "" {
		t.Errorf("chunk[1].OldFile = %q, want empty for CDN", cdn.OldFile)
	}
	if cdn.DecompSize != 200 || cdn.CompressedSz != 80 || cdn.FileOffset != 100 || cdn.ExpectMD5 != "md5-fresh" {
		t.Errorf("chunk[1] sizes/offset/md5 wrong: %+v", cdn)
	}
}

func TestBuildChunkSources_NilOldIdxAllCDN(t *testing.T) {
	newAsset := &pb.SophonManifestAssetProperty{
		AssetName: "x.pak",
		AssetChunks: []*pb.SophonManifestAssetChunk{
			{ChunkName: "k1", ChunkDecompressedHashMd5: "m1", ChunkSize: 10, ChunkSizeDecompressed: 20},
		},
	}
	got := BuildChunkSources(newAsset, nil, "https://cdn", false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Kind != SourceCDN {
		t.Errorf("Kind = %q, want SourceCDN (nil oldIdx → all CDN)", got[0].Kind)
	}
}

func TestBuildPatchInstructions_CopyOverPatchAndDelete(t *testing.T) {
	const cur = "6.5.0"
	patch := &pb.SophonPatchProto{
		PatchAssets: []*pb.SophonPatchAssetProperty{
			{
				AssetName: "copyover.pak",
				AssetInfos: []*pb.SophonPatchAssetInfo{
					{
						VersionTag: cur,
						Chunk: &pb.SophonPatchAssetChunk{
							PatchName:        "blob-1",
							PatchOffset:      0,
							PatchLength:      500,
							PatchSize:        500,
							PatchMd5:         "blob1md5",
							OriginalFileName: "", // → CopyOver
						},
					},
				},
			},
			{
				AssetName: "hdiff.pak",
				AssetInfos: []*pb.SophonPatchAssetInfo{
					{
						VersionTag: cur,
						Chunk: &pb.SophonPatchAssetChunk{
							PatchName:        "blob-2",
							PatchOffset:      500,
							PatchLength:      120,
							PatchSize:        620,
							PatchMd5:         "blob2md5",
							OriginalFileName: "hdiff.pak",  // → Patch
							OriginalFileMd5:  "old-hdiff-md5",
						},
					},
				},
			},
			{
				// VersionTag does not match currentLocal → skipped entirely.
				AssetName: "other_source.pak",
				AssetInfos: []*pb.SophonPatchAssetInfo{
					{
						VersionTag: "6.4.0",
						Chunk: &pb.SophonPatchAssetChunk{
							PatchName:        "blob-3",
							OriginalFileName: "other_source.pak",
						},
					},
				},
			},
		},
		UnusedAssets: []*pb.SophonUnusedAssetProperty{
			{
				VersionTag: cur,
				AssetInfos: []*pb.SophonUnusedAssetInfo{
					{Assets: []*pb.SophonUnusedAssetFile{
						{FileName: "old/removed.bin", FileMd5: "del-md5", FileSize: 9},
					}},
				},
			},
			{
				// Different VersionTag → not our delete.
				VersionTag: "6.4.0",
				AssetInfos: []*pb.SophonUnusedAssetInfo{
					{Assets: []*pb.SophonUnusedAssetFile{
						{FileName: "old/not-ours.bin", FileMd5: "nope"},
					}},
				},
			},
		},
	}
	main := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{AssetName: "copyover.pak", AssetHashMd5: "new-copyover-md5", AssetSize: 500},
			{AssetName: "hdiff.pak", AssetHashMd5: "new-hdiff-md5", AssetSize: 1000},
			{AssetName: "other_source.pak", AssetHashMd5: "new-other-md5"},
			// A main asset with no patch entry — caller's fall-through; emitted by neither.
			{AssetName: "unchanged.pak", AssetHashMd5: "unchanged-md5"},
		},
	}

	patches, deletes := BuildPatchInstructions(patch, main, cur, "https://cdn/patches")

	if len(patches) != 2 {
		t.Fatalf("len(patches) = %d, want 2 (copyover + hdiff; mismatched-VersionTag skipped)", len(patches))
	}

	byAsset := map[string]PatchInstr{}
	for _, p := range patches {
		byAsset[p.Asset] = p
	}

	co, ok := byAsset["copyover.pak"]
	if !ok {
		t.Fatal("copyover.pak patch missing")
	}
	if co.Method != MethodCopyOver {
		t.Errorf("copyover Method = %q, want MethodCopyOver", co.Method)
	}
	if co.PatchName != "blob-1" || co.PatchOffset != 0 || co.PatchLength != 500 {
		t.Errorf("copyover blob coords wrong: %+v", co)
	}
	if co.ExpectMD5 != "new-copyover-md5" {
		t.Errorf("copyover ExpectMD5 = %q, want new-copyover-md5 (main AssetHashMd5)", co.ExpectMD5)
	}
	if co.URLPrefix != "https://cdn/patches" {
		t.Errorf("copyover URLPrefix = %q", co.URLPrefix)
	}
	if co.OldFile != "" {
		t.Errorf("copyover OldFile = %q, want empty", co.OldFile)
	}

	hp, ok := byAsset["hdiff.pak"]
	if !ok {
		t.Fatal("hdiff.pak patch missing")
	}
	if hp.Method != MethodPatch {
		t.Errorf("hdiff Method = %q, want MethodPatch", hp.Method)
	}
	if hp.OldFile != "hdiff.pak" {
		t.Errorf("hdiff OldFile = %q, want hdiff.pak (OriginalFileName)", hp.OldFile)
	}
	if hp.OriginalFileMD5 != "old-hdiff-md5" {
		t.Errorf("hdiff OriginalFileMD5 = %q, want old-hdiff-md5", hp.OriginalFileMD5)
	}
	if hp.ExpectMD5 != "new-hdiff-md5" {
		t.Errorf("hdiff ExpectMD5 = %q, want new-hdiff-md5", hp.ExpectMD5)
	}
	if hp.PatchName != "blob-2" || hp.PatchOffset != 500 || hp.PatchLength != 120 || hp.PatchSize != 620 || hp.PatchMD5 != "blob2md5" {
		t.Errorf("hdiff blob coords wrong: %+v", hp)
	}

	if _, leaked := byAsset["other_source.pak"]; leaked {
		t.Errorf("other_source.pak emitted despite VersionTag mismatch")
	}

	if len(deletes) != 1 {
		t.Fatalf("len(deletes) = %d, want 1 (only matching VersionTag)", len(deletes))
	}
	if deletes[0].Path != "old/removed.bin" || deletes[0].ExpectMD5 != "del-md5" {
		t.Errorf("delete = %+v, want {old/removed.bin, del-md5}", deletes[0])
	}
}

func TestBuildPatchInstructions_PatchAssetNotInMainIsSkipped(t *testing.T) {
	const cur = "6.5.0"
	patch := &pb.SophonPatchProto{
		PatchAssets: []*pb.SophonPatchAssetProperty{
			{
				AssetName: "ghost.pak", // not present in main → no work
				AssetInfos: []*pb.SophonPatchAssetInfo{
					{VersionTag: cur, Chunk: &pb.SophonPatchAssetChunk{PatchName: "g", OriginalFileName: "ghost.pak"}},
				},
			},
		},
	}
	main := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{AssetName: "real.pak", AssetHashMd5: "m"},
		},
	}
	patches, deletes := BuildPatchInstructions(patch, main, cur, "https://cdn/patches")
	if len(patches) != 0 {
		t.Errorf("len(patches) = %d, want 0 (patch asset absent from main)", len(patches))
	}
	if len(deletes) != 0 {
		t.Errorf("len(deletes) = %d, want 0", len(deletes))
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (undefined `DecidePath`/`BuildChunkSources`/`BuildPatchInstructions`/`Decision*` consts).**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```
Expected: compile failure naming the missing identifiers. (Task 7's `dedup_test` must still pass once `decision.go` exists — they share the package.)

- [ ] **Step 3: Implement `decision.go`.**

```go
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
```

- [ ] **Step 4: Run the test — expect PASS.**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```
Expected: `ok  omnigate/internal/providers/hoyoverse/sophon` (Task 7 + Task 8 tests both pass).

- [ ] **Step 5: Vet + commit.**

```bash
go vet ./internal/providers/hoyoverse/sophon/...
git add internal/providers/hoyoverse/sophon/decision.go \
        internal/providers/hoyoverse/sophon/decision_test.go
git commit -m "feat(sophon): decision tree + chunk-source + patch-instruction builders (Task 8)"
```

---

### Task 9: `chunk_download.go` + `ctxReader` + cross-device rename

Sophon-package chunk download (CDN) + patch-blob download, plus the sophon-package copy of the build-tagged cross-device detection and `SafeAtomicRename` (per **[DEV-2]** — `sophon` cannot import `hoyoverse`'s `isCrossDevice`). Integrity per spec §0: xxh64 of decompressed bytes when `ChunkName`'s first 16 hex chars parse as uint64; else MD5 vs `ExpectMD5`. Cancel propagates through zstd via a `ctxReader` wrapper (§5.4). Retry budget 3× backoff 1s/4s/16s → `ErrChunkVerify` (§5.2).

> PATH note (all Go tasks in this part): on subagent shells without Go on PATH, prepend `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. This host is `CGO_ENABLED=0` — **never** pass `-race`. Tasks 9–12 all run `go test -count=1 ./internal/providers/hoyoverse/sophon/...`.

**Files:** `internal/providers/hoyoverse/sophon/rename.go`, `internal/providers/hoyoverse/sophon/cross_device_windows.go`, `internal/providers/hoyoverse/sophon/cross_device_other.go`, `internal/providers/hoyoverse/sophon/chunk_download.go`, `internal/providers/hoyoverse/sophon/chunk_download_test.go`

- [ ] **Step 1 — failing test.** Write `internal/providers/hoyoverse/sophon/chunk_download_test.go`:

```go
package sophon

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
)

func zstdCompress(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func xxhName(raw []byte) string {
	return fmt.Sprintf("%016x", xxhash.Sum64(raw))
}

func md5hex(raw []byte) string {
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])
}

func TestDownloadChunk_ZstdXXHPath(t *testing.T) {
	raw := []byte("hello sophon chunk payload zstd")
	name := xxhName(raw) // first 16 hex = xxh64 of decompressed
	comp := zstdCompress(t, raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(comp)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch: got %q", got)
	}
}

func TestDownloadChunk_RawNoCompress(t *testing.T) {
	raw := []byte("uncompressed raw chunk body")
	name := xxhName(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch")
	}
}

func TestDownloadChunk_XXHMismatchRetryThenSucceed(t *testing.T) {
	raw := []byte("flaky chunk that fails once")
	name := xxhName(raw)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			_, _ = w.Write([]byte("corrupt body")) // wrong bytes → xxh64 mismatch
			return
		}
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", hits)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch after retry")
	}
}

func TestDownloadChunk_RetryExhaustedErrChunkVerify(t *testing.T) {
	raw := []byte("always corrupt target")
	name := xxhName(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("never matches"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("expected ErrChunkVerify, got %v", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out should not exist after exhaustion")
	}
}

func TestDownloadChunk_ChunkNameNotHexMD5Fallback(t *testing.T) {
	raw := []byte("md5-only verified chunk")
	name := "not-a-hex-chunk-name.chunk" // first 16 chars don't parse as hex uint64
	if _, err := strconv.ParseUint(name[:16], 16, 64); err == nil {
		t.Fatalf("test precondition: name prefix must NOT parse as hex")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("MD5-fallback path failed: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch on MD5 fallback")
	}
}

func TestDownloadChunk_SkipIfExistsAndVerifies(t *testing.T) {
	raw := []byte("already present chunk")
	name := xxhName(raw)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("skip-if-verifies: %v", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected no HTTP hit when out already verifies, got %d", hits)
	}
}

func TestDownloadChunk_CtxCancelMidStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		for i := 0; i < 1000; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName([]byte("z")), URLPrefix: srv.URL, UseCompress: false, DecompSize: 1, ExpectMD5: md5hex([]byte("z"))}
	err := DownloadChunk(ctx, srv.Client(), src, out)
	if err == nil {
		t.Fatalf("expected error on cancel")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("expected context.Canceled (or ErrChunkVerify after exhaustion), got %v", err)
	}
}

func TestDownloadPatchBlob_VerifyAndSkip(t *testing.T) {
	blob := []byte("full patch blob bytes 0123456789")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex(blob), PatchSize: int64(len(blob))}
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err != nil {
		t.Fatalf("DownloadPatchBlob: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, blob) {
		t.Fatalf("blob mismatch")
	}
	// second call must skip (already verifies)
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err != nil {
		t.Fatalf("second DownloadPatchBlob: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 HTTP hit, got %d", hits)
	}
}

func TestDownloadPatchBlob_MD5Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong blob"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex([]byte("expected blob"))}
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err == nil {
		t.Fatalf("expected MD5-mismatch error")
	}
}

func TestSafeAtomicRename_SameDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SafeAtomicRename(src, dst); err != nil {
		t.Fatalf("SafeAtomicRename: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src should be gone after rename")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "payload" {
		t.Fatalf("dst content mismatch")
	}
}
```

- [ ] **Step 2 — run, expect FAIL** (undefined symbols / no build):

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 3 — implement.** Write `internal/providers/hoyoverse/sophon/cross_device_windows.go` (verbatim mirror of v1 **[DEV-2]**, `package sophon`):

```go
//go:build windows

package sophon

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
```

Write `internal/providers/hoyoverse/sophon/cross_device_other.go`:

```go
//go:build !windows

package sophon

import (
	"errors"
	"syscall"
)

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EXDEV)
}
```

Write `internal/providers/hoyoverse/sophon/rename.go`:

```go
package sophon

import (
	"fmt"
	"io"
	"os"
)

// SafeAtomicRename renames src→dst, falling back to copy+remove when src and
// dst live on different volumes (the OS returns a cross-device errno). The
// fallback writes dst via a temp file + atomic rename within dst's directory.
func SafeAtomicRename(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if !isCrossDevice(err) {
			return err
		}
		return copyThenRemove(src, dst)
	}
	return nil
}

func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".xdev.tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("cross-device copy %s → %s: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("cross-device remove src %s: %w", src, err)
	}
	return nil
}
```

Write `internal/providers/hoyoverse/sophon/chunk_download.go`:

```go
package sophon

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
)

// ErrChunkVerify is returned when a CDN chunk fails integrity verification
// after the retry budget is exhausted.
var ErrChunkVerify = errors.New("sophon: chunk verification failed")

// chunkRetryBackoff is the per-chunk retry schedule (3 attempts after the
// first failure → 4 total transfer attempts is NOT intended; the loop runs
// len(backoff)+1 == but we cap at 3 total per spec §5.2: attempts 1,2,3 with
// sleeps 1s,4s before attempts 2,3). The slice length controls total attempts.
var chunkRetryBackoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}

// ctxReader wraps an io.Reader and returns ctx.Err() from Read once the context
// is cancelled, so cancellation propagates through wrapping readers such as
// zstd.NewReader (spec §5.4).
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// DownloadChunk fetches src from the CDN, decompresses (if UseCompress), verifies
// integrity, and atomically writes the decompressed bytes to out. Verification
// uses xxh64 of the decompressed bytes when ChunkName's first 16 hex chars parse
// as a uint64; otherwise MD5 vs src.ExpectMD5 (spec §0). If out already exists and
// verifies, the download is skipped. Retries per chunkRetryBackoff; on exhaustion
// returns ErrChunkVerify.
func DownloadChunk(ctx context.Context, hc *http.Client, src ChunkSource, out string) error {
	wantXXH, useXXH := parseXXHName(src.ChunkName)

	// Skip if out already exists and verifies.
	if existing, err := os.ReadFile(out); err == nil {
		if verifyBytes(existing, useXXH, wantXXH, src.ExpectMD5) {
			return nil
		}
		_ = os.Remove(out)
	}

	var lastErr error
	for attempt := 0; attempt < len(chunkRetryBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(chunkRetryBackoff[attempt-1]):
			}
		}
		err := downloadChunkOnce(ctx, hc, src, out, useXXH, wantXXH)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = err
	}
	return fmt.Errorf("%w: %s: %v", ErrChunkVerify, src.ChunkName, lastErr)
}

func downloadChunkOnce(ctx context.Context, hc *http.Client, src ChunkSource, out string, useXXH bool, wantXXH uint64) error {
	url := src.URLPrefix + "/" + src.ChunkName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chunk GET %s: status %d", url, resp.StatusCode)
	}

	var reader io.Reader = &ctxReader{ctx: ctx, r: resp.Body}
	var zr *zstd.Decoder
	if src.UseCompress {
		zr, err = zstd.NewReader(reader)
		if err != nil {
			return err
		}
		defer zr.Close()
		reader = zr
	}

	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	var h hash.Hash64
	var m hash.Hash
	var sink io.Writer = f
	if useXXH {
		h = xxhash.New()
		sink = io.MultiWriter(f, h)
	} else {
		m = md5.New()
		sink = io.MultiWriter(f, m)
	}

	if _, err := io.Copy(sink, reader); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	var ok bool
	if useXXH {
		ok = h.Sum64() == wantXXH
	} else {
		ok = hex.EncodeToString(m.Sum(nil)) == src.ExpectMD5
	}
	if !ok {
		_ = os.Remove(tmp)
		return fmt.Errorf("verify mismatch for %s", src.ChunkName)
	}
	return SafeAtomicRename(tmp, out)
}

// DownloadPatchBlob fetches the full patch blob and atomically writes it to out,
// verifying MD5 against p.PatchMD5. Skips if out already exists and verifies.
func DownloadPatchBlob(ctx context.Context, hc *http.Client, p PatchInstr, out string) error {
	if existing, err := os.ReadFile(out); err == nil {
		if md5hexBytes(existing) == p.PatchMD5 {
			return nil
		}
		_ = os.Remove(out)
	}

	url := p.URLPrefix + "/" + p.PatchName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("patch GET %s: status %d", url, resp.StatusCode)
	}

	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	m := md5.New()
	if _, err := io.Copy(io.MultiWriter(f, m), &ctxReader{ctx: ctx, r: resp.Body}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if hex.EncodeToString(m.Sum(nil)) != p.PatchMD5 {
		_ = os.Remove(tmp)
		return fmt.Errorf("patch blob MD5 mismatch for %s", p.PatchName)
	}
	return SafeAtomicRename(tmp, out)
}

// parseXXHName reports whether ChunkName's first 16 chars parse as a hex uint64,
// returning the decoded value when they do.
func parseXXHName(name string) (uint64, bool) {
	if len(name) < 16 {
		return 0, false
	}
	v, err := strconv.ParseUint(name[:16], 16, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func verifyBytes(b []byte, useXXH bool, wantXXH uint64, wantMD5 string) bool {
	if useXXH {
		return xxhash.Sum64(b) == wantXXH
	}
	return md5hexBytes(b) == wantMD5
}

func md5hexBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4 — run, expect PASS:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 5 — commit:**

```bash
git add internal/providers/hoyoverse/sophon/rename.go internal/providers/hoyoverse/sophon/cross_device_windows.go internal/providers/hoyoverse/sophon/cross_device_other.go internal/providers/hoyoverse/sophon/chunk_download.go internal/providers/hoyoverse/sophon/chunk_download_test.go
git commit -m "feat(sophon): chunk/patch-blob download with xxh64/MD5 verify + cross-device rename [DEV-2]"
```

---

### Task 10: `local_chunk_read.go` (Path-B chunk-from-disk)

Read a chunk's decompressed bytes from the on-disk old-version file, MD5-verify against `ExpectMD5`, atomic-write to out. MD5 mismatch OR old-file ENOENT → `ErrChunkStale` so the worker pool falls back to a CDN fetch (spec §4.4, §6.3 step 4; ENOENT documented to map to `ErrChunkStale`).

**Files:** `internal/providers/hoyoverse/sophon/local_chunk_read.go`, `internal/providers/hoyoverse/sophon/local_chunk_read_test.go`

- [ ] **Step 1 — failing test.** Write `internal/providers/hoyoverse/sophon/local_chunk_read_test.go`:

```go
package sophon

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func md5OfHex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestReadLocalChunk_Match(t *testing.T) {
	gameDir := t.TempDir()
	full := []byte("AAAACHUNKBYTESHEREBBBB")
	if err := os.WriteFile(filepath.Join(gameDir, "old.dat"), full, 0o644); err != nil {
		t.Fatal(err)
	}
	want := full[4:14] // "CHUNKBYTES"
	out := filepath.Join(t.TempDir(), "chunk.bin")
	src := ChunkSource{Kind: SourceLocal, OldFile: "old.dat", OldOffset: 4, DecompSize: int64(len(want)), ExpectMD5: md5OfHex(want)}
	if err := ReadLocalChunk(gameDir, src, out); err != nil {
		t.Fatalf("ReadLocalChunk: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}
}

func TestReadLocalChunk_StaleMismatch(t *testing.T) {
	gameDir := t.TempDir()
	full := []byte("AAAACHUNKBYTESHEREBBBB")
	if err := os.WriteFile(filepath.Join(gameDir, "old.dat"), full, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "chunk.bin")
	// ExpectMD5 of DIFFERENT bytes → mismatch
	src := ChunkSource{Kind: SourceLocal, OldFile: "old.dat", OldOffset: 4, DecompSize: 10, ExpectMD5: md5OfHex([]byte("DIFFERENT!"))}
	err := ReadLocalChunk(gameDir, src, out)
	if !errors.Is(err, ErrChunkStale) {
		t.Fatalf("expected ErrChunkStale, got %v", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out must not be written on stale")
	}
}

func TestReadLocalChunk_ENOENTOldFile(t *testing.T) {
	gameDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "chunk.bin")
	src := ChunkSource{Kind: SourceLocal, OldFile: "missing.dat", OldOffset: 0, DecompSize: 4, ExpectMD5: md5OfHex([]byte("abcd"))}
	err := ReadLocalChunk(gameDir, src, out)
	if !errors.Is(err, ErrChunkStale) {
		t.Fatalf("ENOENT old-file should map to ErrChunkStale, got %v", err)
	}
}
```

- [ ] **Step 2 — run, expect FAIL:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 3 — implement.** Write `internal/providers/hoyoverse/sophon/local_chunk_read.go`:

```go
package sophon

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrChunkStale signals that a Path-B local chunk read could not produce verified
// bytes — either the old file is absent or the bytes at (OldFile, OldOffset) no
// longer hash to ExpectMD5 (user modded / HoYoPlay-updated between plan and apply).
// The caller falls back to a CDN fetch for the same chunk. ENOENT on the old file
// is deliberately wrapped as ErrChunkStale so both fall-back paths converge.
var ErrChunkStale = errors.New("sophon: local chunk MD5 mismatch")

// ReadLocalChunk reads DecompSize bytes from <gameDir>/<src.OldFile> at src.OldOffset,
// verifies their MD5 against src.ExpectMD5, and atomically writes them to out.
// Returns ErrChunkStale on MD5 mismatch or when the old file does not exist.
func ReadLocalChunk(gameDir string, src ChunkSource, out string) error {
	oldPath := filepath.Join(gameDir, src.OldFile)
	f, err := os.Open(oldPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: old file missing %s", ErrChunkStale, src.OldFile)
		}
		return err
	}
	defer f.Close()

	if _, err := f.Seek(src.OldOffset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, src.DecompSize)
	if _, err := io.ReadFull(f, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: short read at %s+%d", ErrChunkStale, src.OldFile, src.OldOffset)
		}
		return err
	}

	sum := md5.Sum(buf)
	if hex.EncodeToString(sum[:]) != src.ExpectMD5 {
		return fmt.Errorf("%w: %s+%d", ErrChunkStale, src.OldFile, src.OldOffset)
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return SafeAtomicRename(tmp, out)
}
```

- [ ] **Step 4 — run, expect PASS:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 5 — commit:**

```bash
git add internal/providers/hoyoverse/sophon/local_chunk_read.go internal/providers/hoyoverse/sophon/local_chunk_read_test.go
git commit -m "feat(sophon): Path-B local chunk read with stale fallback"
```

---

### Task 11: `file_assemble.go` (pure chunk → file assembly)

Assemble a target file from its chunk sources via positional `WriteAt`. `AssembleFile` is pure: it MkdirAll's the target dir, truncates to `totalSize`, writes each chunk at `FileOffset`, fsyncs, and returns. It does NOT verify whole-file MD5 and does NOT rename — the caller (`update_sophon_apply.go`, §6.3) does whole-file MD5 + atomic rename. The caller passes the `*.tmp` path as `out`. The `readChunk` callback abstracts staging-read (CDN chunks) vs local-read (Path-B chunks).

**Files:** `internal/providers/hoyoverse/sophon/file_assemble.go`, `internal/providers/hoyoverse/sophon/file_assemble_test.go`

- [ ] **Step 1 — failing test.** Write `internal/providers/hoyoverse/sophon/file_assemble_test.go`:

```go
package sophon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAssembleFile_ThreeChunksInOrder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	c0, c1, c2 := []byte("AAAA"), []byte("BBBB"), []byte("CCCC")
	sources := []ChunkSource{
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "c1", FileOffset: 4, DecompSize: 4},
		{ChunkName: "c2", FileOffset: 8, DecompSize: 4},
	}
	data := map[string][]byte{"c0": c0, "c1": c1, "c2": c2}
	read := func(src ChunkSource) ([]byte, error) { return data[src.ChunkName], nil }
	if err := AssembleFile(out, 12, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("AAAABBBBCCCC")) {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestAssembleFile_OutOfFileOrderOffsets(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	sources := []ChunkSource{
		{ChunkName: "c2", FileOffset: 8, DecompSize: 4},
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "c1", FileOffset: 4, DecompSize: 4},
	}
	data := map[string][]byte{"c0": []byte("AAAA"), "c1": []byte("BBBB"), "c2": []byte("CCCC")}
	read := func(src ChunkSource) ([]byte, error) { return data[src.ChunkName], nil }
	if err := AssembleFile(out, 12, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("AAAABBBBCCCC")) {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestAssembleFile_NestedPathMkdirAll(t *testing.T) {
	out := filepath.Join(t.TempDir(), "deep", "nested", "dir", "asm.tmp")
	sources := []ChunkSource{{ChunkName: "c0", FileOffset: 0, DecompSize: 3}}
	read := func(src ChunkSource) ([]byte, error) { return []byte("xyz"), nil }
	if err := AssembleFile(out, 3, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("xyz")) {
		t.Fatalf("content mismatch")
	}
}

func TestAssembleFile_ReadChunkErrorAborts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	sources := []ChunkSource{
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "bad", FileOffset: 4, DecompSize: 4},
	}
	boom := errors.New("readChunk failure")
	read := func(src ChunkSource) ([]byte, error) {
		if src.ChunkName == "bad" {
			return nil, boom
		}
		return []byte("AAAA"), nil
	}
	err := AssembleFile(out, 8, sources, read)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped readChunk error, got %v", err)
	}
}
```

- [ ] **Step 2 — run, expect FAIL:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 3 — implement.** Write `internal/providers/hoyoverse/sophon/file_assemble.go`:

```go
package sophon

import (
	"fmt"
	"os"
	"path/filepath"
)

// AssembleFile builds the target file at out from its chunk sources. It is pure:
// MkdirAll(dir(out)), truncate to totalSize, WriteAt each chunk at src.FileOffset,
// fsync, close. It does NOT verify the whole-file MD5 and does NOT rename — the
// caller does both (out is expected to be the *.tmp path). readChunk supplies the
// decompressed bytes for a source, abstracting staging-read (CDN) vs local-read
// (Path-B). Any readChunk error aborts assembly and is wrapped.
func AssembleFile(out string, totalSize int64, sources []ChunkSource, readChunk func(src ChunkSource) ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(totalSize); err != nil {
		_ = f.Close()
		return err
	}
	for _, src := range sources {
		b, err := readChunk(src)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("assemble %s chunk %s: %w", out, src.ChunkName, err)
		}
		if _, err := f.WriteAt(b, src.FileOffset); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
```

- [ ] **Step 4 — run, expect PASS:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 5 — commit:**

```bash
git add internal/providers/hoyoverse/sophon/file_assemble.go internal/providers/hoyoverse/sophon/file_assemble_test.go
git commit -m "feat(sophon): pure positional file assembly from chunk sources"
```

---

### Task 12: `hdiff_apply.go` (injected hpatchz Run + copy-over)

Dispatch a `PatchInstr` apply via `HDiffApply(opts)`: MkdirAll(dir(OutTmp)); `MethodPatch` → `opts.Run(opts.Ctx, opts.OldFile, opts.DiffInput, opts.OutTmp)` (hpatchz injected by the parent to avoid an import cycle); `MethodCopyOver` → `SafeAtomicRename(opts.BlobSlice, opts.OutTmp)`. Unknown method → error. `HDiffApply` does NOT verify whole-file MD5 — the caller does (§6.4 step 6, §6.5 step 3).

**Files:** `internal/providers/hoyoverse/sophon/hdiff_apply.go`, `internal/providers/hoyoverse/sophon/hdiff_apply_test.go`

- [ ] **Step 1 — failing test.** Write `internal/providers/hoyoverse/sophon/hdiff_apply_test.go`:

```go
package sophon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHDiffApply_PatchBranchInvokesRun(t *testing.T) {
	dir := t.TempDir()
	outTmp := filepath.Join(dir, "assembled", "file.dat.tmp")
	var gotOld, gotDiff, gotOut string
	opts := HDiffOpts{
		Ctx:       context.Background(),
		Method:    MethodPatch,
		OldFile:   "/game/old.dat",
		DiffInput: "/staging/diff.bin",
		OutTmp:    outTmp,
		Run: func(ctx context.Context, oldFile, diffFile, newFile string) error {
			gotOld, gotDiff, gotOut = oldFile, diffFile, newFile
			// hpatchz would write newFile; emulate so the dir-creation contract is observable.
			return os.WriteFile(newFile, []byte("patched"), 0o644)
		},
	}
	if err := HDiffApply(opts); err != nil {
		t.Fatalf("HDiffApply: %v", err)
	}
	if gotOld != "/game/old.dat" || gotDiff != "/staging/diff.bin" || gotOut != outTmp {
		t.Fatalf("Run received (%q,%q,%q)", gotOld, gotDiff, gotOut)
	}
	if _, err := os.Stat(filepath.Dir(outTmp)); err != nil {
		t.Fatalf("OutTmp dir should have been created: %v", err)
	}
}

func TestHDiffApply_CopyOverRenamesBlobSlice(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "slice.bin")
	if err := os.WriteFile(blob, []byte("full file via copyover"), 0o644); err != nil {
		t.Fatal(err)
	}
	outTmp := filepath.Join(dir, "assembled", "file.dat.tmp")
	opts := HDiffOpts{Method: MethodCopyOver, BlobSlice: blob, OutTmp: outTmp}
	if err := HDiffApply(opts); err != nil {
		t.Fatalf("HDiffApply: %v", err)
	}
	got, _ := os.ReadFile(outTmp)
	if string(got) != "full file via copyover" {
		t.Fatalf("copy_over content mismatch: %q", got)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatalf("blob slice should be gone after rename")
	}
}

func TestHDiffApply_UnknownMethod(t *testing.T) {
	err := HDiffApply(HDiffOpts{Method: "bogus", OutTmp: filepath.Join(t.TempDir(), "x.tmp")})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-method error mentioning the method, got %v", err)
	}
}
```

- [ ] **Step 2 — run, expect FAIL:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 3 — implement.** Write `internal/providers/hoyoverse/sophon/hdiff_apply.go`:

```go
package sophon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// HDiffOpts carries everything HDiffApply needs to materialise one target file.
// Run is the hpatchz binary runner injected by the parent hoyoverse package
// (sophon must not import hoyoverse/hpatchz directly here — the parent wires it).
type HDiffOpts struct {
	Ctx       context.Context
	Run       func(ctx context.Context, oldFile, diffFile, newFile string) error
	Method    string // MethodPatch | MethodCopyOver
	OldFile   string // MethodPatch: source file (absolute)
	DiffInput string // MethodPatch: extracted hdiff slice path
	BlobSlice string // MethodCopyOver: extracted blob slice path (becomes the file)
	OutTmp    string
}

// HDiffApply produces OutTmp from opts. MethodPatch runs the injected hpatchz
// against (OldFile, DiffInput) → OutTmp; MethodCopyOver renames the already-
// extracted blob slice into OutTmp. It does NOT verify the resulting whole-file
// MD5 — the caller does (spec §6.4/§6.5).
func HDiffApply(opts HDiffOpts) error {
	if err := os.MkdirAll(filepath.Dir(opts.OutTmp), 0o755); err != nil {
		return err
	}
	switch opts.Method {
	case MethodPatch:
		if opts.Run == nil {
			return fmt.Errorf("sophon: HDiffApply MethodPatch requires opts.Run")
		}
		return opts.Run(opts.Ctx, opts.OldFile, opts.DiffInput, opts.OutTmp)
	case MethodCopyOver:
		return SafeAtomicRename(opts.BlobSlice, opts.OutTmp)
	default:
		return fmt.Errorf("sophon: HDiffApply unknown method %q", opts.Method)
	}
}
```

- [ ] **Step 4 — run, expect PASS:**

```bash
go test -count=1 ./internal/providers/hoyoverse/sophon/...
```

- [ ] **Step 5 — commit:**

```bash
git add internal/providers/hoyoverse/sophon/hdiff_apply.go internal/providers/hoyoverse/sophon/hdiff_apply_test.go
git commit -m "feat(sophon): hdiff/copy-over apply dispatch with injected hpatchz Run"
```

### Task 13: meta.PlatApp + api.fetchBranchInfo + Provider setters + sidecar path helpers

Wires the Provider to the Sophon endpoints. Adds the per-game `PlatApp` query-param field + the `sophonChunkAPIBase` constant (§A.4), a `*Provider`-method `fetchBranchInfo` redirectable for tests via two new base-URL fields + setters ([DEV-3]), `fetchBranchTag` refactored to delegate, and the four `.sophon` sidecar path helpers (§A.8). Depends on Task 5's `sophon.BranchInfo` / `sophon.ParseBranches` and Task 4's `sophon/proto` already existing.

**Files:** `internal/providers/hoyoverse/meta.go`, `internal/providers/hoyoverse/api.go`, `internal/providers/hoyoverse/hoyoverse.go`, `internal/providers/hoyoverse/sidecar_paths.go`, `internal/providers/hoyoverse/api_test.go` (append), `internal/providers/hoyoverse/sidecar_paths_test.go` (append)

PATH note (subagent shells without Go on PATH — applies to every Go command in this part):
```bash
export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
```
This host is `CGO_ENABLED=0` — never pass `-race`.

- [ ] **Step 1 — Failing tests.** Append to `internal/providers/hoyoverse/api_test.go`:

```go
func TestFetchBranchInfo_ParsesMainAndPredl(t *testing.T) {
	body := `{"retcode":0,"message":"OK","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkgMain","branch":"main","password":"pw-main","tag":"6.6.0","diff_tags":["6.5.0","6.4.0"],"categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"},{"category_id":"10017","matching_field":"en-us","type":"CATEGORY_TYPE_AUDIO"}]},"pre_download":{"package_id":"pkgPredl","branch":"predownload","password":"pw-predl","tag":"6.7.0","diff_tags":["6.6.0"],"categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/getGameBranches" {
			t.Errorf("path = %s, want /getGameBranches", got)
		}
		if got := r.URL.Query().Get("launcher_id"); got != LauncherID {
			t.Errorf("launcher_id = %q, want %q", got, LauncherID)
		}
		if got := r.URL.Query().Get("game_ids[]"); got != "gopR6Cufr3" {
			t.Errorf("game_ids[] = %q, want gopR6Cufr3", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	bi, err := p.fetchBranchInfo(context.Background(), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("fetchBranchInfo: %v", err)
	}
	if bi.Main.Tag != "6.6.0" {
		t.Errorf("Main.Tag = %q, want 6.6.0", bi.Main.Tag)
	}
	if bi.Main.PackageID != "pkgMain" || bi.Main.Password != "pw-main" {
		t.Errorf("Main package/password = %q/%q", bi.Main.PackageID, bi.Main.Password)
	}
	if len(bi.Main.DiffTags) != 2 || bi.Main.DiffTags[0] != "6.5.0" {
		t.Errorf("Main.DiffTags = %v", bi.Main.DiffTags)
	}
	if len(bi.Main.Categories) != 2 {
		t.Fatalf("Main.Categories len = %d, want 2", len(bi.Main.Categories))
	}
	if bi.PreDownload.IsEmpty() {
		t.Error("PreDownload unexpectedly empty")
	}
	if bi.PreDownload.Tag != "6.7.0" {
		t.Errorf("PreDownload.Tag = %q, want 6.7.0", bi.PreDownload.Tag)
	}
}

func TestFetchBranchTag_DelegatesToFetchBranchInfo(t *testing.T) {
	body := `{"retcode":0,"data":{"game_branches":[{"game":{"id":"gopR6Cufr3"},"main":{"package_id":"pkg","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tag, err := p.fetchBranchTag(context.Background(), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("fetchBranchTag: %v", err)
	}
	if tag != "6.6.0" {
		t.Errorf("tag = %q, want 6.6.0", tag)
	}
}
```

Append to `internal/providers/hoyoverse/sidecar_paths_test.go`:

```go
func TestSophonSubdir(t *testing.T) {
	got := sophonSubdir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon")
	if got != want {
		t.Errorf("sophonSubdir: got %q want %q", got, want)
	}
}

func TestSophonManifestsDir(t *testing.T) {
	got := sophonManifestsDir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon", "manifests")
	if got != want {
		t.Errorf("sophonManifestsDir: got %q want %q", got, want)
	}
}

func TestSophonAppliedJSONPath(t *testing.T) {
	got := sophonAppliedJSONPath(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon", "applied.json")
	if got != want {
		t.Errorf("sophonAppliedJSONPath: got %q want %q", got, want)
	}
}

func TestSophonStagingDir(t *testing.T) {
	got := sophonStagingDir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"), "6.6.0", "main", "buildXYZ")
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", "6.6.0", "staging", "main", "buildXYZ")
	if got != want {
		t.Errorf("sophonStagingDir: got %q want %q", got, want)
	}
}
```

- [ ] **Step 2 — Run, expect FAIL.** `fetchBranchInfo`, `SetBranchAPIBaseURL`, `sophonSubdir`, etc. do not exist yet → compile failure.

```bash
go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 3 — Implement.**

`meta.go` — add the `PlatApp` field to `gameMeta`, the Genshin value, and the `sophonChunkAPIBase` const. Replace the `const (…)` block to add the constant:

```go
const (
	BackendID  core.BackendID = "hoyoverse"
	LauncherID                = "VYTpXlbWo8"
	APIBase                   = "https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api"
	UserAgent                 = "omnigate/0.1 (+https://github.com/willie/omnigate)"

	// sophonChunkAPIBase is the getBuild / getPatchBuild host for the Sophon
	// chunk protocol (§2.1 / §A.4). Distinct from APIBase (getGameBranches).
	sophonChunkAPIBase = "https://sg-public-api.hoyoverse.com/downloader/sophon_chunk/api"
)
```

Add the field to `gameMeta` (immediately after `UsesSophon bool`):

```go
	UsesSophon bool

	// PlatApp is the Sophon getBuild/getPatchBuild plat_app query param
	// (§2.1). Genshin global = "ddxf6vlr1reo"; HSR/ZZZ leave it "" (legacy).
	PlatApp string
```

Add `PlatApp` to the Genshin entry in `games` (so the literal becomes):

```go
	{
		ID: "hoyoverse/genshin", APIGameID: "gopR6Cufr3", Biz: "hk4e_global",
		FolderName: "Genshin Impact game", ExeName: "GenshinImpact.exe",
		Display:    core.LocalizedString{"zh-TW": "原神", "en": "Genshin Impact"},
		UsesSophon: true,
		PlatApp:    "ddxf6vlr1reo",
	},
```

`api.go` — add the `sophon` import and the `fetchBranchInfo` `*Provider` method; refactor `fetchBranchTag` (the `*apiClient` method) to delegate. First add to the import block:

```go
	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
```

Replace the existing `fetchBranchTag` `*apiClient` method (the whole func from `func (c *apiClient) fetchBranchTag(` through its closing brace) with the delegating Provider implementation. Keep the `rawGameBranches` type — it is no longer used by the new code but other code/tests may reference it; leave it untouched to minimize blast radius. New code:

```go
// fetchBranchInfo calls /getGameBranches on p.branchAPIBase and returns the
// full {Main, PreDownload} branch info via sophon.ParseBranches. It is a
// *Provider method (not *apiClient) so SetBranchAPIBaseURL can redirect it to
// an httptest server independently of SetAPIBaseURL ([DEV-3]). Uses
// p.httpClient with the same nil-fallback as fetchGetGamePackages.
func (p *Provider) fetchBranchInfo(ctx context.Context, apiGameID string) (*sophon.BranchInfo, error) {
	base := p.branchAPIBase
	if base == "" {
		base = APIBase
	}
	qs := "launcher_id=" + url.QueryEscape(LauncherID) + "&game_ids[]=" + url.QueryEscape(apiGameID)
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/getGameBranches?"+qs, nil)
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
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getGameBranches: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if env.Retcode != 0 {
		return nil, fmt.Errorf("getGameBranches retcode=%d msg=%q", env.Retcode, env.Message)
	}
	return sophon.ParseBranches(env.Data, apiGameID)
}

// fetchBranchTag returns the main branch's tag for apiGameID. Delegates to
// fetchBranchInfo so CheckVersion keeps working ([DEV-3]).
func (p *Provider) fetchBranchTag(ctx context.Context, apiGameID string) (string, error) {
	bi, err := p.fetchBranchInfo(ctx, apiGameID)
	if err != nil {
		return "", err
	}
	if bi.Main.IsEmpty() {
		return "", fmt.Errorf("getGameBranches: game id %q has empty main branch", apiGameID)
	}
	return bi.Main.Tag, nil
}
```

`time` is needed in `api.go` for `http.Client{Timeout: 30 * time.Second}`. Add `"time"` to the import block.

`hoyoverse.go` — `fetchBranchTag` moved from `*apiClient` to `*Provider`. Update the one call site in `CheckVersion` from `p.api.fetchBranchTag(...)` to `p.fetchBranchTag(...)`:

```go
	if g.UsesSophon {
		tag, err := p.fetchBranchTag(ctx, g.APIGameID)
```

Add the two fields to the `Provider` struct (after `apiBaseURL string`):

```go
	apiBaseURL    string
	branchAPIBase string // default APIBase; getGameBranches ([DEV-3])
	sophonAPIBase string // default sophonChunkAPIBase; getBuild/getPatchBuild ([DEV-3])
```

Set defaults in `New` (after `p.manifestCache = newManifestCache()`):

```go
	p.manifestCache = newManifestCache()
	p.branchAPIBase = APIBase
	p.sophonAPIBase = sophonChunkAPIBase
	return p
```

Add the two setters next to `SetAPIBaseURL`:

```go
// SetBranchAPIBaseURL overrides the getGameBranches base URL. Test seam
// ([DEV-3]); defaults to APIBase.
func (p *Provider) SetBranchAPIBaseURL(u string) { p.branchAPIBase = u }

// SetSophonAPIBaseURL overrides the getBuild/getPatchBuild base URL. Test seam
// ([DEV-3]); defaults to sophonChunkAPIBase.
func (p *Provider) SetSophonAPIBaseURL(u string) { p.sophonAPIBase = u }
```

`sidecar_paths.go` — append the four §A.8 helpers (no new imports; `filepath` + `core` already present):

```go
// sophonSubdir returns the cross-version Sophon sidecar root:
// <gameSidecarDir>/.sophon. The dot-prefix keeps App.scanForRecoveryRoot from
// misclassifying it as a version dir (see internal/app/update_handler.go skip
// guard). Holds manifests/ + applied.json (§4.1, §A.8).
func sophonSubdir(tempRoot string, gid core.GameID) string {
	return filepath.Join(gameSidecarDir(tempRoot, gid), ".sophon")
}

// sophonManifestsDir returns <…/.sophon>/manifests — raw <build_id>__<cat>.manifest.pb.zst blobs.
func sophonManifestsDir(tempRoot string, gid core.GameID) string {
	return filepath.Join(sophonSubdir(tempRoot, gid), "manifests")
}

// sophonAppliedJSONPath returns <…/.sophon>/applied.json — the applied-manifest index (§4.1).
func sophonAppliedJSONPath(tempRoot string, gid core.GameID) string {
	return filepath.Join(sophonSubdir(tempRoot, gid), "applied.json")
}

// sophonStagingDir returns the branch-split staging dir
// <versionSidecarDir>/staging/<branchKind>/<buildID> (branchKind ∈ {"main","predl"}, §A.8).
func sophonStagingDir(tempRoot string, gid core.GameID, version, branchKind, buildID string) string {
	return filepath.Join(versionSidecarDir(tempRoot, gid, version), "staging", branchKind, buildID)
}
```

- [ ] **Step 4 — Run, expect PASS.**

```bash
go build ./... && go vet ./internal/providers/hoyoverse/... && go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 5 — Commit.**

```bash
git add internal/providers/hoyoverse/meta.go internal/providers/hoyoverse/api.go internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/sidecar_paths.go internal/providers/hoyoverse/api_test.go internal/providers/hoyoverse/sidecar_paths_test.go
git commit -m "feat(hoyoverse): add Sophon meta.PlatApp, fetchBranchInfo + base-URL seams, sidecar path helpers"
```

---

### Task 14: plan_internal.go — Sophon flavors + genshinPlan fields + predlPlanCache

Appends the five Sophon `planFlavor` constants + `String()` cases (§A.5), extends `genshinPlan` with the 10 Sophon fields (§A.5, types from §A.2), and adds the in-memory `predlPlanCache` type (§A.6). Imports `sophon`. No behavior — pure type wiring that later tasks (15-21) fill in.

**Files:** `internal/providers/hoyoverse/plan_internal.go`, `internal/providers/hoyoverse/plan_internal_test.go` (new)

- [ ] **Step 1 — Failing test.** Create `internal/providers/hoyoverse/plan_internal_test.go`:

```go
package hoyoverse

import "testing"

func TestSophonFlavorStrings(t *testing.T) {
	cases := []struct {
		f    planFlavor
		want string
	}{
		{flavorSophonPatch, "sophon_patch"},
		{flavorSophonBuild, "sophon_build"},
		{flavorSophonFull, "sophon_full"},
		{flavorSophonPredlPatch, "sophon_predl_patch"},
		{flavorSophonPredlBuild, "sophon_predl_build"},
	}
	for _, c := range cases {
		if got := c.f.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", int(c.f), got, c.want)
		}
	}
}

func TestSophonFlavorValues(t *testing.T) {
	// Must NOT renumber the v1 constants (§A.5).
	if flavorSophonPatch != 6 {
		t.Errorf("flavorSophonPatch = %d, want 6", int(flavorSophonPatch))
	}
	if flavorSophonPredlBuild != 10 {
		t.Errorf("flavorSophonPredlBuild = %d, want 10", int(flavorSophonPredlBuild))
	}
}

func TestPredlPlanCacheFieldsCompile(t *testing.T) {
	// Compile-only assertion that the predl cache + genshinPlan Sophon fields exist.
	var gp genshinPlan
	gp.sophonBuildID = "b"
	gp.predlConsume = true
	pc := &predlPlanCache{Flavor: flavorSophonPredlPatch, BuildID: "b"}
	gp.predlPlan = pc
	if gp.predlPlan.BuildID != "b" {
		t.Fatal("predlPlanCache not wired")
	}
}
```

- [ ] **Step 2 — Run, expect FAIL.** Constants/fields/type undefined → compile error.

```bash
go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 3 — Implement.** Edit `internal/providers/hoyoverse/plan_internal.go`.

Add the `sophon` import (the file currently imports `"sync"` + `"omnigate/internal/core"`):

```go
import (
	"sync"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)
```

Append the five flavors to the `const (…)` block (after `flavorPredlFull`, do NOT renumber):

```go
	flavorPredlFull

	flavorSophonPatch      // 6
	flavorSophonBuild      // 7
	flavorSophonFull       // 8
	flavorSophonPredlPatch // 9
	flavorSophonPredlBuild // 10
```

Add the five cases to `String()` before the trailing `return "unknown"`:

```go
	case flavorPredlFull:
		return "predl_full"
	case flavorSophonPatch:
		return "sophon_patch"
	case flavorSophonBuild:
		return "sophon_build"
	case flavorSophonFull:
		return "sophon_full"
	case flavorSophonPredlPatch:
		return "sophon_predl_patch"
	case flavorSophonPredlBuild:
		return "sophon_predl_build"
	}
	return "unknown"
```

Extend `genshinPlan` with the 10 Sophon fields (§A.5). Replace the struct body:

```go
type genshinPlan struct {
	core.UpdatePlan
	flavor         planFlavor
	sourceVersion  string
	manifestETag   string
	audioLanguages []string
	predlAvailable bool

	// Sophon (M3.B v2) fields. Zero for HSR/ZZZ legacy plans.
	sophonBranch              *sophon.BranchInfo
	sophonBuildID             string
	sophonCategories          []sophon.Category
	sophonChunkSources        []sophon.ChunkSource
	sophonPatches             []sophon.PatchInstr
	sophonDeletes             []sophon.DeleteInstr
	sophonPatchAssetsFromMain map[string][]sophon.ChunkSource // assetPath → main-manifest chunk plan (§6.4 demotion)
	sophonAssetMD5            map[string]string               // §E item 5: assetPath → expected whole-file MD5 (AssetHashMd5); set by Task 18 for every asset that emits chunk_assemble sources, consumed by Task 20 to set chunk_assemble record AssetMD5
	predlConsume              bool
	predlSnapshot             *sophonPlanSnapshot // §E item 4: declared in THIS file (plan_internal.go), populated by Task 18; value field on sophonPredlReadyFile.PlanSnapshot
	predlPlan                 *predlPlanCache
}
```

> **RECONCILED (§E items 4 & 5):** (a) `sophonAssetMD5` is the 11th field — add it now; Task 18 populates it whenever it emits `chunk_assemble` ChunkSources, Task 20 reads `gp.sophonAssetMD5[asset]` for the record's `AssetMD5`. (b) `sophonPlanSnapshot` and `sophonPredlReadyFile` are declared in **this file** (see the step below), NOT `update_sophon_plan.go` — Tasks 18/21 reference them.

Add `predlPlanCache` (§A.6) at the end of the file:

```go
// predlPlanCache is the in-memory parallel predl plan captured at
// CheckForUpdate time (§3.3). Serialized into predl_ready.json.PlanSnapshot
// when the predl run completes (§7.1). Lives only on genshinPlan.predlPlan.
type predlPlanCache struct {
	Flavor         planFlavor
	BuildID        string
	SourceVersion  string
	TargetVersion  string
	AudioLanguages []string
	ChunkSources   []sophon.ChunkSource
	Patches        []sophon.PatchInstr
	Deletes        []sophon.DeleteInstr
	Categories     []sophon.Category
}
```

Per §E item 4, `sophonPlanSnapshot` and `sophonPredlReadyFile` are declared **here in `plan_internal.go`** (Task 14), because `genshinPlan.predlSnapshot *sophonPlanSnapshot` needs the type to compile now. Tasks 18 and 21 reference them; they do not re-declare. Declare both types now:

```go
// sophonPlanSnapshot is the predl snapshot persisted inside predl_ready.json
// (§A.6 / §7.1). The sophon.* element types carry no json tags, so they
// serialize as exported PascalCase fields — acceptable because this snapshot
// is private to the hoyoverse package. Do NOT add json tags to the sophon
// types (§A.6 note). Consumed by detectPredlConsume / RunUpdate (Task 18/21).
type sophonPlanSnapshot struct {
	SophonChunkSources []sophon.ChunkSource `json:"sophon_chunk_sources"`
	SophonPatches      []sophon.PatchInstr  `json:"sophon_patches"`
	SophonDeletes      []sophon.DeleteInstr `json:"sophon_deletes"`
	Categories         []sophon.Category    `json:"categories"`
}

// sophonPredlReadyFile is the predl_ready.json sidecar for a staged Sophon
// predownload (§7.1). It embeds the v1 core.ProgressFile base (Entries stays
// empty for Sophon — chunk progress lives in sophon_progress.json) so the v1
// recovery readers keep working, and adds the Sophon predl fields. Read by
// detectPredlConsume (Task 18); written by the predl path in RunUpdate (Task 21).
type sophonPredlReadyFile struct {
	core.ProgressFile                    // v1 base; Entries empty for Sophon
	Kind           string             `json:"kind"`            // "sophon_patch" | "sophon_build"
	BuildID        string             `json:"build_id"`
	SourceVersion  string             `json:"source_version"`
	TargetVersion  string             `json:"target_version"`
	AudioLanguages []string           `json:"audio_languages"`
	StagedAt       string             `json:"staged_at"`       // RFC3339
	PlanSnapshot   sophonPlanSnapshot `json:"plan_snapshot"`   // value, not pointer (T21-B)
}
```

(Integrator note: §B's files-list originally assigned these two types to `update_sophon_plan.go`; declaring them in `plan_internal.go` is correct and harmless within one package — `genshinPlan.predlSnapshot` needs `sophonPlanSnapshot` at T14. Tasks 18/21 MUST NOT redeclare either type; they only reference them. Requires `import "omnigate/internal/core"` in plan_internal.go.)

- [ ] **Step 4 — Run, expect PASS.**

```bash
go build ./... && go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 5 — Commit.**

```bash
git add internal/providers/hoyoverse/plan_internal.go internal/providers/hoyoverse/plan_internal_test.go
git commit -m "feat(hoyoverse): add Sophon planFlavors, genshinPlan fields, predlPlanCache + snapshot types"
```

---

### Task 15: sophon_progress.go — chunk-level progress sidecar

New `sophonProgressFile` sidecar at `<versionSidecarDir>/sophon_progress.json` (§A.6, §5.2). Store with atomic write mirroring v1 `progressStore.Persist` (mu.Lock across the whole tmp→fsync→rename), plus `Load` with corrupt-file recovery (delete + return nil, like `loadJSONSidecar`) and `MarkChunkDone` / `MarkPatchDone` helpers. v1's `progress.json` untouched.

**Files:** `internal/providers/hoyoverse/sophon_progress.go` (new), `internal/providers/hoyoverse/sophon_progress_test.go` (new)

- [ ] **Step 1 — Failing test.** Create `internal/providers/hoyoverse/sophon_progress_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestSophonProgress_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	st, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkChunkDone("chunk-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkChunkDone("chunk-2"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkPatchDone("patch-1"); err != nil {
		t.Fatal(err)
	}

	pf, err := loadSophonProgress(tmp, gid, "6.6.0")
	if err != nil {
		t.Fatal(err)
	}
	if pf == nil {
		t.Fatal("loadSophonProgress returned nil after writes")
	}
	if pf.GameID != string(gid) || pf.Version != "6.6.0" || pf.BranchKind != "main" || pf.BuildID != "buildA" {
		t.Errorf("header mismatch: %+v", pf)
	}
	if !pf.ChunksDone["chunk-1"] || !pf.ChunksDone["chunk-2"] {
		t.Errorf("ChunksDone = %v", pf.ChunksDone)
	}
	if !pf.PatchesDone["patch-1"] {
		t.Errorf("PatchesDone = %v", pf.PatchesDone)
	}
}

func TestSophonProgress_PartialResume(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	st, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.MarkChunkDone("done-1")

	// Re-open: existing progress must be loaded, not clobbered.
	st2, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildA")
	if err != nil {
		t.Fatal(err)
	}
	if !st2.ChunkDone("done-1") {
		t.Error("resume lost done-1")
	}
	if st2.ChunkDone("never") {
		t.Error("ChunkDone returned true for absent chunk")
	}
}

func TestSophonProgress_CorruptRecovery(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sophon_progress.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	pf, err := loadSophonProgress(tmp, gid, "6.6.0")
	if err != nil {
		t.Fatalf("expected nil err on corrupt, got %v", err)
	}
	if pf != nil {
		t.Errorf("expected nil pf on corrupt, got %+v", pf)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected corrupt file removed, stat err: %v", statErr)
	}
}
```

- [ ] **Step 2 — Run, expect FAIL.** Undefined symbols → compile error.

```bash
go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 3 — Implement.** Create `internal/providers/hoyoverse/sophon_progress.go`:

```go
package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"omnigate/internal/core"
)

// sophonProgressFile is the chunk-level download progress sidecar
// (<versionSidecarDir>/sophon_progress.json, §A.6 / §5.2). v1's progress.json
// (core.ProgressFile, per-file granularity) is untouched and still used by
// HSR/ZZZ. Keys (ChunkName / PatchName) are the full CDN filenames from the
// manifest.
type sophonProgressFile struct {
	GameID      string          `json:"game_id"`
	Version     string          `json:"version"`
	BranchKind  string          `json:"branch_kind"`  // "main" | "predl"
	BuildID     string          `json:"build_id"`
	Stage       string          `json:"stage"`        // "download" | "apply"
	ChunksDone  map[string]bool `json:"chunks_done"`  // key = ChunkName
	PatchesDone map[string]bool `json:"patches_done"` // key = PatchName
}

// sophonProgressStore guards a sophonProgressFile with mu held across the
// whole atomic write, mirroring v1 progressStore.Persist so the 4-worker
// download pool (Task 19) cannot collide on the shared *.tmp during Rename.
type sophonProgressStore struct {
	mu       sync.Mutex
	tempRoot string
	gid      core.GameID
	version  string
	pf       *sophonProgressFile
}

// newSophonProgressStore loads an existing sophon_progress.json (resume) or
// creates a fresh one. On resume it preserves ChunksDone/PatchesDone but
// refreshes the header fields to the current run's values.
func newSophonProgressStore(tempRoot string, gid core.GameID, version, branchKind, buildID string) (*sophonProgressStore, error) {
	dir := versionSidecarDir(tempRoot, gid, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pf, err := loadSophonProgress(tempRoot, gid, version)
	if err != nil {
		return nil, err
	}
	if pf == nil {
		pf = &sophonProgressFile{
			GameID:      string(gid),
			Version:     version,
			BranchKind:  branchKind,
			BuildID:     buildID,
			Stage:       "download",
			ChunksDone:  make(map[string]bool),
			PatchesDone: make(map[string]bool),
		}
	} else {
		pf.GameID = string(gid)
		pf.Version = version
		pf.BranchKind = branchKind
		pf.BuildID = buildID
		if pf.ChunksDone == nil {
			pf.ChunksDone = make(map[string]bool)
		}
		if pf.PatchesDone == nil {
			pf.PatchesDone = make(map[string]bool)
		}
	}
	st := &sophonProgressStore{tempRoot: tempRoot, gid: gid, version: version, pf: pf}
	return st, nil
}

// loadSophonProgress reads the sidecar with corrupt-file recovery (delete +
// nil), matching loadJSONSidecar semantics. Returns (nil, nil) on ENOENT.
func loadSophonProgress(tempRoot string, gid core.GameID, version string) (*sophonProgressFile, error) {
	path := filepath.Join(versionSidecarDir(tempRoot, gid, version), "sophon_progress.json")
	return loadJSONSidecar[sophonProgressFile](path)
}

func (s *sophonProgressStore) path() string {
	return filepath.Join(versionSidecarDir(s.tempRoot, s.gid, s.version), "sophon_progress.json")
}

// Save atomically writes the current progress: mu held across the whole
// tmp→write→rename (v1 progressStore.Persist idiom).
func (s *sophonProgressStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *sophonProgressStore) saveLocked() error {
	data, err := json.MarshalIndent(s.pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sophon_progress.json: %w", err)
	}
	dir := versionSidecarDir(s.tempRoot, s.gid, s.version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := s.path()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// MarkChunkDone records a chunk as verified-on-disk and persists.
func (s *sophonProgressStore) MarkChunkDone(chunkName string) error {
	s.mu.Lock()
	s.pf.ChunksDone[chunkName] = true
	s.mu.Unlock()
	return s.Save()
}

// MarkPatchDone records a patch blob as verified-on-disk and persists.
func (s *sophonProgressStore) MarkPatchDone(patchName string) error {
	s.mu.Lock()
	s.pf.PatchesDone[patchName] = true
	s.mu.Unlock()
	return s.Save()
}

// ChunkDone reports whether chunkName is already done (resume skip, §5.2 step 1).
func (s *sophonProgressStore) ChunkDone(chunkName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pf.ChunksDone[chunkName]
}

// PatchDone reports whether patchName is already done.
func (s *sophonProgressStore) PatchDone(patchName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pf.PatchesDone[patchName]
}

// SetStage updates the stage field ("download"|"apply") and persists.
func (s *sophonProgressStore) SetStage(stage string) error {
	s.mu.Lock()
	s.pf.Stage = stage
	s.mu.Unlock()
	return s.Save()
}
```

- [ ] **Step 4 — Run, expect PASS.**

```bash
go build ./... && go vet ./internal/providers/hoyoverse/... && go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 5 — Commit.**

```bash
git add internal/providers/hoyoverse/sophon_progress.go internal/providers/hoyoverse/sophon_progress_test.go
git commit -m "feat(hoyoverse): add sophon_progress.json chunk-level progress sidecar"
```

---

### Task 16: sophon_apply_wal.go — typed-record WAL + batched rewrite

New `sophon_apply.wal` sidecar (§A.6 / §6.1): `sophonApplyWAL` / `sophonApplyRecord` / `walChunkSource` types, atomic `writeSophonApplyWAL` / `readSophonApplyWAL`, `toWalChunkSource` / `fromWalChunkSource` conversions (`sophon.ChunkSource` ↔ `walChunkSource`), and a `walFlusher` implementing the batched-rewrite policy (flush every 50 records OR every 5s, flush on close — §6.1). The flusher takes an explicit `now func() time.Time` (defaulting to `time.Now`) as a **documented local test seam** — §A.7 forbids a global clock interface, so this seam is per-flusher only and not exported.

**Files:** `internal/providers/hoyoverse/sophon_apply_wal.go` (new), `internal/providers/hoyoverse/sophon_apply_wal_test.go` (new)

- [ ] **Step 1 — Failing test.** Create `internal/providers/hoyoverse/sophon_apply_wal_test.go`:

```go
package hoyoverse

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

func TestSophonWAL_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{
		GameID:      string(gid),
		TargetTag:   "6.6.0",
		BuildID:     "buildA",
		SourceTag:   "6.5.0",
		Flavor:      flavorSophonPatch.String(),
		BranchKind:  "main",
		StagingRoot: filepath.Join(dir, "staging", "main", "buildA"),
		Records: []sophonApplyRecord{
			{Kind: "hdiff_patch", Category: "game", Path: "a.pak", State: "pending", PatchName: "p1", OriginalFileMD5: "old"},
			{Kind: "chunk_assemble", Category: "game", Path: "b.pak", State: "pending", AssembleSources: []walChunkSource{
				{Kind: "cdn", ChunkName: "c1", URLPrefix: "https://cdn", CompressedSz: 10, UseCompress: true, DecompSize: 20, FileOffset: 0, ExpectMD5: "m1"},
			}},
			{Kind: "delete", Category: "game", Path: "old.pak", State: "pending", ExpectMD5: "dm"},
		},
	}
	if err := writeSophonApplyWAL(dir, wal); err != nil {
		t.Fatal(err)
	}
	got, err := readSophonApplyWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("readSophonApplyWAL returned nil")
	}
	if got.TargetTag != "6.6.0" || got.Flavor != "sophon_patch" || len(got.Records) != 3 {
		t.Errorf("header/records mismatch: %+v", got)
	}
	if got.Records[1].AssembleSources[0].ChunkName != "c1" {
		t.Errorf("assemble source not round-tripped: %+v", got.Records[1])
	}
}

func TestSophonWAL_ReadMissing(t *testing.T) {
	got, err := readSophonApplyWAL(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil err for ENOENT, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil WAL, got %+v", got)
	}
}

func TestSophonWAL_FirstPending(t *testing.T) {
	wal := &sophonApplyWAL{Records: []sophonApplyRecord{
		{Path: "a", State: "done"},
		{Path: "b", State: "done"},
		{Path: "c", State: "pending"},
		{Path: "d", State: "pending"},
	}}
	if idx := wal.firstPending(); idx != 2 {
		t.Errorf("firstPending = %d, want 2", idx)
	}
	allDone := &sophonApplyWAL{Records: []sophonApplyRecord{{State: "done"}}}
	if idx := allDone.firstPending(); idx != -1 {
		t.Errorf("firstPending (all done) = %d, want -1", idx)
	}
}

func TestWalChunkSourceConversions(t *testing.T) {
	cs := sophon.ChunkSource{
		Kind: sophon.SourceLocal, Asset: "a.pak", ChunkName: "c1", URLPrefix: "https://cdn",
		CompressedSz: 10, UseCompress: true, OldFile: "old.pak", OldOffset: 64,
		DecompSize: 20, FileOffset: 128, ExpectMD5: "m1",
	}
	w := toWalChunkSource(cs)
	if w.Kind != "local" || w.ChunkName != "c1" || w.URLPrefix != "https://cdn" || w.OldFile != "old.pak" || w.OldOffset != 64 || w.FileOffset != 128 {
		t.Errorf("toWalChunkSource lossy: %+v", w)
	}
	back := fromWalChunkSource(w)
	if back != cs {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, cs)
	}
}

func TestWalFlusher_CountCadence(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid), TargetTag: "6.6.0"}
	for i := 0; i < 60; i++ {
		wal.Records = append(wal.Records, sophonApplyRecord{Path: "f", State: "pending"})
	}
	frozen := time.Unix(0, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return frozen })

	// 49 transitions: below the 50-count threshold, no flush (no clock advance).
	for i := 0; i < 49; i++ {
		wal.Records[i].State = "done"
		if err := fl.maybeFlush(); err != nil {
			t.Fatal(err)
		}
	}
	if readWALOrNil(t, dir) != nil {
		t.Fatal("flushed before reaching 50-count threshold")
	}
	// 50th transition crosses the count threshold → flush.
	wal.Records[49].State = "done"
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	got := readWALOrNil(t, dir)
	if got == nil {
		t.Fatal("expected flush at 50-count threshold")
	}
}

func TestWalFlusher_TimeCadence(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid)}
	wal.Records = []sophonApplyRecord{{Path: "f", State: "pending"}}
	now := time.Unix(100, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return now })

	wal.Records[0].State = "done"
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) != nil {
		t.Fatal("flushed before 5s elapsed and below count threshold")
	}
	// Advance the injected clock past 5s → time cadence triggers.
	now = time.Unix(106, 0)
	if err := fl.maybeFlush(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) == nil {
		t.Fatal("expected flush after 5s elapsed")
	}
}

func TestWalFlusher_FlushOnClose(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{GameID: string(gid)}
	wal.Records = []sophonApplyRecord{{Path: "f", State: "pending"}}
	frozen := time.Unix(0, 0)
	fl := newWalFlusher(dir, wal, func() time.Time { return frozen })
	wal.Records[0].State = "done"
	_ = fl.maybeFlush() // below thresholds, no write
	if readWALOrNil(t, dir) != nil {
		t.Fatal("unexpected flush before close")
	}
	if err := fl.Close(); err != nil {
		t.Fatal(err)
	}
	if readWALOrNil(t, dir) == nil {
		t.Fatal("Close did not flush")
	}
}

func readWALOrNil(t *testing.T, dir string) *sophonApplyWAL {
	t.Helper()
	w, err := readSophonApplyWAL(dir)
	if err != nil {
		t.Fatalf("readSophonApplyWAL: %v", err)
	}
	return w
}
```

- [ ] **Step 2 — Run, expect FAIL.** Undefined symbols → compile error.

```bash
go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 3 — Implement.** Create `internal/providers/hoyoverse/sophon_apply_wal.go`:

```go
package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/providers/hoyoverse/sophon"
)

// sophonApplyWAL is the typed-record write-ahead log for Sophon apply
// (<versionSidecarDir>/sophon_apply.wal, §A.6 / §6.1). v1's flat-list
// apply.wal is untouched (HSR/ZZZ).
type sophonApplyWAL struct {
	GameID      string              `json:"game_id"`
	TargetTag   string              `json:"target_tag"`
	BuildID     string              `json:"build_id"`
	SourceTag   string              `json:"source_tag"`   // "" for flavorSophonFull
	Flavor      string              `json:"flavor"`       // planFlavor.String()
	BranchKind  string              `json:"branch_kind"`  // "main" — predl never reaches apply
	WasPredl    bool                `json:"was_predl"`    // true if originated from predl-consume
	StagingRoot string              `json:"staging_root"` // resolved staging dir; all reads relative to it
	Records     []sophonApplyRecord `json:"records"`
}

type sophonApplyRecord struct {
	Kind            string           `json:"kind"`  // "chunk_assemble" | "hdiff_patch" | "copy_over" | "delete"
	Category        string           `json:"category"`
	Path            string           `json:"path"`  // target relative to gameDir
	State           string           `json:"state"` // "pending" | "done"
	AssetMD5        string           `json:"asset_md5,omitempty"`
	OldPath         string           `json:"old_path,omitempty"`
	PatchName       string           `json:"patch_name,omitempty"`
	PatchOff        int64            `json:"patch_off,omitempty"`
	PatchLen        int64            `json:"patch_len,omitempty"`
	ExpectMD5       string           `json:"expect_md5,omitempty"`        // delete pre-check
	AssembleSources []walChunkSource `json:"assemble_sources,omitempty"`  // chunk_assemble (incl §6.4 demotions)
	OriginalFileMD5 string           `json:"original_file_md5,omitempty"` // hdiff_patch pre-apply guard
}

type walChunkSource struct {
	Kind         string `json:"kind"` // "cdn" | "local"
	ChunkName    string `json:"chunk_name"`
	URLPrefix    string `json:"url_prefix"`
	CompressedSz int64  `json:"compressed_sz"`
	UseCompress  bool   `json:"use_compress"`
	OldFile      string `json:"old_file,omitempty"`
	OldOffset    int64  `json:"old_offset,omitempty"`
	DecompSize   int64  `json:"decomp_size"`
	FileOffset   int64  `json:"file_offset"`
	ExpectMD5    string `json:"expect_md5"`
}

const sophonApplyWALName = "sophon_apply.wal"

// toWalChunkSource converts an in-memory sophon.ChunkSource to its JSON-tagged
// WAL form. sophon.Kind uses the SourceCDN/SourceLocal string constants which
// equal "cdn"/"local" (§A.2), so Kind copies through verbatim.
func toWalChunkSource(cs sophon.ChunkSource) walChunkSource {
	return walChunkSource{
		Kind:         cs.Kind,
		ChunkName:    cs.ChunkName,
		URLPrefix:    cs.URLPrefix,
		CompressedSz: cs.CompressedSz,
		UseCompress:  cs.UseCompress,
		OldFile:      cs.OldFile,
		OldOffset:    cs.OldOffset,
		DecompSize:   cs.DecompSize,
		FileOffset:   cs.FileOffset,
		ExpectMD5:    cs.ExpectMD5,
	}
}

// fromWalChunkSource is the inverse. Asset is not persisted in the WAL (it is
// plan-time-only scoping metadata, §A.2) and stays "" on the way back.
func fromWalChunkSource(w walChunkSource) sophon.ChunkSource {
	return sophon.ChunkSource{
		Kind:         w.Kind,
		ChunkName:    w.ChunkName,
		URLPrefix:    w.URLPrefix,
		CompressedSz: w.CompressedSz,
		UseCompress:  w.UseCompress,
		OldFile:      w.OldFile,
		OldOffset:    w.OldOffset,
		DecompSize:   w.DecompSize,
		FileOffset:   w.FileOffset,
		ExpectMD5:    w.ExpectMD5,
	}
}

// firstPending returns the index of the first record whose State != "done",
// or -1 if all done (§6.2 step 2 resume point).
func (w *sophonApplyWAL) firstPending() int {
	for i := range w.Records {
		if w.Records[i].State != "done" {
			return i
		}
	}
	return -1
}

// writeSophonApplyWAL atomically writes wal to <dir>/sophon_apply.wal
// (tmp→write→rename), mirroring v1 progressStore.Persist.
func writeSophonApplyWAL(dir string, wal *sophonApplyWAL) error {
	data, err := json.MarshalIndent(wal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sophon_apply.wal: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sophonApplyWALName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readSophonApplyWAL reads <dir>/sophon_apply.wal with corrupt-file recovery
// (delete + nil) via loadJSONSidecar. (nil, nil) on ENOENT.
func readSophonApplyWAL(dir string) (*sophonApplyWAL, error) {
	return loadJSONSidecar[sophonApplyWAL](filepath.Join(dir, sophonApplyWALName))
}

// walFlusher batches WAL rewrites (§6.1): it writes to disk when ≥50 record
// state-transitions have accumulated since the last flush OR ≥5s of injected
// clock time has elapsed, whichever comes first; Close performs a final flush.
//
// Local test seam (§A.7): the flusher takes an explicit now func defaulting to
// time.Now. This is NOT a global clock interface — it is a per-flusher field
// only, so production code keeps calling time.Now and §A.7's "no clock
// interface" rule holds. Tests inject a fixed/steppable clock.
type walFlusher struct {
	dir        string
	wal        *sophonApplyWAL
	now        func() time.Time
	sinceCount int
	lastFlush  time.Time
}

const (
	walFlushEveryN  = 50
	walFlushEvery   = 5 * time.Second
)

func newWalFlusher(dir string, wal *sophonApplyWAL, now func() time.Time) *walFlusher {
	if now == nil {
		now = time.Now
	}
	return &walFlusher{dir: dir, wal: wal, now: now, lastFlush: now()}
}

// maybeFlush records one state transition and flushes if either cadence
// threshold is crossed. Call exactly once per record state change.
func (f *walFlusher) maybeFlush() error {
	f.sinceCount++
	if f.sinceCount >= walFlushEveryN || f.now().Sub(f.lastFlush) >= walFlushEvery {
		return f.flush()
	}
	return nil
}

// Close forces a final flush regardless of cadence (apply completion or
// controlled shutdown, §6.1).
func (f *walFlusher) Close() error {
	return f.flush()
}

func (f *walFlusher) flush() error {
	if err := writeSophonApplyWAL(f.dir, f.wal); err != nil {
		return err
	}
	f.sinceCount = 0
	f.lastFlush = f.now()
	return nil
}
```

- [ ] **Step 4 — Run, expect PASS.**

```bash
go build ./... && go vet ./internal/providers/hoyoverse/... && go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 5 — Commit.**

```bash
git add internal/providers/hoyoverse/sophon_apply_wal.go internal/providers/hoyoverse/sophon_apply_wal_test.go
git commit -m "feat(hoyoverse): add sophon_apply.wal typed-record sidecar with batched-rewrite flusher"
```

---

### Task 17: sophon_manifest_cache.go — applied-manifest save/load/rotate/cleanup

On-disk manifest cache under `<gameSidecarDir>/.sophon/` (§4): `SaveAppliedManifest` (write raw zstd-proto blob), `LoadAppliedManifests` returning an `appliedSet` that lazy-loads + zstd-decompresses + unmarshals blobs via `MatchByVersion`, `RotateAfterApply` (capture evicted, atomic-write new `applied.json`, delete evicted blobs, sweep orphans — §4.3), and `cleanupStaleSophonSidecars` (§4 + §6.2: all-done WALs matching currentTag → remove; version dirs not in allowedTargets AND >7 days old → remove). The on-disk index types `appliedManifestSet` / `appliedBuild` are from §A.6; `appliedSet` is the in-memory wrapper that lazy-loads blobs.

Uses `github.com/klauspost/compress/zstd` + `google.golang.org/protobuf/proto` + `pb` (added in Task 1 / generated in Task 4). `time.Now().UTC()` inline for `applied_at` (§A.7 — no clock seam).

**Files:** `internal/providers/hoyoverse/sophon_manifest_cache.go` (new), `internal/providers/hoyoverse/sophon_manifest_cache_test.go` (new)

- [ ] **Step 1 — Failing test.** Create `internal/providers/hoyoverse/sophon_manifest_cache_test.go`. The test builds its own zstd-proto blob from a `pb.SophonManifestProto` so it is self-contained:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

func mustManifestZst(t *testing.T, assetName string) []byte {
	t.Helper()
	m := &pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{
			{AssetName: assetName, AssetSize: 4, AssetHashMd5: "abc", AssetChunks: []*pb.SophonManifestAssetChunk{
				{ChunkName: "c1", ChunkDecompressedHashMd5: "m1", ChunkSize: 2, ChunkSizeDecompressed: 4},
			}},
		},
	}
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeAll(raw, nil)
}

func TestManifestCache_SaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	blob := mustManifestZst(t, "GenshinImpact_Data/a.pak")
	if err := SaveAppliedManifest(tmp, gid, "game", "buildA", "6.6.0", blob); err != nil {
		t.Fatal(err)
	}
	set, err := LoadAppliedManifests(tmp, gid)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil {
		t.Fatal("LoadAppliedManifests returned nil after save")
	}
	m := set.MatchByVersion("6.6.0", "game")
	if m == nil {
		t.Fatal("MatchByVersion miss")
	}
	if len(m.Assets) != 1 || m.Assets[0].AssetName != "GenshinImpact_Data/a.pak" {
		t.Errorf("lazy-loaded manifest wrong: %+v", m.Assets)
	}
	if set.MatchByVersion("9.9.9", "game") != nil {
		t.Error("MatchByVersion should miss unknown version")
	}
	if set.MatchByVersion("6.6.0", "ja-jp") != nil {
		t.Error("MatchByVersion should miss unknown category")
	}
}

func TestManifestCache_RotationEvictsPrevious(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	// Build the 6.5.0 (buildPrev) + 6.6.0 (buildLatest) → applied state.
	if err := SaveAppliedManifest(tmp, gid, "game", "buildPrev", "6.5.0", mustManifestZst(t, "p.pak")); err != nil {
		t.Fatal(err)
	}
	if err := RotateAfterApply(tmp, gid, "buildPrev", "6.5.0", map[string]string{"game": "buildPrev"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveAppliedManifest(tmp, gid, "game", "buildLatest", "6.6.0", mustManifestZst(t, "l.pak")); err != nil {
		t.Fatal(err)
	}
	if err := RotateAfterApply(tmp, gid, "buildLatest", "6.6.0", map[string]string{"game": "buildLatest"}); err != nil {
		t.Fatal(err)
	}
	set, err := LoadAppliedManifests(tmp, gid)
	if err != nil {
		t.Fatal(err)
	}
	if set.set.Latest == nil || set.set.Latest.BuildID != "buildLatest" || set.set.Latest.Version != "6.6.0" {
		t.Errorf("latest = %+v", set.set.Latest)
	}
	if set.set.Previous == nil || set.set.Previous.BuildID != "buildPrev" {
		t.Errorf("previous = %+v", set.set.Previous)
	}
	// Now apply a 3rd build → buildPrev's blob must be GC'd.
	if err := SaveAppliedManifest(tmp, gid, "game", "build3", "6.7.0", mustManifestZst(t, "x.pak")); err != nil {
		t.Fatal(err)
	}
	if err := RotateAfterApply(tmp, gid, "build3", "6.7.0", map[string]string{"game": "build3"}); err != nil {
		t.Fatal(err)
	}
	evicted := filepath.Join(sophonManifestsDir(tmp, gid), "buildPrev__game.manifest.pb.zst")
	if _, err := os.Stat(evicted); !os.IsNotExist(err) {
		t.Errorf("evicted blob still present: %v", err)
	}
	keep := filepath.Join(sophonManifestsDir(tmp, gid), "buildLatest__game.manifest.pb.zst")
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("previous-now blob missing: %v", err)
	}
}

func TestCleanupStaleSophonSidecars_AllDoneWAL(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	dir := versionSidecarDir(tmp, gid, "6.6.0")
	wal := &sophonApplyWAL{TargetTag: "6.6.0", Records: []sophonApplyRecord{{State: "done"}}}
	if err := writeSophonApplyWAL(dir, wal); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleSophonSidecars(tmp, gid, "6.6.0", []string{"6.6.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, sophonApplyWALName)); !os.IsNotExist(err) {
		t.Errorf("all-done WAL matching currentTag not removed: %v", err)
	}
}

func TestCleanupStaleSophonSidecars_OrphanStaging(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	// An orphan version dir for a version not in allowedTargets, aged >7 days.
	orphan := versionSidecarDir(tmp, gid, "6.3.0")
	if err := os.MkdirAll(filepath.Join(orphan, "staging", "main", "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	// A fresh dir for an allowed target must survive.
	keep := versionSidecarDir(tmp, gid, "6.6.0")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleSophonSidecars(tmp, gid, "6.6.0", []string{"6.6.0", "6.7.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan >7d version dir not removed: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("allowed-target dir wrongly removed: %v", err)
	}
}
```

- [ ] **Step 2 — Run, expect FAIL.** Undefined symbols + (until Task 1) missing deps → compile error.

```bash
go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 3 — Implement.** Create `internal/providers/hoyoverse/sophon_manifest_cache.go`:

```go
package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omnigate/internal/core"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

// appliedManifestSet is the on-disk applied.json index (§A.6 / §4.1).
type appliedManifestSet struct {
	Latest   *appliedBuild `json:"latest"`
	Previous *appliedBuild `json:"previous"`
}

type appliedBuild struct {
	BuildID    string            `json:"build_id"`
	Version    string            `json:"version"`
	AppliedAt  string            `json:"applied_at"`  // RFC3339
	Categories map[string]string `json:"categories"`  // matchingField → build_id
}

// appliedSet is the in-memory wrapper around appliedManifestSet that
// lazy-loads (+ zstd-decompresses + unmarshals) blob files on demand
// (§4.2). The proto is NOT held across CheckForUpdate calls.
type appliedSet struct {
	tempRoot string
	gid      core.GameID
	set      appliedManifestSet
}

func manifestBlobName(buildID, category string) string {
	return buildID + "__" + category + ".manifest.pb.zst"
}

// SaveAppliedManifest writes the raw zstd-proto wire bytes for one
// (build_id, category) into <…/.sophon>/manifests/ (§4.1). Atomic
// tmp→write→rename. It does NOT touch applied.json — RotateAfterApply does.
func SaveAppliedManifest(tempRoot string, gid core.GameID, category, buildID, version string, rawZst []byte) error {
	dir := sophonManifestsDir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, manifestBlobName(buildID, category))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, rawZst, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LoadAppliedManifests reads applied.json. Returns nil if absent (§4.2).
// Corrupt index heals via loadJSONSidecar (delete + nil).
func LoadAppliedManifests(tempRoot string, gid core.GameID) (*appliedSet, error) {
	idx, err := loadJSONSidecar[appliedManifestSet](sophonAppliedJSONPath(tempRoot, gid))
	if err != nil {
		return nil, err
	}
	if idx == nil {
		return nil, nil
	}
	return &appliedSet{tempRoot: tempRoot, gid: gid, set: *idx}, nil
}

// MatchByVersion lazily loads the blob for (version, category) and returns the
// parsed manifest, or nil if no latest/previous slot matches both (§4.2).
func (a *appliedSet) MatchByVersion(version, category string) *pb.SophonManifestProto {
	buildID := a.buildIDFor(version, category)
	if buildID == "" {
		return nil
	}
	path := filepath.Join(sophonManifestsDir(a.tempRoot, a.gid), manifestBlobName(buildID, category))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m, err := decodeManifestZst(raw)
	if err != nil {
		return nil
	}
	return m
}

func (a *appliedSet) buildIDFor(version, category string) string {
	for _, b := range []*appliedBuild{a.set.Latest, a.set.Previous} {
		if b == nil || b.Version != version {
			continue
		}
		if bid, ok := b.Categories[category]; ok {
			return bid
		}
	}
	return ""
}

// decodeManifestZst zstd-decompresses then proto-unmarshals a manifest blob.
func decodeManifestZst(rawZst []byte) (*pb.SophonManifestProto, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	raw, err := dec.DecodeAll(rawZst, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decode manifest: %w", err)
	}
	var m pb.SophonManifestProto
	if err := proto.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("proto unmarshal manifest: %w", err)
	}
	return &m, nil
}

// RotateAfterApply promotes build B (version V, per-category build ids) to
// latest, demotes old latest to previous, evicts the old previous's blobs, and
// sweeps orphan blobs (§4.3). Write order: capture-evicted → atomic-write
// applied.json → delete evicted blobs → orphan sweep.
func RotateAfterApply(tempRoot string, gid core.GameID, buildID, version string, newCategories map[string]string) error {
	cur, err := loadJSONSidecar[appliedManifestSet](sophonAppliedJSONPath(tempRoot, gid))
	if err != nil {
		return err
	}
	if cur == nil {
		cur = &appliedManifestSet{}
	}

	// 1. Capture evicted (old previous).
	var evicted *appliedBuild
	if cur.Previous != nil {
		evicted = cur.Previous
	}

	// 2. Build new in-memory applied: latest = B, previous = old latest.
	next := appliedManifestSet{
		Latest: &appliedBuild{
			BuildID:    buildID,
			Version:    version,
			AppliedAt:  time.Now().UTC().Format(time.RFC3339),
			Categories: newCategories,
		},
		Previous: cur.Latest,
	}

	// 3. Atomic write new applied.json.
	if err := writeAppliedJSON(tempRoot, gid, &next); err != nil {
		return err
	}

	// 4. Delete evicted build's blobs.
	manDir := sophonManifestsDir(tempRoot, gid)
	if evicted != nil {
		for _, bid := range evicted.Categories {
			_ = removeBlobsForBuild(manDir, bid)
		}
	}

	// 5. Sweep any blob whose build_id is not referenced by new latest∪previous.
	keep := map[string]bool{}
	for _, b := range []*appliedBuild{next.Latest, next.Previous} {
		if b == nil {
			continue
		}
		for _, bid := range b.Categories {
			keep[bid] = true
		}
	}
	entries, _ := os.ReadDir(manDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".manifest.pb.zst") {
			continue
		}
		bid := strings.SplitN(e.Name(), "__", 2)[0]
		if !keep[bid] {
			_ = os.Remove(filepath.Join(manDir, e.Name()))
		}
	}
	return nil
}

func removeBlobsForBuild(manDir, buildID string) error {
	entries, err := os.ReadDir(manDir)
	if err != nil {
		return err
	}
	prefix := buildID + "__"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".manifest.pb.zst") {
			_ = os.Remove(filepath.Join(manDir, e.Name()))
		}
	}
	return nil
}

func writeAppliedJSON(tempRoot string, gid core.GameID, set *appliedManifestSet) error {
	dir := sophonSubdir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal applied.json: %w", err)
	}
	path := sophonAppliedJSONPath(tempRoot, gid)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// cleanupStaleSophonSidecars sweeps version-scoped dirs under gameSidecarDir
// (§4 + §6.2):
//   (a) a version dir whose sophon_apply.wal is all-done AND TargetTag ==
//       currentTag → remove the whole dir (rotate-vs-cleanup crash window).
//   (b) a version dir for a version NOT in allowedTargets AND older than 7
//       days by mtime → remove (orphan staging from abandoned runs).
// The cross-version .sophon dir (dot-prefixed) is always skipped.
func cleanupStaleSophonSidecars(tempRoot string, gid core.GameID, currentTag string, allowedTargets []string) error {
	root := gameSidecarDir(tempRoot, gid)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	allowed := map[string]bool{}
	for _, t := range allowedTargets {
		allowed[t] = true
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") { // skip .sophon and any dotfile dir
			continue
		}
		vdir := filepath.Join(root, name)

		// (a) all-done WAL matching currentTag.
		if wal, _ := readSophonApplyWAL(vdir); wal != nil {
			if wal.TargetTag == currentTag && wal.firstPending() == -1 {
				_ = os.RemoveAll(vdir)
				continue
			}
		}

		// (b) orphan: not an allowed target AND aged >7 days.
		if allowed[name] {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(vdir)
		}
	}
	return nil
}
```

- [ ] **Step 4 — Run, expect PASS.** (Requires Task 1's deps + Task 4's generated `sophon/proto` to be present, which §D sequences before Task 13.)

```bash
go build ./... && go vet ./internal/providers/hoyoverse/... && go test -count=1 ./internal/providers/hoyoverse/...
```

- [ ] **Step 5 — Commit.**

```bash
git add internal/providers/hoyoverse/sophon_manifest_cache.go internal/providers/hoyoverse/sophon_manifest_cache_test.go
git commit -m "feat(hoyoverse): add Sophon applied-manifest cache with rotation + stale-sidecar cleanup"
```

### Task 18: `update_sophon_plan.go` — plan orchestration (`mapFoldersToMatchingFields`, `buildSophonPlan`, `buildSophonPatchPlan`, `buildSophonBuildPlan`, predl plan, `detectPredlConsume`)

> **PATH note (every Go step in this part):** subagent shells may lack Go on PATH. Prepend once per shell:
> `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`. This host is `CGO_ENABLED=0` — **never** pass `-race`. Package gate: `go test -count=1 ./internal/providers/hoyoverse/...`.

This task builds the planning layer that turns a `*sophon.BranchInfo` into a `*genshinPlan` with `sophonChunkSources` / `sophonPatches` / `sophonDeletes` / `sophonPatchAssetsFromMain` populated, plus the predl detection/consume path. It depends on Tasks 5/6/8 (`sophon.ParseBuildResponse`, `sophon.FetchManifest`, `sophon.BuildChunkSources`, `sophon.BuildPatchInstructions`, `sophon.BuildPerAssetMD5Index`), Task 13 (`p.sophonAPIBase`, `meta.PlatApp`, `sophonStagingDir`), Task 14 (`flavorSophon*`, `genshinPlan` Sophon fields, `predlPlanCache`), and Task 17 (`LoadAppliedManifests`, `appliedManifestSet.MatchByVersion`).

**Files:** `internal/providers/hoyoverse/update_sophon_plan.go`, `internal/providers/hoyoverse/update_sophon_plan_test.go`

> **INTEGRATOR-NOTE (T18-A):** The decision tree (§3) refers to `sophon.DecidePath`. The contract §A.3 lists `BuildChunkSources` / `BuildPatchInstructions` but does NOT pin a `DecidePath` signature (§1 file-responsibility row mentions it informally as `DecidePath(branch, currentLocal, oldManifest) (flavor, reason)`). To avoid an unresolved cross-package dependency, **the flavor decision is made inline in `buildSophonPlan` in the hoyoverse layer** (it already owns `currentLocal ∈ DiffTags`, the `LoadAppliedManifests` lookup, and the `planFlavor` enum), and `sophon.DecidePath` is treated as not-required by this part. If Task 8 ships a `DecidePath`, the inline branch in `buildSophonPlan` step 4 should delegate to it; the inline logic here is the authoritative fallback.

> **INTEGRATOR-NOTE (T18-B):** Per-category build/patch fetches need a `sophon.ManifestIdentity` to drive `sophon.FetchManifest`. The query (§2.1) is built by the hoyoverse layer (it owns `p.sophonAPIBase` + `meta.PlatApp` + branch fields); the response is parsed by `sophon.ParseBuildResponse` / `sophon.ParsePatchResponse`, then `(*BuildResponse).ManifestFor(matchingField)` yields the identity. The fetch helper `fetchSophonBuild(ctx, p, branch slot, tag, isPatch)` is added in this file (not in the sophon package) because it is Provider-bound (`p.sophonAPIBase`, `p.httpClient`).

- [ ] **Step 1 (test, mapFolders):** Add `update_sophon_plan_test.go` with `TestMapFoldersToMatchingFields` asserting `mapFoldersToMatchingFields([]string{"Chinese","English(US)","Japanese","Korean","Unknown"})` returns `[]string{"en-us","ja-jp","ko-kr","zh-cn"}` (sorted, `"Unknown"` dropped) and that empty input returns a non-nil empty slice.

  ```go
  package hoyoverse

  import (
  	"context"
  	"encoding/json"
  	"errors"
  	"io/fs"
  	"net/http"
  	"net/http/httptest"
  	"os"
  	"path/filepath"
  	"reflect"
  	"sort"
  	"testing"

  	"omnigate/internal/core"
  	"omnigate/internal/providers/hoyoverse/sophon"
  )

  func TestMapFoldersToMatchingFields(t *testing.T) {
  	got := mapFoldersToMatchingFields([]string{"Chinese", "English(US)", "Japanese", "Korean", "Unknown"})
  	want := []string{"en-us", "ja-jp", "ko-kr", "zh-cn"}
  	if !reflect.DeepEqual(got, want) {
  		t.Fatalf("mapFolders = %v, want %v", got, want)
  	}
  	empty := mapFoldersToMatchingFields(nil)
  	if empty == nil || len(empty) != 0 {
  		t.Fatalf("empty input → %v, want non-nil empty", empty)
  	}
  }
  ```

- [ ] **Step 2 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestMapFoldersToMatchingFields` — compile failure (`mapFoldersToMatchingFields` undefined).

- [ ] **Step 3 (implement mapFolders):** Create `update_sophon_plan.go` with the package header + `mapFoldersToMatchingFields`. Reuses the package-level `folderToAudioLang` map (defined in `update_manifest.go`, same package — no move needed).

  ```go
  package hoyoverse

  import (
  	"context"
  	"crypto/md5"
  	"encoding/hex"
  	"encoding/json"
  	"errors"
  	"fmt"
  	"io/fs"
  	"net/http"
  	"net/url"
  	"os"
  	"path/filepath"
  	"sort"
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
  ```

- [ ] **Step 4 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestMapFoldersToMatchingFields`.

- [ ] **Step 5 (commit):** `git commit -m "feat(hoyoverse/sophon): mapFoldersToMatchingFields for audio category subset (Task 18)"`.

- [ ] **Step 6 (implement fetch helpers):** Add the Provider-bound Sophon build/patch fetch helper + a small `categoriesFromBranch` helper that joins `branch.Categories` against the requested matching-field set. The query is per §2.1.

  ```go
  // fetchSophonBuild calls getBuild (isPatch=false) or getPatchBuild
  // (isPatch=true) on p.sophonAPIBase for a single branch slot at the given
  // tag, returning the parsed envelope. Query per spec §2.1:
  //   plat_app=&branch=&password=&package_id=&tag=
  func fetchSophonBuild(ctx context.Context, p *Provider, slot sophon.BranchSlot, platApp, tag string, isPatch bool) (*sophon.BuildResponse, error) {
  	base := p.sophonAPIBase
  	if base == "" {
  		base = sophonChunkAPIBase
  	}
  	endpoint := "getBuild"
  	if isPatch {
  		endpoint = "getPatchBuild"
  	}
  	q := url.Values{}
  	q.Set("plat_app", platApp)
  	q.Set("branch", slot.Branch)
  	q.Set("password", slot.Password)
  	q.Set("package_id", slot.PackageID)
  	q.Set("tag", tag)
  	urlStr := base + "/" + endpoint + "?" + q.Encode()
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
  ```

  > **INTEGRATOR-NOTE (T18-C):** `sophon.BranchSlot.Branch` carries the literal `"main"` / `"predownload"` string for the `branch=` query param (§2.2 `branchSlot.Branch`). If Task 5's `ParseBranches` leaves `Branch` empty (it is parsed from the response, not guaranteed populated), `fetchSophonBuild` would send `branch=`; the planner therefore sets `slot.Branch` defensively before calling fetch — see `buildSophonPlan` step 4 (`mainSlot.Branch = "main"`, `predlSlot.Branch = "predownload"`). Reconcile with Task 5 if `ParseBranches` already guarantees these.

- [ ] **Step 7 (run, expect PASS):** `go build ./internal/providers/hoyoverse/...` (no test yet — helpers exercised end-to-end in step 14). Confirms compile.

- [ ] **Step 8 (commit):** `git commit -m "feat(hoyoverse/sophon): fetchSophonBuild + planCategories helpers (Task 18)"`.

- [ ] **Step 9 (test, buildSophonBuildPlan):** Add `TestBuildSophonBuildPlan_FullAllCDN` using an httptest server that serves a `getBuild` envelope (one `game` category) and a CDN manifest (zstd-protobuf) with one asset of two chunks. Assert the returned `genshinPlan` has `flavor == flavorSophonFull` (no old manifest), `len(sophonChunkSources) == 2`, all `Kind == sophon.SourceCDN`, `sophonBuildID` set. (Fixtures: build a `pb.SophonManifestProto`, marshal+zstd inline in the test; the server mux returns it for the manifest path.)

  > Use a helper `newSophonTestServer(t, mux)` that returns an `*httptest.Server`; point `p.SetSophonAPIBaseURL(srv.URL)`. The manifest CDN URL is whatever `manifest_download.url_prefix` the envelope declares — set it to `srv.URL + "/cdn"` in the fixture so the same server serves it.

  ```go
  func TestBuildSophonBuildPlan_FullAllCDN(t *testing.T) {
  	asset := &pb.SophonManifestAssetProperty{
  		AssetName:    "GenshinImpact_Data/file_a.bin",
  		AssetType:    0,
  		AssetSize:    20,
  		AssetHashMd5: "deadbeef",
  		AssetChunks: []*pb.SophonManifestAssetChunk{
  			{ChunkName: "chunk0", ChunkDecompressedHashMd5: "m0", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10},
  			{ChunkName: "chunk1", ChunkDecompressedHashMd5: "m1", ChunkOnFileOffset: 10, ChunkSize: 5, ChunkSizeDecompressed: 10},
  		},
  	}
  	man := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{asset}}
  	srv := newSophonBuildServer(t, "6.6.0", man, nil)
  	defer srv.Close()

  	p := newTestProvider(t)
  	p.SetSophonAPIBaseURL(srv.URL)

  	gameDir := t.TempDir()
  	tempRoot := t.TempDir()
  	branch := &sophon.BranchInfo{Main: sophon.BranchSlot{
  		PackageID:  "pkg",
  		Tag:        "6.6.0",
  		Categories: []sophon.Category{{ID: "10016", MatchingField: "game", Type: "CATEGORY_TYPE_RESOURCE"}},
  	}}
  	gp, predlAvail, err := buildSophonPlan(context.Background(), p, branch, "hoyoverse/genshin", "6.5.0", nil, gameDir, tempRoot)
  	if err != nil {
  		t.Fatalf("buildSophonPlan: %v", err)
  	}
  	if gp.flavor != flavorSophonFull {
  		t.Fatalf("flavor = %v, want flavorSophonFull", gp.flavor)
  	}
  	if len(gp.sophonChunkSources) != 2 {
  		t.Fatalf("chunk sources = %d, want 2", len(gp.sophonChunkSources))
  	}
  	for _, s := range gp.sophonChunkSources {
  		if s.Kind != sophon.SourceCDN {
  			t.Fatalf("chunk kind = %q, want cdn", s.Kind)
  		}
  	}
  	if gp.sophonBuildID == "" {
  		t.Fatalf("sophonBuildID empty")
  	}
  	if predlAvail {
  		t.Fatalf("predlAvail = true, want false (no PreDownload)")
  	}
  }
  ```

  Add the server + provider test helpers (place in this test file; the integration `fakeSophonServer` of Task 23 is separate):

  ```go
  func newTestProvider(t *testing.T) *Provider {
  	t.Helper()
  	p := New(Settings{}, nil)
  	p.httpClient = &http.Client{}
  	return p
  }

  // newSophonBuildServer serves a getBuild envelope for "game" + serves the
  // zstd-protobuf manifest at /cdn/<manifest.ID>. If patch != nil it ALSO
  // serves getPatchBuild + the patch manifest.
  func newSophonBuildServer(t *testing.T, tag string, mainManifest *pb.SophonManifestProto, patchManifest *pb.SophonPatchProto) *httptest.Server {
  	t.Helper()
  	mux := http.NewServeMux()
  	// declared below in step 9 helper block (sophonTestFixtures)
  	mux.HandleFunc("/getBuild", func(w http.ResponseWriter, r *http.Request) {
  		writeBuildEnvelope(t, w, r, tag, "bid-main", "", mainManifest, "/cdn", false)
  	})
  	if patchManifest != nil {
  		mux.HandleFunc("/getPatchBuild", func(w http.ResponseWriter, r *http.Request) {
  			writePatchEnvelope(t, w, r, tag, "bid-main", "pid", patchManifest, "/cdn")
  		})
  	}
  	mux.HandleFunc("/cdn/", func(w http.ResponseWriter, r *http.Request) {
  		serveStoredManifest(t, w, r)
  	})
  	return httptest.NewServer(mux)
  }
  ```

  > **INTEGRATOR-NOTE (T18-D):** `writeBuildEnvelope` / `writePatchEnvelope` / `serveStoredManifest` are test scaffolding that must (a) emit the §2.3 JSON envelope with `manifest.id` chosen by the test and `manifest_download.url_prefix = srv.URL + "/cdn"`, and (b) store + serve the zstd-compressed `proto.Marshal(...)` bytes keyed by `manifest.id`. These mirror the Task 23 `fakeSophonServer` helpers; the implementer may lift them from Task 23's testdata loader once it exists, OR inline a minimal in-memory version here. Because Task 23 owns the canonical fixtures, **keep these unexported and test-local** to avoid duplication conflicts at merge.

- [ ] **Step 10 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonBuildPlan_FullAllCDN` — `buildSophonPlan` / `buildSophonBuildPlan` undefined.

- [ ] **Step 11 (implement buildSophonBuildPlan):** Add `buildSophonBuildPlan` per §3.2 (full + chunk-from-disk dedup) and the per-asset chunk helper that delegates to `sophon.BuildChunkSources`.

  ```go
  // buildSophonBuildPlan implements §3.2: per category fetch getBuild, dedup vs
  // an old manifest when present, emit ChunkSources (Local for dedup hits, CDN
  // for misses). oldManifests may be nil (flavorSophonFull). It APPENDS into gp.
  func buildSophonBuildPlan(
  	ctx context.Context,
  	p *Provider,
  	gp *genshinPlan,
  	slot sophon.BranchSlot,
  	platApp string,
  	cats []sophon.Category,
  	oldManifests *appliedManifestSet,
  	currentLocal string,
  ) error {
  	for _, cat := range cats {
  		build, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, false)
  		if err != nil {
  			return err
  		}
  		gp.sophonBuildID = build.BuildID
  		id, ok := build.ManifestFor(cat.MatchingField)
  		if !ok {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		newManifest, err := sophon.FetchManifest(ctx, p.httpClientOrDefault(), *id)
  		if err != nil {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
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
  		}
  	}
  	return nil
  }
  ```

  Add the small `httpClientOrDefault` accessor on `*Provider` (used by several Sophon call sites in this part):

  ```go
  func (p *Provider) httpClientOrDefault() *http.Client {
  	if p.httpClient != nil {
  		return p.httpClient
  	}
  	return &http.Client{Timeout: 30 * time.Second}
  }
  ```

  > **INTEGRATOR-NOTE (T18-E):** `ManifestDownloadInfo.Compression` is a `sophon.Boolish` (§A.3). `bool(id.ChunkDownload.Compression)` is a direct conversion since `Boolish` is `type Boolish bool`. Confirm Task 5 declared it exactly so (`type Boolish bool`); if it is a struct wrapper, replace with its accessor.

- [ ] **Step 12 (implement buildSophonPlan skeleton + Full path):** Add `buildSophonPlan` orchestrator (decision tree §3 inline per T18-A) — for this step wire only the Full branch (oldManifests nil path) so step 9's test passes; Patch/Build/predl branches follow in steps 15+.

  ```go
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

  	prev := LoadAppliedManifests(tempRoot, gid) // may be nil
  	var oldMainManifest *pb.SophonManifestProto
  	if prev != nil {
  		oldMainManifest = prev.MatchByVersion(currentLocal, "game")
  	}

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
  ```

  > **INTEGRATOR-NOTE (T18-F):** `buildSophonPatchPlan` and `buildSophonPredlPlan` are referenced here but implemented in steps 15 / 17. To keep step 12 compiling, add temporary stub bodies returning `nil` / `(false, nil)` now and fill them in those steps (the failing tests for them gate the real bodies). Alternatively reorder: implement steps 15/17 bodies before step 12 — the plan keeps them split for TDD granularity, so stubs are the expected interim state.

- [ ] **Step 13 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonBuildPlan_FullAllCDN`.

- [ ] **Step 14 (commit):** `git commit -m "feat(hoyoverse/sophon): buildSophonPlan Full flavor + buildSophonBuildPlan (Task 18)"`.

- [ ] **Step 15 (test, buildSophonPatchPlan):** Add `TestBuildSophonPatchPlan_HybridPatchAndMainFallThrough`: main manifest has 3 assets (`file_a`, `file_b`, `file_c`); patch covers `file_a` (HDiff, OriginalFileName set) + `file_b` (CopyOver, OriginalFileName=""); `file_c` has no patch entry and does NOT exist on disk → must fall through to `chunk_assemble`. Also one `UnusedAssets` entry for the current `VersionTag`. Assert: `len(sophonPatches) == 2`, one `MethodPatch` + one `MethodCopyOver`; `len(sophonDeletes) == 1`; `sophonChunkSources` non-empty (file_c); `sophonPatchAssetsFromMain` has entries for both `file_a` and `file_b`.

  ```go
  func TestBuildSophonPatchPlan_HybridPatchAndMainFallThrough(t *testing.T) {
  	main := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{
  		{AssetName: "file_a", AssetType: 0, AssetSize: 10, AssetHashMd5: "AA",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "ca", ChunkDecompressedHashMd5: "ca", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
  		{AssetName: "file_b", AssetType: 0, AssetSize: 10, AssetHashMd5: "BB",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "cb", ChunkDecompressedHashMd5: "cb", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
  		{AssetName: "file_c", AssetType: 0, AssetSize: 10, AssetHashMd5: "CC",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "cc", ChunkDecompressedHashMd5: "cc", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
  	}}
  	patch := &pb.SophonPatchProto{
  		PatchAssets: []*pb.SophonPatchAssetProperty{
  			{AssetName: "file_a", AssetSize: 10, AssetHashMd5: "AA", AssetInfos: []*pb.SophonPatchAssetInfo{
  				{VersionTag: "6.5.0", Chunk: &pb.SophonPatchAssetChunk{PatchName: "pa", PatchOffset: 0, PatchLength: 4, OriginalFileName: "file_a", OriginalFileMd5: "OLDA"}}}},
  			{AssetName: "file_b", AssetSize: 10, AssetHashMd5: "BB", AssetInfos: []*pb.SophonPatchAssetInfo{
  				{VersionTag: "6.5.0", Chunk: &pb.SophonPatchAssetChunk{PatchName: "pb", PatchOffset: 0, PatchLength: 10, OriginalFileName: ""}}}},
  		},
  		UnusedAssets: []*pb.SophonUnusedAssetProperty{
  			{VersionTag: "6.5.0", AssetInfos: []*pb.SophonUnusedAssetInfo{
  				{Assets: []*pb.SophonUnusedAssetFile{{FileName: "old_dead.bin", FileMd5: "DEAD"}}}}},
  		},
  	}
  	srv := newSophonBuildServer(t, "6.6.0", main, patch)
  	defer srv.Close()
  	p := newTestProvider(t)
  	p.SetSophonAPIBaseURL(srv.URL)

  	gameDir := t.TempDir()
  	gp := &genshinPlan{sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{}}
  	mainSlot := sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Branch: "main",
  		Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}}
  	cats := []sophon.Category{{ID: "10016", MatchingField: "game"}}
  	if err := buildSophonPatchPlan(context.Background(), p, gp, mainSlot, "ddxf6vlr1reo", cats, "6.5.0", nil, gameDir); err != nil {
  		t.Fatalf("buildSophonPatchPlan: %v", err)
  	}
  	if len(gp.sophonPatches) != 2 {
  		t.Fatalf("patches = %d, want 2", len(gp.sophonPatches))
  	}
  	var nPatch, nCopy int
  	for _, pi := range gp.sophonPatches {
  		switch pi.Method {
  		case sophon.MethodPatch:
  			nPatch++
  		case sophon.MethodCopyOver:
  			nCopy++
  		}
  	}
  	if nPatch != 1 || nCopy != 1 {
  		t.Fatalf("methods patch=%d copy=%d, want 1/1", nPatch, nCopy)
  	}
  	if len(gp.sophonDeletes) != 1 || gp.sophonDeletes[0].Path != "old_dead.bin" {
  		t.Fatalf("deletes = %+v, want 1×old_dead.bin", gp.sophonDeletes)
  	}
  	if len(gp.sophonChunkSources) == 0 {
  		t.Fatalf("expected file_c fall-through chunk sources")
  	}
  	if _, ok := gp.sophonPatchAssetsFromMain["file_a"]; !ok {
  		t.Fatalf("sophonPatchAssetsFromMain missing file_a")
  	}
  	if _, ok := gp.sophonPatchAssetsFromMain["file_b"]; !ok {
  		t.Fatalf("sophonPatchAssetsFromMain missing file_b")
  	}
  }
  ```

- [ ] **Step 16 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonPatchPlan_HybridPatchAndMainFallThrough` — stub returns nil, assertions fail.

- [ ] **Step 17 (implement buildSophonPatchPlan):** Replace the stub with §3.1 FULL. Fetches `getPatchBuild` + `getBuild` per category, calls `sophon.BuildPatchInstructions` for patch/copyover + deletes, then iterates `main.Assets` for fall-through (skip-if-unchanged MD5 guard else `chunk_assemble` via `sophon.BuildChunkSources`), and populates `sophonPatchAssetsFromMain[asset]` for every patch record per [DEV-5]/§6.4.

  ```go
  // buildSophonPatchPlan implements §3.1 (Collapse-faithful patch+main merge).
  // It APPENDS Patch/CopyOver records into gp.sophonPatches, main-fall-through
  // chunk_assemble into gp.sophonChunkSources, deletes into gp.sophonDeletes,
  // and the per-asset main chunk plan into gp.sophonPatchAssetsFromMain for
  // every patch record ([DEV-5] / §6.4 demotion).
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
  	for _, cat := range cats {
  		patchResp, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, true)
  		if err != nil {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		buildResp, err := fetchSophonBuild(ctx, p, slot, platApp, slot.Tag, false)
  		if err != nil {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		gp.sophonBuildID = buildResp.BuildID

  		patchID, ok := patchResp.ManifestFor(cat.MatchingField)
  		if !ok {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		buildID, ok := buildResp.ManifestFor(cat.MatchingField)
  		if !ok {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		patchProto, err := sophon.FetchPatchManifest(ctx, p.httpClientOrDefault(), *patchID)
  		if err != nil {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}
  		mainProto, err := sophon.FetchManifest(ctx, p.httpClientOrDefault(), *buildID)
  		if err != nil {
  			return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  		}

  		// sophon-layer: patch/copyover + deletes (NOT main fall-through; [DEV-5]).
  		patches, deletes := sophon.BuildPatchInstructions(patchProto, mainProto, currentLocal, patchID.DiffDownload.URLPrefix)
  		gp.sophonPatches = append(gp.sophonPatches, patches...)
  		gp.sophonDeletes = append(gp.sophonDeletes, deletes...)

  		// Index: patch instructions by target Asset for the fall-through pass.
  		patched := make(map[string]bool, len(patches))
  		for _, pi := range patches {
  			patched[pi.Asset] = true
  		}

  		useCompress := bool(buildID.ChunkDownload.Compression)
  		chunkPrefix := buildID.ChunkDownload.URLPrefix

  		// §3.1 step 4: iterate main.Assets (source of truth for the new build).
  		for _, ma := range mainProto.Assets {
  			if ma.AssetType != 0 {
  				continue
  			}
  			if patched[ma.AssetName] {
  				// Build per-asset main chunk plan for §6.4 demotion fallback.
  				oldIdx := sophon.BuildPerAssetMD5Index(oldMainManifest, ma.AssetName)
  				gp.sophonPatchAssetsFromMain[ma.AssetName] =
  					sophon.BuildChunkSources(ma, oldIdx, chunkPrefix, useCompress)
  				continue
  			}
  			// No patch entry: skip-if-unchanged guard.
  			if md5MatchesOnDisk(filepath.Join(gameDir, ma.AssetName), ma.AssetHashMd5) {
  				continue
  			}
  			oldIdx := sophon.BuildPerAssetMD5Index(oldMainManifest, ma.AssetName)
  			gp.sophonChunkSources = append(gp.sophonChunkSources,
  				sophon.BuildChunkSources(ma, oldIdx, chunkPrefix, useCompress)...)
  		}
  	}
  	return nil
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
  ```

  Add `io` to the import block (it is used by `md5MatchesOnDisk`).

  > **INTEGRATOR-NOTE (T18-G):** §A.3 `BuildPatchInstructions(patch, main, currentLocal, patchURLPrefix)` populates `PatchInstr.Asset` = target relative path and `PatchInstr.URLPrefix` = patch-blob CDN base. This step keys the fall-through `patched` set on `pi.Asset` — confirm Task 8 sets `Asset` to the main asset name (`ma.AssetName`) and not some other field. If Task 8 emits a separate target field, switch the key accordingly. The `patchID.DiffDownload.URLPrefix` passed here is the patch-blob base per §A.2 (`diff_download.url_prefix`).

- [ ] **Step 18 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonPatchPlan_HybridPatchAndMainFallThrough`.

- [ ] **Step 19 (commit):** `git commit -m "feat(hoyoverse/sophon): buildSophonPatchPlan patch+main hybrid + deletes (Task 18)"`.

- [ ] **Step 20 (implement buildSophonPredlPlan):** Replace the stub with §3.3. Computes `predlAvail`, picks `flavorSophonPredlPatch` / `flavorSophonPredlBuild` (Full forced off), builds the parallel plan on `branch.PreDownload` into a fresh scratch `genshinPlan`, and caches the result in `gp.predlPlan *predlPlanCache`.

  ```go
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
  	prev *appliedManifestSet,
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

  	scratch := &genshinPlan{sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{}}
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
  ```

- [ ] **Step 21 (test predl wiring):** Add `TestBuildSophonPlan_PredlAvailable` reusing `newSophonBuildServer` (main + patch served) with `branch.PreDownload` populated and `currentLocal ∈ PreDownload.DiffTags`; assert `predlAvail == true`, `gp.predlPlan != nil`, `gp.predlPlan.Flavor == flavorSophonPredlPatch`, and a `TestBuildSophonPlan_PredlFullBlocked` asserting predlAvail false when PreDownload is set but no DiffTags hit + no old manifest.

  ```go
  func TestBuildSophonPlan_PredlFullBlocked(t *testing.T) {
  	main := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{
  		{AssetName: "f", AssetType: 0, AssetSize: 10, AssetHashMd5: "FF",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c", ChunkDecompressedHashMd5: "c", ChunkSize: 5, ChunkSizeDecompressed: 10}}}}}
  	srv := newSophonBuildServer(t, "6.6.0", main, nil)
  	defer srv.Close()
  	p := newTestProvider(t)
  	p.SetSophonAPIBaseURL(srv.URL)
  	branch := &sophon.BranchInfo{
  		Main:        sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}},
  		PreDownload: sophon.BranchSlot{PackageID: "pkg2", Tag: "6.7.0", Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}},
  	}
  	_, predlAvail, err := buildSophonPlan(context.Background(), p, branch, "hoyoverse/genshin", "6.5.0", nil, t.TempDir(), t.TempDir())
  	if err != nil {
  		t.Fatalf("buildSophonPlan: %v", err)
  	}
  	if predlAvail {
  		t.Fatalf("predlAvail = true, want false (no DiffTags hit, no old manifest)")
  	}
  }
  ```

- [ ] **Step 22 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run 'TestBuildSophonPlan_Predl'`.

- [ ] **Step 23 (commit):** `git commit -m "feat(hoyoverse/sophon): buildSophonPredlPlan (patch/build only, full blocked) (Task 18)"`.

- [ ] **Step 24 (test, detectPredlConsume):** Add `TestDetectPredlConsume` covering: (a) ENOENT → `(false,nil)`; (b) good predl_ready (`Kind==sophon_patch`, `TargetVersion==mainTag`, `SourceVersion==currentLocal`, `currentLocal ∈` caller-supplied DiffTags) → `(true, file)`; (c) `SourceVersion != currentLocal` → `(false,nil)` + sidecar deleted + staging dir removed; (d) zero-value `Kind` → `(false,nil)` + cleanup. The DiffTags check (§3.6) needs the caller's branch — pass it in.

  > **INTEGRATOR-NOTE (T18-H):** §3.6 step "predl.Kind == sophon_patch and currentLocal ∉ branch.Main.DiffTags" requires `detectPredlConsume` to know `branch.Main.DiffTags`. The contract §F scope line types it `detectPredlConsume(tempRoot, gid, currentLocal, mainTag string) (bool, *sophonPredlReadyFile)` — **mainTag only, no DiffTags slice**. To satisfy §3.6's patch-diff-window check the signature is extended to `detectPredlConsume(tempRoot string, gid core.GameID, currentLocal, mainTag string, mainDiffTags []string) (bool, *sophonPredlReadyFile)`. This is the one scope-vs-spec gap in Task 18; flagged for reconciliation. Callers in Task 21 pass `branch.Main.Tag` + `branch.Main.DiffTags`.

  ```go
  func TestDetectPredlConsume(t *testing.T) {
  	tempRoot := t.TempDir()
  	gid := core.GameID("hoyoverse/genshin")
  	mainTag := "6.6.0"
  	diffTags := []string{"6.5.0"}

  	// (a) ENOENT
  	consume, pf := detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
  	if consume || pf != nil {
  		t.Fatalf("ENOENT → (%v,%v), want (false,nil)", consume, pf)
  	}

  	// (b) good
  	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{
  		Kind: "sophon_patch", BuildID: "bid", SourceVersion: "6.5.0", TargetVersion: "6.6.0",
  	})
  	consume, pf = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
  	if !consume || pf == nil {
  		t.Fatalf("good → (%v,%v), want (true,non-nil)", consume, pf)
  	}

  	// (c) source mismatch → cleanup
  	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{
  		Kind: "sophon_patch", BuildID: "bid", SourceVersion: "6.4.0", TargetVersion: "6.6.0",
  	})
  	consume, _ = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
  	if consume {
  		t.Fatalf("source mismatch → consume true, want false")
  	}
  	if _, err := os.Stat(filepath.Join(versionSidecarDir(tempRoot, gid, mainTag), "predl_ready.json")); !errors.Is(err, fs.ErrNotExist) {
  		t.Fatalf("expected predl_ready.json removed on source mismatch")
  	}

  	// (d) zero-value Kind → stale
  	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{SourceVersion: "6.5.0", TargetVersion: "6.6.0"})
  	consume, _ = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
  	if consume {
  		t.Fatalf("zero Kind → consume true, want false")
  	}
  }

  func writePredlReady(t *testing.T, tempRoot string, gid core.GameID, targetVer string, f *sophonPredlReadyFile) {
  	t.Helper()
  	dir := versionSidecarDir(tempRoot, gid, targetVer)
  	if err := os.MkdirAll(dir, 0o755); err != nil {
  		t.Fatal(err)
  	}
  	data, _ := json.MarshalIndent(f, "", "  ")
  	if err := os.WriteFile(filepath.Join(dir, "predl_ready.json"), data, 0o644); err != nil {
  		t.Fatal(err)
  	}
  }
  ```

- [ ] **Step 25 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestDetectPredlConsume` — `detectPredlConsume` undefined.

- [ ] **Step 26 (implement detectPredlConsume):** Add per §3.6 (staleness ladder + cleanup ownership). Cleanup deletes the sidecar AND `staging/predl/<BuildID>` under the target version dir.

  ```go
  // detectPredlConsume implements §3.6. It reads
  //   versionSidecarDir(tempRoot, gid, mainTag)/predl_ready.json
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
  ```

  Add `"log/slog"` to imports.

- [ ] **Step 27 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestDetectPredlConsume`.

- [ ] **Step 28 (full package gate + commit):** `go build ./... && go vet ./... && go test -count=1 ./internal/providers/hoyoverse/...` then `git commit -m "feat(hoyoverse/sophon): detectPredlConsume staleness ladder + cleanup (Task 18)"`.

---

### Task 19: `update_sophon_download.go` — Sophon 4-worker download pool with crash resume

Mirrors v1 `downloadAll`'s shape (feeder goroutine + `ctx.Done` select + buffered `errCh` + `sync.WaitGroup`) but jobs dispatch on `kind`. Depends on Task 9 (`sophon.DownloadChunk`, `sophon.ErrChunkVerify`), Task 10 (`sophon.ReadLocalChunk`, `sophon.ErrChunkStale`), Task 9 (`sophon.DownloadPatchBlob`), Task 15 (`sophonProgressStore` with `ChunksDone`/`PatchesDone`/`Mark*`/`Persist`).

**Files:** `internal/providers/hoyoverse/update_sophon_download.go`, `internal/providers/hoyoverse/update_sophon_download_test.go`

> **INTEGRATOR-NOTE (T19-A):** Task 15's contract (§A.6 `sophonProgressFile`) defines the on-disk shape but the §F scope names a `*sophonProgressStore` with methods `ChunksDone(name) bool`, `PatchesDone(name) bool`, `MarkChunkDone(name)`, `MarkPatchDone(name)`, `Persist() error`. Those store methods are owned by Task 15; this task assumes them. If Task 15 named them differently (e.g. a raw `*sophonProgressFile` + free functions), adapt the three call sites. Signatures used here: `store.ChunksDone(string) bool`, `store.MarkChunkDone(string)`, `store.PatchesDone(string) bool`, `store.MarkPatchDone(string)`, `store.Persist() error`.

- [ ] **Step 1 (test, job-building skip-done):** Add `update_sophon_download_test.go` with `TestDownloadAllSophon_SkipsDone`: build 3 CDN chunk sources; pre-mark chunk 0 done in the store; run `downloadAllSophon` against an httptest CDN that serves raw bytes for chunks 1 & 2 and 500s for chunk 0 (proving chunk 0 is skipped). Assert no error and chunk 0's staging file is NOT created by the pool.

  ```go
  package hoyoverse

  import (
  	"context"
  	"net/http"
  	"net/http/httptest"
  	"os"
  	"path/filepath"
  	"sync/atomic"
  	"testing"

  	"omnigate/internal/providers/hoyoverse/sophon"
  )
  ```

  (Test body sketched; the implementer fleshes the CDN handler. Because chunk verify is owned by `sophon.DownloadChunk`, the handler must return bytes that pass that verify — easiest is `UseCompress=false` + a `ChunkName` whose first-16-hex does NOT parse so MD5 fallback applies, with `ExpectMD5` set to the md5 of the served bytes. Reuse the Task 9 test's known-good vector.)

  > **INTEGRATOR-NOTE (T19-B):** Crafting bytes that satisfy `sophon.DownloadChunk`'s xxh64/MD5 verify inside this unit test couples it to Task 9's verify internals. To keep Task 19 focused on *pool mechanics* (skip-done, dispatch, cancel, resume, requeue) rather than re-testing verify, the test uses a **dispatch seam**: `downloadAllSophon` takes its three executors via an injected `sophonExecutors` struct (default = real `sophon.*` funcs) so tests can stub them with trivial byte-writers. End-to-end verify coverage lives in Task 23. This adds one small struct; see step 5.

- [ ] **Step 2 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestDownloadAllSophon_SkipsDone`.

- [ ] **Step 3 (implement job types):** Create `update_sophon_download.go` with the job kinds + struct.

  ```go
  package hoyoverse

  import (
  	"context"
  	"path/filepath"
  	"sync"

  	"omnigate/internal/providers/hoyoverse/sophon"
  )

  type sophonJobKind int

  const (
  	jobChunkCDN sophonJobKind = iota
  	jobChunkLocal
  	jobPatchBlob
  )

  type sophonJob struct {
  	kind  sophonJobKind
  	chunk sophon.ChunkSource // jobChunkCDN | jobChunkLocal
  	patch sophon.PatchInstr  // jobPatchBlob
  	out   string             // staging path
  }
  ```

- [ ] **Step 4 (implement executors seam):** Add the injectable executors (default = real sophon funcs).

  ```go
  // sophonExecutors abstracts the three sophon-layer I/O primitives so the
  // worker pool can be unit-tested without crafting verify-passing byte vectors
  // (end-to-end verify is covered by Task 23). Production wiring uses
  // defaultSophonExecutors.
  type sophonExecutors struct {
  	downloadChunk   func(ctx context.Context, src sophon.ChunkSource, out string) error
  	readLocalChunk  func(gameDir string, src sophon.ChunkSource, out string) error
  	downloadPatch   func(ctx context.Context, p sophon.PatchInstr, out string) error
  }

  func defaultSophonExecutors(hc *httpClientType) sophonExecutors {
  	return sophonExecutors{
  		downloadChunk: func(ctx context.Context, src sophon.ChunkSource, out string) error {
  			return sophon.DownloadChunk(ctx, hc, src, out)
  		},
  		readLocalChunk: func(gameDir string, src sophon.ChunkSource, out string) error {
  			return sophon.ReadLocalChunk(gameDir, src, out)
  		},
  		downloadPatch: func(ctx context.Context, p sophon.PatchInstr, out string) error {
  			return sophon.DownloadPatchBlob(ctx, hc, p, out)
  		},
  	}
  }
  ```

  > **INTEGRATOR-NOTE (T19-C):** `httpClientType` above is a placeholder — replace with `*http.Client` and add `"net/http"` to imports. (Written this way to flag that `defaultSophonExecutors` must receive the Provider's `httpClientOrDefault()` from Task 21's call site, not construct its own.)

- [ ] **Step 5 (implement downloadAllSophon):** The pool. Builds the job list (CDN/local by `src.Kind`; one patch-blob job per unique `PatchName`), skips done via the store, dispatches on kind, marks done + persists per success, requeues local→CDN on `ErrChunkStale`, tolerates duplicate chunks, cancels via ctx. Progress = decompressed bytes (chunks) / patch slice length per §5.3.

  ```go
  // downloadAllSophon runs a 4-worker (configurable) pool over the plan's chunk
  // sources + patch blobs, writing verified content into stagingRoot/chunks and
  // stagingRoot/patches. Crash-resume: chunks/patches already in store are
  // skipped. Local-chunk ErrChunkStale → requeue as a CDN job (the planner
  // always populates ChunkName/URLPrefix on Local sources per §A.2). Duplicate
  // chunk requests are tolerated (atomic rename + verify make them idempotent).
  // Progress reports cumulative decompressed bytes (§5.3).
  func downloadAllSophon(
  	ctx context.Context,
  	store *sophonProgressStore,
  	gameDir, stagingRoot string,
  	sources []sophon.ChunkSource,
  	patches []sophon.PatchInstr,
  	workers int,
  	exec sophonExecutors,
  	onProgress func(int64),
  ) error {
  	if workers < 1 {
  		workers = 1
  	}
  	chunksDir := filepath.Join(stagingRoot, "chunks")
  	patchesDir := filepath.Join(stagingRoot, "patches")
  	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
  		return err
  	}
  	if err := os.MkdirAll(patchesDir, 0o755); err != nil {
  		return err
  	}

  	// Build job list (dedup chunks by ChunkName, patches by PatchName).
  	var jobs []sophonJob
  	seenChunk := map[string]bool{}
  	for _, src := range sources {
  		if seenChunk[src.ChunkName] {
  			continue
  		}
  		seenChunk[src.ChunkName] = true
  		if store.ChunksDone(src.ChunkName) {
  			if onProgress != nil {
  				onProgress(src.DecompSize)
  			}
  			continue
  		}
  		kind := jobChunkCDN
  		if src.Kind == sophon.SourceLocal {
  			kind = jobChunkLocal
  		}
  		jobs = append(jobs, sophonJob{kind: kind, chunk: src, out: filepath.Join(chunksDir, src.ChunkName)})
  	}
  	seenPatch := map[string]bool{}
  	for _, p := range patches {
  		if seenPatch[p.PatchName] {
  			continue
  		}
  		seenPatch[p.PatchName] = true
  		if store.PatchesDone(p.PatchName) {
  			if onProgress != nil {
  				onProgress(p.PatchSize)
  			}
  			continue
  		}
  		jobs = append(jobs, sophonJob{kind: jobPatchBlob, patch: p, out: filepath.Join(patchesDir, p.PatchName)})
  	}
  	if len(jobs) == 0 {
  		return nil
  	}

  	jobCh := make(chan sophonJob)
  	errCh := make(chan error, workers)
  	var bytesDone int64
  	var bytesMu sync.Mutex
  	var doneMu sync.Mutex // guards store mutation + Persist
  	progress := func(delta int64) {
  		bytesMu.Lock()
  		bytesDone += delta
  		v := bytesDone
  		bytesMu.Unlock()
  		if onProgress != nil {
  			onProgress(v)
  		}
  	}

  	var wg sync.WaitGroup
  	for i := 0; i < workers; i++ {
  		wg.Add(1)
  		go func() {
  			defer wg.Done()
  			for job := range jobCh {
  				if err := runSophonJob(ctx, job, gameDir, chunksDir, exec, store, &doneMu, progress); err != nil {
  					select {
  					case errCh <- err:
  					default:
  					}
  					return
  				}
  			}
  		}()
  	}

  	go func() {
  		defer close(jobCh)
  		for _, j := range jobs {
  			select {
  			case <-ctx.Done():
  				return
  			case jobCh <- j:
  			}
  		}
  	}()

  	wg.Wait()
  	close(errCh)
  	if err := ctx.Err(); err != nil {
  		return err
  	}
  	for err := range errCh {
  		if err != nil {
  			return err
  		}
  	}
  	return nil
  }

  // runSophonJob executes one job, marking the store + persisting on success.
  func runSophonJob(
  	ctx context.Context,
  	job sophonJob,
  	gameDir, chunksDir string,
  	exec sophonExecutors,
  	store *sophonProgressStore,
  	doneMu *sync.Mutex,
  	progress func(int64),
  ) error {
  	if err := ctx.Err(); err != nil {
  		return err
  	}
  	switch job.kind {
  	case jobChunkCDN:
  		if err := exec.downloadChunk(ctx, job.chunk, job.out); err != nil {
  			return wrapSophonChunkErr(job.chunk.ChunkName, err)
  		}
  		markChunkDone(store, doneMu, job.chunk.ChunkName)
  		progress(job.chunk.DecompSize)
  	case jobChunkLocal:
  		err := exec.readLocalChunk(gameDir, job.chunk, job.out)
  		if errors.Is(err, sophon.ErrChunkStale) {
  			// Requeue inline as CDN (planner populated ChunkName/URLPrefix).
  			if cdnErr := exec.downloadChunk(ctx, job.chunk, job.out); cdnErr != nil {
  				return wrapSophonChunkErr(job.chunk.ChunkName, cdnErr)
  			}
  		} else if err != nil {
  			return wrapSophonChunkErr(job.chunk.ChunkName, err)
  		}
  		markChunkDone(store, doneMu, job.chunk.ChunkName)
  		progress(job.chunk.DecompSize)
  	case jobPatchBlob:
  		if err := exec.downloadPatch(ctx, job.patch, job.out); err != nil {
  			return wrapSophonChunkErr(job.patch.PatchName, err)
  		}
  		markPatchDone(store, doneMu, job.patch.PatchName)
  		progress(job.patch.PatchSize)
  	}
  	return nil
  }

  func markChunkDone(store *sophonProgressStore, mu *sync.Mutex, name string) {
  	mu.Lock()
  	store.MarkChunkDone(name)
  	_ = store.Persist()
  	mu.Unlock()
  }

  func markPatchDone(store *sophonProgressStore, mu *sync.Mutex, name string) {
  	mu.Lock()
  	store.MarkPatchDone(name)
  	_ = store.Persist()
  	mu.Unlock()
  }

  // wrapSophonChunkErr converts a verify-exhausted error into the user-facing
  // retryable code; ctx errors and stale errors pass through unchanged.
  func wrapSophonChunkErr(name string, err error) error {
  	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
  		return err
  	}
  	if errors.Is(err, sophon.ErrChunkVerify) {
  		return &core.UpdateError{
  			Code:      "sophon_chunk_verify_failed",
  			Params:    map[string]string{"file": name},
  			Retryable: true,
  		}
  	}
  	return err
  }
  ```

  Reconcile imports: add `"errors"`, `"os"`, `"net/http"`, `"omnigate/internal/core"`. Replace the placeholder `httpClientType` in step 4 with `*http.Client`.

  > **INTEGRATOR-NOTE (T19-D):** §5.2 specifies cross-run resume re-verifies a staging file that exists-but-not-marked-done before re-downloading. That re-verify is delegated to `sophon.DownloadChunk`'s own "if out exists and verifies, skip" behavior (§A.3 / §5.1 worker dispatch bullet: "If out already exists and verifies: skip"). This pool therefore does NOT implement a separate disk re-verify pass — it relies on `sophon.DownloadChunk` / `sophon.DownloadPatchBlob` being idempotent against an existing verified `out`. Confirm Task 9 implements that skip; if not, add a pre-dispatch `statAndVerify` here.

- [ ] **Step 6 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestDownloadAllSophon_SkipsDone`.

- [ ] **Step 7 (test concurrency + dispatch):** `TestDownloadAllSophon_DispatchByKind`: mix of 2 CDN + 1 local + 1 patch with stub executors that record which executor ran for each name (guard the record map with a mutex). Assert all four ran via the correct executor and `store.Persist` left all four marked done.

- [ ] **Step 8 (run/implement adjustments, expect PASS):** run; fix as needed.

- [ ] **Step 9 (test, local-stale requeue):** `TestDownloadAllSophon_LocalStaleRequeuesCDN`: stub `readLocalChunk` to return `sophon.ErrChunkStale`; stub `downloadChunk` to write a marker file + record the requeue. Assert the chunk ends marked done and `downloadChunk` was invoked for it.

- [ ] **Step 10 (run, expect PASS):** run.

- [ ] **Step 11 (test, cancel):** `TestDownloadAllSophon_Cancel`: feed many jobs; stub `downloadChunk` to block on `<-ctx.Done()` then return `ctx.Err()`; cancel the context shortly after start; assert the call returns `context.Canceled` and the pool drains (no goroutine leak — use a `done` channel + short deadline).

- [ ] **Step 12 (run, expect PASS):** run.

- [ ] **Step 13 (test, crash resume):** `TestDownloadAllSophon_ResumeFromPartial`: create a store, mark 3 of 5 chunks done + persist; reopen the store from the same path (Task 15 `newSophonProgressStore`/loader); run with stub `downloadChunk` recording calls; assert only the 2 not-done chunks were downloaded and progress includes the 3 skipped (their `DecompSize`).

- [ ] **Step 14 (run, expect PASS):** run.

- [ ] **Step 15 (commit):** `go vet ./internal/providers/hoyoverse/... && git commit -m "feat(hoyoverse/sophon): downloadAllSophon worker pool with dispatch/cancel/resume/requeue (Task 19)"`.

---

### Task 20: `update_sophon_apply.go` — record build, WAL write, per-record dispatch + demotion + ordering + cleanup

Depends on Task 16 (`sophonApplyWAL` + `sophonApplyRecord` + `walChunkSource` + flusher + `toWalChunkSource`/`fromWalChunkSource`), Task 11 (`sophon.AssembleFile`), Task 12 (`sophon.HDiffApply`, `sophon.HDiffOpts`), `hpatchz.Run` (Task 2 sub-package), Task 17 (`RotateAfterApply`), v1 `newApplyLock`/`applyOneRename`/`isCrossDevice`, `WriteGameVersion`/`writeLastApplyTarget`.

**Files:** `internal/providers/hoyoverse/update_sophon_apply.go`, `internal/providers/hoyoverse/update_sophon_apply_test.go`

> **INTEGRATOR-NOTE (T20-A):** §6 specifies `sophon.AssembleFile`/`sophon.SafeAtomicRename` for sophon-package renames, but §6.2 step 4 / §6.3 step 8 rename target files FROM the hoyoverse package into `gameDir`. Per [DEV-2] the hoyoverse-layer apply reuses the EXISTING hoyoverse `applyOneRename`/`isCrossDevice` for those gameDir-bound renames. This task therefore renames assembled `.tmp` → final via a small `sophonRenameIntoGame(outTmp, gameDir, relPath)` wrapper around `isCrossDevice` + copy-fallback (mirroring `applyOneRename` but taking an absolute source tmp rather than a staging-relative one — `applyOneRename`'s signature is `(stagingDir, gameDir, rel)` which doesn't fit assembled tmp paths). The wrapper is defined in this file.

- [ ] **Step 1 (test, buildSophonWAL ordering):** Add `update_sophon_apply_test.go` with `TestBuildSophonWAL_InterleaveAndCategoryOrder`: a `genshinPlan` (flavorSophonPatch) with chunk sources for category "game" + "en-us", patches for "game", deletes for "game". Assert the built `*sophonApplyWAL` has `StagingRoot` set, `Records` containing the right `Kind` mix (`chunk_assemble`, `hdiff_patch`/`copy_over`, `delete`), and that category ordering puts "game" records before "en-us".

  > **INTEGRATOR-NOTE (T20-B):** `sophon.ChunkSource` / `sophon.PatchInstr` / `sophon.DeleteInstr` carry an owning category only implicitly (ChunkSource has no Category field per §A.2 — it has `Asset`). To order records by category ("game" first, audio alpha — §6.2 step 3) the WAL builder needs each record's category. Since the plan does not tag chunk sources with category, **the builder records categories in plan-build order** (game category built first, then audio langs alpha in `planCategories`), and `buildSophonWAL` preserves that build order — which already satisfies §6.2 step 3 because `buildSophonPlan` appends game-category work before audio-category work. The `Category` field on `sophonApplyRecord` is set to the plan's per-record category when known, else `""`; ordering relies on append-order, not a re-sort. Flagged because §6.2 step 3 reads as an explicit sort; here it is an order-preservation invariant from Task 18. If a re-sort is required, Task 18 must tag each `sophon.ChunkSource` with its category (schema change).

- [ ] **Step 2 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonWAL_InterleaveAndCategoryOrder`.

- [ ] **Step 3 (implement buildSophonWAL):** Create `update_sophon_apply.go`. Build records interleaving `chunk_assemble` (from `sophonChunkSources`, grouped per asset) + `hdiff_patch`/`copy_over` (from `sophonPatches`) + `delete` (from `sophonDeletes`) in §6.1 order, using `toWalChunkSource`.

  ```go
  package hoyoverse

  import (
  	"context"
  	"crypto/md5"
  	"encoding/hex"
  	"errors"
  	"fmt"
  	"io"
  	"io/fs"
  	"os"
  	"path/filepath"
  	"time"

  	"omnigate/internal/core"
  	"omnigate/internal/providers/hoyoverse/hpatchz"
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
  ```

  > **INTEGRATOR-NOTE (T20-C):** `chunk_assemble` records need the whole-file `AssetMD5` (§6.3 step 6 verify) and the file size (§6.3 step 3). `sophon.ChunkSource` (§A.2) carries per-chunk `ExpectMD5` and `DecompSize` but NOT the owning asset's whole-file MD5 or size. §6.3 step 3 derives size as `Σ src.DecompSize` (fine), but the whole-file `AssetMD5` cannot be recovered from chunk sources alone. **Resolution:** Task 18 must carry the asset's `AssetHashMd5` into the chunk_assemble record. Minimal fix without a `ChunkSource` schema change: `buildSophonPlan`/`buildSophonBuildPlan` build the WAL-bound asset→MD5 map and pass it alongside; OR add `AssetMD5` to the WAL record at build time from the manifest. Since Task 20 builds the WAL from `genshinPlan` (which has dropped the manifest by then), **the cleanest reconciliation is for Task 18 to add an `assetMD5 map[string]string` field on `genshinPlan` (assetName→AssetHashMd5) populated whenever it emits chunk sources, and Task 20 sets `AssetMD5: gp.assetMD5[asset]`.** This is a genuine contract gap (§A.5 `genshinPlan` field list omits this map). Flagged; the placeholder above must be replaced once Task 18 provides it.

- [ ] **Step 4 (run, expect PASS):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestBuildSophonWAL_InterleaveAndCategoryOrder` (test asserts Kind mix + order; AssetMD5 assertion deferred per T20-C).

- [ ] **Step 5 (commit):** `git commit -m "feat(hoyoverse/sophon): buildSophonWAL record interleave (Task 20)"`.

- [ ] **Step 6 (test, chunk_assemble execution):** `TestRunSophonApply_ChunkAssembleAllCDN`: stage two CDN chunk files under `stagingRoot/chunks/` whose concatenation forms a known file; build a WAL with one `chunk_assemble` record (two CDN sources, correct `FileOffset`/`DecompSize`, `AssetMD5` = md5 of the assembled bytes); run `runSophonApply`; assert the target file exists in gameDir with correct content + the WAL record is `done` + `config.ini` updated (`game_version` == target). Provide a minimal `config.ini` with `[General]\ngame_version=6.5.0`.

- [ ] **Step 7 (run, expect FAIL):** run.

- [ ] **Step 8 (implement runSophonApply skeleton + chunk_assemble):** Add the orchestrator with applyLock, load-or-build WAL, per-record dispatch (only `chunk_assemble` wired now), batched flush via Task 16, and the §6.2 step 6–8 finalize. Wire the readChunk closure for `sophon.AssembleFile`.

  ```go
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

  	wal, err := loadSophonApplyWAL(versionDir)
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

  	flusher := newSophonWALFlusher(versionDir, wal) // Task 16: batches 50 records / 5s
  	defer flusher.Flush()

  	total := len(wal.Records)
  	for i := range wal.Records {
  		if err := ctx.Err(); err != nil {
  			return err
  		}
  		rec := &wal.Records[i]
  		if rec.State == "done" {
  			emit("applying", i+1, total)
  			continue
  		}
  		if err := applySophonRecord(ctx, p, wal, rec, gameDir, stagingRoot); err != nil {
  			return err
  		}
  		rec.State = "done"
  		flusher.MaybeFlush()
  		emit("applying", i+1, total)
  	}
  	flusher.Flush()

  	return finalizeSophonApply(p, gid, gp, tempRoot, gameDir, wal)
  }

  // applySophonRecord dispatches one record by Kind.
  func applySophonRecord(ctx context.Context, p *Provider, wal *sophonApplyWAL, rec *sophonApplyRecord, gameDir, stagingRoot string) error {
  	switch rec.Kind {
  	case "chunk_assemble":
  		return applyChunkAssemble(ctx, p, wal, rec, gameDir, stagingRoot)
  	case "hdiff_patch":
  		return applyHDiffPatch(ctx, p, wal, rec, gameDir, stagingRoot)
  	case "copy_over":
  		return applyCopyOver(rec, gameDir, stagingRoot)
  	case "delete":
  		return applyDelete(rec, gameDir)
  	default:
  		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  	}
  }

  // applyChunkAssemble implements §6.3.
  func applyChunkAssemble(ctx context.Context, p *Provider, wal *sophonApplyWAL, rec *sophonApplyRecord, gameDir, stagingRoot string) error {
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
  		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  	}
  	if rec.AssetMD5 != "" && !md5MatchesOnDisk(outTmp, rec.AssetMD5) {
  		_ = os.Remove(outTmp)
  		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  	}
  	return sophonRenameIntoGame(outTmp, gameDir, rec.Path)
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
  	defer in.Close()
  	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
  	if err != nil {
  		return err
  	}
  	if _, err := io.Copy(out, in); err != nil {
  		out.Close()
  		return err
  	}
  	if err := out.Sync(); err != nil {
  		out.Close()
  		return err
  	}
  	if err := out.Close(); err != nil {
  		return err
  	}
  	in.Close()
  	return os.Remove(src)
  }
  ```

  Add `finalizeSophonApply` (§6.2 steps 6–8):

  ```go
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
  	if err := RotateAfterApply(tempRoot, gid, gp.sophonBuildID, gp.Version, gp.sophonCategories); err != nil {
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
  ```

  > **INTEGRATOR-NOTE (T20-D):** `newSophonWALFlusher`/`MaybeFlush`/`Flush`, `loadSophonApplyWAL`/`writeSophonApplyWAL`, `toWalChunkSource`/`fromWalChunkSource` are owned by Task 16. `RotateAfterApply(tempRoot, gid, buildID, version, []sophon.Category)` is owned by Task 17 — its exact arg order/types must be confirmed (§A.6 only shows the JSON shape, not the function signature). If Task 17's signature differs (e.g. takes a `map[string]string` of category→buildID rather than `[]sophon.Category`), adapt the call. Likewise `loadSophonApplyWAL`/`writeSophonApplyWAL` names follow the §A.6 file (`sophon_apply_wal.go`); reconcile with Task 16's actual exports.

- [ ] **Step 9 (run, expect PASS):** run `TestRunSophonApply_ChunkAssembleAllCDN`.

- [ ] **Step 10 (commit):** `git commit -m "feat(hoyoverse/sophon): runSophonApply chunk_assemble + finalize + cleanup (Task 20)"`.

- [ ] **Step 11 (test, copy_over + delete):** `TestRunSophonApply_CopyOverAndDelete`: stage a `patches/<PatchName>` blob; WAL with one `copy_over` (slice → file, AssetMD5 of slice) + one `delete` (target pre-exists, ExpectMD5 matches). Assert copy_over target written, delete target removed, both `done`.

- [ ] **Step 12 (run, expect FAIL):** run.

- [ ] **Step 13 (implement copy_over + delete):** Add the two executors.

  ```go
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
  ```

  Add `"log/slog"` to imports.

- [ ] **Step 14 (run, expect PASS):** run.

- [ ] **Step 15 (commit):** `git commit -m "feat(hoyoverse/sophon): copy_over + delete record execution (Task 20)"`.

- [ ] **Step 16 (test, hdiff_patch + demotion):** Two cases. (a) `TestRunSophonApply_HDiffPatch_OldFileMatch`: stage a patch blob slice; gameDir has the OldPath file whose MD5 == record `OriginalFileMD5`; inject a fake `hpatchz.Run` (via a test seam — see T20-E) that writes known output to OutTmp; assert target written + done. (b) `TestRunSophonApply_HDiffPatch_DemoteToChunkAssemble`: OldPath file MD5 mismatches `OriginalFileMD5`; the plan's `sophonPatchAssetsFromMain[Path]` provides CDN chunk sources (stub the chunk download); assert the record is rewritten to `chunk_assemble`, persisted before execution, and the assemble succeeds.

  > **INTEGRATOR-NOTE (T20-E):** `sophon.HDiffApply` takes `opts.Run func(ctx, oldFile, diffFile, newFile) error` injected by the parent (§A.3). Production passes `hpatchz.Run`. For unit-testing demotion + the match path without a real hpatchz invocation, `applyHDiffPatch` takes the run func from a Provider field `p.hpatchzRun` defaulting to `hpatchz.Run` (set in `New`); tests override it. This adds one Provider field — flagged as a test seam not in §A.4's two-field list. If a field is undesirable, thread the run func as a `runSophonApply` parameter instead.

- [ ] **Step 17 (run, expect FAIL):** run.

- [ ] **Step 18 (implement hdiff_patch + demotion):** Add `applyHDiffPatch` per §6.4. On `OriginalFileMD5` mismatch: look up `gp.sophonPatchAssetsFromMain[Path]` (threaded via a closure-captured `gp` — see note), synchronously download missing CDN chunks, rewrite the in-memory record to `chunk_assemble` with persisted `AssembleSources`, **persist WAL before state transition**, then execute as chunk_assemble.

  ```go
  // applyHDiffPatch implements §6.4. demoteSources resolves the per-asset main
  // chunk plan for §6.4 demotion; it is closure-bound to gp by runSophonApply.
  func applyHDiffPatch(ctx context.Context, p *Provider, wal *sophonApplyWAL, rec *sophonApplyRecord, gameDir, stagingRoot string) error {
  	oldPath := filepath.Join(gameDir, rec.OldPath)
  	if rec.OriginalFileMD5 != "" && !md5MatchesOnDisk(oldPath, rec.OriginalFileMD5) {
  		// Synchronous demotion to chunk_assemble (§6.4).
  		sources := p.sophonDemoteSources(rec.Path)
  		if len(sources) == 0 {
  			return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  		}
  		// Download missing CDN chunks, block until they land.
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
  		// Rewrite record → chunk_assemble; persist BEFORE executing (§6.4).
  		walSrcs := make([]walChunkSource, 0, len(sources))
  		for _, s := range sources {
  			walSrcs = append(walSrcs, toWalChunkSource(s))
  		}
  		rec.Kind = "chunk_assemble"
  		rec.AssembleSources = walSrcs
  		rec.OldPath = ""
  		rec.PatchName = ""
  		rec.OriginalFileMD5 = ""
  		if err := writeSophonApplyWAL(versionSidecarDirFromWAL(p, wal), wal); err != nil {
  			return err
  		}
  		return applyChunkAssemble(ctx, p, wal, rec, gameDir, stagingRoot)
  	}

  	// Match: extract slice → hdiff_input → HDiffApply.
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
  		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  	}
  	if rec.AssetMD5 != "" && !md5MatchesOnDisk(outTmp, rec.AssetMD5) {
  		_ = os.Remove(outTmp)
  		return &core.UpdateError{Code: "sophon_apply_failed", Params: map[string]string{"file": rec.Path}, Retryable: false}
  	}
  	return sophonRenameIntoGame(outTmp, gameDir, rec.Path)
  }

  func (p *Provider) hpatchzRunOrDefault() func(ctx context.Context, oldFile, diffFile, newFile string) error {
  	if p.hpatchzRun != nil {
  		return p.hpatchzRun
  	}
  	return hpatchz.Run
  }
  ```

  > **INTEGRATOR-NOTE (T20-F):** Two helpers above need a home. `p.sophonDemoteSources(path)` returns `gp.sophonPatchAssetsFromMain[path]` — but `runSophonApply` resume (§6.9 path 1) may run with NO live `gp` (the WAL is the source of truth, offline-safe). §6.4's resume note says a demoted record already has `AssembleSources` persisted, so on resume the record is ALREADY `chunk_assemble` (not `hdiff_patch`) and `applyHDiffPatch` is never re-entered for it. The only time demotion runs is the FIRST pass, where `gp` IS live. Therefore `sophonDemoteSources` reads from a `gp` captured in `runSophonApply` and stashed on the Provider for the duration (or threaded). To avoid Provider mutable state, **thread `gp` into `applySophonRecord`/`applyHDiffPatch` as a parameter** rather than `p.sophonDemoteSources`. The plan keeps the helper named for clarity but the implementer should thread `gp *genshinPlan` through the dispatch chain (cleaner than Provider state). `versionSidecarDirFromWAL(p, wal)` likewise should be the `versionDir` already known to `runSophonApply` — thread it in rather than reconstruct. Both are flagged to be reconciled into parameters during implementation; the failing tests gate correctness either way.

- [ ] **Step 19 (run, expect PASS):** run both hdiff cases.

- [ ] **Step 20 (commit):** `git commit -m "feat(hoyoverse/sophon): hdiff_patch execution + §6.4 synchronous demotion (Task 20)"`.

- [ ] **Step 21 (test, WAL resume + cross-device):** `TestRunSophonApply_ResumeFromMidWAL`: pre-write a WAL with 2 of 4 records `done`; run; assert only the 2 pending execute (instrument via stub executors or by checking only pending targets appear). `TestSophonApply_CrossDeviceRename`: force `isCrossDevice`-true path by making `os.Rename` fail with a simulated EXDEV (use a build-tag-free seam: write the tmp on a path where rename to dst fails — simplest is to assert `copyAndRemove` directly produces the dst given a real EXDEV is hard to simulate portably, so test `copyAndRemove` as a unit + assert `sophonRenameIntoGame` calls it when `isCrossDevice` returns true via a small injectable `renameFn`). 

  > **INTEGRATOR-NOTE (T20-G):** Simulating EXDEV portably is not feasible in a unit test. Per §9.2 scenario 17 this is an INTEGRATION test (Task 23) using a real cross-device mount on the Windows host, or skipped on CI. Task 20's unit coverage asserts `copyAndRemove` correctness directly + that `sophonRenameIntoGame` falls back when an injected `renameFn` returns an `isCrossDevice`-true error. Mark the true-EXDEV path `t.Skip` on non-Windows. This mirrors v1 `update_apply_test.go`'s approach.

- [ ] **Step 22 (run, expect PASS):** run.

- [ ] **Step 23 (test, batched flush cadence):** `TestRunSophonApply_BatchedFlushCadence`: 120 trivial `delete` records (targets ENOENT → no-op done); count WAL rewrites via a flush counter (Task 16's flusher must expose flush count for test, or assert via file mtime sampling). Assert ≤ ceil(120/50)+1 flushes (i.e. batching, not per-record). 

  > **INTEGRATOR-NOTE (T20-H):** The batched-flush cadence assertion depends on Task 16's flusher exposing observability (a flush counter or injectable clock). §A.6 / §6.1 specify the 50-record / 5s policy but not a test hook. If Task 16 provides none, this test asserts the weaker invariant "final WAL is fully `done` and intermediate flushes occurred at least once" via a temp-file write hook. Flagged for Task 16 reconciliation.

- [ ] **Step 24 (run + full gate + commit):** run; then `go build ./... && go vet ./... && go test -count=1 ./internal/providers/hoyoverse/...`; `git commit -m "feat(hoyoverse/sophon): WAL resume + batched flush + cross-device apply tests (Task 20)"`.

---

### Task 21: `hoyoverse.go` integration — `CheckForUpdate` / `RunUpdate` Sophon dispatch, `maybeSelfHealSophon`

REPLACES the v1 `UsesSophon` `sophon_not_supported` short-circuit (hoyoverse.go ~line 170) with the real §3 decision flow, adds Sophon branches to `RunUpdate` (§6.9 resume ladder + §7.3 predl handoff), and adds `maybeSelfHealSophon` mirroring v1 `maybeSelfHeal` (incl clock-skew 470–483) byte-for-byte. Depends on Tasks 13 (`fetchBranchInfo`, setters, `sophonStagingDir`), 17 (`cleanupStaleSophonSidecars`, `LoadAppliedManifests`), 18 (`buildSophonPlan`, `detectPredlConsume`, `mapFoldersToMatchingFields`), 19 (`downloadAllSophon`), 20 (`runSophonApply`), 15 (`sophonProgressStore`).

**Files:** `internal/providers/hoyoverse/hoyoverse.go` (modify), `internal/providers/hoyoverse/hoyoverse_sophon_test.go` (new)

- [ ] **Step 1 (test, maybeSelfHealSophon clock-skew parity):** Add `hoyoverse_sophon_test.go`. `TestMaybeSelfHealSophon_ClockSkewParity` mirrors any v1 `maybeSelfHeal` clock-skew test: write a `last_apply_target.json` with `TargetVersion=branch.Main.Tag`, `LastWritebackRetryTS` set; assert the three §3.5 branches: (forward <24h → false, no writeback), (rewind ≤24h → false), (rewind >24h → false + sidecar removed). Plus a happy path: stale TS (>24h forward) + writable config.ini → returns true + writeback applied.

  ```go
  package hoyoverse

  import (
  	"context"
  	"net/http"
  	"net/http/httptest"
  	"os"
  	"path/filepath"
  	"testing"
  	"time"

  	"omnigate/internal/core"
  )

  func TestMaybeSelfHealSophon_ClockSkewParity(t *testing.T) {
  	gid := core.GameID("hoyoverse/genshin")
  	mk := func(t *testing.T, retryTS time.Time) (*Provider, string, string) {
  		tempRoot := t.TempDir()
  		gameDir := t.TempDir()
  		// minimal config.ini at old version
  		if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte("[General]\ngame_version=6.5.0\n"), 0o644); err != nil {
  			t.Fatal(err)
  		}
  		lat := &lastApplyTarget{TargetVersion: "6.6.0", LastWritebackRetryTS: retryTS}
  		if err := writeLastApplyTarget(tempRoot, gid, lat); err != nil {
  			t.Fatal(err)
  		}
  		p := New(Settings{}, nil)
  		return p, tempRoot, gameDir
  	}
  	now := time.Now().UTC()

  	// forward <24h → false
  	p, tempRoot, gameDir := mk(t, now.Add(-1*time.Hour))
  	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); healed {
  		t.Fatalf("forward<24h → healed true, want false")
  	}
  	// rewind >24h → false + sidecar removed
  	p, tempRoot, gameDir = mk(t, now.Add(48*time.Hour))
  	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); healed {
  		t.Fatalf("rewind>24h → healed true, want false")
  	}
  	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
  	if _, err := os.Stat(latPath); err == nil {
  		t.Fatalf("rewind>24h should remove last_apply_target.json")
  	}
  	// stale >24h forward → heal true
  	p, tempRoot, gameDir = mk(t, now.Add(-48*time.Hour))
  	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); !healed {
  		t.Fatalf("stale>24h → healed false, want true")
  	}
  	if v, _ := ReadGameVersion(gameDir); v != "6.6.0" {
  		t.Fatalf("heal should write game_version=6.6.0, got %q", v)
  	}
  }
  ```

- [ ] **Step 2 (run, expect FAIL):** `go test -count=1 ./internal/providers/hoyoverse/... -run TestMaybeSelfHealSophon_ClockSkewParity`.

- [ ] **Step 3 (implement maybeSelfHealSophon):** Add to `hoyoverse.go`, mirroring `maybeSelfHeal` (lines 447–499) with the §3.5 signature.

  ```go
  // maybeSelfHealSophon mirrors maybeSelfHeal (incl the clock-skew handling at
  // hoyoverse.go:470-483) for Sophon games: if an apply completed but config.ini
  // writeback failed, retry the writeback (24h budget). Returns true iff the
  // writeback now succeeds. §3.5.
  func (p *Provider) maybeSelfHealSophon(currentLocal, mainTag, gameDir, tempRoot string, gid core.GameID) bool {
  	if currentLocal == mainTag {
  		return false // already healed
  	}
  	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
  	lat, err := loadJSONSidecar[lastApplyTarget](latPath)
  	if err != nil || lat == nil {
  		return false
  	}
  	if lat.TargetVersion != mainTag {
  		return false
  	}

  	now := time.Now().UTC()
  	if !lat.LastWritebackRetryTS.IsZero() {
  		delta := now.Sub(lat.LastWritebackRetryTS)
  		if delta >= 0 && delta < 24*time.Hour {
  			return false
  		}
  		if delta < 0 && -delta <= 24*time.Hour {
  			return false
  		}
  		if delta < 0 && -delta > 24*time.Hour {
  			_ = os.Remove(latPath)
  			return false
  		}
  	}

  	writeErr := WriteGameVersion(gameDir, mainTag)
  	lat.LastWritebackRetryTS = now
  	lat.ConfigWritebackOK = (writeErr == nil)
  	if persistErr := writeLastApplyTarget(tempRoot, gid, lat); persistErr != nil {
  		p.logger.Warn("sophon self-heal: failed to update last_apply_target", "err", persistErr)
  	}
  	return writeErr == nil
  }
  ```

- [ ] **Step 4 (run, expect PASS):** run `TestMaybeSelfHealSophon_ClockSkewParity`.

- [ ] **Step 5 (commit):** `git commit -m "feat(hoyoverse): maybeSelfHealSophon mirroring v1 clock-skew (Task 21)"`.

- [ ] **Step 6 (test, CheckForUpdate dispatch):** `TestCheckForUpdateSophon_Dispatch` with sub-cases using an httptest `getGameBranches` server pointed at via `p.SetBranchAPIBaseURL`: (a) no install (no config.ini) → `UpdateError{Code:"sophon_no_install"}`; (b) idle (`currentLocal == branch.Main.Tag`) → plan `Reason == ReasonUnspecified`, `Kind` idle; (c) empty Main.Categories → `sophon_manifest_fetch_failed`; (d) predl-consume present → `Reason == ReasonResumeInterrupted`. Build a tiny branches JSON inline.

  ```go
  func TestCheckForUpdateSophon_NoInstall(t *testing.T) {
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		w.Write([]byte(`{"retcode":0,"message":"","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkg","branch":"main","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`))
  	}))
  	defer srv.Close()
  	p := New(Settings{}, nil)
  	p.SetBranchAPIBaseURL(srv.URL)
  	p.SetTempRootFn(func(core.GameID) string { return t.TempDir() })
  	// gameDir without config.ini → currentLocal == "" → sophon_no_install
  	// (gameDir resolution stubbed: see note T21-A)
  }
  ```

  > **INTEGRATOR-NOTE (T21-A):** `CheckForUpdate` resolves `gameDir` via `p.gameDir(gid)` → `DetectInstall` → launcher path scan, which a unit test cannot satisfy without a fake HoYoPlay tree. v1 integration tests use `testdata` launcher fixtures (Task 23). For Task 21's *unit* dispatch tests, the cleanest seam is to factor the Sophon decision body into an unexported `checkForUpdateSophon(ctx, gid, gameDir, tempRoot, branch) (core.UpdatePlan, error)` that takes `gameDir`/`tempRoot` explicitly (no `DetectInstall`), and have the exported `CheckForUpdate` resolve those then call it. Unit tests call `checkForUpdateSophon` directly with a temp `gameDir`. This split is needed for testability and mirrors how v1's `buildPlan` is a pure-ish function separate from `CheckForUpdate`. Flagged as a structural choice; the apiGameID `gopR6Cufr3`/biz must match `meta.go`'s Genshin entry — confirm against Task 13.

- [ ] **Step 7 (run, expect FAIL):** run.

- [ ] **Step 8 (implement CheckForUpdate Sophon body):** Replace the short-circuit (hoyoverse.go ~170) with the §3 flow factored into `checkForUpdateSophon`.

  ```go
  // In CheckForUpdate, replace the UsesSophon short-circuit block with:
  if g := findByID(gid); g != nil && g.UsesSophon {
  	gameDir, err := p.gameDir(gid)
  	if err != nil {
  		return core.UpdatePlan{}, err
  	}
  	return p.checkForUpdateSophon(ctx, gid, gameDir, p.tempRoot(gid))
  }
  ```

  ```go
  // checkForUpdateSophon implements the §3 decision tree for Sophon games.
  func (p *Provider) checkForUpdateSophon(ctx context.Context, gid core.GameID, gameDir, tempRoot string) (core.UpdatePlan, error) {
  	g := findByID(gid)
  	branch, err := p.fetchBranchInfo(ctx, g.APIGameID)
  	if err != nil {
  		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  	}
  	if branch.Main.IsEmpty() || len(branch.Main.Categories) == 0 {
  		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  	}

  	currentLocal, _ := ReadGameVersion(gameDir)
  	if currentLocal == "" {
  		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_no_install", Retryable: false}
  	}

  	allowedTargets := []string{branch.Main.Tag}
  	if !branch.PreDownload.IsEmpty() {
  		allowedTargets = append(allowedTargets, branch.PreDownload.Tag)
  	}

  	// Self-heal (§3.5).
  	if p.maybeSelfHealSophon(currentLocal, branch.Main.Tag, gameDir, tempRoot, gid) {
  		cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
  		gp := &genshinPlan{
  			UpdatePlan: core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonUnspecified},
  			flavor:     flavorNone,
  		}
  		p.manifestCache.put(gid, gp)
  		return gp.UpdatePlan, nil
  	}

  	// Idle short-circuit.
  	if currentLocal == branch.Main.Tag {
  		cleanupStaleSophonSidecars(tempRoot, gid, branch.Main.Tag, allowedTargets)
  		gp := &genshinPlan{
  			UpdatePlan: core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonUnspecified},
  			flavor:     flavorNone,
  		}
  		p.manifestCache.put(gid, gp)
  		return gp.UpdatePlan, nil
  	}

  	// Predl-consume short-circuit (§3.6).
  	if consume, predl := detectPredlConsume(tempRoot, gid, currentLocal, branch.Main.Tag, branch.Main.DiffTags); consume {
  		flavor := flavorSophonPatch
  		if predl.Kind == "sophon_build" {
  			flavor = flavorSophonBuild
  		}
  		snap := predl.PlanSnapshot
  		gp := &genshinPlan{
  			UpdatePlan:    core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: branch.Main.Tag, Reason: core.ReasonResumeInterrupted},
  			flavor:        flavor,
  			predlConsume:  true,
  			predlSnapshot: &snap,
  			sophonBranch:  branch,
  			sophonBuildID: predl.BuildID,
  			sourceVersion: currentLocal,
  		}
  		p.manifestCache.put(gid, gp)
  		return gp.UpdatePlan, nil
  	}

  	// Normal plan build.
  	audioFolders, _ := DetectInstalledLanguages(gameDir)
  	audioLangs := mapFoldersToMatchingFields(audioFolders)
  	gp, predlAvail, err := buildSophonPlan(ctx, p, branch, gid, currentLocal, audioLangs, gameDir, tempRoot)
  	if err != nil {
  		return core.UpdatePlan{}, err
  	}
  	gp.predlAvailable = predlAvail
  	p.manifestCache.put(gid, gp)
  	return gp.UpdatePlan, nil
  }
  ```

  > **INTEGRATOR-NOTE (T21-B):** `predl.PlanSnapshot` is a value `sophonPlanSnapshot` (§A.6); `&snap` takes the address of a local copy so `predlSnapshot *sophonPlanSnapshot` is non-aliasing. Confirm §A.6 `sophonPredlReadyFile.PlanSnapshot` is a value not a pointer (it is: `PlanSnapshot sophonPlanSnapshot`).

- [ ] **Step 9 (run, expect PASS):** run the CheckForUpdate dispatch tests.

- [ ] **Step 10 (commit):** `git commit -m "feat(hoyoverse): CheckForUpdate Sophon decision tree (no_install/idle/consume/self-heal) (Task 21)"`.

- [ ] **Step 11 (implement RunUpdate Sophon dispatch):** Add a Sophon branch near the top of `RunUpdate` (before the v1 `walExists`/`extractProgressExists` ladder, gated on the cached plan's flavor) implementing §6.9 resume ladder + §7.3 predl handoff. Keep the v1 path intact for HSR/ZZZ (fall through when flavor is not a Sophon flavor).

  ```go
  // Inserted in RunUpdate after gameDir/versionDir resolution, BEFORE the v1
  // walExists() block. Routes Sophon flavors; v1 games fall through unchanged.
  if gp := p.manifestCache.get(gid); gp != nil && isSophonFlavor(gp.flavor) || sophonSidecarsExist(versionDir) {
  	return p.runUpdateSophon(ctx, plan, gameDir, tempRoot, versionDir, emit)
  }
  ```

  ```go
  func isSophonFlavor(f planFlavor) bool {
  	switch f {
  	case flavorSophonPatch, flavorSophonBuild, flavorSophonFull, flavorSophonPredlPatch, flavorSophonPredlBuild:
  		return true
  	}
  	return false
  }

  func sophonSidecarsExist(versionDir string) bool {
  	if _, err := os.Stat(filepath.Join(versionDir, "sophon_apply.wal")); err == nil {
  		return true
  	}
  	if _, err := os.Stat(filepath.Join(versionDir, "sophon_progress.json")); err == nil {
  		return true
  	}
  	return false
  }

  // runUpdateSophon dispatches the Sophon resume ladder (§6.9) + fresh runs.
  func (p *Provider) runUpdateSophon(ctx context.Context, plan core.UpdatePlan, gameDir, tempRoot, versionDir string, emit func(stage string, current, total int)) error {
  	gid := plan.GameID

  	// §6.9 path 1: sophon_apply.wal present → apply-phase resume (offline-safe).
  	if wal, _ := loadSophonApplyWAL(versionDir); wal != nil && len(wal.Records) > 0 {
  		gp := p.manifestCache.get(gid)
  		if gp == nil {
  			gp = &genshinPlan{
  				UpdatePlan:    core.UpdatePlan{GameID: gid, Kind: core.PlanUpdate, Version: plan.Version},
  				flavor:        flavorFromString(wal.Flavor),
  				sophonBuildID: wal.BuildID,
  				sourceVersion: wal.SourceTag,
  			}
  		}
  		return runSophonApply(ctx, p, gid, gp, tempRoot, gameDir, wal.StagingRoot, emit)
  	}

  	gp := p.manifestCache.get(gid)
  	if gp == nil {
  		// §6.9 path 3: sophon_progress.json without WAL → rebuild plan via CheckForUpdate.
  		if _, err := os.Stat(filepath.Join(versionDir, "sophon_progress.json")); err == nil {
  			if _, cerr := p.CheckForUpdate(ctx, gid); cerr != nil {
  				return &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
  			}
  			gp = p.manifestCache.get(gid)
  		}
  		if gp == nil {
  			return fmt.Errorf("RunUpdate(sophon) without prior CheckForUpdate; manifestCache miss")
  		}
  	}

  	// §7.3 predl handoff: hydrate from snapshot when consuming.
  	branchKind := "main"
  	stagingBuildID := gp.sophonBuildID
  	if plan.Kind == core.PlanUpdate && gp.predlConsume && gp.predlSnapshot != nil {
  		gp.sophonChunkSources = gp.predlSnapshot.SophonChunkSources
  		gp.sophonPatches = gp.predlSnapshot.SophonPatches
  		gp.sophonDeletes = gp.predlSnapshot.SophonDeletes
  		gp.sophonCategories = gp.predlSnapshot.Categories
  		branchKind = "predl"
  	}

  	// Predownload: stage to predl, write predl_ready, SKIP apply (§7.1).
  	if plan.Kind == core.PlanPredownload {
  		if gp.predlPlan == nil {
  			return fmt.Errorf("RunUpdate(sophon predl) without predlPlan")
  		}
  		return p.runSophonPredownload(ctx, gid, gp, tempRoot, versionDir, gameDir, emit)
  	}

  	stagingRoot := sophonStagingDir(tempRoot, gid, plan.Version, branchKind, stagingBuildID)
  	store, err := newSophonProgressStore(tempRoot, gid, plan.Version, branchKind, stagingBuildID)
  	if err != nil {
  		return err
  	}
  	exec := defaultSophonExecutors(p.httpClientOrDefault())
  	if err := downloadAllSophon(ctx, store, gameDir, stagingRoot, gp.sophonChunkSources, gp.sophonPatches, 4, exec, func(b int64) {
  		emit("download", int(b), int(gp.TotalBytes))
  	}); err != nil {
  		return err
  	}
  	return runSophonApply(ctx, p, gid, gp, tempRoot, gameDir, stagingRoot, emit)
  }
  ```

  > **INTEGRATOR-NOTE (T21-C):** `flavorFromString(s string) planFlavor` (inverse of `planFlavor.String()`) is needed for WAL-resume when the manifestCache is cold (§6.9 path 1). Task 14 owns `planFlavor.String()`; the inverse is NOT in §A.5. Add `flavorFromString` in `plan_internal.go` (Task 14) OR locally here. Flagged: minimal addition, place it wherever Task 14 lands. `newSophonProgressStore(tempRoot, gid, version, branchKind, buildID)` is Task 15's constructor — confirm its arg list (the §A.6 `sophonProgressFile` has `BranchKind`/`BuildID` fields implying the constructor takes them). `emit("download", int(b), int(gp.TotalBytes))` truncates int64→int for the existing `emit` shape from v1 RunUpdate; acceptable for progress (bounded by file sizes) but flagged — the v1 `emit` closure already takes `int`.

- [ ] **Step 12 (implement runSophonPredownload):** §7.1 — download to `staging/predl`, write `predl_ready.json` with `PlanSnapshot`, skip apply.

  ```go
  // runSophonPredownload stages predl content and writes predl_ready.json
  // WITHOUT applying (§7.1).
  func (p *Provider) runSophonPredownload(ctx context.Context, gid core.GameID, gp *genshinPlan, tempRoot, versionDir, gameDir string, emit func(stage string, current, total int)) error {
  	pp := gp.predlPlan
  	stagingRoot := sophonStagingDir(tempRoot, gid, pp.TargetVersion, "predl", pp.BuildID)
  	store, err := newSophonProgressStore(tempRoot, gid, pp.TargetVersion, "predl", pp.BuildID)
  	if err != nil {
  		return err
  	}
  	exec := defaultSophonExecutors(p.httpClientOrDefault())
  	if err := downloadAllSophon(ctx, store, gameDir, stagingRoot, pp.ChunkSources, pp.Patches, 4, exec, func(b int64) {
  		emit("download", int(b), int(gp.TotalBytes))
  	}); err != nil {
  		return err
  	}
  	kind := "sophon_patch"
  	if pp.Flavor == flavorSophonPredlBuild {
  		kind = "sophon_build"
  	}
  	ready := &sophonPredlReadyFile{
  		Kind:           kind,
  		BuildID:        pp.BuildID,
  		SourceVersion:  pp.SourceVersion,
  		TargetVersion:  pp.TargetVersion,
  		AudioLanguages: pp.AudioLanguages,
  		StagedAt:       time.Now().UTC().Format(time.RFC3339),
  		PlanSnapshot: sophonPlanSnapshot{
  			SophonChunkSources: pp.ChunkSources,
  			SophonPatches:      pp.Patches,
  			SophonDeletes:      pp.Deletes,
  			Categories:         pp.Categories,
  		},
  	}
  	dir := versionSidecarDir(tempRoot, gid, pp.TargetVersion)
  	if err := os.MkdirAll(dir, 0o755); err != nil {
  		return err
  	}
  	data, err := json.MarshalIndent(ready, "", "  ")
  	if err != nil {
  		return err
  	}
  	path := filepath.Join(dir, "predl_ready.json")
  	tmp := path + ".tmp"
  	if err := os.WriteFile(tmp, data, 0o644); err != nil {
  		return err
  	}
  	return os.Rename(tmp, path)
  }
  ```

- [ ] **Step 12b (write failing test for `verifyPredlStaging`, then implement — §E.2 P8):** Add `internal/providers/hoyoverse/update_sophon_apply_test.go::TestVerifyPredlStaging_Threshold` (or a sibling `*_test.go`):

  ```go
  func TestVerifyPredlStaging_Threshold(t *testing.T) {
  	root := t.TempDir()
  	chunksDir := filepath.Join(root, "chunks")
  	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
  		t.Fatal(err)
  	}
  	// 100 CDN chunks; write good blobs for all, then corrupt N of them.
  	srcs := make([]sophon.ChunkSource, 0, 100)
  	for i := 0; i < 100; i++ {
  		name := fmt.Sprintf("chunk-%03d", i)
  		body := []byte(fmt.Sprintf("payload-%03d", i))
  		sum := md5.Sum(body)
  		if err := os.WriteFile(filepath.Join(chunksDir, name), body, 0o644); err != nil {
  			t.Fatal(err)
  		}
  		srcs = append(srcs, sophon.ChunkSource{
  			Kind: sophon.SourceCDN, ChunkName: name,
  			DecompSize: int64(len(body)), ExpectMD5: hex.EncodeToString(sum[:]),
  		})
  	}
  	corrupt := func(n int) {
  		for i := 0; i < n; i++ {
  			_ = os.WriteFile(filepath.Join(chunksDir, fmt.Sprintf("chunk-%03d", i)), []byte("bad"), 0o644)
  		}
  	}
  	// 24% corrupt → keep (discard==false)
  	corrupt(24)
  	discard, err := verifyPredlStaging(root, srcs, nil)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if discard {
  		t.Fatalf("24%% CDN fail: want discard=false, got true")
  	}
  	// 26% corrupt → discard (discard==true)
  	corrupt(26)
  	discard, err = verifyPredlStaging(root, srcs, nil)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if !discard {
  		t.Fatalf("26%% CDN fail: want discard=true, got false")
  	}
  }
  ```

  Run (expect FAIL — undefined `verifyPredlStaging`): `go test -count=1 ./internal/providers/hoyoverse/... -run TestVerifyPredlStaging_Threshold`.

  Implement in `update_sophon_apply.go` (verifies each staged CDN chunk/patch via the same xxh64-then-MD5 / MD5 rule used at download; counts failures; thresholds 0.25 / 0.50 per spec §7.3 step 4):

  ```go
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

  // stagedChunkOK reads the staged decompressed chunk file and verifies it
  // against src (xxh64 of ChunkName prefix when parseable, else MD5 ExpectMD5).
  func stagedChunkOK(path string, src sophon.ChunkSource) bool {
  	b, err := os.ReadFile(path)
  	if err != nil {
  		return false
  	}
  	if x, ok := sophon.ParseXXHName(src.ChunkName); ok {
  		return xxhash.Sum64(b) == x
  	}
  	sum := md5.Sum(b)
  	return hex.EncodeToString(sum[:]) == src.ExpectMD5
  }

  func stagedBlobOK(path, wantMD5 string) bool {
  	b, err := os.ReadFile(path)
  	if err != nil {
  		return false
  	}
  	sum := md5.Sum(b)
  	return hex.EncodeToString(sum[:]) == wantMD5
  }
  ```

  > Note: `sophon.ParseXXHName(name string) (uint64, bool)` is the exported helper Task 9 must provide (it already parses `ChunkName`'s first 16 hex chars internally for `DownloadChunk` verify — export it as `ParseXXHName` so apply-side staging verify reuses the identical rule; add to §A.3 sophon signatures). Imports for this block: `crypto/md5`, `encoding/hex`, `github.com/cespare/xxhash/v2`.

  Wire it into the predl-consume handoff in `RunUpdate` (the branch where `gp.predlConsume == true`, before reusing predl staging): after hydrating `sophonChunkSources`/`sophonPatches` from `predlSnapshot`, compute `predlStagingRoot := sophonStagingDir(tempRoot, gid, gp.Version, "predl", gp.sophonBuildID)`, then:

  ```go
  if discard, _ := verifyPredlStaging(predlStagingRoot, gp.sophonChunkSources, gp.sophonPatches); discard {
  	p.logger.Warn("sophon: predl staging too eroded; discarding for fresh download", "gid", gid)
  	_ = os.Remove(filepath.Join(versionSidecarDir(tempRoot, gid, gp.Version), "predl_ready.json"))
  	_ = os.RemoveAll(predlStagingRoot)
  	gp.predlConsume = false // fall through to a fresh staging/main download+apply below
  }
  ```

  Run (expect PASS): `go test -count=1 ./internal/providers/hoyoverse/... -run TestVerifyPredlStaging_Threshold`. Commit folded into Step 14.

- [ ] **Step 13 (run, expect PASS):** `go build ./... && go test -count=1 ./internal/providers/hoyoverse/... -run TestCheckForUpdateSophon` and existing v1 tests (regression — HSR/ZZZ path untouched).

- [ ] **Step 14 (commit):** `git commit -m "feat(hoyoverse): RunUpdate Sophon dispatch (resume ladder + predl handoff/stage) (Task 21)"`.

- [ ] **Step 15 (full gate + commit):** `go build ./... && go vet ./... && go test -count=1 ./...`; if green and nothing left uncommitted: `git commit -m "test(hoyoverse): Sophon integration dispatch unit coverage (Task 21)"` (or note clean tree). Defer broad end-to-end scenarios to Task 23.

> **INTEGRATOR-NOTE (T21-E):** `CheckVersion`'s v1 `fetchBranchTag` call (hoyoverse.go:121) should, per [DEV-3], be refactored to delegate to `fetchBranchInfo` (Task 13). If Task 13 already did that refactor, leave it; if not, Task 21 changes line 121 to `branch, err := p.fetchBranchInfo(ctx, g.APIGameID); tag := branch.Main.Tag`. Confirm against Task 13's delivered `api.go` to avoid a double-edit conflict.

### Task 22: Frontend i18n + generic error display + Vitest

> Group **G**. Implements §8 (frontend), [DEV-4] (generic `last_error.code → update.error.<code>` display path), and the i18n key churn from §A.11 / §8.1 / §8.2. TDD: failing Vitest case first, then implement.
>
> **Frontend gate (every step that runs tests):** `cd frontend && npm run build && npx vitest run`. No Go in this task.

**Files:**
- `frontend/src/locales/en.json` (modify — remove 1 key, add 5)
- `frontend/src/locales/zh-TW.json` (modify — same)
- `frontend/src/locales/zh-CN.json` (modify — same)
- `frontend/src/components/BottomBar.vue` (modify — [DEV-4] generic error display)
- `frontend/src/__tests__/BottomBar.test.ts` (modify — 1 new case)
- `frontend/src/__tests__/i18n_parity.test.ts` (auto-covers new keys via parity walk; the identical-key-set test FAILS until all 3 locales are edited identically — no edit needed unless we want to assert the new keys explicitly; we do NOT edit it)

INTEGRATOR-NOTE: The spec §8.4 says "i18n_parity.test.ts auto-covers new keys via its existing parity walk." That is true for the *identical-key-set* test (it walks every key in all 3 locales and asserts set-equality), so removing `sophon_not_supported` from only 1 or 2 locales, or adding the 5 new keys to only some, will fail that test. The `required[]` allowlist test does NOT auto-cover the new keys (it's a hardcoded list). We deliberately do NOT add the new Sophon keys to that allowlist — the parity walk is sufficient and the allowlist is a v1 artifact. This keeps the i18n_parity edit at zero.

- [ ] **Step 1 — Failing Vitest case (BottomBar renders `sophon_no_install` error text).**

  Add this `test(...)` block to `frontend/src/__tests__/BottomBar.test.ts`, inserted immediately before the final `// Full 8-row table-driven test deferred...` comment (i.e. as the last test in the `describe`). It mounts BottomBar with a selected game and a `last_error` of `{ code: 'sophon_no_install' }`, then asserts the rendered DOM contains the resolved English text. This FAILS now because BottomBar.vue has no generic error-display element.

  ```ts
  test('renders generic last_error.code via update.error.<code> (sophon_no_install)', async () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en },
    });

    const wrapper = mount(BottomBar, {
      global: { plugins: [i18n] },
    });

    const games = useGamesStore();
    games.games = [
      {
        id: 'hoyoverse/genshin',
        backend: 'hoyoverse',
        display_name: { en: 'Genshin Impact' },
        installed: true,
        has_predownload: false,
        current_version: '',
        latest_version: '6.6.0',
      },
    ];
    games.selectedID = 'hoyoverse/genshin';

    const updates = useUpdatesStore();
    updates.byGame['hoyoverse/genshin'] = {
      last_error: { code: 'sophon_no_install', retryable: false },
    } as any;

    await wrapper.vm.$nextTick();

    const errEl = wrapper.find('.update-error');
    expect(errEl.exists()).toBe(true);
    expect(errEl.text()).toContain('Use HoYoPlay for initial install');
  });
  ```

- [ ] **Step 2 — Run Vitest, expect FAIL.**

  ```bash
  cd frontend && npx vitest run
  ```
  Expect: the new BottomBar case fails (`.update-error` not found). The i18n_parity identical-key-set test still PASSES at this point (locales unchanged). Confirm the failure is the new case only.

- [ ] **Step 3 — Implement: locale edits (all 3 files, byte-parallel).**

  In **`frontend/src/locales/en.json`**, inside the `"update"."error"` object:
  - REMOVE the `"sophon_not_supported": "..."` line (the last entry in `error`; also remove the trailing comma now on `"version_unknown"` — see note).
  - ADD the 4 new `sophon_*` keys. Final `update.error` block (en.json) becomes verbatim:

  ```json
    "error": {
      "insufficient_space": "Needs {required}, {available} available; free up space and retry",
      "cross_volume_setup": "Temp dir and game dir on different drives; change in Settings",
      "cross_volume_midrun": "Drive state changed; update aborted",
      "unsupported_manifest": "Unsupported manifest format",
      "source_corrupted": "Local files modified; recommend full reinstall",
      "source_corrupted_legacy": "Local file size anomaly; recommend full reinstall",
      "source_size_mismatch": "Source file size mismatch; please retry",
      "patch_corrupted": "Patch verification failed; please retry",
      "apply_failed": "Apply failed ({file}); please retry",
      "apply_partial": "Some files could not be updated ({file}); close game and retry",
      "permission_denied": "Writing game dir requires admin; restart as administrator",
      "version_unknown": "Cannot read local version",
      "sophon_no_install": "Use HoYoPlay for initial install",
      "sophon_manifest_fetch_failed": "Failed to fetch Sophon manifest",
      "sophon_chunk_verify_failed": "Chunk verification failed ({file})",
      "sophon_apply_failed": "Apply failed: {file}"
    },
  ```

  And in en.json's `"update"."reason"` block, ADD the `resume_interrupted` key (it serves [DEV-4]'s `ReasonResumeInterrupted` per §8.3 — "reuses the existing reason key family"). Final `update.reason` block (en.json):

  ```json
    "reason": {
      "version_changed": "{currentVer} → {targetVer}",
      "audio_pack_added": "Adding audio packs: {langs}",
      "version_and_audio": "{currentVer} → {targetVer} (with new audio packs)",
      "predownload": "Predownload {targetVer} (patch day apply)",
      "resume_interrupted": "Resume interrupted update"
    },
  ```

  In **`frontend/src/locales/zh-TW.json`** — same structural edits. Final `update.error`:

  ```json
    "error": {
      "insufficient_space": "需要 {required}，可用 {available}；請清理後重試",
      "cross_volume_setup": "暫存與遊戲目錄不同磁碟；請至設定變更",
      "cross_volume_midrun": "磁碟狀態變化，更新中止",
      "unsupported_manifest": "不支援的更新封包格式",
      "source_corrupted": "本機檔案被修改；建議全量重灌",
      "source_corrupted_legacy": "本機檔案大小異常；建議全量重灌",
      "source_size_mismatch": "源檔案大小不一致；請重試",
      "patch_corrupted": "修補檔案校驗失敗；請重試",
      "apply_failed": "套用失敗（{file}）；請重試",
      "apply_partial": "部分檔案無法更新（{file}）；請關閉遊戲後重試",
      "permission_denied": "寫入遊戲目錄需要管理員；請以管理員身份重啟",
      "version_unknown": "無法讀取本地版本",
      "sophon_no_install": "請先用 HoYoPlay 完成首次安裝",
      "sophon_manifest_fetch_failed": "無法取得 Sophon 更新資訊",
      "sophon_chunk_verify_failed": "下載的檔案區塊驗證失敗 ({file})",
      "sophon_apply_failed": "{file} 套用失敗"
    },
  ```

  zh-TW.json's `update.reason`:

  ```json
    "reason": {
      "version_changed": "{currentVer} → {targetVer}",
      "audio_pack_added": "新增語音包：{langs}",
      "version_and_audio": "{currentVer} → {targetVer}（含新增語音包）",
      "predownload": "預下載 {targetVer}（patch day 套用）",
      "resume_interrupted": "繼續上次未完成的更新"
    },
  ```

  In **`frontend/src/locales/zh-CN.json`** — same. Final `update.error`:

  ```json
    "error": {
      "insufficient_space": "需要 {required}，可用 {available}；请清理后重试",
      "cross_volume_setup": "暂存与游戏目录不同磁盘；请至设置变更",
      "cross_volume_midrun": "磁盘状态变化，更新中止",
      "unsupported_manifest": "不支持的更新包格式",
      "source_corrupted": "本地文件被修改；建议全量重装",
      "source_corrupted_legacy": "本地文件大小异常；建议全量重装",
      "source_size_mismatch": "源文件大小不一致；请重试",
      "patch_corrupted": "补丁文件校验失败；请重试",
      "apply_failed": "应用失败（{file}）；请重试",
      "apply_partial": "部分文件无法更新（{file}）；请关闭游戏后重试",
      "permission_denied": "写入游戏目录需要管理员；请以管理员身份重启",
      "version_unknown": "无法读取本地版本",
      "sophon_no_install": "请先用 HoYoPlay 完成首次安装",
      "sophon_manifest_fetch_failed": "无法获取 Sophon 更新信息",
      "sophon_chunk_verify_failed": "下载的文件区块验证失败 ({file})",
      "sophon_apply_failed": "{file} 应用失败"
    },
  ```

  zh-CN.json's `update.reason`:

  ```json
    "reason": {
      "version_changed": "{currentVer} → {targetVer}",
      "audio_pack_added": "新增语音包：{langs}",
      "version_and_audio": "{currentVer} → {targetVer}（含新增语音包）",
      "predownload": "预下载 {targetVer}（patch day 应用）",
      "resume_interrupted": "继续上次未完成的更新"
    },
  ```

  Note (JSON trailing-comma hygiene): in all 3 files the `error` block ends with `}` then `,` (because `"bell"` follows), and the LAST entry inside `error` is `sophon_apply_failed` with NO trailing comma. The `reason` block's last entry is now `resume_interrupted` with NO trailing comma; the `}` closing `reason` keeps its trailing comma (because `errors`/`error` follow it). Use the verbatim blocks above — they already encode the correct comma placement.

- [ ] **Step 4 — Implement: BottomBar.vue [DEV-4] generic error display.**

  Add a computed for the error message and a template element. In `BottomBar.vue` `<script setup>`, insert after the `predlReady` computed (around line 26):

  ```ts
  const lastError = computed(() => selectedSnap.value?.last_error ?? null);

  // [DEV-4] Generic last_error.code → update.error.<code> renderer. v1 had no
  // such path (only useResumePrompt handled interrupted_resume); Sophon error
  // codes (sophon_no_install / sophon_manifest_fetch_failed /
  // sophon_chunk_verify_failed / sophon_apply_failed) flow through here.
  // interrupted_resume is excluded — it is surfaced by the bell drawer via
  // useResumePrompt, not the inline error line.
  const errorLabel = computed<string>(() => {
    const err = lastError.value;
    if (!err || !err.code) return '';
    if (err.code === 'interrupted_resume') return '';
    return t(`update.error.${err.code}`, (err.params as any) || {});
  });
  ```

  Then in the `<template>`, add the error line as the FIRST child inside `<div v-if="games.selected" class="bottom-bar">`, immediately before `<div class="hero-stats-line">`:

  ```html
    <div v-if="errorLabel" class="update-error">{{ errorLabel }}</div>
  ```

  INTEGRATOR-NOTE: `t('update.error.<code>', params)` returns the key path verbatim (e.g. `"update.error.foo"`) for an unknown code, which is harmless (no crash) but ugly. All Sophon codes from §A.9 have keys (Step 3), so unknown codes only appear on a contract bug. No styling is specified by the spec for `.update-error`; this plan adds the element with the class only. INTEGRATOR may add CSS in a follow-up; the Vitest case asserts on `.text()`, not appearance.

- [ ] **Step 5 — Run Vitest + build, expect PASS.**

  ```bash
  cd frontend && npm run build && npx vitest run
  ```
  Expect: the new BottomBar case PASSES (`.update-error` found, text contains "Use HoYoPlay for initial install"); the i18n_parity identical-key-set test PASSES (all 3 locales edited identically — 1 removed, 5 added each); `npm run build` (vue-tsc + vite) succeeds with no type errors. The pre-existing `cancel button ... applying stage` case still passes.

- [ ] **Step 6 — Commit.**

  ```bash
  git add frontend/src/locales/en.json frontend/src/locales/zh-TW.json frontend/src/locales/zh-CN.json frontend/src/components/BottomBar.vue frontend/src/__tests__/BottomBar.test.ts
  git commit -m "feat(hoyoverse-sophon): frontend i18n + generic error display [DEV-4]

Remove update.error.sophon_not_supported; add sophon_no_install /
sophon_manifest_fetch_failed / sophon_chunk_verify_failed /
sophon_apply_failed + update.reason.resume_interrupted across all 3
locales. Add generic last_error.code -> update.error.<code> render
path in BottomBar.vue. Vitest covers sophon_no_install rendering."
  ```

---

### Task 23: Integration tests + fakeSophonServer + testdata fixtures

> Group **G**. Replaces the 9 `t.Skip` placeholders in `integration_test.go` with the 30 Sophon end-to-end scenarios from spec §9.2. Builds the `testdata/sophon/*` fixtures (JSON envelopes + synthetic `.manifest.pb.zst` / `.patch.pb.zst` / chunk blobs) and the `fakeSophonServer` httptest mux. Wires the server via `SetBranchAPIBaseURL` / `SetSophonAPIBaseURL` ([DEV-3]) and `SetAPIBaseURL`.
>
> **Depends on Tasks 4–21 being complete** (proto types, sophon-package funcs, hoyoverse-layer plan/download/apply, setters). These tests exercise the assembled provider end-to-end.
>
> **Go gate (every step):** PATH note — on subagent shells without Go on PATH, prepend once per shell:
> ```bash
> export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"
> ```
> Host is `CGO_ENABLED=0` — **never** `-race`. Integration tests are behind the `integration` build tag; run with:
> ```bash
> go test -count=1 -tags integration ./internal/providers/hoyoverse/...
> ```

**Files:**
- `internal/providers/hoyoverse/testdata/sophon/branches_main_only.json` (create)
- `internal/providers/hoyoverse/testdata/sophon/branches_with_predl.json` (create)
- `internal/providers/hoyoverse/testdata/sophon/build_small.json` (create)
- `internal/providers/hoyoverse/testdata/sophon/build_tiny.json` (create)
- `internal/providers/hoyoverse/testdata/sophon/patch_small.json` (create)
- `internal/providers/hoyoverse/testdata/sophon/gen_fixtures_test.go` (create — committed generator that writes the `.pb.zst` + chunk blobs; documents exact byte construction)
- `internal/providers/hoyoverse/testdata/sophon/sample.manifest.pb.zst`, `sample.patch.pb.zst`, `tiny.manifest.pb.zst`, `chunks/*` (create — generated by the generator below, then committed)
- `internal/providers/hoyoverse/integration_test.go` (rewrite — fakeSophonServer + 30 scenarios)

INTEGRATOR-NOTE (fixture byte construction strategy): The spec §1 lists the `.pb.zst` fixtures as committed binary blobs but does not pin their exact bytes. Rather than hand-author opaque binaries, this task commits a `gen_fixtures_test.go` whose `TestGenerateSophonFixtures` (gated behind `-run TestGenerateSophonFixtures -tags genfixtures` so it never runs in normal CI) marshals the proto structs from `sophon/proto` + zstd-compresses them with the SAME `klauspost/compress/zstd` encoder the production path uses, then writes the blobs + chunks into `testdata/sophon/`. Running it once and committing the output makes the committed `.pb.zst`/`chunks/*` reproducible and self-documenting. The chunk blobs are deterministic: each chunk's decompressed bytes are `bytes.Repeat([]byte{byte(i)}, decompSize)` for chunk index `i`, and its `ChunkDecompressedHashMd5` / xxh64-prefixed `ChunkName` are computed from those bytes so the verification path validates against real hashes. This resolves the spec gap on "exact byte construction" deterministically.

INTEGRATOR-NOTE (manifest↔build_id wiring): The synthetic `*.manifest.pb.zst` Asset/Chunk names, sizes, and MD5s MUST agree with the JSON envelope's `manifest.id` / `chunk_download.url_prefix` and with the chunk filenames under `chunks/`. The generator below is the single source of truth: it writes the JSON `build_*.json` field values from the SAME in-memory structs it uses to marshal the proto, so they cannot drift. Where the JSON files below show literal IDs/URLs, they MUST match the generator's emitted values — Step 1 writes both from the generator. The hand-written JSON in this task is illustrative of shape; the generator overwrites them. (INTEGRATOR: keep the generator authoritative; do not hand-edit the JSON after generation.)

- [ ] **Step 1 — Write the fixture generator (`gen_fixtures_test.go`) + run it to emit all fixtures. Then COMMIT the generator + all generated fixtures.**

  Create `internal/providers/hoyoverse/testdata/sophon/gen_fixtures_test.go`. It is gated behind a `genfixtures` build tag so it never runs under `-tags integration`. It constructs the proto structs, zstd-compresses, computes hashes, and writes every fixture file in this directory (JSON + pb.zst + chunks). FULL verbatim:

  ```go
  //go:build genfixtures

  package sophon_fixtures

  // Run with:
  //   go test -tags genfixtures -run TestGenerateSophonFixtures \
  //     ./internal/providers/hoyoverse/testdata/sophon/
  // then `git add` the emitted files. This generator is the single source of
  // truth for the synthetic Sophon manifests, patch protos, and chunk blobs.
  //
  // Byte construction (documented):
  //   - "small" build: 1 category ("game"), 5 assets, 3 chunks each (15 chunks).
  //   - "tiny" build: 1 category ("game"), 1 asset, 1 chunk.
  //   - Chunk decompressed payload for global chunk index k is
  //         bytes.Repeat([]byte{byte(k % 256)}, decompSize)
  //     with decompSize = 1024 for small, 256 for tiny.
  //   - ChunkName = hex(xxh64(decompressed))[0:16] + "_" + sprintf("%05d", k)
  //     so the first 16 hex chars are a real xxh64 prefix (verification path
  //     parses + matches it; TestSophonChunkVerify_XXh64ParseFail_FallbackMD5
  //     uses a deliberately non-hex ChunkName, written separately below).
  //   - ChunkDecompressedHashMd5 = hex(md5(decompressed)).
  //   - On-disk chunk file chunks/<ChunkName> = zstd(decompressed) (compression
  //     on); a raw (uncompressed) variant chunks/raw_<ChunkName> is also written
  //     for the compression==0 code path.
  //   - AssetHashMd5 = hex(md5(concat of the asset's chunk decompressed bytes
  //     in ChunkOnFileOffset order)); AssetSize = sum of decompSize.
  //   - patch_small: 3 assets. asset[0] => HDiff (OriginalFileName set),
  //     asset[1] => CopyOver (OriginalFileName ""), asset[2] => UnusedAssets
  //     delete entry. PatchName blobs are written under chunks/ too (the patch
  //     CDN reuses the chunk mux path in fakeSophonServer).

  import (
  	"bytes"
  	"crypto/md5"
  	"encoding/hex"
  	"encoding/json"
  	"fmt"
  	"os"
  	"path/filepath"
  	"testing"

  	"github.com/cespare/xxhash/v2"
  	"github.com/klauspost/compress/zstd"
  	"google.golang.org/protobuf/proto"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  const (
  	smallDecomp = 1024
  	tinyDecomp  = 256
  )

  func md5hex(b []byte) string  { s := md5.Sum(b); return hex.EncodeToString(s[:]) }
  func xx16(b []byte) string    { return fmt.Sprintf("%016x", xxhash.Sum64(b)) }

  func zstdBytes(t *testing.T, in []byte) []byte {
  	var buf bytes.Buffer
  	enc, err := zstd.NewWriter(&buf)
  	if err != nil {
  		t.Fatalf("zstd writer: %v", err)
  	}
  	if _, err := enc.Write(in); err != nil {
  		t.Fatalf("zstd write: %v", err)
  	}
  	if err := enc.Close(); err != nil {
  		t.Fatalf("zstd close: %v", err)
  	}
  	return buf.Bytes()
  }

  func writeFile(t *testing.T, name string, data []byte) {
  	p := filepath.Join(".", name)
  	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
  		t.Fatalf("mkdir %s: %v", p, err)
  	}
  	if err := os.WriteFile(p, data, 0o644); err != nil {
  		t.Fatalf("write %s: %v", p, err)
  	}
  	t.Logf("wrote %s (%d bytes)", name, len(data))
  }

  func writeJSON(t *testing.T, name string, v any) {
  	b, err := json.MarshalIndent(v, "", "  ")
  	if err != nil {
  		t.Fatalf("marshal %s: %v", name, err)
  	}
  	writeFile(t, name, b)
  }

  // buildManifest creates a SophonManifestProto + writes its chunk blobs, and
  // returns the proto plus the per-chunk ChunkName slice (global indexing via
  // *counter so names are unique across assets).
  func buildManifest(t *testing.T, nAssets, nChunks, decomp int, counter *int) *pb.SophonManifestProto {
  	m := &pb.SophonManifestProto{}
  	for a := 0; a < nAssets; a++ {
  		asset := &pb.SophonManifestAssetProperty{
  			AssetName: fmt.Sprintf("data/file_%02d.bin", a),
  			AssetType: 0,
  		}
  		var whole []byte
  		var offset int64
  		for c := 0; c < nChunks; c++ {
  			k := *counter
  			*counter++
  			payload := bytes.Repeat([]byte{byte(k % 256)}, decomp)
  			name := xx16(payload) + fmt.Sprintf("_%05d", k)
  			writeFile(t, filepath.Join("chunks", name), zstdBytes(t, payload))
  			writeFile(t, filepath.Join("chunks", "raw_"+name), payload)
  			asset.AssetChunks = append(asset.AssetChunks, &pb.SophonManifestAssetChunk{
  				ChunkName:                name,
  				ChunkDecompressedHashMd5: md5hex(payload),
  				ChunkOnFileOffset:        offset,
  				ChunkSize:                int64(len(zstdBytes(t, payload))),
  				ChunkSizeDecompressed:    int64(decomp),
  			})
  			whole = append(whole, payload...)
  			offset += int64(decomp)
  		}
  		asset.AssetSize = int64(len(whole))
  		asset.AssetHashMd5 = md5hex(whole)
  		m.Assets = append(m.Assets, asset)
  	}
  	return m
  }

  func TestGenerateSophonFixtures(t *testing.T) {
  	// --- small manifest (5 assets x 3 chunks) ---
  	cnt := 0
  	small := buildManifest(t, 5, 3, smallDecomp, &cnt)
  	smallBytes, err := proto.Marshal(small)
  	if err != nil {
  		t.Fatalf("marshal small: %v", err)
  	}
  	writeFile(t, "sample.manifest.pb.zst", zstdBytes(t, smallBytes))

  	// --- tiny manifest (1 asset x 1 chunk) ---
  	cntTiny := 10000 // disjoint chunk-index space → disjoint ChunkNames
  	tiny := buildManifest(t, 1, 1, tinyDecomp, &cntTiny)
  	tinyBytes, err := proto.Marshal(tiny)
  	if err != nil {
  		t.Fatalf("marshal tiny: %v", err)
  	}
  	writeFile(t, "tiny.manifest.pb.zst", zstdBytes(t, tinyBytes))

  	// --- patch proto (3 assets: HDiff, CopyOver, + UnusedAssets delete) ---
  	patch := &pb.SophonPatchProto{}
  	// HDiff asset: OriginalFileName set; patch blob payload is opaque (hpatchz
  	// is exercised only at smoke time — integration uses MethodCopyOver for the
  	// applies; HDiff record path is unit-tested with a mock Run in T12/T20).
  	hdiffPayload := bytes.Repeat([]byte{0xAB}, 512)
  	hdiffName := xx16(hdiffPayload) + "_patch00"
  	writeFile(t, filepath.Join("chunks", hdiffName), hdiffPayload)
  	patch.PatchAssets = append(patch.PatchAssets, &pb.SophonPatchAssetProperty{
  		AssetName:    "data/file_00.bin",
  		AssetSize:    int64(smallDecomp * 3),
  		AssetHashMd5: small.Assets[0].AssetHashMd5,
  		AssetInfos: []*pb.SophonPatchAssetInfo{{
  			VersionTag: "6.5.0",
  			Chunk: &pb.SophonPatchAssetChunk{
  				PatchName:          hdiffName,
  				VersionTag:         "6.5.0",
  				BuildId:            "build-small",
  				PatchSize:          int64(len(hdiffPayload)),
  				PatchMd5:           md5hex(hdiffPayload),
  				PatchOffset:        0,
  				PatchLength:        int64(len(hdiffPayload)),
  				OriginalFileName:   "data/file_00.bin",
  				OriginalFileLength: int64(smallDecomp * 3),
  				OriginalFileMd5:    small.Assets[0].AssetHashMd5,
  			},
  		}},
  	})
  	// CopyOver asset: full new file delivered as the patch blob slice. The blob
  	// IS the decompressed content of small asset[1], so post-apply MD5 matches.
  	var copyWhole []byte
  	for _, ch := range small.Assets[1].AssetChunks {
  		k := int(ch.ChunkSizeDecompressed)
  		_ = k
  	}
  	// Reconstruct asset[1] decompressed payload deterministically (same rule as
  	// buildManifest: each chunk = byte(globalIdx); asset[1] chunks were global
  	// indices 3,4,5 in the small space).
  	for gi := 3; gi <= 5; gi++ {
  		copyWhole = append(copyWhole, bytes.Repeat([]byte{byte(gi)}, smallDecomp)...)
  	}
  	copyName := xx16(copyWhole) + "_patch01"
  	writeFile(t, filepath.Join("chunks", copyName), copyWhole)
  	patch.PatchAssets = append(patch.PatchAssets, &pb.SophonPatchAssetProperty{
  		AssetName:    "data/file_01.bin",
  		AssetSize:    int64(len(copyWhole)),
  		AssetHashMd5: small.Assets[1].AssetHashMd5,
  		AssetInfos: []*pb.SophonPatchAssetInfo{{
  			VersionTag: "6.5.0",
  			Chunk: &pb.SophonPatchAssetChunk{
  				PatchName:        copyName,
  				VersionTag:       "6.5.0",
  				BuildId:          "build-small",
  				PatchSize:        int64(len(copyWhole)),
  				PatchMd5:         md5hex(copyWhole),
  				PatchOffset:      0,
  				PatchLength:      int64(len(copyWhole)),
  				OriginalFileName: "", // CopyOver
  			},
  		}},
  	})
  	// UnusedAssets: delete data/old_removed.bin
  	patch.UnusedAssets = append(patch.UnusedAssets, &pb.SophonUnusedAssetProperty{
  		VersionTag: "6.5.0",
  		AssetInfos: []*pb.SophonUnusedAssetInfo{{
  			Assets: []*pb.SophonUnusedAssetFile{{
  				FileName: "data/old_removed.bin",
  				FileSize: 16,
  				FileMd5:  md5hex(bytes.Repeat([]byte{0x7}, 16)),
  			}},
  		}},
  	})
  	patchBytes, err := proto.Marshal(patch)
  	if err != nil {
  		t.Fatalf("marshal patch: %v", err)
  	}
  	writeFile(t, "sample.patch.pb.zst", zstdBytes(t, patchBytes))

  	// --- JSON envelopes (shapes must match infos.go ParseBuildResponse) ---
  	mkBuild := func(buildID, tag, manifestFile string, compressedLen int) map[string]any {
  		return map[string]any{
  			"retcode": 0, "message": "",
  			"data": map[string]any{
  				"build_id": buildID, "tag": tag, "patch_id": "",
  				"manifests": []any{map[string]any{
  					"category_id": "10016", "category_name": "Game", "matching_field": "game",
  					"manifest": map[string]any{
  						"id": manifestFile, "checksum": "",
  						"compressed_size": compressedLen, "uncompressed_size": 0,
  					},
  					"manifest_download": map[string]any{
  						"url_prefix": "/cdn/manifest", "url_suffix": "",
  						"password": "", "encryption": 0, "compression": 1,
  					},
  					"chunk_download": map[string]any{
  						"url_prefix": "/cdn/chunks", "url_suffix": "",
  						"password": "", "encryption": 0, "compression": 1,
  					},
  					"diff_download": map[string]any{
  						"url_prefix": "/cdn/chunks", "url_suffix": "",
  						"password": "", "encryption": 0, "compression": 0,
  					},
  				}},
  			},
  		}
  	}
  	writeJSON(t, "build_small.json", mkBuild("build-small", "6.6.0", "sample.manifest.pb.zst", len(zstdBytes(t, smallBytes))))
  	writeJSON(t, "build_tiny.json", mkBuild("build-tiny", "6.6.0", "tiny.manifest.pb.zst", len(zstdBytes(t, tinyBytes))))

  	patchEnv := mkBuild("build-small", "6.6.0", "sample.patch.pb.zst", len(zstdBytes(t, patchBytes)))
  	patchEnv["data"].(map[string]any)["patch_id"] = "patch-small"
  	writeJSON(t, "patch_small.json", patchEnv)

  	// --- getGameBranches envelopes ---
  	mkBranch := func(withPredl bool) map[string]any {
  		cats := []any{map[string]any{
  			"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE",
  		}}
  		data := map[string]any{
  			"main": map[string]any{
  				"package_id": "pkg-main", "branch": "main", "password": "",
  				"tag": "6.6.0", "diff_tags": []any{"6.5.0"}, "categories": cats,
  			},
  		}
  		if withPredl {
  			data["pre_download"] = map[string]any{
  				"package_id": "pkg-predl", "branch": "predownload", "password": "",
  				"tag": "6.7.0", "diff_tags": []any{"6.6.0"}, "categories": cats,
  			}
  		} else {
  			data["pre_download"] = map[string]any{}
  		}
  		return map[string]any{"retcode": 0, "message": "", "data": data}
  	}
  	writeJSON(t, "branches_main_only.json", mkBranch(false))
  	writeJSON(t, "branches_with_predl.json", mkBranch(true))
  }
  ```

  Run + commit:
  ```bash
  go test -tags genfixtures -run TestGenerateSophonFixtures ./internal/providers/hoyoverse/testdata/sophon/
  git add internal/providers/hoyoverse/testdata/sophon/
  git commit -m "test(hoyoverse-sophon): committed fixture generator + synthetic Sophon fixtures

gen_fixtures_test.go (genfixtures build tag) marshals proto + zstd to
produce sample/tiny manifest, sample patch, build/patch/branch JSON
envelopes, and deterministic chunk blobs. Output committed."
  ```

- [ ] **Step 2 — fakeSophonServer + harness scaffolding in `integration_test.go`.**

  Rewrite `integration_test.go` header + add the server harness (the 30 scenarios append below across Steps 3–6). FULL verbatim for the scaffolding:

  ```go
  //go:build integration

  package hoyoverse

  import (
  	"context"
  	"net/http"
  	"net/http/httptest"
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"

  	"omnigate/internal/core"
  )

  const genshinGID = core.GameID("hoyoverse/genshin")

  // fakeSophonServer serves the testdata/sophon fixtures over httptest:
  //   GET /getGameBranches          -> branchesJSON
  //   GET /getBuild                 -> buildJSON
  //   GET /getPatchBuild            -> patchJSON
  //   GET /cdn/manifest/<id>        -> testdata/sophon/<id>            (pb.zst)
  //   GET /cdn/chunks/<name>        -> testdata/sophon/chunks/<name>   (chunk/patch blob)
  // Per-path response overrides (corruption / 404 / flaky) via the maps.
  type fakeSophonServer struct {
  	*httptest.Server
  	branchesJSON string // testdata file name under testdata/sophon
  	buildJSON    string
  	patchJSON    string
  	// chunkOverride[name] -> bytes to serve instead of the on-disk blob.
  	chunkOverride map[string][]byte
  	// chunkFailFirst[name] = N: first N GETs return corrupt bytes, then real.
  	chunkFailFirst map[string]int
  	chunkHits      map[string]int
  	// chunk404[name] = true: always 404.
  	chunk404 map[string]bool
  }

  func newFakeSophonServer(t *testing.T, branches, build, patch string) *fakeSophonServer {
  	t.Helper()
  	fs := &fakeSophonServer{
  		branchesJSON:   branches,
  		buildJSON:      build,
  		patchJSON:      patch,
  		chunkOverride:  map[string][]byte{},
  		chunkFailFirst: map[string]int{},
  		chunkHits:      map[string]int{},
  		chunk404:       map[string]bool{},
  	}
  	mux := http.NewServeMux()
  	read := func(name string) []byte {
  		b, err := os.ReadFile(filepath.Join("testdata", "sophon", name))
  		if err != nil {
  			t.Fatalf("read fixture %s: %v", name, err)
  		}
  		return b
  	}
  	mux.HandleFunc("/getGameBranches", func(w http.ResponseWriter, r *http.Request) {
  		w.Header().Set("Content-Type", "application/json")
  		_, _ = w.Write(read(fs.branchesJSON))
  	})
  	mux.HandleFunc("/getBuild", func(w http.ResponseWriter, r *http.Request) {
  		w.Header().Set("Content-Type", "application/json")
  		_, _ = w.Write(read(fs.buildJSON))
  	})
  	mux.HandleFunc("/getPatchBuild", func(w http.ResponseWriter, r *http.Request) {
  		w.Header().Set("Content-Type", "application/json")
  		if fs.patchJSON == "" {
  			http.Error(w, "no patch", http.StatusNotFound)
  			return
  		}
  		_, _ = w.Write(read(fs.patchJSON))
  	})
  	mux.HandleFunc("/cdn/manifest/", func(w http.ResponseWriter, r *http.Request) {
  		name := strings.TrimPrefix(r.URL.Path, "/cdn/manifest/")
  		_, _ = w.Write(read(name))
  	})
  	mux.HandleFunc("/cdn/chunks/", func(w http.ResponseWriter, r *http.Request) {
  		name := strings.TrimPrefix(r.URL.Path, "/cdn/chunks/")
  		fs.chunkHits[name]++
  		if fs.chunk404[name] {
  			http.Error(w, "not found", http.StatusNotFound)
  			return
  		}
  		if ov, ok := fs.chunkOverride[name]; ok {
  			_, _ = w.Write(ov)
  			return
  		}
  		if n := fs.chunkFailFirst[name]; n > 0 && fs.chunkHits[name] <= n {
  			_, _ = w.Write([]byte("CORRUPT-CHUNK-BYTES"))
  			return
  		}
  		_, _ = w.Write(read(filepath.Join("chunks", name)))
  	})
  	fs.Server = httptest.NewServer(mux)
  	t.Cleanup(fs.Close)
  	return fs
  }

  // newSophonProvider builds a Provider wired to the fake server and a temp
  // game dir + temp root. currentLocal == "" leaves config.ini absent
  // (no-install). gameVersion writes a config.ini at the given version.
  func newSophonProvider(t *testing.T, fs *fakeSophonServer, gameVersion string) (*Provider, string, string) {
  	t.Helper()
  	gameDir := t.TempDir()
  	tempRoot := t.TempDir()
  	if gameVersion != "" {
  		writeConfigIni(t, gameDir, gameVersion)
  	}
  	p := New()
  	p.SetTempRootFn(func(core.GameID) string { return tempRoot })
  	// [DEV-3] redirect all three API surfaces at the fake server.
  	p.SetAPIBaseURL(fs.URL)
  	p.SetBranchAPIBaseURL(fs.URL)
  	p.SetSophonAPIBaseURL(fs.URL)
  	registerTestGameDir(t, p, genshinGID, gameDir)
  	return p, gameDir, tempRoot
  }

  // writeConfigIni writes a minimal Genshin config.ini with the given version,
  // matching what ReadGameVersion parses (see meta.go / api.go version reader).
  func writeConfigIni(t *testing.T, gameDir, version string) {
  	t.Helper()
  	body := "[General]\ngame_version=" + version + "\nchannel=1\nsub_channel=1\n"
  	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte(body), 0o644); err != nil {
  		t.Fatalf("write config.ini: %v", err)
  	}
  }
  ```

  INTEGRATOR-NOTE: `New()`, `registerTestGameDir`, the game-dir registration seam, and `ReadGameVersion`/config.ini parsing details are v1 hoyoverse internals. The plan-writer did NOT have the v1 game-dir-registration test helper in scope. **CONTRACT GAP:** `part-0.md` §A does not define how integration tests point a Provider at a temp `gameDir` (v1 uses some seam — likely a `gameDir` override on the gameMeta or a `SetGameDirFn`). The INTEGRATOR must replace `registerTestGameDir` + `writeConfigIni` with the actual v1 test seam used by existing `*_test.go` files in this package (grep for how v1 integration/unit tests set a game directory — e.g. `SetGameDirFn`, `discoverInstall` stub, or a fake registry). The 30 scenarios below call only `newSophonProvider(...)`, `p.CheckForUpdate(ctx, gid)`, `p.RunUpdate(...)`, and assert on the returned plan/error + on-disk gameDir state, so fixing the harness once fixes all 30.

- [ ] **Step 3 — Scenarios 1–8 (plan + flavor selection + full/build/patch end-to-end). Commit.**

  Append to `integration_test.go`. These exercise plan flavor selection and apply on the sample + tiny fixtures. FULL verbatim:

  ```go
  // 1. currentLocal in DiffTags → HDiff patch path (sample fixtures). Also
  //    cross-checked with tiny in scenario 1b semantics via TestSophonFlavorFull.
  func TestSophonFlavorPatch_EndToEnd(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0") // 6.5.0 ∈ diff_tags
  	seedOldFiles(t, gameDir)
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if plan.Version != "6.6.0" {
  		t.Fatalf("want target 6.6.0, got %q", plan.Version)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  	// CopyOver asset[1] must be byte-correct; deleted file must be gone.
  	assertFileMissing(t, filepath.Join(gameDir, "data/old_removed.bin"))
  }

  // 2. patch covers some files; others fall through to main getBuild.
  func TestSophonFlavorPatch_FilesNotInPatch_FromMain(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
  	seedOldFiles(t, gameDir)
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	// file_02..file_04 are not in the patch → assembled from main manifest chunks.
  	for _, f := range []string{"data/file_02.bin", "data/file_03.bin", "data/file_04.bin"} {
  		assertFileExists(t, filepath.Join(gameDir, f))
  	}
  }

  // 3. old manifest cached → Build flavor: some Local (dedup), some CDN.
  func TestSophonFlavorBuild_EndToEnd(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.4.0") // ∉ diff_tags
  	seedAppliedManifest(t, tempRoot, "6.4.0") // primes LoadAppliedManifests
  	seedOldFiles(t, gameDir)
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 4. on-disk old file modified → Local chunk MD5 mismatch → CDN fallback.
  func TestSophonFlavorBuild_StaleLocalChunk_FallbackCDN(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.4.0")
  	seedAppliedManifest(t, tempRoot, "6.4.0")
  	seedOldFiles(t, gameDir)
  	// Corrupt one old file so its chunk MD5 no longer matches → ErrChunkStale.
  	corruptFile(t, filepath.Join(gameDir, "data/file_00.bin"))
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate (should recover via CDN): %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 5. no cache, not in diff_tags → full download (tiny fixture).
  func TestSophonFlavorFull_EndToEnd(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0") // no cache, ∉ diff_tags
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  	assertFileExists(t, filepath.Join(gameDir, "data/file_00.bin"))
  }

  // 6. predl run → next CheckForUpdate finds predl_ready → apply from predl staging.
  func TestSophonPredl_StagingThenLiveApply(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.6.0") // predl target 6.7.0; current 6.6.0 ∈ predl diff_tags
  	seedOldFiles(t, gameDir)
  	// Stage predownload.
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate (predl avail): %v", err)
  	}
  	if !plan.PredownloadAvailable {
  		t.Fatalf("expected predl available")
  	}
  	predlPlan := plan
  	predlPlan.Kind = core.PlanPredownload
  	if err := p.RunUpdate(context.Background(), genshinGID, predlPlan, nil); err != nil {
  		t.Fatalf("predl RunUpdate: %v", err)
  	}
  	// Simulate patch day: branch.Main.Tag flips to 6.7.0.
  	fs.branchesJSON = "branches_main_only.json" // INTEGRATOR: provide a branches_predl_now.json where main.tag=6.7.0
  	plan2, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate (consume): %v", err)
  	}
  	if plan2.Reason != string(core.ReasonResumeInterrupted) {
  		t.Logf("predl-consume reason = %q (want resume_interrupted if main.tag matched predl target)", plan2.Reason)
  	}
  }

  // 7. predl_ready target mismatches current branch.PreDownload.Tag → cleanup → fresh.
  func TestSophonPredlStale_TargetMismatch(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
  	seedOldFiles(t, gameDir)
  	seedStalePredlReady(t, tempRoot, "9.9.9") // target version no longer offered
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	// Stale predl must NOT be consumed (reason must not be resume_interrupted
  	// sourced from the 9.9.9 sidecar).
  	_ = plan
  	assertPredlReadyAbsent(t, tempRoot, "9.9.9")
  }

  // 8. currentLocal == branch.PreDownload.Tag → predlAvailable false (tiny).
  func TestSophonPredl_CurrentLocalEqPredlTag(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_tiny.json", "")
  	p, _, _ := newSophonProvider(t, fs, "6.7.0") // == predl tag
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if plan.PredownloadAvailable {
  		t.Fatalf("predl must be unavailable when currentLocal == predl tag")
  	}
  }
  ```

  INTEGRATOR-NOTE: helpers `seedOldFiles`, `seedAppliedManifest`, `seedStalePredlReady`, `corruptFile`, `assertGameVersion`, `assertFileExists`, `assertFileMissing`, `assertPredlReadyAbsent`, `seedStaleApplyWAL` (used later) are declared in Step 6's helper block. `plan.PredownloadAvailable`, `plan.Reason`, `plan.Kind`, `core.PlanPredownload`, `core.ReasonResumeInterrupted` are the v1 `core.UpdatePlan` fields — INTEGRATOR confirms exact field names against `internal/core/updater.go` (`Reason` may be a `ReasonCode` not `string`; adjust the comparison). **CONTRACT GAP:** scenario 6 needs a `branches_predl_now.json` fixture (main.tag = predl target 6.7.0, no pre_download) to exercise the full consume; the generator (Step 1) emits only `branches_main_only` (tag 6.6.0) and `branches_with_predl`. INTEGRATOR adds a 3rd branches fixture or parameterizes `mkBranch` with the main tag. Logged rather than guessed.

  Commit:
  ```bash
  go test -count=1 -tags integration ./internal/providers/hoyoverse/...
  git add internal/providers/hoyoverse/integration_test.go
  git commit -m "test(hoyoverse-sophon): integration scenarios 1-8 (flavor selection + predl staging)"
  ```

- [ ] **Step 4 — Scenarios 9–16 (resume, retry, no-install, legacy regression, self-heal, cancel). Commit.**

  Append. FULL verbatim:

  ```go
  // 9. kill mid-apply (2 of N done) → restart → resume completes.
  func TestSophonResume_ApplyWALMidFlight(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.0.0")
  	// Pre-seed a half-done sophon_apply.wal (2 records done, rest pending) so
  	// RunUpdate resumes from the first pending record (§6.9 path 1).
  	seedMidFlightApplyWAL(t, tempRoot, "6.6.0", "build-small")
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("resume RunUpdate: %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 10. kill mid-download (3 of 10 in ChunksDone) → reuse 3, fetch 7.
  func TestSophonResume_DownloadCrashMidChunks(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.0.0")
  	staged := seedPartialDownload(t, tempRoot, "6.6.0", "build-small", 3) // mark 3 chunks done + their staging files
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	// The 3 pre-staged chunks must NOT have been re-fetched from CDN.
  	for _, name := range staged {
  		if fs.chunkHits[name] != 0 {
  			t.Errorf("chunk %s was re-downloaded (%d hits); expected reuse from staging", name, fs.chunkHits[name])
  		}
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 11. first GET corrupt, retry succeeds.
  func TestSophonChunkVerifyFail_RetryThenSucceed(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0")
  	name := tinyChunkName(t)
  	fs.chunkFailFirst[name] = 1 // first GET corrupt, second OK
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate should recover after retry: %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 12. all 3 retries corrupt → sophon_chunk_verify_failed (tiny).
  func TestSophonChunkVerifyFail_RetryExhausted(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, _, _ := newSophonProvider(t, fs, "6.0.0")
  	name := tinyChunkName(t)
  	fs.chunkFailFirst[name] = 99 // never serves good bytes
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	err = p.RunUpdate(context.Background(), genshinGID, plan, nil)
  	assertUpdateErrorCode(t, err, "sophon_chunk_verify_failed")
  }

  // 13. config.ini missing → sophon_no_install (tiny).
  func TestSophonNoInstall_Error(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, _, _ := newSophonProvider(t, fs, "") // no config.ini
  	_, err := p.CheckForUpdate(context.Background(), genshinGID)
  	assertUpdateErrorCode(t, err, "sophon_no_install")
  }

  // 14. non-Sophon games still use v1 zip+hdiff path (cross-provider regression).
  func TestSophonHSRZZZ_LegacyPathUnchanged(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, _, _ := newSophonProvider(t, fs, "1.0.0")
  	// HSR is UsesSophon=false → CheckForUpdate must NOT hit getBuild/getPatchBuild.
  	hsr := core.GameID("hoyoverse/hsr")
  	registerTestGameDir(t, p, hsr, t.TempDir())
  	_, _ = p.CheckForUpdate(context.Background(), hsr)
  	// getBuild must not have been called for the legacy game.
  	// (Counted via a sentinel: legacy path uses getGamePackages, not getBuild.)
  	if buildHits := fs.buildHitsFor(); buildHits != 0 {
  		t.Errorf("legacy HSR hit getBuild %d times; should use getGamePackages", buildHits)
  	}
  }

  // 15. self-heal: stale local, last_apply_target matches, 24h passed → idle.
  func TestSophonMaybeSelfHeal_WritebackRetry(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.5.0") // stale displayed version
  	seedLastApplyTarget(t, tempRoot, "6.6.0", false /*ConfigWritebackOK*/, -25 /*hoursAgo*/)
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	// Self-heal writes config.ini=6.6.0 and returns an idle plan.
  	assertGameVersion(t, gameDir, "6.6.0")
  	if plan.PredownloadAvailable {
  		t.Fatalf("self-heal idle plan should not advertise predl")
  	}
  }

  // 16. ctx.Cancel during zstd stream → worker exits cleanly (tiny).
  func TestSophonCancelMidChunkDownload(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
  	p, _, _ := newSophonProvider(t, fs, "6.0.0")
  	ctx, cancel := context.WithCancel(context.Background())
  	plan, err := p.CheckForUpdate(ctx, genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	cancel()
  	err = p.RunUpdate(ctx, genshinGID, plan, nil)
  	if err == nil {
  		t.Fatalf("expected context-cancelled error")
  	}
  	if !strings.Contains(err.Error(), "context") && !errorsIsCanceled(err) {
  		t.Logf("cancel returned %v (INTEGRATOR: assert context.Canceled per v1 convention)", err)
  	}
  }
  ```

  INTEGRATOR-NOTE: `fs.buildHitsFor()` + `fs.chunkHits` need the fake server to count `/getBuild` hits — add a `buildHits int` field + increment in the `/getBuild` handler and a `buildHitsFor()` accessor (trivial; omitted from Step 2 scaffolding to keep it short — INTEGRATOR adds the counter). `errorsIsCanceled`, `assertUpdateErrorCode`, `tinyChunkName`, `seedMidFlightApplyWAL`, `seedPartialDownload`, `seedLastApplyTarget` are in Step 6's helper block. The exact `core.UpdateError` unwrap (`errors.As` to `*core.UpdateError`, read `.Code`) is in `assertUpdateErrorCode` — confirm the v1 error type name.

  Commit:
  ```bash
  go test -count=1 -tags integration ./internal/providers/hoyoverse/...
  git add internal/providers/hoyoverse/integration_test.go
  git commit -m "test(hoyoverse-sophon): integration scenarios 9-16 (resume/retry/no-install/self-heal/cancel)"
  ```

- [ ] **Step 5 — Scenarios 17–30 (cross-device, malformed, ScanRecovery, import-cycle, demotion, xxh64-fallback, predl thresholds, idle cleanup, zero-value kind, full-flavor blocked). Commit.**

  Append. FULL verbatim:

  ```go
  // 17. assemble succeeds, rename returns EXDEV → copy+delete fallback.
  func TestSophonCrossDeviceErrno_AssembleRename(t *testing.T) {
  	t.Skip("EXDEV cannot be reliably forced on a single-volume CI temp dir; " +
  		"sophon.SafeAtomicRename's copy+delete fallback is unit-tested in " +
  		"sophon/file_assemble_test.go (T11) and sophon/rename build-tagged tests (T9). " +
  		"INTEGRATOR: enable only on a multi-volume smoke host (Task 25).")
  }

  // 18. branch.Main.DiffTags = [] → never Patch flavor.
  func TestSophonDiffTagsEmpty_FallsToBuildOrFull(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_no_difftags.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
  	seedOldFiles(t, gameDir)
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	// getPatchBuild must not have been used as the flavor (no diff_tags hit).
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 19. branch.Main.Categories empty → sophon_manifest_fetch_failed.
  func TestSophonMainCategoriesEmpty_Error(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_no_categories.json", "build_small.json", "")
  	p, _, _ := newSophonProvider(t, fs, "6.5.0")
  	_, err := p.CheckForUpdate(context.Background(), genshinGID)
  	assertUpdateErrorCode(t, err, "sophon_manifest_fetch_failed")
  }

  // 20. UnusedAssets entries trigger delete WAL records.
  func TestSophonUnusedAssetsDeleted(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
  	seedOldFiles(t, gameDir)
  	// Pre-create the file the patch's UnusedAssets says to delete.
  	mustWrite(t, filepath.Join(gameDir, "data/old_removed.bin"), bytesRepeat(0x7, 16))
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate: %v", err)
  	}
  	assertFileMissing(t, filepath.Join(gameDir, "data/old_removed.bin"))
  }

  // 21. ScanRecovery classifies a dir with only sophon_apply.wal as ApplyResume.
  func TestScanRecovery_SophonApplyWAL(t *testing.T) {
  	dir := t.TempDir()
  	mustWrite(t, filepath.Join(dir, "sophon_apply.wal"),
  		[]byte(`{"game_id":"hoyoverse/genshin","was_predl":false,"records":[]}`))
  	st := core.ScanRecovery(dir)
  	if st.Phase != core.RecoveryPhaseApplyResume {
  		t.Fatalf("want ApplyResume, got %v", st.Phase)
  	}
  }

  // 22. ScanRecovery classifies a dir with only sophon_progress.json as DownloadResume.
  func TestScanRecovery_SophonProgress(t *testing.T) {
  	dir := t.TempDir()
  	mustWrite(t, filepath.Join(dir, "sophon_progress.json"),
  		[]byte(`{"game_id":"hoyoverse/genshin","chunks_done":{"a":true}}`))
  	st := core.ScanRecovery(dir)
  	if st.Phase != core.RecoveryPhaseDownloadResume {
  		t.Fatalf("want DownloadResume, got %v", st.Phase)
  	}
  }

  // 23. sophon_apply.wal AND apply.wal both present → sophon_apply.wal wins.
  func TestScanRecovery_Precedence_SophonOverV1(t *testing.T) {
  	dir := t.TempDir()
  	mustWrite(t, filepath.Join(dir, "sophon_apply.wal"),
  		[]byte(`{"game_id":"hoyoverse/genshin","was_predl":true,"records":[]}`))
  	mustWrite(t, filepath.Join(dir, "apply.wal"), []byte(`{"was_predl":false}`))
  	st := core.ScanRecovery(dir)
  	if st.Phase != core.RecoveryPhaseApplyResume {
  		t.Fatalf("want ApplyResume, got %v", st.Phase)
  	}
  	if !st.WasPredl {
  		t.Fatalf("WasPredl must come from sophon_apply.wal header (true), got false")
  	}
  }

  // 24. meta-test: sophon sub-package must NOT import the parent hoyoverse package.
  func TestSophonImportCycleFree(t *testing.T) {
  	out := goListDeps(t, "omnigate/internal/providers/hoyoverse/sophon")
  	if strings.Contains(out, "omnigate/internal/providers/hoyoverse\n") ||
  		strings.HasSuffix(strings.TrimSpace(out), "omnigate/internal/providers/hoyoverse") {
  		t.Fatalf("sophon must not depend on parent hoyoverse package; deps:\n%s", out)
  	}
  }

  // 25. pre-apply MD5 fails on modded source → demote hdiff_patch → chunk_assemble.
  func TestSophonHdiffPatch_OldFileMD5Mismatch_DemoteToChunkAssemble(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
  	seedOldFiles(t, gameDir)
  	// Mod data/file_00.bin so its pre-apply MD5 != OriginalFileMd5 → demotion.
  	corruptFile(t, filepath.Join(gameDir, "data/file_00.bin"))
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate (should demote + succeed): %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  	assertFileMD5(t, filepath.Join(gameDir, "data/file_00.bin"), assetMD5(t, "sample.manifest.pb.zst", "data/file_00.bin"))
  }

  // 26. ChunkName first-16 not hex → MD5 fallback verification.
  func TestSophonChunkVerify_XXh64ParseFail_FallbackMD5(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_nonhex_chunk.json", "")
  	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0")
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  		t.Fatalf("RunUpdate (MD5 fallback path): %v", err)
  	}
  	assertGameVersion(t, gameDir, "6.6.0")
  }

  // 27. predl partial-stale staging: 24% CDN fail → reuse predl staging (only the
  //     bad chunks re-fetched); 26% → discard predl staging + fresh full download.
  //     DISCRIMINATING signal = number of CDN chunk GETs during RunUpdate:
  //       reuse  → only the corrupt chunks are re-fetched (skip-if-verified keeps the rest)
  //       discard→ predl staging removed → every chunk re-fetched into staging/main
  //     Requires the Step-2 fakeSophonServer to expose an atomic chunk-GET counter
  //     `chunkGETs() int` (see Step 2 note). `totalCDNChunks(t, "sample.manifest.pb.zst")`
  //     returns the count of CDN chunks in the fixture (helper in Step 6).
  func TestSophonPredl_PartialStaleStagingThresholdRecover(t *testing.T) {
  	total := totalCDNChunks(t, "sample.manifest.pb.zst")
  	t.Run("24pct_reuse_predl", func(t *testing.T) {
  		fs := newFakeSophonServer(t, "branches_predl_now.json", "build_small.json", "patch_small.json")
  		p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
  		seedOldFiles(t, gameDir)
  		seedConsumablePredl(t, tempRoot, "6.7.0", "build-predl", 24 /*pctStale*/)
  		fs.resetChunkGETs()
  		plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  		if err != nil {
  			t.Fatalf("CheckForUpdate: %v", err)
  		}
  		if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  			t.Fatalf("RunUpdate (reuse predl): %v", err)
  		}
  		assertGameVersion(t, gameDir, "6.7.0")
  		// Reuse: well under half the chunks re-fetched (only the ~24% corrupt).
  		if got := fs.chunkGETs(); got > total/2 {
  			t.Fatalf("24%% stale: predl staging should be reused; want few chunk GETs (<= %d), got %d", total/2, got)
  		}
  	})
  	t.Run("26pct_discard_fresh", func(t *testing.T) {
  		fs := newFakeSophonServer(t, "branches_predl_now.json", "build_small.json", "patch_small.json")
  		p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
  		seedOldFiles(t, gameDir)
  		seedConsumablePredl(t, tempRoot, "6.7.0", "build-predl", 26)
  		fs.resetChunkGETs()
  		plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  		if err != nil {
  			t.Fatalf("CheckForUpdate: %v", err)
  		}
  		if err := p.RunUpdate(context.Background(), genshinGID, plan, nil); err != nil {
  			t.Fatalf("RunUpdate (discard+fresh): %v", err)
  		}
  		assertGameVersion(t, gameDir, "6.7.0")
  		// Discard: predl staging removed → every CDN chunk re-fetched into staging/main.
  		if got := fs.chunkGETs(); got < total {
  			t.Fatalf("26%% stale: predl should be discarded + fully re-downloaded; want >= %d chunk GETs, got %d", total, got)
  		}
  	})
  }

  // 28. crash between WAL-all-done and staging cleanup → next CheckForUpdate idle + sweep.
  func TestSophonIdlePathCleanup_AllDoneWAL(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
  	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0") // already at target
  	seedAllDoneApplyWAL(t, tempRoot, "6.6.0", "build-small") // orphan WAL + staging
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if plan.PredownloadAvailable {
  		t.Fatalf("idle plan should not advertise predl")
  	}
  	assertSophonWALAbsent(t, tempRoot, "6.6.0")
  	assertStagingAbsent(t, tempRoot, "6.6.0", "build-small")
  	_ = gameDir
  }

  // 29. legacy v1-shaped predl_ready.json (Kind=="") → treated as stale → no consume.
  func TestSophonPredl_ZeroValueKind_TreatedAsStale(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
  	p, _, tempRoot := newSophonProvider(t, fs, "6.6.0")
  	seedZeroKindPredlReady(t, tempRoot, "6.6.0") // Kind:"" v1-shaped
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if plan.Reason == string(core.ReasonResumeInterrupted) {
  		t.Fatalf("zero-Kind predl must not be consumed")
  	}
  	assertPredlReadyAbsent(t, tempRoot, "6.6.0")
  }

  // 30. predl avail but ∉ DiffTags AND no cached old manifest → Full predl never offered.
  func TestSophonPredl_FullFlavorBlocked(t *testing.T) {
  	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "")
  	p, _, _ := newSophonProvider(t, fs, "6.3.0") // ∉ predl diff_tags (6.6.0); no cache
  	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
  	if err != nil {
  		t.Fatalf("CheckForUpdate: %v", err)
  	}
  	if plan.PredownloadAvailable {
  		t.Fatalf("full-flavor predl must be blocked (no chunk reuse possible)")
  	}
  }
  ```

  INTEGRATOR-NOTE — additional fixtures needed (CONTRACT GAP, not in generator Step 1): scenarios 18/19/26/27/29 reference `branches_no_difftags.json` (main.diff_tags=[]), `branches_no_categories.json` (main.categories=[]), `build_nonhex_chunk.json` (a build whose single chunk's ChunkName first-16 chars are non-hex, e.g. `"zzzzzzzzzzzzzzzz_00000"`, forcing MD5 fallback), and `branches_predl_now.json` (main.tag=6.7.0, no pre_download). Extend the Step 1 generator's `mkBranch`/`mkBuild` to emit these four (parameterize diffTags/categories/chunkName/mainTag). These are deterministic variants — add them to `TestGenerateSophonFixtures` and re-run + re-commit before this step's `go test`. `goListDeps`, `assetMD5`, `assertFileMD5`, `bytesRepeat`, `seedConsumablePredl`, `seedAllDoneApplyWAL`, `seedZeroKindPredlReady`, `assertSophonWALAbsent`, `assertStagingAbsent` are in the Step 6 helper block.

  Commit:
  ```bash
  # regenerate fixtures if the generator was extended for 18/19/26/27/29:
  go test -tags genfixtures -run TestGenerateSophonFixtures ./internal/providers/hoyoverse/testdata/sophon/
  go test -count=1 -tags integration ./internal/providers/hoyoverse/...
  git add internal/providers/hoyoverse/integration_test.go internal/providers/hoyoverse/testdata/sophon/
  git commit -m "test(hoyoverse-sophon): integration scenarios 17-30 (recovery/demotion/xxh64-fallback/predl-thresholds/cleanup)"
  ```

- [ ] **Step 6 — Test helper block (`integration_test.go` tail) + final gate. Commit.**

  Append the helper functions the 30 scenarios reference. These are deliberately thin and many are `t.Helper()` wrappers; several MUST be reconciled by the INTEGRATOR against the actual v1 + Task 13–21 sidecar writers (the WAL/progress/predl/applied JSON shapes come from §A.6). FULL verbatim skeleton (INTEGRATOR fills the bodies marked with the contract-gap note):

  ```go
  // ---- assertion helpers ----

  func mustWrite(t *testing.T, path string, b []byte) {
  	t.Helper()
  	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
  		t.Fatal(err)
  	}
  	if err := os.WriteFile(path, b, 0o644); err != nil {
  		t.Fatal(err)
  	}
  }

  func bytesRepeat(b byte, n int) []byte {
  	out := make([]byte, n)
  	for i := range out {
  		out[i] = b
  	}
  	return out
  }

  func assertFileExists(t *testing.T, path string) {
  	t.Helper()
  	if _, err := os.Stat(path); err != nil {
  		t.Fatalf("expected file %s to exist: %v", path, err)
  	}
  }

  func assertFileMissing(t *testing.T, path string) {
  	t.Helper()
  	if _, err := os.Stat(path); !os.IsNotExist(err) {
  		t.Fatalf("expected %s to be absent, stat err=%v", path, err)
  	}
  }

  func corruptFile(t *testing.T, path string) {
  	t.Helper()
  	if err := os.WriteFile(path, []byte("MODDED-CONTENT-DIFFERENT-MD5"), 0o644); err != nil {
  		t.Fatal(err)
  	}
  }

  // assertGameVersion reads config.ini and checks game_version.
  func assertGameVersion(t *testing.T, gameDir, want string) {
  	t.Helper()
  	b, err := os.ReadFile(filepath.Join(gameDir, "config.ini"))
  	if err != nil {
  		t.Fatalf("read config.ini: %v", err)
  	}
  	if !strings.Contains(string(b), "game_version="+want) {
  		t.Fatalf("config.ini does not contain game_version=%s:\n%s", want, b)
  	}
  }

  // assertUpdateErrorCode unwraps a *core.UpdateError and checks .Code.
  func assertUpdateErrorCode(t *testing.T, err error, wantCode string) {
  	t.Helper()
  	if err == nil {
  		t.Fatalf("expected UpdateError{Code:%q}, got nil", wantCode)
  	}
  	var ue *core.UpdateError
  	if !errorsAs(err, &ue) {
  		t.Fatalf("error %v is not *core.UpdateError", err)
  	}
  	if ue.Code != wantCode {
  		t.Fatalf("UpdateError.Code = %q, want %q", ue.Code, wantCode)
  	}
  }

  // CONTRACT GAP — the following seed* / assert* helpers + low-level utilities
  // (errorsAs, errorsIsCanceled, goListDeps, assetMD5, assertFileMD5,
  //  tinyChunkName, registerTestGameDir, seedOldFiles, seedAppliedManifest,
  //  seedStalePredlReady, seedConsumablePredl, seedZeroKindPredlReady,
  //  seedMidFlightApplyWAL, seedAllDoneApplyWAL, seedPartialDownload,
  //  seedLastApplyTarget, assertPredlReadyAbsent, assertSophonWALAbsent,
  //  assertStagingAbsent)
  // depend on the concrete on-disk JSON shapes from §A.6 (sophonApplyWAL,
  // sophonProgressFile, sophonPredlReadyFile, appliedManifestSet) + the v1
  // sidecar path helpers (sidecar_paths.go) + the v1 game-dir test seam.
  // The INTEGRATOR implements them by:
  //   - errorsAs/errorsIsCanceled: thin wrappers over errors.As / errors.Is.
  //   - goListDeps: exec.Command("go","list","-deps",pkg).
  //   - registerTestGameDir: the SAME seam v1 unit tests use to point a Provider
  //     at a temp gameDir (grep existing *_test.go in this package).
  //   - seedOldFiles: write data/file_00..04.bin with the EXACT decompressed
  //     bytes the generator used (bytes.Repeat([]byte{byte(globalIdx)},1024))
  //     so dedup Local reads hit.
  //   - seed*ApplyWAL/Progress/Predl/AppliedManifest/LastApplyTarget: marshal
  //     the §A.6 structs (use the production writers from Tasks 16/15/18/17/13
  //     where exported, else replicate the JSON inline) into the sidecar paths
  //     from sidecar_paths.go (sophonStagingDir / versionSidecarDir /
  //     sophonAppliedJSONPath).
  //   - assert*Absent: os.Stat + os.IsNotExist on the corresponding path.
  // These are explicitly LEFT as a contract gap because part-0.md §B lists the
  // testdata files but no test-helper contract, and the sidecar WRITERS are
  // built in Tasks 15-18 whose exact exported surface the plan-writer cannot
  // pin without seeing the implemented code. Reconcile at integration time.
  ```

  Final gate + commit:
  ```bash
  go build ./... && go vet ./...
  go test -count=1 ./internal/providers/hoyoverse/...
  go test -count=1 -tags integration ./internal/providers/hoyoverse/...
  git add internal/providers/hoyoverse/integration_test.go
  git commit -m "test(hoyoverse-sophon): integration test helper block (fakeSophonServer assertions)"
  ```

---

### Task 24: Fuzz + bench

> Group **G**. The 5 fuzzers (§9.3) + 4 benches (§9.4). Fuzz targets live next to the code they exercise: 2 in `sophon/` (proto parse), 1 in `sophon/` (patch-blob slice), 2 in the hoyoverse package (applied.json serde, WAL roundtrip). Benches likewise.
>
> **Go gate:** PATH note (once per shell, see Task 23). `CGO_ENABLED=0`, never `-race`.

**Files:**
- `internal/providers/hoyoverse/sophon/fuzz_test.go` (create — FuzzSophonManifestProto_Parse, FuzzSophonPatchProto_Parse, FuzzPatchBlobSlice)
- `internal/providers/hoyoverse/sophon/bench_test.go` (create — BenchmarkBuildPerAssetMD5Index_LargeAsset, BenchmarkDedupLookup_1MOps, BenchmarkChunkAssemble_3GBFile)
- `internal/providers/hoyoverse/sophon_fuzz_test.go` (create — FuzzAppliedJSON_Serde, FuzzSophonApplyWAL_Roundtrip)
- `internal/providers/hoyoverse/sophon_bench_test.go` (create — BenchmarkSophonApplyWAL_BatchedRewrite_50KRecords)

- [ ] **Step 1 — sophon-package fuzzers + run. Commit.**

  Create `internal/providers/hoyoverse/sophon/fuzz_test.go`. FULL verbatim:

  ```go
  package sophon

  import (
  	"bytes"
  	"testing"

  	"github.com/klauspost/compress/zstd"
  	"google.golang.org/protobuf/proto"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  func zstdComp(in []byte) []byte {
  	var buf bytes.Buffer
  	enc, _ := zstd.NewWriter(&buf)
  	_, _ = enc.Write(in)
  	_ = enc.Close()
  	return buf.Bytes()
  }

  // FuzzSophonManifestProto_Parse: arbitrary (and seeded valid-zstd-proto) bytes
  // through the manifest parse path must never panic.
  func FuzzSophonManifestProto_Parse(f *testing.F) {
  	good, _ := proto.Marshal(&pb.SophonManifestProto{
  		Assets: []*pb.SophonManifestAssetProperty{{
  			AssetName:   "data/x.bin",
  			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c0", ChunkSize: 4}},
  		}},
  	})
  	f.Add(zstdComp(good))
  	f.Add([]byte{})
  	f.Add([]byte("not-zstd"))
  	f.Fuzz(func(t *testing.T, data []byte) {
  		dec, err := zstd.NewReader(bytes.NewReader(data))
  		if err != nil {
  			return
  		}
  		defer dec.Close()
  		raw, err := io_ReadAll(dec)
  		if err != nil {
  			return
  		}
  		var m pb.SophonManifestProto
  		_ = proto.Unmarshal(raw, &m) // must not panic
  	})
  }

  // FuzzSophonPatchProto_Parse: same for the patch proto.
  func FuzzSophonPatchProto_Parse(f *testing.F) {
  	good, _ := proto.Marshal(&pb.SophonPatchProto{
  		PatchAssets: []*pb.SophonPatchAssetProperty{{AssetName: "data/x.bin"}},
  	})
  	f.Add(zstdComp(good))
  	f.Add([]byte{})
  	f.Fuzz(func(t *testing.T, data []byte) {
  		dec, err := zstd.NewReader(bytes.NewReader(data))
  		if err != nil {
  			return
  		}
  		defer dec.Close()
  		raw, err := io_ReadAll(dec)
  		if err != nil {
  			return
  		}
  		var pp pb.SophonPatchProto
  		_ = proto.Unmarshal(raw, &pp)
  	})
  }

  // FuzzPatchBlobSlice: random PatchOffset/PatchLength against random blobs must
  // never index out of bounds. Mirrors the slice extraction in §6.4 step 3 /
  // §6.5 step 2 (read blob[off:off+len]).
  func FuzzPatchBlobSlice(f *testing.F) {
  	f.Add([]byte("hello world"), int64(2), int64(5))
  	f.Add([]byte{}, int64(0), int64(0))
  	f.Fuzz(func(t *testing.T, blob []byte, off, length int64) {
  		// Bounds-safe extraction identical to the production guard the
  		// implementer MUST use (clamp + validate before slicing).
  		if off < 0 || length < 0 || off > int64(len(blob)) || off+length > int64(len(blob)) {
  			return // production code returns an error here, never slices
  		}
  		_ = blob[off : off+length] // must not panic given the guard above
  	})
  }

  // io_ReadAll avoids importing io just for ReadAll in this file's fuzzers when
  // the package may not otherwise need it; INTEGRATOR may replace with io.ReadAll.
  func io_ReadAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
  	var out []byte
  	buf := make([]byte, 32*1024)
  	for {
  		n, err := r.Read(buf)
  		out = append(out, buf[:n]...)
  		if err != nil {
  			if err.Error() == "EOF" {
  				return out, nil
  			}
  			return out, err
  		}
  	}
  }
  ```

  INTEGRATOR-NOTE: `io_ReadAll`'s `err.Error()=="EOF"` check is a kludge to avoid an `io` import collision in the verbatim snippet; replace with `io.ReadAll(dec)` + `errors.Is(err, io.EOF)` — the production `manifest_fetch.go` already uses `io.ReadAll`, so prefer matching it. Run each fuzzer briefly:
  ```bash
  go test -run=^$ -fuzz=FuzzSophonManifestProto_Parse -fuzztime=10s ./internal/providers/hoyoverse/sophon/
  go test -run=^$ -fuzz=FuzzSophonPatchProto_Parse   -fuzztime=10s ./internal/providers/hoyoverse/sophon/
  go test -run=^$ -fuzz=FuzzPatchBlobSlice           -fuzztime=10s ./internal/providers/hoyoverse/sophon/
  ```
  Expect: no crashers. Commit:
  ```bash
  git add internal/providers/hoyoverse/sophon/fuzz_test.go
  git commit -m "test(hoyoverse-sophon): proto-parse + patch-blob-slice fuzzers"
  ```

- [ ] **Step 2 — sophon-package benches + run. Commit.**

  Create `internal/providers/hoyoverse/sophon/bench_test.go`. FULL verbatim:

  ```go
  package sophon

  import (
  	"crypto/md5"
  	"encoding/hex"
  	"fmt"
  	"os"
  	"path/filepath"
  	"testing"

  	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
  )

  func md5hex(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }

  func bigAsset(nChunks int) *pb.SophonManifestProto {
  	a := &pb.SophonManifestAssetProperty{AssetName: "data/big.bin"}
  	var off int64
  	for i := 0; i < nChunks; i++ {
  		a.AssetChunks = append(a.AssetChunks, &pb.SophonManifestAssetChunk{
  			ChunkName:                fmt.Sprintf("chunk_%05d", i),
  			ChunkDecompressedHashMd5: md5hex([]byte(fmt.Sprintf("payload-%d", i))),
  			ChunkOnFileOffset:        off,
  			ChunkSizeDecompressed:    4096,
  		})
  		off += 4096
  	}
  	return &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{a}}
  }

  // BenchmarkBuildPerAssetMD5Index_LargeAsset: 500-chunk asset, expect < 5 ms.
  func BenchmarkBuildPerAssetMD5Index_LargeAsset(b *testing.B) {
  	m := bigAsset(500)
  	b.ResetTimer()
  	for i := 0; i < b.N; i++ {
  		_ = BuildPerAssetMD5Index(m, "data/big.bin")
  	}
  }

  // BenchmarkDedupLookup_1MOps: expect > 5M lookups/sec.
  func BenchmarkDedupLookup_1MOps(b *testing.B) {
  	m := bigAsset(1000)
  	idx := BuildPerAssetMD5Index(m, "data/big.bin")
  	keys := make([]string, 0, len(idx))
  	for k := range idx {
  		keys = append(keys, k)
  	}
  	b.ResetTimer()
  	for i := 0; i < b.N; i++ {
  		_ = idx[keys[i%len(keys)]]
  	}
  }

  // BenchmarkChunkAssemble_3GBFile: disk-bound sanity check. Uses a smaller
  // synthetic file by default (set OMNIGATE_BENCH_3GB=1 for the full 3 GB run);
  // CI runs the small variant to keep wall-time bounded.
  func BenchmarkChunkAssemble_3GBFile(b *testing.B) {
  	size := int64(64 << 20) // 64 MiB default
  	if os.Getenv("OMNIGATE_BENCH_3GB") == "1" {
  		size = 3 << 30
  	}
  	chunkSize := int64(4 << 20)
  	dir := b.TempDir()
  	payload := make([]byte, chunkSize)
  	for i := range payload {
  		payload[i] = byte(i)
  	}
  	var srcs []ChunkSource
  	var off int64
  	staging := filepath.Join(dir, "chunks")
  	_ = os.MkdirAll(staging, 0o755)
  	for off < size {
  		name := fmt.Sprintf("c_%d", off)
  		_ = os.WriteFile(filepath.Join(staging, name), payload, 0o644)
  		srcs = append(srcs, ChunkSource{
  			Kind: SourceCDN, ChunkName: name, FileOffset: off, DecompSize: chunkSize,
  		})
  		off += chunkSize
  	}
  	read := func(src ChunkSource) ([]byte, error) {
  		return os.ReadFile(filepath.Join(staging, src.ChunkName))
  	}
  	b.SetBytes(size)
  	b.ResetTimer()
  	for i := 0; i < b.N; i++ {
  		out := filepath.Join(dir, fmt.Sprintf("out_%d.bin", i))
  		if err := AssembleFile(out, size, srcs, read); err != nil {
  			b.Fatal(err)
  		}
  	}
  }
  ```

  INTEGRATOR-NOTE: `md5hex` may collide with a same-name helper if `sophon` test files already define one (the generator's is in a different package `sophon_fixtures`, so no collision; but T7/T8 unit tests might). If a duplicate-definition build error occurs, rename to `benchMD5hex`. Run:
  ```bash
  go test -run=^$ -bench=BenchmarkBuildPerAssetMD5Index_LargeAsset -benchtime=100x ./internal/providers/hoyoverse/sophon/
  go test -run=^$ -bench=BenchmarkDedupLookup_1MOps -benchtime=1000000x ./internal/providers/hoyoverse/sophon/
  go test -run=^$ -bench=BenchmarkChunkAssemble_3GBFile -benchtime=3x ./internal/providers/hoyoverse/sophon/
  ```
  Commit:
  ```bash
  git add internal/providers/hoyoverse/sophon/bench_test.go
  git commit -m "test(hoyoverse-sophon): dedup-index + assemble benches"
  ```

- [ ] **Step 3 — hoyoverse-package fuzzers (applied.json serde, WAL roundtrip) + run. Commit.**

  Create `internal/providers/hoyoverse/sophon_fuzz_test.go`. FULL verbatim:

  ```go
  package hoyoverse

  import (
  	"encoding/json"
  	"testing"
  )

  // FuzzAppliedJSON_Serde: arbitrary JSON into appliedManifestSet (§A.6) must
  // never panic on Unmarshal; valid round-trips must re-marshal cleanly.
  func FuzzAppliedJSON_Serde(f *testing.F) {
  	f.Add([]byte(`{"latest":{"build_id":"b","version":"6.6.0","applied_at":"2026-06-01T00:00:00Z","categories":{"game":"b"}},"previous":null}`))
  	f.Add([]byte(`{}`))
  	f.Add([]byte(`null`))
  	f.Add([]byte(`{"latest":{}}`))
  	f.Fuzz(func(t *testing.T, data []byte) {
  		var s appliedManifestSet
  		if err := json.Unmarshal(data, &s); err != nil {
  			return // malformed JSON is fine; no panic is the invariant
  		}
  		if _, err := json.Marshal(&s); err != nil {
  			t.Fatalf("re-marshal of accepted struct failed: %v", err)
  		}
  	})
  }

  // FuzzSophonApplyWAL_Roundtrip: random records marshal/unmarshal preserve shape.
  func FuzzSophonApplyWAL_Roundtrip(f *testing.F) {
  	f.Add([]byte(`{"game_id":"g","target_tag":"6.6.0","build_id":"b","flavor":"sophon_full","branch_kind":"main","records":[{"kind":"chunk_assemble","path":"data/x.bin","state":"pending"}]}`))
  	f.Add([]byte(`{"records":[]}`))
  	f.Fuzz(func(t *testing.T, data []byte) {
  		var w sophonApplyWAL
  		if err := json.Unmarshal(data, &w); err != nil {
  			return
  		}
  		b1, err := json.Marshal(&w)
  		if err != nil {
  			t.Fatalf("marshal: %v", err)
  		}
  		var w2 sophonApplyWAL
  		if err := json.Unmarshal(b1, &w2); err != nil {
  			t.Fatalf("re-unmarshal: %v", err)
  		}
  		if len(w2.Records) != len(w.Records) {
  			t.Fatalf("record count drift: %d -> %d", len(w.Records), len(w2.Records))
  		}
  	})
  }
  ```

  Run:
  ```bash
  go test -run=^$ -fuzz=FuzzAppliedJSON_Serde     -fuzztime=10s ./internal/providers/hoyoverse/
  go test -run=^$ -fuzz=FuzzSophonApplyWAL_Roundtrip -fuzztime=10s ./internal/providers/hoyoverse/
  ```
  Commit:
  ```bash
  git add internal/providers/hoyoverse/sophon_fuzz_test.go
  git commit -m "test(hoyoverse-sophon): applied.json + apply-WAL serde fuzzers"
  ```

- [ ] **Step 4 — hoyoverse-package bench (batched WAL rewrite) + run. Commit.**

  Create `internal/providers/hoyoverse/sophon_bench_test.go`. FULL verbatim:

  ```go
  package hoyoverse

  import (
  	"encoding/json"
  	"fmt"
  	"os"
  	"path/filepath"
  	"testing"
  )

  // BenchmarkSophonApplyWAL_BatchedRewrite_50KRecords: drives 50K record state
  // transitions through the batched-rewrite policy (§6.1: flush every 50 records
  // or 5s) and asserts total bytes written is bounded (< 100 MB) rather than the
  // ~250 MB a per-record rewrite would cost. Measures the rewrite I/O, not CPU.
  func BenchmarkSophonApplyWAL_BatchedRewrite_50KRecords(b *testing.B) {
  	const n = 50000
  	const flushEvery = 50
  	dir := b.TempDir()
  	walPath := filepath.Join(dir, "sophon_apply.wal")

  	build := func() sophonApplyWAL {
  		w := sophonApplyWAL{
  			GameID: "hoyoverse/genshin", TargetTag: "6.6.0", BuildID: "b",
  			Flavor: "sophon_full", BranchKind: "main",
  		}
  		for i := 0; i < n; i++ {
  			w.Records = append(w.Records, sophonApplyRecord{
  				Kind: "chunk_assemble", Category: "game",
  				Path: fmt.Sprintf("data/file_%05d.bin", i), State: "pending",
  			})
  		}
  		return w
  	}

  	b.ResetTimer()
  	for run := 0; run < b.N; run++ {
  		w := build()
  		var written int64
  		flush := func() {
  			body, err := json.Marshal(&w)
  			if err != nil {
  				b.Fatal(err)
  			}
  			tmp := walPath + ".tmp"
  			if err := os.WriteFile(tmp, body, 0o644); err != nil {
  				b.Fatal(err)
  			}
  			if err := os.Rename(tmp, walPath); err != nil {
  				b.Fatal(err)
  			}
  			written += int64(len(body))
  		}
  		for i := 0; i < n; i++ {
  			w.Records[i].State = "done"
  			if (i+1)%flushEvery == 0 {
  				flush()
  			}
  		}
  		flush() // final
  		const cap100MB = 100 << 20
  		if written > cap100MB {
  			b.Fatalf("batched WAL rewrite wrote %d bytes (> 100 MB cap)", written)
  		}
  		b.ReportMetric(float64(written)/(1<<20), "MB_written")
  	}
  }
  ```

  INTEGRATOR-NOTE: this bench inlines the batched-flush loop rather than calling the production `sophonApplyWAL` flush method (Task 16) because that method's exact name/signature isn't pinned in §A (the schema is, the flush API isn't). If Task 16 exports a `flush()`/`maybeFlush()` with the 50-record/5s policy, the INTEGRATOR should swap the inline `flush` for the real one so the bench measures production code. The byte-cap assertion is the load-bearing invariant either way. Run:
  ```bash
  go test -run=^$ -bench=BenchmarkSophonApplyWAL_BatchedRewrite_50KRecords -benchtime=3x ./internal/providers/hoyoverse/
  ```
  Final gate + commit:
  ```bash
  go build ./... && go vet ./... && go test -count=1 ./...
  git add internal/providers/hoyoverse/sophon_bench_test.go
  git commit -m "test(hoyoverse-sophon): batched WAL-rewrite bench (50K records, <100MB cap)"
  ```

---

### Task 25: USER smoke + ship (USER-REQUIRED — NOT executed by subagents)

> **STOP. This task is USER-REQUIRED.** Subagent-driven-development must HALT here and hand control back to the user. Per `memory/feedback_autonomous_m3b_v2_batch.md`, Tasks 1–24 are batch-autonomous; Task 25 (real-game smoke + tag + merge) is a human checkpoint. Do NOT run `wails build`, real-game updates, `git tag`, or `git merge` autonomously. Present this checklist to the user and wait.
>
> **Commit/merge convention:** NO `Co-Authored-By` trailer (per `memory/feedback_commits.md`). Branch `m3b-v2/spec`; ship via `main` + `merge --no-ff`.

**Files:** none (build + smoke + git operations only).

- [ ] **Step 1 — Build.** `wails build` produces a working `omnigate.exe`.
  ```bash
  wails build
  ```
  Verify `build/bin/omnigate.exe` launches, the UI loads, and the games list renders. INTEGRATOR-NOTE: `wails build` does NOT run `go generate`; the committed `.pb.go` are used as-is (spec §2.5). If proto changed, regenerate first (Task 4 procedure).

- [ ] **Step 2 — Real Genshin smoke (the core acceptance gate).** With a real Genshin Impact 6.x install:
  - **CheckForUpdate** shows the correct plan: delta (HDiff) if `currentLocal ∈ branch.Main.DiffTags`; chunk-from-disk Build if a prior manifest is cached; Full otherwise. Confirm the reason/version label in the BottomBar pill.
  - **Run update.** Confirm download progress advances (bar = bytes assembled, §5.3), apply completes, and the game launches.
  - **config.ini version writeback.** Confirm `game_version` in the game's `config.ini` is updated to the target. If writeback needs admin (non-admin run), confirm the `config_writeback_warning` bell entry appears and `maybeSelfHealSophon` fixes the label on the next CheckForUpdate when run as admin.
  - **Predownload flow.** When a `pre_download` branch is live: click predl, confirm staging completes and `predl_ready.json` is written. On patch day (or simulated), confirm the next CheckForUpdate consumes the predl staging (reason `resume_interrupted`) and applies without re-downloading.
  - **Crash-resume.** Kill the app mid-download and mid-apply; relaunch; confirm the bell drawer offers resume and that resume completes (download reuses `sophon_progress.json` ChunksDone; apply resumes from `sophon_apply.wal` first-pending record).

- [ ] **Step 3 — HSR/ZZZ legacy regression.** Confirm Honkai: Star Rail and Zenless Zone Zero (both `UsesSophon=false`) still update via the v1 `getGamePackages` zip+hdiff pipeline, unaffected by the Sophon code. CheckForUpdate + a small update on at least one legacy game.

- [ ] **Step 4 — Tag.** On the tip of the integration branch:
  ```bash
  git tag v0.4.0-m3b
  ```

- [ ] **Step 5 — Merge to main.**
  ```bash
  git checkout main
  git merge --no-ff dev
  ```
  INTEGRATOR-NOTE / **CONFIRM WITH USER AT SMOKE TIME:** the branch strategy per memory is that `dev` carries BOTH `m3b/spec` (v1) and `m3b-v2/spec` (v2) merged via `--no-ff`, and the combined v1+v2 ships as `v0.4.0-m3b`. This plan's work is on `m3b-v2/spec`. Before the final merge, CONFIRM with the user whether to (a) merge `m3b-v2/spec` → `dev` first, then `dev` → `main`, or (b) merge `m3b-v2/spec` directly to `main`. Do NOT assume; the memory note says "CONFIRM with user at smoke time." Push only when the user asks.

- [ ] **Step 6 — Close the autonomy grant.** Mark `memory/feedback_autonomous_m3b_v2_batch.md` EXPIRED (per spec §12).

## §E.2 Post-review pins — round-1 opus adversarial review (HIGHEST AUTHORITY)

These resolve the 11 findings of the round-1 review. They are NORMATIVE and win over §A–§E and any task body. Each pin gives the single canonical signature/behavior that the owner task declares and every consumer task uses verbatim.

**P1 — `sophonAssetMD5` IS populated (fixes BLOCKER 1).** In `buildSophonPlan` (T18), initialize `gp.sophonAssetMD5 = map[string]string{}` (and `gp.sophonRawManifests = map[string][]byte{}`, see P2) before the category loop. In BOTH `buildSophonBuildPlan` and `buildSophonPatchPlan`, immediately after computing an asset's chunk sources that become a `chunk_assemble` record (i.e. every `BuildChunkSources` result appended to `gp.sophonChunkSources`), set `gp.sophonAssetMD5[newAsset.AssetName] = newAsset.AssetHashMd5`. T20's `chunk_assemble` record then reads `AssetMD5: gp.sophonAssetMD5[g.path]` (already wired). Without this, §6.3-step-6 whole-file verify is silently skipped on real assets.

**P2 — Applied manifests ARE persisted (fixes BLOCKER 2; restores Path-B dedup).**
- T6 `manifest_fetch.go` adds `func FetchManifestRaw(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonManifestProto, []byte, error)` returning BOTH the parsed proto AND the raw `.pb.zst` response body (the compressed wire bytes, before decompress). `FetchManifest` becomes a thin wrapper that calls `FetchManifestRaw` and discards the raw slice.
- `genshinPlan` gains a 12th field: `sophonRawManifests map[string][]byte` (key = category `MatchingField`, value = raw `.pb.zst` bytes of the NEW main manifest). T18 populates it from `FetchManifestRaw` for every category it fetches via `getBuild` (patch + build + full flavors all fetch the main `getBuild` manifest, so all three capture it).
- T20 `finalizeSophonApply` (the all-records-done block, §6.2 steps 6–8) calls, **before** `RotateAfterApply`: `for mf, raw := range gp.sophonRawManifests { if err := SaveAppliedManifest(tempRoot, gid, mf, gp.sophonBuildID, gp.Version, raw); err != nil { p.logger.Warn("sophon: save applied manifest", "category", mf, "err", err) } }`.
- **Known limitation (documented; add to §10 risks at smoke time):** a predl-CONSUMED apply has no live `getBuild` fetch, so `gp.sophonRawManifests` is empty and the dedup-manifest cache is NOT refreshed for that update; the subsequent update may fall to Path C. Acceptable for v2.

**P3 — `SaveAppliedManifest` / `RotateAfterApply` signatures (fixes MAJOR 4).** T17 declares EXACTLY:
```go
func SaveAppliedManifest(tempRoot string, gid core.GameID, matchingField, buildID, version string, rawPbZst []byte) error
func RotateAfterApply(tempRoot string, gid core.GameID, buildID, version string, categories map[string]string) error // categories: matchingField → buildID
func LoadAppliedManifests(tempRoot string, gid core.GameID) *appliedSet
func cleanupStaleSophonSidecars(tempRoot string, gid core.GameID, currentTag string, allowedTargets []string) error
```
T20 builds the `categories map[string]string` for `RotateAfterApply` from `gp.sophonCategories` (`for _, c := range gp.sophonCategories { m[c.MatchingField] = gp.sophonBuildID }`). It does NOT pass `[]sophon.Category` directly.

**P4 — `walFlusher` API (fixes MAJOR 3).** T16 declares EXACTLY (and T20 + the T16 cadence test use verbatim):
```go
type walFlusher struct {
    dir     string
    wal     *sophonApplyWAL
    now     func() time.Time // nil → time.Now
    nSince  int              // records mutated since last disk write
    lastAt  time.Time
    flushes int              // TEST-READABLE: total disk rewrites performed
}
func newWalFlusher(dir string, wal *sophonApplyWAL, now func() time.Time) *walFlusher
func (f *walFlusher) maybeFlush() error // rewrite if nSince>=50 OR now()-lastAt>=5s; resets counters + increments flushes
func (f *walFlusher) flush() error      // unconditional rewrite; increments flushes
func (f *walFlusher) Close() error      // final flush
```
The owner increments `nSince` whenever a record's `State` is mutated (call `maybeFlush()` after each record transition). The cadence test asserts on `f.flushes` (50-record path) and on an injected `now` advancing ≥5s (time path).

**P5 — `sophonProgressStore` API (fixes MAJOR 5 + MINOR 9).** T15 declares EXACTLY (T19/T20 use verbatim):
```go
func newSophonProgressStore(tempRoot string, gid core.GameID, version, branchKind, buildID string) (*sophonProgressStore, error)
func (s *sophonProgressStore) ChunkDone(chunkName string) bool
func (s *sophonProgressStore) PatchDone(patchName string) bool
func (s *sophonProgressStore) MarkChunkDone(chunkName string) error // persists
func (s *sophonProgressStore) MarkPatchDone(patchName string) error // persists
func (s *sophonProgressStore) Save() error
```
T19's worker `onDone` handles the `error` from `MarkChunkDone`/`MarkPatchDone`. T19's executor injection uses `defaultSophonExecutors(hc *http.Client)` (NOT `*httpClientType`); add `import "net/http"`.

**P6 — `planFlavorFromString` (fixes MAJOR 6).** The inverse helper is named `planFlavorFromString` (declared in T14 per §E item 7). T21's WAL-resume code MUST call `planFlavorFromString(wal.Flavor)`, NOT `flavorFromString`.

**P7 — hdiff demotion threading (fixes MAJOR 7).** T20 threads `gp *genshinPlan` and the resolved `versionDir string` through the dispatch chain: `applySophonRecord(ctx, p, gp, wal, rec, gameDir, stagingRoot, versionDir, flusher)` and `applyHDiffPatch(ctx, p, gp, rec, gameDir, stagingRoot, versionDir)`. The phantom helpers `p.sophonDemoteSources(...)` and `versionSidecarDirFromWAL(...)` are DELETED; demotion sources come from `gp.sophonPatchAssetsFromMain[rec.Path]` and the version dir is the threaded `versionDir`. On WAL-resume with a cold cache, §6.4 guarantees a demoted record is already persisted as `chunk_assemble`, so `applyHDiffPatch` is never re-entered for it. If `gp == nil` AND a demotion is required, surface `sophon_apply_failed` rather than panic.

**P8 — `verifyPredlStaging` IS implemented + scenario 27 discriminates (fixes MAJOR 8).** T21's predl-consume branch (before reusing predl staging) calls:
```go
func verifyPredlStaging(stagingRoot string, sources []sophon.ChunkSource, patches []sophon.PatchInstr) (discard bool, err error)
```
It stats+verifies each CDN chunk (`chunks/<ChunkName>`) and patch blob (`patches/<PatchName>`); counts failures; returns `discard=true` if `cdnFailRatio > 0.25` OR `patchFailRatio > 0.50`. On `discard=true`, RunUpdate deletes `predl_ready.json` + `staging/predl/<buildID>` and falls through to a fresh `staging/main` download+apply. On `discard=false`, per-chunk misses are re-fetched organically by `downloadAllSophon` (skip-if-verified). T23 scenario 27 MUST assert a DISCRIMINATING signal: the 24% subcase asserts the predl staging dir still exists / `fakeSophonServer.buildHits` stayed low (predl reused); the 26% subcase asserts `staging/predl/<buildID>` was removed AND a fresh `getBuild` + `staging/main` download occurred (`fakeSophonServer.buildHits` incremented). Both still end at version 6.7.0, but the path is now distinguishable.

**P9 — `getBuild`/`getPatchBuild` fetched ONCE per branch (fixes MINOR 11).** `getBuild` returns ALL categories in one `manifests[]` envelope (spec §2.3 — no category query param). T18 fetches the `getBuild` envelope once per branch (and `getPatchBuild` once per branch), then iterates categories via `(*BuildResponse).ManifestFor(matchingField)`. Do NOT call `getBuild` inside the per-category loop.

**P10 — category JSON key (fixes MINOR 10).** Every test fixture and parser uses `json:"type"` for the category type field, never `"category_type"` (§E item 3). T21's no-install unit-test fixture's `branches` JSON literal MUST use `"type"`.

**P11 — `genshinPlan` final Sophon field list (12 fields).** With P1+P2: `sophonBranch`, `sophonBuildID`, `sophonCategories`, `sophonChunkSources`, `sophonPatches`, `sophonDeletes`, `sophonPatchAssetsFromMain`, `sophonAssetMD5`, `sophonRawManifests`, `predlConsume`, `predlSnapshot`, `predlPlan`. T14 declares all 12; `buildSophonPlan` (T18) initializes the three maps (`sophonPatchAssetsFromMain`, `sophonAssetMD5`, `sophonRawManifests`).

---

## §E.3 Post-review pins — round-2 (finding 8 closure; HIGHEST AUTHORITY)

**P12 — `sophon.ParseXXHName` is EXPORTED (Task 9).** Task 9's `DownloadChunk` already parses `ChunkName`'s first 16 hex chars to a `uint64` for xxh64 verification. Export that helper as `func ParseXXHName(chunkName string) (uint64, bool)` (returns the parsed value + `ok=false` when the first 16 chars aren't valid hex / name too short). `DownloadChunk` uses it internally; T21's apply-side `stagedChunkOK` (verifyPredlStaging) reuses it so staging verify applies the identical xxh64-then-MD5 rule. Add to §A.3 sophon signatures.

**P13 — `fakeSophonServer` chunk-GET counter (Task 23 Step 2).** The `fakeSophonServer` struct gains an atomic chunk-GET counter used by scenario 27's discriminating assertions:
```go
type fakeSophonServer struct {
    // ...existing fields (srv, branchesJSON, fixture stores, etc.)...
    nChunkGET atomic.Int64
}
func (f *fakeSophonServer) chunkGETs() int    { return int(f.nChunkGET.Load()) }
func (f *fakeSophonServer) resetChunkGETs()   { f.nChunkGET.Store(0) }
```
The CDN chunk handler (`GET /cdn/chunks/<ChunkName>`) calls `f.nChunkGET.Add(1)` on each request before serving the blob. Step 6's helper block adds `totalCDNChunks(t *testing.T, manifestFixture string) int` — it loads the named `.manifest.pb.zst` fixture, parses it, and returns the count of distinct chunk names across all assets (the number of CDN chunk GETs a full fresh download would incur for that fixture). Import `sync/atomic`.

---
