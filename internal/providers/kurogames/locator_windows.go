//go:build windows

package kurogames

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// uninstallRoots are the three registry locations where Windows records
// application uninstall entries. The Kuro installer (32-bit) typically lands in
// the WOW6432Node view, but we scan all three for robustness.
var uninstallRoots = []struct {
	key  registry.Key
	path string
}{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// platformUninstallReader scans the Windows uninstall roots for a subkey whose
// name starts with "KRInstall" and contains "Wuthering Waves", then returns its
// UninstallString (fallback: DisplayIcon). The directory containing that path is
// the install folder. Returns ("", false) on any error or no match.
func platformUninstallReader() (string, bool) {
	for _, root := range uninstallRoots {
		if s, ok := scanUninstallRoot(root.key, root.path); ok {
			return s, true
		}
	}
	return "", false
}

func scanUninstallRoot(root registry.Key, path string) (string, bool) {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return "", false
	}

	for _, name := range names {
		if !strings.HasPrefix(name, "KRInstall") || !strings.Contains(name, "Wuthering Waves") {
			continue
		}
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		s := readUninstallTarget(sub)
		sub.Close()
		if s != "" {
			return s, true
		}
	}
	return "", false
}

// readUninstallTarget returns the UninstallString, falling back to DisplayIcon.
func readUninstallTarget(k registry.Key) string {
	if v, _, err := k.GetStringValue("UninstallString"); err == nil && v != "" {
		return v
	}
	if v, _, err := k.GetStringValue("DisplayIcon"); err == nil && v != "" {
		return v
	}
	return ""
}
