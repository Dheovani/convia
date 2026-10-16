/*
Package app runs Convia's lifecycle and bridges the core to the interface.

Only Bridge is bound by Wails: the interface calls its methods as promises, and a returned
error rejects the promise with the error's message, which must be safe to show. Events from
the core to the interface are emitted through the Wails runtime.
*/
package app

import (
	"context"
	"log/slog"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/tray"
)

// Autostart is the setting that starts Convia with Windows.
type Autostart interface {
	Enabled() (bool, error)
	Enable(executable string) error
	Disable() error
}

// Bridge is what the interface may call.
type Bridge struct {
	ctx        context.Context
	autostart  Autostart
	executable string
	data       *Data
	network    *Network
}

// StorageProblem names what stopped Convia's data from opening, or is empty.
func (b *Bridge) StorageProblem() string { return b.data.Problem() }

// StartOver sets the unreadable data aside, keeping it, and starts with none.
func (b *Bridge) StartOver() error { return b.data.StartOver() }

// StartsWithWindows reports whether Convia starts when the person signs in.
func (b *Bridge) StartsWithWindows() (bool, error) { return b.autostart.Enabled() }

// SetStartsWithWindows turns starting with Windows on or off.
func (b *Bridge) SetStartsWithWindows(on bool) error {
	if on {
		return b.autostart.Enable(b.executable)
	}
	return b.autostart.Disable()
}

// App is the process's lifecycle, never exposed to the interface.
type App struct {
	ctx        context.Context
	logger     *slog.Logger
	trayIcon   []byte
	unreadIcon []byte
	data       *Data
	network    *Network
	Bridge     *Bridge
}

func New(logger *slog.Logger, autostart Autostart, executable string, trayIcon, unreadIcon []byte, data *Data, network *Network) *App {
	return &App{logger: logger, trayIcon: trayIcon, unreadIcon: unreadIcon, data: data, network: network,
		Bridge: &Bridge{autostart: autostart, executable: executable, data: data, network: network}}
}

// Startup is Wails's OnStartup.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.Bridge.ctx = ctx
	if err := runtime.InitializeNotifications(ctx); err != nil {
		a.logger.Warn("prepare notifications", "error", err)
	}
	runtime.OnNotificationResponse(ctx, func(result runtime.NotificationResult) {
		a.show()
		if contact, ok := result.Response.UserInfo["contact"].(string); ok {
			runtime.EventsEmit(ctx, OpenEvent, contact)
		}
	})
	a.network.onUnread = tray.SetUnread
	tray.Start(a.trayIcon, a.unreadIcon, tray.Actions{
		Show:                 a.show,
		Quit:                 func() { runtime.Quit(a.ctx) },
		StartsWithWindows:    a.Bridge.StartsWithWindows,
		SetStartsWithWindows: a.Bridge.SetStartsWithWindows,
	}, a.logger)

	if err := a.data.Err(); err != nil {
		a.logger.Error("open the data", "problem", a.data.Problem(), "error", err)
	}

	a.network.attach(ctx)
	if me, err := a.data.Me(); err == nil {
		a.network.Start(me, a.data.Store())
	}

	a.logger.Info("convia started")
}

// SecondInstance is Wails's OnSecondInstanceLaunch: the process that tried to start has already exited.
func (a *App) SecondInstance(options.SecondInstanceData) {
	if a.ctx != nil {
		a.show()
	}
}

// Shutdown is Wails's OnShutdown.
func (a *App) Shutdown(context.Context) {
	a.network.Stop()
	tray.Stop()
	if err := a.data.Close(); err != nil {
		a.logger.Error("close the data", "error", err)
	}
	a.logger.Info("convia stopped")
}

func (a *App) show() {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}
