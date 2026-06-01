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
	// applied.json is written by RotateAfterApply, not SaveAppliedManifest, so
	// MatchByVersion needs the rotate to record the version→buildID mapping.
	if err := RotateAfterApply(tmp, gid, "buildA", "6.6.0", map[string]string{"game": "buildA"}); err != nil {
		t.Fatal(err)
	}
	set := LoadAppliedManifests(tmp, gid)
	if set == nil {
		t.Fatal("LoadAppliedManifests returned nil")
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
	set := LoadAppliedManifests(tmp, gid)
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
