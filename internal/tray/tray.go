// Package tray is Convia's icon in the Windows notification area (ADR 0007).
package tray

import (
	"log/slog"
	"runtime"
	"sync"

	"fyne.io/systray"
)

// Actions are what the icon's menu does.
type Actions struct {
	Show                 func()
	Quit                 func()
	StartsWithWindows    func() (bool, error)
	SetStartsWithWindows func(bool) error
}

var (
	icons struct {
		sync.Mutex
		read, unread []byte
		showing      bool
		started      bool
	}
)

// Start shows the icon, and unread while something is (ADR 0033). It runs on a thread of its own, apart from the window's.
func Start(icon, unread []byte, actions Actions, logger *slog.Logger) {
	icons.Lock()
	icons.read, icons.unread = icon, unread
	icons.Unlock()
	go func() {
		runtime.LockOSThread()
		systray.Run(func() { build(icon, actions, logger) }, nil)
	}()
}

// Stop removes the icon.
func Stop() { systray.Quit() }

var menu struct {
	sync.Mutex
	show, autostart, quit *systray.MenuItem
	words                 [3]string
}

// SetWords names the menu's items in the interface's language: opening, starting with Windows, and quitting.
func SetWords(show, autostart, quit string) {
	menu.Lock()
	defer menu.Unlock()
	menu.words = [3]string{show, autostart, quit}
	if menu.show != nil {
		menu.show.SetTitle(show)
		menu.autostart.SetTitle(autostart)
		menu.quit.SetTitle(quit)
	}
}

// SetUnread shows whether anything is unread.
func SetUnread(unread bool) {
	icons.Lock()
	defer icons.Unlock()
	if !icons.started || unread == icons.showing {
		icons.showing = unread
		return
	}
	icons.showing = unread
	if unread {
		systray.SetIcon(icons.unread)
	} else {
		systray.SetIcon(icons.read)
	}
}

func build(icon []byte, actions Actions, logger *slog.Logger) {
	icons.Lock()
	icons.started = true
	if icons.showing {
		icon = icons.unread
	}
	icons.Unlock()
	systray.SetIcon(icon)
	systray.SetTooltip("Convia")
	systray.SetOnTapped(actions.Show)

	menu.Lock()
	words := menu.words
	if words[0] == "" {
		words = [3]string{"Open Convia", "Start with Windows", "Quit Convia"}
	}
	show := systray.AddMenuItem(words[0], "")
	systray.AddSeparator()
	on, err := actions.StartsWithWindows()
	if err != nil {
		logger.Warn("read whether Convia starts with Windows", "error", err)
	}
	autostart := systray.AddMenuItemCheckbox(words[1], "", on)
	systray.AddSeparator()
	quit := systray.AddMenuItem(words[2], "")
	menu.show, menu.autostart, menu.quit = show, autostart, quit
	menu.Unlock()

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
