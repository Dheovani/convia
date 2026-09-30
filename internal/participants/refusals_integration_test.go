package participants

import (
	"context"
	"errors"
	"testing"

	"convia/internal/calls"
)

/*
These cover the refusals that decide what a participation still permits once it
has ended, which a coverage audit found nothing reached.

They are not error handling. Each one is the moment the domain says a state
change does not apply any more, and a defect in any of them would not fail a
request: it would succeed at the wrong thing.
*/

/*
TestARoleCannotBeGivenToSomebodyWhoAlreadyLeft is why departure is terminal
rather than cosmetic.

A role is an authority inside a call. Granting one to somebody who has gone
would write an authority they hold and cannot be seen to hold, and the record
would say a moderator was appointed at a moment nobody was there. If they came
back, they would come back promoted by a change nobody watching the call ever
saw announced.

As with an ended call, the rule holds in two layers, and the one below is the
one that matters: see TestTheStoreRefusesTheSamePromotionTheServiceDoes.
*/
func TestARoleCannotBeGivenToSomebodyWhoAlreadyLeft(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	if _, err := setup.service.Leave(ctx, setup.first, participant.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	if _, err := setup.service.SetRole(ctx, setup.first, participant.ID,
		string(RoleModerator), ""); !errors.Is(err, ErrGone) {
		t.Fatalf("SetRole() after leaving error = %v, want %v", err, ErrGone)
	}

	// The refusal has to leave the record alone as well as answer no.
	stored, err := setup.service.Get(ctx, setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Role != RoleMember {
		t.Errorf("role = %q after a refused promotion, want %q", stored.Role, RoleMember)
	}
	if stored.Status != StatusLeft {
		t.Errorf("status = %q, want %q", stored.Status, StatusLeft)
	}
}

/*
TestTheStoreRefusesTheSamePromotionTheServiceDoes keeps the guard from living in
one layer only.

The service checks Present() before it writes, so the store's own condition is
never reached through it. That makes the store's guard the one nobody would
notice losing -- and the store is what a later caller would reach for.
*/
func TestTheStoreRefusesTheSamePromotionTheServiceDoes(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	if _, err := setup.service.Leave(ctx, setup.first, participant.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	store := NewStore(setup.pool)
	if _, err := store.SetRole(ctx, setup.first, participant.ID, RoleModerator,
		participant.CreatedAt); !errors.Is(err, ErrGone) {
		t.Fatalf("store.SetRole() after leaving error = %v, want %v", err, ErrGone)
	}

	// A participant of another tenant is not gone, it is not found: the store
	// distinguishes the two, and conflating them would leak that the id exists.
	if _, err := store.SetRole(ctx, setup.second, participant.ID, RoleModerator,
		participant.CreatedAt); !errors.Is(err, ErrNotFound) {
		t.Errorf("store.SetRole() across tenants error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestAGuestIsNotAdmittedToACallThatEnded is the guest half of a rule the
person-facing path already has.

A guest arrives with an invitation, which is permission granted earlier, so the
call it was granted for may be over by the time it is used. Admitting them
would put somebody into a conversation that stopped, holding a credential for a
media room nobody is in.

**The rule is enforced twice on purpose**, so deleting either half alone leaves
it holding: this early-out, and lockCall's check under the call's row lock,
which is the one that closes the race between ending a call and joining it. The
early-out is what keeps an ended call from costing a transaction, and the
locked check is what makes the answer true. Mutating one and watching this pass
is the design, not a gap -- mutating both is what this fails for.
*/
func TestAGuestIsNotAdmittedToACallThatEnded(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	if _, err := setup.calls.End(ctx, setup.first, call.ID, calls.ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	_, _, err := setup.service.AdmitGuest(ctx, setup.first, call.ID,
		"inv_4XZQP7KN2VJH6TBWMDR3YAFC5E", string(RoleMember))
	if !errors.Is(err, ErrCallEnded) {
		t.Fatalf("AdmitGuest() into an ended call error = %v, want %v", err, ErrCallEnded)
	}
}

/*
TestTheLockedCheckIsWhatActuallyStopsAJoinIntoAnEndedCall pins the half of the
rule that survives a race.

A service reading the call and then joining has a window between the two, and
the call can end inside it. Only the check taken under the call's own row lock
is answering about the state the write lands in, which is why it exists below a
check that looks identical.
*/
func TestTheLockedCheckIsWhatActuallyStopsAJoinIntoAnEndedCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")

	if _, err := setup.calls.End(ctx, setup.first, call.ID, calls.ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	// Straight at the store, which is where a caller racing the ending arrives.
	store := NewStore(setup.pool)
	_, _, err := store.Join(ctx, Participant{
		ApplicationID: setup.first,
		CallID:        call.ID,
		UserID:        user,
		Role:          RoleMember,
		CreatedAt:     call.CreatedAt,
	}, nil)
	if !errors.Is(err, ErrCallEnded) {
		t.Fatalf("store.Join() into an ended call error = %v, want %v", err, ErrCallEnded)
	}
}

/*
TestAGuestWithoutAnInvitationIsNotAdmitted guards the one identity a guest has.

A guest has no user record; the invitation is what names them. Admitting one
without it would create a participation nothing can be traced back to, and the
call would hold somebody no audit could account for.
*/
func TestAGuestWithoutAnInvitationIsNotAdmitted(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	_, _, err := setup.service.AdmitGuest(ctx, setup.first, call.ID, "", string(RoleMember))

	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("AdmitGuest() without an invitation error = %v, want a validation error", err)
	}
	if invalid.Field != "invitation_id" {
		t.Errorf("field = %q, want %q", invalid.Field, "invitation_id")
	}
}

/*
TestLeavingARoomNobodyIsCallingInIsNotAnError keeps a client's cleanup from
depending on a race it cannot win.

Somebody closing a window sends a departure; the last other person leaving ends
the call at the same moment. Whichever lands second must not fail, or a normal
exit reports an error the person can do nothing about.
*/
func TestLeavingARoomNobodyIsCallingInIsNotAnError(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	room := setup.personalRoom(t, setup.first, ana)

	if err := setup.service.LeaveRoom(ctx, setup.first, room.ID, ana,
		calls.ActorPerson); err != nil {
		t.Fatalf("LeaveRoom() with no call error = %v, want none", err)
	}
}
