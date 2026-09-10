package calls

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"convia/internal/events"
	"convia/internal/media"
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

/*
TestACallLifecycleIsAnnounced is the emission half of M14-001 for calls.

A conversation starting and ending is the frame everything else in a call hangs
on, and it is the one thing a client cannot discover from the media plane: the
media plane knows a room emptied, not that Convia considers the call over.
*/
func TestACallLifecycleIsAnnounced(t *testing.T) {
	setup := newFixture(t)
	stream := listenTo(t, setup, setup.first)

	call := setup.start(t, setup.first, setup.firstRoom)

	started := next(t, stream)
	if started.Type != events.CallStarted {
		t.Errorf("starting a call announced %q", started.Type)
	}
	if started.Subject.Type != events.SubjectCall || started.Subject.ID != call.ID {
		t.Errorf("the announcement is about %+v, not the call that started", started.Subject)
	}
	if started.Data["room_id"] != setup.firstRoom {
		t.Errorf("the announcement says the call is in room %v", started.Data["room_id"])
	}
	if started.Data["status"] != string(StatusActive) {
		t.Errorf("the announcement says the call is %v", started.Data["status"])
	}

	if _, err := setup.service.End(context.Background(), setup.first, call.ID,
		ActorApplication, "Finished."); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	ended := next(t, stream)
	if ended.Type != events.CallEnded {
		t.Errorf("ending a call announced %q", ended.Type)
	}
	if ended.Data["status"] != string(StatusEnded) {
		t.Errorf("the announcement says the call is %v", ended.Data["status"])
	}
}

/*
TestAnAnnouncementCarriesNothingTheApplicationWrote is the privacy rule the
audit trail already follows, checked where it is easiest to lose.

Call metadata and an end reason are both composed by the application and may
describe the people in the conversation. An event stream reaches every
subscriber at once, so it is the last place either should appear.
*/
func TestAnAnnouncementCarriesNothingTheApplicationWrote(t *testing.T) {
	setup := newFixture(t)
	stream := listenTo(t, setup, setup.first)

	call, err := setup.service.Start(context.Background(), setup.first, setup.firstRoom,
		Definition{Metadata: map[string]string{"subject": "ana-performance-review"}}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	started, err := json.Marshal(next(t, stream))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertSilentAbout(t, started, "ana-performance-review")

	if _, err := setup.service.End(context.Background(), setup.first, call.ID,
		ActorApplication, "Ana was upset."); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	ended, err := json.Marshal(next(t, stream))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertSilentAbout(t, ended, "Ana was upset")
}

/*
TestAnotherTenantIsNeverToldAboutThisOne checks isolation against real rows
rather than against constructed events.

The application on an event comes from the row it describes, so this is where a
mistake would actually be made.
*/
func TestAnotherTenantIsNeverToldAboutThisOne(t *testing.T) {
	setup := newFixture(t)
	stream := listenTo(t, setup, setup.second)

	setup.start(t, setup.first, setup.firstRoom)

	select {
	case event := <-stream.Events():
		t.Errorf("the second tenant was told about %s of %s", event.Type, event.ApplicationID)
	case <-time.After(50 * time.Millisecond):
	}
}

/*
TestACallThatNeverHappenedIsAnnouncedAsEnded covers the abandonment path.

A call whose media session could not be realized is ended rather than left
holding its room, and a subscriber that saw it start must be told it is over —
otherwise the stream would leave a conversation open that Convia has already
closed.
*/
func TestACallThatNeverHappenedIsAnnouncedAsEnded(t *testing.T) {
	setup := newFixtureWith(t, &scriptedMedia{openErr: fmt.Errorf("dial: %w", media.ErrUnavailable)})
	stream := listenTo(t, setup, setup.first)

	if _, err := setup.service.Start(context.Background(), setup.first, setup.firstRoom,
		Definition{}, ActorApplication); err == nil {
		t.Fatal("Start() succeeded with a media plane that refuses")
	}

	if started := next(t, stream).Type; started != events.CallStarted {
		t.Fatalf("the first announcement was %q", started)
	}
	if ended := next(t, stream); ended.Type != events.CallEnded {
		t.Errorf("an abandoned call announced %q rather than ending", ended.Type)
	}
}

// assertSilentAbout fails when application-composed text reached a subscriber.
func assertSilentAbout(t *testing.T, announcement []byte, text string) {
	t.Helper()

	if strings.Contains(string(announcement), text) {
		t.Errorf("an announcement carries application-composed text %q: %s", text, announcement)
	}
}
