package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"omnigate/internal/core"
)

func md5hexBytes(b []byte) string {
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}

// chunkedBody builds a 250-byte body split into 3 chunks:
// c0 [0,99], c1 [100,199], c2 [200,249] — end inclusive, tiles exactly.
func chunkedBody() ([]byte, []core.Chunk) {
	body := make([]byte, 250)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	bounds := [][2]int64{{0, 99}, {100, 199}, {200, 249}}
	chunks := make([]core.Chunk, len(bounds))
	for i, b := range bounds {
		chunks[i] = core.Chunk{Start: b[0], End: b[1], Hash: md5hexBytes(body[b[0] : b[1]+1])}
	}
	return body, chunks
}

func chunkedFileTask(body []byte, chunks []core.Chunk, url string) core.FileTask {
	return core.FileTask{
		Path: "big.pak", Hash: md5hexBytes(body), Size: int64(len(body)),
		URL: url, Chunks: chunks,
	}
}

func planOf(ft core.FileTask) *core.UpdatePlan {
	return &core.UpdatePlan{
		TotalBytes: ft.Size,
		Files:      []core.FileTask{ft},
	}
}

// rangeRecorder is an httptest handler that serves Range requests over body,
// records every Range header, and can fail specific ranges N times.
type rangeRecorder struct {
	body []byte

	mu       sync.Mutex
	ranges   []string       // every received Range header, in order
	failLeft map[string]int // Range header -> remaining times to drop the conn
	ignore   bool           // ignore Range: always respond 200 + full body
}

func (rr *rangeRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rh := r.Header.Get("Range")
		rr.mu.Lock()
		rr.ranges = append(rr.ranges, rh)
		fail := rr.failLeft[rh] > 0
		if fail {
			rr.failLeft[rh]--
		}
		ignore := rr.ignore
		rr.mu.Unlock()

		if fail {
			// Send SOME bytes before dropping: a zero-byte drop on a reused
			// connection triggers Go transport's transparent GET retry, which
			// would silently consume failLeft without failing the attempt.
			var start, end int64
			if _, err := fmt.Sscanf(rh, "bytes=%d-%d", &start, &end); err == nil {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(rr.body)))
				w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(rr.body[start : start+(end-start+1)/2])
			} else {
				w.Header().Set("Content-Length", fmt.Sprint(len(rr.body)))
				w.Write(rr.body[:len(rr.body)/2])
			}
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // drop connection mid-body
		}
		if ignore || rh == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(rr.body)))
			w.WriteHeader(http.StatusOK)
			w.Write(rr.body)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(rh, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(rr.body)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(rr.body[start : end+1])
	}
}

func (rr *rangeRecorder) recorded() []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := make([]string, len(rr.ranges))
	copy(out, rr.ranges)
	return out
}

// newChunkedDownloader wires a downloader over a fresh progress store.
func newChunkedDownloader(t *testing.T, client *http.Client, plan *core.UpdatePlan) *downloader {
	t.Helper()
	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.5.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	return &downloader{
		client: client, logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
}

// TestChunked_HappyPathAndProgress: all chunks download via 206; the staged
// file matches the body byte-for-byte and bytesDone == size exactly.
func TestChunked_HappyPath(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{body: body}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)
	d := newChunkedDownloader(t, srv.Client(), plan)

	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(d.progress.dir(), "big.pak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("staged file differs from body")
	}
	if n := d.bytesDone.Load(); n != int64(len(body)) {
		t.Errorf("bytesDone = %d, want %d", n, len(body))
	}
	recs := rr.recorded()
	if len(recs) != 3 || recs[0] != "bytes=0-99" || recs[1] != "bytes=100-199" || recs[2] != "bytes=200-249" {
		t.Errorf("ranges = %v", recs)
	}
}

// TestChunked_ResumeAfterInterrupt (T7): call 1 fails persistently on chunk 2
// (exhausts per-chunk netRetries) → whole run fails, bytesDone refunded to 0,
// .part keeps chunks 0-1. Call 2 (fresh downloader, same store) must request
// ONLY chunk 2 and finish with bytesDone == size.
func TestChunked_ResumeAfterInterrupt(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{
		body:     body,
		failLeft: map[string]int{"bytes=200-249": netRetries + 1},
	}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.5.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	d1 := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d1.runDownload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("call 1: err = %v, want network", err)
	}
	if n := d1.bytesDone.Load(); n != 0 {
		t.Errorf("call 1: bytesDone = %d, want 0 (committed prefix refunded)", n)
	}
	if _, err := os.Stat(filepath.Join(ps.dir(), "big.pak.part")); err != nil {
		t.Fatalf(".part must survive a failed chunked run: %v", err)
	}

	before := len(rr.recorded())
	d2 := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d2.runDownload(context.Background()); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	call2 := rr.recorded()[before:]
	for _, r := range call2 {
		if r != "bytes=200-249" {
			t.Errorf("call 2 requested %q; resume must skip verified chunks 0-1", r)
		}
	}
	if len(call2) == 0 {
		t.Error("call 2 made no requests")
	}
	if n := d2.bytesDone.Load(); n != int64(len(body)) {
		t.Errorf("call 2: bytesDone = %d, want %d", n, len(body))
	}
	got, _ := os.ReadFile(filepath.Join(ps.dir(), "big.pak"))
	if string(got) != string(body) {
		t.Error("staged file differs from body")
	}
}

// TestChunked_ChunkRetryNoDoubleCount (T7b): chunk 1 fails once then succeeds
// within the SAME run. bytesDone must be exactly size — the failed attempt's
// partial bytes refunded in place, no prefix re-commit (spec BLK-2).
func TestChunked_ChunkRetryNoDoubleCount(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{
		body:     body,
		failLeft: map[string]int{"bytes=100-199": 1},
	}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)
	d := newChunkedDownloader(t, srv.Client(), plan)

	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	if n := d.bytesDone.Load(); n != int64(len(body)) {
		t.Errorf("bytesDone = %d, want %d (retried chunk must not double-count)", n, len(body))
	}
	var c1Requests int
	for _, r := range rr.recorded() {
		if r == "bytes=100-199" {
			c1Requests++
		}
	}
	if c1Requests != 2 {
		t.Errorf("chunk 1 requested %d times, want 2 (fail + retry)", c1Requests)
	}
}

// TestChunked_AbortRollsBackPrefix (T7c): .part pre-seeded with valid chunks
// 0-1; chunk 2 fails persistently. The resume-scan commits the 200-byte
// prefix, then the whole-call failure must refund it — bytesDone exactly 0.
func TestChunked_AbortRollsBackPrefix(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{
		body:     body,
		failLeft: map[string]int{"bytes=200-249": netRetries + 1},
	}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.5.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	// Pre-seed .part with valid chunks 0-1.
	partPath := filepath.Join(ps.dir(), "big.pak.part")
	if err := os.WriteFile(partPath, body[:200], 0o644); err != nil {
		t.Fatal(err)
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	err := d.runDownload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if n := d.bytesDone.Load(); n != 0 {
		t.Errorf("bytesDone = %d, want 0 (resume-scan prefix must be refunded on abort)", n)
	}
	// Only chunk 2 should ever have been requested (scan skipped 0-1).
	for _, r := range rr.recorded() {
		if r != "bytes=200-249" {
			t.Errorf("requested %q; pre-seeded chunks must not be re-fetched", r)
		}
	}
}

// TestChunked_ValidPrefixSkipped (T9): .part holds valid chunks 0-1; only
// chunk 2 is requested and the run succeeds with bytesDone == size.
func TestChunked_ValidPrefixSkipped(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{body: body}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.5.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(ps.dir(), "big.pak.part")
	if err := os.WriteFile(partPath, body[:200], 0o644); err != nil {
		t.Fatal(err)
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	recs := rr.recorded()
	if len(recs) != 1 || recs[0] != "bytes=200-249" {
		t.Errorf("ranges = %v, want exactly [bytes=200-249]", recs)
	}
	if n := d.bytesDone.Load(); n != int64(len(body)) {
		t.Errorf("bytesDone = %d, want %d (prefix counted once)", n, len(body))
	}
	got, _ := os.ReadFile(filepath.Join(ps.dir(), "big.pak"))
	if string(got) != string(body) {
		t.Error("staged file differs from body")
	}
}

// TestChunked_PartShorterThanChunk (T9b): .part is truncated mid-chunk-1
// (150 bytes: chunk 0 complete, chunk 1 half). Short read must count as
// mismatch — resume from chunk 1, no panic, full success.
func TestChunked_PartShorterThanChunk(t *testing.T) {
	body, bounds := chunkedBody()
	rr := &rangeRecorder{body: body}
	srv := httptest.NewServer(rr.handler())
	defer srv.Close()

	ft := chunkedFileTask(body, bounds, srv.URL)
	plan := planOf(ft)

	tmp := t.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.5.0")
	if err := ps.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(ps.dir(), "big.pak.part")
	if err := os.WriteFile(partPath, body[:150], 0o644); err != nil {
		t.Fatal(err)
	}
	d := &downloader{
		client: srv.Client(), logger: slogTest(t), progress: ps,
		plan: plan, clock: fakeRetryClock{},
	}
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("runDownload: %v", err)
	}
	recs := rr.recorded()
	if len(recs) != 2 || recs[0] != "bytes=100-199" || recs[1] != "bytes=200-249" {
		t.Errorf("ranges = %v, want [bytes=100-199 bytes=200-249]", recs)
	}
	if n := d.bytesDone.Load(); n != int64(len(body)) {
		t.Errorf("bytesDone = %d, want %d", n, len(body))
	}
	got, _ := os.ReadFile(filepath.Join(ps.dir(), "big.pak"))
	if string(got) != string(body) {
		t.Error("staged file differs from body")
	}
}
