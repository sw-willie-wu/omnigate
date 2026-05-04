package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"

	"launcher-collection-tmp/internal/app"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("main panic", "err", r, "stack", string(debug.Stack()))
			os.Exit(1)
		}
	}()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	a := app.New("", logger)

	// Wails AssetServer middleware: any /_asset/* request from WebView2 hits
	// the App's asset handler before falling through to the embedded fs.
	assetMux := http.NewServeMux()
	assetMux.Handle("/_asset/", app.AssetHandlerForApp(a))

	err := wails.Run(&options.App{
		Title:            "launcher-collection",
		Width:            1280, Height: 720,
		MinWidth:         1280, MinHeight: 720,
		MaxWidth:         1280, MaxHeight: 720,
		DisableResize:    true,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 8, G: 8, B: 14, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: assetMux,
		},
		OnStartup: a.Startup,
		Bind:      []interface{}{a},
	})
	if err != nil {
		slog.Error("wails run", "err", err)
	}
}

// silence unused-import checker for context (used transitively by App)
var _ = context.Background
