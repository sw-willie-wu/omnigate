package app

import (
	"os"
	"path/filepath"
	"strings"
)

// underTempDir reports whether dir is inside the OS temp dir (the `wails dev` /
// `go run` signature) — used to avoid writing data into a throwaway build dir.
func underTempDir(dir string) bool {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tmp, d)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// ResolveDataDir picks the directory for omnigate.db / log / .cache (spec §4):
// OMNIGATE_DATA_DIR override wins; else the executable's directory, unless that
// is under the OS temp dir (dev/go-run) in which case CWD is used so dev data
// stays in the repo root rather than a throwaway build dir.
func ResolveDataDir() string {
	if v := os.Getenv("OMNIGATE_DATA_DIR"); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if !underTempDir(dir) {
			return dir
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// DataDirWritable probes dir by creating and removing a temp file. A read-only
// dataDir (e.g. exe dropped in Program Files) drives the WebView2 cache to the
// Wails default so the GUI still renders (spec §4 / §8).
func DataDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".omnigate-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
