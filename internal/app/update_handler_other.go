//go:build !windows

package app

import "os"

var osTempDir = func() string { return os.TempDir() }
var osRemoveAll = func(path string) error { return os.RemoveAll(path) }
var osReadDir = func(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func platformHasFreeSpace(dir string, need int64) bool { return true } // stub
// On non-Windows the FS check is unavailable; return ("", false) so caller
// skips the unsupported-filesystem branch (CI Linux runners need this).
func platformFilesystemName(dir string) (string, bool) { return "", false }
