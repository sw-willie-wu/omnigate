package hypergryph

import (
	"path/filepath"

	"omnigate/internal/core"
)

const (
	BackendID core.BackendID = "hypergryph"
	UserAgent                = "omnigate/0.4 (+https://github.com/willie/omnigate)"
)

type gameMeta struct {
	ID         core.GameID
	FolderName string // relative to launcher root, e.g. filepath.Join("games", "EndField Game")
	ExeName    string
	Display    core.LocalizedString
}

var games = []gameMeta{
	{
		ID:         "hypergryph/endfield",
		FolderName: filepath.Join("games", "EndField Game"),
		ExeName:    "Endfield.exe",
		Display: core.LocalizedString{
			"zh-TW": "明日方舟：終末地",
			"zh-CN": "明日方舟：终末地",
			"en":    "Arknights: Endfield",
		},
	},
}

func findByID(id core.GameID) *gameMeta {
	for i := range games {
		if games[i].ID == id {
			return &games[i]
		}
	}
	return nil
}
