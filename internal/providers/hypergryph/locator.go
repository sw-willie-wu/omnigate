package hypergryph

import (
	"context"
	"path/filepath"

	"omnigate/internal/core"
)

// gryphRootReader returns the GRYPHLINK launcher root the installer recorded in
// the Windows uninstall entry (e.g. C:\Program Files\GRYPHLINK). ok is false
// when no matching uninstall entry exists. The live implementation scans the
// Windows registry; tests inject a fixture.
//
// Unlike hoyoverse/kuro, GRYPHLINK records only the launcher root — not a
// per-game path — so the locator joins each game's FolderName under it.
type gryphRootReader func() (string, bool)

// gryphLocator maps the GRYPHLINK launcher root onto our GameID(s) by joining
// each game's FolderName (the provider's own single source of truth) under the
// root. Splitting the registry scan into an injectable reader keeps the join +
// mapping testable on any OS and cross-compilable.
type gryphLocator struct {
	root gryphRootReader
}

// LocateInstalls derives each known game's install folder by joining its
// FolderName under the GRYPHLINK launcher root. Best-effort: a missing root
// yields an empty (non-nil) map.
func (l gryphLocator) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	out := map[core.GameID]string{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	root, ok := l.root()
	if !ok || root == "" {
		return out, nil
	}
	for gid, folder := range FolderNames() {
		out[gid] = filepath.Join(root, folder)
	}
	return out, nil
}

// LocateInstalls makes *Provider satisfy core.InstallLocator. The App
// auto-discovers this capability and stat-validates every returned path.
func (p *Provider) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	return gryphLocator{root: platformGryphRoot}.LocateInstalls(ctx)
}
