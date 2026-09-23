package peers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/accounts"
	"convia/internal/sessions"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const sampleRemoteID = "rrm_7KQZP4XN2VJH6TBWMDR3YAFC5E"

// recordingService answers the session routes and remembers what it relayed.
type recordingService struct {
	answer Response
	err    error

	relayed bool
	target  string
	body    []byte
	forgot  bool
}

func (service *recordingService) Invite(context.Context, sessions.Principal, string, string) (Invitation, error) {
	return Invitation{ID: sampleInvitationID, InviteeAccountID: "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E",
		InviteeUsername: "bia", ExpiresAt: time.Now().Add(InvitationLifetime)}, service.err
}

func (service *recordingService) Revoke(context.Context, sessions.Principal, string) error {
	return service.err
}

func (service *recordingService) Pending(ctx context.Context, principal sessions.Principal,
	roomID string) ([]Invitation, error) {
	invitation, err := service.Invite(ctx, principal, roomID, "")
	return []Invitation{invitation}, err
}

func (service *recordingService) Look(context.Context, string, sessions.Principal, accounts.Identity, string) (Link, Preview, error) {
	return Link{}, Preview{}, service.err
}

func (service *recordingService) Join(context.Context, string, accounts.Account, accounts.Identity, string) (Joined, error) {
	return Joined{}, service.err
}

func (service *recordingService) RemoteRooms(context.Context, string) ([]RemoteRoom, error) {
	return nil, service.err
}

func (service *recordingService) RemoteRoom(context.Context, string, string) (RemoteRoom, error) {
	return RemoteRoom{ID: sampleRemoteID, Home: "https://convia.example", RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}, nil
}

func (service *recordingService) Relay(_ context.Context, _ accounts.Identity, _ RemoteRoom, _, target string,
	body []byte) (Response, error) {
	service.relayed, service.target, service.body = true, target, body
	return service.answer, service.err
}

func (service *recordingService) Leave(context.Context, accounts.Identity, RemoteRoom) error {
	return service.err
}

func (service *recordingService) Forget(context.Context, RemoteRoom) error {
	service.forgot = true
	return service.err
}

type openIdentities struct{}

func (openIdentities) Identity(context.Context, string) (accounts.Identity, error) {
	return accounts.NewIdentity()
}

func (openIdentities) Account(context.Context, string) (accounts.Account, error) {
	return accounts.Account{ID: "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", Username: "ana"}, nil
}

// asPerson builds a request as the session middleware would have left it.
func asPerson(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: "cvs_token"})
	request.SetPathValue("remote_room_id", sampleRemoteID)
	return request.WithContext(sessions.ContextWithPrincipal(request.Context(), sessions.Principal{
		SessionID: "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E", AccountID: "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		UserID: "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E", ApplicationID: "app_CONVIAAAAAAAAAAAAAAAAAAAAA",
	}))
}

/*
TestPendingLinksNameTheAddressTheListWasReadAt covers a list read without an
Origin, which a page sends on no GET: the links name the address the request
reached, through the proxy that terminated it.
*/
func TestPendingLinksNameTheAddressTheListWasReadAt(t *testing.T) {
	handler := NewSessionHandler(quiet(), &recordingService{}, openIdentities{})

	request := asPerson(http.MethodGet, "/v1/me/rooms/room_7KQZP4XN2VJH6TBWMDR3YAFC5E/invitations", "")
	request.Host = "convia.example"
	request.Header.Del("Origin")
	request.Header.Set("X-Forwarded-Proto", "https")

	response := httptest.NewRecorder()
	handler.Pending(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if want := `"link":"https://convia.example/invitations/` + sampleInvitationID + `"`; !strings.Contains(response.Body.String(), want) {
		t.Errorf("body = %s, want a link like %s", response.Body, want)
	}
}

/*
TestAHomesRefusalNeverSignsSomebodyOut is the translation that matters most.

The page returns to the sign-in form on any 401. A home that stopped recognizing
somebody has said nothing about their session on their own installation, so
passing its 401 along would sign them out of the wrong Convia.
*/
func TestAHomesRefusalNeverSignsSomebodyOut(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		service := &recordingService{answer: Response{Status: status, Code: "unauthenticated"}}
		handler := NewSessionHandler(quiet(), service, openIdentities{})

		response := httptest.NewRecorder()
		handler.History(response, asPerson(http.MethodGet, "/v1/me/remote-rooms/x/messages", ""))

		if response.Code != http.StatusForbidden {
			t.Errorf("a home's %d became %d, want %d", status, response.Code, http.StatusForbidden)
		}
	}
}

func TestAnUnreachableHomeIsUnavailable(t *testing.T) {
	service := &recordingService{err: ErrUnreachable}
	handler := NewSessionHandler(quiet(), service, openIdentities{})

	response := httptest.NewRecorder()
	handler.History(response, asPerson(http.MethodGet, "/v1/me/remote-rooms/x/messages", ""))

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestForgettingARoomAsksNothingOfItsHome: forgetting is for a home that does
// not answer, so it needs neither the home nor the key that talks to it.
func TestForgettingARoomAsksNothingOfItsHome(t *testing.T) {
	service := &recordingService{}
	handler := NewSessionHandler(quiet(), service, nil)

	response := httptest.NewRecorder()
	handler.Forget(response, asPerson(http.MethodDelete, "/v1/me/remote-rooms/"+sampleRemoteID, ""))

	if response.Code != http.StatusNoContent || !service.forgot {
		t.Errorf("Forget() = %d, forgot %v, want %d and the pointer dropped",
			response.Code, service.forgot, http.StatusNoContent)
	}
	if service.relayed {
		t.Error("forgetting a room sent a request to its home")
	}
}

// TestOnlyAMessageIdentifierReachesTheHomesAddress keeps a path value from
// steering the request somewhere else on the home.
func TestOnlyAMessageIdentifierReachesTheHomesAddress(t *testing.T) {
	for _, id := range []string{"../../users", "msg_short", "msg_7KQZP4XN2VJH6TBWMDR3YAFC5E/../x"} {
		service := &recordingService{answer: Response{Status: http.StatusOK, Body: []byte(`{}`)}}
		handler := NewSessionHandler(quiet(), service, openIdentities{})

		request := asPerson(http.MethodPost, "/v1/me/remote-rooms/x/messages/x/delete", "")
		request.SetPathValue("message_id", id)
		response := httptest.NewRecorder()
		handler.Withdraw(response, request)

		if service.relayed {
			t.Errorf("message identifier %q was relayed to %q", id, service.target)
		}
		if response.Code != http.StatusNotFound {
			t.Errorf("message identifier %q answered %d, want %d", id, response.Code, http.StatusNotFound)
		}
	}

	service := &recordingService{answer: Response{Status: http.StatusOK, Body: []byte(`{}`)}}
	request := asPerson(http.MethodPost, "/v1/me/remote-rooms/x/messages/x/delete", "")
	request.SetPathValue("message_id", "msg_7KQZP4XN2VJH6TBWMDR3YAFC5E")
	NewSessionHandler(quiet(), service, openIdentities{}).Withdraw(httptest.NewRecorder(), request)
	if service.target != "/v1/peer/messages/msg_7KQZP4XN2VJH6TBWMDR3YAFC5E/delete" {
		t.Errorf("a valid message was relayed to %q", service.target)
	}
}

// TestARelayCarriesOnlyWhatThisInstallationUnderstood re-encodes bodies and
// forwards only the query parameters the route has.
func TestARelayCarriesOnlyWhatThisInstallationUnderstood(t *testing.T) {
	service := &recordingService{answer: Response{Status: http.StatusOK, Body: []byte(`{"data":[]}`)}}
	handler := NewSessionHandler(quiet(), service, openIdentities{})

	handler.History(httptest.NewRecorder(), asPerson(http.MethodGet,
		"/v1/me/remote-rooms/x/messages?limit=5&cursor=3&direction=newer&user_id=usr_OTHER", ""))
	if service.target != "/v1/peer/rooms/room_7KQZP4XN2VJH6TBWMDR3YAFC5E/messages?cursor=3&direction=newer&limit=5" {
		t.Errorf("history was relayed to %q", service.target)
	}

	response := httptest.NewRecorder()
	handler.Post(response, asPerson(http.MethodPost, "/v1/me/remote-rooms/x/messages", `{"body":"hello","user_id":"usr_OTHER"}`))
	if response.Code != http.StatusBadRequest {
		t.Errorf("a body naming an author answered %d, want %d: it must be refused here, not relayed", response.Code, http.StatusBadRequest)
	}

	handler.Post(httptest.NewRecorder(), asPerson(http.MethodPost, "/v1/me/remote-rooms/x/messages", `{"body":"hello"}`))
	if string(service.body) != `{"body":"hello"}` {
		t.Errorf("the relayed body is %s", service.body)
	}
}

// TestAnInvitationLinkNamesWhereItWasMadeFrom, which is the address the
// inviting person reached their Convia at.
func TestAnInvitationLinkNamesWhereItWasMadeFrom(t *testing.T) {
	handler := NewSessionHandler(quiet(), &recordingService{}, openIdentities{})

	request := asPerson(http.MethodPost, "/v1/me/rooms/x/invitations", `{"handle":"bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH"}`)
	request.Header.Set("Origin", "http://192.168.1.10:8080")
	response := httptest.NewRecorder()
	handler.Invite(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	if want := `"link":"http://192.168.1.10:8080/invitations/` + sampleInvitationID + `"`; !strings.Contains(response.Body.String(), want) {
		t.Errorf("the response %s does not carry %s", response.Body, want)
	}
}

func TestAnInvitationMadeWithoutAnOriginStillNamesSomewhere(t *testing.T) {
	handler := NewSessionHandler(quiet(), &recordingService{}, openIdentities{})

	request := asPerson(http.MethodPost, "/v1/me/rooms/x/invitations", `{"handle":"bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH"}`)
	request.Host = "convia.example"
	request.Header.Del("Origin")
	request.Header.Set("X-Forwarded-Proto", "https")

	response := httptest.NewRecorder()
	handler.Invite(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	if want := `"link":"https://convia.example/invitations/` + sampleInvitationID + `"`; !strings.Contains(response.Body.String(), want) {
		t.Errorf("the response %s does not carry %s", response.Body, want)
	}
}

func TestAnInvitationPrefersTheOriginWhenThereIsOne(t *testing.T) {
	handler := NewSessionHandler(quiet(), &recordingService{}, openIdentities{})

	request := asPerson(http.MethodPost, "/v1/me/rooms/x/invitations", `{"handle":"bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH"}`)
	request.Host = "behind-the-proxy.internal"
	request.Header.Set("Origin", "https://convia.example")

	response := httptest.NewRecorder()
	handler.Invite(response, request)

	if want := `"link":"https://convia.example/invitations/`; !strings.Contains(response.Body.String(), want) {
		t.Errorf("the response %s does not name the address the browser used", response.Body)
	}
}
