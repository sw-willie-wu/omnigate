package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"omnigate/internal/core"
)

// errRangeUnsupported signals the CDN answered a Range request with 200
// (full body) instead of 206. chunkedDownload cleans up (refund + .part
// removal) before returning it; processFile falls back to full-GET.
var errRangeUnsupported = errors.New("server ignores Range requests")

// chunkedDownload downloads f into partPath chunk by chunk using HTTP Range
// requests, resuming any valid prefix already present in partPath. Called
// exactly ONCE per processFile invocation — it owns all per-chunk net/hash
// retries internally, so the resume-scan runs once and cannot re-commit
// bytes across attempts (spec 2026-07-10 BLK-2).
//
// Accounting contract (spec §3.2): `committed` tracks every byte this call
// has booked into bytesDone (resume-scan prefix + chunks that passed MD5).
// In-flight bytes of a failed/mismatched attempt are refunded in place by
// the chunk helpers and never enter committed — the deferred refund and the
// in-place refunds therefore operate on disjoint sets. On ANY error return
// the defer refunds committed in full; on errRangeUnsupported it also
// removes the .part (full-GET restarts from a truncated file and re-counts
// from zero).
func (d *downloader) chunkedDownload(ctx context.Context, f core.FileTask, partPath string) (err error) {
	var committed int64
	defer func() {
		if err != nil {
			d.bytesDone.Add(-committed)
			if errors.Is(err, errRangeUnsupported) {
				_ = os.Remove(partPath)
			}
		}
	}()

	// Resume scan (once per call): find the first chunk whose bytes in
	// .part are absent, short (M4: short read == mismatch) or corrupt.
	resumeFrom := 0
	if pf, oerr := os.Open(partPath); oerr == nil {
		for _, c := range f.Chunks {
			if !chunkValid(pf, c) {
				break
			}
			resumeFrom++
		}
		pf.Close()
	}
	for _, c := range f.Chunks[:resumeFrom] {
		n := c.End - c.Start + 1
		d.bytesDone.Add(n)
		committed += n
	}
	if resumeFrom > 0 {
		d.logger.Debug("chunked resume", "path", f.Path, "valid_chunks", resumeFrom, "of", len(f.Chunks))
		d.emitProgress("")
	}

	out, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	for i := resumeFrom; i < len(f.Chunks); i++ {
		c := f.Chunks[i]
		if cerr := d.downloadChunk(ctx, f, out, c); cerr != nil {
			out.Close()
			return cerr
		}
		committed += c.End - c.Start + 1
	}
	// Close before hashing/renaming — Windows cannot rename an open file.
	if cerr := out.Close(); cerr != nil {
		return fmt.Errorf("close part: %w", cerr)
	}

	// Whole-file verification: cheap insurance over the per-chunk MD5s
	// (also catches a wrong final length). Cancellable mid-hash (M-c).
	if fi, serr := os.Stat(partPath); serr != nil || fi.Size() != f.Size {
		return fmt.Errorf("chunked part size: stat=%v, want %d bytes", serr, f.Size)
	}
	got, err := d.md5FileCtx(ctx, partPath)
	if err != nil {
		return err
	}
	if got != f.Hash {
		return &core.UpdateError{
			Code:      "corrupt",
			Retryable: true,
			Params: map[string]string{
				"url":  sanitizeURL(f.URL),
				"path": f.Path,
			},
		}
	}
	return nil
}

// downloadChunk fetches one chunk with per-chunk hash retries (net retries
// live one level down). A mismatched chunk's bytes are refunded before the
// re-download; a verified chunk's bytes stay booked (caller commits them).
func (d *downloader) downloadChunk(ctx context.Context, f core.FileTask, out *os.File, c core.Chunk) error {
	want := c.End - c.Start + 1
	for hashAttempt := 0; hashAttempt <= hashRetries; hashAttempt++ {
		gotHash, err := d.chunkWithNetRetries(ctx, f, out, c)
		if err != nil {
			return err
		}
		if gotHash == c.Hash {
			return nil
		}
		// Discarding the range → refund before re-downloading it.
		d.bytesDone.Add(-want)
		d.logger.Debug("chunk hash mismatch", "path", f.Path, "start", c.Start, "got", gotHash, "want", c.Hash, "attempt", hashAttempt+1)
		if hashAttempt == hashRetries {
			return &core.UpdateError{
				Code:      "corrupt",
				Retryable: true,
				Params: map[string]string{
					"url":  sanitizeURL(f.URL),
					"path": f.Path,
				},
			}
		}
	}
	return &core.UpdateError{Code: "internal"} // unreachable
}

// chunkWithNetRetries performs one verified-bytes attempt of a chunk,
// retrying transport failures per netRetries/netBackoff (mirrors
// processFile's full-GET loop at chunk granularity). Failed attempts refund
// their streamed bytes in place; on success the bytes stay booked and the
// hash is returned for the caller to judge.
func (d *downloader) chunkWithNetRetries(ctx context.Context, f core.FileTask, out *os.File, c core.Chunk) (string, error) {
	for attempt := 0; attempt <= netRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if attempt > 0 {
			d.clock.Sleep(netBackoff[attempt-1])
		}
		gotHash, written, err := d.rangeGet(ctx, f.URL, out, c)
		if err != nil {
			d.bytesDone.Add(-written) // refund the aborted attempt in place
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if errors.Is(err, errRangeUnsupported) {
				return "", err
			}
			var ue *core.UpdateError
			if errors.As(err, &ue) && !(ue.Code == "network" && ue.Retryable) {
				return "", ue
			}
			d.logger.Debug("chunk attempt failed", "path", f.Path, "start", c.Start, "attempt", attempt+1, "err", err)
			if attempt == netRetries {
				if ue != nil {
					return "", ue
				}
				return "", &core.UpdateError{
					Code:      "network",
					Retryable: true,
					Params: map[string]string{
						"url":    sanitizeURL(f.URL),
						"reason": err.Error(),
					},
				}
			}
			continue
		}
		return gotHash, nil
	}
	return "", &core.UpdateError{Code: "internal"} // unreachable
}

// rangeGet issues one Range GET for chunk c, writing bytes at their final
// offset via WriteAt and hashing as it streams. Reports written — its exact
// bytesDone contribution — on every return path (same contract as
// singleDownload). Stall watchdog + classify semantics identical to the
// full-GET path.
func (d *downloader) rangeGet(ctx context.Context, urlStr string, out *os.File, c core.Chunk) (hash string, written int64, err error) {
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(d.stall(), func() { cancel(errStalled) })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", c.Start, c.End))
	resp, err := d.client.Do(req)
	if err != nil {
		return "", 0, d.classify(ctx, reqCtx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "", 0, errRangeUnsupported
	}
	if resp.StatusCode != http.StatusPartialContent {
		return "", 0, fmt.Errorf("http status %d for range request", resp.StatusCode)
	}

	want := c.End - c.Start + 1
	h := md5.New()
	buf := make([]byte, 64*1024)
	for {
		if e := ctx.Err(); e != nil { // parent ctx: user cancel
			return "", written, e
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if written+int64(n) > want {
				return "", written, fmt.Errorf("server sent beyond requested range")
			}
			watchdog.Reset(d.stall())
			if _, werr := out.WriteAt(buf[:n], c.Start+written); werr != nil {
				return "", written, werr
			}
			h.Write(buf[:n])
			written += int64(n)
			d.bytesDone.Add(int64(n))
			d.emitProgress("") // throttled byte progress
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", written, d.classify(ctx, reqCtx, rerr)
		}
	}
	if written != want {
		return "", written, fmt.Errorf("range size mismatch: got %d, want %d", written, want)
	}
	return hex.EncodeToString(h.Sum(nil)), written, nil
}

// chunkValid reports whether r holds chunk c's exact bytes at its offsets.
// A short read (truncated .part) counts as mismatch per spec M4.
func chunkValid(r io.ReaderAt, c core.Chunk) bool {
	want := c.End - c.Start + 1
	h := md5.New()
	n, err := io.Copy(h, io.NewSectionReader(r, c.Start, want))
	if err != nil || n != want {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == c.Hash
}

// md5FileCtx hashes a file like md5File but checks ctx between buffers so a
// user cancel is honored mid-hash (a 24 GiB verify takes ~45s).
func (d *downloader) md5FileCtx(ctx context.Context, path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := md5.New()
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := fh.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
