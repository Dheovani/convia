package messages

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"convia/internal/events"
)

/*
TestEachChangeIsAnnouncedOnce is what a chat client's fan-out depends on.

Since M16 an application's backend is several processes, and the one that
handled a post is almost never the one holding the socket of the person who
needs to see it. Without these events every other connected person waits for a
poll, which is the failure the event stream exists to prevent.
*/
func TestEachChangeIsAnnouncedOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	message, err := setup.service.Post(ctx, setup.first, room.ID, author, "hello")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Edit(ctx, setup.first, message.ID, author, "hello again"); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if _, err := setup.service.Delete(ctx, setup.first, message.ID, author); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	announced := setup.published.all()
	want := []events.Type{events.MessagePosted, events.MessageEdited, events.MessageDeleted}

	if len(announced) != len(want) {
		t.Fatalf("%d events were announced, want %d: %+v", len(announced), len(want), announced)
	}

	for index, kind := range want {
		event := announced[index]
		if event.Type != kind {
			t.Errorf("event %d is %q, want %q", index, event.Type, kind)
		}
		if event.Subject.Type != events.SubjectMessage || event.Subject.ID != message.ID {
			t.Errorf("event %d is about %+v, want the message", index, event.Subject)
		}
		if event.ApplicationID != setup.first {
			t.Errorf("event %d belongs to %q", index, event.ApplicationID)
		}
		if event.Data["room_id"] != room.ID {
			t.Errorf("event %d does not name the room: %v", index, event.Data)
		}
		if event.Data["sequence"] != message.Sequence {
			t.Errorf("event %d carries sequence %v, want %d", index, event.Data["sequence"], message.Sequence)
		}
	}
}

/*
TestNothingSaidLeavesInAnEvent is the rule that makes these events safe to
deliver by webhook at all.

An event reaches every destination an application registered, which are HTTP
endpoints outside Convia. Copying what people wrote into them would send private
conversations to third parties as a side effect of subscribing to a roster.
A subscriber learns that a message exists and reads it back through the API,
where the scope it holds is checked.

It is also what keeps a retried delivery honest: an event that arrives late says
only that a message changed, so re-reading yields the current text rather than
an older one the subscriber would write over the newer.
*/
func TestNothingSaidLeavesInAnEvent(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	const secret = "the merger closes on tuesday"

	message, err := setup.service.Post(ctx, setup.first, room.ID, author, secret)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Edit(ctx, setup.first, message.ID, author, secret+", revised"); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}

	for _, event := range setup.published.all() {
		/*
			Serialized rather than inspected field by field. A future field
			holding the body under another name would pass a field check and
			fail this one, which is the point: the assertion is about what
			leaves Convia, not about the fields somebody remembered to look at.
		*/
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(string(encoded), secret) {
			t.Errorf("a %s event carries what was said: %s", event.Type, encoded)
		}
	}
}

/*
TestAMessageEventIsDeliverableByWebhook records the decision, so that changing
it takes changing a test that says why.

Presence is the one type Convia refuses to deliver durably, because a redelivered
presence report arrives after it stopped being true. A message does not have that
problem: it is a fact that happened, it does not expire, and the event carries no
text that could be stale.
*/
func TestAMessageEventIsDeliverableByWebhook(t *testing.T) {
	for _, kind := range []events.Type{events.MessagePosted, events.MessageEdited, events.MessageDeleted} {
		if !events.Durable(kind) {
			t.Errorf("%s is refused by webhook, which is the presence rule applied where it does not hold", kind)
		}
	}
}
