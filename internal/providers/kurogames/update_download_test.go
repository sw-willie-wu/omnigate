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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"launcher-collection-tmp/internal/core"
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
