// Command convia is the Convia desktop application: the window and the local core behind it.
package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/Dheovani/convia/internal/app"
	"github.com/Dheovani/convia/internal/autostart"
	"github.com/Dheovani/convia/internal/home"
	"github.com/Dheovani/convia/internal/logging"
	"github.com/Dheovani/convia/internal/protect"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	dir, err := home.Dir()
	if err != nil {
		log.Fatalf("convia: %v", err)
	}

	files, err := logging.Open(filepath.Join(dir, "logs"))
	if err != nil {
		log.Fatalf("convia: %v", err)
	}

	defer files.Close()
	logger := logging.New(files)

	executable, err := os.Executable()
	if err != nil {
		logger.Error("locate the executable", "error", err)
	}

	application := app.New(
		logger,
		autostart.Default,
		executable,
		trayIcon,
		app.OpenData(dir, protect.DPAPI{}),
		app.NewNetwork(os.Getenv(app.RendezvousOverride), logger),
	)

	err = wails.Run(&options.App{
		Title:     "Convia",
		Width:     1280,
		Height:    800,
		MinWidth:  940,
		MinHeight: 500,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// The theme's surface, so the window does not flash another colour before it is drawn.
		BackgroundColour:  &options.RGBA{R: 0x17, G: 0x17, B: 0x1f, A: 0xff},
		HideWindowOnClose: true,
		StartHidden:       slices.Contains(os.Args[1:], autostart.HiddenFlag),
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               home.InstanceID(dir),
			OnSecondInstanceLaunch: application.SecondInstance,
		},
		OnStartup:  application.Startup,
		OnShutdown: application.Shutdown,
		Bind:       []any{application.Bridge},
	})
	if err != nil {
		logger.Error("run the window", "error", err)
		log.Fatalf("convia: %v", err)
	}
}
