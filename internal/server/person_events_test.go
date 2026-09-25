package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/api"
	"convia/internal/events"
	"convia/internal/events/serving"
	"convia/internal/sessions"
)

// servingPeople starts the whole routed server with a person's stream on a
// broker the test can publish into.
func servingPeople(t *testing.T, broker *events.Broker) *httptest.Server {
	t.Helper()

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.PersonalEvents = serving.NewPersonHandler(discard, broker, nil,
		stubSessionAuthenticator{principal: samplePerson()}, stubVisitors{}, nil,
		stubRooms{room: sampleRoom(), member: sampleMember()})

	server := httptest.NewServer(New("127.0.0.1:0", discard, dependencies).Handler)
	t.Cleanup(server.Close)
	return server
}

// dialPersonStream opens the stream as a browser on a page of some origin.
func dialPersonStream(t *testing.T, server *httptest.Server, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	header := http.Header{"Cookie": []string{sessions.CookieName + "=cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"}}
	if origin != "" {
		header.Set("Origin", origin)
	}

	connection, response, err := websocket.Dial(ctx,
		strings.Replace(server.URL, "http://", "ws://", 1)+api.Prefix+"/me/events",
		&websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		t.Cleanup(func() { connection.CloseNow() })
	}
	return connection, response, err
}

/*
TestAPersonsStreamSurvivesTheMiddlewareChain is the tenant stream's test for the
surface that wraps more: authentication by cookie, and the origin check, both
between the listener and a handler that has to take the connection over.
*/
func TestAPersonsStreamSurvivesTheMiddlewareChain(t *testing.T) {
	broker := events.NewBroker()
	server := servingPeople(t, broker)

	connection, _, err := dialPersonStream(t, server, server.URL)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	broker.Publish(events.New(events.MessagePosted, sampleApplication().ID, sampleMessage().ID, "req_1",
		events.Data{"room_id": sampleRoom().ID, "sequence": int64(1)}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var received events.Event
	if err := wsjson.Read(ctx, connection, &received); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if received.Subject.ID != sampleMessage().ID {
		t.Errorf("the person received an event about %q", received.Subject.ID)
	}
}

/*
TestAHandshakeFromAnotherPageIsRefused is cross-site WebSocket hijacking, closed
by the check every state-changing request on this surface already passes.

A handshake is a GET, and every other GET here is exempt. This one is not,
because what it opens goes on carrying somebody's conversations: a page on a
sibling subdomain that could open it would be reading them as they happen, and
SameSite does not see a sibling.
*/
func TestAHandshakeFromAnotherPageIsRefused(t *testing.T) {
	for name, origin := range map[string]string{
		"a sibling subdomain": "http://docs.example.com",
		"another host":        "https://attacker.test",
		"absent":              "",
	} {
		t.Run(name, func(t *testing.T) {
			server := servingPeople(t, events.NewBroker())

			_, response, err := dialPersonStream(t, server, origin)
			if err == nil {
				t.Fatal("a handshake from another page was upgraded")
			}
			if response == nil {
				t.Fatalf("Dial() error = %v, with no HTTP answer to inspect", err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
			}
		})
	}
}

// TestAnOrdinaryReadStillNeedsNoOrigin keeps the exemption for everything that
// is not a handshake, which a check on the method alone would have removed.
func TestAnOrdinaryReadStillNeedsNoOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, api.Prefix+"/me", nil)
	request = asPerson(request)
	request.Header.Del("Origin")
	request.Header.Set("Upgrade", "h2c")

	if response := serveBrowser(request); response.Code != http.StatusOK {
		t.Errorf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
}
