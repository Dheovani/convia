package participants

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"convia/internal/events"
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
TestTheRosterIsAnnouncedAsItChanges is why M14 exists at all.

Who is in a conversation right now is the thing whose value decays fastest: a
roster that is thirty seconds out of date shows people who have gone and misses
people who have arrived, and no amount of polling makes that better without
making it expensive.
*/
func TestTheRosterIsAnnouncedAsItChanges(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")

	stream := listenTo(t, setup, setup.first)

	participant, _, err := setup.service.Join(ctx, setup.first, call.ID,
		Admission{UserID: userID, Role: string(RoleMember)})
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}

	joined := next(t, stream)
	if joined.Type != events.ParticipantJoined {
		t.Errorf("joining announced %q", joined.Type)
	}
	if joined.Subject.Type != events.SubjectParticipant || joined.Subject.ID != participant.ID {
		t.Errorf("the announcement is about %+v", joined.Subject)
	}
	// The room is what a person's stream admits an event by.
	if joined.Data["room_id"] != call.RoomID {
		t.Errorf("the announcement places the call in room %v, want %s", joined.Data["room_id"], call.RoomID)
	}
	if joined.Data["call_id"] != call.ID {
		t.Errorf("the announcement places the participant in %v", joined.Data["call_id"])
	}
	if joined.Data["user_id"] != userID {
		t.Errorf("the announcement names user %v", joined.Data["user_id"])
	}
	if joined.Data["guest"] != false {
		t.Errorf("a named user was announced with guest = %v", joined.Data["guest"])
	}

	if _, err := setup.service.SetRole(ctx, setup.first, participant.ID,
		string(RoleModerator), ""); err != nil {
		t.Fatalf("SetRole() error = %v", err)
	}
	if promoted := next(t, stream); promoted.Type != events.ParticipantRoleChanged ||
		promoted.Data["role"] != string(RoleModerator) {
		t.Errorf("promoting announced %q with role %v", promoted.Type, promoted.Data["role"])
	}

	if _, err := setup.service.Leave(ctx, setup.first, participant.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if left := next(t, stream); left.Type != events.ParticipantLeft ||
		left.Data["status"] != string(StatusLeft) {
		t.Errorf("leaving announced %q with status %v", left.Type, left.Data["status"])
	}
}

/*
TestARemovalIsAnnouncedWithoutSayingWhy is the privacy line the audit trail
already draws.

The authority is Convia's own and is published; the reason is text the
application composed about the person being removed, and it reaches every
subscriber at once. A client that needs it reads the participant.
*/
func TestARemovalIsAnnouncedWithoutSayingWhy(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	participant := setup.join(t, setup.first, call.ID, userID, RoleMember)

	stream := listenTo(t, setup, setup.first)

	if _, err := setup.service.Remove(ctx, setup.first, participant.ID,
		RemoverApplication, "", "Ana would not stop shouting."); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	removed := next(t, stream)
	if removed.Type != events.ParticipantRemoved {
		t.Errorf("removing announced %q", removed.Type)
	}
	if removed.Data["removed_by"] != string(RemoverApplication) {
		t.Errorf("the announcement says %v removed them", removed.Data["removed_by"])
	}

	encoded, err := json.Marshal(removed)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "shouting") {
		t.Errorf("the announcement carries the removal reason: %s", encoded)
	}
}

/*
TestIssuingAConnectionCredentialIsNotAnnounced is the exclusion M14-001 decided
deliberately.

It is the result of a request the subscriber itself made, and it is a fact
about a secret. Announcing it would tell every subscriber that somebody was
handed the means to connect, which is both useless and more than they need.
*/
func TestIssuingAConnectionCredentialIsNotAnnounced(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	participant := setup.join(t, setup.first, call.ID, userID, RoleMember)

	stream := listenTo(t, setup, setup.first)

	if _, _, err := setup.service.Session(ctx, setup.first, participant.ID); err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	nothingMore(t, stream)

	/*
		It is still recorded. The audit trail is where an incident is
		reconstructed, and an issued credential is exactly the kind of thing an
		incident asks about.
	*/
	if !strings.Contains(setup.logs.String(), "participant.session_issued") {
		t.Error("issuing a credential was neither announced nor recorded")
	}
}

/*
TestJoiningTwiceIsAnnouncedOnce follows the idempotency of joining into the
stream.

A client whose connection dropped and came back is the same person, and a
second announcement would tell every subscriber somebody arrived who was
already there.
*/
func TestJoiningTwiceIsAnnouncedOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")

	stream := listenTo(t, setup, setup.first)

	admission := Admission{UserID: userID, Role: string(RoleMember)}
	if _, _, err := setup.service.Join(ctx, setup.first, call.ID, admission); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if _, _, err := setup.service.Join(ctx, setup.first, call.ID, admission); err != nil {
		t.Fatalf("Join() again error = %v", err)
	}

	if joined := next(t, stream).Type; joined != events.ParticipantJoined {
		t.Fatalf("the first announcement was %q", joined)
	}
	nothingMore(t, stream)
}
