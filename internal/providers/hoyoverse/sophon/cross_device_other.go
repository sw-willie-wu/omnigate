//go:build !windows

package sophon

import (
	"errors"
	"syscall"
)

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EXDEV)
}
