/*
Command convia is the Convia desktop application: the window, and the local
core behind it, that run on the user's own machine. See README.md for what
that means, and docs/adr for the decisions behind how it is built.
*/
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// The interface, built by Vite into frontend/dist and compiled into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	err := wails.Run(&options.App{
		Title:     "Convia",
		Width:     1280,
		Height:    800,
		MinWidth:  940,
		MinHeight: 500,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
	})
	if err != nil {
		log.Fatalf("convia: %v", err)
	}
}
