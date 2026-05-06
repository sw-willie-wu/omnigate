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
