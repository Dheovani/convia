package peers_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

/*
Two installations, against each other, over HTTP and nothing else.

This is `M33-010`, and it is the journeys that were checked by hand while
`M18-023`, `M33-001`, `M33-002` and `M33-007` were built — retyped into a
terminal once per change, which is the sort of proof that stops happening on the
change where it would have mattered.

**It drives the public surface only.** No store, no database, no package under
test: two addresses and a token each, which is all a third party would have. So
it runs against anything that answers as a Convia, including two installations
somebody else built, and it keeps working when the reference client leaves this
repository ([ADR 0021](../../docs/adr/0021-the-reference-client-is-one-client-among-many.md)).

The two are deliberately not symmetrical. **Only B has a media plane**, which is
how joining a call in B's room proves the credential is the home's: A could not
have issued one.
*/

const (
	firstEnvironment  = "CONVIA_TEST_FEDERATION_A"
	secondEnvironment = "CONVIA_TEST_FEDERATION_B"

	/*
		federationWaitFor bounds every wait, so a broken expectation fails
		rather than hangs.

		It is longer than it looks like it needs to be, and deliberately: a
		visitor's installation follows a home over a connection it opens
		itself, and `following.go` leaves a home that did not answer alone for
		`backoff(attempt)` -- one second doubling to a ceiling of a minute. A
		budget shorter than that ceiling can run out while the system is
		behaving exactly as designed, which is a test failing for a reason that
		is not a defect.

		Three failed attempts already put the next one past twenty seconds.
		On an idle machine none of them fail and the event arrives in
		milliseconds; on a loaded one they can, which is where this was found.
	*/
	federationWaitFor = 75 * time.Second
)

// installation is one Convia, reached the way any client reaches one.
type installation struct {
	t       *testing.T
	address string
}

// person is somebody signed in to one installation.
type person struct {
	installation
	token    string
	handle   string
	username string
	userID   string
}

func federation(t *testing.T) (installation, installation) {
	t.Helper()

	first := strings.TrimSpace(os.Getenv(firstEnvironment))
	second := strings.TrimSpace(os.Getenv(secondEnvironment))
	if first == "" || second == "" {
		t.Skipf("set %s and %s to the addresses of two running installations", firstEnvironment, secondEnvironment)
	}
	return installation{t: t, address: first}, installation{t: t, address: second}
}

// named makes a name no other run of this test will have chosen.
func named(prefix string) string {
	return prefix + strings.ToLower(rand.Text()[:10])
}

func (convia installation) do(token, method, path string, body any) (int, []byte) {
	convia.t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			convia.t.Fatalf("encode the request: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), federationWaitFor)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, convia.address+path, payload)
	if err != nil {
		convia.t.Fatalf("build the request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		convia.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	answered, err := io.ReadAll(response.Body)
	if err != nil {
		convia.t.Fatalf("read the answer to %s %s: %v", method, path, err)
	}
	return response.StatusCode, answered
}

// expect makes one request and insists on a status, decoding the answer.
func (convia installation) expect(token, method, path string, body any, want int, into any) {
	convia.t.Helper()

	status, answered := convia.do(token, method, path, body)
	if status != want {
		convia.t.Fatalf("%s %s answered %d, want %d: %s", method, path, status, want, answered)
	}
	if into != nil {
		if err := json.Unmarshal(answered, into); err != nil {
			convia.t.Fatalf("decode the answer to %s %s: %v: %s", method, path, err, answered)
		}
	}
}

// joins registers somebody new, which is how an installation gets a person
// without an operator or a fixture.
func (convia installation) joins(prefix string) person {
	convia.t.Helper()

	username := named(prefix)
	var answered struct {
		Token  string `json:"token"`
		Handle string `json:"handle"`
		UserID string `json:"user_id"`
	}
	convia.expect("", http.MethodPost, "/v1/accounts",
		map[string]string{"username": username, "password": "correct horse battery staple 2026"},
		http.StatusCreated, &answered)

	return person{installation: convia, token: answered.Token, handle: answered.Handle,
		username: username, userID: answered.UserID}
}

// opens makes a room, which is what somebody invites another person into.
func (who person) opens(name string) string {
	who.t.Helper()

	var room struct {
		ID string `json:"id"`
	}
	who.expect(who.token, http.MethodPost, "/v1/me/rooms", map[string]string{"name": name},
		http.StatusCreated, &room)
	return room.ID
}

// invites makes an invitation into a room here and answers the link to send.
func (who person) invites(roomID, handle string) string {
	who.t.Helper()

	var invitation struct {
		Link string `json:"link"`
	}
	who.expect(who.token, http.MethodPost, "/v1/me/rooms/"+roomID+"/invitations",
		map[string]string{"handle": handle}, http.StatusCreated, &invitation)
	return invitation.Link
}

// remoteRoom is a room elsewhere, as this person's own installation reports it.
type remoteRoom struct {
	ID     string `json:"id"`
	Home   string `json:"home"`
	RoomID string `json:"room_id"`
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Unread *int64 `json:"unread"`
	Call   *struct {
		ID string `json:"id"`
	} `json:"call"`
}

// accepts follows a link, which is how somebody becomes a visitor.
func (who person) accepts(link string) remoteRoom {
	who.t.Helper()

	var joined struct {
		Remote remoteRoom `json:"remote_room"`
	}
	who.expect(who.token, http.MethodPost, "/v1/me/remote-rooms",
		map[string]string{"link": link}, http.StatusCreated, &joined)
	return joined.Remote
}

// elsewhere is the rooms this person is in on other installations.
func (who person) elsewhere() []remoteRoom {
	who.t.Helper()

	var page struct {
		Data []remoteRoom `json:"data"`
	}
	who.expect(who.token, http.MethodGet, "/v1/me/remote-rooms", nil, http.StatusOK, &page)
	return page.Data
}

// says posts a message in a room on this person's own installation.
func (who person) says(roomID, what string) {
	who.t.Helper()
	who.expect(who.token, http.MethodPost, "/v1/me/rooms/"+roomID+"/messages",
		map[string]string{"body": what}, http.StatusCreated, nil)
}

/*
watching opens this person's event stream and answers what arrives on it.

It is the interface's own stream, reached with the header a client that is not a
browser uses. What a home says about a room elsewhere reaches it through the
installation this person signed in to, translated on the way.
*/
func (who person) watching(ctx context.Context) <-chan map[string]any {
	who.t.Helper()

	address := strings.Replace(strings.Replace(who.address, "https://", "wss://", 1), "http://", "ws://", 1)
	connection, _, err := websocket.Dial(ctx, address+"/v1/me/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + who.token}},
	})
	if err != nil {
		who.t.Fatalf("open the event stream: %v", err)
	}
	who.t.Cleanup(func() { connection.CloseNow() })

	arriving := make(chan map[string]any, 32)
	go func() {
		defer close(arriving)
		for {
			var event map[string]any
			if err := wsjson.Read(ctx, connection, &event); err != nil {
				return
			}
			select {
			case arriving <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return arriving
}

// awaits reads the stream until one event satisfies what is being waited for.
func awaits(t *testing.T, arriving <-chan map[string]any, what string, matches func(map[string]any) bool) map[string]any {
	t.Helper()

	deadline := time.After(federationWaitFor)
	for {
		select {
		case event, open := <-arriving:
			if !open {
				t.Fatalf("the stream closed before %s", what)
			}
			if matches(event) {
				return event
			}
		case <-deadline:
			t.Fatalf("nothing on the stream was %s", what)
		}
	}
}

func typed(event map[string]any, kind string) bool {
	name, _ := event["type"].(string)
	return name == kind
}

func subject(event map[string]any) string {
	about, _ := event["subject"].(map[string]any)
	id, _ := about["id"].(string)
	return id
}

func inRoom(event map[string]any) string {
	data, _ := event["data"].(map[string]any)
	id, _ := data["room_id"].(string)
	return id
}

/*
TestTwoInstallationsShareARoom is the whole of what federation promises, driven
end to end: an invitation crosses, somebody joins from elsewhere, and what
happens at the home reaches them as it happens.
*/
func TestTwoInstallationsShareARoom(t *testing.T) {
	first, second := federation(t)

	bia := first.joins("bia")
	ana := second.joins("ana")
	room := ana.opens("Standup")

	pointer := bia.accepts(ana.invites(room, bia.handle))
	if pointer.RoomID != room || pointer.Home == "" {
		t.Fatalf("the pointer = %+v, want Ana's room at Ana's installation", pointer)
	}
	if pointer.ID == room {
		t.Error("the pointer uses the home's own identifier, which this installation does not own")
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	arriving := bia.watching(ctx)

	// Something said at the home, named by the pointer Bia's Convia keeps.
	ana.says(room, "Standup in five minutes.")
	said := awaits(t, arriving, "what was said in the room elsewhere", func(event map[string]any) bool {
		return typed(event, "message.posted") && inRoom(event) == pointer.ID
	})
	if _, carried := said["cursor"]; carried {
		t.Error("the home's cursor was passed on, which Bia's own Convia cannot resume from")
	}

	/*
		And something that happened to the room, which names it in the subject
		rather than in the data. Reading only one of the two places dropped
		every `room.*` event a home sent, and did so silently.
	*/
	ana.expect(ana.token, http.MethodPatch, "/v1/me/rooms/"+room,
		map[string]string{"name": "Standup, renamed"}, http.StatusOK, nil)
	awaits(t, arriving, "the room being renamed", func(event map[string]any) bool {
		return typed(event, "room.updated") && subject(event) == pointer.ID
	})

	// What the home says about the room, in the list Bia's Convia draws.
	rooms := bia.elsewhere()
	if len(rooms) != 1 {
		t.Fatalf("Bia is in %d rooms elsewhere, want 1", len(rooms))
	}
	if rooms[0].Unread == nil || *rooms[0].Unread != 1 {
		t.Errorf("unread = %v, want the one thing Ana said", rooms[0].Unread)
	}
}

/*
TestAVisitorJoinsACallAtItsHome is `M33-002`, and the asymmetry is the proof.

**Only the second installation has a media plane.** So a credential to connect
with can only have come from the home, and the address in it can only be the
home's.
*/
func TestAVisitorJoinsACallAtItsHome(t *testing.T) {
	first, second := federation(t)

	bia := first.joins("bia")
	ana := second.joins("ana")
	room := ana.opens("A room with a call in it")
	pointer := bia.accepts(ana.invites(room, bia.handle))

	var seat struct {
		CallID     string `json:"call_id"`
		MediaURL   string `json:"media_url"`
		MediaToken string `json:"media_token"`
	}
	bia.expect(bia.token, http.MethodPost, "/v1/me/remote-rooms/"+pointer.ID+"/call/join",
		nil, http.StatusCreated, &seat)

	if seat.MediaURL == "" || seat.MediaToken == "" {
		t.Fatalf("joining answered %+v, want the home's media address and a credential", seat)
	}

	// The first installation has no media plane, so it cannot have issued this.
	var refused struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	ownRoom := bia.opens("A room on an installation with no media")
	bia.expect(bia.token, http.MethodPost, "/v1/me/rooms/"+ownRoom+"/call/join",
		nil, http.StatusServiceUnavailable, &refused)
	if refused.Error.Code != "unavailable" {
		t.Fatalf("the installation without a media plane answered %q, want it unavailable", refused.Error.Code)
	}

	// And the home has the visitor in its own call.
	var roster struct {
		Data []struct {
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		} `json:"data"`
	}
	ana.expect(ana.token, http.MethodGet, "/v1/me/rooms/"+room+"/call/participants",
		nil, http.StatusOK, &roster)
	if len(roster.Data) != 1 || !strings.HasPrefix(roster.Data[0].DisplayName, bia.username) {
		t.Fatalf("the home's roster = %+v, want the visitor in it", roster.Data)
	}
	if roster.Data[0].Role != "member" {
		t.Errorf("the visitor joined as %q: moderation is the home's, and a visitor is not its owner",
			roster.Data[0].Role)
	}

	// The room's own list says a call is running there, which is how the button
	// knows before anybody presses it.
	rooms := bia.elsewhere()
	if len(rooms) != 1 || rooms[0].Call == nil || rooms[0].Call.ID != seat.CallID {
		t.Errorf("the room elsewhere = %+v, want it holding the call just started", rooms)
	}

	bia.expect(bia.token, http.MethodPost, "/v1/me/remote-rooms/"+pointer.ID+"/call/leave",
		nil, http.StatusNoContent, nil)
}

/*
TestBeingTakenOutOfARoomElsewhere is `M33-007`.

A home decides who is in its rooms. Without acting on what it says, the room
would sit in the visitor's list answering `404` to everything they tried.
*/
func TestBeingTakenOutOfARoomElsewhere(t *testing.T) {
	first, second := federation(t)

	bia := first.joins("bia")
	ana := second.joins("ana")
	room := ana.opens("A room to be put out of")
	pointer := bia.accepts(ana.invites(room, bia.handle))

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	arriving := bia.watching(ctx)

	ana.expect(ana.token, http.MethodDelete, "/v1/me/rooms/"+room+"/members/"+pointer.UserID,
		nil, http.StatusNoContent, nil)

	awaits(t, arriving, "the home saying Bia is out", func(event map[string]any) bool {
		return typed(event, "room.member_removed") && subject(event) == pointer.ID
	})

	// The pointer goes with it, rather than waiting to be forgotten by hand.
	deadline := time.After(federationWaitFor)
	for {
		if len(bia.elsewhere()) == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the room stayed in Bia's list after the home took her out of it")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

/*
TestARoomAVisitorIsNotInStaysPrivate is the property everything else rests on.

A visitor's stream at a home covers the rooms they are in, and nothing else. It
is the one failure here that would be silent: everything would still work, and
somebody would be reading a conversation they were never in.
*/
func TestARoomAVisitorIsNotInStaysPrivate(t *testing.T) {
	first, second := federation(t)

	bia := first.joins("bia")
	ana := second.joins("ana")
	shared := ana.opens("The room they share")
	pointer := bia.accepts(ana.invites(shared, bia.handle))
	private := ana.opens("The room Ana keeps to herself")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	arriving := bia.watching(ctx)

	ana.says(private, "Nothing Bia should ever see.")
	ana.says(shared, "This one is for both of us.")

	/*
		The shared room's message is the fence. Anything about the private room
		would have to arrive before it, since it was said first, so reading
		until the expected one arrives is what proves the other did not.
	*/
	awaits(t, arriving, "what was said in the room they share", func(event map[string]any) bool {
		if typed(event, "message.posted") && inRoom(event) != pointer.ID {
			t.Errorf("a message about %v arrived, and Bia is not in that room", inRoom(event))
		}
		return typed(event, "message.posted") && inRoom(event) == pointer.ID
	})

	status, answered := bia.do(bia.token, http.MethodGet,
		fmt.Sprintf("/v1/me/remote-rooms/%s/messages", pointer.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("reading the shared room answered %d: %s", status, answered)
	}
	if bytes.Contains(answered, []byte("Nothing Bia should ever see")) {
		t.Error("the private room's conversation reached the visitor")
	}
}
