package kurogames

import (
	"path/filepath"

	"omnigate/internal/core"
)

var _ core.LastPlayedProbe = (*Provider)(nil)

// LastPlayedFiles implements core.LastPlayedProbe. Wuthering Waves (Unreal 4)
// writes its client log to <installDir>\Client\Saved\Logs\Client.log, truncated
// on each launch. installDir is a.resolved[gid].Path == the GAME folder
// (<launcherRoot>\Wuthering Waves Game), so Client\… joins directly. An empty
// installDir (unresolved) yields no signal.
func (p *Provider) LastPlayedFiles(gid core.GameID, installDir string) []string {
	if findByID(gid) == nil || installDir == "" {
		return nil
	}
	return []string{filepath.Join(installDir, "Client", "Saved", "Logs", "Client.log")}
}
