package app

import (
	"errors"
	"net/http"
	"testing"

	"convia/internal/desktop/client"
	"convia/internal/desktop/secrets"
)

/*
TestSigningInKeepsTheSessionAndTellsTheInterfaceOnlyWhoItIs.

Two things at once, and both are the point of the boundary: the session is kept
where the operating system keeps secrets, and what crosses to the interface is
the person without it.
*/
func TestSigningInKeepsTheSessionAndTellsTheInterfaceOnlyWhoItIs(t *testing.T) {
	installation := &serving{}
	address := installation.start(t)
	held := store(nil)
	made, _ := listening(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	person, err := made.SignIn("ana", "correct horse battery staple")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if person.Username != "ana" || person.AccountID != "acc_1" {
		t.Errorf("SignIn() = %+v", person)
	}

	kept, err := held.Read(address)
	if err != nil {
		t.Fatalf("nothing was kept for %s: %v", address, err)
	}
	if kept.Token != "cvs_new" || kept.Username != "ana" {
		t.Errorf("what was kept is %+v", kept)
	}
}

/*
TestChangingThePasswordKeepsTheRotatedSession.

Convia rotates the session in the same request, because a client left holding
the old one would be signed out by its own password change. Keeping the old one
would mean signing in again at the next start, for no reason anybody could see.
*/
func TestChangingThePasswordKeepsTheRotatedSession(t *testing.T) {
	installation := &serving{}
	address := installation.start(t)
	held := store(nil)
	made, _ := listening(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := made.SignIn("ana", "correct horse battery staple"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if err := made.ChangePassword("correct horse battery staple", "a new one entirely"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	kept, err := held.Read(address)
	if err != nil {
		t.Fatalf("nothing is kept after the password changed: %v", err)
	}
	if kept.Token != "cvs_rotated" {
		t.Errorf("what is kept is %q, want the rotated session", kept.Token)
	}

	// And the request that follows carries the new one rather than the old.
	if response := through(made, http.MethodGet, "/v1/me/rooms", ""); response.Code != http.StatusOK {
		t.Errorf("the next request was answered %d, so the rotated session was not held", response.Code)
	}
}

/*
TestSigningOutForgetsTheSessionWhateverHappened.

A session the installation has already ended is one this application must stop
presenting, and a request that never arrived leaves behind a session somebody
meant to end. Keeping it in either case would present it again at the next
start.
*/
func TestSigningOutForgetsTheSessionWhateverHappened(t *testing.T) {
	installation := &serving{}
	address := installation.start(t)
	held := store(nil)
	made, _ := listening(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := made.SignIn("ana", "correct horse battery staple"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if err := made.SignOut(); err != nil {
		t.Fatalf("SignOut() error = %v", err)
	}
	if held.holds(address) {
		t.Error("the session survived signing out")
	}

	// The installation is still remembered: signing out is not the same as
	// being done with the installation.
	remembered, err := made.Installations()
	if err != nil {
		t.Fatalf("Installations() error = %v", err)
	}
	if len(remembered) != 1 {
		t.Errorf("Installations() = %v after signing out", remembered)
	}
}

/*
TestDeletingTheAccountLeavesNothingBehind: every session it had is gone at the
installation, so one kept here would be a credential for an account that no
longer exists.
*/
func TestDeletingTheAccountLeavesNothingBehind(t *testing.T) {
	installation := &serving{}
	address := installation.start(t)
	held := store(nil)
	made, _ := listening(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := made.SignIn("ana", "correct horse battery staple"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if err := made.DeleteAccount("correct horse battery staple"); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}
	if held.holds(address) {
		t.Error("a session is kept for an account that no longer exists")
	}
	if made.watchingNow() {
		t.Error("the stream is still open for an account that no longer exists")
	}
}

/*
TestNothingIsAskedOfAnInstallationNobodyChose.

It is the interface calling in the wrong order rather than anything a person
did, and it has to say so rather than reach for a client that is not there.
*/
func TestNothingIsAskedOfAnInstallationNobodyChose(t *testing.T) {
	made, _ := listening(t, store(nil))

	if _, err := made.SignIn("ana", "correct horse battery staple"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("SignIn() error = %v, want %v", err, ErrNotConnected)
	}
	if err := made.SignOut(); !errors.Is(err, ErrNotConnected) {
		t.Errorf("SignOut() error = %v, want %v", err, ErrNotConnected)
	}
	if err := made.ChangePassword("one", "another"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("ChangePassword() error = %v, want %v", err, ErrNotConnected)
	}
}

/*
TestASignInThatWasRefusedLeavesNothingBehind.

A wrong password must not leave a session kept, a stream open, or an
application that believes somebody is signed in. Each of those would be a
different way of being wrong at the next start.
*/
func TestASignInThatWasRefusedLeavesNothingBehind(t *testing.T) {
	refusing := &serving{refuses: true}
	address := refusing.start(t)
	held := store(nil)
	made, _ := listening(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	person, err := made.SignIn("ana", "a wrong password")
	var refusal *client.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("SignIn() = %+v, %v, want it refused", person, err)
	}
	if refusal.Code != "unauthenticated" {
		t.Errorf("SignIn() was refused as %q", refusal.Code)
	}

	if _, reading := held.Read(address); !errors.Is(reading, secrets.ErrNotFound) {
		t.Error("a refused sign-in kept a session")
	}
	if made.watchingNow() {
		t.Error("a refused sign-in opened a stream")
	}
	if response := through(made, http.MethodGet, "/v1/me/rooms", ""); response.Code != http.StatusServiceUnavailable {
		t.Errorf("the interface was answered %d after a refused sign-in, want nothing to carry it with", response.Code)
	}
}
