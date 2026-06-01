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

// mkBuildEnvelope returns a getBuild/getPatchBuild JSON envelope (the full
// {"retcode":0,"message":"","data":{...}} that fetchSophonBuild decodes).
func mkBuildEnvelope(buildID, tag, patchID, manifestFile, chunkURLPrefix, diffURLPrefix string, compressedLen int) map[string]any {
	return map[string]any{
		"retcode": 0, "message": "",
		"data": map[string]any{
			"build_id": buildID, "tag": tag, "patch_id": patchID,
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
					"url_prefix": chunkURLPrefix, "url_suffix": "",
					"password": "", "encryption": 0, "compression": 1,
				},
				"diff_download": map[string]any{
					"url_prefix": diffURLPrefix, "url_suffix": "",
					"password": "", "encryption": 0, "compression": 0,
				},
			}},
		},
	}
}

// mkBranchEnvelope returns a getGameBranches JSON envelope using the
// game_branches[] array format that ParseBranches expects.
// mainTag, predlTag: version tags ("" predlTag → no pre_download slot).
// diffTags: list of versions that trigger patch flavor for main.
// predlDiffTags: diff_tags for pre_download slot.
// withCategories: if false, categories list is empty (triggers malformed-response path).
func mkBranchEnvelope(mainTag string, diffTags []string, predlTag string, predlDiffTags []string, withCategories bool) map[string]any {
	gameCat := map[string]any{
		"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE",
	}
	var mainCats []any
	if withCategories {
		mainCats = []any{gameCat}
	} else {
		mainCats = []any{}
	}

	mainSlot := map[string]any{
		"package_id": "pkg-main", "branch": "main", "password": "",
		"tag": mainTag, "diff_tags": diffTags, "categories": mainCats,
	}

	var predlSlot map[string]any
	if predlTag != "" {
		predlCats := []any{gameCat}
		predlSlot = map[string]any{
			"package_id": "pkg-predl", "branch": "predownload", "password": "",
			"tag": predlTag, "diff_tags": predlDiffTags, "categories": predlCats,
		}
	} else {
		predlSlot = map[string]any{} // empty → IsEmpty() == true
	}

	return map[string]any{
		"retcode": 0, "message": "",
		"data": map[string]any{
			"game_branches": []any{
				map[string]any{
					"game": map[string]any{"id": "gopR6Cufr3", "biz": "hk4e_global"},
					"main":         mainSlot,
					"pre_download": predlSlot,
				},
			},
		},
	}
}

// buildNonHexManifest creates a manifest with one asset whose ChunkName begins
// with non-hex chars (forcing MD5 fallback in the verification path, T26).
func buildNonHexManifest(t *testing.T) *pb.SophonManifestProto {
	decomp := tinyDecomp
	payload := bytes.Repeat([]byte{0xFE}, decomp)
	// non-hex ChunkName: first 16 chars are "zzzzzzzzzzzzzzzz" → hex parse fails
	name := "zzzzzzzzzzzzzzzz_00000"
	writeFile(t, filepath.Join("chunks", name), zstdBytes(t, payload))
	asset := &pb.SophonManifestAssetProperty{
		AssetName: "data/file_00.bin",
		AssetType: 0,
		AssetSize: int64(decomp),
		AssetHashMd5: md5hex(payload),
		AssetChunks: []*pb.SophonManifestAssetChunk{{
			ChunkName:                name,
			ChunkDecompressedHashMd5: md5hex(payload),
			ChunkOnFileOffset:        0,
			ChunkSize:                int64(len(zstdBytes(t, payload))),
			ChunkSizeDecompressed:    int64(decomp),
		}},
	}
	return &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{asset}}
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

	// --- nonhex manifest (scenario 26: MD5 fallback verification) ---
	nonhex := buildNonHexManifest(t)
	nonhexBytes, err := proto.Marshal(nonhex)
	if err != nil {
		t.Fatalf("marshal nonhex: %v", err)
	}
	writeFile(t, "nonhex.manifest.pb.zst", zstdBytes(t, nonhexBytes))

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

	// --- JSON envelopes (getBuild/getPatchBuild) ---
	writeJSON(t, "build_small.json", mkBuildEnvelope(
		"build-small", "6.6.0", "", "sample.manifest.pb.zst",
		"/cdn/chunks", "/cdn/chunks",
		len(zstdBytes(t, smallBytes)),
	))
	writeJSON(t, "build_tiny.json", mkBuildEnvelope(
		"build-tiny", "6.6.0", "", "tiny.manifest.pb.zst",
		"/cdn/chunks", "/cdn/chunks",
		len(zstdBytes(t, tinyBytes)),
	))
	writeJSON(t, "build_nonhex_chunk.json", mkBuildEnvelope(
		"build-nonhex", "6.6.0", "", "nonhex.manifest.pb.zst",
		"/cdn/chunks", "/cdn/chunks",
		len(zstdBytes(t, nonhexBytes)),
	))

	patchEnv := mkBuildEnvelope(
		"build-small", "6.6.0", "patch-small", "sample.patch.pb.zst",
		"/cdn/chunks", "/cdn/chunks",
		len(zstdBytes(t, patchBytes)),
	)
	writeJSON(t, "patch_small.json", patchEnv)

	// --- getGameBranches envelopes ---
	// branches_main_only.json: main tag=6.6.0, diff_tags=["6.5.0"], no predl.
	writeJSON(t, "branches_main_only.json",
		mkBranchEnvelope("6.6.0", []string{"6.5.0"}, "", nil, true))

	// branches_with_predl.json: main tag=6.6.0, predl tag=6.7.0 diff_tags=["6.6.0"]
	writeJSON(t, "branches_with_predl.json",
		mkBranchEnvelope("6.6.0", []string{"6.5.0"}, "6.7.0", []string{"6.6.0"}, true))

	// branches_predl_now.json: main tag=6.7.0 (predl has become live), no predl.
	writeJSON(t, "branches_predl_now.json",
		mkBranchEnvelope("6.7.0", []string{"6.6.0"}, "", nil, true))

	// branches_no_difftags.json: main tag=6.6.0, diff_tags=[], no predl.
	// Scenario 18: with no diff_tags currentLocal never hits Patch flavor.
	writeJSON(t, "branches_no_difftags.json",
		mkBranchEnvelope("6.6.0", []string{}, "", nil, true))

	// branches_no_categories.json: main.categories=[] → malformed → sophon_manifest_fetch_failed.
	writeJSON(t, "branches_no_categories.json",
		mkBranchEnvelope("6.6.0", []string{"6.5.0"}, "", nil, false))
}
