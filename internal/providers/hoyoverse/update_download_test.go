package hoyoverse

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"omnigate/internal/core"
)

// makeBlobServer returns an httptest server that serves `payload` as the
// blob body, supporting Range requests + ETag.
func makeBlobServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		if rng := r.Header.Get("Range"); rng != "" {
			s := strings.TrimPrefix(rng, "bytes=")
			parts := strings.SplitN(s, "-", 2)
			start, _ := strconv.ParseInt(parts[0], 10, 64)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)-int(start)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
}

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestDownload_FullDownload_Happy(t *testing.T) {
	payload := []byte("hello world this is a test blob")
	srv := makeBlobServer(t, payload)
	defer srv.Close()

	ps := newProgressStoreForTest(t)
	tasks := []core.FileTask{{
		URL:  srv.URL + "/blob.zip",
		Hash: md5hex(payload),
		Size: int64(len(payload)),
		Path: "blob.zip",
	}}
	if err := downloadAll(context.Background(), ps, tasks, 4, nil); err != nil {
		t.Fatalf("downloadAll: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.versionDir(), "blob.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch")
	}
	pf := ps.snapshot()
	if _, ok := pf.Entries["blob.zip"]; !ok {
		t.Error("entry not marked complete")
	}
}

func TestDownload_RangeResume(t *testing.T) {
	payload := []byte("0123456789ABCDEF0123456789ABCDEF") // 32 bytes
	srv := makeBlobServer(t, payload)
	defer srv.Close()

	ps := newProgressStoreForTest(t)
	partPath := filepath.Join(ps.versionDir(), "blob.zip.part")
	if err := os.MkdirAll(ps.versionDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath, payload[:16], 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := []core.FileTask{{
		URL:  srv.URL + "/blob.zip",
		Hash: md5hex(payload),
		Size: int64(len(payload)),
		Path: "blob.zip",
	}}
	if err := downloadAll(context.Background(), ps, tasks, 4, nil); err != nil {
		t.Fatalf("downloadAll: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.versionDir(), "blob.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("resume produced wrong content; got %q", string(got))
	}
}

func TestDownload_MD5Mismatch_Retries(t *testing.T) {
	payload := []byte("garbage payload doesn't match expected hash")
	srv := makeBlobServer(t, payload)
	defer srv.Close()

	ps := newProgressStoreForTest(t)
	tasks := []core.FileTask{{
		URL:  srv.URL + "/blob.zip",
		Hash: "deadbeefdeadbeefdeadbeefdeadbeef", // wrong
		Size: int64(len(payload)),
		Path: "blob.zip",
	}}
	err := downloadAll(context.Background(), ps, tasks, 4, nil)
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	var ue *core.UpdateError
	if !asUpdateError(err, &ue) {
		t.Fatalf("expected core.UpdateError; got %T %v", err, err)
	}
}

func TestDownload_AlreadyCompleteSkips(t *testing.T) {
	payload := []byte("already cached payload")
	srv := makeBlobServer(t, payload)
	defer srv.Close()

	ps := newProgressStoreForTest(t)
	if err := os.MkdirAll(ps.versionDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	finalPath := filepath.Join(ps.versionDir(), "blob.zip")
	if err := os.WriteFile(finalPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(finalPath)
	if err := ps.MarkComplete("blob.zip", int64(len(payload)), stat.ModTime(), md5hex(payload)); err != nil {
		t.Fatal(err)
	}
	tasks := []core.FileTask{{
		URL:  srv.URL + "/blob.zip",
		Hash: md5hex(payload),
		Size: int64(len(payload)),
		Path: "blob.zip",
	}}
	hits := newHitCountingTransport()
	hits.wrap(srv)
	if err := downloadAll(context.Background(), ps, tasks, 4, nil); err != nil {
		t.Fatalf("downloadAll: %v", err)
	}
	if hits.count() != 0 {
		t.Errorf("expected 0 HTTP hits when already complete, got %d", hits.count())
	}
}

func TestDownload_Parallel4Workers(t *testing.T) {
	payloads := make([][]byte, 4)
	servers := make([]*httptest.Server, 4)
	tasks := make([]core.FileTask, 4)
	for i := 0; i < 4; i++ {
		payloads[i] = []byte(fmt.Sprintf("blob-%d-payload-data-here", i))
		servers[i] = makeBlobServer(t, payloads[i])
		defer servers[i].Close()
		tasks[i] = core.FileTask{
			URL:  servers[i].URL + fmt.Sprintf("/blob-%d.zip", i),
			Hash: md5hex(payloads[i]),
			Size: int64(len(payloads[i])),
			Path: fmt.Sprintf("blob-%d.zip", i),
		}
	}
	ps := newProgressStoreForTest(t)
	if err := downloadAll(context.Background(), ps, tasks, 4, nil); err != nil {
		t.Fatalf("downloadAll: %v", err)
	}
	for i, task := range tasks {
		got, err := os.ReadFile(filepath.Join(ps.versionDir(), task.Path))
		if err != nil {
			t.Errorf("blob %d: %v", i, err)
			continue
		}
		if string(got) != string(payloads[i]) {
			t.Errorf("blob %d content mismatch", i)
		}
	}
}

// hitCountingTransport intercepts HTTP requests for hit counting.
type hitCountingTransport struct {
	hits int
}

func newHitCountingTransport() *hitCountingTransport { return &hitCountingTransport{} }
func (h *hitCountingTransport) wrap(srv *httptest.Server) {
	prev := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits++
		prev.ServeHTTP(w, r)
	})
}
func (h *hitCountingTransport) count() int { return h.hits }

// asUpdateError unwraps + type-asserts.
func asUpdateError(err error, target **core.UpdateError) bool {
	for e := err; e != nil; {
		if u, ok := e.(*core.UpdateError); ok {
			*target = u
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if uw, ok := e.(unwrapper); ok {
			e = uw.Unwrap()
			continue
		}
		break
	}
	return false
}

// Suppress unused-import warnings in case io is referenced only by the
// implementation's test helpers (kept for parity with plan).
var _ = io.Discard
