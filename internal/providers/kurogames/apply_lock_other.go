//go:build !windows

package kurogames

import "errors"

func platformApplyLock() applyLock {
	return &stubApplyLock{}
}

type stubApplyLock struct {
	held bool
}

var stubLockSentinel = errors.New("applyLock stub: already held")

func (s *stubApplyLock) Acquire(gameDir string) error {
	if s.held {
		return stubLockSentinel
	}
	s.held = true
	return nil
}

func (s *stubApplyLock) Release() error {
	s.held = false
	return nil
}
