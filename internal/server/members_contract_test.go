package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/rooms"
)

// memberDependencies serves every route with one scripted rooms service.
func memberDependencies(stub stubRooms) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.TenantRooms = rooms.NewTenantHandler(logger, stub)
	return dependencies
}

func serveMember(t *testing.T, stub stubRooms, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), memberDependencies(stub)).
		Handler.ServeHTTP(response, request)
	return response
}

/*
TestMembershipResponsesMatchSpecification proves the implementation answers with
the bodies the contract promises.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe.
*/
func TestMembershipResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	member := document.Paths.Find(api.Prefix + "/rooms/{room_id}/members/{user_id}")
	roster := document.Paths.Find(api.Prefix + "/rooms/{room_id}/members")
	theirs := document.Paths.Find(api.Prefix + "/users/{user_id}/rooms")

	memberTarget := api.Prefix + "/rooms/" + sampleRoom().ID + "/members/" + sampleUser().ID
	rosterTarget := api.Prefix + "/rooms/" + sampleRoom().ID + "/members"
	theirsTarget := api.Prefix + "/users/" + sampleUser().ID + "/rooms"

	tests := map[string]struct {
		request   *http.Request
		stub      stubRooms
		status    int
		operation *openapi3.Operation
	}{
		"given a place": {
			request:   authenticatedRequest(http.MethodPut, memberTarget, ""),
			stub:      stubRooms{room: sampleRoom(), member: sampleMember()},
			status:    http.StatusCreated,
			operation: member.Put,
		},
		"the room is not there": {
			request:   authenticatedRequest(http.MethodPut, memberTarget, ""),
			stub:      stubRooms{err: rooms.ErrNotFound},
			status:    http.StatusNotFound,
			operation: member.Put,
		},
		"the person is not there": {
			request:   authenticatedRequest(http.MethodPut, memberTarget, ""),
			stub:      stubRooms{err: rooms.ErrUserNotFound},
			status:    http.StatusNotFound,
			operation: member.Put,
		},
		"the person is suspended": {
			request:   authenticatedRequest(http.MethodPut, memberTarget, ""),
			stub:      stubRooms{err: rooms.ErrUserUnavailable},
			status:    http.StatusConflict,
			operation: member.Put,
		},
		"the roster": {
			request:   authenticatedRequest(http.MethodGet, rosterTarget+"?limit=2", ""),
			stub:      stubRooms{room: sampleRoom(), member: sampleMember()},
			status:    http.StatusOK,
			operation: roster.Get,
		},
		"a roster page that is not a number": {
			request:   authenticatedRequest(http.MethodGet, rosterTarget+"?limit=many", ""),
			stub:      stubRooms{room: sampleRoom(), member: sampleMember()},
			status:    http.StatusBadRequest,
			operation: roster.Get,
		},
		"their rooms": {
			request:   authenticatedRequest(http.MethodGet, theirsTarget, ""),
			stub:      stubRooms{room: sampleRoom(), member: sampleMember()},
			status:    http.StatusOK,
			operation: theirs.Get,
		},
		"the rooms of nobody": {
			request:   authenticatedRequest(http.MethodGet, theirsTarget, ""),
			stub:      stubRooms{err: rooms.ErrUserNotFound},
			status:    http.StatusNotFound,
			operation: theirs.Get,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := serveMember(t, test.stub, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestRemovingAMemberAnswersWithNothing keeps a removal from growing a body.

204 is the answer whether or not they were there, because the caller asked for a
state the room is now in, and a retried request must not look like a mistake.
*/
func TestRemovingAMemberAnswersWithNothing(t *testing.T) {
	target := api.Prefix + "/rooms/" + sampleRoom().ID + "/members/" + sampleUser().ID

	response := serveMember(t, stubRooms{room: sampleRoom(), member: sampleMember()},
		authenticatedRequest(http.MethodDelete, target, ""))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if response.Body.Len() != 0 {
		t.Errorf("a removal answered with %q", response.Body)
	}
}

/*
TestMembershipNeedsItsOwnScopes is the escalation this milestone was careful to
avoid.

A credential that creates and renames rooms has never been able to touch people.
Folding membership into `rooms:write` would silently widen every key already
issued into one that can put anybody anywhere.
*/
func TestMembershipNeedsItsOwnScopes(t *testing.T) {
	document := loadSpecification(t)

	cases := map[string]struct {
		operation *openapi3.Operation
		required  string
	}{
		"adding":           {document.Paths.Find(api.Prefix + "/rooms/{room_id}/members/{user_id}").Put, "members:write"},
		"removing":         {document.Paths.Find(api.Prefix + "/rooms/{room_id}/members/{user_id}").Delete, "members:write"},
		"a roster":         {document.Paths.Find(api.Prefix + "/rooms/{room_id}/members").Get, "members:read"},
		"a person's rooms": {document.Paths.Find(api.Prefix + "/users/{user_id}/rooms").Get, "members:read"},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if test.operation.Security == nil || len(*test.operation.Security) == 0 {
				t.Fatal("the operation carries no security requirement")
			}

			var granted []string
			for _, requirement := range *test.operation.Security {
				for _, scopes := range requirement {
					granted = append(granted, scopes...)
				}
			}

			if !slices.Contains(granted, test.required) {
				t.Errorf("requires %v, want %q", granted, test.required)
			}
			for _, scope := range granted {
				if scope == "rooms:read" || scope == "rooms:write" {
					t.Errorf("a room scope grants membership, which widens every key already issued")
				}
			}
		})
	}
}
