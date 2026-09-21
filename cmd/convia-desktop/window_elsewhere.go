//go:build !windows

package main

import (
	"errors"
	"io/fs"
	"log/slog"
)

/*
open refuses on every system but Windows.

The first version of the application is for Windows, and the window is the part
that says so. This file exists so that the command still compiles, is vetted,
and is tested on Linux, where everything else in this repository is checked —
the alternative is a build constraint that excludes the whole command and an
error nobody sees until somebody builds on Windows.

macOS and Linux are not refused on principle. Convia's calls need a webview
that can share a screen, and WKWebView cannot; that is a decision with a date
on it, not a position.
*/
func open(*slog.Logger, fs.FS) error {
	return errors.New("this version of Convia's application is for Windows. The service, `convia`, runs anywhere")
}
