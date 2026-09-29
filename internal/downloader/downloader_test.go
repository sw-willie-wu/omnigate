package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hangHandler writes the response header, flushes it (so the client sees a
// complete response head and starts reading the body) and then delivers ZERO
// body bytes until the request context is done — the exact shape of a stalled
// CDN stream.
func hangHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter is not an http.Flusher")
			return
		}
		f.Flush()
		<-r.Context().Done()
	}
}

func TestFetch_StallReturnsErrStalled(t *testing.T) {
	srv := httptest.NewServer(hangHandler(t))
	defer srv.Close()

	start := time.Now()
	err := Fetch(context.Background(), srv.Client(), srv.URL, Options{Stall: 200 * time.Millisecond},
		func(body io.Reader) error {
			_, err := io.Copy(io.Discard, body)
			return err
		})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrStalled) {
		t.Fatalf("err = %v, want errors.Is(err, ErrStalled)", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, must NOT report as context.Canceled (stall is retryable)", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("err = %q leaks the URL", err.Error())
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want < 2s (watchdog must abort the read)", elapsed)
	}
}

func TestFetch_SlowButSteadySucceeds(t *testing.T) {
	const (
		chunks    = 40
		chunkSize = 1024
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter is not an http.Flusher")
			return
		}
		buf := make([]byte, chunkSize)
		for i := 0; i < chunks; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := w.Write(buf); err != nil {
				return
			}
			f.Flush()
		}
	}))
	defer srv.Close()

	var got int
	start := time.Now()
	err := Fetch(context.Background(), NewClient(), srv.URL,
		Options{Stall: 500 * time.Millisecond, OnBytes: func(n int) { got += n }},
		func(body io.Reader) error {
			_, err := io.Copy(io.Discard, body)
			return err
		})
	if err != nil {
		t.Fatalf("Fetch: %v (a slow-but-steady stream must not be aborted)", err)
	}
	if want := chunks * chunkSize; got != want {
		t.Errorf("OnBytes total = %d, want %d", got, want)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("elapsed = %v, want >= 1s (the stream should really have been slow)", elapsed)
	}

	// Control: an overall Client.Timeout shorter than the transfer kills it —
	// this is the v0.4.4 bug the stall watchdog replaces.
	err = Fetch(context.Background(), &http.Client{Timeout: 300 * time.Millisecond}, srv.URL,
		Options{Stall: 500 * time.Millisecond},
		func(body io.Reader) error {
			_, err := io.Copy(io.Discard, body)
			return err
		})
	if err == nil {
		t.Error("control with Client.Timeout=300ms: err = nil, want a timeout failure")
	}
}

// TestFetch_StallDuringSlowSinkIsErrStalled covers the OTHER stall shape: here
// ctxReader notices the cancelled request context before the transport does, so
// the raw error is context.Canceled and only classify's step-2 rewrite turns it
// into the retryable ErrStalled. Without that rewrite a stalled download would
// surface downstream as "the user cancelled".
func TestFetch_StallDuringSlowSinkIsErrStalled(t *testing.T) {
	const size = 8 * 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content-Length set and fully written: the body has no short read and no
		// early EOF, so the only thing that can end the stream is the watchdog.
		w.Header().Set("Content-Length", fmt.Sprint(size))
		_, _ = w.Write(make([]byte, size))
	}))
	defer srv.Close()

	err := Fetch(context.Background(), srv.Client(), srv.URL, Options{Stall: 100 * time.Millisecond},
		func(body io.Reader) error {
			if _, err := io.ReadFull(body, make([]byte, 1024)); err != nil {
				return err
			}
			// A sink (disk) too slow to feed the watchdog mid-stream.
			time.Sleep(300 * time.Millisecond)
			_, err := io.Copy(io.Discard, body)
			return err
		})

	if !errors.Is(err, ErrStalled) {
		t.Fatalf("err = %v (%T), want errors.Is(err, ErrStalled)", err, err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, a stall must not report as context.Canceled (it is retryable)", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("err = %q leaks the URL", err.Error())
	}
}

func TestFetch_ParentCancelIsTerminal(t *testing.T) {
	srv := httptest.NewServer(hangHandler(t))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()

	err := Fetch(ctx, srv.Client(), srv.URL, Options{Stall: 5 * time.Second},
		func(body io.Reader) error {
			_, err := io.Copy(io.Discard, body)
			return err
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
	if err != ctx.Err() {
		t.Errorf("err = %v (%T), want the identity ctx.Err() = %v per the doc contract", err, err, ctx.Err())
	}
	if errors.Is(err, ErrStalled) {
		t.Errorf("err = %v, parent cancellation must be terminal, not ErrStalled", err)
	}
}

func TestFetch_StallDoesNotHijackConsumeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	want := &fs.PathError{Op: "write", Path: "x", Err: syscall.Errno(112)} // ERROR_DISK_FULL
	err := Fetch(context.Background(), srv.Client(), srv.URL, Options{Stall: 100 * time.Millisecond},
		func(body io.Reader) error {
			if _, err := io.Copy(io.Discard, body); err != nil {
				return err
			}
			// Post-stream work (Sync/verify/rename) outstays the stall window;
			// the watchdog must not rewrite this local-disk failure.
			time.Sleep(300 * time.Millisecond)
			return want
		})

	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v (%T), want *fs.PathError", err, err)
	}
	if pe != want {
		t.Errorf("err = %v, want the consume error identity preserved", err)
	}
	if errors.Is(err, ErrStalled) {
		t.Errorf("err = %v, consume errors must not be hijacked by the watchdog", err)
	}
	if !IsFilesystemErr(err) {
		t.Errorf("IsFilesystemErr(%v) = false, want true", err)
	}
}

func TestFetch_StatusAndAccept(t *testing.T) {
	drain := func(body io.Reader) error {
		_, err := io.Copy(io.Discard, body)
		return err
	}

	t.Run("500 is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := Fetch(context.Background(), srv.Client(), srv.URL, Options{}, drain)
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("err = %v (%T), want *StatusError", err, err)
		}
		if se.Code != http.StatusInternalServerError {
			t.Errorf("Code = %d, want 500", se.Code)
		}
		if se.URL != srv.URL {
			t.Errorf("URL = %q, want %q", se.URL, srv.URL)
		}
		if got, want := se.Error(), fmt.Sprintf("downloader: GET %s: http 500", srv.URL); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("206 accepted when asked for", func(t *testing.T) {
		var gotRange string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRange = r.Header.Get("Range")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("0123456789"))
		}))
		defer srv.Close()

		var n int64
		err := Fetch(context.Background(), srv.Client(), srv.URL, Options{
			AcceptStatus: []int{http.StatusPartialContent},
			Header:       http.Header{"Range": {"bytes=0-9"}},
		}, func(body io.Reader) error {
			var err error
			n, err = io.Copy(io.Discard, body)
			return err
		})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if n != 10 {
			t.Errorf("copied %d bytes, want 10", n)
		}
		if gotRange != "bytes=0-9" {
			t.Errorf("server saw Range = %q, want %q", gotRange, "bytes=0-9")
		}
	})

	t.Run("200 rejected when only 206 accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("whole file"))
		}))
		defer srv.Close()

		err := Fetch(context.Background(), srv.Client(), srv.URL,
			Options{AcceptStatus: []int{http.StatusPartialContent}}, drain)
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("err = %v (%T), want *StatusError", err, err)
		}
		if se.Code != http.StatusOK {
			t.Errorf("Code = %d, want 200", se.Code)
		}
	})
}

func TestFetch_NilClientUsesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	var got []byte
	err := Fetch(context.Background(), nil, srv.URL, Options{}, func(body io.Reader) error {
		var err error
		got, err = io.ReadAll(body)
		return err
	})
	if err != nil {
		t.Fatalf("Fetch with nil client: %v", err)
	}
	if string(got) != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

func TestNewClient_Bounds(t *testing.T) {
	c := NewClient()
	if c.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 (no overall timeout for GB-scale bodies)", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 15*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 15s", tr.TLSHandshakeTimeout)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
}

func TestIsFilesystemErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"PathError disk full", &fs.PathError{Op: "write", Path: "x", Err: syscall.Errno(112)}, true},
		{"LinkError", &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errors.New("boom")}, true},
		{"wrapped PathError", fmt.Errorf("w: %w", &fs.PathError{Op: "open", Path: "x", Err: errors.New("boom")}), true},
		{"url.Error", &url.Error{Op: "Get", URL: "http://x", Err: context.Canceled}, false},
		{"plain error", errors.New("x"), false},
		{"bare errno", syscall.Errno(112), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFilesystemErr(tt.err); got != tt.want {
				t.Errorf("IsFilesystemErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
