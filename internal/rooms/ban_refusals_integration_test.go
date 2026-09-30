package rooms

import (
	"context"
	"errors"
	"testing"
)

/*
TestBanningSomebodyWhoIsNotAPersonIsRefused stops a ban from creating the person
it is about.

A ban is a row keyed by a user identifier, and nothing about writing one
requires that identifier to name anybody. Without this check an application
could fill room_bans with identifiers that resolve to nothing, and each of those
rows would then be consulted on every join for a room -- a list that grows, is
never satisfied, and describes nobody.

It is also an oracle if it is not refused before the write: an application of
another tenant could learn which identifiers exist by which bans it was allowed
to record.

Adding a stranger is already covered; banning one was not, and the two reach the
person through different paths.
*/
func TestBanningSomebodyWhoIsNotAPersonIsRefused(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")

	// A well-formed identifier that names nobody, and one belonging to the
	// other tenant: both are strangers here, and both get the same answer.
	stranger := setup.newPerson(t, setup.second, "ada")

	for name, userID := range map[string]string{
		"nobody":              "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA",
		"another tenant's":    stranger,
		"a malformed id":      "not-an-id",
		"the wrong id family": "room_4XZQP7KN2VJH6TBWMDR3YAFC5E",
	} {
		if _, err := setup.service.Ban(ctx, setup.first, room.ID, userID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("banning %s error = %v, want %v", name, err, ErrUserNotFound)
		}
	}

	// Nothing was recorded, so nothing is consulted on a later join.
	banned, err := setup.service.IsBanned(ctx, setup.first, room.ID, stranger)
	if err != nil {
		t.Fatalf("IsBanned() error = %v", err)
	}
	if banned {
		t.Error("a refused ban was written anyway")
	}
}
