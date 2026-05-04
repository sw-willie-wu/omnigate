package iconext

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtract_NonExistentPath(t *testing.T) {
	_, err := Extract(filepath.Join(os.TempDir(), "definitely-not-an-exe.exe"))
	if err == nil {
		t.Errorf("expected error on non-existent path, got nil")
	}
}

func TestExtract_Windows_Sample(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PE icon extraction requires Windows")
	}
	bytesOut, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(bytesOut) == 0 {
		t.Fatalf("extracted bytes empty")
	}
	// Verify the bytes are a valid PNG and decode to a non-zero-sized image.
	img, err := png.Decode(bytes.NewReader(bytesOut))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		t.Errorf("extracted PNG has zero dimensions: %v", bounds)
	}
	t.Logf("extracted PNG: %dx%d, %d bytes", bounds.Dx(), bounds.Dy(), len(bytesOut))
}

func TestExtract_NonWindowsReturnsErrUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("only relevant on non-Windows")
	}
	_, err := Extract("anything")
	if err == nil || err.Error() != ErrUnsupported.Error() {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}

func TestExtract_CachedRepeats(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows")
	}
	a, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Extract("testdata/sample.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("cached repeat returned different bytes: len(a)=%d, len(b)=%d", len(a), len(b))
	}
	// Image dimensions provide a sanity check the cache returns the same content
	imgA, _ := png.Decode(bytes.NewReader(a))
	imgB, _ := png.Decode(bytes.NewReader(b))
	if !boundsEqual(imgA, imgB) {
		t.Errorf("cached repeat returned different image bounds")
	}
}

func boundsEqual(a, b image.Image) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Bounds() == b.Bounds()
}
