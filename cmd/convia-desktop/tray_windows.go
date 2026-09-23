//go:build windows

package main

import (
	_ "embed"
	"log/slog"
	"sync"

	"fyne.io/systray"
)

/*
The icon beside the clock, which is what keeps Convia reachable after somebody
closes the window.

A conversation is not over because a window is, and a call arriving while the
window is shut has to reach somebody. So closing puts Convia here, and leaving
for good is a thing somebody says rather than a thing that happens to them.

The same icon the executable carries, read from the file it was built from:
Windows wants the bytes, not the resource.
*/
//go:embed icon/convia.ico
var trayIcon []byte

/*
The words the menu starts with.

They are English, and they are replaced the moment the interface loads and
hands over the ones in whatever language somebody chose. A menu beside the
clock is drawn by Windows rather than by the interface, so it is the one place
Convia's catalogue cannot reach on its own.
*/
const (
	openLabel = "Open Convia"
	quitLabel = "Quit"
)

/*
tray is the icon, once it exists.

systray builds its menu on a thread of its own and tells us when it is ready,
so everything here is written there and read from wherever the interface
happens to call — which is why it is behind a lock rather than merely assigned.
*/
type tray struct {
	mutex sync.Mutex
	open  *systray.MenuItem
	quit  *systray.MenuItem
}

/*
start puts the icon beside the clock and answers what to do when it is used.

`onOpen` brings the window back and `onQuit` ends the application for good.
Both happen on systray's own goroutine, so both do their work through the
window rather than touching anything here.
*/
func (icon *tray) start(log *slog.Logger, onOpen, onQuit func()) {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle(windowTitle)
		systray.SetTooltip(windowTitle)

		open := systray.AddMenuItem(openLabel, "")
		systray.AddSeparator()
		quit := systray.AddMenuItem(quitLabel, "")

		icon.mutex.Lock()
		icon.open, icon.quit = open, quit
		icon.mutex.Unlock()

		log.Debug("convia is beside the clock")

		for {
			select {
			case <-open.ClickedCh:
				onOpen()
			case <-quit.ClickedCh:
				onQuit()
				return
			}
		}
	}, func() {
		log.Debug("convia is no longer beside the clock")
	})
}

/*
named gives the menu the interface's words.

It is called every time the interface starts and whenever somebody changes
language, so it may arrive before the menu exists — in which case there is
nothing to rename and the English above stands until the next time.
*/
func (icon *tray) named(open, quit string) {
	icon.mutex.Lock()
	defer icon.mutex.Unlock()

	if icon.open != nil && open != "" {
		icon.open.SetTitle(open)
	}
	if icon.quit != nil && quit != "" {
		icon.quit.SetTitle(quit)
	}
}

// stop takes the icon away, so that quitting does not leave one behind for
// somebody to click at a process that is gone.
func (icon *tray) stop() { systray.Quit() }
