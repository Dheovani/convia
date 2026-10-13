// Package tray is Convia's icon in the Windows notification area (ADR 0007).
package tray

import (
	"log/slog"
	"runtime"

	"fyne.io/systray"
)

// Actions are what the icon's menu does.
type Actions struct {
	Show                 func()
	Quit                 func()
	StartsWithWindows    func() (bool, error)
	SetStartsWithWindows func(bool) error
}

// Start shows the icon. It runs on a thread of its own, apart from the window's.
func Start(icon []byte, actions Actions, logger *slog.Logger) {
	go func() {
		runtime.LockOSThread()
		systray.Run(func() { build(icon, actions, logger) }, nil)
	}()
}

// Stop removes the icon.
func Stop() { systray.Quit() }

func build(icon []byte, actions Actions, logger *slog.Logger) {
	systray.SetIcon(icon)
	systray.SetTooltip("Convia")
	systray.SetOnTapped(actions.Show)

	show := systray.AddMenuItem("Open Convia", "")
	systray.AddSeparator()
	on, err := actions.StartsWithWindows()
	if err != nil {
		logger.Warn("read whether Convia starts with Windows", "error", err)
	}
	autostart := systray.AddMenuItemCheckbox("Start with Windows", "", on)
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Convia", "")

	for {
		select {
		case <-show.ClickedCh:
			actions.Show()
		case <-autostart.ClickedCh:
			want := !autostart.Checked()
			if err := actions.SetStartsWithWindows(want); err != nil {
				logger.Error("change whether Convia starts with Windows", "error", err)
				continue
			}
			if want {
				autostart.Check()
			} else {
				autostart.Uncheck()
			}
		case <-quit.ClickedCh:
			actions.Quit()
			return
		}
	}
}
