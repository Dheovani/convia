package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

/*
TestABuildWithoutTheInterfaceSaysSoRatherThanOpeningNothing.

A window with no interface in it is an application that starts and shows a
blank rectangle, which is the hardest kind of failure to diagnose: nothing is
wrong with the machine, the runtime or the installation. The build is what is
wrong, and the message names the command that fixes it.
*/
func TestABuildWithoutTheInterfaceSaysSoRatherThanOpeningNothing(t *testing.T) {
	err := run(quiet(), nil, false)
	if err == nil {
		t.Fatal("run() opened a window with no interface in it")
	}
	if !strings.Contains(err.Error(), "web/") {
		t.Errorf("run() said %q, which does not say what to build", err)
	}
}
