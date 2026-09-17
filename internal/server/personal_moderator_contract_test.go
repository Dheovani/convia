package server

import (
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
)

// anotherOwner owns a room the signed-in person is in without owning it.
const anotherOwner = "usr_4N7XRPQ2KZ6WJBHT3MDYAFVC5E"

/*
TestModeratorsAndHandoversMatchSpecification proves the routes that name
moderators and hand a room over answer as the contract promises, and that a
moderator is let in where the contract says and kept out elsewhere.
*/
func TestModeratorsAndHandoversMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	moderatorPath := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/moderators/{user_id}")
	ownerPath := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/owner")
	room := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}")
	ban := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/bans/{user_id}")
	bans := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/bans")

	roomTarget := api.Prefix + "/me/rooms/" + sampleRoom().ID
	moderatorTarget := roomTarget + "/moderators/" + somebodyElse
	banTarget := roomTarget + "/bans/" + somebodyElse
	handover := `{"user_id":"` + somebodyElse + `"}`

	// The signed-in person moderates a room somebody else owns.
	moderator := inTheRoom()
	moderator.room.Personal = true
	moderator.room.OwnerUserID = anotherOwner
	moderator.moderator = samplePerson().UserID
	stranger := inTheRoom()
	stranger.stranger = true

	tests := map[string]struct {
		request   *http.Request
		rooms     stubRooms
		status    int
		operation *openapi3.Operation
	}{
		"naming a moderator of a room I own": {
			request:   browser(http.MethodPut, moderatorTarget, ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusNoContent,
			operation: moderatorPath.Put,
		},
		"naming myself a moderator of a room I own": {
			request:   browser(http.MethodPut, roomTarget+"/moderators/"+samplePerson().UserID, ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusBadRequest,
			operation: moderatorPath.Put,
		},
		"naming a moderator as a moderator": {
			request:   browser(http.MethodPut, moderatorTarget, ""),
			rooms:     moderator,
			status:    http.StatusForbidden,
			operation: moderatorPath.Put,
		},
		"unnaming a moderator of a room I am not in": {
			request:   browser(http.MethodDelete, moderatorTarget, ""),
			rooms:     stranger,
			status:    http.StatusNotFound,
			operation: moderatorPath.Delete,
		},
		"unnaming a moderator of a room I own": {
			request:   browser(http.MethodDelete, moderatorTarget, ""),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusNoContent,
			operation: moderatorPath.Delete,
		},
		"handing over a room I own": {
			request:   browser(http.MethodPut, roomTarget+"/owner", handover),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusOK,
			operation: ownerPath.Put,
		},
		"handing over a room I own to myself": {
			request:   browser(http.MethodPut, roomTarget+"/owner", `{"user_id":"`+samplePerson().UserID+`"}`),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusBadRequest,
			operation: ownerPath.Put,
		},
		"handing over a room somebody else owns": {
			request:   browser(http.MethodPut, roomTarget+"/owner", handover),
			rooms:     moderator,
			status:    http.StatusForbidden,
			operation: ownerPath.Put,
		},
		"handing over a room without saying to whom": {
			request:   browser(http.MethodPut, roomTarget+"/owner", `{"owner":"`+somebodyElse+`"}`),
			rooms:     ownedBySignedInPerson(),
			status:    http.StatusBadRequest,
			operation: ownerPath.Put,
		},
		"banning a member as a moderator": {
			request:   browser(http.MethodPut, banTarget, ""),
			rooms:     moderator,
			status:    http.StatusNoContent,
			operation: ban.Put,
		},
		"banning the owner as a moderator": {
			request:   browser(http.MethodPut, roomTarget+"/bans/"+anotherOwner, ""),
			rooms:     moderator,
			status:    http.StatusForbidden,
			operation: ban.Put,
		},
		"who is banned, as a moderator": {
			request:   browser(http.MethodGet, roomTarget+"/bans", ""),
			rooms:     moderator,
			status:    http.StatusOK,
			operation: bans.Get,
		},
		"deleting a room as a moderator": {
			request:   browser(http.MethodDelete, roomTarget, ""),
			rooms:     moderator,
			status:    http.StatusForbidden,
			operation: room.Delete,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := servePersonalRooms(t, test.rooms, everybody(), test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if test.status == http.StatusNoContent {
				if response.Body.Len() != 0 {
					t.Errorf("body = %q, want none", response.Body)
				}
				return
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)),
				response.Body.Bytes())
		})
	}
}
