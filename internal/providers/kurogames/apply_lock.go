package kurogames

// applyLock guards apply phase against concurrent game launches.
// Windows: file lock on `<gameDir>\.lc_update.lock` via LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY.
// Non-Windows: stub for tests on CI Linux runners.
type applyLock interface {
	Acquire(gameDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
