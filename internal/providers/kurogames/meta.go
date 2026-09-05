package kurogames

import "omnigate/internal/core"

const (
	BackendID core.BackendID = "kurogames"
	UserAgent                = "omnigate/0.4 (+https://github.com/willie/omnigate)"
)

// gameMeta holds compile-time per-game constants for kurogames.
type gameMeta struct {
	ID         core.GameID
	FolderName string // subfolder under launcher root, e.g. "Wuthering Waves Game"
	ExeName    string // launches via this exe
	Display    core.LocalizedString
}

var games = []gameMeta{
	{
		ID:         "kurogames/wutheringwaves",
		FolderName: "Wuthering Waves Game",
		ExeName:    "Wuthering Waves.exe",
		Display: core.LocalizedString{
			"zh-TW": "鳴潮",
			"zh-CN": "鸣潮",
			"en":    "Wuthering Waves",
		},
	},
}

// findByID returns the gameMeta for a GameID or nil if not registered.
func findByID(id core.GameID) *gameMeta {
	for i := range games {
		if games[i].ID == id {
			return &games[i]
		}
	}
	return nil
}
