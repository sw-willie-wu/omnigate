//go:build windows

package hoyoverse

import (
	"errors"

	"golang.org/x/sys/windows"
)

func init() {
	crossDeviceErrForTest = windows.ERROR_NOT_SAME_DEVICE
}

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
