package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/calls"
)

// callDependencies serves every route with one scripted call service.
func callDependencies(stub stubCalls) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.Calls = calls.NewHandler(logger, stub)
	dependencies.TenantCalls = calls.NewTenantHandler(logger, stub)
	return dependencies
}

func serveCall(t *testing.T, stub stubCalls, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), callDependencies(stub)).
		Handler.ServeHTTP(response, request)
	return response
}

/*
TestCallResponsesMatchSpecification proves the implementation answers with the
bodies the contract promises, on both surfaces.

The schemas forbid unknown properties, so this also fails if a response ever
grows a field the specification does not describe.
*/
func TestCallResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	roomCalls := document.Paths.Find(api.Prefix + "/rooms/{room_id}/calls")
	ownCalls := document.Paths.Find(api.Prefix + "/calls")
	ownCall := document.Paths.Find(api.Prefix + "/calls/{call_id}")
	ownEnd := document.Paths.Find(api.Prefix + "/calls/{call_id}/end")

	operatorCalls := document.Paths.Find(api.Prefix + "/applications/{application_id}/calls")
	operatorCall := document.Paths.Find(api.Prefix + "/applications/{application_id}/calls/{call_id}")
	operatorEnd := document.Paths.Find(api.Prefix + "/applications/{application_id}/calls/{call_id}/end")

	tenantTarget := api.Prefix + "/calls"
	roomTarget := api.Prefix + "/rooms/" + sampleRoom().ID + "/calls"
	operatorTarget := api.Prefix + "/applications/" + sampleApplication().ID + "/calls"

	tests := map[string]struct {
		request   *http.Request
		stub      stubCalls
		status    int
		operation *openapi3.Operation
	}{
		"started": {
			request:   authenticatedRequest(http.MethodPost, roomTarget, `{"metadata":{"agenda":"sprint_review"}}`),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusCreated,
			operation: roomCalls.Post,
		},
		"the room already has a call": {
			request:   authenticatedRequest(http.MethodPost, roomTarget, `{}`),
			stub:      stubCalls{err: calls.ErrCallInProgress},
			status:    http.StatusConflict,
			operation: roomCalls.Post,
		},
		"the room is closed": {
			request:   authenticatedRequest(http.MethodPost, roomTarget, `{}`),
			stub:      stubCalls{err: calls.ErrRoomClosed},
			status:    http.StatusConflict,
			operation: roomCalls.Post,
		},
		"the room is missing": {
			request:   authenticatedRequest(http.MethodPost, roomTarget, `{}`),
			stub:      stubCalls{err: calls.ErrRoomNotFound},
			status:    http.StatusNotFound,
			operation: roomCalls.Post,
		},
		"a room's history": {
			request:   authenticatedRequest(http.MethodGet, roomTarget+"?limit=2", ""),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: roomCalls.Get,
		},
		"the whole history": {
			request:   authenticatedRequest(http.MethodGet, tenantTarget+"?status=ended", ""),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: ownCalls.Get,
		},
		"get": {
			request:   authenticatedRequest(http.MethodGet, tenantTarget+"/"+sampleCall().ID, ""),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: ownCall.Get,
		},
		"missing call": {
			request:   authenticatedRequest(http.MethodGet, tenantTarget+"/"+sampleCall().ID, ""),
			stub:      stubCalls{err: calls.ErrNotFound},
			status:    http.StatusNotFound,
			operation: ownCall.Get,
		},
		"ended without a reason": {
			request:   authenticatedRequest(http.MethodPost, tenantTarget+"/"+sampleCall().ID+"/end", ""),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: ownEnd.Post,
		},
		"ended with a reason": {
			request:   authenticatedRequest(http.MethodPost, tenantTarget+"/"+sampleCall().ID+"/end", `{"reason":"the meeting finished"}`),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: ownEnd.Post,
		},
		"an operator reads a history": {
			request:   asOperator(httptest.NewRequest(http.MethodGet, operatorTarget, nil)),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: operatorCalls.Get,
		},
		"an operator reads a call": {
			request:   asOperator(httptest.NewRequest(http.MethodGet, operatorTarget+"/"+sampleCall().ID, nil)),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: operatorCall.Get,
		},
		"an operator ends a call": {
			request:   asOperator(jsonRequest(http.MethodPost, operatorTarget+"/"+sampleCall().ID+"/end", `{"reason":"an incident"}`)),
			stub:      stubCalls{call: sampleCall()},
			status:    http.StatusOK,
			operation: operatorEnd.Post,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := serveCall(t, test.stub, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestACallNeverPublishesProviderDetail is the guard M09-011 asks for.

A media provider will eventually give Convia a session identifier of its own,
and that identifier is infrastructure: publishing it would make a Convia client
depend on which provider Convia happens to use, which is the coupling the whole
media-plane boundary exists to prevent.

Two things are asserted, because either alone would be easy to defeat. The
schema forbids properties it does not name, so a response that grew one would
fail validation. And the response a client actually receives is compared with
the Convia-owned field set, so a field added to both the struct and the schema
still has to be a deliberate act rather than a slip.
*/
func TestACallNeverPublishesProviderDetail(t *testing.T) {
	owned := []string{
		"id", "application_id", "room_id", "status", "metadata",
		"started_by", "ended_by", "end_reason", "created_at", "updated_at", "ended_at",
	}

	document := loadSpecification(t)
	schema, found := document.Components.Schemas["Call"]
	if !found || schema.Value == nil {
		t.Fatal("the specification describes no Call schema")
	}

	if schema.Value.AdditionalProperties.Has == nil || *schema.Value.AdditionalProperties.Has {
		t.Error("the Call schema permits properties it does not name, so a leaked field would validate")
	}
	for name := range schema.Value.Properties {
		if !slices.Contains(owned, name) {
			t.Errorf("the Call schema publishes %q, which is not a Convia-owned field", name)
		}
	}

	ended := sampleCall()
	actor := calls.ActorOperator
	ended.Status = calls.StatusEnded
	ended.EndedBy = &actor
	ended.EndReason = "an incident"
	ended.EndedAt = &ended.UpdatedAt

	response := serveCall(t, stubCalls{call: ended},
		authenticatedRequest(http.MethodGet, api.Prefix+"/calls/"+ended.ID, ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the response: %v", err)
	}
	for name := range body {
		if !slices.Contains(owned, name) {
			t.Errorf("a call response carries %q, which is not a Convia-owned field", name)
		}
	}
}
