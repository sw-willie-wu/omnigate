package hpatchz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedHpatchzNonEmpty(t *testing.T) {
	if len(embeddedHpatchz) == 0 {
		t.Fatal("embeddedHpatchz is empty; Task 1 may not have committed the binary")
	}
	if len(embeddedHpatchz) < 100*1024 {
		t.Errorf("embeddedHpatchz size %d bytes is suspiciously small (<100KB); expected ~250-900KB", len(embeddedHpatchz))
	}
}

func TestEmbeddedSHAComputed(t *testing.T) {
	want := sha256.Sum256(embeddedHpatchz)
	got := embeddedHpatchzSHA()
	if hex.EncodeToString(want[:]) != got {
		t.Errorf("SHA mismatch: want %s got %s", hex.EncodeToString(want[:]), got)
	}
	// First 8 hex chars used as cache filename suffix.
	if len(got) < 8 {
		t.Fatal("sha256 hex too short")
	}
}

func TestExtractHpatchzOnce_CachesAndReuses(t *testing.T) {
	tmp := t.TempDir()
	prevTempDir := osTempDirHpatchz
	osTempDirHpatchz = func() string { return tmp }
	defer func() { osTempDirHpatchz = prevTempDir }()
	resetHpatchzExtractOnce()

	path1, err := extractHpatchzOnce(context.Background())
	if err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if !strings.Contains(path1, embeddedHpatchzSHA()[:8]) {
		t.Errorf("path %q should contain SHA-8 prefix %q", path1, embeddedHpatchzSHA()[:8])
	}
	stat1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mtime1 := stat1.ModTime()

	// Wait a tick to detect any rewrite.
	time.Sleep(20 * time.Millisecond)

	path2, err := extractHpatchzOnce(context.Background())
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if path1 != path2 {
		t.Errorf("expected same path, got %q vs %q", path1, path2)
	}
	stat2, err := os.Stat(path2)
	if err != nil {
		t.Fatal(err)
	}
	if !stat2.ModTime().Equal(mtime1) {
		t.Errorf("file rewrite suspected; mtime changed: %v → %v", mtime1, stat2.ModTime())
	}
}

func TestRunHpatchz_BogusFiles(t *testing.T) {
	tmp := t.TempDir()
	prevTempDir := osTempDirHpatchz
	osTempDirHpatchz = func() string { return tmp }
	defer func() { osTempDirHpatchz = prevTempDir }()
	resetHpatchzExtractOnce()

	// Run with non-existent input paths to exercise the full Run pipeline:
	//   1. ctx not canceled → reaches extract
	//   2. extractHpatchzOnce succeeds (writes binary)
	//   3. exec.CommandContext(... -f <bogus> <bogus> <bogus>) runs
	//   4. hpatchz exits non-zero (cannot open input)
	//   5. Run returns error wrapping ExitError + stderr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := Run(ctx,
		filepath.Join(tmp, "nonexistent_old"),
		filepath.Join(tmp, "nonexistent_diff"),
		filepath.Join(tmp, "nonexistent_new"),
	)
	if err == nil {
		t.Fatal("expected error from hpatchz with bogus input paths")
	}
	// Verify error wraps stderr / exit-code info (loose check; substring may
	// vary by hpatchz version but "hpatchz" should appear in our wrap).
	if !strings.Contains(err.Error(), "hpatchz") {
		t.Errorf("expected error mentions hpatchz; got %v", err)
	}
}
