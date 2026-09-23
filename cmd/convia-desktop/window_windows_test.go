//go:build windows

package main

import (
	"errors"
	"strings"
	"testing"
)

/*
TestAMachineWithoutTheWebviewRuntimeIsToldSo.

Without this the application opens nothing and exits, which on the machines
where it happens â€” older or administered by somebody else â€” looks like Convia
being broken. The message has to name the runtime, because that is the word
whoever can install it will search for.
*/
func TestAMachineWithoutTheWebviewRuntimeIsToldSo(t *testing.T) {
	if err := webviewProblem("", nil); err == nil {
		t.Error("a machine with no runtime was let through to a window that cannot open")
	}
	if err := webviewProblem("   ", nil); err == nil {
		t.Error("a blank version was read as a runtime")
	}

	looking := errors.New("the registry could not be read")
	err := webviewProblem("", looking)
	if !errors.Is(err, looking) {
		t.Errorf("webviewProblem() = %v, want it to carry why the lookup failed", err)
	}

	if err := webviewProblem("120.0.2210.91", nil); err != nil {
		t.Errorf("webviewProblem() = %v for an installed runtime", err)
	}
}

// TestWhatIsShownNamesTheRuntimeAndWhereItComesFrom: the message is the whole
// remedy, because there is no window to put anything else in.
func TestWhatIsShownNamesTheRuntimeAndWhereItComesFrom(t *testing.T) {
	for _, required := range []string{"WebView2", "developer.microsoft.com"} {
		if !strings.Contains(missingWebview, required) {
			t.Errorf("what is shown does not mention %q", required)
		}
	}
}

/*
TestABuildWithoutWailsTagsSaysHowToBuildIt.

Wails compiles a stub without them, and the stub opens no window. It says so
itself, in terms of a project built with its CLI; this says it in terms of this
repository, because the command somebody needs is the one that works here.
*/
func TestABuildWithoutWailsTagsSaysHowToBuildIt(t *testing.T) {
	for _, required := range []string{"-tags desktop,production", "./cmd/convia-desktop"} {
		if !strings.Contains(missingTags, required) {
			t.Errorf("what is shown does not contain %q, which is what somebody has to type", required)
		}
	}
}

/*
TestNobodyIsLookingAtAWindowThatIsMinimisedOrPutAway is the rule notifications
turn on, and the second case is the one that was missing: minimising Convia
left it convinced somebody was still reading, so nothing was ever said while
the window sat in the taskbar.
*/
func TestNobodyIsLookingAtAWindowThatIsMinimisedOrPutAway(t *testing.T) {
	for _, each := range []struct {
		window    string
		shown     bool
		minimised bool
		looking   bool
	}{
		{"on the screen", true, false, true},
		{"minimised", true, true, false},
		{"beside the clock", false, false, false},
		{"beside the clock, minimised before that", false, true, false},
	} {
		if got := looking(each.shown, each.minimised); got != each.looking {
			t.Errorf("looking at a window %s = %v, want %v", each.window, got, each.looking)
		}
	}
}
