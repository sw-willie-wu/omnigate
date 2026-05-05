//go:build windows

package kurogames

import "testing"

func TestApplyLock_AcquireSucceedsOnce(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()
}

func TestApplyLock_SecondAcquireFails(t *testing.T) {
	dir := t.TempDir()
	l1 := newApplyLock()
	if err := l1.Acquire(dir); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err == nil {
		t.Errorf("second Acquire should fail")
		_ = l2.Release()
	}
}

func TestApplyLock_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	l1 := newApplyLock()
	if err := l1.Acquire(dir); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l2 := newApplyLock()
	if err := l2.Acquire(dir); err != nil {
		t.Errorf("re-Acquire after Release: %v", err)
	}
	_ = l2.Release()
}
