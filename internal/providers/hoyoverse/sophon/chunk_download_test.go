package sophon

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"

	"omnigate/internal/downloader"
)

func xxhName(raw []byte) string {
	return fmt.Sprintf("%016x", xxhash.Sum64(raw))
}

func md5hex(raw []byte) string {
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])
}

// zstdCompress is reused from manifest_fetch_test.go in the same package

// fastRetry shrinks the package's retry schedule and stall budget so a test that
// exercises the retry loop finishes in milliseconds instead of the production
// 21 s of sleep. Every test that reaches retryDownload's sleep or the stall
// watchdog must call it.
func fastRetry(t *testing.T) {
	t.Helper()
	oldBackoff, oldStall := RetryBackoff, StallTimeout
	RetryBackoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	StallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { RetryBackoff, StallTimeout = oldBackoff, oldStall })
}

func TestDownloadChunk_ZstdXXHPath(t *testing.T) {
	raw := []byte("hello sophon chunk payload zstd")
	name := xxhName(raw) // first 16 hex = xxh64 of decompressed
	comp := zstdCompress(t, raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(comp)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch: got %q", got)
	}
}

func TestDownloadChunk_RawNoCompress(t *testing.T) {
	raw := []byte("uncompressed raw chunk body")
	name := xxhName(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch")
	}
}

func TestDownloadChunk_XXHMismatchRetryThenSucceed(t *testing.T) {
	fastRetry(t)
	raw := []byte("flaky chunk that fails once")
	name := xxhName(raw)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			_, _ = w.Write([]byte("corrupt body")) // wrong bytes → xxh64 mismatch
			return
		}
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", hits)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch after retry")
	}
}

func TestDownloadChunk_RetryExhaustedErrChunkVerify(t *testing.T) {
	fastRetry(t)
	raw := []byte("always corrupt target")
	name := xxhName(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("never matches"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("expected ErrChunkVerify, got %v", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out should not exist after exhaustion")
	}
}

func TestDownloadChunk_ChunkNameNotHexMD5Fallback(t *testing.T) {
	raw := []byte("md5-only verified chunk")
	name := "not-a-hex-chunk-name.chunk" // first 16 chars don't parse as hex uint64
	if _, err := strconv.ParseUint(name[:16], 16, 64); err == nil {
		t.Fatalf("test precondition: name prefix must NOT parse as hex")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("MD5-fallback path failed: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) {
		t.Fatalf("content mismatch on MD5 fallback")
	}
}

func TestDownloadChunk_SkipIfExistsAndVerifies(t *testing.T) {
	raw := []byte("already present chunk")
	name := xxhName(raw)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	src := ChunkSource{Kind: SourceCDN, ChunkName: name, URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("skip-if-verifies: %v", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected no HTTP hit when out already verifies, got %d", hits)
	}
}

func TestDownloadChunk_CtxCancelMidStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		for i := 0; i < 1000; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName([]byte("z")), URLPrefix: srv.URL, UseCompress: false, DecompSize: 1, ExpectMD5: md5hex([]byte("z"))}
	err := DownloadChunk(ctx, srv.Client(), src, out)
	if err == nil {
		t.Fatalf("expected error on cancel")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("expected context.Canceled (or ErrChunkVerify after exhaustion), got %v", err)
	}
}

func TestDownloadPatchBlob_VerifyAndSkip(t *testing.T) {
	blob := []byte("full patch blob bytes 0123456789")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex(blob), PatchSize: int64(len(blob))}
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err != nil {
		t.Fatalf("DownloadPatchBlob: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, blob) {
		t.Fatalf("blob mismatch")
	}
	// second call must skip (already verifies)
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err != nil {
		t.Fatalf("second DownloadPatchBlob: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 HTTP hit, got %d", hits)
	}
}

func TestDownloadPatchBlob_MD5Mismatch(t *testing.T) {
	fastRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong blob"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex([]byte("expected blob"))}
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err == nil {
		t.Fatalf("expected MD5-mismatch error")
	}
}

// newStallServer starts a test server whose handler is handed a release channel.
// Handlers that hang on it are released BEFORE the server is closed, because
// httptest.Server.Close blocks until every live handler has returned.
func newStallServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, release <-chan struct{})) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, release)
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

// hangForever answers 200 with flushed headers and then delivers zero bytes
// until the test tears the server down — exactly the shape the stall watchdog
// exists for (a connect-level failure would be a different test).
func hangForever(w http.ResponseWriter, release <-chan struct{}) {
	w.WriteHeader(http.StatusOK)
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	<-release
}

// assertNoLeftovers pins that a failed download leaves neither the destination
// nor its .tmp staging file behind.
func assertNoLeftovers(t *testing.T, out string) {
	t.Helper()
	for _, p := range []string{out, out + ".tmp"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s must not exist after a failed download (stat err = %v)", p, err)
		}
	}
}

type errWriter struct{ err error }

func (w errWriter) Write(p []byte) (int, error) { return 0, w.err }

type failReader struct{ err error }

func (r *failReader) Read(p []byte) (int, error) { return 0, r.err }

// shortWriter accepts less than it was given without reporting an error, which
// is how io.Copy comes to synthesise a bare io.ErrShortWrite.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestDownloadChunk_StallRetriesThenSucceeds(t *testing.T) {
	fastRetry(t)
	raw := []byte("chunk that stalls once, then arrives intact")
	comp := zstdCompress(t, raw)
	var hits int32
	srv := newStallServer(t, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		if atomic.AddInt32(&hits, 1) == 1 {
			hangForever(w, release)
			return
		}
		_, _ = w.Write(comp)
	})

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	if err := DownloadChunk(context.Background(), srv.Client(), src, out); err != nil {
		t.Fatalf("the attempt after the stall should succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("hits = %d, want 2 (one stalled attempt, one good one)", got)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("out content: err=%v got=%q", err, got)
	}
}

func TestDownloadChunk_StallExhausted(t *testing.T) {
	fastRetry(t)
	raw := []byte("chunk from a CDN that never sends a single byte")
	var hits int32
	srv := newStallServer(t, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		atomic.AddInt32(&hits, 1)
		hangForever(w, release)
	})

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("an exhausted stall is a transport failure; want ErrDownload, got %v", err)
	}
	if !errors.Is(err, downloader.ErrStalled) {
		t.Fatalf("the cause must stay reachable; want downloader.ErrStalled in the chain, got %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("a watchdog abort must not look like user cancellation: %v", err)
	}
	if errors.Is(err, ErrChunkVerify) {
		t.Fatalf("a stall is not a content fault: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("hits = %d, want 4 transfer attempts", got)
	}
	assertNoLeftovers(t, out)
}

func TestDownloadChunk_CorruptZstdIsVerifyFailure(t *testing.T) {
	fastRetry(t)
	raw := []byte("the payload the CDN was supposed to serve")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("CORRUPT-CHUNK-BYTES"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("a 200 whose body is not zstd is bad CONTENT; want ErrChunkVerify, got %v", err)
	}
	if errors.Is(err, ErrDownload) {
		t.Fatalf("must not be classified as transport: %v", err)
	}
	assertNoLeftovers(t, out)
}

func TestDownloadChunk_CRCMismatchIsVerifyFailure(t *testing.T) {
	fastRetry(t)
	raw := []byte("a chunk whose trailing frame checksum got bit-flipped in transit")
	bad := zstdCompress(t, raw)
	// The last 4 bytes of a zstd frame are its content checksum: break them and
	// the decoder reports "CRC check failed" — arriving together with the body's
	// clean io.EOF. That must be content corruption, not a transport fault.
	for i := len(bad) - 4; i < len(bad); i++ {
		bad[i] ^= 0xFF
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bad)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("a CRC failure is bad content; want ErrChunkVerify, got %v", err)
	}
	if errors.Is(err, ErrDownload) {
		t.Fatalf("must not be classified as transport: %v", err)
	}
	assertNoLeftovers(t, out)
}

func TestDownloadChunk_TruncatedStreamIsDownloadFailure(t *testing.T) {
	fastRetry(t)
	// Incompressible bytes, so half the frame is genuinely short of the payload.
	raw := make([]byte, 32*1024)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	comp := zstdCompress(t, raw)
	if len(comp) < len(raw) {
		t.Fatalf("precondition: random bytes must not compress (comp=%d raw=%d)", len(comp), len(raw))
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// Declare the FULL length, deliver half, then kill the connection.
		// Returning after a half write would instead let Go compute a matching
		// Content-Length and look like a clean EOF. The Flush is insurance for
		// smaller fixtures: this 16 KiB half already exceeds net/http's response
		// buffer and is flushed anyway, but a shorter body would stay in the
		// server's buffer and the client would fail inside hc.Do without ever
		// reading the body. What actually pins that the failure came from the
		// BODY is the no-URL assertion below — an hc.Do failure is always a
		// *url.Error carrying the URL.
		w.Header().Set("Content-Length", strconv.Itoa(len(comp)))
		_, _ = w.Write(comp[:len(comp)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "c.bin")
	src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: true, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}
	err := DownloadChunk(context.Background(), srv.Client(), src, out)
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("a truncated body is a transport failure; want ErrDownload, got %v", err)
	}
	if errors.Is(err, ErrChunkVerify) {
		t.Fatalf("a truncated body must not be reported as bad content: %v", err)
	}
	// The failure must come from reading the BODY, not from hc.Do: the latter
	// always yields a *url.Error whose message carries the URL.
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error carries the URL, so it came from hc.Do and the body was never read: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("hits = %d, want 4 transfer attempts", got)
	}
	assertNoLeftovers(t, out)
}

// Both steps that touch the filesystem before the GET get their own case, so the
// "zero requests" guarantee cannot be satisfied by MkdirAll alone: moving only
// OpenFile after the GET has to fail a test.
func TestDownloadChunk_FilesystemErrorNoRetry(t *testing.T) {
	cases := []struct {
		name string
		// setup returns out, having arranged for the named step to fail.
		setup func(t *testing.T) string
	}{{
		name: "MkdirAll fails: out's parent is a regular file",
		setup: func(t *testing.T) string {
			fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
			if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(fileAsDir, "c.bin")
		},
	}, {
		name: "OpenFile fails: the .tmp staging path is a directory",
		setup: func(t *testing.T) string {
			out := filepath.Join(t.TempDir(), "c.bin")
			// MkdirAll(filepath.Dir(out)) now succeeds, so only the sink open can
			// fail: a directory cannot be opened for writing.
			if err := os.MkdirAll(out+".tmp", 0o755); err != nil {
				t.Fatal(err)
			}
			return out
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastRetry(t)
			raw := []byte("a chunk that can never be written anywhere")
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				_, _ = w.Write(raw)
			}))
			defer srv.Close()

			out := tc.setup(t)
			src := ChunkSource{Kind: SourceCDN, ChunkName: xxhName(raw), URLPrefix: srv.URL, UseCompress: false, DecompSize: int64(len(raw)), ExpectMD5: md5hex(raw)}

			start := time.Now()
			err := DownloadChunk(context.Background(), srv.Client(), src, out)
			elapsed := time.Since(start)

			if !downloader.IsFilesystemErr(err) {
				t.Fatalf("want a filesystem error, got %v", err)
			}
			if errors.Is(err, ErrDownload) || errors.Is(err, ErrChunkVerify) {
				t.Fatalf("a local fault must come back RAW, not wrapped in a download sentinel: %v", err)
			}
			if got := atomic.LoadInt32(&hits); got != 0 {
				t.Fatalf("hits = %d, want 0: the whole sink is prepared before the GET", got)
			}
			if elapsed > 100*time.Millisecond {
				t.Fatalf("filesystem faults must not be retried; took %v", elapsed)
			}
		})
	}
}

func TestCopyChunkBody(t *testing.T) {
	raw := make([]byte, 32*1024)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	comp := zstdCompress(t, raw)

	t.Run("sink filesystem error stays raw", func(t *testing.T) {
		// Errno 112 is Windows ERROR_DISK_FULL — the shape §3.5 relies on to
		// surface as "internal" and never be retried.
		diskFull := &fs.PathError{Op: "write", Path: "c.bin.tmp", Err: syscall.Errno(112)}
		err := copyChunkBody(bytes.NewReader(comp), true, errWriter{err: diskFull})
		if !downloader.IsFilesystemErr(err) {
			t.Fatalf("want the *fs.PathError through untouched, got %v", err)
		}
		if errors.Is(err, errDecode) {
			t.Fatalf("a sink write failure is not a decode failure: %v", err)
		}
	})

	t.Run("sink short write stays raw", func(t *testing.T) {
		// The other sink-failure shape: no *fs.PathError, just io.Copy reporting
		// that the writer swallowed less than it was handed. Still local, so it
		// must not be blamed on the content.
		err := copyChunkBody(bytes.NewReader(raw), false, shortWriter{})
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("want io.ErrShortWrite, got %v", err)
		}
		if errors.Is(err, errDecode) {
			t.Fatalf("a sink short write is not a decode failure: %v", err)
		}
	})

	t.Run("undecodable body is a decode failure", func(t *testing.T) {
		err := copyChunkBody(bytes.NewReader([]byte("CORRUPT-CHUNK-BYTES")), true, io.Discard)
		if !errors.Is(err, errDecode) {
			t.Fatalf("want errDecode, got %v", err)
		}
	})

	t.Run("truncated body keeps the reader error", func(t *testing.T) {
		body := io.MultiReader(bytes.NewReader(comp[:len(comp)/2]), &failReader{err: io.ErrUnexpectedEOF})
		err := copyChunkBody(body, true, io.Discard)
		if err != io.ErrUnexpectedEOF {
			t.Fatalf("want the transport error verbatim, got %v (%T)", err, err)
		}
	})

	t.Run("cancellation keeps the reader error", func(t *testing.T) {
		err := copyChunkBody(&failReader{err: context.Canceled}, false, io.Discard)
		if err != context.Canceled {
			t.Fatalf("want context.Canceled verbatim, got %v (%T)", err, err)
		}
	})
}

func TestRetryDownload_CancelBeatsFilesystemErr(t *testing.T) {
	fastRetry(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	err := retryDownload(ctx, "c.bin", func() error {
		calls++
		// The cancellation is what provoked the local fault; the user's intent
		// must outrank the "filesystem errors are terminal" branch.
		cancel()
		return &fs.PathError{Op: "write", Path: "c.bin.tmp", Err: syscall.Errno(112)}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if downloader.IsFilesystemErr(err) {
		t.Fatalf("the PathError must not be what surfaces: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDownloadPatchBlob_RetriesThenSucceeds(t *testing.T) {
	fastRetry(t)
	blob := []byte("patch blob that only arrives on the second attempt")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex(blob), PatchSize: int64(len(blob))}
	if err := DownloadPatchBlob(context.Background(), srv.Client(), p, out); err != nil {
		t.Fatalf("patch blobs retry now, so this should succeed; got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, blob) {
		t.Fatalf("blob content mismatch: %q", got)
	}
}

func TestDownloadPatchBlob_StallExhausted(t *testing.T) {
	fastRetry(t)
	var hits int32
	srv := newStallServer(t, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		atomic.AddInt32(&hits, 1)
		hangForever(w, release)
	})

	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex([]byte("never arrives"))}
	err := DownloadPatchBlob(context.Background(), srv.Client(), p, out)
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("want ErrDownload, got %v", err)
	}
	if !errors.Is(err, downloader.ErrStalled) {
		t.Fatalf("want downloader.ErrStalled in the chain, got %v", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrChunkVerify) {
		t.Fatalf("misclassified stall: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("hits = %d, want 4 transfer attempts", got)
	}
	assertNoLeftovers(t, out)
}

func TestDownloadPatchBlob_MD5MismatchExhaustedErrChunkVerify(t *testing.T) {
	fastRetry(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte("wrong blob"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "p.bin")
	p := PatchInstr{PatchName: "patch.bin", URLPrefix: srv.URL, PatchMD5: md5hex([]byte("expected blob"))}
	err := DownloadPatchBlob(context.Background(), srv.Client(), p, out)
	if !errors.Is(err, ErrChunkVerify) {
		t.Fatalf("an exhausted MD5 mismatch is a content fault; want ErrChunkVerify, got %v", err)
	}
	if errors.Is(err, ErrDownload) {
		t.Fatalf("must not be classified as transport: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("hits = %d, want 4 transfer attempts", got)
	}
	assertNoLeftovers(t, out)
}

func TestSafeAtomicRename_SameDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SafeAtomicRename(src, dst); err != nil {
		t.Fatalf("SafeAtomicRename: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src should be gone after rename")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "payload" {
		t.Fatalf("dst content mismatch")
	}
}
