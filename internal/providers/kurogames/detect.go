package kurogames

import (
	"context"
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

// DefaultRoot is the layer-3 fallback install root for this backend (the former
// settings default). Per-game resolution scans here when no override or
// launcher-config entry applies. Exported so settings migration can reference it.
const DefaultRoot = `C:\Program Files\Wuthering Waves`

// HasGamesSegment is true when this backend's install root contains a "games/"
// segment before each game folder (hoyoverse layout). kuro/hyper join directly.
const HasGamesSegment = false

// FolderNames maps each known game ID to its on-disk install folder name,
// used by settings migration to derive per-game paths from an old root.
func FolderNames() map[core.GameID]string {
	out := make(map[core.GameID]string, len(games))
	for _, g := range games {
		out[g.ID] = g.FolderName
	}
	return out
}

// DetectInstall scans the kurogames launcher install root and returns each
// known game whose folder + canonical .exe is present.
//
// kuroPath should be the launcher root (e.g. "C:\\Program Files\\Wuthering Waves").
// Missing path returns ([], nil) — not an error; user may not have the launcher
// installed.
func DetectInstall(ctx context.Context, kuroPath string) ([]core.InstalledGame, error) {
	info, err := os.Stat(kuroPath)
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
		gameDir := filepath.Join(kuroPath, g.FolderName)
		exePath := filepath.Join(gameDir, g.ExeName)
		// require BOTH the folder and the exe to exist
		if dirInfo, err := os.Stat(gameDir); err != nil || !dirInfo.IsDir() {
			continue
		}
		if _, err := os.Stat(exePath); err != nil {
			continue
		}
		out = append(out, core.InstalledGame{
			GameID:         g.ID,
			InstallPath:    gameDir,
			CurrentVersion: "", // populated by CheckVersion later
		})
	}
	return out, nil
}
