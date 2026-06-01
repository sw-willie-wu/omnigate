package hoyoverse

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omnigate/internal/core"
)

// downloadAll dispatches `tasks` across `workerCount` workers, downloading
// each FileTask with byte-range resume + MD5 verify + 3× exponential-backoff
// retry. Persists completion via ps.MarkComplete; partial state is preserved
// in `<versionDir>/<path>.part` for resume on next run.
//
// onProgress (optional) is called with cumulative bytes downloaded across
// all workers. Cancel propagates via ctx; pool drains and returns ctx.Err().
func downloadAll(ctx context.Context, ps *progressStore, tasks []core.FileTask, workerCount int, onProgress func(int64)) error {
	if workerCount < 1 {
		workerCount = 1
	}
	if err := os.MkdirAll(ps.versionDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir versionDir: %w", err)
	}

	pf := ps.snapshot()
	// Seed the running total with bytes already on disk so the percentage is
	// accurate across a resume and across multi-file plans where some files are
	// already done: fully-completed files contribute their whole size; a
	// partially-downloaded file contributes its .part prefix (downloadOneAttempt
	// resumes from that offset and streams only the remaining bytes).
	var bytesDone int64
	pending := make([]core.FileTask, 0, len(tasks))
	for _, t := range tasks {
		if e, ok := pf.Entries[t.Path]; ok && e.Hash == t.Hash && e.Size == t.Size {
			bytesDone += t.Size
			continue
		}
		if stat, err := os.Stat(filepath.Join(ps.versionDir(), t.Path+".part")); err == nil {
			if sz := stat.Size(); sz <= t.Size {
				bytesDone += sz
			}
		}
		pending = append(pending, t)
	}
	if onProgress != nil {
		onProgress(bytesDone)
	}
	if len(pending) == 0 {
		return nil
	}

	taskCh := make(chan core.FileTask)
	errCh := make(chan error, workerCount)
	var bytesMu sync.Mutex
	progress := func(delta int64) {
		bytesMu.Lock()
		bytesDone += delta
		v := bytesDone
		bytesMu.Unlock()
		if onProgress != nil {
			onProgress(v)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				if err := downloadOne(ctx, ps, task, progress); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	go func() {
		defer close(taskCh)
		for _, t := range pending {
			select {
			case <-ctx.Done():
				return
			case taskCh <- t:
			}
		}
	}()

	wg.Wait()
	close(errCh)

	if err := ctx.Err(); err != nil {
		return err
	}
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func downloadOne(ctx context.Context, ps *progressStore, task core.FileTask, progress func(int64)) error {
	const maxAttempts = 3
	delays := []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}
		err := downloadOneAttempt(ctx, ps, task, progress)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return &core.UpdateError{
		Code:      "download_corrupted",
		Params:    map[string]string{"path": task.Path, "url": sanitizeURL(task.URL), "err": lastErr.Error()},
		Retryable: true,
	}
}

func downloadOneAttempt(ctx context.Context, ps *progressStore, task core.FileTask, progress func(int64)) error {
	finalPath := filepath.Join(ps.versionDir(), task.Path)
	partPath := finalPath + ".part"

	var startOffset int64
	if stat, err := os.Stat(partPath); err == nil {
		startOffset = stat.Size()
		if startOffset > task.Size {
			_ = os.Remove(partPath)
			startOffset = 0
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", task.URL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if startOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if startOffset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	defer f.Close()

	hasher := md5.New()
	if startOffset > 0 {
		prefix, err := os.Open(partPath)
		if err != nil {
			return fmt.Errorf("re-read prefix: %w", err)
		}
		_, _ = io.CopyN(hasher, prefix, startOffset)
		prefix.Close()
	}

	w := io.MultiWriter(f, hasher)
	var src io.Reader = resp.Body
	if progress != nil {
		// Stream progress as bytes flow so the bar advances during a single
		// large file. HoYoverse legacy plans (HSR/ZZZ) are a few huge package
		// files; reporting only at completion left the UI stuck at 0% for the
		// whole multi-GB download.
		src = &progressReader{r: resp.Body, report: progress}
	}
	written, err := io.Copy(w, src)
	if err != nil {
		return fmt.Errorf("download body: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close part: %w", err)
	}

	totalSize := startOffset + written
	if totalSize != task.Size {
		return fmt.Errorf("size mismatch: got %d want %d", totalSize, task.Size)
	}
	gotHash := hex.EncodeToString(hasher.Sum(nil))
	if gotHash != task.Hash {
		return fmt.Errorf("md5 mismatch: got %s want %s", gotHash, task.Hash)
	}

	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	stat, _ := os.Stat(finalPath)
	return ps.MarkComplete(task.Path, totalSize, stat.ModTime(), gotHash)
}

// progressReader wraps an io.Reader, invoking report with the number of bytes
// read on each Read. Used to stream download progress during io.Copy (delta per
// read; downloadAll accumulates the deltas into a running total).
type progressReader struct {
	r      io.Reader
	report func(int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 && pr.report != nil {
		pr.report(int64(n))
	}
	return n, err
}

func sanitizeURL(u string) string {
	if idx := strings.Index(u, "?"); idx >= 0 {
		return u[:idx]
	}
	return u
}
