package messages

import (
	"context"
	"errors"
	"strings"
	"testing"
)

/*
TestErasingSomebodyLeavesTheConversationStanding is the decision `00017` argues
for, asserted rather than trusted.

Taking a person's messages out of a room takes the conversation away from the
people still in it: every reply left behind answers something that is no longer
there. So the row keeps its place and loses what Convia held about them.
*/
func TestErasingSomebodyLeavesTheConversationStanding(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	const hers = "the merger closes on tuesday"

	first, err := setup.service.Post(ctx, setup.first, room.ID, ana, hers)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Post(ctx, setup.first, room.ID, bruno, "understood"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	third, err := setup.service.Post(ctx, setup.first, room.ID, ana, "keep it quiet")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	erased, err := setup.service.Erase(ctx, setup.first, ana.UserID)
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if erased.Messages != 2 {
		t.Errorf("Erase() redacted %d messages, want 2", erased.Messages)
	}

	page, err := setup.service.History(ctx, setup.first, room.ID, HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if got := sequencesOf(page.Messages); !equal(got, []int64{3, 2, 1}) {
		t.Errorf("the history reads %v, want every position still standing", got)
	}

	for _, message := range page.Messages {
		if message.ID == first.ID || message.ID == third.ID {
			if message.Body != "" {
				t.Errorf("an erased message still carries %q", message.Body)
			}
			if message.Author.UserID != "" {
				t.Errorf("an erased message still names %q", message.Author.UserID)
			}
			if !message.Deleted() {
				t.Error("an erased message does not report as withdrawn")
			}
			continue
		}

		// Somebody else's message is untouched, which is the whole point.
		if message.Body != "understood" {
			t.Errorf("another person's message became %q", message.Body)
		}
		if message.Author.UserID != bruno.UserID {
			t.Errorf("another person's message lost its author")
		}
	}
}

/*
TestErasingReachesMessagesAlreadyWithdrawn keeps a tombstone from being the one
place a link to the person survives.

A withdrawn message still names who wrote it, and that link is exactly what
erasure is for.
*/
func TestErasingReachesMessagesAlreadyWithdrawn(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	message, err := setup.service.Post(ctx, setup.first, room.ID, ana, "never mind")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Delete(ctx, setup.first, message.ID, ana); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := setup.service.Erase(ctx, setup.first, ana.UserID); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	withdrawn, err := setup.service.Get(ctx, setup.first, message.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if withdrawn.Author.UserID != "" {
		t.Errorf("a withdrawn message still names %q after erasure", withdrawn.Author.UserID)
	}
}

/*
TestErasingForgetsWhereSomebodyHadRead covers the second thing Convia holds
about a person in a conversation.

A read position names a person and a room they were in, which is a fact about
them, so it goes when they do.
*/
func TestErasingForgetsWhereSomebodyHadRead(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for index := 0; index < 3; index++ {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}
	if _, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 3); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}

	erased, err := setup.service.Erase(ctx, setup.first, bruno.UserID)
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if erased.ReadPositions != 1 {
		t.Errorf("Erase() forgot %d read positions, want 1", erased.ReadPositions)
	}

	state, err := setup.service.ReadState(ctx, setup.first, room.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if state.Sequence != Unseen {
		t.Errorf("the erased reader is still at position %d", state.Sequence)
	}
}

/*
TestErasingDoesNotCrossTenants keeps one application from erasing another's
people, which would be a deletion nobody asked for and nobody could undo.
*/
func TestErasingDoesNotCrossTenants(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "ours"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	erased, err := setup.service.Erase(ctx, setup.second, ana.UserID)
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if erased.Messages != 0 {
		t.Errorf("another tenant erased %d of our messages", erased.Messages)
	}

	survived, err := setup.service.History(ctx, setup.first, room.ID, HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(survived.Messages) != 1 || survived.Messages[0].Body != "ours" {
		t.Errorf("the message did not survive another tenant's erasure: %+v", survived.Messages)
	}
}

/*
TestNothingErasedIsWrittenToTheLog keeps the audit line from becoming the one
place the erasure did not reach.
*/
func TestNothingErasedIsWrittenToTheLog(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	const secret = "the merger closes on tuesday"
	if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, secret); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	setup.logs.Reset()
	if _, err := setup.service.Erase(ctx, setup.first, ana.UserID); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	written := setup.logs.String()
	if strings.Contains(written, secret) {
		t.Errorf("the erasure log carries what was erased:\n%s", written)
	}
	if !strings.Contains(written, "messages.erased") {
		t.Errorf("the erasure was not recorded:\n%s", written)
	}
}

/*
TestAnErasedMessageCannotBeChanged keeps erasure terminal.

Nobody is left to be its author, so the author check can never pass — and the
deletion check refuses it first, which is the answer that says why.
*/
func TestAnErasedMessageCannotBeChanged(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	message, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Erase(ctx, setup.first, ana.UserID); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	if _, err := setup.service.Edit(ctx, setup.first, message.ID, ana, "back again"); !errors.Is(err, ErrDeleted) {
		t.Errorf("editing an erased message error = %v, want %v", err, ErrDeleted)
	}
}
