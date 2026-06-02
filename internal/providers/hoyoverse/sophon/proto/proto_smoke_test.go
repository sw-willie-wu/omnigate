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
