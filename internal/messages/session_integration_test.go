package messages

import (
	"context"
	"errors"
	"testing"

	"convia/internal/rooms"
	"convia/internal/sessions"
)

// asPerson binds the fixture's service to one signed-in person.
func (setup fixture) asPerson(applicationID, userID string) *Personal {
	return AsPerson(setup.service, setup.rooms, sessions.Principal{
		SessionID:     "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		AccountID:     "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E",
		UserID:        userID,
		ApplicationID: applicationID,
	})
}

// join gives somebody a place in a room.
func (setup fixture) join(t *testing.T, applicationID, roomID, userID string) {
	t.Helper()

	if _, _, err := setup.rooms.AddMember(context.Background(), applicationID, roomID, userID); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
}

/*
TestThePersonIsTheAuthorAndNothingElseCanBe is the property the whole surface
rests on.

An application names which of its people is speaking, because it is acting on
their behalf. A person cannot name anybody, because naming somebody would mean
naming somebody else — and the type has no parameter and no field that could.
*/
func TestThePersonIsTheAuthorAndNothingElseCanBe(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	setup.join(t, setup.first, room.ID, ana.UserID)

	message, err := setup.asPerson(setup.first, ana.UserID).Post(ctx, room.ID, "on my way")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if message.Author.UserID != ana.UserID {
		t.Errorf("the message is attributed to %q, want %q", message.Author.UserID, ana.UserID)
	}
	if message.Author.Guest() {
		t.Error("a signed-in person wrote as a guest")
	}
}

/*
TestSomebodyOutsideAConversationIsToldItIsNotThere is the privacy rule.

A refusal that separates "not yours" from "does not exist" confirms to somebody
outside a conversation that the conversation is happening, which is most of what
they were asking.
*/
func TestSomebodyOutsideAConversationIsToldItIsNotThere(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")
	setup.join(t, setup.first, room.ID, ana.UserID)

	if _, err := setup.asPerson(setup.first, ana.UserID).Post(ctx, room.ID, "a secret"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	stranger := setup.asPerson(setup.first, bruno.UserID)

	if _, err := stranger.History(ctx, room.ID, HistoryOptions{}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("reading error = %v, want %v", err, ErrRoomNotFound)
	}
	if _, err := stranger.Post(ctx, room.ID, "hello"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("writing error = %v, want %v", err, ErrRoomNotFound)
	}
	if _, err := stranger.ReadState(ctx, room.ID); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("reading read state error = %v, want %v", err, ErrRoomNotFound)
	}
	if _, err := stranger.MarkRead(ctx, room.ID, 1); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("marking read error = %v, want %v", err, ErrRoomNotFound)
	}
}

/*
TestTheSidebarCarriesWhatHasNotBeenRead is the one view an interface redraws
constantly, so it is the one that must answer completely in one call.
*/
func TestTheSidebarCarriesWhatHasNotBeenRead(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	standup := setup.newRoom(t, setup.first)
	support := setup.newRoom(t, setup.first)

	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for _, room := range []string{standup.ID, support.ID} {
		setup.join(t, setup.first, room, ana.UserID)
		setup.join(t, setup.first, room, bruno.UserID)
	}

	speaker := setup.asPerson(setup.first, ana.UserID)
	for index := 0; index < 3; index++ {
		if _, err := speaker.Post(ctx, standup.ID, "hello"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}
	if _, err := speaker.Post(ctx, support.ID, "a ticket"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	reader := setup.asPerson(setup.first, bruno.UserID)

	sidebar, err := reader.Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Rooms() error = %v", err)
	}
	if len(sidebar.Rooms) != 2 {
		t.Fatalf("the sidebar holds %d rooms, want 2", len(sidebar.Rooms))
	}

	unread := make(map[string]int64, len(sidebar.Rooms))
	for _, room := range sidebar.Rooms {
		unread[room.Room.ID] = room.Unread
		if room.Room.Name == "" {
			t.Error("a sidebar row carries no name, so nothing could render it")
		}
	}
	if unread[standup.ID] != 3 || unread[support.ID] != 1 {
		t.Errorf("unread = %v, want 3 and 1", unread)
	}

	// Reading one room clears that badge and leaves the other alone.
	if _, err := reader.MarkRead(ctx, standup.ID, 3); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}

	sidebar, err = reader.Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Rooms() error = %v", err)
	}
	for _, room := range sidebar.Rooms {
		want := int64(1)
		if room.Room.ID == standup.ID {
			want = 0
		}
		if room.Unread != want {
			t.Errorf("%s has %d unread, want %d", room.Room.ID, room.Unread, want)
		}
	}

	// The author's own sidebar counts nothing: they wrote all of it.
	hers, err := speaker.Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Rooms() error = %v", err)
	}
	for _, room := range hers.Rooms {
		if room.Unread != 0 {
			t.Errorf("the author has %d unread of her own in %s", room.Unread, room.Room.ID)
		}
	}
}

/*
TestASidebarShowsOnlyTheRoomsSomebodyIsIn keeps a listing from becoming a
directory of every conversation in the deployment.
*/
func TestASidebarShowsOnlyTheRoomsSomebodyIsIn(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	mine := setup.newRoom(t, setup.first)
	setup.newRoom(t, setup.first)

	ana := setup.newAuthor(t, setup.first, "ana")
	setup.join(t, setup.first, mine.ID, ana.UserID)

	sidebar, err := setup.asPerson(setup.first, ana.UserID).Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Rooms() error = %v", err)
	}
	if len(sidebar.Rooms) != 1 || sidebar.Rooms[0].Room.ID != mine.ID {
		t.Errorf("the sidebar holds %+v, want only the room she is in", sidebar.Rooms)
	}
}

/*
TestBeingRemovedDoesNotMakeYourWordsSomebodyElses covers the asymmetry the
session type documents.

Membership governs reaching a room. Editing is governed by authorship, which is
stricter: somebody who wrote a message was in the room when they wrote it, and
having been removed since does not hand their own words to anybody else.
*/
func TestBeingRemovedDoesNotMakeYourWordsSomebodyElses(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	setup.join(t, setup.first, room.ID, ana.UserID)

	person := setup.asPerson(setup.first, ana.UserID)

	message, err := person.Post(ctx, room.ID, "something I regret")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.rooms.RemoveMember(ctx, setup.first, room.ID, ana.UserID); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}

	if _, err := person.History(ctx, room.ID, HistoryOptions{}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("reading after removal error = %v, want %v", err, ErrRoomNotFound)
	}

	if _, err := person.Delete(ctx, message.ID); err != nil {
		t.Errorf("withdrawing her own message after removal error = %v, want none", err)
	}
}

/*
TestOnePersonCannotActAsAnotherThroughTheSameService is the tenant boundary and
the person boundary at once.

Two principals over one service must not be able to read each other's rooms even
though they share every dependency.
*/
func TestOnePersonCannotActAsAnotherThroughTheSameService(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	setup.join(t, setup.first, room.ID, ana.UserID)

	theirs := setup.newAuthor(t, setup.second, "ana")

	if _, err := setup.asPerson(setup.second, theirs.UserID).History(ctx, room.ID,
		HistoryOptions{}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("another tenant's person read our room: %v", err)
	}

	sidebar, err := setup.asPerson(setup.second, theirs.UserID).Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Rooms() error = %v", err)
	}
	if len(sidebar.Rooms) != 0 {
		t.Errorf("another tenant's person sees %d of our rooms", len(sidebar.Rooms))
	}
}
