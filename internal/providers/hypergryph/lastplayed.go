package hypergryph

import (
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

var _ core.LastPlayedProbe = (*Provider)(nil)

// LastPlayedFiles implements core.LastPlayedProbe. Endfield (Unity) writes its
// player log under %USERPROFILE%\AppData\LocalLow\Gryphline\Endfield, rewritten
// each launch (Player-prev.log confirms rotation). Independent of installDir.
func (p *Provider) LastPlayedFiles(gid core.GameID, _ string) []string {
	if findByID(gid) == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "AppData", "LocalLow", "Gryphline", "Endfield")
	return []string{
		filepath.Join(dir, "Player.log"),
		filepath.Join(dir, "output_log.txt"),
	}
}
