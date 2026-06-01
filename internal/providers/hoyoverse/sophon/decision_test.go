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
