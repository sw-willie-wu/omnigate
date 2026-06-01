package sophon

import (
	"fmt"
	"os"
	"path/filepath"
)

// AssembleFile builds the target file at out from its chunk sources. It is pure:
// MkdirAll(dir(out)), truncate to totalSize, WriteAt each chunk at src.FileOffset,
// fsync, close. It does NOT verify the whole-file MD5 and does NOT rename — the
// caller does both (out is expected to be the *.tmp path). readChunk supplies the
// decompressed bytes for a source, abstracting staging-read (CDN) vs local-read
// (Path-B). Any readChunk error aborts assembly and is wrapped.
func AssembleFile(out string, totalSize int64, sources []ChunkSource, readChunk func(src ChunkSource) ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(totalSize); err != nil {
		_ = f.Close()
		return err
	}
	for _, src := range sources {
		b, err := readChunk(src)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("assemble %s chunk %s: %w", out, src.ChunkName, err)
		}
		if _, err := f.WriteAt(b, src.FileOffset); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
