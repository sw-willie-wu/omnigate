package core

import (
	"errors"
	"os"
	"syscall"
)

// IsPermissionError reports whether err (anywhere in its wrap chain) is a
// filesystem permission error. On Windows os.Rename/os.OpenFile return errors
// that map to os.ErrPermission for ERROR_ACCESS_DENIED via syscall.Errno.Is;
// the explicit errno==5 check is belt-and-suspenders for any path that does not
// reach that mapping.
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == 5 { // ERROR_ACCESS_DENIED (Windows)
		return true
	}
	return false
}
