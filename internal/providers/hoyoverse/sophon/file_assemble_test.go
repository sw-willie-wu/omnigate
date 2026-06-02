package sophon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAssembleFile_ThreeChunksInOrder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	c0, c1, c2 := []byte("AAAA"), []byte("BBBB"), []byte("CCCC")
	sources := []ChunkSource{
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "c1", FileOffset: 4, DecompSize: 4},
		{ChunkName: "c2", FileOffset: 8, DecompSize: 4},
	}
	data := map[string][]byte{"c0": c0, "c1": c1, "c2": c2}
	read := func(src ChunkSource) ([]byte, error) { return data[src.ChunkName], nil }
	if err := AssembleFile(out, 12, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("AAAABBBBCCCC")) {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestAssembleFile_OutOfFileOrderOffsets(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	sources := []ChunkSource{
		{ChunkName: "c2", FileOffset: 8, DecompSize: 4},
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "c1", FileOffset: 4, DecompSize: 4},
	}
	data := map[string][]byte{"c0": []byte("AAAA"), "c1": []byte("BBBB"), "c2": []byte("CCCC")}
	read := func(src ChunkSource) ([]byte, error) { return data[src.ChunkName], nil }
	if err := AssembleFile(out, 12, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("AAAABBBBCCCC")) {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestAssembleFile_NestedPathMkdirAll(t *testing.T) {
	out := filepath.Join(t.TempDir(), "deep", "nested", "dir", "asm.tmp")
	sources := []ChunkSource{{ChunkName: "c0", FileOffset: 0, DecompSize: 3}}
	read := func(src ChunkSource) ([]byte, error) { return []byte("xyz"), nil }
	if err := AssembleFile(out, 3, sources, read); err != nil {
		t.Fatalf("AssembleFile: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, []byte("xyz")) {
		t.Fatalf("content mismatch")
	}
}

func TestAssembleFile_ReadChunkErrorAborts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "asm.tmp")
	sources := []ChunkSource{
		{ChunkName: "c0", FileOffset: 0, DecompSize: 4},
		{ChunkName: "bad", FileOffset: 4, DecompSize: 4},
	}
	boom := errors.New("readChunk failure")
	read := func(src ChunkSource) ([]byte, error) {
		if src.ChunkName == "bad" {
			return nil, boom
		}
		return []byte("AAAA"), nil
	}
	err := AssembleFile(out, 8, sources, read)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped readChunk error, got %v", err)
	}
}
