package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"convia/internal/api"
	"convia/internal/audit"
)

// parameter matches one `{name}` in a route pattern.
var parameter = regexp.MustCompile(`\{[a-z_]+\}`)

// concrete turns a route pattern into a path a request can be sent to.
func concrete(pattern string) string {
	return parameter.ReplaceAllStringFunc(pattern, func(name string) string {
		return strings.Trim(name, "{}") + "_sample"
	})
}

func refusedFor(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", response.Body, err)
	}
	if response.Code != http.StatusBadRequest || body.Error.Code != string(api.CodeInvalidRequest) {
		t.Fatalf("status %d code %q, want 400 invalid_request: %s", response.Code, body.Error.Code, response.Body)
	}
	return body.Error.Message
}

/*
TestEveryHighImpactRouteAsksWhy walks the table rather than a list, so a route
marked later is held to the same rule without anybody adding it here.

What it proves is that the refusal happens before the handler: these requests
name things that do not exist, and they are refused for not saying why rather
than answered with not found.
*/
func TestEveryHighImpactRouteAsksWhy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New("127.0.0.1:0", logger, newDependencies(stubApplications{})).Handler

	asked := 0
	for _, entry := range routeTable(logger, newDependencies(stubApplications{})) {
		if !entry.reasoned {
			continue
		}
		asked++

		request := asOperatorWithoutSaying(httptest.NewRequest(entry.method, concrete(entry.path), nil))
		if entry.confirms != "" {
			request.Header.Set(confirmHeader, path.Base(request.URL.Path))
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if message := refusedFor(t, response); !strings.Contains(message, reasonHeader) {
			t.Errorf("%s %s: refused with %q, which does not say what was missing", entry.method, entry.path, message)
		}
	}

	if asked < 11 {
		t.Errorf("only %d routes ask why; suspending, deleting, revoking, ending and removing all should", asked)
	}
}

/*
TestEveryDeletionNamesItsTargetTwice is `M21-011` over the table: a destructive
route answers a request whose confirmation names something else -- the
mistake it exists for -- and one that names nothing, the same way.
*/
func TestEveryDeletionNamesItsTargetTwice(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New("127.0.0.1:0", logger, newDependencies(stubApplications{})).Handler

	confirmed := 0
	for _, entry := range routeTable(logger, newDependencies(stubApplications{})) {
		if entry.confirms == "" {
			continue
		}
		confirmed++

		for _, presented := range []string{"", "something_else"} {
			request := asOperatorWithoutSaying(httptest.NewRequest(entry.method, concrete(entry.path), nil))
			request.Header.Set(reasonHeader, "cleaning up a test tenant")
			if presented != "" {
				request.Header.Set(confirmHeader, presented)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if message := refusedFor(t, response); !strings.Contains(message, confirmHeader) {
				t.Errorf("%s %s confirmed by %q: refused with %q", entry.method, entry.path, presented, message)
			}
		}

		if entry.method != http.MethodDelete {
			t.Errorf("%s %s confirms but is not a deletion", entry.method, entry.path)
		}
	}

	if confirmed != 5 {
		t.Errorf("%d routes confirm their target, want the five operator deletions", confirmed)
	}
}

/*
TestTheReasonReachesTheTrail checks the other half: what was accepted is what
the trail will read, trimmed, and nothing about it is lost on the way.
*/
func TestTheReasonReachesTheTrail(t *testing.T) {
	var reached string
	next := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		reached, _ = audit.ReasonFromContext(request.Context())
	})

	request := httptest.NewRequest(http.MethodDelete, "/v1/applications/app_A", nil)
	request.SetPathValue("application_id", "app_A")
	request.Header.Set(reasonHeader, "  tenant asked to be removed, ticket 812  ")
	request.Header.Set(confirmHeader, "app_A")

	response := httptest.NewRecorder()
	deliberate(slog.New(slog.NewTextHandler(io.Discard, nil)), true, "application_id", next).ServeHTTP(response, request)

	if reached != "tenant asked to be removed, ticket 812" {
		t.Errorf("the handler saw reason %q (status %d)", reached, response.Code)
	}
}

// TestAReasonThatCannotBeRecordedIsRefused says which rule it broke.
func TestAReasonThatCannotBeRecordedIsRefused(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	request := httptest.NewRequest(http.MethodPost, "/v1/applications/app_A/suspend", nil)
	request.Header.Set(reasonHeader, strings.Repeat("why ", 200))

	response := httptest.NewRecorder()
	deliberate(slog.New(slog.NewTextHandler(io.Discard, nil)), true, "", next).ServeHTTP(response, request)

	if message := refusedFor(t, response); !strings.Contains(message, "at most") {
		t.Errorf("refused with %q, which does not say what was wrong", message)
	}
	if reached {
		t.Error("a request with a reason the trail cannot hold reached the handler")
	}
}

/*
TestAnUnauthenticatedCallerLearnsThatFirst keeps the extra questions from
telling somebody without a key which operations are the dangerous ones.
*/
func TestAnUnauthenticatedCallerLearnsThatFirst(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New("127.0.0.1:0", logger, newDependencies(stubApplications{})).Handler

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/applications/app_A", nil))

	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 before any question about reasons", response.Code)
	}
}

// TestOnlyTheOperatorSurfaceAsksWhy is the check that stops the process at startup.
func TestOnlyTheOperatorSurfaceAsksWhy(t *testing.T) {
	cases := map[string]route{
		"a tenant route":   {method: http.MethodDelete, path: "/v1/rooms/{room_id}", surface: surfaceTenant, reasoned: true},
		"a missing target": {method: http.MethodDelete, path: "/v1/applications/{application_id}", surface: surfaceOperator, confirms: "room_id"},
		"a session route":  {method: http.MethodPost, path: "/v1/me/rooms", surface: surfaceSession, confirms: "room_id"},
	}
	for name, entry := range cases {
		if deliberation(entry) == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := deliberation(route{method: http.MethodDelete, path: "/v1/applications/{application_id}",
		surface: surfaceOperator, reasoned: true, confirms: "application_id"}); err != nil {
		t.Errorf("a well-marked route was refused: %v", err)
	}
}

/*
TestTheContractSaysWhichRoutesAskWhy holds the specification to the table in
both directions, the way authentication and scopes already are: a client
reading the contract must learn which operations need the headers before the
first one is refused.
*/
func TestTheContractSaysWhichRoutesAskWhy(t *testing.T) {
	document := loadSpecification(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, entry := range routeTable(logger, testDependencies()) {
		operation := document.Paths.Find(entry.path).GetOperation(entry.method)
		if operation == nil {
			continue
		}

		documented := map[string]bool{}
		for _, reference := range operation.Parameters {
			if reference.Value != nil && reference.Value.In == "header" && reference.Value.Required {
				documented[reference.Value.Name] = true
			}
		}

		if documented[reasonHeader] != entry.reasoned {
			t.Errorf("%s %s: asks why in code = %t, in the contract = %t",
				entry.method, entry.path, entry.reasoned, documented[reasonHeader])
		}
		if documented[confirmHeader] != (entry.confirms != "") {
			t.Errorf("%s %s: confirms in code = %t, in the contract = %t",
				entry.method, entry.path, entry.confirms != "", documented[confirmHeader])
		}
	}
}
