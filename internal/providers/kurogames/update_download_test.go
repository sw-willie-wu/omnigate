package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omnigate/internal/core"
)

// fakeRetryClock makes Sleep instant for fast tests.
type fakeRetryClock struct{}

func (fakeRetryClock) Now() time.Time                         { return time.Now() }
func (fakeRetryClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (fakeRetryClock) Sleep(d time.Duration)                  {} // instant

// md5hex returns the lowercase-hex MD5 of s — matches the kurogames manifest
// hash format (research markdown 2026-05-05).
func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// slogTest returns a real *slog.Logger that discards output. downloader.logger
// is typed as *slog.Logger (concrete), so we MUST return a concrete logger here.
func slogTest(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestDownload_HappyPath(t *testing.T) {
	body := "hello world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Version:    "3.4.0",
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}

	d := &downloader{
		client:   srv.Client(),
		logger:   slogTest(t),
		progress: ps,
		plan:     plan,
		clock:    fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.dir(), "a.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func Test5xxRetriesThenFails(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("x"), Size: 1, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
	if attempts.Load() != int64(netRetries+1) {
		t.Errorf("attempts = %d, want %d", attempts.Load(), netRetries+1)
	}
}

func TestHashRetriesThenFails(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte("wrong-content"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("expected"), Size: int64(len("wrong-content")), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("err = %v, want corrupt", err)
	}
	if attempts.Load() != int64(hashRetries+1) {
		t.Errorf("attempts = %d, want %d", attempts.Load(), hashRetries+1)
	}
}

func TestDownload_CancelMidStream(t *testing.T) {
	// Server holds connection open
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("x"), Size: 100, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := d.runDownload(ctx)
	if err == nil {
		t.Fatal("expected ctx error")
	}
}

// TestCancel_BeforeStart: ctx already cancelled when runDownload is called
// → must return ctx.Err() immediately, no HTTP traffic, no .part files.
// Spec §7.4 mandate.
func TestCancel_BeforeStart(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("x"), Size: 1, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}

	// Cancel BEFORE runDownload starts
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := d.runDownload(ctx)
	if err == nil {
		t.Fatal("expected ctx.Err(), got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("server hit %d times after pre-cancel; want 0", got)
	}
}

// --- Task A: stall watchdog + classify (spec 2026-07-10 §3.1) ---

// TestStall_NoOverallTimeout (T1): a download whose TOTAL duration far
// exceeds stallTimeout must succeed as long as bytes keep flowing — the
// watchdog fires on "no bytes for stallTimeout", never on elapsed time.
// This is the D1 fix: the old 5-minute http.Client.Timeout made any
// >5-min transfer (e.g. the 24 GiB pakchunk70) deterministically fail.
func TestStall_NoOverallTimeout(t *testing.T) {
	body := strings.Repeat("a", 50)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		fl := w.(http.Flusher)
		for i := 0; i < len(body); i++ {
			w.Write([]byte{body[i]})
			fl.Flush()
			time.Sleep(10 * time.Millisecond) // 50 bytes × 10ms ≈ 500ms total ≫ 100ms stall
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
		stallTimeout: 100 * time.Millisecond,
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("slow-but-flowing download failed: %v", err)
	}
}

// TestStall_DetectedAndRetryable (T2): server sends partial body then hangs.
// Targets the singleDownload seam directly (through runDownload the total
// time would be ~4×stallTimeout across netRetries).
func TestStall_DetectedAndRetryable(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	d := &downloader{
		client: srv.Client(), logger: slogTest(t), clock: fakeRetryClock{},
		stallTimeout: 50 * time.Millisecond,
	}
	start := time.Now()
	_, _, err := d.singleDownload(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x.part"), 100)
	elapsed := time.Since(start)

	var ue *core.UpdateError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v (%T), want *core.UpdateError", err, err)
	}
	if ue.Code != "network" || !ue.Retryable || ue.Params["reason"] != "stalled" {
		t.Errorf("got Code=%q Retryable=%v reason=%q; want network/true/stalled", ue.Code, ue.Retryable, ue.Params["reason"])
	}
	if elapsed > 2*time.Second {
		t.Errorf("stall detection took %v; want ~50ms, definitely not minutes", elapsed)
	}
}

// TestStall_RetriedByNetRetries: a persistent stall must burn the netRetries
// budget (like any transport failure) rather than surfacing after the first
// stall. Pins the processFile retry semantics for retryable network
// UpdateErrors (spec §3.1 branch 1 "可重試").
func TestStall_RetriedByNetRetries(t *testing.T) {
	var attempts atomic.Int64
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("irrelevant"), Size: 100, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
		stallTimeout: 30 * time.Millisecond,
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
	if attempts.Load() != int64(netRetries+1) {
		t.Errorf("attempts = %d, want %d (stall must consume netRetries)", attempts.Load(), netRetries+1)
	}
}

// TestStall_UserCancelNotRetried (T2b): parent-ctx cancel mid-stream must
// NOT be retried — exactly one request, error is context.Canceled.
// Single-file plan keeps the request count deterministic (4 workers).
func TestStall_UserCancelNotRetried(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("x"), Size: 100, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
		// stallTimeout deliberately unset → 60s default; watchdog must not fire here
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := d.runDownload(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (user cancel must not retry)", attempts.Load())
	}
}

// TestMidStreamError_ClassifiedNetwork (T2c): an ordinary mid-stream
// transport failure (server drops conn at half body; watchdog did NOT fire,
// parent ctx alive) must be classified as retryable network — burning
// netRetries, never touching hashRetries, and never reported as corrupt.
// Guards spec BLK-1 (the 2-way classify returned nil for this case).
func TestMidStreamError_ClassifiedNetwork(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // drop the connection mid-body
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("whatever"), Size: 100, URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("mid-stream transport error misclassified as corrupt: %v", err)
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
	if attempts.Load() != int64(netRetries+1) {
		t.Errorf("attempts = %d, want %d (netRetries, not hashRetries)", attempts.Load(), netRetries+1)
	}
}

// --- Task B: per-discard progress rollback + isComplete (spec §3.2) ---

// TestRetryRollback_NetErrorThenSuccess (T3): two half-body drops then a
// clean download. bytesDone must equal TotalBytes exactly — failed attempts'
// partial bytes are refunded, the successful attempt counts once.
func TestRetryRollback_NetErrorThenSuccess(t *testing.T) {
	body := "the-real-content"
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if n <= 2 {
			w.Write([]byte(body[:len(body)/2]))
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // drop mid-body
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	if got := d.bytesDone.Load(); got != plan.TotalBytes {
		t.Errorf("bytesDone = %d, want %d (failed attempts must be refunded)", got, plan.TotalBytes)
	}
}

// TestRetryRollback_AllFail (T4): every attempt drops mid-body. All partial
// bytes must be refunded — bytesDone ends at exactly 0.
func TestRetryRollback_AllFail(t *testing.T) {
	body := "the-real-content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write([]byte(body[:len(body)/2]))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("err = %v, want network", err)
	}
	if got := d.bytesDone.Load(); got != 0 {
		t.Errorf("bytesDone = %d, want 0 (all partial bytes refunded)", got)
	}
}

// TestHashMismatchRollback_AllFail (T5): hash never matches. Every full-file
// download is discarded and refunded — corrupt error, bytesDone exactly 0.
func TestHashMismatchRollback_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("wrong-content"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len("wrong-content")),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex("expected"), Size: int64(len("wrong-content")), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("err = %v, want corrupt", err)
	}
	if got := d.bytesDone.Load(); got != 0 {
		t.Errorf("bytesDone = %d, want 0", got)
	}
}

// TestHashMismatchRollback_ThenSuccess (T5b): first download has wrong
// content (same length → hash-mismatch path, not size-mismatch), second is
// correct. bytesDone must be exactly Size, not 2×Size. This is the exact
// over-count spec BLOCKING-2 identified: rollback tied to function error
// return misses the mid-loop discard on the eventually-successful path.
func TestHashMismatchRollback_ThenSuccess(t *testing.T) {
	right := "right-stuff"
	wrong := "wrong-stuff" // same length: triggers hash mismatch, not size mismatch
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Write([]byte(wrong))
			return
		}
		w.Write([]byte(right))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len(right)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(right), Size: int64(len(right)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	if got := d.bytesDone.Load(); got != plan.TotalBytes {
		t.Errorf("bytesDone = %d, want %d (discarded mismatch must be refunded)", got, plan.TotalBytes)
	}
}

// TestSizeMismatchRollback: server closes cleanly after a SHORT body (no
// Content-Length → client sees clean EOF, not a read error). singleDownload
// returns the size-mismatch error with written>0; those bytes must be
// refunded like any other discarded attempt.
func TestSizeMismatchRollback(t *testing.T) {
	body := "the-real-content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body[:len(body)/2])) // clean short body, no CL header
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	plan := &core.UpdatePlan{
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("err = %v, want network (size mismatch is transport-ish)", err)
	}
	if got := d.bytesDone.Load(); got != 0 {
		t.Errorf("bytesDone = %d, want 0 (short-body bytes refunded)", got)
	}
}

// TestPrecountSkipConsistency (T6): progress.json has an entry but the file
// is GONE from disk. Pre-count and the per-file skip check must agree (both
// via isComplete): the file is re-downloaded and counted exactly once —
// never "pre-counted AND re-downloaded" (spec D3 double-count).
func TestPrecountSkipConsistency(t *testing.T) {
	body := "hello"
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Entry claims completion, but the file does not exist on disk.
	if err := ps.MarkComplete("a.dll", time.Now(), int64(len(body))); err != nil {
		t.Fatal(err)
	}

	plan := &core.UpdatePlan{
		TotalBytes: int64(len(body)),
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (missing file must be re-downloaded)", attempts.Load())
	}
	if got := d.bytesDone.Load(); got != plan.TotalBytes {
		t.Errorf("bytesDone = %d, want %d (no pre-count + re-download double-count)", got, plan.TotalBytes)
	}
}

func TestDownload_ResumeSkipsCompleted(t *testing.T) {
	// Pre-populate progress.json with one completed file
	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Create the file with known content
	body := "hello"
	completePath := filepath.Join(ps.dir(), "a.dll")
	if err := os.WriteFile(completePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(completePath)
	if err := ps.MarkComplete("a.dll", fi.ModTime(), fi.Size()); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	plan := &core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "a.dll", Hash: md5hex(body), Size: int64(len(body)), URL: srv.URL},
		},
		TotalBytes: int64(len(body)),
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 0 {
		t.Errorf("server hit %d times; should be 0 (resume skipped)", attempts.Load())
	}
}
