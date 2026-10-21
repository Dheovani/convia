package app

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

type fakeAutostart struct {
	on       bool
	launches string
	fail     error
}

func (f *fakeAutostart) Enabled() (bool, error) { return f.on, f.fail }
func (f *fakeAutostart) Enable(executable string) error {
	f.on, f.launches = true, executable
	return f.fail
}
func (f *fakeAutostart) Disable() error {
	f.on = false
	return f.fail
}

func TestTheInterfaceTurnsStartingWithWindowsOnAndOff(t *testing.T) {
	setting := &fakeAutostart{}
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)), setting, `C:\Convia\convia.exe`, nil, nil, nil, NewNetwork("", nil)).Bridge

	if on, _ := a.StartsWithWindows(); on {
		t.Fatal("on before anybody turned it on")
	}
	if err := a.SetStartsWithWindows(true); err != nil || !setting.on || setting.launches != `C:\Convia\convia.exe` {
		t.Fatalf("turning it on: %v, launching %q", err, setting.launches)
	}
	if err := a.SetStartsWithWindows(false); err != nil || setting.on {
		t.Fatalf("turning it off: %v", err)
	}

	setting.fail = errors.New("access denied")
	if err := a.SetStartsWithWindows(true); err == nil {
		t.Fatal("a setting that could not be written reported success")
	}
}
