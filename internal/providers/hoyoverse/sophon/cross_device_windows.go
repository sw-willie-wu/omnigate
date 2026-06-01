//go:build windows

package sophon

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
