package hoyoverse

import (
	"context"
	"strings"
	"testing"

	"omnigate/internal/providers/hoyoverse/sophon"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// On-demand skip planner tests (spec 2026-09-29-sophon-on-demand-skip §4).
//
// Fixture (game category only — newSophonBuildServer serves a single "game"
// manifest entry):
//
//	main:  file_a (patch pa), file_b (copy-over pb), file_c (unchanged),
//	       file_d (unchanged, blacklisted, absent),
//	       file_e (unchanged, blacklisted, PRESENT as 0-byte → MD5 mismatch → chunk source),
//	       file_f (unchanged, NOT blacklisted, absent → chunk source),
//	       file_d_<md5>.hash (unchanged, companion of d),
//	       file_i.pck (unchanged, blacklisted, absent),
//	       file_i_<md5>.hash (COPY-OVER pi, companion of i),
//	       file_g (MethodPatch pg, OldFile file_g_old ABSENT, blacklisted → dropped),
//	       file_h (MethodPatch ph, OldFile file_h_old PRESENT, blacklisted → kept)
//	blacklist: file_d, file_e, file_g, file_h, file_i.pck

const odHex = "0123456789abcdef0123456789abcdef"

var (
	odHashD = "file_d_" + odHex + ".hash"
	odHashI = "file_i_" + odHex + ".hash"
)

func odMainAsset(name, md5 string, size int64) *pb.SophonManifestAssetProperty {
	return &pb.SophonManifestAssetProperty{AssetName: name, AssetType: 0, AssetSize: size, AssetHashMd5: md5,
		AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c_" + name, ChunkDecompressedHashMd5: "c_" + name, ChunkOnFileOffset: 0, ChunkSize: size / 2, ChunkSizeDecompressed: size}}}
}

func odPatchAsset(name, md5 string, size int64, chunk *pb.SophonPatchAssetChunk) *pb.SophonPatchAssetProperty {
	return &pb.SophonPatchAssetProperty{AssetName: name, AssetSize: size, AssetHashMd5: md5,
		AssetInfos: []*pb.SophonPatchAssetInfo{{VersionTag: "6.5.0", Chunk: chunk}}}
}

// newOnDemandFixture builds the manifests and a gameDir with the blacklist
// and the two "present" files. Returns (main, patch, gameDir).
func newOnDemandFixture(t *testing.T) (*pb.SophonManifestProto, *pb.SophonPatchProto, string) {
	t.Helper()
	main := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{
		odMainAsset("file_a", "AA", 10),
		odMainAsset("file_b", "BB", 10),
		odMainAsset("file_c", "CC", 10),
		odMainAsset("file_d", "DD", 10),
		odMainAsset("file_e", "EE", 10),
		odMainAsset("file_f", "FF", 10),
		odMainAsset(odHashD, "DH", 1),
		odMainAsset("file_i.pck", "II", 10),
		odMainAsset(odHashI, "IH", 1),
		odMainAsset("file_g", "GG", 10),
		odMainAsset("file_h", "HH", 10),
	}}
	patch := &pb.SophonPatchProto{
		PatchAssets: []*pb.SophonPatchAssetProperty{
			odPatchAsset("file_a", "AA", 10, &pb.SophonPatchAssetChunk{PatchName: "pa", PatchSize: 40, PatchLength: 4, OriginalFileName: "file_a", OriginalFileMd5: "OLDA"}),
			odPatchAsset("file_b", "BB", 10, &pb.SophonPatchAssetChunk{PatchName: "pb", PatchSize: 10, PatchLength: 10}),
			odPatchAsset("file_g", "GG", 10, &pb.SophonPatchAssetChunk{PatchName: "pg", PatchSize: 77, PatchLength: 5, OriginalFileName: "file_g_old", OriginalFileMd5: "OLDG"}),
			odPatchAsset("file_h", "HH", 10, &pb.SophonPatchAssetChunk{PatchName: "ph", PatchSize: 88, PatchLength: 5, OriginalFileName: "file_h_old", OriginalFileMd5: "OLDH"}),
			odPatchAsset(odHashI, "IH", 1, &pb.SophonPatchAssetChunk{PatchName: "pi", PatchSize: 9, PatchLength: 1}),
		},
		UnusedAssets: []*pb.SophonUnusedAssetProperty{
			{VersionTag: "6.5.0", AssetInfos: []*pb.SophonUnusedAssetInfo{
				{Assets: []*pb.SophonUnusedAssetFile{{FileName: "old_dead.bin", FileMd5: "DEAD"}}}}},
		},
	}
	gameDir := t.TempDir()
	writeBlacklist(t, gameDir, blEntry("file_d"), blEntry("file_e"), blEntry("file_g"), blEntry("file_h"), blEntry("file_i.pck"))
	touch(t, gameDir, "file_e")
	touch(t, gameDir, "file_h_old")
	return main, patch, gameDir
}

func odNewPlan() *genshinPlan {
	return &genshinPlan{
		sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{},
		sophonAssetMD5:            map[string]string{},
		sophonRawManifests:        map[string][]byte{},
	}
}

var (
	odSlot = sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Branch: "main",
		Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}}
	odCats = []sophon.Category{{ID: "10016", MatchingField: "game"}}
)

func chunkAssets(gp *genshinPlan) map[string]bool {
	m := map[string]bool{}
	for _, s := range gp.sophonChunkSources {
		m[s.Asset] = true
	}
	return m
}

func patchAssets(gp *genshinPlan) map[string]bool {
	m := map[string]bool{}
	for _, p := range gp.sophonPatches {
		m[p.Asset] = true
	}
	return m
}

func runOnDemandPatchPlan(t *testing.T, skipper bool) (*genshinPlan, *onDemandSkipper) {
	t.Helper()
	main, patch, gameDir := newOnDemandFixture(t)
	srv := newSophonBuildServer(t, "6.6.0", main, patch)
	t.Cleanup(srv.Close)
	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)
	var skip *onDemandSkipper
	if skipper {
		lg, _ := debugLogger()
		skip = loadOnDemandSkipper(odGID, gameDir, lg)
		if skip == nil {
			t.Fatal("skipper nil for starrail")
		}
	}
	gp := odNewPlan()
	if err := buildSophonPatchPlan(context.Background(), p, gp, odSlot, "", odCats, "6.5.0", nil, gameDir, nil, skip); err != nil {
		t.Fatalf("buildSophonPatchPlan: %v", err)
	}
	return gp, skip
}

func TestBuildSophonPatchPlan_SkipsOnDemandAssets(t *testing.T) {
	gp, skip := runOnDemandPatchPlan(t, true)
	chunks, patches := chunkAssets(gp), patchAssets(gp)
	for _, n := range []string{"file_d", odHashD, "file_g", "file_i.pck", odHashI} {
		if chunks[n] {
			t.Errorf("%s must not have a chunk source", n)
		}
		if patches[n] {
			t.Errorf("%s must not have a patch instr", n)
		}
		if _, ok := gp.sophonAssetMD5[n]; ok {
			t.Errorf("%s must not be in sophonAssetMD5", n)
		}
		if _, ok := gp.sophonPatchAssetsFromMain[n]; ok {
			t.Errorf("%s must not be in sophonPatchAssetsFromMain", n)
		}
	}
	for _, n := range []string{"file_a", "file_b", "file_h"} {
		if !patches[n] {
			t.Errorf("%s patch instr must be kept; got %v", n, patches)
		}
	}
	for _, n := range []string{"file_c", "file_e", "file_f"} {
		if !chunks[n] {
			t.Errorf("%s must be planned as chunk_assemble; got %v", n, chunks)
		}
	}
	if len(gp.sophonDeletes) != 1 || gp.sophonDeletes[0].Path != "old_dead.bin" {
		t.Errorf("deletes = %+v, want [old_dead.bin]", gp.sophonDeletes)
	}
	wantStat(t, skip, "game", onDemandStat{N: 5, Bytes: 32})
}

func TestBuildSophonPatchPlan_NilSkipperUnchanged(t *testing.T) {
	gp, _ := runOnDemandPatchPlan(t, false)
	chunks, patches := chunkAssets(gp), patchAssets(gp)
	for _, n := range []string{"file_d", odHashD, "file_f", "file_i.pck", "file_e", "file_c"} {
		if !chunks[n] {
			t.Errorf("nil skipper: %s must be planned; got %v", n, chunks)
		}
	}
	for _, n := range []string{"file_a", "file_b", "file_g", "file_h", odHashI} {
		if !patches[n] {
			t.Errorf("nil skipper: %s patch instr must exist; got %v", n, patches)
		}
	}
}

func TestBuildSophonBuildPlan_SkipsOnDemandAssets(t *testing.T) {
	main, _, gameDir := newOnDemandFixture(t)
	srv := newSophonBuildServer(t, "6.6.0", main, nil)
	defer srv.Close()
	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)
	lg, _ := debugLogger()
	skip := loadOnDemandSkipper(odGID, gameDir, lg)
	gp := odNewPlan()
	if err := buildSophonBuildPlan(context.Background(), p, gp, odSlot, "", odCats, nil, "6.5.0", gameDir, skip); err != nil {
		t.Fatalf("buildSophonBuildPlan: %v", err)
	}
	chunks := chunkAssets(gp)
	// Build flavor: no patch records, so g/h are judged by their own names
	// (absent + blacklisted → skipped) and i.hash follows i.pck.
	for _, n := range []string{"file_d", odHashD, "file_i.pck", odHashI, "file_g", "file_h"} {
		if chunks[n] {
			t.Errorf("build flavor: %s must be skipped", n)
		}
	}
	for _, n := range []string{"file_a", "file_b", "file_c", "file_e", "file_f"} {
		if !chunks[n] {
			t.Errorf("build flavor: %s must be planned; got %v", n, chunks)
		}
	}
}

func TestBuildSophonPlan_PredlSkipsOnDemandAssets(t *testing.T) {
	buf := captureSlog(t) // before newTestProvider: New() snapshots slog.Default()
	main, patch, gameDir := newOnDemandFixture(t)
	srv := newSophonBuildServer(t, "6.6.0", main, patch)
	defer srv.Close()
	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)
	branch := &sophon.BranchInfo{
		Main:        sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Categories: odCats},
		PreDownload: sophon.BranchSlot{PackageID: "pkg2", Tag: "6.7.0", DiffTags: []string{"6.5.0"}, Categories: odCats},
	}
	gp, predlAvail, err := buildSophonPlan(context.Background(), p, branch, odGID, "6.5.0", nil, gameDir, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("buildSophonPlan: %v", err)
	}
	if !predlAvail || gp.predlPlan == nil {
		t.Fatalf("predlAvail=%v predlPlan=%v, want predl patch plan", predlAvail, gp.predlPlan)
	}
	predlPatched := map[string]bool{}
	for _, pi := range gp.predlPlan.Patches {
		predlPatched[pi.Asset] = true
		if pi.Asset == "file_g" || pi.Asset == odHashI {
			t.Errorf("predl plan must not carry skipped patch %s", pi.Asset)
		}
	}
	// The predl plan must judge file_h by its OWN patch probe (old file
	// present → kept). Reusing the main plan's skipper (which judged file_h
	// by its new name under the full flavor → absent → skipped) would drop it.
	if !predlPatched["file_h"] {
		t.Errorf("predl plan must keep file_h's patch (own skipper, OldFile probe); got %v", predlPatched)
	}
	for _, s := range gp.predlPlan.ChunkSources {
		if s.Asset == "file_d" || s.Asset == odHashD || s.Asset == "file_i.pck" {
			t.Errorf("predl plan must not carry skipped asset %s", s.Asset)
		}
	}
	log := buf.String()
	if !strings.Contains(log, "phase=main") {
		t.Errorf("want main summary line:\n%s", log)
	}
	// Own skipper ⇒ own accounting: d, d.hash, g, i.pck, i.hash = 5 / 32 B
	// (a shared skipper would re-report the main plan's 6 / 42 B here).
	if !strings.Contains(log, "phase=predl category=game n=5 assetBytes=32") {
		t.Errorf("want predl summary with its own accounting (n=5 assetBytes=32):\n%s", log)
	}
}

func TestSumSophonTotalBytes_DropsReleasedBlobOnly(t *testing.T) {
	gpNil, _ := runOnDemandPatchPlan(t, false)
	gpSkip, _ := runOnDemandPatchPlan(t, true)
	sumNil, sumSkip := sumSophonTotalBytes(gpNil), sumSophonTotalBytes(gpSkip)
	// chunk side: d(10)+d.hash(1)+i.pck(10)=21 ; patch side: pg(77)+pi(9)=86.
	// i.hash is a copy-over patch record → never a chunk source in either run.
	if want := int64(21 + 77 + 9); sumNil-sumSkip != want {
		t.Errorf("total delta = %d (nil %d, skip %d), want %d", sumNil-sumSkip, sumNil, sumSkip, want)
	}
	// Absolute: skip run = chunks c,e,f (30) + blobs pa(40)+pb(10)+ph(88) = 168.
	if sumSkip != 168 {
		t.Errorf("skip total = %d, want 168", sumSkip)
	}
}
