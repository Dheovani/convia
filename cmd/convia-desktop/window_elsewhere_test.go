//go:build !windows

package main

import (
	"strings"
	"testing"
)

/*
TestTheApplicationRefusesOnSystemsItWasNotBuiltFor.

The command compiles everywhere so that everywhere checks it. What it must not
do is appear to work: a person on macOS or Linux is told this version is for
Windows, and told that the service is not.
*/
func TestTheApplicationRefusesOnSystemsItWasNotBuiltFor(t *testing.T) {
	err := open(quiet(), nil, nil)
	if err == nil {
		t.Fatal("open() succeeded on a system with no window to open")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Errorf("open() said %q, which does not say which system the application is for", err)
	}
}
