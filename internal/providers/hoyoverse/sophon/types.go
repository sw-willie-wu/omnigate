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
