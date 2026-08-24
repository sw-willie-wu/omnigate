package app

import (
	"fmt"
	"os"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"omnigate/internal/core"
)

// GachaImportOutcome is what the frontend needs after a file import: which uid
// partition received the pulls and how many were new vs already stored.
// A zero-value outcome (UID == "") means the user cancelled the file picker.
type GachaImportOutcome struct {
	UID   string `json:"uid"`
	Added int    `json:"added"`
	Total int    `json:"total"`
}

// ImportGachaRecords opens a JSON file picker and imports a third-party gacha
// export (currently: WuWa wuwatracker-pulls) into the export's own uid
// partition. The active-account summary is NOT recomputed here — the frontend
// reloads it so the board refreshes only when the imported uid is the visible one.
func (a *App) ImportGachaRecords(gameID string) (GachaImportOutcome, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return GachaImportOutcome{}, err
	}
	ip, ok := p.(core.GachaImportProvider)
	if !ok || a.gachaStore == nil {
		return GachaImportOutcome{}, fmt.Errorf("gacha import unsupported for %s", gameID)
	}
	if a.ctx == nil { // headless/test: no dialog host (mirrors BrowseForDirectory)
		return GachaImportOutcome{}, nil
	}
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "選擇 wuwatracker 匯出檔",
		Filters: []wruntime.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return GachaImportOutcome{}, err
	}
	if path == "" { // cancelled
		return GachaImportOutcome{}, nil
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 64<<20 {
		return GachaImportOutcome{}, fmt.Errorf("gacha import: file too large (%d bytes)", fi.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GachaImportOutcome{}, err
	}
	return a.importGachaData(gid, ip, data)
}

// importGachaData is the dialog-free core of ImportGachaRecords (testable).
func (a *App) importGachaData(gid core.GameID, ip core.GachaImportProvider, data []byte) (GachaImportOutcome, error) {
	game := string(gid)
	res, err := ip.ParseGachaImport(gid, data, func(uid string) map[string]bool {
		all, err := a.gachaStore.AllPulls(game, uid)
		if err != nil {
			// nil degrades offset inference to the +8 default — worth a trace.
			a.logger.Warn("gacha import: existing-pulls read failed", "gid", game, "err", err)
			return nil
		}
		m := make(map[string]bool, len(all))
		for _, p := range all {
			m[p.ID] = true
		}
		return m
	})
	if err != nil {
		a.logger.Warn("gacha import parse failed", "gid", game, "err", err)
		return GachaImportOutcome{}, err
	}
	added, err := a.gachaStore.UpsertPulls(game, res.UID, res.Pulls)
	if err != nil {
		return GachaImportOutcome{}, err
	}
	a.logger.Info("gacha import done", "gid", game, "uid", res.UID, "added", added, "total", len(res.Pulls))
	return GachaImportOutcome{UID: res.UID, Added: added, Total: len(res.Pulls)}, nil
}
