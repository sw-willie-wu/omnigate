package sophon

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/klauspost/compress/zstd"

	"omnigate/internal/downloader"
)

// ErrChunkVerify is returned when a download's CONTENT is bad after the retry
// budget is exhausted: the decompressed-content MD5 does not match, or the zstd
// stream was rejected by the decoder (a 200 whose body is not the promised
// frame is corrupt content, semantically the same as a hash mismatch). Shared by
// chunks and patch blobs.
var ErrChunkVerify = errors.New("sophon: chunk verification failed")

// ErrDownload is returned when a download fails for TRANSPORT reasons after the
// retry budget is exhausted: a stalled stream, an unacceptable status, a reset
// connection, a body truncated against its Content-Length.
var ErrDownload = errors.New("sophon: download failed")

// RetryBackoff is the retry schedule shared by chunks and patch blobs. The loop
// runs len(RetryBackoff)+1 == 4 transfer attempts, sleeping 1 s, then 4 s, then
// 16 s before attempts 2, 3 and 4 (21 s of sleep in the worst case). Tests
// override it to keep the suite fast.
var RetryBackoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}

// StallTimeout is how long a body may deliver zero bytes before the shared
// downloader's watchdog aborts the attempt. There is deliberately NO overall
// timeout: an 85 MB patch blob may legitimately take many minutes. Tests
// override it.
var StallTimeout = downloader.DefaultStallTimeout

var (
	// errVerifyMismatch marks a decompressed-content MD5 that does not match.
	errVerifyMismatch = errors.New("sophon: md5 mismatch")
	// errDecode marks a body the zstd decoder refused.
	errDecode = errors.New("sophon: zstd decode failed")
)

// errTap remembers the last non-EOF error the UNDERLYING body reader produced,
// so copyChunkBody can still tell a transport fault from bad content after a
// wrapping decoder has rewritten the error into its own vocabulary.
//
// Excluding io.EOF is load-bearing: zstd reports "CRC check failed" — the most
// typical CDN bit-rot shape — only after it has consumed the whole body, so the
// body's own clean io.EOF arrives alongside it; recording that EOF would make
// every corrupt frame look like a transport failure. A truncation that
// contradicts a declared Content-Length arrives as io.ErrUnexpectedEOF instead,
// which IS recorded and stays a transport failure.
type errTap struct {
	r   io.Reader
	err error
}

func (t *errTap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.err = err
	}
	return n, err
}

// copyChunkBody streams body — zstd-decoding it when useCompress — into w, and
// maps the failure onto this package's taxonomy:
//
//	the body reader failed      → that error, raw (transport / stall / ctx)
//	a filesystem fault or a
//	short write                 → that error, raw. These are the two shapes a
//	                              failing SINK produces: os.File surfaces every
//	                              fault (disk full included) as *fs.PathError,
//	                              and io.MultiWriter / the zstd decoder's WriteTo
//	                              synthesise a bare io.ErrShortWrite. A filesystem
//	                              fault is never retried and surfaces as
//	                              "internal"; io.ErrShortWrite is returned raw
//	                              here too (not blamed on the content), but it is
//	                              not a filesystem error, so retryDownload still
//	                              retries it and it exhausts as ErrDownload
//	                              ("network") — practically unreachable, since
//	                              os.File.Write and md5.Write never short-write
//	                              without also returning an error.
//	anything else               → errDecode-wrapped (the decoder rejected the
//	                              bytes ⇒ corrupt content, retried like an MD5
//	                              mismatch and reported as ErrChunkVerify)
//
// It is a free function so the classification can be unit-tested without a
// server or a disk.
func copyChunkBody(body io.Reader, useCompress bool, w io.Writer) error {
	tap := &errTap{r: body}
	var src io.Reader = tap
	if useCompress {
		zr, err := zstd.NewReader(tap)
		if err != nil {
			return fmt.Errorf("%w: %v", errDecode, err)
		}
		// The decoder runs goroutines; Close must always happen.
		defer zr.Close()
		src = zr
	}
	if _, cerr := io.Copy(w, src); cerr != nil {
		if tap.err != nil {
			return tap.err
		}
		if downloader.IsFilesystemErr(cerr) || errors.Is(cerr, io.ErrShortWrite) {
			return cerr
		}
		return fmt.Errorf("%w: %v", errDecode, cerr)
	}
	return nil
}

// retryDownload runs once until it succeeds or the budget in RetryBackoff is
// spent, and turns the last failure into one of this package's two sentinels.
// The order of the checks is the contract:
//
//	cancellation first  — the user's intent outranks any fault it provoked
//	filesystem next     — a local fault will not heal, so do not burn attempts
//	then classify       — content faults become ErrChunkVerify, the rest
//	                      ErrDownload, both keeping the cause reachable via
//	                      errors.Is through the two %w verbs
func retryDownload(ctx context.Context, name string, once func() error) error {
	var (
		lastErr    error
		lastVerify bool
	)
	attempts := len(RetryBackoff) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(RetryBackoff[attempt-1]):
			}
		}
		err := once()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if downloader.IsFilesystemErr(err) {
			return err
		}
		lastVerify = errors.Is(err, errVerifyMismatch) || errors.Is(err, errDecode)
		lastErr = err
		slog.Debug("sophon: download attempt failed", "name", name, "attempt", attempt+1, "err", err)
	}
	slog.Warn("sophon: download exhausted retries", "name", name, "attempts", attempts, "err", lastErr)
	if lastVerify {
		return fmt.Errorf("%w: %s: %w", ErrChunkVerify, name, lastErr)
	}
	return fmt.Errorf("%w: %s: %w", ErrDownload, name, lastErr)
}

// DownloadChunk fetches src from the CDN, decompresses (if UseCompress), verifies
// integrity, and atomically writes the decompressed bytes to out. Verification is
// MD5 of the DECOMPRESSED bytes vs src.ExpectMD5 (= ChunkDecompressedHashMd5).
//
// NOTE: the ChunkName's 16-hex prefix is the xxh64 of the COMPRESSED on-wire bytes
// (verified against the live HoYoverse CDN 2026-06-01), NOT the decompressed
// content — so it must NOT be used to verify the decompressed/staged bytes. The
// decompressed-content MD5 is authoritative. If out already exists and verifies,
// the download is skipped. Otherwise there are 4 transfer attempts per
// RetryBackoff; on exhaustion the error is ErrChunkVerify (bad content) or
// ErrDownload (transport). A local filesystem fault is returned raw and is never
// retried.
func DownloadChunk(ctx context.Context, hc *http.Client, src ChunkSource, out string) error {
	// Skip if out already exists and verifies (decompressed-content MD5).
	if existing, err := os.ReadFile(out); err == nil {
		if md5hexBytes(existing) == src.ExpectMD5 {
			return nil
		}
		_ = os.Remove(out)
	}

	return retryDownload(ctx, src.ChunkName, func() error {
		return downloadChunkOnce(ctx, hc, src, out)
	})
}

func downloadChunkOnce(ctx context.Context, hc *http.Client, src ChunkSource, out string) error {
	// Open the sink BEFORE the GET so a filesystem fault costs zero requests.
	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	// Verify the DECOMPRESSED content via MD5 == src.ExpectMD5
	// (ChunkDecompressedHashMd5). See DownloadChunk doc re: the ChunkName xxh
	// prefix being the COMPRESSED-wire hash, not used here.
	m := md5.New()
	url := src.URLPrefix + "/" + src.ChunkName
	if err := downloader.Fetch(ctx, hc, url, downloader.Options{Stall: StallTimeout}, func(body io.Reader) error {
		return copyChunkBody(body, src.UseCompress, io.MultiWriter(f, m))
	}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}

	// Everything below runs after Fetch has returned, i.e. outside the stall
	// window: a slow Sync must never be mistaken for a dead stream.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if hex.EncodeToString(m.Sum(nil)) != src.ExpectMD5 {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %s", errVerifyMismatch, src.ChunkName)
	}
	if err := SafeAtomicRename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// DownloadPatchBlob fetches the full patch blob and atomically writes it to out,
// verifying MD5 against p.PatchMD5. Skips if out already exists and verifies.
// Retries on the same budget as DownloadChunk, with the same error taxonomy.
func DownloadPatchBlob(ctx context.Context, hc *http.Client, p PatchInstr, out string) error {
	if existing, err := os.ReadFile(out); err == nil {
		if md5hexBytes(existing) == p.PatchMD5 {
			return nil
		}
		_ = os.Remove(out)
	}

	return retryDownload(ctx, p.PatchName, func() error {
		return downloadPatchBlobOnce(ctx, hc, p, out)
	})
}

func downloadPatchBlobOnce(ctx context.Context, hc *http.Client, p PatchInstr, out string) error {
	// Sink first, same as downloadChunkOnce.
	tmp := out + ".tmp"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	m := md5.New()
	url := p.URLPrefix + "/" + p.PatchName
	if err := downloader.Fetch(ctx, hc, url, downloader.Options{Stall: StallTimeout}, func(body io.Reader) error {
		// Patch blobs are served uncompressed.
		return copyChunkBody(body, false, io.MultiWriter(f, m))
	}); err != nil {
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
		return fmt.Errorf("%w: %s", errVerifyMismatch, p.PatchName)
	}
	if err := SafeAtomicRename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ParseXXHName reports whether ChunkName's first 16 chars parse as a hex uint64,
// returning the decoded value when they do. The value is the xxh64 of the
// COMPRESSED on-wire chunk bytes (HoYoverse download-integrity hash); content
// integrity is verified separately via the decompressed-content MD5
// (ChunkDecompressedHashMd5). Retained for a possible future on-wire transfer
// check; not currently used for verification.
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

func md5hexBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
