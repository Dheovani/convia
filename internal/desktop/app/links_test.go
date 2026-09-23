package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"convia/internal/desktop/installations"
)

/*
TestAClickedInvitationBecomesTheLinkConviaReads.

It is the same link somebody was sent, with the one word changed. The scheme
Convia is reached over is decided the way every other address in this
application is: HTTPS everywhere, plain HTTP only for this machine.
*/
func TestAClickedInvitationBecomesTheLinkConviaReads(t *testing.T) {
	for clicked, want := range map[string]string{
		"convia://convia.example/invitations/inv_X":      "https://convia.example/invitations/inv_X",
		"convia://convia.example:8443/invitations/inv_X": "https://convia.example:8443/invitations/inv_X",
		"convia://localhost:8080/invitations/inv_X":      "http://localhost:8080/invitations/inv_X",
		"convia://127.0.0.1:8080/invitations/inv_X":      "http://127.0.0.1:8080/invitations/inv_X",
		"CONVIA://convia.example/invitations/inv_X":      "https://convia.example/invitations/inv_X",
		"  convia://convia.example/invitations/inv_X  ":  "https://convia.example/invitations/inv_X",
	} {
		read, err := Invitation(clicked)
		if err != nil || read != want {
			t.Errorf("Invitation(%q) = %q, %v, want %q", clicked, read, err, want)
		}
	}
}

/*
TestSomethingElseHandedToTheSchemeIsRefused.

Anything on the machine can hand anything to a registered scheme, so what
arrives is a stranger's until it is read. A link carrying credentials or a
query is refused rather than tidied: the installation refuses those too, and
tidying one here would pass it on with this application's blessing.
*/
func TestSomethingElseHandedToTheSchemeIsRefused(t *testing.T) {
	for _, handed := range []string{
		"",
		"https://convia.example/invitations/inv_X",
		"convia://",
		"convia://convia.example",
		"convia://convia.example/",
		"convia://user:secret@convia.example/invitations/inv_X",
		"convia://convia.example/invitations/inv_X?next=somewhere",
		"convia://convia.example/invitations/inv_X#fragment",
		"file:///etc/passwd",
		"not a link at all",
	} {
		if read, err := Invitation(handed); !errors.Is(err, ErrNotAnInvitation) {
			t.Errorf("Invitation(%q) = %q, %v, want it refused", handed, read, err)
		}
	}
}

/*
TestAnInvitationWaitsForSomewhereToPutIt.

Clicking a link on a machine where Convia is closed starts the application with
it: it arrives before the window exists, before an installation is chosen, and
before anybody has signed in. It has to wait, and it has to be handed over
once — twice would reopen the same invitation the next time anybody looked.
*/
func TestAnInvitationWaitsForSomewhereToPutIt(t *testing.T) {
	made, told := listening(t, store(nil))

	if !made.Arrived([]string{"convia://convia.example/invitations/inv_X"}) {
		t.Fatal("Arrived() ignored an invitation")
	}
	if len(told.on(LinkTopic)) != 1 {
		t.Errorf("the window was told %d times", len(told.on(LinkTopic)))
	}

	if taken := made.PendingLink(); taken != "https://convia.example/invitations/inv_X" {
		t.Errorf("PendingLink() = %q", taken)
	}
	if again := made.PendingLink(); again != "" {
		t.Errorf("PendingLink() = %q the second time, and the invitation would open twice", again)
	}
}

/*
TestALaunchCarryingNothingIsNotAnArrival.

An application started from its icon, or a second one launched by somebody
double-clicking it, carries no invitation. Saying one arrived would flash an
empty panel at somebody in the middle of a conversation.
*/
func TestALaunchCarryingNothingIsNotAnArrival(t *testing.T) {
	made, told := listening(t, store(nil))

	if made.Arrived(nil) {
		t.Error("Arrived() found an invitation in nothing")
	}
	if made.Arrived([]string{"--some-flag", `C:\Users\somebody\a file.txt`}) {
		t.Error("Arrived() read an invitation out of arguments that carried none")
	}
	if len(told.on(LinkTopic)) != 0 {
		t.Error("the window was told about an invitation that did not arrive")
	}
	if taken := made.PendingLink(); taken != "" {
		t.Errorf("PendingLink() = %q", taken)
	}
}

/*
TestTheLinkItselfIsNotWrittenDown.

An invitation link is the whole of what an invitation is: whoever holds one can
use it, which is the same thing a session is. So it belongs where a session
belongs, which is nowhere anybody reads — not a terminal somebody screenshots,
not a log file somebody sends to be helped with. The installation it lives on
is worth saying, and is not a secret.
*/
func TestTheLinkItselfIsNotWrittenDown(t *testing.T) {
	var written bytes.Buffer

	made := New(slog.New(slog.NewTextHandler(&written, nil)), installations.In(t.TempDir()), store(nil), nil)
	made.Start(context.Background(), Window{})

	const invitation = "inv_7QK4XMZP2VJH6TBWNDR3YAFC5"
	if !made.Arrived([]string{"convia://elsewhere.example/invitations/" + invitation}) {
		t.Fatal("Arrived() ignored an invitation")
	}

	said := written.String()
	if strings.Contains(said, invitation) {
		t.Errorf("the invitation was written to the log: %s", said)
	}
	if !strings.Contains(said, "elsewhere.example") {
		t.Errorf("the log does not say where the invitation came from: %s", said)
	}
}
