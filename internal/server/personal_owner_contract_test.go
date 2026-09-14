package server

import (
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
)

// ownedBySignedInPerson is a room the person every browser request carries owns.
func ownedBySignedInPerson() stubRooms {
	owned := inTheRoom()
	owned.room.OwnerUserID = samplePerson().UserID
	owned.room.Personal = true
	return owned
}

/*
TestWhatOnlyAnOwnerMayDoMatchesSpecification proves the owner's routes answer as
the contract promises, for the owner, for a member who is not the owner, and
for somebody outside the room.

A member is told `403`: they already know the room exists. Somebody outside it is
told `404`, as on every route a person reaches.
*/
func TestWhatOnlyAnOwnerMayDoMatchesSpecification(t *testing.T) {
	document := loadSpecification(t)

	room := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}")
	closing := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/close")
	reopening := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/reopen")
	member := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/members/{user_id}")
	bans := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/bans")
	ban := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/bans/{user_id}")

	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID
	memberTarget := roomTarget + "/members/" + sampleUser().ID
	banTarget := roomTarget + "/bans/" + sampleUser().ID

	stranger := inTheRoom()
	stranger.stranger = true

	tests := map[string]struct {
		request   *http.Request
		rooms     stubRooms
		status    int
		operation *openapi3.Operation
	}{
		"renaming a room I own": {
			request:   browser(http.MethodPatch, roomTarget, `{"name":"Weekly standup"}`),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusOK,
			operation: room.Patch,
		},
		"renaming a room somebody else owns": {
			request:   browser(http.MethodPatch, roomTarget, `{"name":"Weekly standup"}`),
			rooms:     inTheRoom(),
			status:    http.StatusForbidden,
			operation: room.Patch,
		},
		"renaming a room I am not in": {
			request:   browser(http.MethodPatch, roomTarget, `{"name":"Weekly standup"}`),
			rooms:     stranger,
			status:    http.StatusNotFound,
			operation: room.Patch,
		},
		"closing a room I own": {
			request:   browser(http.MethodPost, roomTarget+"/close", ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusOK,
			operation: closing.Post,
		},
		"reopening a room I own": {
			request:   browser(http.MethodPost, roomTarget+"/reopen", ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusOK,
			operation: reopening.Post,
		},
		"deleting a room somebody else owns": {
			request:   browser(http.MethodDelete, roomTarget, ""),
			rooms:     inTheRoom(),
			status:    http.StatusForbidden,
			operation: room.Delete,
		},
		"removing somebody from a room somebody else owns": {
			request:   browser(http.MethodDelete, memberTarget, ""),
			rooms:     inTheRoom(),
			status:    http.StatusForbidden,
			operation: member.Delete,
		},
		"removing myself from a room I own": {
			request:   browser(http.MethodDelete, roomTarget+"/members/"+samplePerson().UserID, ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusBadRequest,
			operation: member.Delete,
		},
		"who is banned from a room I own": {
			request:   browser(http.MethodGet, roomTarget+"/bans", ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusOK,
			operation: bans.Get,
		},
		"who is banned from a room somebody else owns": {
			request:   browser(http.MethodGet, roomTarget+"/bans", ""),
			rooms:     inTheRoom(),
			status:    http.StatusForbidden,
			operation: bans.Get,
		},
		"banning somebody from a room somebody else owns": {
			request:   browser(http.MethodPut, banTarget, ""),
			rooms:     inTheRoom(),
			status:    http.StatusForbidden,
			operation: ban.Put,
		},
		"lifting a ban on a room I am not in": {
			request:   browser(http.MethodDelete, banTarget, ""),
			rooms:     stranger,
			status:    http.StatusNotFound,
			operation: ban.Delete,
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

// somebodyElse is a person in the room who is not the one signed in.
const somebodyElse = "usr_7QK4XMZP2VJH6TBWNDR3YAFC5E"

// TestAnOwnersActsWithNothingToSayAnswerWithNoBody covers the owner's acts that
// answer only that they happened.
func TestAnOwnersActsWithNothingToSayAnswerWithNoBody(t *testing.T) {
	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID

	for name, request := range map[string]*http.Request{
		"deleting a room":   browser(http.MethodDelete, roomTarget, ""),
		"removing somebody": browser(http.MethodDelete, roomTarget+"/members/"+somebodyElse, ""),
		"banning somebody":  browser(http.MethodPut, roomTarget+"/bans/"+somebodyElse, ""),
		"lifting a ban":     browser(http.MethodDelete, roomTarget+"/bans/"+somebodyElse, ""),
	} {
		t.Run(name, func(t *testing.T) {
			response := servePersonalRooms(t, ownedBySignedInPerson(), everybody(), request)
			if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
				t.Errorf("answered %d with %q, want %d and no body", response.Code, response.Body, http.StatusNoContent)
			}
		})
	}
}
