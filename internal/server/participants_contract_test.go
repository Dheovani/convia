package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/participants"
)

// participantDependencies serves every route with one scripted roster service.
func participantDependencies(stub stubParticipants) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.Participants = participants.NewHandler(logger, stub)
	dependencies.TenantParticipants = participants.NewTenantHandler(logger, stub)
	return dependencies
}

func serveParticipant(t *testing.T, stub stubParticipants, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), participantDependencies(stub)).
		Handler.ServeHTTP(response, request)
	return response
}

/*
TestParticipantResponsesMatchSpecification proves the implementation answers
with the bodies the contract promises, on both surfaces.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe.
*/
func TestParticipantResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	callRoster := document.Paths.Find(api.Prefix + "/calls/{call_id}/participants")
	ownParticipant := document.Paths.Find(api.Prefix + "/participants/{participant_id}")
	ownLeave := document.Paths.Find(api.Prefix + "/participants/{participant_id}/leave")
	ownRemove := document.Paths.Find(api.Prefix + "/participants/{participant_id}/remove")

	operatorRoster := document.Paths.Find(api.Prefix + "/applications/{application_id}/calls/{call_id}/participants")
	operatorParticipant := document.Paths.Find(api.Prefix + "/applications/{application_id}/participants/{participant_id}")
	operatorRemove := document.Paths.Find(api.Prefix + "/applications/{application_id}/participants/{participant_id}/remove")

	rosterTarget := api.Prefix + "/calls/" + sampleCall().ID + "/participants"
	participantTarget := api.Prefix + "/participants/" + sampleParticipant().ID
	operatorTarget := api.Prefix + "/applications/" + sampleApplication().ID

	joinBody := `{"user_id":"` + sampleUser().ID + `","role":"member"}`

	tests := map[string]struct {
		request   *http.Request
		stub      stubParticipants
		status    int
		operation *openapi3.Operation
	}{
		"admitted": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{participant: sampleParticipant(), admitted: true},
			status:    http.StatusCreated,
			operation: callRoster.Post,
		},
		"already in the call": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: callRoster.Post,
		},
		"the call has ended": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrCallEnded},
			status:    http.StatusConflict,
			operation: callRoster.Post,
		},
		"the call is full": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrCallFull},
			status:    http.StatusConflict,
			operation: callRoster.Post,
		},
		"the person was removed": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrRemoved},
			status:    http.StatusConflict,
			operation: callRoster.Post,
		},
		"the person is suspended": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrUserSuspended},
			status:    http.StatusConflict,
			operation: callRoster.Post,
		},
		"the person is unknown": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrUserNotFound},
			status:    http.StatusNotFound,
			operation: callRoster.Post,
		},
		"the call is missing": {
			request:   authenticatedRequest(http.MethodPost, rosterTarget, joinBody),
			stub:      stubParticipants{err: participants.ErrCallNotFound},
			status:    http.StatusNotFound,
			operation: callRoster.Post,
		},
		"roster": {
			request:   authenticatedRequest(http.MethodGet, rosterTarget+"?status=joined&limit=2", ""),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: callRoster.Get,
		},
		"get": {
			request:   authenticatedRequest(http.MethodGet, participantTarget, ""),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: ownParticipant.Get,
		},
		"missing participant": {
			request:   authenticatedRequest(http.MethodGet, participantTarget, ""),
			stub:      stubParticipants{err: participants.ErrNotFound},
			status:    http.StatusNotFound,
			operation: ownParticipant.Get,
		},
		"promoted": {
			request:   authenticatedRequest(http.MethodPatch, participantTarget, `{"role":"moderator"}`),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: ownParticipant.Patch,
		},
		"promoted by someone who is not a moderator": {
			request:   authenticatedRequest(http.MethodPatch, participantTarget, `{"role":"moderator","by":"`+sampleParticipant().ID+`"}`),
			stub:      stubParticipants{err: participants.ErrNotAModerator},
			status:    http.StatusForbidden,
			operation: ownParticipant.Patch,
		},
		"left": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/leave", ""),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: ownLeave.Post,
		},
		"removed without a body": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/remove", ""),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: ownRemove.Post,
		},
		"removed by a moderator": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/remove", `{"by":"`+sampleParticipant().ID+`","reason":"disruptive"}`),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: ownRemove.Post,
		},
		"removed by someone who is not a moderator": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/remove", `{"by":"`+sampleParticipant().ID+`"}`),
			stub:      stubParticipants{err: participants.ErrNotAModerator},
			status:    http.StatusForbidden,
			operation: ownRemove.Post,
		},
		"an operator reads a roster": {
			request:   asOperator(httptest.NewRequest(http.MethodGet, operatorTarget+"/calls/"+sampleCall().ID+"/participants", nil)),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: operatorRoster.Get,
		},
		"an operator reads a participant": {
			request:   asOperator(httptest.NewRequest(http.MethodGet, operatorTarget+"/participants/"+sampleParticipant().ID, nil)),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: operatorParticipant.Get,
		},
		"an operator removes someone": {
			request:   asOperator(jsonRequest(http.MethodPost, operatorTarget+"/participants/"+sampleParticipant().ID+"/remove", `{"reason":"an incident"}`)),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusOK,
			operation: operatorRemove.Post,
		},
		"an operator cannot act as a participant": {
			request:   asOperator(jsonRequest(http.MethodPost, operatorTarget+"/participants/"+sampleParticipant().ID+"/remove", `{"by":"`+sampleParticipant().ID+`"}`)),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusBadRequest,
			operation: operatorRemove.Post,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := serveParticipant(t, test.stub, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestARosterNamesPeopleOnlyByIdentifier is the privacy rule M10-015 asks for.

A roster is a list of who is in a conversation, and it is read by more callers
and stored in more places than a user record is. Copying a display name into
every entry would put a person's name where nobody asked for it, and would
duplicate something the application owns and can change — a corrected name
would then be right in one place and stale in the other.

Naming people by their Convia identifier keeps the roster useful without
carrying anything about them. A caller that wants a name reads the user, where
it lives and stays current.
*/
func TestARosterNamesPeopleOnlyByIdentifier(t *testing.T) {
	owned := []string{
		"id", "application_id", "call_id", "user_id", "role", "status",
		"removed_by", "removed_by_participant_id", "removal_reason",
		"created_at", "updated_at", "left_at",
	}

	document := loadSpecification(t)
	schema, found := document.Components.Schemas["Participant"]
	if !found || schema.Value == nil {
		t.Fatal("the specification describes no Participant schema")
	}

	if schema.Value.AdditionalProperties.Has == nil || *schema.Value.AdditionalProperties.Has {
		t.Error("the Participant schema permits properties it does not name, so a leaked field would validate")
	}
	for name := range schema.Value.Properties {
		if !slices.Contains(owned, name) {
			t.Errorf("the Participant schema publishes %q, which is not a Convia-owned identifier or state", name)
		}
	}

	removed := sampleParticipant()
	remover := participants.RemoverParticipant
	removed.Status = participants.StatusRemoved
	removed.RemovedBy = &remover
	removed.RemovedByID = sampleParticipant().ID
	removed.RemovalReason = "disruptive"
	removed.LeftAt = &removed.UpdatedAt

	response := serveParticipant(t, stubParticipants{participant: removed},
		authenticatedRequest(http.MethodGet, api.Prefix+"/participants/"+removed.ID, ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the response: %v", err)
	}
	for name := range body {
		if !slices.Contains(owned, name) {
			t.Errorf("a roster entry carries %q, which is not a Convia-owned identifier or state", name)
		}
	}

	/*
		The user's display name is the specific thing that must not appear.
		Asserting on the value as well as the field names catches it arriving
		under a different name.
	*/
	if strings.Contains(response.Body.String(), sampleUser().DisplayName) {
		t.Errorf("a roster entry carries the person's display name: %s", response.Body)
	}
}
