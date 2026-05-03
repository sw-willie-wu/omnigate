package hoyoverse

import (
	"context"
	"os"
	"path/filepath"

	"launcher-collection-tmp/internal/core"
)

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
