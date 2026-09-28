package hoyoverse

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

const (
	odGID     = core.GameID("hoyoverse/starrail")
	odBLRel   = "StarRail_Data/Persistent/DownloadBlacklist.json"
	hex32     = "0123456789abcdef0123456789abcdef"
	hex31     = "0123456789abcdef0123456789abcde"
	odGameCat = "game"
)

// writeBlacklist writes the JSON Lines blacklist for starrail; it only
// creates the record's parent directory.
func writeBlacklist(t *testing.T, gameDir string, lines ...string) {
	t.Helper()
	p := filepath.Join(gameDir, filepath.FromSlash(odBLRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// blEntry renders one blacklist line for rel.
func blEntry(rel string) string { return `{"fileName":"` + rel + `"}` }

// touch creates an empty file at <gameDir>/<rel>.
func touch(t *testing.T, gameDir, rel string) {
	t.Helper()
	p := filepath.Join(gameDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func odAsset(name string, size int64) *pb.SophonManifestAssetProperty {
	return &pb.SophonManifestAssetProperty{AssetName: name, AssetType: 0, AssetSize: size}
}

func ident(n string) string { return n }

// debugLogger returns a Debug-level text logger writing into buf.
func debugLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func wantStat(t *testing.T, s *onDemandSkipper, cat string, want onDemandStat) {
	t.Helper()
	got := s.skipped[cat]
	if got == nil || *got != want {
		if got == nil {
			t.Errorf("skipped[%q] = nil, want %+v", cat, want)
		} else {
			t.Errorf("skipped[%q] = %+v, want %+v", cat, *got, want)
		}
	}
}

func TestOnDemand_NilForGamesWithoutBlacklist(t *testing.T) {
	lg, _ := debugLogger()
	for _, gid := range []core.GameID{"hoyoverse/genshin", "hoyoverse/zzz", "bogus/game"} {
		s := loadOnDemandSkipper(gid, t.TempDir(), lg)
		if s != nil {
			t.Errorf("%s: want nil skipper, got %+v", gid, s)
		}
		s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("x", 1)}, ident) // must not panic
		if s.IsSkipped("x") {
			t.Errorf("%s: nil.IsSkipped = true", gid)
		}
		s.LogSummary(lg, "main") // must not panic
	}
}

func TestOnDemand_MissingFileIsEmpty(t *testing.T) {
	lg, buf := debugLogger()
	dir := t.TempDir()
	s := loadOnDemandSkipper(odGID, dir, lg)
	if s == nil {
		t.Fatal("want non-nil skipper")
	}
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("a/b.usm", 1)}, ident)
	if s.IsSkipped("a/b.usm") {
		t.Error("nothing blacklisted → must not skip")
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("missing file must not warn:\n%s", buf.String())
	}
}

func TestOnDemand_ParsesJSONL(t *testing.T) {
	lg, buf := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir,
		blEntry("a/b.usm")+"\r",
		"",
		"{bad",
		blEntry(""),
		`{"fileName":"a\\c.usm"}`,
		blEntry("./a/d.usm"),
	)
	s := loadOnDemandSkipper(odGID, dir, lg)
	if s == nil {
		t.Fatal("nil skipper")
	}
	for _, want := range []string{"a/b.usm", "a/c.usm", "a/d.usm"} {
		if _, ok := s.set[want]; !ok {
			t.Errorf("set missing %q; set=%v", want, s.set)
		}
	}
	if len(s.set) != 3 {
		t.Errorf("set size = %d, want 3: %v", len(s.set), s.set)
	}
	log := buf.String()
	if !strings.Contains(log, "on-demand blacklist loaded") || !strings.Contains(log, "entries=3") || !strings.Contains(log, "malformed=2") {
		t.Errorf("debug line missing/incorrect:\n%s", log)
	}
}

func TestOnDemand_UnreadableWarns(t *testing.T) {
	lg, buf := debugLogger()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(odBLRel)), 0o755); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	s := loadOnDemandSkipper(odGID, dir, lg)
	if s == nil || len(s.set) != 0 {
		t.Fatalf("want non-nil empty skipper, got %+v", s)
	}
	if !strings.Contains(buf.String(), "on-demand blacklist unreadable") {
		t.Errorf("want Warn line:\n%s", buf.String())
	}
}

func TestOnDemand_BasePass(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("a/b.usm"), blEntry("a/e.usm"))
	touch(t, dir, "a/e.usm")
	s := loadOnDemandSkipper(odGID, dir, lg)
	probed := map[string]int{}
	probe := func(n string) string { probed[n]++; return n }
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("a/b.usm", 10), odAsset("a/e.usm", 10), odAsset("a/f.usm", 10)}, probe)
	if !s.IsSkipped("a/b.usm") || s.IsSkipped("a/e.usm") || s.IsSkipped("a/f.usm") {
		t.Errorf("verdicts b/e/f = %v/%v/%v, want true/false/false", s.IsSkipped("a/b.usm"), s.IsSkipped("a/e.usm"), s.IsSkipped("a/f.usm"))
	}
	wantStat(t, s, odGameCat, onDemandStat{N: 1, Bytes: 10})
	if len(probed) != 2 || probed["a/b.usm"] != 1 || probed["a/e.usm"] != 1 {
		t.Errorf("probe must only be called for blacklisted names (set before stat); got %v", probed)
	}
}

func TestOnDemand_BasePassProbeUsesOldFile(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("a/g"))
	touch(t, dir, "a/g_old")
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("a/g", 10)}, func(string) string { return "a/g_old" })
	if s.IsSkipped("a/g") {
		t.Error("old file present → must NOT skip even though the new name is absent")
	}
	s2 := loadOnDemandSkipper(odGID, dir, lg)
	s2.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("a/g", 10)}, func(string) string { return "a/g_missing" })
	if !s2.IsSkipped("a/g") {
		t.Error("probe path absent → skip")
	}
}

func TestOnDemand_CompanionFollowsSkippedStem(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10), odAsset("v/x_"+hex32+".hash", 1)}, ident)
	if !s.IsSkipped("v/x.pck") || !s.IsSkipped("v/x_"+hex32+".hash") {
		t.Error("pck and its companion hash must both be skipped")
	}
	wantStat(t, s, odGameCat, onDemandStat{N: 2, Bytes: 11})
}

func TestOnDemand_CompanionOrderIndependent(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("v/x_"+hex32+".hash", 1), odAsset("v/x.pck", 10)}, ident)
	if !s.IsSkipped("v/x.pck") || !s.IsSkipped("v/x_"+hex32+".hash") {
		t.Error("hash listed before pck must still be skipped (two-pass)")
	}
	wantStat(t, s, odGameCat, onDemandStat{N: 2, Bytes: 11})
}

func TestOnDemand_CompanionNotWhenPckPresent(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	touch(t, dir, "v/x.pck")
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10), odAsset("v/x_"+hex32+".hash", 1)}, ident)
	if s.IsSkipped("v/x.pck") || s.IsSkipped("v/x_"+hex32+".hash") {
		t.Error("pck present → neither skipped")
	}
}

func TestOnDemand_CompanionRegexStrict(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	names := []string{"v/x_" + hex31 + ".hash", "v/y_" + hex32 + ".hash", "v/x_" + strings.ToUpper(hex32) + ".HASH"}
	assets := []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10)}
	for _, n := range names {
		assets = append(assets, odAsset(n, 1))
	}
	s.Decide(odGameCat, assets, ident)
	for _, n := range names {
		if s.IsSkipped(n) {
			t.Errorf("%q must not be treated as a companion", n)
		}
	}
}

func TestOnDemand_CompanionSelfBlacklisted(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	h := "v/z_" + hex32 + ".hash"
	writeBlacklist(t, dir, blEntry(h))
	touch(t, dir, "v/z.pck")
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("v/z.pck", 10), odAsset(h, 1)}, ident)
	if s.IsSkipped("v/z.pck") {
		t.Error("pck not blacklisted and present → not skipped")
	}
	if !s.IsSkipped(h) {
		t.Error("hash itself blacklisted and absent → base-rule fallback must skip it")
	}
	wantStat(t, s, odGameCat, onDemandStat{N: 1, Bytes: 1})
}

func TestOnDemand_CompanionStemUsesPathExt(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("d.ir/pkg_version"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide(odGameCat, []*pb.SophonManifestAssetProperty{odAsset("d.ir/pkg_version", 10), odAsset("d.ir/pkg_version_"+hex32+".hash", 1)}, ident)
	if !s.IsSkipped("d.ir/pkg_version") || !s.IsSkipped("d.ir/pkg_version_"+hex32+".hash") {
		t.Error("extensionless stem under a dotted directory must pair with its hash (path.Ext semantics)")
	}
}

func TestOnDemand_CompanionScopedToCategory(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide("a", []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10)}, ident)
	s.Decide("b", []*pb.SophonManifestAssetProperty{odAsset("v/x_"+hex32+".hash", 1)}, ident)
	if s.IsSkipped("v/x_" + hex32 + ".hash") {
		t.Error("stem registry is per Decide call; a hash in another category must not follow")
	}
}

func TestOnDemand_CountOnce(t *testing.T) {
	lg, _ := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide("game", []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10), odAsset("v/x.pck", 10)}, ident)
	wantStat(t, s, "game", onDemandStat{N: 1, Bytes: 10})
	s.Decide("ja-jp", []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10), odAsset("v/x_"+hex32+".hash", 1)}, ident)
	if !s.IsSkipped("v/x_" + hex32 + ".hash") {
		t.Error("already-decided pck must still register its stem for companions in the current call")
	}
	wantStat(t, s, "game", onDemandStat{N: 1, Bytes: 10})
	wantStat(t, s, "ja-jp", onDemandStat{N: 1, Bytes: 1})
}

func TestOnDemand_LogSummary(t *testing.T) {
	lg, buf := debugLogger()
	dir := t.TempDir()
	writeBlacklist(t, dir, blEntry("a/b.usm"), blEntry("v/x.pck"))
	s := loadOnDemandSkipper(odGID, dir, lg)
	s.Decide("game", []*pb.SophonManifestAssetProperty{odAsset("a/b.usm", 100)}, ident)
	s.Decide("ja-jp", []*pb.SophonManifestAssetProperty{odAsset("v/x.pck", 10), odAsset("v/x_"+hex32+".hash", 1)}, ident)
	buf.Reset()
	s.LogSummary(lg, "main")
	log := buf.String()
	for _, want := range []string{
		`msg="sophon plan: skipped on-demand assets" phase=main category=game n=1 assetBytes=100`,
		`phase=main category=ja-jp n=2 assetBytes=11`,
		`phase=main category=total n=3 assetBytes=111`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
	if n := strings.Count(log, "skipped on-demand assets"); n != 3 {
		t.Errorf("want 3 lines, got %d:\n%s", n, log)
	}

	// No skips → silent.
	s2 := loadOnDemandSkipper(odGID, dir, lg)
	s2.Decide("game", []*pb.SophonManifestAssetProperty{odAsset("zzz", 1)}, ident)
	buf.Reset()
	s2.LogSummary(lg, "main")
	if buf.Len() != 0 {
		t.Errorf("no skips must log nothing:\n%s", buf.String())
	}
}
