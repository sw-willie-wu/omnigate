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
