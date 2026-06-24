package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDataDir_EnvOverrideWins(t *testing.T) {
	t.Setenv("OMNIGATE_DATA_DIR", `X:\custom`)
	if got := ResolveDataDir(); got != `X:\custom` {
		t.Fatalf("got %q", got)
	}
}

func TestUnderTempDir(t *testing.T) {
	if !underTempDir(filepath.Join(os.TempDir(), "go-buildXYZ")) {
		t.Fatal("temp-dir exe not detected")
	}
	if underTempDir(`C:\Program Files\omnigate`) {
		t.Fatal("non-temp path wrongly flagged")
	}
}

func TestDataDirWritable(t *testing.T) {
	if !DataDirWritable(t.TempDir()) {
		t.Fatal("temp dir should be writable")
	}
	if DataDirWritable(filepath.Join(t.TempDir(), "does", "not", "exist")) {
		t.Fatal("missing dir not writable")
	}
}
