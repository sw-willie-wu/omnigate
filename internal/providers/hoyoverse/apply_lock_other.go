//go:build !windows

package hoyoverse

func platformApplyLock() applyLock {
	return &otherApplyLock{}
}

type otherApplyLock struct{}

// Acquire is a no-op on non-Windows. Hoyoverse runs on Windows production
// hosts; this stub exists to keep `go build ./...` clean on Linux/macOS
// dev machines.
func (l *otherApplyLock) Acquire(versionDir string) error { return nil }
func (l *otherApplyLock) Release() error                  { return nil }
