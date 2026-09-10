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
	ownSession := document.Paths.Find(api.Prefix + "/participants/{participant_id}/session")

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
		"connection instructions": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/session", ""),
			stub:      stubParticipants{participant: sampleParticipant()},
			status:    http.StatusCreated,
			operation: ownSession.Post,
		},
		"nothing to connect to": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/session", ""),
			stub:      stubParticipants{err: participants.ErrNoMediaPlane},
			status:    http.StatusServiceUnavailable,
			operation: ownSession.Post,
		},
		"no longer in the call": {
			request:   authenticatedRequest(http.MethodPost, participantTarget+"/session", ""),
			stub:      stubParticipants{err: participants.ErrGone},
			status:    http.StatusConflict,
			operation: ownSession.Post,
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
	/*
		"guest" and "invitation_id" are on this list because they are Convia's
		own state and Convia's own identifier. A guest is precisely the case
		where Convia holds no personal data at all: the invitation is the whole
		of what it knows, and the application that sent it is the only party
		that can say who redeemed it.
	*/
	owned := []string{
		"id", "application_id", "call_id", "guest", "user_id", "invitation_id",
		"role", "status", "removed_by", "removed_by_participant_id",
		"removal_reason", "created_at", "updated_at", "left_at",
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

/*
TestConnectionInstructionsNameNoProvider is the exit criterion of M13 checked
against the wire.

A client joins through Convia alone, so what it receives has to be sayable
without naming any media infrastructure. If a provider concept ever appears
here it becomes part of the public contract, and removing it later breaks every
client that read it.
*/
func TestConnectionInstructionsNameNoProvider(t *testing.T) {
	owned := []string{"participant_id", "call_id", "media_url", "media_token", "expires_at"}

	document := loadSpecification(t)
	schema, found := document.Components.Schemas["JoinSession"]
	if !found || schema.Value == nil {
		t.Fatal("the specification describes no JoinSession schema")
	}

	if schema.Value.AdditionalProperties.Has == nil || *schema.Value.AdditionalProperties.Has {
		t.Error("the JoinSession schema permits properties it does not name, so a leaked field would validate")
	}
	for name := range schema.Value.Properties {
		if !slices.Contains(owned, name) {
			t.Errorf("the JoinSession schema publishes %q, which is not Convia-owned connection data", name)
		}
	}

	response := serveParticipant(t, stubParticipants{participant: sampleParticipant()},
		authenticatedRequest(http.MethodPost,
			api.Prefix+"/participants/"+sampleParticipant().ID+"/session", ""))

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the response: %v", err)
	}
	for name := range body {
		if !slices.Contains(owned, name) {
			t.Errorf("connection instructions carry %q, which is not Convia-owned connection data", name)
		}
	}

	/*
		The room the provider knows the call by is the specific thing that must
		not appear. A client that learned it could reason about Convia's
		infrastructure, and Convia could never rename one again.
	*/
	rendered := strings.ToLower(response.Body.String())
	for _, leaked := range []string{"livekit", "webrtc", "sfu", "room_name", "roomname", "grant", "sid"} {
		if strings.Contains(rendered, leaked) {
			t.Errorf("connection instructions mention %q: %s", leaked, response.Body)
		}
	}
}

/*
TestConnectionInstructionsCarryTheCredentialExactlyOnce is the other half of
redaction.

Everywhere else a credential renders as a placeholder, which is the point of
the type. This one response has to contain the real thing, so the test proves
the single Reveal is wired to the right field rather than that redaction leaks.
*/
func TestConnectionInstructionsCarryTheCredentialExactlyOnce(t *testing.T) {
	response := serveParticipant(t, stubParticipants{participant: sampleParticipant()},
		authenticatedRequest(http.MethodPost,
			api.Prefix+"/participants/"+sampleParticipant().ID+"/session", ""))

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}

	var body struct {
		MediaToken string `json:"media_token"`
		MediaURL   string `json:"media_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the response: %v", err)
	}

	if body.MediaToken != "a-signed-connection-credential" {
		t.Errorf("media_token = %q, want the credential the service issued", body.MediaToken)
	}
	if body.MediaURL != "wss://media.example" {
		t.Errorf("media_url = %q, want the address the service issued", body.MediaURL)
	}
	if strings.Contains(response.Body.String(), "[redacted]") {
		t.Error("the response redacted the credential it exists to deliver")
	}
}
