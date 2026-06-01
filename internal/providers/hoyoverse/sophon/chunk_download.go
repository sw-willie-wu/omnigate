package sophon

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
)

// ErrChunkVerify is returned when a CDN chunk fails integrity verification
// after the retry budget is exhausted.
var ErrChunkVerify = errors.New("sophon: chunk verification failed")

// chunkRetryBackoff is the per-chunk retry schedule (3 attempts after the
// first failure → 4 total transfer attempts is NOT intended; the loop runs
// len(backoff)+1 == but we cap at 3 total per spec §5.2: attempts 1,2,3 with
// sleeps 1s,4s before attempts 2,3). The slice length controls total attempts.
var chunkRetryBackoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}

// ctxReader wraps an io.Reader and returns ctx.Err() from Read once the context
// is cancelled, so cancellation propagates through wrapping readers such as
// zstd.NewReader (spec §5.4).
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// DownloadChunk fetches src from the CDN, decompresses (if UseCompress), verifies
// integrity, and atomically writes the decompressed bytes to out. Verification
// uses xxh64 of the decompressed bytes when ChunkName's first 16 hex chars parse
// as a uint64; otherwise MD5 vs src.ExpectMD5 (spec §0). If out already exists and
// verifies, the download is skipped. Retries per chunkRetryBackoff; on exhaustion
// returns ErrChunkVerify.
func DownloadChunk(ctx context.Context, hc *http.Client, src ChunkSource, out string) error {
	wantXXH, useXXH := ParseXXHName(src.ChunkName)

	// Skip if out already exists and verifies.
	if existing, err := os.ReadFile(out); err == nil {
		if verifyBytes(existing, useXXH, wantXXH, src.ExpectMD5) {
			return nil
		}
		_ = os.Remove(out)
	}

	var lastErr error
	for attempt := 0; attempt < len(chunkRetryBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(chunkRetryBackoff[attempt-1]):
			}
		}
		err := downloadChunkOnce(ctx, hc, src, out, useXXH, wantXXH)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = err
	}
	return fmt.Errorf("%w: %s: %v", ErrChunkVerify, src.ChunkName, lastErr)
}

func downloadChunkOnce(ctx context.Context, hc *http.Client, src ChunkSource, out string, useXXH bool, wantXXH uint64) error {
	url := src.URLPrefix + "/" + src.ChunkName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chunk GET %s: status %d", url, resp.StatusCode)
	}

	var reader io.Reader = &ctxReader{ctx: ctx, r: resp.Body}
	var zr *zstd.Decoder
	if src.UseCompress {
		zr, err = zstd.NewReader(reader)
		if err != nil {
			return err
		}
		defer zr.Close()
		reader = zr
	}

	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	var h hash.Hash64
	var m hash.Hash
	var sink io.Writer = f
	if useXXH {
		h = xxhash.New()
		sink = io.MultiWriter(f, h)
	} else {
		m = md5.New()
		sink = io.MultiWriter(f, m)
	}

	if _, err := io.Copy(sink, reader); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	var ok bool
	if useXXH {
		ok = h.Sum64() == wantXXH
	} else {
		ok = hex.EncodeToString(m.Sum(nil)) == src.ExpectMD5
	}
	if !ok {
		_ = os.Remove(tmp)
		return fmt.Errorf("verify mismatch for %s", src.ChunkName)
	}
	return SafeAtomicRename(tmp, out)
}

// DownloadPatchBlob fetches the full patch blob and atomically writes it to out,
// verifying MD5 against p.PatchMD5. Skips if out already exists and verifies.
func DownloadPatchBlob(ctx context.Context, hc *http.Client, p PatchInstr, out string) error {
	if existing, err := os.ReadFile(out); err == nil {
		if md5hexBytes(existing) == p.PatchMD5 {
			return nil
		}
		_ = os.Remove(out)
	}

	url := p.URLPrefix + "/" + p.PatchName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("patch GET %s: status %d", url, resp.StatusCode)
	}

	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	m := md5.New()
	if _, err := io.Copy(io.MultiWriter(f, m), &ctxReader{ctx: ctx, r: resp.Body}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if hex.EncodeToString(m.Sum(nil)) != p.PatchMD5 {
		_ = os.Remove(tmp)
		return fmt.Errorf("patch blob MD5 mismatch for %s", p.PatchName)
	}
	return SafeAtomicRename(tmp, out)
}

// ParseXXHName reports whether ChunkName's first 16 chars parse as a hex uint64,
// returning the decoded value when they do. Exported so the apply-side staging
// verify (Task 21) applies the identical xxh64-then-MD5 rule (§E.3 P12).
func ParseXXHName(name string) (uint64, bool) {
	if len(name) < 16 {
		return 0, false
	}
	v, err := strconv.ParseUint(name[:16], 16, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func verifyBytes(b []byte, useXXH bool, wantXXH uint64, wantMD5 string) bool {
	if useXXH {
		return xxhash.Sum64(b) == wantXXH
	}
	return md5hexBytes(b) == wantMD5
}

func md5hexBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
