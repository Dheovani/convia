package livekit

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"convia/internal/media"
)

/*
These variables point the tests below at a real LiveKit server.

They are skipped when unset, so `go test ./...` stays runnable without any
media infrastructure, and CI always sets them so the coverage is never optional
where it matters. docs/media.md explains how to start one locally.

The unit tests in this package assert what Convia sends; only these assert that
a real server accepts it. That distinction is the whole reason they exist:
Convia speaks the provider's HTTP API directly rather than through its SDK, so
nothing but a real server can confirm the wire format is right.
*/
const (
	testURLEnvironment       = "CONVIA_TEST_LIVEKIT_URL"
	testAPIKeyEnvironment    = "CONVIA_TEST_LIVEKIT_API_KEY"
	testAPISecretEnvironment = "CONVIA_TEST_LIVEKIT_API_SECRET"
)

// newRealPlane builds an adapter pointed at the configured LiveKit server.
func newRealPlane(t *testing.T, secret string) *Plane {
	t.Helper()

	endpoint := strings.TrimSpace(os.Getenv(testURLEnvironment))
	key := strings.TrimSpace(os.Getenv(testAPIKeyEnvironment))
	configured := strings.TrimSpace(os.Getenv(testAPISecretEnvironment))

	if endpoint == "" || key == "" || configured == "" {
		t.Skipf("set %s, %s, and %s to run the media integration tests",
			testURLEnvironment, testAPIKeyEnvironment, testAPISecretEnvironment)
	}
	if secret == "" {
		secret = configured
	}

	plane, err := New(Config{
		URL:       endpoint,
		APIKey:    key,
		APISecret: media.APISecret(secret),
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("build an adapter: %v", err)
	}
	return plane
}

/*
newCallID produces an identifier shaped like a real one.

Every test uses its own, so runs never collide with each other or with rooms
left behind by a previous one.
*/
func newCallID() string {
	return "call_" + rand.Text()
}

/*
TestARealServerRealizesAndReleasesASession is the whole boundary against a real
provider.

It is deliberately the plainest test here. Convia's two operations are open and
close, and if a real LiveKit accepts both of them as this adapter sends them,
the wire format is right.
*/
func TestARealServerRealizesAndReleasesASession(t *testing.T) {
	plane := newRealPlane(t, "")
	callID := newCallID()

	session, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: callID})
	if err != nil {
		t.Fatalf("open a session on a real server: %v", err)
	}
	t.Cleanup(func() {
		if err := plane.CloseSession(context.Background(), session); err != nil {
			t.Errorf("release the session: %v", err)
		}
	})

	if !session.Realized() {
		t.Fatal("a real server created a room but the session is not realized")
	}
	if session.Reference != callID {
		t.Errorf("the session references %q rather than the call it was opened for", session.Reference)
	}
}

/*
TestReleasingASessionTwiceOnARealServerSucceeds proves the claim ADR 0001
makes.

Releasing is best-effort and may be attempted again, and a room the provider
has already reclaimed — on its own empty timeout, or because a previous attempt
in fact succeeded — must not be reported as a failure. Only a real server
settles which of those it does.
*/
func TestReleasingASessionTwiceOnARealServerSucceeds(t *testing.T) {
	plane := newRealPlane(t, "")

	session, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: newCallID()})
	if err != nil {
		t.Fatalf("open a session on a real server: %v", err)
	}

	if err := plane.CloseSession(context.Background(), session); err != nil {
		t.Fatalf("release the session: %v", err)
	}
	if err := plane.CloseSession(context.Background(), session); err != nil {
		t.Errorf("releasing an already released session failed: %v", err)
	}
}

/*
TestARealServerAnswersAboutSomebodyWhoIsNotThere proves the two questions a
report is checked with are asked in a shape a real server accepts, and that
"not there" comes back as an answer rather than a failure.

A real connection cannot be opened from here: that takes a WebRTC client, which
ADR 0002 keeps out of this module. What can be proved is that the questions are
understood and that their negative answers are read correctly.
*/
func TestARealServerAnswersAboutSomebodyWhoIsNotThere(t *testing.T) {
	plane := newRealPlane(t, "")
	ctx := context.Background()

	session, err := plane.OpenSession(ctx, media.SessionRequest{CallID: newCallID()})
	if err != nil {
		t.Fatalf("open a session on a real server: %v", err)
	}
	t.Cleanup(func() { _ = plane.CloseSession(context.Background(), session) })

	nobody := "part_" + rand.Text()

	connected, err := plane.Connected(ctx, session, nobody)
	if err != nil {
		t.Fatalf("ask a real server whether somebody is connected: %v", err)
	}
	if connected {
		t.Error("a real server reported somebody connected who never was")
	}

	if err := plane.Disconnect(ctx, session, nobody); err != nil {
		t.Errorf("disconnecting somebody who is not connected failed on a real server: %v", err)
	}
}

// TestARealServerAnswersAboutARoomItNoLongerHas covers the report that arrives
// after the call's session was released.
func TestARealServerAnswersAboutARoomItNoLongerHas(t *testing.T) {
	plane := newRealPlane(t, "")
	ctx := context.Background()

	session, err := plane.OpenSession(ctx, media.SessionRequest{CallID: newCallID()})
	if err != nil {
		t.Fatalf("open a session on a real server: %v", err)
	}
	if err := plane.CloseSession(ctx, session); err != nil {
		t.Fatalf("release the session: %v", err)
	}

	nobody := "part_" + rand.Text()

	if connected, err := plane.Connected(ctx, session, nobody); err != nil || connected {
		t.Errorf("Connected() on a released session = %t, %v, want false and no error", connected, err)
	}
	if err := plane.Disconnect(ctx, session, nobody); err != nil {
		t.Errorf("Disconnect() on a released session error = %v", err)
	}
}

/*
TestARealServerKeepsAModerationTokenToItsOwnRoom proves the permission is as
narrow as moderationOf claims, which only a real server can: a token minted to
act inside one room is refused inside another.
*/
func TestARealServerKeepsAModerationTokenToItsOwnRoom(t *testing.T) {
	plane := newRealPlane(t, "")
	ctx := context.Background()

	mine, err := plane.OpenSession(ctx, media.SessionRequest{CallID: newCallID()})
	if err != nil {
		t.Fatalf("open a session on a real server: %v", err)
	}
	t.Cleanup(func() { _ = plane.CloseSession(context.Background(), mine) })

	theirs, err := plane.OpenSession(ctx, media.SessionRequest{CallID: newCallID()})
	if err != nil {
		t.Fatalf("open another session on a real server: %v", err)
	}
	t.Cleanup(func() { _ = plane.CloseSession(context.Background(), theirs) })

	body := map[string]any{"room": theirs.Reference, "identity": "part_" + rand.Text()}

	err = plane.call(ctx, "GetParticipant", moderationOf(mine.Reference), body, nil)
	if err == nil {
		t.Fatal("a real server let a token for one room act inside another")
	}
	if media.Retryable(err) {
		t.Errorf("a refused token produced %v, which invites a retry that cannot work", err)
	}
}

/*
TestARealServerRefusingACredentialIsTerminal proves the distinction reaches the
control plane correctly.

A wrong secret is a misconfiguration: retrying it forever would never fix it,
and an operator has to act. The classification is asserted against a real
refusal because the status and code a provider actually answers with are the
thing being relied on.
*/
func TestARealServerRefusingACredentialIsTerminal(t *testing.T) {
	plane := newRealPlane(t, "a secret this server has certainly never been given")

	_, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: newCallID()})
	if err == nil {
		t.Fatal("a real server accepted a token signed with the wrong secret")
	}
	if media.Retryable(err) {
		t.Errorf("a rejected credential produced %v, which invites a retry that cannot work", err)
	}
}

/*
TestARealServerNeverSeesACredentialInAnError repeats M12-003 where the message
is the provider's own.

The unit tests control what the fake says. Here the detail comes from LiveKit,
so this is what proves Convia does not add a credential to it on the way out.
*/
func TestARealServerNeverSeesACredentialInAnError(t *testing.T) {
	const wrong = "a secret this server has certainly never been given"

	plane := newRealPlane(t, wrong)

	_, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: newCallID()})
	if err == nil {
		t.Fatal("a real server accepted a token signed with the wrong secret")
	}
	if strings.Contains(err.Error(), wrong) {
		t.Errorf("the secret is in the error an operator will read: %v", err)
	}
}
