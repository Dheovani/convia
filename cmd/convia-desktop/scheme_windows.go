//go:build windows

package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"

	"convia/internal/desktop/app"
)

/*
Registering the scheme that lets a clicked invitation open this application.

It is written under the current user rather than the machine, which is what
makes it need no administrator: a person who installed Convia for themselves
can open their own invitations without asking anybody. The installer `M35-011`
builds may write the machine-wide entry instead, and this will then find it
already correct and leave it alone.

**It is read before it is written.** A registry write on every start is a
change to somebody's machine on every start, and there is nothing to change
once it is right.
*/

// where the scheme lives, under HKEY_CURRENT_USER.
const schemeKey = `Software\Classes\` + app.Scheme

/*
registerScheme makes `convia://` open this executable, if it does not already.

A failure is not a reason to refuse to start. What it costs is that clicking an
invitation does not open Convia; pasting the link still works, which is how
every invitation was opened before this existed.
*/
func registerScheme(log *slog.Logger) {
	executable, err := os.Executable()
	if err != nil {
		log.Warn("find this executable, so invitations could open it", "error", err)
		return
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		log.Warn("find this executable, so invitations could open it", "error", err)
		return
	}

	/*
		`"%1"` is the link, quoted so that one containing a space arrives as
		one argument rather than several — and anything at all can be handed
		to a registered scheme, so that quoting is a boundary rather than a
		tidiness.

		The quotes are written out rather than taken from %q, which escapes for
		Go: it would put C:\\Users in the registry where Windows wants
		C:\Users.
	*/
	command := `"` + executable + `" "%1"`

	if current, err := schemeCommand(); err == nil && current == command {
		return
	}

	if err := writeScheme(command); err != nil {
		log.Warn("register the scheme that opens invitations", "error", err)
		return
	}
	log.Info("invitations clicked on this machine now open Convia", "scheme", app.Scheme+"://")
}

// schemeCommand is what is registered today, or an error when nothing is.
func schemeCommand() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, schemeKey+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer func() { _ = key.Close() }()

	command, _, err := key.GetStringValue("")
	return command, err
}

func writeScheme(command string) error {
	scheme, _, err := registry.CreateKey(registry.CURRENT_USER, schemeKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = scheme.Close() }()

	// The two values Windows reads to decide that this is a URL scheme at all.
	if err := scheme.SetStringValue("", "URL:Convia"); err != nil {
		return err
	}
	if err := scheme.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	open, _, err := registry.CreateKey(registry.CURRENT_USER, schemeKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = open.Close() }()

	return open.SetStringValue("", command)
}
