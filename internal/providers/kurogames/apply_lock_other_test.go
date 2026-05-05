//go:build !windows

package kurogames

import (
	"errors"
	"testing"
)

func TestApplyLockStub_AcquireOnceReleaseSucceeds(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	if err := l.Acquire(dir); err != nil {
		t.Fatalf("stub Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("stub Release: %v", err)
	}
}

func TestApplyLockStub_SecondAcquireSentinel(t *testing.T) {
	dir := t.TempDir()
	l := newApplyLock()
	_ = l.Acquire(dir)
	err := l.Acquire(dir)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	if !errors.Is(err, stubLockSentinel) {
		t.Errorf("err = %v, want stubLockSentinel", err)
	}
}
