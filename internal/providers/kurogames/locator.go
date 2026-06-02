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

// LocateInstalls derives each game's install folder from the recorded uninstall
// string. The uninstall executable sits in the LAUNCHER root (e.g.
// C:\…\Wuthering Waves\uninst.exe); the game itself lives in a subfolder
// (FolderName, e.g. "Wuthering Waves Game") that holds launcherDownloadConfig.json
// — so we join FolderName onto the launcher root, NOT return the root directly.
// Best-effort: a missing record yields an empty (non-nil) map.
func (l kuroLocator) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	out := make(map[core.GameID]string, 1)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s, ok := l.read(); ok && s != "" {
		root := filepath.Dir(s)
		for gid, folder := range FolderNames() {
			out[gid] = filepath.Join(root, folder)
		}
	}
	return out, nil
}

// LocateInstalls makes *Provider satisfy core.InstallLocator. The App
// auto-discovers this capability and stat-validates every returned path.
func (p *Provider) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	return kuroLocator{read: platformUninstallReader}.LocateInstalls(ctx)
}
