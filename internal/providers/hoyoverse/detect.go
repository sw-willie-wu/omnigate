package hoyoverse

import (
	"context"
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

// DefaultRoot is the layer-3 fallback install root for this backend (the former
// settings default). Per-game resolution scans here when no override or
// launcher-config entry applies. Exported so settings migration can reference it.
const DefaultRoot = `C:\Program Files\HoYoPlay`

// HasGamesSegment is true when this backend's install root contains a "games/"
// segment before each game folder (hoyoverse layout). kuro/hyper join directly.
const HasGamesSegment = true

// FolderNames maps each known game ID to its on-disk install folder name,
// used by settings migration to derive per-game paths from an old root.
func FolderNames() map[core.GameID]string {
	out := make(map[core.GameID]string, len(games))
	for _, g := range games {
		out[g.ID] = g.FolderName
	}
	return out
}

// DetectInstall scans a HoYoPlay install folder (default: C:\Program Files\HoYoPlay)
// and returns each known game whose folder is present.
//
// hoyoplayPath should point at the directory that contains config.ini and games/.
// Missing path returns ([], nil) — not an error; user may not have HoYoPlay installed.
func DetectInstall(ctx context.Context, hoyoplayPath string) ([]core.InstalledGame, error) {
	info, err := os.Stat(hoyoplayPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	out := []core.InstalledGame{}
	for _, g := range games {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		gamePath := filepath.Join(hoyoplayPath, "games", g.FolderName)
		if st, err := os.Stat(gamePath); err == nil && st.IsDir() {
			out = append(out, core.InstalledGame{
				GameID:         g.ID,
				InstallPath:    gamePath,
				CurrentVersion: "", // populated by CheckVersion later
			})
		}
	}
	return out, nil
}
