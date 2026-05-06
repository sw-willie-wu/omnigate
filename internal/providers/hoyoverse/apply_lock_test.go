package hoyoverse

import (
	"testing"
)

func TestApplyLock_AcquireRelease(t *testing.T) {
	versionDir := t.TempDir()
	lock := newApplyLock()
	if err := lock.Acquire(versionDir); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

func TestApplyLock_DoubleAcquireFails(t *testing.T) {
	versionDir := t.TempDir()
	lock1 := newApplyLock()
	if err := lock1.Acquire(versionDir); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lock1.Release()

	lock2 := newApplyLock()
	err := lock2.Acquire(versionDir)
	if err == nil {
		t.Error("expected error on double acquire against same versionDir")
	}
}

func TestApplyLock_ReleaseAndReacquire(t *testing.T) {
	versionDir := t.TempDir()
	lock1 := newApplyLock()
	if err := lock1.Acquire(versionDir); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock2 := newApplyLock()
	if err := lock2.Acquire(versionDir); err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := lock2.Release(); err != nil {
		t.Errorf("re-release: %v", err)
	}
}
