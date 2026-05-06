package hoyoverse

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func makeZipBlob(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	buf := bytes.Buffer{}
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipsToStaging(t *testing.T) {
	versionDir := t.TempDir()
	zipBlob := makeZipBlob(t, map[string][]byte{
		"hdiffmap.json":                 []byte(`{"entries":[]}`),
		"GenshinImpact_Data/foo.hdiff":  []byte("PATCH-DATA"),
		"deletefiles.txt":               []byte("OldFile.dll\n"),
	})
	zipPath := filepath.Join(versionDir, "patch.zip")
	if err := os.WriteFile(zipPath, zipBlob, 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir := filepath.Join(versionDir, "staging")
	if err := extractZipToStaging(context.Background(), zipPath, stagingDir); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, name := range []string{"hdiffmap.json", "GenshinImpact_Data/foo.hdiff", "deletefiles.txt"} {
		if _, err := os.Stat(filepath.Join(stagingDir, name)); err != nil {
			t.Errorf("expected %s in staging: %v", name, err)
		}
	}
}

func TestParseHdiffmap(t *testing.T) {
	data, err := os.ReadFile("testdata/hdiffmap-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	hm, err := parseHdiffmap(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hm.Entries) != 1 {
		t.Fatalf("entries len = %d want 1", len(hm.Entries))
	}
	e := hm.Entries[0]
	if e.SourceMD5Hash != "5eb63bbbe01eeed093cb22bb8f5acdc3" {
		t.Errorf("sourceMD5Hash mismatch: %q", e.SourceMD5Hash)
	}
}

func TestSourceMD5Verify_Match(t *testing.T) {
	gameDir := t.TempDir()
	relPath := "GenshinImpact_Data/Native/Data/foo.dat"
	srcPath := filepath.Join(gameDir, relPath)
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatal(err)
	}
	srcContent := []byte("hello world")
	if err := os.WriteFile(srcPath, srcContent, 0o644); err != nil {
		t.Fatal(err)
	}
	wantMD5 := md5.Sum(srcContent)
	if err := verifySourceMD5(srcPath, hex.EncodeToString(wantMD5[:])); err != nil {
		t.Errorf("expected match; got %v", err)
	}
}

func TestSourceMD5Verify_Mismatch(t *testing.T) {
	gameDir := t.TempDir()
	srcPath := filepath.Join(gameDir, "foo.dat")
	if err := os.WriteFile(srcPath, []byte("modified content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySourceMD5(srcPath, "deadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Error("expected error on MD5 mismatch")
	}
}

func TestParseHdifffiles_JSONPerLine(t *testing.T) {
	data, err := os.ReadFile("testdata/hdifffiles-sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseHdifffiles(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries len = %d want 3 (blank line skipped)", len(entries))
	}
	want := []string{
		"GenshinImpact_Data/Native/Data/foo.dat",
		"GenshinImpact_Data/StreamingAssets/AudioAssets/Banks0.pck",
		"GenshinImpact_Data/Plugins/x86_64/UnityPlayer.dll",
	}
	for i, w := range want {
		if entries[i].RemoteName != w {
			t.Errorf("entries[%d].RemoteName = %q want %q", i, entries[i].RemoteName, w)
		}
	}
}

func TestParseHdifffiles_InvalidJSONLine(t *testing.T) {
	bad := []byte(`{"remoteName": "ok.dll"}` + "\n" + `{not json}` + "\n")
	if _, err := parseHdifffiles(bad); err == nil {
		t.Error("expected parse error on malformed JSON line")
	}
}

func TestParseHdifffiles_EmptyRemoteName(t *testing.T) {
	bad := []byte(`{"remoteName": ""}` + "\n")
	if _, err := parseHdifffiles(bad); err == nil {
		t.Error("expected error on empty remoteName")
	}
}

func TestApplyPatchZip_Legacy_SkipsMissingSource(t *testing.T) {
	// Legacy zip: hdifffiles.txt + .hdiff entries; source files are NOT in gameDir
	// so every entry should be silently skipped (matches reference impl behavior).
	versionDir := t.TempDir()
	gameDir := t.TempDir() // empty — no source files
	stagingDir := filepath.Join(versionDir, "staging")

	zipBlob := makeZipBlob(t, map[string][]byte{
		"hdifffiles.txt":                                []byte(`{"remoteName": "GenshinImpact_Data/missing.dll"}` + "\n"),
		"GenshinImpact_Data/missing.dll.hdiff":          []byte("PATCH-DATA"),
	})
	zipPath := filepath.Join(versionDir, "patch.zip")
	if err := os.WriteFile(zipPath, zipBlob, 0o644); err != nil {
		t.Fatal(err)
	}

	emitCalls := []struct {
		stage         string
		current, total int
	}{}
	emit := func(stage string, current, total int) {
		emitCalls = append(emitCalls, struct {
			stage          string
			current, total int
		}{stage, current, total})
	}

	if err := applyPatchZip(context.Background(), zipPath, gameDir, stagingDir, emit); err != nil {
		t.Fatalf("applyPatchZip legacy with missing sources should succeed; got %v", err)
	}

	// Expect: extracting (0,1) → patching (0,0) — zero entries to patch since
	// all listed sources are absent. No hpatchz invocation; no error.
	var sawPatchingZero bool
	for _, c := range emitCalls {
		if c.stage == "patching" && c.current == 0 && c.total == 0 {
			sawPatchingZero = true
		}
	}
	if !sawPatchingZero {
		t.Errorf("expected patching emit with total=0 (no entries to patch); got %+v", emitCalls)
	}
}

func TestExtractAudioOnly_NoHdiffmap(t *testing.T) {
	versionDir := t.TempDir()
	zipBlob := makeZipBlob(t, map[string][]byte{
		"GenshinImpact_Data/StreamingAssets/AudioAssets/Korean/voice1.wem": []byte("WEM-DATA"),
	})
	zipPath := filepath.Join(versionDir, "audio_ko-kr.zip")
	if err := os.WriteFile(zipPath, zipBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	stagingDir := filepath.Join(versionDir, "staging")
	if err := extractZipToStaging(context.Background(), zipPath, stagingDir); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if hasHdiffMetadata(stagingDir) {
		t.Error("expected hasHdiffMetadata false for audio-only zip")
	}
}
