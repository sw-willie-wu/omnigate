package hypergryph

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
	"testing"
	"time"

	"omnigate/internal/core"
)

type instantClock struct{}

func (instantClock) Now() time.Time                         { return time.Now() }
func (instantClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (instantClock) Sleep(d time.Duration)                  {}

func md5hex(b []byte) string { h := md5.Sum(b); return hex.EncodeToString(h[:]) }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func asUpdateError(err error, target **core.UpdateError) bool { return errors.As(err, target) }

func newDownloaderFor(t *testing.T, srv *httptest.Server, plan *core.UpdatePlan) (*downloader, *progressStore) {
	t.Helper()
	root := t.TempDir()
	ps := newProgressStore(root, "hypergryph/endfield", plan.Version)
	if err := ps.Init(plan.ManifestETag); err != nil {
		t.Fatalf("progress init: %v", err)
	}
	d := &downloader{
		client:   srv.Client(),
		logger:   testLogger(),
		tempRoot: root,
		progress: ps,
		plan:     plan,
		clock:    instantClock{},
	}
	return d, ps
}

func TestDownload_SuccessAndMD5(t *testing.T) {
	payload := []byte("endfield-file-contents")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "data/a.bundle", Hash: md5hex(payload), Size: int64(len(payload)), URL: srv.URL + "/a"}},
		TotalBytes: int64(len(payload)),
	}
	d, ps := newDownloaderFor(t, srv, plan)
	if err := d.runDownload(context.Background()); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ps.dir(), "data/a.bundle"))
	if err != nil || string(got) != string(payload) {
		t.Errorf("downloaded file mismatch: %q err=%v", got, err)
	}
}

func TestDownload_CorruptAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("wrong-bytes"))
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "a.bin", Hash: md5hex([]byte("expected")), Size: int64(len("wrong-bytes")), URL: srv.URL + "/a"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	err := d.runDownload(context.Background())
	var ue *core.UpdateError
	if err == nil || !asUpdateError(err, &ue) || ue.Code != "corrupt" {
		t.Fatalf("expected corrupt, got %v", err)
	}
}

func TestDownload_404NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "a.bin", Hash: "x", Size: 5, URL: srv.URL + "/a"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	err := d.runDownload(context.Background())
	var ue *core.UpdateError
	if err == nil || !asUpdateError(err, &ue) || ue.Code != "manifest_not_found" {
		t.Fatalf("expected manifest_not_found, got %v", err)
	}
}

func TestDownload_CancelMidway(t *testing.T) {
	// Handler writes one byte, cancels the ctx, then blocks until the request
	// ctx is done — so the download is interrupted mid-stream and the next
	// processFile attempt-loop iteration returns ctx.Err().
	cancelCh := make(chan func(), 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{0})
		if f := <-cancelCh; f != nil {
			f()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	plan := &core.UpdatePlan{
		GameID: "hypergryph/endfield", Version: "1.2.6", ManifestETag: "1.2.6",
		Files: []core.FileTask{{Path: "big.bin", Hash: "x", Size: 1 << 20, URL: srv.URL + "/big"}},
	}
	d, _ := newDownloaderFor(t, srv, plan)
	ctx, cancel := context.WithCancel(context.Background())
	cancelCh <- cancel
	err := d.runDownload(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
