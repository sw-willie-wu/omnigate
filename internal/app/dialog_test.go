package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDialogDefaultDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, in, want string
	}{
		{"existing dir", dir, dir},
		{"missing path", filepath.Join(dir, "nope"), ""},
		{"a file not a dir", file, ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dialogDefaultDir(tc.in); got != tc.want {
				t.Errorf("dialogDefaultDir(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBrowseForDirectory_NilCtx(t *testing.T) {
	a := &App{} // ctx is nil
	got, err := a.BrowseForDirectory("anything")
	if err != nil {
		t.Errorf("nil-ctx should return nil error, got %v", err)
	}
	if got != "" {
		t.Errorf("nil-ctx should return empty path, got %q", got)
	}
}
