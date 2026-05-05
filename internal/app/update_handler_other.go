//go:build !windows

package app

import "os"

func osTempDir() string                               { return os.TempDir() }
func osRemoveAll(path string) error                   { return os.RemoveAll(path) }
func osReadDir(path string) ([]os.DirEntry, error)    { return os.ReadDir(path) }
func platformHasFreeSpace(dir string, need int64) bool { return true } // stub
// On non-Windows the FS check is unavailable; return ("", false) so caller
// skips the unsupported-filesystem branch (CI Linux runners need this).
func platformFilesystemName(dir string) (string, bool) { return "", false }
