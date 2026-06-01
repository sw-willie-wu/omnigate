package sophon

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
)

func xxhName(raw []byte) string {
	return fmt.Sprintf("%016x", xxhash.Sum64(raw))
}

func md5hex(raw []byte) string {
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])
}

// zstdCompress is reused from manifest_fetch_test.go in the same package

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
