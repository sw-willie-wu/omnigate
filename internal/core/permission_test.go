package core

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestIsPermissionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"os.ErrPermission", os.ErrPermission, true},
		{"wrapped os.ErrPermission", fmt.Errorf("rename: %w", os.ErrPermission), true},
		{"LinkError access denied", &os.LinkError{Op: "rename", Err: syscall.Errno(5)}, true},
		{"unrelated", fmt.Errorf("boom"), false},
	}
	for _, c := range cases {
		if got := IsPermissionError(c.err); got != c.want {
			t.Errorf("%s: IsPermissionError=%v want %v", c.name, got, c.want)
		}
	}
}
