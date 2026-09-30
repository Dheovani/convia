package invitations

import (
	"context"
	"errors"
	"testing"
)

/*
These cover the two refusals on the declining side, which a coverage audit found
nothing reached.

Redeeming a withdrawn invitation is tested; declining one was not, and the two
are not the same question. Redeeming is somebody trying to get in. Declining is
somebody answering an invitation that is no longer theirs to answer, and a
defect there writes a refusal nobody was asked for.
*/

/*
TestAWithdrawnInvitationCannotBeDeclined keeps a withdrawal from being rewritten
as a refusal.

The two look alike from outside and mean opposite things: an application
withdrew the invitation, or the invitee said no. Letting a decline land on a
revoked invitation would tell the application its guest turned them down, when
in fact the application had already changed its mind.
*/
func TestAWithdrawnInvitationCannotBeDeclined(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, _ := setup.invite(t, setup.first, call.ID,
		setup.newUser(t, setup.first, "ada"), "member")

	revoked, err := setup.service.Revoke(ctx, setup.first, invitation.ID)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if _, err := setup.service.Decline(ctx, revoked); !errors.Is(err, ErrUnusable) {
		t.Fatalf("Decline() on a withdrawn invitation error = %v, want %v", err, ErrUnusable)
	}

	// The refusal has to leave the record alone as well as answer no.
	stored, err := setup.service.Get(ctx, setup.first, invitation.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.DeclinedAt != nil {
		t.Error("a refused decline was written anyway")
	}
}

/*
TestAMalformedInvitationIdentifierIsNotFoundRatherThanInvalid keeps the shape of
an identifier from being an oracle.

Answering "that is not an invitation identifier" tells somebody probing which of
their guesses are the right shape, which is the only expensive part of guessing.
The answer is the same one a well-formed identifier nobody holds gets.

**This pins the answer rather than the check that produces it**, and it survives
the check being deleted: a malformed identifier then reaches the query, matches
no row, and comes back the same way. That is the point -- the shape check is an
early-out that saves a round trip, and the contract holds without it. What it
would not survive is somebody answering a distinct error for a bad shape.
*/
func TestAMalformedInvitationIdentifierIsNotFoundRatherThanInvalid(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for _, id := range []string{"", "inv_", "not-an-id", "call_4XZQP7KN2VJH6TBWMDR3YAFC5E"} {
		if _, err := setup.service.Revoke(ctx, setup.first, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Revoke(%q) error = %v, want %v", id, err, ErrNotFound)
		}
	}
}
