//go:build windows

package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

/*
Opening with Windows, which is what makes the icon beside the clock worth
anything.

Without it, Convia reaches somebody only after they have opened it by hand —
which means every morning, and means a call that arrives before they do reaches
nothing. So it opens with the machine, and it opens **hidden**: no window, just
the icon, the session resumed and the stream open.

It is written under the current user, like the scheme that opens invitations
and for the same reason: a person who installed Convia for themselves does not
need an administrator to have it open for them.
*/

const (
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "Convia"

	// hidden is what this executable is started with by Windows, and the one
	// thing that makes opening with the machine bearable: no window appears.
	hidden = "--hidden"
)

/*
openWithWindows makes this executable open with the machine, if it does not
already.

It is read before it is written, like the scheme: there is nothing to change
once it is right, and a registry write on every start is a change to somebody's
machine on every start.

A failure is not a reason to refuse to start. What it costs is that Convia is
reachable only after somebody opens it.
*/
func openWithWindows(log *slog.Logger) {
	executable, err := os.Executable()
	if err != nil {
		log.Warn("find this executable, so Convia could open with Windows", "error", err)
		return
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		log.Warn("find this executable, so Convia could open with Windows", "error", err)
		return
	}

	command := `"` + executable + `" ` + hidden

	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		log.Warn("open what Windows starts with", "error", err)
		return
	}
	defer func() { _ = key.Close() }()

	if current, _, err := key.GetStringValue(runName); err == nil && current == command {
		return
	}

	if err := key.SetStringValue(runName, command); err != nil {
		log.Warn("make Convia open with Windows", "error", err)
		return
	}
	log.Info("convia now opens with Windows, beside the clock")
}

// openingHidden reports the start that Windows makes: no window, just the icon.
func openingHidden(arguments []string) bool {
	for _, argument := range arguments {
		if argument == hidden {
			return true
		}
	}
	return false
}
