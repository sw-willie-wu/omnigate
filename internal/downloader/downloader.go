// Package downloader is the shared HTTP fetch helper for game-file downloads.
//
// Its reason to exist: an overall http.Client.Timeout caps the WHOLE body read,
// so any transfer longer than the cap fails deterministically — a 30 s timeout
// killed multi-GB Sophon patch blobs, a 5-minute one killed 24 GiB kurogames
// paks. The right bound for a bulk download is not "how long may it take" but
// "how long may it deliver ZERO bytes": a stall watchdog. Connection setup stays
// bounded by the Transport (see NewClient).
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// ErrStalled is the watchdog cancel cause: the body delivered no bytes for the
// whole Stall window. Callers treat it as retryable.
var ErrStalled = errors.New("downloader: stream stalled")

// DefaultStallTimeout is how long a body may deliver zero bytes before the
// watchdog aborts it. Mirrors kurogames' defaultStallTimeout.
const DefaultStallTimeout = 60 * time.Second

// StatusError reports a response status that is not in Options.AcceptStatus.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("downloader: GET %s: http %d", e.URL, e.Code)
}

// NewClient returns the client bulk downloads should use: NO overall timeout,
// with connection setup bounded by the Transport instead.
func NewClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// Options tunes a single Fetch.
type Options struct {
	Stall        time.Duration // <= 0 → DefaultStallTimeout
	Header       http.Header   // optional request headers (e.g. Range)
	AcceptStatus []int         // nil → {200}; Range requests need {206}
	OnBytes      func(n int)   // optional per-Read callback (progress / accounting)
}

// Fetch GETs url and hands the body — wrapped in the stall watchdog and the
// request context — to consume. consume must only STREAM (read → write); do
// post-processing (Sync/verify/rename) after Fetch returns, outside the
// watchdog window.
//
// A nil hc uses http.DefaultClient.
//
// Errors:
//
//	errors.Is(err, ErrStalled)  watchdog fired (retryable) — never carries the URL
//	err == ctx.Err()            parent cancelled (terminal)
//	*StatusError                status not accepted
//	anything else               raw transport or consume error, identity preserved
func Fetch(ctx context.Context, hc *http.Client, url string, opts Options, consume func(body io.Reader) error) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	stall := opts.Stall
	if stall <= 0 {
		stall = DefaultStallTimeout
	}

	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	wd := time.AfterFunc(stall, func() { cancel(ErrStalled) })
	defer wd.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, vs := range opts.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return classify(ctx, reqCtx, url, stall, err)
	}
	defer resp.Body.Close()

	accept := opts.AcceptStatus
	if accept == nil {
		accept = []int{http.StatusOK}
	}
	ok := false
	for _, code := range accept {
		if resp.StatusCode == code {
			ok = true
			break
		}
	}
	if !ok {
		// Body closed by the deferred Close above.
		return &StatusError{URL: url, Code: resp.StatusCode}
	}

	body := &ctxReader{ctx: reqCtx, r: &stallReader{r: resp.Body, wd: wd, d: stall, onBytes: opts.OnBytes}}
	err = consume(body)
	// End the watchdog window the moment streaming is over, so post-stream work
	// the caller does after Fetch returns can never trip it. (What keeps a slow
	// consume's own error from being rewritten as a stall is the source gate in
	// classify, not this Stop — the watchdog may well have fired already.)
	wd.Stop()
	return classify(ctx, reqCtx, url, stall, err)
}

// classify maps a raw transport/consume error onto Fetch's error contract. The
// order is fixed.
func classify(ctx context.Context, reqCtx context.Context, url string, stall time.Duration, err error) error {
	if err == nil {
		return nil
	}
	// A stall surfaces in two observed shapes: ctxReader notices the cancelled
	// request context first → context.Canceled; the transport notices first →
	// the cancel cause object itself, ErrStalled. The pair of Is checks is the
	// SOURCE GATE: without it, a consume-side *fs.PathError that happened to
	// outlive the stall window would be hijacked and reported as retryable.
	if context.Cause(reqCtx) == ErrStalled &&
		(errors.Is(err, context.Canceled) || errors.Is(err, ErrStalled)) {
		// The URL belongs in the log, not in the error string.
		slog.Debug("downloader: stream stalled", "url", url, "stall", stall)
		return fmt.Errorf("%w", ErrStalled)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// stallReader feeds the watchdog: any read that delivered bytes proves the
// stream is alive and restarts the clock.
type stallReader struct {
	r       io.Reader
	wd      *time.Timer
	d       time.Duration
	onBytes func(n int)
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.wd.Reset(s.d)
		if s.onBytes != nil {
			s.onBytes(n)
		}
	}
	return n, err
}

// ctxReader returns ctx.Err() from Read once the context is cancelled, so
// cancellation propagates through wrapping readers such as zstd.NewReader.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// IsFilesystemErr reports whether err is a local filesystem fault. Every
// os.OpenFile / MkdirAll / Write / Sync / Rename failure — disk-full included —
// arrives wrapped in *fs.PathError or *os.LinkError, so no errno branch (and no
// per-OS build tag) is needed. This covers errors returned by os.* file
// operations only: other sink-side errors, such as a bare io.ErrShortWrite
// synthesised by io.MultiWriter / io.Copy, are NOT classified as filesystem
// errors by this function. Callers treat these as non-retryable local
// faults ("internal"), never "network".
func IsFilesystemErr(err error) bool {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return true
	}
	var le *os.LinkError
	return errors.As(err, &le)
}
