package hypergryph

// applyLock guards apply phase against concurrent game launches.
type applyLock interface {
	Acquire(gameDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
