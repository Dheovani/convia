package secrets

import (
	"errors"
	"runtime"
	"testing"
)

/*
TestASessionIsKeptPerInstallationAndComesBack is the whole of this package on
the system it supports: what was kept comes back, one installation's session
does not overwrite another's, and forgetting removes it.

It touches the real store, because a fake one would only prove the fake works.
Everything it writes is removed again, whatever the test does.
*/
func TestASessionIsKeptPerInstallationAndComesBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skipf("the session store has no implementation on %s", runtime.GOOS)
	}

	keeper := NewKeeper()
	const first = "https://first.convia.test"
	const second = "https://second.convia.test"
	forget := func() {
		_ = keeper.Forget(first)
		_ = keeper.Forget(second)
	}
	// Anything an interrupted run left behind is removed first, so this test
	// says the same thing whatever ran before it.
	forget()
	t.Cleanup(forget)

	if _, err := keeper.Read(first); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read() before anything was kept = %v, want %v", err, ErrNotFound)
	}

	if err := keeper.Keep(first, Session{Username: "ana", Token: "cvs_first"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}
	if err := keeper.Keep(second, Session{Username: "bruno", Token: "cvs_second"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}

	kept, err := keeper.Read(first)
	if err != nil || kept.Username != "ana" || kept.Token != "cvs_first" {
		t.Errorf("Read() = %+v, %v, want ana's session", kept, err)
	}
	if kept, err := keeper.Read(second); err != nil || kept.Token != "cvs_second" {
		t.Errorf("one installation's session overwrote another: %+v, %v", kept, err)
	}

	// Signing in again replaces what was kept rather than adding to it.
	if err := keeper.Keep(first, Session{Username: "ana", Token: "cvs_rotated"}); err != nil {
		t.Fatalf("Keep() again error = %v", err)
	}
	if kept, _ := keeper.Read(first); kept.Token != "cvs_rotated" {
		t.Errorf("Read() = %q, want the session kept last", kept.Token)
	}

	if err := keeper.Forget(first); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	if _, err := keeper.Read(first); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read() after forgetting = %v, want %v", err, ErrNotFound)
	}
	if err := keeper.Forget(first); err != nil {
		t.Errorf("forgetting what is not there error = %v, want it to succeed", err)
	}
	if kept, err := keeper.Read(second); err != nil || kept.Token != "cvs_second" {
		t.Errorf("forgetting one installation took another's session: %+v, %v", kept, err)
	}
}

/*
TestASystemWithNoKeychainSaysSo rather than keeping a bearer credential
somewhere nothing guards it.
*/
func TestASystemWithNoKeychainSaysSo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has a keychain")
	}

	keeper := NewKeeper()
	if err := keeper.Keep("https://convia.test", Session{Token: "cvs_x"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Keep() = %v, want %v", err, ErrUnsupported)
	}
	if _, err := keeper.Read("https://convia.test"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Read() = %v, want %v", err, ErrUnsupported)
	}
}

// TestOneInstallationIsOneTarget keeps the naming honest: two addresses never
// collide, and the name says what it is for.
func TestOneInstallationIsOneTarget(t *testing.T) {
	if target("https://a.convia.test") == target("https://b.convia.test") {
		t.Error("two installations share one target")
	}
	if got := target("https://convia.test"); got != "Convia:https://convia.test" {
		t.Errorf("target() = %q", got)
	}
}
