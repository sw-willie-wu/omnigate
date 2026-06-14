package main

import (
	"context"
	"embed"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"

	"omnigate/internal/app"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// logFilePath returns "omnigate.log" next to the executable. A packaged GUI
// build has no console, so file logging is the only way to inspect runs.
// Falls back to a cwd-relative path if the executable path can't be resolved.
func logFilePath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "omnigate.log")
	}
	return "omnigate.log"
}

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("main panic", "err", r, "stack", string(debug.Stack()))
			os.Exit(1)
		}
	}()

	// Log to stderr (visible under `wails dev`) and to omnigate.log next to the
	// binary (the only sink a packaged GUI build leaves behind). Capped at 5 MB
	// with a single .1 backup so the log can't grow without bound.
	var sink io.Writer = os.Stderr
	if lf, err := app.OpenRotatingFile(logFilePath(), 5<<20); err == nil {
		sink = io.MultiWriter(os.Stderr, lf)
		defer lf.Close()
	}

	logger := slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	a := app.New("", logger)

	// Wails AssetServer middleware: any /_asset/* request from WebView2 hits
	// the App's asset handler before falling through to the embedded fs.
	assetMux := http.NewServeMux()
	assetMux.Handle("/_asset/", app.AssetHandlerForApp(a))

	err := wails.Run(&options.App{
		Title: "Omnigate",
		Width: 1280, Height: 720,
		MinWidth: 1280, MinHeight: 720,
		MaxWidth: 1280, MaxHeight: 720,
		DisableResize:    true,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 8, G: 8, B: 14, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: assetMux,
		},
		OnStartup:  a.Startup,
		OnShutdown: func(context.Context) { a.Close() },
		Bind:       []interface{}{a},
	})
	if err != nil {
		slog.Error("wails run", "err", err)
	}
}

// silence unused-import checker for context (used transitively by App)
var _ = context.Background
