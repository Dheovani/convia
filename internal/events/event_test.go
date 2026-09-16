package events

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"convia/internal/credentials"
)

/*
TestAnEventIsSelfDescribing is what M14-002 and M14-003 asked for together.

An event has to be interpretable on its own, away from the connection it
arrived on, because the same envelope is what a webhook will carry when M15
arrives and what an operator will read out of a log today. Everything needed
for that is on the event: what it is, which version of the shape it is in, when
it happened, whose it is, what it is about, and what caused it.
*/
func TestAnEventIsSelfDescribing(t *testing.T) {
	event := New(ParticipantJoined, "app_1", "part_1", "req_1", Data{"call_id": "call_1"})

	if !strings.HasPrefix(event.ID, idPrefix) {
		t.Errorf("the event identifier %q does not say what it identifies", event.ID)
	}
	if event.Version != Version {
		t.Errorf("the event carries version %d, not %d", event.Version, Version)
	}
	if event.OccurredAt.IsZero() {
		t.Error("the event says nothing about when it happened")
	}
	if event.OccurredAt.Location().String() != "UTC" {
		t.Errorf("the event happened in %s rather than UTC", event.OccurredAt.Location())
	}
	if event.ApplicationID != "app_1" {
		t.Errorf("the event belongs to %q", event.ApplicationID)
	}
	if event.CorrelationID != "req_1" {
		t.Errorf("the event names request %q", event.CorrelationID)
	}
}

/*
TestTwoEventsAreNeverTheSameEvent covers the one property an identifier exists
for.

A consumer that keeps a record of what it has already handled is relying on
this, and two events sharing an identifier would make one of them invisible.
*/
func TestTwoEventsAreNeverTheSameEvent(t *testing.T) {
	seen := make(map[string]bool, 64)
	for range 64 {
		id := New(CallStarted, "app_1", "call_1", "", nil).ID
		if seen[id] {
			t.Fatalf("the identifier %q was issued twice", id)
		}
		seen[id] = true
	}
}

/*
TestTheSubjectCannotDisagreeWithTheType makes a whole class of mistake
unrepresentable.

An event claiming to be about a call while carrying a participant identifier
would send a consumer looking for something that is not there. The subject type
is therefore derived from the event type rather than supplied, so there is no
argument a caller could get wrong.
*/
func TestTheSubjectCannotDisagreeWithTheType(t *testing.T) {
	expected := map[Type]SubjectType{
		CallStarted:            SubjectCall,
		CallEnded:              SubjectCall,
		ParticipantJoined:      SubjectParticipant,
		ParticipantLeft:        SubjectParticipant,
		ParticipantRemoved:     SubjectParticipant,
		ParticipantRoleChanged: SubjectParticipant,
		InvitationDeclined:     SubjectInvitation,
		MessagePosted:          SubjectMessage,
		MessageEdited:          SubjectMessage,
		MessageDeleted:         SubjectMessage,
		MemberAdded:            SubjectRoom,
		MemberRemoved:          SubjectRoom,
		RoomUpdated:            SubjectRoom,
		RoomClosed:             SubjectRoom,
		RoomReopened:           SubjectRoom,
		RoomDeleted:            SubjectRoom,
		PresenceChanged:        SubjectUser,
	}

	for _, kind := range Types() {
		want, described := expected[kind]
		if !described {
			t.Errorf("%q was added to the vocabulary without deciding what it is about", kind)
			continue
		}
		if got := New(kind, "app_1", "sub_1", "", nil).Subject.Type; got != want {
			t.Errorf("%q is about a %q, want %q", kind, got, want)
		}
	}
}

/*
TestOnlyPresenceIsRefusedADurableDelivery states the one exception, and states
that it is one.

A webhook is a delivery with attempts behind it. A presence report that failed
once arrives after it stopped being true, and after the newer one that replaced
it — so an application subscribing to it by webhook would end up with a roster
that never settles. Every other type is a record of something that happened,
and a late delivery of one of those is still true.

It is written as a whole-vocabulary check rather than an assertion about one
constant, so that a second advisory type is a decision somebody makes here
rather than a default they inherit.
*/
func TestOnlyPresenceIsRefusedADurableDelivery(t *testing.T) {
	for _, kind := range Types() {
		durable := Durable(kind)
		if kind == PresenceChanged && durable {
			t.Errorf("%q may be queued for redelivery, which would deliver it after it stopped being true", kind)
		}
		if kind != PresenceChanged && !durable {
			t.Errorf("%q records something that happened and must be deliverable by webhook", kind)
		}
	}
}

/*
TestBuildingAnEventConviaDoesNotPublishIsRefused guards the closed vocabulary.

Every caller is inside Convia, so a type nobody recognizes is a programming
mistake rather than a runtime condition, and publishing something no client can
interpret would be the worse way to discover it.
*/
func TestBuildingAnEventConviaDoesNotPublishIsRefused(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Error("an unknown event type produced an event")
		}
	}()

	New(Type("call.rescheduled"), "app_1", "call_1", "", nil)
}

/*
TestEveryTypeIsDeliverableToSomebody is the test the default branch in
readingScopeFor exists for.

Adding a type to the vocabulary without deciding which scope may see it would
otherwise produce an event delivered to nobody, silently, and the first sign of
it would be a client asking why an event it was promised never arrives.
*/
func TestEveryTypeIsDeliverableToSomebody(t *testing.T) {
	granted := credentials.Scopes()

	for _, kind := range Types() {
		scope := readingScopeFor(kind)
		if scope == "" {
			t.Errorf("%q has no scope that may read it", kind)
			continue
		}
		if !slices.Contains(granted, scope) {
			t.Errorf("%q requires %q, which is not a scope Convia recognizes", kind, scope)
		}
	}
}

/*
TestTheWireShapeIsTheOneTheContractPublishes checks the JSON a subscriber
actually reads.

The field names are the contract, and Go's own field names are not: a rename
that forgot its tag would compile, pass every other test, and break every
client at once.
*/
func TestTheWireShapeIsTheOneTheContractPublishes(t *testing.T) {
	event := New(ParticipantRemoved, "app_1", "part_1", "req_1", Data{
		"call_id": "call_1",
		"guest":   true,
	})

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	for _, field := range []string{"id", "version", "type", "occurred_at",
		"application_id", "subject", "correlation_id", "data"} {
		if _, present := wire[field]; !present {
			t.Errorf("the delivered event carries no %q: %s", field, encoded)
		}
	}

	subject, isObject := wire["subject"].(map[string]any)
	if !isObject {
		t.Fatalf("the subject is not an object: %s", encoded)
	}
	if subject["type"] != string(SubjectParticipant) || subject["id"] != "part_1" {
		t.Errorf("the subject is %v", subject)
	}

	/*
		A boolean stays a boolean. Flattening it to a string would make every
		consumer parse a value Convia already knew the type of, and `guest` is
		exactly the field a client branches on.
	*/
	data, isObject := wire["data"].(map[string]any)
	if !isObject {
		t.Fatalf("the data is not an object: %s", encoded)
	}
	if data["guest"] != true {
		t.Errorf("the guest flag arrived as %#v", data["guest"])
	}
}

/*
TestAnEventWithNoCauseCarriesNoCorrelation keeps an absent value absent.

An empty string would look like a request identifier that failed to be
recorded. Nothing is the honest answer when nothing caused it.
*/
func TestAnEventWithNoCauseCarriesNoCorrelation(t *testing.T) {
	encoded, err := json.Marshal(New(CallEnded, "app_1", "call_1", "", nil))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if strings.Contains(string(encoded), "correlation_id") {
		t.Errorf("an uncaused event still carries a correlation identifier: %s", encoded)
	}
	if strings.Contains(string(encoded), `"data"`) {
		t.Errorf("an event with nothing to say still carries a data object: %s", encoded)
	}
}
