package app

import (
	"context"
	"os"

	"omnigate/internal/core"
)

type resolvedEntry struct {
	Path   string
	Source core.InstallSource
}

// backendScanner is the narrow provider capability resolution needs.
type backendScanner interface {
	ID() core.BackendID
	DefaultScan(ctx context.Context) (map[core.GameID]string, error)
}

func statDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// resolveBackendLocked resolves every gid for one backend. It scans DefaultScan
// and (if the provider is a core.InstallLocator) LocateInstalls ONCE, then
// resolves each gid by precedence: override (verbatim) → launcher (stat-valid)
// → default (stat-valid) → unresolved. NO locking: `overrides` is read by the
// caller and passed in. Naming follows the "Locked" convention used elsewhere:
// the caller either holds the settings write lock or needs no lock.
func resolveBackendLocked(ctx context.Context, p backendScanner, gids []core.GameID, overrides map[string]GameSettings) map[core.GameID]resolvedEntry {
	defaultMap, _ := p.DefaultScan(ctx)
	var locMap map[core.GameID]string
	if loc, ok := p.(core.InstallLocator); ok {
		locMap, _ = loc.LocateInstalls(ctx)
	}
	out := make(map[core.GameID]resolvedEntry, len(gids))
	for _, gid := range gids {
		if ov := overrides[string(gid)].Path; ov != "" {
			out[gid] = resolvedEntry{Path: ov, Source: core.SourceOverride}
			continue
		}
		if dir := locMap[gid]; statDir(dir) {
			out[gid] = resolvedEntry{Path: dir, Source: core.SourceLauncher}
			continue
		}
		if dir := defaultMap[gid]; statDir(dir) {
			out[gid] = resolvedEntry{Path: dir, Source: core.SourceDefault}
			continue
		}
		out[gid] = resolvedEntry{Source: core.SourceUnresolved}
	}
	return out
}
