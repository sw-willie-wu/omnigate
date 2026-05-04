// Package iconext extracts the largest available icon from a Windows PE
// executable's resource section, encoded as PNG. The extracted bytes are
// cached in-memory across calls (keyed by exePath + mtime).
//
// The package is built only on Windows. On other platforms the public API
// returns ErrUnsupported.
package iconext

import "errors"

// ErrUnsupported is returned by Extract on non-Windows builds and when the
// underlying Win32 calls report no extractable icon.
var ErrUnsupported = errors.New("iconext: extraction unsupported on this platform")

// Extract returns the PNG-encoded bytes of the largest icon embedded in
// exePath. Implementations must cache by (exePath, mtime) so repeated calls
// for the same .exe are cheap; the cache is bounded (~32 MB hard cap, LRU).
//
// Errors: ErrUnsupported on non-Windows; os.PathError if exePath does not
// exist or is not readable; any error from the underlying icon-decoding
// path (NoIcon, BitmapDecodeError) is wrapped with %w.
//
// The function is safe for concurrent use.
//
// (Implementation lives in iconext_windows.go / iconext_other.go.)
func Extract(exePath string) ([]byte, error) {
	return extractImpl(exePath)
}
