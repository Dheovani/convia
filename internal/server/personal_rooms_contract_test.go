package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/rooms"
	"convia/internal/users"
)

// servePersonalRooms serves one request with the person-facing rooms surface
// scripted by one stub.
func servePersonalRooms(t *testing.T, stub stubRooms, directory stubUsers,
	request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.PersonalRooms = rooms.NewSessionHandler(logger, stub, directory)

	response := httptest.NewRecorder()
	New("127.0.0.1:0", logger, dependencies).Handler.ServeHTTP(response, request)
	return response
}

func inTheRoom() stubRooms {
	return stubRooms{room: sampleRoom(), member: sampleMember()}
}

func everybody() stubUsers {
	return stubUsers{user: sampleUser()}
}

/*
TestPersonalRoomResponsesMatchSpecification proves the person-facing rooms
routes answer with the bodies the contract promises.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe — which matters more here
than usual, because a person's view of a room is deliberately narrower than an
application's.
*/
func TestPersonalRoomResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	collection := document.Paths.Find(api.Prefix + "/me/rooms")
	members := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/members")
	member := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/members/{user_id}")
	people := document.Paths.Find(api.Prefix + "/me/people")

	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID
	memberTarget := roomTarget + "/members/" + sampleUser().ID

	tests := map[string]struct {
		request   *http.Request
		rooms     stubRooms
		status    int
		operation *openapi3.Operation
	}{
		"opening a room": {
			request:   browser(http.MethodPost, api.Prefix+"/me/rooms", `{"name":"Weekend plans"}`),
			rooms:     inTheRoom(),
			status:    http.StatusCreated,
			operation: collection.Post,
		},
		"opening a room with no name": {
			request: browser(http.MethodPost, api.Prefix+"/me/rooms", `{"name":""}`),
			rooms: stubRooms{err: rooms.ValidationError{Field: "name",
				Message: "The name must not be empty."}},
			status:    http.StatusBadRequest,
			operation: collection.Post,
		},
		"opening a room while suspended": {
			request:   browser(http.MethodPost, api.Prefix+"/me/rooms", `{"name":"Weekend plans"}`),
			rooms:     stubRooms{err: rooms.ErrUserUnavailable},
			status:    http.StatusForbidden,
			operation: collection.Post,
		},
		"who is in a room I am in": {
			request:   browser(http.MethodGet, roomTarget+"/members", ""),
			rooms:     inTheRoom(),
			status:    http.StatusOK,
			operation: members.Get,
		},
		"who is in a room I am not in": {
			request:   browser(http.MethodGet, roomTarget+"/members", ""),
			rooms:     stubRooms{room: sampleRoom(), stranger: true},
			status:    http.StatusNotFound,
			operation: members.Get,
		},
		"adding somebody I share a room with": {
			request:   browser(http.MethodPut, memberTarget, ""),
			rooms:     inTheRoom(),
			status:    http.StatusCreated,
			operation: member.Put,
		},
		"adding a stranger": {
			request:   browser(http.MethodPut, memberTarget, ""),
			rooms:     stubRooms{room: sampleRoom(), member: sampleMember(), unshared: true},
			status:    http.StatusNotFound,
			operation: member.Put,
		},
		"adding to a room I am not in": {
			request:   browser(http.MethodPut, memberTarget, ""),
			rooms:     stubRooms{room: sampleRoom(), stranger: true},
			status:    http.StatusNotFound,
			operation: member.Put,
		},
		"people I could add": {
			request:   browser(http.MethodGet, api.Prefix+"/me/people", ""),
			rooms:     inTheRoom(),
			status:    http.StatusOK,
			operation: people.Get,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := servePersonalRooms(t, test.rooms, everybody(), test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)),
				response.Body.Bytes())
		})
	}
}

// TestLeavingAnswersWithNothing covers the one route here whose success has no
// body, which the table above cannot validate against a schema.
func TestLeavingAnswersWithNothing(t *testing.T) {
	target := api.Prefix + "/me/rooms/" + sampleRoom().ID + "/leave"

	response := servePersonalRooms(t, inTheRoom(), everybody(), browser(http.MethodPost, target, ""))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Errorf("leaving answered %d with %q, want %d and no body",
			response.Code, response.Body, http.StatusNoContent)
	}

	stranger := stubRooms{room: sampleRoom(), stranger: true}
	if response := servePersonalRooms(t, stranger, everybody(),
		browser(http.MethodPost, target, "")); response.Code != http.StatusNotFound {
		t.Errorf("leaving a room I am not in answered %d, want %d", response.Code, http.StatusNotFound)
	}
}

/*
TestAPersonOpensARoomWithANameAndNothingElse is the rule that keeps a person from
deciding things on the application's behalf.

An alias is the application's namespace, and one person squatting it would take
a name the application meant to use. The schema forbids every field but the
name, and the implementation refuses a body carrying one rather than quietly
ignoring it.
*/
func TestAPersonOpensARoomWithANameAndNothingElse(t *testing.T) {
	document := loadSpecification(t)

	schema, described := document.Components.Schemas["OwnRoomRequest"]
	if !described || schema.Value == nil {
		t.Fatal("the specification describes no OwnRoomRequest schema")
	}
	for property := range schema.Value.Properties {
		if property != "name" {
			t.Errorf("a person's room request carries %q, which is the application's to decide", property)
		}
	}
	if schema.Value.AdditionalProperties.Has == nil || *schema.Value.AdditionalProperties.Has {
		t.Error("the schema allows unknown properties, so an alias would pass validation")
	}

	for _, body := range []string{
		`{"name":"Weekend plans","alias":"standup"}`,
		`{"name":"Weekend plans","metadata":{"team":"core"}}`,
		`{"name":"Weekend plans","max_participants":2}`,
	} {
		response := servePersonalRooms(t, inTheRoom(), everybody(),
			browser(http.MethodPost, api.Prefix+"/me/rooms", body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want %d: %s", body, response.Code, http.StatusBadRequest, response.Body)
		}
	}
}

/*
TestEveryReasonSomebodyCannotBeAddedIsOneAnswer is the privacy rule of discovery,
at the transport.

A stranger, and somebody who shares a room but whom the domain refuses because
they are suspended, must be indistinguishable to the person asking: telling them
apart would reveal another person's suspension. The domain also folds an
identifier that names nobody into the same error, which the rooms integration
tests check against a real database.
*/
func TestEveryReasonSomebodyCannotBeAddedIsOneAnswer(t *testing.T) {
	target := api.Prefix + "/me/rooms/" + sampleRoom().ID + "/members/" + sampleUser().ID

	stranger := inTheRoom()
	stranger.unshared = true

	suspended := inTheRoom()
	suspended.refuseAdd = rooms.ErrUserUnavailable

	toStranger := servePersonalRooms(t, stranger, everybody(), browser(http.MethodPut, target, ""))
	toSuspended := servePersonalRooms(t, suspended, everybody(), browser(http.MethodPut, target, ""))

	if toStranger.Code != http.StatusNotFound || toSuspended.Code != http.StatusNotFound {
		t.Fatalf("a stranger answered %d and a suspended person %d, want %d for both",
			toStranger.Code, toSuspended.Code, http.StatusNotFound)
	}
	if refusal(t, toStranger) != refusal(t, toSuspended) {
		t.Errorf("the refusals differ, which tells a person who is suspended:\n  stranger:  %s\n  suspended: %s",
			toStranger.Body, toSuspended.Body)
	}
}

// refusal returns what an error response told its caller.
func refusal(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read the refusal %q: %v", response.Body, err)
	}
	return body.Error.Message
}

// TestAPersonSeesPeopleByName covers the one field this surface adds that the
// application surface deliberately leaves out.
func TestAPersonSeesPeopleByName(t *testing.T) {
	named := users.User{DisplayName: "Ana Ribeiro"}

	response := servePersonalRooms(t, inTheRoom(), stubUsers{user: named},
		browser(http.MethodGet, api.Prefix+"/me/people", ""))

	var page struct {
		Data []struct {
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("read the people: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].DisplayName != "Ana Ribeiro" {
		t.Errorf("people = %s, want one person called Ana Ribeiro", response.Body)
	}
}
