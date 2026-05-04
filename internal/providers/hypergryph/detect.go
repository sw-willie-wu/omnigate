package hypergryph

import (
	"context"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

// DetectInstall scans the GRYPHLINK launcher root and returns each known
// game whose folder + canonical .exe is present.
func DetectInstall(ctx context.Context, gryphPath string) ([]core.InstalledGame, error) {
	info, err := os.Stat(gryphPath)
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
		gameDir := filepath.Join(gryphPath, g.FolderName)
		exePath := filepath.Join(gameDir, g.ExeName)
		if dirInfo, err := os.Stat(gameDir); err != nil || !dirInfo.IsDir() {
			continue
		}
		if _, err := os.Stat(exePath); err != nil {
			continue
		}
		out = append(out, core.InstalledGame{
			GameID:         g.ID,
			InstallPath:    gameDir,
			CurrentVersion: "",
		})
	}
	return out, nil
}
