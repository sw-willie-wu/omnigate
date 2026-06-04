package hoyoverse

import (
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

var _ core.LastPlayedProbe = (*Provider)(nil)

// localLowProduct maps each supported game to its Unity player-log folder under
// %USERPROFILE%\AppData\LocalLow. Publisher folders are NOT uniform: Genshin and
// ZZZ live under miHoYo, Star Rail under Cognosphere (global-version names).
var localLowProduct = map[core.GameID]string{
	"hoyoverse/genshin":  filepath.Join("miHoYo", "Genshin Impact"),
	"hoyoverse/starrail": filepath.Join("Cognosphere", "Star Rail"),
	"hoyoverse/zzz":      filepath.Join("miHoYo", "ZenlessZoneZero"),
}

// LastPlayedFiles implements core.LastPlayedProbe. The Unity player log
// (output_log.txt; Player.log on newer Unity) is rewritten on each launch, so
// its mtime reflects the last play — including launches outside omnigate. The
// log lives under LocalLow, independent of installDir (ignored here).
func (p *Provider) LastPlayedFiles(gid core.GameID, _ string) []string {
	sub, ok := localLowProduct[gid]
	if !ok {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "AppData", "LocalLow", sub)
	return []string{
		filepath.Join(dir, "output_log.txt"),
		filepath.Join(dir, "Player.log"),
	}
}
