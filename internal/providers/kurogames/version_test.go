package kurogames

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchVersion_ReadsLauncherDownloadConfig(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "launcherDownloadConfig.json"),
		[]byte(`{"version":"3.3.0","reUseVersion":"","state":"","isPreDownload":false,"appId":"50004"}`),
		0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readLauncherDownloadConfigVersion(filepath.Join(gameDir, "launcherDownloadConfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.3.0" {
		t.Errorf("version = %q, want 3.3.0", got)
	}
	_ = context.Background
}

func TestFetchVersion_MissingFileReturnsEmpty(t *testing.T) {
	got, err := readLauncherDownloadConfigVersion(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("expected nil error on missing file, got %v", err)
	}
	if got != "" {
		t.Errorf("version on missing file = %q, want empty", got)
	}
}

func TestFetchVersion_MalformedJSONReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLauncherDownloadConfigVersion(p)
	if err == nil {
		t.Errorf("expected error on malformed json, got nil; got version=%q", got)
	}
	if got != "" {
		t.Errorf("version on malformed = %q, want empty", got)
	}
}
