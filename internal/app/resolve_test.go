package app

import (
	"context"
	"testing"

	"omnigate/internal/core"
)

type fakeScanner struct {
	id      core.BackendID
	def     map[core.GameID]string
	located map[core.GameID]string // nil → not an InstallLocator
}

func (f *fakeScanner) ID() core.BackendID { return f.id }
func (f *fakeScanner) DefaultScan(context.Context) (map[core.GameID]string, error) {
	return f.def, nil
}

// fakeLocatorScanner adds InstallLocator.
type fakeLocatorScanner struct{ fakeScanner }

func (f *fakeLocatorScanner) LocateInstalls(context.Context) (map[core.GameID]string, error) {
	return f.located, nil
}

func TestResolveBackendLocked_Precedence(t *testing.T) {
	existing := t.TempDir()
	gid := core.GameID("x/a")
	gids := []core.GameID{gid}

	// 1. override wins, verbatim even if missing
	got := resolveBackendLocked(context.Background(),
		&fakeLocatorScanner{fakeScanner{id: "x", def: map[core.GameID]string{gid: existing}, located: map[core.GameID]string{gid: existing}}},
		gids, map[string]GameSettings{string(gid): {Path: `Z:\override`}})
	if got[gid].Path != `Z:\override` || got[gid].Source != core.SourceOverride {
		t.Errorf("override: %+v", got[gid])
	}

	// 2. no override: launcher (stat-valid) beats default
	got = resolveBackendLocked(context.Background(),
		&fakeLocatorScanner{fakeScanner{id: "x", def: map[core.GameID]string{gid: `Q:\nope`}, located: map[core.GameID]string{gid: existing}}},
		gids, map[string]GameSettings{})
	if got[gid].Path != existing || got[gid].Source != core.SourceLauncher {
		t.Errorf("launcher: %+v", got[gid])
	}

	// 3. launcher stale (missing) → default
	got = resolveBackendLocked(context.Background(),
		&fakeLocatorScanner{fakeScanner{id: "x", def: map[core.GameID]string{gid: existing}, located: map[core.GameID]string{gid: `Q:\nope`}}},
		gids, map[string]GameSettings{})
	if got[gid].Path != existing || got[gid].Source != core.SourceDefault {
		t.Errorf("default: %+v", got[gid])
	}

	// 4. nothing → unresolved (also: a scanner with NO locator)
	got = resolveBackendLocked(context.Background(),
		&fakeScanner{id: "x", def: map[core.GameID]string{}},
		gids, map[string]GameSettings{})
	if got[gid].Source != core.SourceUnresolved {
		t.Errorf("unresolved: %+v", got[gid])
	}
}
