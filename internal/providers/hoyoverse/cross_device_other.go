//go:build !windows

package hoyoverse

import (
	"errors"
	"syscall"
)

func init() {
	crossDeviceErrForTest = syscall.EXDEV
}

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EXDEV)
}
