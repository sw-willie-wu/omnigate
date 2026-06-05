package app

import (
	"os"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dialogDefaultDir returns current iff it is an existing directory, else "".
// Wails' OpenDirectoryDialog returns an error WITHOUT opening the dialog when
// DefaultDirectory is a non-existent path (pkg/runtime/dialog.go), and the panel's
// primary use case is fixing a wrong/missing path. Uses os.Lstat (not Stat) to
// bit-match Wails' internal fs.DirExists, which uses Lstat.
func dialogDefaultDir(current string) string {
	if current == "" {
		return ""
	}
	if fi, err := os.Lstat(current); err == nil && fi.IsDir() {
		return current
	}
	return ""
}

// BrowseForDirectory opens the native folder picker (seeded at current only when
// it exists) and returns the chosen absolute path, or "" if cancelled. The
// a.ctx == nil guard prevents Wails' getFrontend(nil) from calling log.Fatalf
// (process exit) in headless/test contexts; it is not a panic.
func (a *App) BrowseForDirectory(current string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "選擇資料夾",
		DefaultDirectory: dialogDefaultDir(current),
	})
}

// BrowseForImage opens the native file picker filtered to image files and
// returns the chosen absolute path, or "" if cancelled. nil-ctx guard mirrors
// BrowseForDirectory (avoids Wails getFrontend(nil) log.Fatalf in tests).
func (a *App) BrowseForImage(current string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "選擇背景圖片",
		DefaultDirectory: dialogDefaultDir(filepath.Dir(current)),
		Filters: []wruntime.FileFilter{
			{DisplayName: "Images (*.png;*.jpg;*.jpeg;*.webp;*.bmp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp;*.bmp"},
		},
	})
}
