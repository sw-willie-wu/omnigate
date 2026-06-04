package hoyoverse

import (
	"context"

	"omnigate/internal/core"
)

// hoyoplayInstallReader returns the REAL install path HoYoPlay recorded for a
// given biz (e.g. "hk4e_global"). ok is false when no record exists. The live
// implementation reads the Windows registry; tests inject a fixture.
type hoyoplayInstallReader func(biz string) (string, bool)

// hoyoplayLocator maps HoYoPlay's per-biz GameInstallPath records onto our
// GameIDs. Splitting the registry read into an injectable reader keeps this
// logic testable on any OS and cross-compilable.
type hoyoplayLocator struct {
	read hoyoplayInstallReader
}

// LocateInstalls iterates the games table and, for each game with a non-empty
// Biz, asks the reader for HoYoPlay's recorded GameInstallPath. Best-effort:
// missing/empty records are simply omitted. Always returns a non-nil map.
func (l hoyoplayLocator) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	out := make(map[core.GameID]string, len(games))
	for _, g := range games {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if g.Biz == "" {
			continue
		}
		if path, ok := l.read(g.Biz); ok && path != "" {
			out[g.ID] = path
		}
	}
	return out, nil
}

// LocateInstalls makes *Provider satisfy core.InstallLocator. The App
// auto-discovers this capability and stat-validates every returned path.
func (p *Provider) LocateInstalls(ctx context.Context) (map[core.GameID]string, error) {
	return hoyoplayLocator{read: platformInstallReader}.LocateInstalls(ctx)
}
