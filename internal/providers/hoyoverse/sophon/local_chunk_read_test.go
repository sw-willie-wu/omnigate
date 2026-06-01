package sophon

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func md5OfHex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestReadLocalChunk_Match(t *testing.T) {
	gameDir := t.TempDir()
	full := []byte("AAAACHUNKBYTESHEREBBBB")
	if err := os.WriteFile(filepath.Join(gameDir, "old.dat"), full, 0o644); err != nil {
		t.Fatal(err)
	}
	want := full[4:14] // "CHUNKBYTES"
	out := filepath.Join(t.TempDir(), "chunk.bin")
	src := ChunkSource{Kind: SourceLocal, OldFile: "old.dat", OldOffset: 4, DecompSize: int64(len(want)), ExpectMD5: md5OfHex(want)}
	if err := ReadLocalChunk(gameDir, src, out); err != nil {
		t.Fatalf("ReadLocalChunk: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}
}

func TestReadLocalChunk_StaleMismatch(t *testing.T) {
	gameDir := t.TempDir()
	full := []byte("AAAACHUNKBYTESHEREBBBB")
	if err := os.WriteFile(filepath.Join(gameDir, "old.dat"), full, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "chunk.bin")
	// ExpectMD5 of DIFFERENT bytes → mismatch
	src := ChunkSource{Kind: SourceLocal, OldFile: "old.dat", OldOffset: 4, DecompSize: 10, ExpectMD5: md5OfHex([]byte("DIFFERENT!"))}
	err := ReadLocalChunk(gameDir, src, out)
	if !errors.Is(err, ErrChunkStale) {
		t.Fatalf("expected ErrChunkStale, got %v", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out must not be written on stale")
	}
}

func TestReadLocalChunk_ENOENTOldFile(t *testing.T) {
	gameDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "chunk.bin")
	src := ChunkSource{Kind: SourceLocal, OldFile: "missing.dat", OldOffset: 0, DecompSize: 4, ExpectMD5: md5OfHex([]byte("abcd"))}
	err := ReadLocalChunk(gameDir, src, out)
	if !errors.Is(err, ErrChunkStale) {
		t.Fatalf("ENOENT old-file should map to ErrChunkStale, got %v", err)
	}
}
