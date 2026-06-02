package kurogames

import (
	"context"
	"path/filepath"

	"omnigate/internal/core"
)

// kuroUninstallReader returns the Windows uninstall string the Kuro installer
// recorded for Wuthering Waves (e.g. C:\…\Wuthering Waves\uninst.exe). ok is
// false when no matching uninstall entry exists. The live implementation scans
// the Windows registry; tests inject a fixture.
type kuroUninstallReader func() (string, bool)

// kuroLocator maps Kuro's uninstall record onto our GameID. The install folder
// is the directory containing the uninstall executable. Splitting the registry
// scan into an injectable reader keeps the dir-derivation + mapping testable on
// any OS and cross-compilable.
type kuroLocator struct {
	read kuroUninstallReader
}

// LocateInstalls derives the Wuthering Waves install folder from the recorded
// uninstall string (its parent directory). Best-effort: a missing record is
// simply omitted. Always returns a non-nil map.
func (l kuroLocator) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	out := make(map[core.GameID]string, 1)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s, ok := l.read(); ok && s != "" {
		out["kurogames/wutheringwaves"] = filepath.Dir(s)
	}
	return out, nil
}

// LocateInstalls makes *Provider satisfy core.InstallLocator. The App
// auto-discovers this capability and stat-validates every returned path.
func (p *Provider) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	return kuroLocator{read: platformUninstallReader}.LocateInstalls(ctx)
}
