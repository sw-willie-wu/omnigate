package hoyoverse

// applyLock guards Stage F's per-version apply phase against concurrent
// runs (e.g. a second Omnigate instance, or stale state after crash).
//
// Windows: file lock on `<versionDir>/apply.lock` via LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY.
// Non-Windows: stub for tests on CI Linux runners (returns nil error;
// real Genshin updates only run on Windows production hosts).
//
// Mirrors internal/providers/kurogames/apply_lock.go.
type applyLock interface {
	Acquire(versionDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
