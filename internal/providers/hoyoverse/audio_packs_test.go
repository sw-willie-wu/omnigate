package hoyoverse

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// audioAssetsRel mirrors what the impl uses; fixture builder echoes it.
const testAudioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

func TestDetectInstalledLanguages_None(t *testing.T) {
	dir := t.TempDir()
	got, err := DetectInstalledLanguages(core.GameID("hoyoverse/genshin"), dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestDetectInstalledLanguages_OneLang(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, testAudioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstalledLanguages(core.GameID("hoyoverse/genshin"), dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "Chinese" {
		t.Errorf("expected [Chinese], got %v", got)
	}
}

func TestDetectInstalledLanguages_MultipleLangs(t *testing.T) {
	dir := t.TempDir()
	for _, lang := range []string{"Chinese", "English(US)", "Japanese", "Korean"} {
		if err := os.MkdirAll(filepath.Join(dir, testAudioAssetsRel, lang), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DetectInstalledLanguages(core.GameID("hoyoverse/genshin"), dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("expected 4 langs, got %d: %v", len(got), got)
	}
	// Result should be sorted alphabetically (per spec).
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
}

func TestDetectInstalledLanguages_IgnoresFiles(t *testing.T) {
	dir := t.TempDir()
	audioDir := filepath.Join(dir, testAudioAssetsRel)
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Add a real lang dir + a stray file (e.g. audio_lang_14 indirection file).
	if err := os.MkdirAll(filepath.Join(audioDir, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio_lang_14"), []byte("Chinese\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstalledLanguages(core.GameID("hoyoverse/genshin"), dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "Chinese" {
		t.Errorf("expected files ignored, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Star Rail (Sophon, 4.6+): Persistent audio root + launcher record file.
// ---------------------------------------------------------------------------

const (
	hsrGID         = core.GameID("hoyoverse/starrail")
	hsrAudioRel    = "StarRail_Data/Persistent/Audio/AudioPackage/Windows"
	hsrRecordRel   = "StarRail_Data/Persistent/AudioLaucherRecord.txt"
	zzzGIDForAudio = core.GameID("hoyoverse/zzz")
)

// mkAudioFolders creates <dir>/<rel>/<folder> for each folder.
func mkAudioFolders(t *testing.T, dir, rel string, folders ...string) {
	t.Helper()
	for _, f := range folders {
		if err := os.MkdirAll(filepath.Join(dir, rel, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// writeHSRRecord writes the launcher record. It only creates the record's
// parent dir (StarRail_Data/Persistent), never the Audio tree, so tests can
// assert behaviour when AudioAssetsRel does not exist.
func writeHSRRecord(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, hsrRecordRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureSlog swaps the default slog handler for a buffer for the duration
// of the test and returns the buffer.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func assertLangs(t *testing.T, got []string, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil slice, want non-nil %v", want)
	}
	if len(want) == 0 {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// spec (b): no record → folder scan.
func TestDetectInstalledLanguages_StarRail_FolderScan(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Chinese(PRC)", "Japanese")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Chinese(PRC)", "Japanese")
}

// spec (a): record lists Japanese; a partial Chinese(PRC) folder exists but
// is not launcher-installed → only Japanese, and a mismatch warning is logged.
func TestDetectInstalledLanguages_StarRail_RecordCRLF(t *testing.T) {
	for _, rec := range []string{"Japanese\r\n", "Japanese\n", "Japanese"} {
		t.Run(fmt.Sprintf("%q", rec), func(t *testing.T) {
			dir := t.TempDir()
			mkAudioFolders(t, dir, hsrAudioRel, "Chinese(PRC)", "Japanese")
			writeHSRRecord(t, dir, rec)
			buf := captureSlog(t)
			got, err := DetectInstalledLanguages(hsrGID, dir)
			assertLangs(t, got, err, "Japanese")
			log := buf.String()
			for _, want := range []string{
				"audio record/folder mismatch",
				"recorded=[Japanese]",
				`scanned="[Chinese(PRC) Japanese]"`,
				"using=[Japanese]",
			} {
				if !strings.Contains(log, want) {
					t.Errorf("log missing %q; got:\n%s", want, log)
				}
			}
		})
	}
}

func TestDetectInstalledLanguages_StarRail_RecordMultiSeparators(t *testing.T) {
	for _, rec := range []string{"Japanese, Korean", "Japanese\nKorean", "Japanese;Korean"} {
		t.Run(fmt.Sprintf("%q", rec), func(t *testing.T) {
			dir := t.TempDir()
			mkAudioFolders(t, dir, hsrAudioRel, "Japanese", "Korean")
			writeHSRRecord(t, dir, rec)
			buf := captureSlog(t)
			got, err := DetectInstalledLanguages(hsrGID, dir)
			assertLangs(t, got, err, "Japanese", "Korean")
			if strings.Contains(buf.String(), "mismatch") {
				t.Errorf("unexpected mismatch warning:\n%s", buf.String())
			}
		})
	}
}

// Tokens in matching_field form ("ja-jp") are accepted and mapped to folders.
// A second installed folder makes the folder-scan fallback answer differ, so
// this test fails if code-form tokens are silently rejected.
func TestDetectInstalledLanguages_StarRail_RecordCodeForm(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese", "Korean")
	writeHSRRecord(t, dir, "ja-jp")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese")
}

// The stale-record union only admits KNOWN folders from the scan: stray
// non-language directories under the audio root (e.g. "SFX") never leak into
// the plan.
func TestDetectInstalledLanguages_StarRail_RecordStaleUnionFiltersUnknown(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese", "SFX")
	writeHSRRecord(t, dir, "Korean")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese", "Korean")
}

func TestDetectInstalledLanguages_StarRail_RecordUnknownFiltered(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese")
	writeHSRRecord(t, dir, "Foo\nJapanese")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese")
}

func TestDetectInstalledLanguages_StarRail_RecordOnlyUnknownFallsBack(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese")
	writeHSRRecord(t, dir, "Foo")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese")
}

func TestDetectInstalledLanguages_StarRail_RecordEmptyFallsBack(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Chinese(PRC)")
	writeHSRRecord(t, dir, "  \r\n")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Chinese(PRC)")
}

// spec (c): record is stale (names a language with no folder) → union with
// the installed known folders so an installed pack is never left behind.
func TestDetectInstalledLanguages_StarRail_RecordStaleUnion(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese")
	writeHSRRecord(t, dir, "Korean")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese", "Korean")
}

// spec (d): record names Japanese but the audio tree is gone → still
// Japanese (re-download), no existence check.
func TestDetectInstalledLanguages_StarRail_RecordFolderMissingKept(t *testing.T) {
	dir := t.TempDir()
	writeHSRRecord(t, dir, "Japanese")
	got, err := DetectInstalledLanguages(hsrGID, dir)
	assertLangs(t, got, err, "Japanese")
}

// ZZZ has no audio meta → always empty, even if look-alike trees exist.
func TestDetectInstalledLanguages_ZZZ_AlwaysEmpty(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, testAudioAssetsRel, "Chinese")
	mkAudioFolders(t, dir, hsrAudioRel, "Japanese")
	got, err := DetectInstalledLanguages(zzzGIDForAudio, dir)
	assertLangs(t, got, err)
}

func TestDetectInstalledLanguages_UnknownGame_Empty(t *testing.T) {
	dir := t.TempDir()
	mkAudioFolders(t, dir, testAudioAssetsRel, "Chinese")
	got, err := DetectInstalledLanguages(core.GameID("bogus/game"), dir)
	assertLangs(t, got, err)
}
