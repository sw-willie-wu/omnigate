package sophon

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrChunkStale signals that a Path-B local chunk read could not produce verified
// bytes — either the old file is absent or the bytes at (OldFile, OldOffset) no
// longer hash to ExpectMD5 (user modded / HoYoPlay-updated between plan and apply).
// The caller falls back to a CDN fetch for the same chunk. ENOENT on the old file
// is deliberately wrapped as ErrChunkStale so both fall-back paths converge.
var ErrChunkStale = errors.New("sophon: local chunk MD5 mismatch")

// ReadLocalChunk reads DecompSize bytes from <gameDir>/<src.OldFile> at src.OldOffset,
// verifies their MD5 against src.ExpectMD5, and atomically writes them to out.
// Returns ErrChunkStale on MD5 mismatch or when the old file does not exist.
func ReadLocalChunk(gameDir string, src ChunkSource, out string) error {
	oldPath := filepath.Join(gameDir, src.OldFile)
	f, err := os.Open(oldPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: old file missing %s", ErrChunkStale, src.OldFile)
		}
		return err
	}
	defer f.Close()

	if _, err := f.Seek(src.OldOffset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, src.DecompSize)
	if _, err := io.ReadFull(f, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: short read at %s+%d", ErrChunkStale, src.OldFile, src.OldOffset)
		}
		return err
	}

	sum := md5.Sum(buf)
	if hex.EncodeToString(sum[:]) != src.ExpectMD5 {
		return fmt.Errorf("%w: %s+%d", ErrChunkStale, src.OldFile, src.OldOffset)
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return SafeAtomicRename(tmp, out)
}
