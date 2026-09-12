package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/messages"
	"convia/internal/sessions"
)

/*
asPerson presents the session cookie a signed-in person's browser would send.

The value only has to have the right shape: the verifier in these fixtures is a
stub, and whether a real token authenticates is settled by the sessions package.
*/
func asPerson(request *http.Request) *http.Request {
	request.AddCookie(&http.Cookie{
		Name:  sessions.CookieName,
		Value: "cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
	})
	return request
}

// personalDependencies serves every route with one scripted pair of services.
func personalDependencies(service stubMessages, membership stubRooms) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.PersonalMessages = messages.NewSessionHandler(logger, service, membership)
	return dependencies
}

func servePersonal(t *testing.T, service stubMessages, membership stubRooms,
	request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
		personalDependencies(service, membership)).Handler.ServeHTTP(response, request)
	return response
}

func member() stubRooms {
	return stubRooms{room: sampleRoom(), member: sampleMember()}
}

/*
TestPersonalResponsesMatchSpecification proves the session surface answers with
the bodies the contract promises.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe.
*/
func TestPersonalResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	sidebar := document.Paths.Find(api.Prefix + "/me/rooms")
	history := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/messages")
	readState := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/read_state")
	message := document.Paths.Find(api.Prefix + "/me/messages/{message_id}")
	withdraw := document.Paths.Find(api.Prefix + "/me/messages/{message_id}/delete")

	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID
	messageTarget := api.Prefix + "/me/messages/" + sampleMessage().ID

	said := `{"body":"On my way."}`

	tests := map[string]struct {
		request    *http.Request
		service    stubMessages
		membership stubRooms
		status     int
		operation  *openapi3.Operation
	}{
		"the sidebar": {
			request:    asPerson(httptest.NewRequest(http.MethodGet, api.Prefix+"/me/rooms", nil)),
			service:    stubMessages{readState: sampleReadState()},
			membership: member(),
			status:     http.StatusOK,
			operation:  sidebar.Get,
		},
		"the history of a room I am in": {
			request:    asPerson(httptest.NewRequest(http.MethodGet, roomTarget+"/messages?limit=2", nil)),
			service:    stubMessages{message: sampleMessage()},
			membership: member(),
			status:     http.StatusOK,
			operation:  history.Get,
		},
		"the history of a room I am not in": {
			request:    asPerson(httptest.NewRequest(http.MethodGet, roomTarget+"/messages", nil)),
			service:    stubMessages{message: sampleMessage()},
			membership: stubRooms{room: sampleRoom(), stranger: true},
			status:     http.StatusNotFound,
			operation:  history.Get,
		},
		"saying something": {
			request:    asPerson(jsonRequest(http.MethodPost, roomTarget+"/messages", said)),
			service:    stubMessages{message: sampleMessage()},
			membership: member(),
			status:     http.StatusCreated,
			operation:  history.Post,
		},
		"saying something in a room I am not in": {
			request:    asPerson(jsonRequest(http.MethodPost, roomTarget+"/messages", said)),
			service:    stubMessages{message: sampleMessage()},
			membership: stubRooms{room: sampleRoom(), stranger: true},
			status:     http.StatusNotFound,
			operation:  history.Post,
		},
		"saying something in a closed room": {
			request:    asPerson(jsonRequest(http.MethodPost, roomTarget+"/messages", said)),
			service:    stubMessages{err: messages.ErrRoomClosed},
			membership: member(),
			status:     http.StatusConflict,
			operation:  history.Post,
		},
		"how far I have read": {
			request:    asPerson(httptest.NewRequest(http.MethodGet, roomTarget+"/read_state", nil)),
			service:    stubMessages{readState: sampleReadState()},
			membership: member(),
			status:     http.StatusOK,
			operation:  readState.Get,
		},
		"marking a room read": {
			request:    asPerson(jsonRequest(http.MethodPut, roomTarget+"/read_state", `{"sequence":42}`)),
			service:    stubMessages{readState: sampleReadState()},
			membership: member(),
			status:     http.StatusOK,
			operation:  readState.Put,
		},
		"changing what I said": {
			request:    asPerson(jsonRequest(http.MethodPatch, messageTarget, said)),
			service:    stubMessages{message: sampleMessage()},
			membership: member(),
			status:     http.StatusOK,
			operation:  message.Patch,
		},
		"changing what somebody else said": {
			request:    asPerson(jsonRequest(http.MethodPatch, messageTarget, said)),
			service:    stubMessages{err: messages.ErrNotAuthor},
			membership: member(),
			status:     http.StatusConflict,
			operation:  message.Patch,
		},
		"withdrawing something I said": {
			request:    asPerson(httptest.NewRequest(http.MethodPost, messageTarget+"/delete", nil)),
			service:    stubMessages{message: withdrawnMessage()},
			membership: member(),
			status:     http.StatusOK,
			operation:  withdraw.Post,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := servePersonal(t, test.service, test.membership, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestAStrangerIsToldTheRoomIsNotThere is the privacy rule this surface rests on.

A refusal that separates "not yours" from "does not exist" confirms to somebody
outside a conversation that the conversation is happening, which is most of what
they were asking. 403 would be that confirmation; 404 is not.
*/
func TestAStrangerIsToldTheRoomIsNotThere(t *testing.T) {
	stranger := stubRooms{room: sampleRoom(), stranger: true}
	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID

	requests := map[string]*http.Request{
		"reading":      asPerson(httptest.NewRequest(http.MethodGet, roomTarget+"/messages", nil)),
		"writing":      asPerson(jsonRequest(http.MethodPost, roomTarget+"/messages", `{"body":"hello"}`)),
		"read state":   asPerson(httptest.NewRequest(http.MethodGet, roomTarget+"/read_state", nil)),
		"marking read": asPerson(jsonRequest(http.MethodPut, roomTarget+"/read_state", `{"sequence":1}`)),
	}

	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			response := servePersonal(t, stubMessages{message: sampleMessage()}, stranger, request)

			if response.Code != http.StatusNotFound {
				t.Errorf("status code = %d, want %d: %s", response.Code, http.StatusNotFound, response.Body)
			}
			if strings.Contains(response.Body.String(), "forbidden") {
				t.Errorf("the refusal admits the room exists: %s", response.Body)
			}
		})
	}
}

/*
TestAPersonCannotNameAnAuthor is the property that makes this surface safe.

There is no author field, so the schema forbids one — and a client that sends
one is told its request is invalid rather than having it quietly ignored. Being
ignored would be worse: the message would be attributed correctly, the caller
would believe otherwise, and nothing would say so.
*/
func TestAPersonCannotNameAnAuthor(t *testing.T) {
	document := loadSpecification(t)

	schema, described := document.Components.Schemas["OwnMessageRequest"]
	if !described || schema.Value == nil {
		t.Fatal("the specification describes no OwnMessageRequest schema")
	}

	for property := range schema.Value.Properties {
		if property != "body" {
			t.Errorf("a person's message request carries %q, which would let them name somebody", property)
		}
	}
	if schema.Value.AdditionalProperties.Has == nil || *schema.Value.AdditionalProperties.Has {
		t.Error("the schema allows unknown properties, so an author field would pass validation")
	}

	// And the implementation agrees: a body naming somebody is refused.
	response := servePersonal(t, stubMessages{message: sampleMessage()}, member(),
		asPerson(jsonRequest(http.MethodPost, api.Prefix+"/me/rooms/"+sampleRoom().ID+"/messages",
			`{"body":"hello","user_id":"`+sampleUser().ID+`"}`)))

	if response.Code != http.StatusBadRequest {
		t.Errorf("naming an author answered %d, want %d: %s",
			response.Code, http.StatusBadRequest, response.Body)
	}
}

/*
TestAPersonalResponseIsNeverCached is what keeps one person's conversation from
reaching another through an intermediary.

These bodies are private in a way no other surface's are: a history is large,
looks cacheable, and belongs to exactly one reader.
*/
func TestAPersonalResponseIsNeverCached(t *testing.T) {
	targets := []*http.Request{
		asPerson(httptest.NewRequest(http.MethodGet, api.Prefix+"/me/rooms", nil)),
		asPerson(httptest.NewRequest(http.MethodGet,
			api.Prefix+"/me/rooms/"+sampleRoom().ID+"/messages", nil)),
	}

	for _, request := range targets {
		response := servePersonal(t, stubMessages{message: sampleMessage(), readState: sampleReadState()},
			member(), request)

		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s answered Cache-Control %q, want no-store", request.URL.Path, got)
		}
		if got := response.Header().Values("Vary"); !containsFold(got, "Cookie") {
			t.Errorf("%s answered Vary %v, want it to include Cookie", request.URL.Path, got)
		}
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), want) {
				return true
			}
		}
	}
	return false
}
