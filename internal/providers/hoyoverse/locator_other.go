//go:build !windows

package hoyoverse

// platformInstallReader is a no-op off Windows: HoYoPlay records its install
// paths in the Windows registry, which doesn't exist on other platforms.
func platformInstallReader(string) (string, bool) { return "", false }
