package invitations

import (
	"context"
	"testing"
	"time"

	"convia/internal/events"
	"convia/internal/participants"
)

// announced is how long a test waits for an event that was published
// synchronously and should therefore already be there.
const announced = 2 * time.Second

// listenTo subscribes to everything the fixture's broker carries for a tenant.
func listenTo(t *testing.T, setup fixture, applicationID string) *events.Stream {
	t.Helper()

	stream, err := setup.broker.Subscribe(applicationID, events.Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	t.Cleanup(stream.Close)
	return stream
}

// next takes the event a stream should already be holding.
func next(t *testing.T, stream *events.Stream) events.Event {
	t.Helper()

	select {
	case event := <-stream.Events():
		return event
	case <-time.After(announced):
		t.Fatal("nothing was announced")
		return events.Event{}
	}
}

// nothingMore fails when a stream carries an event it should not.
func nothingMore(t *testing.T, stream *events.Stream) {
	t.Helper()

	select {
	case event := <-stream.Events():
		t.Errorf("an unexpected %s about %s was announced", event.Type, event.Subject.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

/*
TestDecliningIsAnnouncedAndTheRestIsNot is the whole of M14-001 for
invitations, and the reasoning is worth stating where it can be checked.

Issuing and withdrawing are the application's own acts, made through requests
that already returned the result. Redeeming is somebody else's act, but it
already arrives as a participant joining, and announcing both would report one
arrival twice. Declining is the only one left: it is the invitee's own
decision, it is the only signal that somebody is not coming, and nothing else
observes it.
*/
func TestDecliningIsAnnouncedAndTheRestIsNot(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")

	stream := listenTo(t, setup, setup.first)

	invitation, _ := setup.invite(t, setup.first, call.ID, userID, "member")
	nothingMore(t, stream)

	withdrawn, _ := setup.invite(t, setup.first, call.ID, userID, "member")
	if _, err := setup.service.Revoke(ctx, setup.first, withdrawn.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	nothingMore(t, stream)

	declined, err := setup.service.Decline(ctx, invitation)
	if err != nil {
		t.Fatalf("Decline() error = %v", err)
	}

	announcement := next(t, stream)
	if announcement.Type != events.InvitationDeclined {
		t.Errorf("declining announced %q", announcement.Type)
	}
	if announcement.Subject.Type != events.SubjectInvitation ||
		announcement.Subject.ID != declined.ID {
		t.Errorf("the announcement is about %+v", announcement.Subject)
	}
	if announcement.Data["call_id"] != call.ID {
		t.Errorf("the announcement names call %v", announcement.Data["call_id"])
	}
	if announcement.Data["user_id"] != userID {
		t.Errorf("the announcement names user %v", announcement.Data["user_id"])
	}
}

/*
TestRedeemingArrivesAsSomebodyJoining is the other side of the decision above.

A subscriber is told once, in the vocabulary that describes what actually
changed: somebody is now in the call.
*/
func TestRedeemingArrivesAsSomebodyJoining(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	invitation, _ := setup.invite(t, setup.first, call.ID, userID, "member")

	stream := listenTo(t, setup, setup.first)

	_, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	joined := next(t, stream)
	if joined.Type != events.ParticipantJoined {
		t.Errorf("redeeming announced %q", joined.Type)
	}
	if joined.Subject.ID != participant.ID {
		t.Errorf("the announcement is about %q", joined.Subject.ID)
	}
	nothingMore(t, stream)
}

/*
TestAGuestIsAnnouncedByTheirInvitation carries the guest slice's decision into
the stream.

Convia has no name for a guest, so the identity it publishes is the invitation
they redeemed — and `guest` is published rather than left to be inferred from
an absent `user_id`, for the same reason the roster publishes it.
*/
func TestAGuestIsAnnouncedByTheirInvitation(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation := setup.inviteGuest(t, setup.first, call.ID, "member")

	stream := listenTo(t, setup, setup.first)

	_, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	joined := next(t, stream)
	if joined.Subject.ID != participant.ID {
		t.Errorf("the announcement is about %q", joined.Subject.ID)
	}
	if joined.Data["guest"] != true {
		t.Errorf("a guest was announced with guest = %v", joined.Data["guest"])
	}
	if joined.Data["invitation_id"] != invitation.ID {
		t.Errorf("the announcement identifies the guest as %v", joined.Data["invitation_id"])
	}

	/*
		Not an empty string, which would read as a person whose identifier
		failed to be recorded. A guest has no user, and the announcement says
		so by carrying nothing at all.
	*/
	if _, present := joined.Data["user_id"]; present {
		t.Errorf("a guest was announced with a user: %v", joined.Data["user_id"])
	}
	if joined.Data["role"] != string(participants.RoleMember) {
		t.Errorf("the guest was announced as a %v", joined.Data["role"])
	}
}
