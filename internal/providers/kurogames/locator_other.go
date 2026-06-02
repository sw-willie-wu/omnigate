//go:build !windows

package kurogames

// platformUninstallReader is a no-op off Windows: the Kuro installer records its
// uninstall entry in the Windows registry, which doesn't exist on other
// platforms.
func platformUninstallReader() (string, bool) { return "", false }
