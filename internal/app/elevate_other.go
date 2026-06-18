//go:build !windows

package app

import "errors"

func isElevated() bool { return true }

func relaunchElevated(args []string) error {
	return errors.New("elevation not supported on this platform")
}
