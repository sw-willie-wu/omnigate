package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

// audioAssetsRel mirrors what the impl uses; fixture builder echoes it.
const testAudioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

func TestDetectInstalledLanguages_None(t *testing.T) {
	dir := t.TempDir()
	got, err := DetectInstalledLanguages(dir)
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
	got, err := DetectInstalledLanguages(dir)
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
	got, err := DetectInstalledLanguages(dir)
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
	got, err := DetectInstalledLanguages(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "Chinese" {
		t.Errorf("expected files ignored, got %v", got)
	}
}
