package sevenzip

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// testdata/sample.7z is an LZMA2 archive containing:
//
//	hello.txt      -> "hello sevenzip"
//	sub/world.txt  -> "nested world"
func TestExtract_RoundTrip(t *testing.T) {
	resetExtractOnce()
	dest := t.TempDir()
	if err := Extract(context.Background(), filepath.Join("testdata", "sample.7z"), dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cases := map[string]string{
		"hello.txt":                       "hello sevenzip",
		filepath.Join("sub", "world.txt"): "nested world",
	}
	for rel, want := range cases {
		got, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, string(got), want)
		}
	}
}

func TestExtract_NotAnArchive(t *testing.T) {
	resetExtractOnce()
	bad := filepath.Join(t.TempDir(), "garbage.7z")
	if err := os.WriteFile(bad, []byte("not a 7z file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Extract(context.Background(), bad, t.TempDir()); err == nil {
		t.Fatal("expected error extracting a non-7z file")
	}
}

func TestExtract_CanceledContext(t *testing.T) {
	resetExtractOnce()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Extract(ctx, filepath.Join("testdata", "sample.7z"), t.TempDir()); err == nil {
		t.Fatal("expected error with canceled context")
	}
}
