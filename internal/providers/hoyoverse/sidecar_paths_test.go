package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// testSample is hoisted to package level to keep test functions tidy.
type testSample struct {
	X int `json:"x"`
}

func TestGameSidecarDir(t *testing.T) {
	got := gameSidecarDir(`C:\temp\omnigate\hoyoverse`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\omnigate\hoyoverse`, "hoyoverse-genshin")
	if got != want {
		t.Errorf("gameSidecarDir: got %q want %q", got, want)
	}
}

func TestVersionSidecarDir(t *testing.T) {
	got := versionSidecarDir(`C:\temp\omnigate\hoyoverse`, core.GameID("hoyoverse/genshin"), "5.7.0")
	want := filepath.Join(`C:\temp\omnigate\hoyoverse`, "hoyoverse-genshin", "5.7.0")
	if got != want {
		t.Errorf("versionSidecarDir: got %q want %q", got, want)
	}
}

func TestLoadJSONSidecar_Missing(t *testing.T) {
	got, err := loadJSONSidecar[testSample](filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected nil err for ENOENT, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil pointer, got %v", got)
	}
}

func TestLoadJSONSidecar_Corrupt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "corrupt.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadJSONSidecar[testSample](path)
	if err != nil {
		t.Fatalf("expected nil err for corrupt (warn+remove), got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil pointer, got %v", got)
	}
	// File should have been os.Remove'd.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected corrupt file removed, stat err: %v", err)
	}
}

func TestLoadJSONSidecar_Valid(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "valid.json")
	if err := os.WriteFile(path, []byte(`{"x":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadJSONSidecar[testSample](path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == nil || got.X != 42 {
		t.Errorf("expected X=42, got %+v", got)
	}
}

func TestSophonSubdir(t *testing.T) {
	got := sophonSubdir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon")
	if got != want {
		t.Errorf("sophonSubdir: got %q want %q", got, want)
	}
}

func TestSophonManifestsDir(t *testing.T) {
	got := sophonManifestsDir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon", "manifests")
	if got != want {
		t.Errorf("sophonManifestsDir: got %q want %q", got, want)
	}
}

func TestSophonAppliedJSONPath(t *testing.T) {
	got := sophonAppliedJSONPath(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", ".sophon", "applied.json")
	if got != want {
		t.Errorf("sophonAppliedJSONPath: got %q want %q", got, want)
	}
}

func TestSophonStagingDir(t *testing.T) {
	got := sophonStagingDir(`C:\temp\og\hoyo`, core.GameID("hoyoverse/genshin"), "6.6.0", "main", "buildXYZ")
	want := filepath.Join(`C:\temp\og\hoyo`, "hoyoverse-genshin", "6.6.0", "staging", "main", "buildXYZ")
	if got != want {
		t.Errorf("sophonStagingDir: got %q want %q", got, want)
	}
}
