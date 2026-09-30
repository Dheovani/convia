package calls

import (
	"context"
	"errors"
	"testing"
)

/*
These cover what ending a call does when there is nothing left to end, which a
coverage audit found nothing reached.

EndIfEmpty runs on a timer and after every departure, so it is called far more
often than it acts. Its uninteresting answers are its normal ones, and a defect
there does not raise an error: it ends a conversation somebody is still in, or
rewrites who ended one that was already over.
*/

/*
TestEndingAnEmptyCallThatAlreadyEndedChangesNothing keeps the last departure
from being credited to the sweep behind it.

Ending a call records who ended it and why. EndIfEmpty arrives after the person
who actually ended it, so if it wrote again, every call would eventually read
as ended by the system, and the reason the last person gave would be gone.
*/
func TestEndingAnEmptyCallThatAlreadyEndedChangesNothing(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.start(t, setup.first, setup.firstRoom)

	ended, err := setup.service.End(ctx, setup.first, call.ID, ActorPerson, "Goodbye.")
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}

	again, acted, err := setup.service.EndIfEmpty(ctx, setup.first, call.ID,
		ActorApplication, "Nobody is here.")
	if err != nil {
		t.Fatalf("EndIfEmpty() on an ended call error = %v", err)
	}
	if acted {
		t.Error("EndIfEmpty() acted on a call that had already ended")
	}
	if !again.EndedAt.Equal(*ended.EndedAt) {
		t.Error("the sweep moved the moment the call ended")
	}
	if again.EndReason != ended.EndReason {
		t.Errorf("reason = %q, want the first one %q", again.EndReason, ended.EndReason)
	}
	// EndedBy is a pointer, so this compares who ended the call rather than
	// which copy of the answer the two reads happened to allocate.
	if ended.EndedBy == nil || again.EndedBy == nil || *again.EndedBy != *ended.EndedBy {
		t.Errorf("ended by %v, want the first one %v", again.EndedBy, ended.EndedBy)
	}
	if *ended.EndedBy != ActorPerson {
		t.Errorf("the person who ended it is recorded as %v, want %v", *ended.EndedBy, ActorPerson)
	}
}

/*
TestEndingAnEmptyCallThatIsNotThereIsNotFound separates a call that is over from
one that never was, and keeps the separation across tenants.

The sweep holds identifiers it read earlier, so by the time it acts the call may
be gone. Answering "already ended" for a call this application never had would
confirm the identifier to somebody guessing.
*/
func TestEndingAnEmptyCallThatIsNotThereIsNotFound(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if _, _, err := setup.service.EndIfEmpty(ctx, setup.first,
		"call_4XZQP7KN2VJH6TBWMDR3YAFC5E", ActorApplication, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("EndIfEmpty() on a call that is not there error = %v, want %v", err, ErrNotFound)
	}

	call := setup.start(t, setup.first, setup.firstRoom)
	if _, _, err := setup.service.EndIfEmpty(ctx, setup.second, call.ID,
		ActorApplication, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("EndIfEmpty() across tenants error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestAMalformedCallIdentifierIsNotFoundRatherThanInvalid keeps the shape of an
identifier from being an oracle.

Answering "that is not a call identifier" tells somebody probing which of their
guesses are the right shape, which is the only expensive part of guessing. The
answer is the same one a well-formed identifier nobody holds gets.

**This pins the answer rather than the check that produces it**, and it survives
the check being deleted: a malformed identifier then reaches the query, matches
no row, and comes back the same way. That is the point -- the shape check is an
early-out that saves a round trip, and the contract holds without it. What it
would not survive is somebody answering a distinct error for a bad shape.
*/
func TestAMalformedCallIdentifierIsNotFoundRatherThanInvalid(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for _, id := range []string{"", "call_", "not-an-id", "user_4XZQP7KN2VJH6TBWMDR3YAFC5E"} {
		if _, err := setup.service.End(ctx, setup.first, id, ActorApplication, ""); !errors.Is(err, ErrNotFound) {
			t.Errorf("End(%q) error = %v, want %v", id, err, ErrNotFound)
		}
	}
}
