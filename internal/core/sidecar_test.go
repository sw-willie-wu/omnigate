package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProgress_CorruptReturnsErr(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProgress(dir); err == nil {
		t.Error("expected err on corrupt JSON")
	}
}
