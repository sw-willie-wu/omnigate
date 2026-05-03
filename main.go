package main

import (
	"embed"

	"launcher-collection-tmp/internal/app"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.New("")
	err := wails.Run(&options.App{
		Title:            "launcher-collection",
		Width:            1280, Height: 720,
		MinWidth:         1280, MinHeight: 720,
		MaxWidth:         1280, MaxHeight: 720,
		DisableResize:    true,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 8, G: 8, B: 14, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        a.Startup,
		Bind:             []interface{}{a},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
