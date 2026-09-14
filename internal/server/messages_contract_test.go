package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/messages"
)

// messageDependencies serves every route with one scripted message service.
func messageDependencies(stub stubMessages) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.TenantMessages = messages.NewTenantHandler(logger, stub)
	return dependencies
}

func serveMessage(t *testing.T, stub stubMessages, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), messageDependencies(stub)).
		Handler.ServeHTTP(response, request)
	return response
}

func sampleReadState() messages.ReadState {
	return messages.ReadState{
		RoomID:    sampleRoom().ID,
		UserID:    sampleUser().ID,
		Sequence:  42,
		Unread:    3,
		UpdatedAt: sampleApplication().CreatedAt,
	}
}

// withdrawn is the sample message after its author took it back.
func withdrawnMessage() messages.Message {
	at := sampleMessage().CreatedAt.Add(time.Minute)

	message := sampleMessage()
	message.Body = ""
	message.DeletedAt = &at
	return message
}

/*
TestMessageResponsesMatchSpecification proves the implementation answers with
the bodies the contract promises.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe.
*/
func TestMessageResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	history := document.Paths.Find(api.Prefix + "/rooms/{room_id}/messages")
	message := document.Paths.Find(api.Prefix + "/messages/{message_id}")
	withdraw := document.Paths.Find(api.Prefix + "/messages/{message_id}/delete")
	readState := document.Paths.Find(api.Prefix + "/rooms/{room_id}/read_state")

	historyTarget := api.Prefix + "/rooms/" + sampleRoom().ID + "/messages"
	messageTarget := api.Prefix + "/messages/" + sampleMessage().ID

	said := `{"user_id":"` + sampleUser().ID + `","body":"Standup in five minutes."}`
	author := `{"user_id":"` + sampleUser().ID + `"}`
	readTarget := api.Prefix + "/rooms/" + sampleRoom().ID + "/read_state"
	marked := `{"user_id":"` + sampleUser().ID + `","sequence":42}`

	tests := map[string]struct {
		request   *http.Request
		stub      stubMessages
		status    int
		operation *openapi3.Operation
	}{
		"said": {
			request:   authenticatedRequest(http.MethodPost, historyTarget, said),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusCreated,
			operation: history.Post,
		},
		"said in a closed room": {
			request:   authenticatedRequest(http.MethodPost, historyTarget, said),
			stub:      stubMessages{err: messages.ErrRoomClosed},
			status:    http.StatusConflict,
			operation: history.Post,
		},
		"said in a room that is not there": {
			request:   authenticatedRequest(http.MethodPost, historyTarget, said),
			stub:      stubMessages{err: messages.ErrRoomNotFound},
			status:    http.StatusNotFound,
			operation: history.Post,
		},
		"history": {
			request:   authenticatedRequest(http.MethodGet, historyTarget+"?limit=2&direction=older", ""),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusOK,
			operation: history.Get,
		},
		"history forwards from a cursor": {
			request:   authenticatedRequest(http.MethodGet, historyTarget+"?direction=newer&cursor=17", ""),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusOK,
			operation: history.Get,
		},
		"history in a direction Convia does not recognize": {
			request:   authenticatedRequest(http.MethodGet, historyTarget+"?direction=sideways", ""),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusBadRequest,
			operation: history.Get,
		},
		"history from a cursor that is not a sequence": {
			request:   authenticatedRequest(http.MethodGet, historyTarget+"?cursor=b3BhcXVl", ""),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusBadRequest,
			operation: history.Get,
		},
		"get": {
			request:   authenticatedRequest(http.MethodGet, messageTarget, ""),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusOK,
			operation: message.Get,
		},
		"missing message": {
			request:   authenticatedRequest(http.MethodGet, messageTarget, ""),
			stub:      stubMessages{err: messages.ErrNotFound},
			status:    http.StatusNotFound,
			operation: message.Get,
		},
		"edited": {
			request:   authenticatedRequest(http.MethodPatch, messageTarget, said),
			stub:      stubMessages{message: sampleMessage()},
			status:    http.StatusOK,
			operation: message.Patch,
		},
		"edited by somebody else": {
			request:   authenticatedRequest(http.MethodPatch, messageTarget, said),
			stub:      stubMessages{err: messages.ErrNotAuthor},
			status:    http.StatusConflict,
			operation: message.Patch,
		},
		"edited after being withdrawn": {
			request:   authenticatedRequest(http.MethodPatch, messageTarget, said),
			stub:      stubMessages{err: messages.ErrDeleted},
			status:    http.StatusConflict,
			operation: message.Patch,
		},
		"withdrawn": {
			request:   authenticatedRequest(http.MethodPost, messageTarget+"/delete", author),
			stub:      stubMessages{message: withdrawnMessage()},
			status:    http.StatusOK,
			operation: withdraw.Post,
		},
		"withdrawn by somebody else": {
			request:   authenticatedRequest(http.MethodPost, messageTarget+"/delete", author),
			stub:      stubMessages{err: messages.ErrNotAuthor},
			status:    http.StatusConflict,
			operation: withdraw.Post,
		},
		"marked read": {
			request:   authenticatedRequest(http.MethodPut, readTarget, marked),
			stub:      stubMessages{readState: sampleReadState()},
			status:    http.StatusOK,
			operation: readState.Put,
		},
		"marked read beyond the history": {
			request: authenticatedRequest(http.MethodPut, readTarget, marked),
			stub: stubMessages{err: messages.ValidationError{
				Field: "sequence", Message: "The room has no message at that position."}},
			status:    http.StatusBadRequest,
			operation: readState.Put,
		},
		"read state": {
			request:   authenticatedRequest(http.MethodGet, readTarget+"?user_id="+sampleUser().ID, ""),
			stub:      stubMessages{readState: sampleReadState()},
			status:    http.StatusOK,
			operation: readState.Get,
		},
		"read state of nobody in particular": {
			request:   authenticatedRequest(http.MethodGet, readTarget, ""),
			stub:      stubMessages{readState: sampleReadState()},
			status:    http.StatusBadRequest,
			operation: readState.Get,
		},
		"read state of somebody who never opened the room": {
			request: authenticatedRequest(http.MethodGet, readTarget+"?user_id="+sampleUser().ID, ""),
			stub: stubMessages{readState: messages.ReadState{
				RoomID: sampleRoom().ID, UserID: sampleUser().ID, Sequence: messages.Unseen, Unread: 7}},
			status:    http.StatusOK,
			operation: readState.Get,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := serveMessage(t, test.stub, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestATombstoneCarriesNoBody is the property a client renders a withdrawn
message from, asserted against the wire rather than against the domain.

A tombstone that sent `"body": ""` would be indistinguishable from a message
somebody managed to send empty, and a client would have to consult a timestamp
to tell which it was looking at.
*/
func TestATombstoneCarriesNoBody(t *testing.T) {
	request := authenticatedRequest(http.MethodPost,
		api.Prefix+"/messages/"+sampleMessage().ID+"/delete",
		`{"user_id":"`+sampleUser().ID+`"}`)

	response := serveMessage(t, stubMessages{message: withdrawnMessage()}, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d: %s", response.Code, response.Body)
	}

	body := response.Body.String()
	if strings.Contains(body, `"body"`) {
		t.Errorf("the tombstone carries a body field: %s", body)
	}
	if !strings.Contains(body, `"deleted":true`) {
		t.Errorf("the tombstone does not say it was withdrawn: %s", body)
	}
	if !strings.Contains(body, `"deleted_at"`) {
		t.Errorf("the tombstone does not say when it was withdrawn: %s", body)
	}
}

/*
TestAHistoryNamesPeopleOnlyByIdentifier applies the roster's privacy rule to
the thing read far more often than a roster is.

A history is fetched every time somebody opens a room, cached by clients, and
kept for as long as the conversation matters. Copying a display name into every
message would put a person's name in the most-replicated structure Convia has,
and a corrected name would then be right in one place and stale in thousands.
*/
func TestAHistoryNamesPeopleOnlyByIdentifier(t *testing.T) {
	owned := []string{
		"id", "application_id", "room_id", "sequence", "user_id", "invitation_id",
		"body", "deleted", "created_at", "edited_at", "deleted_at",
		// deleted_by names a role, author or owner, and never a person.
		"deleted_by",
	}

	document := loadSpecification(t)
	schema, found := document.Components.Schemas["Message"]
	if !found || schema.Value == nil {
		t.Fatal("the specification describes no Message schema")
	}

	for property := range schema.Value.Properties {
		if !slices.Contains(owned, property) {
			t.Errorf("the Message schema carries %q, which is not Convia's to publish", property)
		}
	}
}
