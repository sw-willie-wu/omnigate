package kurogames

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
	"sync/atomic"
	"time"

	"omnigate/internal/core"
)

const (
	downloadWorkers = 4 // empirical CDN throttle threshold; M3.B+ may surface as setting

	netRetries  = 3
	hashRetries = 2

	// defaultStallTimeout is how long the body stream may deliver ZERO bytes
	// before the stall watchdog aborts the request. Deliberately NOT an
	// overall request timeout: a 24 GiB pak at any speed keeps resetting the
	// watchdog, whereas http.Client.Timeout capped the whole transfer at
	// 5 minutes and made >5-min files deterministically fail (2026-07-10
	// WuWa 3.4.1→3.5.0 pakchunk70 incident).
	defaultStallTimeout = 60 * time.Second
)

var netBackoff = []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}

// errStalled is the cancellation cause the stall watchdog injects into the
// per-request context; classify() keys off it to distinguish "our watchdog
// killed a stalled stream" (retryable) from a user cancel (terminal).
var errStalled = errors.New("download stalled")

// downloader wraps the dependencies needed for a download phase.
//
// TODO(M3.A.v2): chunkInfos resume — files >100 MiB carry per-100-MiB
// chunk MD5s in manifestFileRaw.ChunkInfos enabling byte-range resume.
// M3.A's downloader IGNORES this field — does single GETs and full-file
// MD5. Trade-off: a 30 GB pak file failing at 20 GB re-downloads from 0.
type downloader struct {
	client    *http.Client
	logger    *slog.Logger
	tempRoot  string                 // <TempDir>/<gameID-flat>/<version>/
	progress  *progressStore
	plan      *core.UpdatePlan
	onEvent   func(core.UpdateEvent) // throttled by App layer
	bytesDone atomic.Int64           // sum across workers
	clock     RetryClock             // injected for retry backoff in tests

	// stallTimeout overrides defaultStallTimeout (tests inject short values).
	// Zero/negative means default — read via stall(), never directly, so
	// zero-valued downloader literals keep working.
	stallTimeout time.Duration
}

func (d *downloader) stall() time.Duration {
	if d.stallTimeout <= 0 {
		return defaultStallTimeout
	}
	return d.stallTimeout
}

// classify maps a failed request's error to retry semantics. Three-way by
// design (spec 2026-07-10 §3.1 BLK-1): collapsing to two branches either
// mislabels user cancels as retryable or returns nil for ordinary transport
// errors (which would send a truncated .part into hash verification and
// surface as bogus `corrupt`).
func (d *downloader) classify(ctx, reqCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if context.Cause(reqCtx) == errStalled { // 1. our watchdog fired → retryable
		return &core.UpdateError{
			Code:      "network",
			Retryable: true,
			Params:    map[string]string{"reason": "stalled"},
		}
	}
	if ctx.Err() != nil { // 2. parent cancelled (user/deadline) → terminal
		return ctx.Err()
	}
	return err // 3. plain transport error → raw; netRetries loop wraps it
}

// RetryClock abstracts time.Sleep + time.Now + time.NewTicker for the
// download-retry backoff seam. **Distinct from `app.Clock`** (which only
// needs Now/NewTicker for the ticker-drain emitter — no Sleep). The
// distinct name avoids confusion when reading both packages side-by-side.
// Production: realRetryClock; tests: fakeRetryClock (instant Sleep).
type RetryClock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
	Sleep(d time.Duration)
}

type realRetryClock struct{}

func (realRetryClock) Now() time.Time                         { return time.Now() }
func (realRetryClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (realRetryClock) Sleep(d time.Duration)                  { time.Sleep(d) }

// isComplete reports whether f is already fully downloaded and verified:
// progress entry present with matching size AND the staged file exists on
// disk with matching size + mtime (ms truncated). The pre-count in
// runDownload and the skip check in processFile MUST both use this — if
// their conditions diverge, a file can be pre-counted AND re-downloaded,
// double-counting its bytes (spec 2026-07-10 D3).
func isComplete(dir string, f core.FileTask, pf *core.ProgressFile) bool {
	if pf == nil {
		return false
	}
	e, ok := pf.Entries[f.Path]
	if !ok || e.Size != f.Size {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, f.Path))
	return err == nil && fi.Size() == f.Size && fi.ModTime().Truncate(time.Millisecond).Equal(e.MTime)
}

// runDownload runs the download phase: dispatches plan.Files across N
// workers, retries net/hash failures per policy, emits per-file progress.
// Returns nil on success or *core.UpdateError on terminal failure.
func (d *downloader) runDownload(ctx context.Context) error {
	// Snapshot existing progress once, before workers spawn. Workers skip
	// off this snapshot: a file's own entry can only be written by its own
	// job (one file = one job), so the snapshot never goes stale for the
	// skip decision.
	progress, _ := core.LoadProgress(d.progress.dir())
	type job struct {
		index int
		file  core.FileTask
	}

	// Pre-count completed bytes so progress UI is accurate from tick 1.
	for _, f := range d.plan.Files {
		if isComplete(d.progress.dir(), f, progress) {
			d.bytesDone.Add(f.Size)
		}
	}

	jobCh := make(chan job, len(d.plan.Files))
	errCh := make(chan error, downloadWorkers)

	// Spawn workers
	for w := 0; w < downloadWorkers; w++ {
		go func() {
			for j := range jobCh {
				if err := d.processFile(ctx, j.file, progress); err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}

	// Enqueue
	for i, f := range d.plan.Files {
		select {
		case <-ctx.Done():
			close(jobCh)
			return ctx.Err()
		case jobCh <- job{index: i, file: f}:
		}
	}
	close(jobCh)

	// Wait for all workers to finish or first error
	var firstErr error
	for w := 0; w < downloadWorkers; w++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// processFile downloads one file with retries + verification + progress
// update. Skips if isComplete says so — the SAME predicate the pre-count
// used, so skip and pre-count can never disagree.
//
// Files with chunk metadata go through chunkedDownload (called exactly once
// — it owns its per-chunk retries; spec BLK-2), falling back to the classic
// full-GET loop only when the CDN ignores Range requests. Chunk-less files
// take the full-GET loop directly.
func (d *downloader) processFile(ctx context.Context, f core.FileTask, pf *core.ProgressFile) error {
	finalPath := filepath.Join(d.progress.dir(), f.Path)

	if isComplete(d.progress.dir(), f, pf) {
		// Already complete; emit progress tick for accurate UI
		d.emitProgress(f.Path)
		return nil
	}

	partPath := finalPath + ".part"
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(finalPath), err)
	}

	downloadedByChunks := false
	if len(f.Chunks) > 0 {
		err := d.chunkedDownload(ctx, f, partPath)
		switch {
		case err == nil:
			downloadedByChunks = true
		case errors.Is(err, errRangeUnsupported):
			d.logger.Info("CDN ignores Range; falling back to full download", "path", f.Path)
			// chunkedDownload already refunded its bytes and removed .part.
		default:
			return err
		}
	}
	if !downloadedByChunks {
		// Cleanup any leftover .part before re-download (spec §5.2) — the
		// full-GET path cannot validate partial content.
		_ = os.Remove(partPath)
		if err := d.fullGetDownload(ctx, f, partPath); err != nil {
			return err
		}
	}

	// Finalize (shared by both paths): rename .part → final, record progress
	// with exact mtime captured post-rename. Stat error here would corrupt
	// progress.json with zero values — surface it rather than silently
	// degrade resume semantics (Task 8 review fix).
	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("rename %s: %w", finalPath, err)
	}
	fi, err := os.Stat(finalPath)
	if err != nil {
		return fmt.Errorf("stat post-rename %s: %w", finalPath, err)
	}
	if err := d.progress.MarkComplete(f.Path, fi.ModTime(), fi.Size()); err != nil {
		return fmt.Errorf("progress.MarkComplete: %w", err)
	}
	d.emitProgress(f.Path)
	return nil
}

// fullGetDownload runs the whole-file download with netRetries backoff —
// the classic path for chunk-less files and the Range fallback.
func (d *downloader) fullGetDownload(ctx context.Context, f core.FileTask, partPath string) error {
	for attempt := 0; attempt <= netRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt > 0 {
			d.clock.Sleep(netBackoff[attempt-1])
		}
		if err := d.downloadAndVerify(ctx, f, partPath); err != nil {
			// User cancel is terminal regardless of attempt budget — never
			// wrap it as network (classify branch 2 surfaces it raw).
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Structured errors terminate UNLESS they are retryable network
			// (a stalled stream from the watchdog behaves like any transport
			// failure and must consume the netRetries budget).
			var ue *core.UpdateError
			if errors.As(err, &ue) && !(ue.Code == "network" && ue.Retryable) {
				return ue // e.g. hash mismatch retries exhausted → corrupt
			}
			d.logger.Debug("download attempt failed", "path", f.Path, "attempt", attempt+1, "err", err)
			if attempt == netRetries {
				if ue != nil {
					return ue // already a proper network UpdateError (stalled)
				}
				return &core.UpdateError{
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
		return nil
	}
	return &core.UpdateError{Code: "internal", Retryable: true} // unreachable
}

// downloadAndVerify writes .part, hashes during stream, fails on hash
// mismatch (caller retries up to hashRetries times before surfacing
// `corrupt`). Net errors propagate to caller which manages netRetries.
//
// This function is the sole debtor of bytesDone on the full-GET path:
// rollback happens at each DISCARD (failed attempt's stranded bytes, or a
// hash-mismatched .part being removed) — never tied to this function's own
// error return, because a mismatch-then-success sequence discards bytes on
// the way to a nil return (spec 2026-07-10 BLOCKING-2: that path used to
// leave the file counted twice). Callers must not touch bytesDone.
func (d *downloader) downloadAndVerify(ctx context.Context, f core.FileTask, partPath string) error {
	for hashAttempt := 0; hashAttempt <= hashRetries; hashAttempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		gotHash, written, err := d.singleDownload(ctx, f.URL, partPath, f.Size)
		if err != nil {
			// The attempt's bytes are stranded in a doomed .part (the next
			// attempt's os.Create truncates it) — refund immediately.
			d.bytesDone.Add(-written)
			return err
		}
		if gotHash == f.Hash {
			return nil
		}
		// Discarding the .part → refund its bytes, retry or surface corrupt.
		d.bytesDone.Add(-written)
		d.logger.Debug("hash mismatch", "path", f.Path, "got", gotHash, "want", f.Hash, "attempt", hashAttempt+1)
		_ = os.Remove(partPath)
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

// singleDownload streams URL into partPath while computing MD5 (the
// kurogames manifest hash algorithm per research markdown 2026-05-05).
// It reports written — the exact amount it added to bytesDone — on EVERY
// return path, so the caller can refund a discarded attempt. It never
// rolls back itself (single responsibility: downloadAndVerify owns refunds).
func (d *downloader) singleDownload(ctx context.Context, urlStr, partPath string, expectedSize int64) (hash string, written int64, err error) {
	// Stall watchdog: the request runs on a derived cancel-cause context; the
	// watchdog cancels it with errStalled after stall() with NO bytes. First
	// cancel wins in WithCancelCause, so the deferred cancel(nil) cannot
	// clobber the errStalled cause.
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(d.stall(), func() { cancel(errStalled) })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", 0, d.classify(ctx, reqCtx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http status %d", resp.StatusCode)
	}

	f, err := os.Create(partPath)
	if err != nil {
		return "", 0, fmt.Errorf("create part: %w", err)
	}
	defer f.Close()

	h := md5.New()
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil { // parent ctx: user cancel
			return "", written, err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(d.stall())
			if _, werr := f.Write(buf[:n]); werr != nil {
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
	if expectedSize > 0 && written != expectedSize {
		return "", written, fmt.Errorf("size mismatch: got %d, want %d", written, expectedSize)
	}
	return hex.EncodeToString(h.Sum(nil)), written, nil
}

// emitProgress sends a throttled UpdateEvent. App-layer ticker-drain
// emitter further coalesces to 8 Hz for byte progress; per-file completion
// is also bounded (callers don't spam).
func (d *downloader) emitProgress(currentFile string) {
	if d.onEvent == nil {
		return
	}
	d.onEvent(core.UpdateEvent{
		Phase:       core.PhaseDownload,
		Current:     d.bytesDone.Load(),
		Total:       d.plan.TotalBytes,
		CurrentFile: currentFile,
	})
}
