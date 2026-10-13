// Package autostart turns starting Convia with Windows on and off. It is off unless the person turns it on (ADR 0016).
package autostart

import "errors"

// ErrUnsupported is returned where the system has no such setting.
var ErrUnsupported = errors.New("autostart: not supported on this system")

// Entry is one program started at sign-in, under name in the registry key at path.
type Entry struct {
	path string
	name string
}

// HiddenFlag starts Convia in the notification area, without showing its window.
const HiddenFlag = "--hidden"

// Default is Convia's entry in the current user's Run key.
var Default = Entry{path: `Software\Microsoft\Windows\CurrentVersion\Run`, name: "Convia"}
