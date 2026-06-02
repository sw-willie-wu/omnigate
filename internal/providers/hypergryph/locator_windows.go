//go:build windows

package hypergryph

import (
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// uninstallRoots are the three registry locations where Windows records
// application uninstall entries. The GRYPHLINK installer (32-bit) typically
// lands in the WOW6432Node view, but we scan all three for robustness.
var uninstallRoots = []struct {
	key  registry.Key
	path string
}{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// platformGryphRoot scans the Windows uninstall roots for a subkey whose
// DisplayName is "GRYPHLINK" and returns its launcher root: InstallLocation if
// present, else the directory of DisplayIcon / UninstallString. Returns
// ("", false) on any error or no match.
func platformGryphRoot() (string, bool) {
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
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		dn, _, err := sub.GetStringValue("DisplayName")
		if err != nil || dn != "GRYPHLINK" {
			sub.Close()
			continue
		}
		r := readGryphRoot(sub)
		sub.Close()
		if r != "" {
			return r, true
		}
	}
	return "", false
}

// readGryphRoot returns the launcher root: InstallLocation if non-empty, else
// the parent directory of DisplayIcon, else of UninstallString.
func readGryphRoot(k registry.Key) string {
	if v, _, err := k.GetStringValue("InstallLocation"); err == nil && v != "" {
		return v
	}
	if v, _, err := k.GetStringValue("DisplayIcon"); err == nil && v != "" {
		return filepath.Dir(v)
	}
	if v, _, err := k.GetStringValue("UninstallString"); err == nil && v != "" {
		return filepath.Dir(v)
	}
	return ""
}
