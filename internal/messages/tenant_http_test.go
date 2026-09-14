package messages

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/credentials"
)

/*
stubService stands in for the message service.

Transport and authorization tests need to control what a handler receives
without PostgreSQL; whether the domain rules hold is settled by this package's
own domain and integration tests.
*/
type stubService struct {
	message   Message
	page      Page
	readState ReadState
	unread    map[string]int64
	err       error

	// seen records what the layer above passed down, so a test can assert the
	// handler read the request rather than merely answered it.
	seenRoomID   string
	seenID       string
	seenAuthor   Author
	seenBody     string
	seenOptions  HistoryOptions
	seenReader   string
	seenSequence int64
	seenRooms    []string
}

func (stub *stubService) Post(_ context.Context, _, roomID string, author Author, body string) (Message, error) {
	stub.seenRoomID, stub.seenAuthor, stub.seenBody = roomID, author, body
	return stub.message, stub.err
}

func (stub *stubService) Get(_ context.Context, _, id string) (Message, error) {
	stub.seenID = id
	return stub.message, stub.err
}

func (stub *stubService) History(_ context.Context, _, roomID string, options HistoryOptions) (Page, error) {
	stub.seenRoomID, stub.seenOptions = roomID, options
	return stub.page, stub.err
}

func (stub *stubService) Edit(_ context.Context, _, id string, author Author, body string) (Message, error) {
	stub.seenID, stub.seenAuthor, stub.seenBody = id, author, body
	return stub.message, stub.err
}

func (stub *stubService) Delete(_ context.Context, _, id string, author Author) (Message, error) {
	stub.seenID, stub.seenAuthor = id, author
	return stub.message, stub.err
}

func (stub *stubService) Remove(_ context.Context, _, id string) (Message, error) {
	stub.seenID = id
	return stub.message, stub.err
}

func (stub *stubService) MarkRead(_ context.Context, _, roomID, userID string, sequence int64) (ReadState, error) {
	stub.seenRoomID, stub.seenReader, stub.seenSequence = roomID, userID, sequence
	return stub.readState, stub.err
}

func (stub *stubService) ReadState(_ context.Context, _, roomID, userID string) (ReadState, error) {
	stub.seenRoomID, stub.seenReader = roomID, userID
	return stub.readState, stub.err
}

func (stub *stubService) UnreadByRoom(_ context.Context, _, userID string, roomIDs []string) (map[string]int64, error) {
	stub.seenReader, stub.seenRooms = userID, roomIDs
	return stub.unread, stub.err
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func principalWith(scopes ...credentials.Scope) credentials.Principal {
	return credentials.Principal{
		ApplicationID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		CredentialID:  "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		Scopes:        scopes,
	}
}

// serve runs one request through the handler with a verified caller attached.
func serve(t *testing.T, stub *stubService, principal credentials.Principal,
	method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(), principal))

	// The routes carry path values, which httptest does not fill in.
	if strings.Contains(target, "/rooms/") {
		request.SetPathValue("room_id", "room_7KQZP4XN2VJH6TBWMDR3YAFC5E")
	}
	if strings.Contains(target, "/messages/") {
		request.SetPathValue("message_id", "msg_6TBWNDR3YAFC5E7QK4XMZP2VJH")
	}

	response := httptest.NewRecorder()
	handler := NewTenantHandler(quiet(), stub)

	switch {
	case method == http.MethodPost && strings.HasSuffix(target, "/delete"):
		handler.Delete(response, request)
	case method == http.MethodPost:
		handler.Post(response, request)
	case method == http.MethodPatch:
		handler.Edit(response, request)
	case strings.Contains(target, "/rooms/"):
		handler.History(response, request)
	default:
		handler.Get(response, request)
	}
	return response
}

func sample() Message {
	at := time.Date(2026, time.September, 11, 14, 2, 31, 114_523_000, time.UTC)

	return Message{
		ID:            "msg_6TBWNDR3YAFC5E7QK4XMZP2VJH",
		ApplicationID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		RoomID:        "room_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		Sequence:      42,
		Author:        Author{UserID: "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"},
		Body:          "Standup in five minutes.",
		CreatedAt:     at,
	}
}

/*
TestEveryOperationNeedsItsScope walks the surface and checks that each one is
refused without the permission it requires.

The scopes are separate on purpose: a credential that reads a room's settings
is doing administration, while one that reads its history is reading people's
conversations. A test that only checked "some scope" would let the two collapse
into one.
*/
func TestEveryOperationNeedsItsScope(t *testing.T) {
	said := `{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E","body":"hello"}`
	author := `{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`

	cases := []struct {
		name     string
		method   string
		target   string
		body     string
		required credentials.Scope
	}{
		{"post", http.MethodPost, "/v1/rooms/room_1/messages", said, credentials.ScopeMessagesWrite},
		{"history", http.MethodGet, "/v1/rooms/room_1/messages", "", credentials.ScopeMessagesRead},
		{"get", http.MethodGet, "/v1/messages/msg_1", "", credentials.ScopeMessagesRead},
		{"edit", http.MethodPatch, "/v1/messages/msg_1", said, credentials.ScopeMessagesWrite},
		{"delete", http.MethodPost, "/v1/messages/msg_1/delete", author, credentials.ScopeMessagesWrite},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			/*
				Every scope except the one this operation needs, so the refusal
				can only be about the missing permission and not about the
				caller being unprivileged in general.
			*/
			var others []credentials.Scope
			for _, scope := range credentials.Scopes() {
				if scope != testCase.required {
					others = append(others, scope)
				}
			}

			refused := serve(t, &stubService{message: sample()}, principalWith(others...),
				testCase.method, testCase.target, testCase.body)
			if refused.Code != http.StatusForbidden {
				t.Errorf("without %s the status is %d, want %d",
					testCase.required, refused.Code, http.StatusForbidden)
			}

			allowed := serve(t, &stubService{message: sample()}, principalWith(testCase.required),
				testCase.method, testCase.target, testCase.body)
			if allowed.Code == http.StatusForbidden {
				t.Errorf("with %s the request was still refused: %s",
					testCase.required, allowed.Body)
			}
		})
	}
}

/*
TestWritingDoesNotGrantReading pins the separation the two scopes exist for.

A service that announces deployments into a room has no business reading what
people replied to them, and a credential shaped for that is a real thing an
operator should be able to issue.
*/
func TestWritingDoesNotGrantReading(t *testing.T) {
	writer := principalWith(credentials.ScopeMessagesWrite)

	posted := serve(t, &stubService{message: sample()}, writer,
		http.MethodPost, "/v1/rooms/room_1/messages",
		`{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E","body":"deploy finished"}`)
	if posted.Code != http.StatusCreated {
		t.Fatalf("a writer could not post: %d %s", posted.Code, posted.Body)
	}

	read := serve(t, &stubService{message: sample()}, writer,
		http.MethodGet, "/v1/rooms/room_1/messages", "")
	if read.Code != http.StatusForbidden {
		t.Errorf("a writer read the history: %d %s", read.Code, read.Body)
	}
}

/*
TestTheHandlerReadsTheWindowAClientAsked keeps the query string from being
accepted and then ignored, which would look to a client like a history that
refuses to page.
*/
func TestTheHandlerReadsTheWindowAClientAsked(t *testing.T) {
	stub := &stubService{page: Page{Messages: []Message{sample()}}}

	response := serve(t, stub, principalWith(credentials.ScopeMessagesRead),
		http.MethodGet, "/v1/rooms/room_1/messages?direction=newer&cursor=17&limit=5", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body)
	}

	if stub.seenOptions.Direction != Newer {
		t.Errorf("the direction reached the service as %q, want %q", stub.seenOptions.Direction, Newer)
	}
	if stub.seenOptions.After == nil || *stub.seenOptions.After != 17 {
		t.Errorf("the cursor reached the service as %v, want 17", stub.seenOptions.After)
	}
	if stub.seenOptions.Limit != 5 {
		t.Errorf("the limit reached the service as %d, want 5", stub.seenOptions.Limit)
	}
}

/*
TestAMalformedWindowIsReportedRatherThanIgnored keeps a client mistake from
silently becoming a different request.

A cursor that is not a sequence, read as "no cursor", would answer with the
newest messages — which is a plausible-looking page that is not the one the
client asked for, and the worst kind of wrong answer.
*/
func TestAMalformedWindowIsReportedRatherThanIgnored(t *testing.T) {
	for _, query := range []string{"?cursor=b3BhcXVl", "?direction=sideways", "?limit=many", "?cursor=-1"} {
		response := serve(t, &stubService{page: Page{}}, principalWith(credentials.ScopeMessagesRead),
			http.MethodGet, "/v1/rooms/room_1/messages"+query, "")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%q answered %d, want %d", query, response.Code, http.StatusBadRequest)
		}
	}
}

/*
TestTheAuthorReachesTheServiceFromTheBody is what makes the "only the author"
rule enforceable at all: the handler must pass down who the request named, and
not a default.
*/
func TestTheAuthorReachesTheServiceFromTheBody(t *testing.T) {
	stub := &stubService{message: sample()}

	serve(t, stub, principalWith(credentials.ScopeMessagesWrite),
		http.MethodPost, "/v1/rooms/room_1/messages",
		`{"invitation_id":"inv_4XZQP7KN2VJH6TBWMDR3YAFC5E","body":"hello from a guest"}`)

	if stub.seenAuthor.InvitationID != "inv_4XZQP7KN2VJH6TBWMDR3YAFC5E" {
		t.Errorf("the guest reached the service as %+v", stub.seenAuthor)
	}
	if !stub.seenAuthor.Guest() {
		t.Error("an invitation author did not reach the service as a guest")
	}

	stub = &stubService{message: sample()}
	serve(t, stub, principalWith(credentials.ScopeMessagesWrite),
		http.MethodPost, "/v1/messages/msg_1/delete",
		`{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`)

	if stub.seenAuthor.UserID != "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E" {
		t.Errorf("the withdrawing author reached the service as %+v", stub.seenAuthor)
	}
}

/*
TestADomainErrorBecomesTheAnswerItMeans keeps the transport from flattening
distinctions the caller needs in order to act.

ErrNotAuthor is the one worth spelling out: it is a conflict rather than a
forbidden, because the credential already carries everything this surface can
grant and asking for another scope would send the caller after something that
would not help.
*/
func TestADomainErrorBecomesTheAnswerItMeans(t *testing.T) {
	cases := map[error]int{
		ErrRoomNotFound: http.StatusNotFound,
		ErrNotFound:     http.StatusNotFound,
		ErrRoomClosed:   http.StatusConflict,
		ErrDeleted:      http.StatusConflict,
		ErrNotAuthor:    http.StatusConflict,
		ValidationError{Field: "body", Message: "The message must not be empty."}: http.StatusBadRequest,
		errors.New("the database fell over"):                                      http.StatusInternalServerError,
	}

	for domainError, want := range cases {
		response := serve(t, &stubService{err: domainError}, principalWith(credentials.ScopeMessagesWrite),
			http.MethodPost, "/v1/rooms/room_1/messages",
			`{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E","body":"hello"}`)

		if response.Code != want {
			t.Errorf("%v answered %d, want %d", domainError, response.Code, want)
		}
	}
}

/*
TestNothingSaidReachesAFailureBody keeps an internal error from quoting the
message that caused it back to the caller.
*/
func TestNothingSaidReachesAFailureBody(t *testing.T) {
	const secret = "the merger closes on tuesday"

	response := serve(t, &stubService{err: errors.New("write message: " + secret)},
		principalWith(credentials.ScopeMessagesWrite),
		http.MethodPost, "/v1/rooms/room_1/messages",
		`{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E","body":"`+secret+`"}`)

	if strings.Contains(response.Body.String(), secret) {
		t.Errorf("the failure body carries what was said: %s", response.Body)
	}
}

/*
TestATombstoneOmitsItsBody is the wire property a client renders a withdrawn
message from.
*/
func TestATombstoneOmitsItsBody(t *testing.T) {
	at := sample().CreatedAt.Add(time.Minute)

	tombstone := sample()
	tombstone.Body = ""
	tombstone.DeletedAt = &at

	response := serve(t, &stubService{message: tombstone}, principalWith(credentials.ScopeMessagesWrite),
		http.MethodPost, "/v1/messages/msg_1/delete", `{"user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`)

	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v: %s", err, response.Body)
	}

	if _, carries := decoded["body"]; carries {
		t.Errorf("the tombstone carries a body: %s", response.Body)
	}
	if decoded["deleted"] != true {
		t.Errorf("the tombstone does not say it was withdrawn: %s", response.Body)
	}
	if decoded["sequence"] != float64(sample().Sequence) {
		t.Errorf("the tombstone lost its place in the history: %s", response.Body)
	}
}
