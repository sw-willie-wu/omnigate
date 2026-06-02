//go:build windows

package hypergryph

import "testing"

func TestApplyLock_AcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Second exclusive acquire from a fresh lock on the same dir must fail.
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err == nil {
		t.Errorf("expected second acquire to fail")
		_ = l2.Release()
	}
	if err := l.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
