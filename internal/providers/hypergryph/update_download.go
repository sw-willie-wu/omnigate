package hypergryph

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
	downloadWorkers = 4

	netRetries  = 3
	hashRetries = 2
)

var netBackoff = []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}

type downloader struct {
	client    *http.Client
	logger    *slog.Logger
	tempRoot  string
	progress  *progressStore
	plan      *core.UpdatePlan
	onEvent   func(core.UpdateEvent)
	bytesDone atomic.Int64
	clock     RetryClock
}

// RetryClock abstracts time for the download-retry backoff seam.
type RetryClock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
	Sleep(d time.Duration)
}

type realRetryClock struct{}

func (realRetryClock) Now() time.Time                         { return time.Now() }
func (realRetryClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (realRetryClock) Sleep(d time.Duration)                  { time.Sleep(d) }

func (d *downloader) runDownload(ctx context.Context) error {
	progress, _ := core.LoadProgress(d.progress.dir())
	type job struct {
		index int
		file  core.FileTask
	}

	if progress != nil {
		for _, f := range d.plan.Files {
			if e, ok := progress.Entries[f.Path]; ok {
				if e.Size == f.Size {
					d.bytesDone.Add(f.Size)
				}
			}
		}
	}

	jobCh := make(chan job, len(d.plan.Files))
	errCh := make(chan error, downloadWorkers)

	for w := 0; w < downloadWorkers; w++ {
		go func() {
			for j := range jobCh {
				if err := d.processFile(ctx, j.file); err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}

	for i, f := range d.plan.Files {
		select {
		case <-ctx.Done():
			close(jobCh)
			return ctx.Err()
		case jobCh <- job{index: i, file: f}:
		}
	}
	close(jobCh)

	var firstErr error
	for w := 0; w < downloadWorkers; w++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (d *downloader) processFile(ctx context.Context, f core.FileTask) error {
	finalPath := filepath.Join(d.progress.dir(), f.Path)

	progress, _ := core.LoadProgress(d.progress.dir())
	if progress != nil {
		if e, ok := progress.Entries[f.Path]; ok {
			if e.Size == f.Size {
				if fi, err := os.Stat(finalPath); err == nil && fi.Size() == f.Size && fi.ModTime().Truncate(time.Millisecond).Equal(e.MTime) {
					d.emitProgress(f.Path)
					return nil
				}
			}
		}
	}

	partPath := finalPath + ".part"
	_ = os.Remove(partPath)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(finalPath), err)
	}

	for attempt := 0; attempt <= netRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt > 0 {
			d.clock.Sleep(netBackoff[attempt-1])
		}
		if err := d.downloadAndVerify(ctx, f, partPath); err != nil {
			var ue *core.UpdateError
			if errors.As(err, &ue) {
				return ue
			}
			d.logger.Debug("download attempt failed", "path", f.Path, "attempt", attempt+1, "err", err)
			if attempt == netRetries {
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
	return &core.UpdateError{Code: "internal", Retryable: true} // unreachable
}

func (d *downloader) downloadAndVerify(ctx context.Context, f core.FileTask, partPath string) error {
	for hashAttempt := 0; hashAttempt <= hashRetries; hashAttempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		gotHash, err := d.singleDownload(ctx, f.URL, partPath, f.Size)
		if err != nil {
			return err
		}
		if gotHash == f.Hash {
			return nil
		}
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

func (d *downloader) singleDownload(ctx context.Context, urlStr, partPath string, expectedSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", &core.UpdateError{Code: "manifest_not_found", Retryable: false, Params: map[string]string{"url": sanitizeURL(urlStr)}}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	f, err := os.Create(partPath)
	if err != nil {
		return "", fmt.Errorf("create part: %w", err)
	}
	defer f.Close()

	h := md5.New()
	written := int64(0)
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			h.Write(buf[:n])
			written += int64(n)
			d.bytesDone.Add(int64(n))
			d.emitProgress("")
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if expectedSize > 0 && written != expectedSize {
		return "", fmt.Errorf("size mismatch: got %d, want %d", written, expectedSize)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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
