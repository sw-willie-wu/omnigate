//go:build windows

package hoyoverse

import "golang.org/x/sys/windows/registry"

// platformInstallReader reads HoYoPlay's recorded install path for a biz from
//
//	HKEY_CURRENT_USER\SOFTWARE\Cognosphere\HYP\1_0\<biz>\GameInstallPath
//
// Returns ("", false) on any open/read error (e.g. game not installed).
func platformInstallReader(biz string) (string, bool) {
	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		`SOFTWARE\Cognosphere\HYP\1_0\`+biz,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return "", false
	}
	defer k.Close()

	path, _, err := k.GetStringValue("GameInstallPath")
	if err != nil {
		return "", false
	}
	return path, true
}
