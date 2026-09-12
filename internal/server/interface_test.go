package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
)

// thePage stands in for Convia's own interface. What it serves does not matter;
// that it is distinguishable from the API's answers is the whole point.
const thePage = "<!doctype html><title>Convia</title>"

func servedWithAnInterface() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.Interface = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(response, thePage)
	})

	return New("127.0.0.1:0", logger, dependencies).Handler
}

func fetch(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	return response
}

/*
TestAnUnclaimedPathBelongsToTheInterface is the routing this milestone adds.

The interface routes in the browser, so a path the API never registered is a
screen rather than a mistake. It cannot be a route in the table — the contract
test compares that table against the OpenAPI document in both directions, and a
page is not an operation — so it hangs off the fallback instead.
*/
func TestAnUnclaimedPathBelongsToTheInterface(t *testing.T) {
	handler := servedWithAnInterface()

	for _, target := range []string{"/", "/rooms", "/rooms/room_123", "/anything/at/all"} {
		response := fetch(handler, http.MethodGet, target)

		if response.Code != http.StatusOK {
			t.Errorf("%s answered %d, want %d", target, response.Code, http.StatusOK)
		}
		if !strings.Contains(response.Body.String(), "<!doctype html>") {
			t.Errorf("%s did not reach the interface: %s", target, response.Body)
		}
	}
}

/*
TestTheAPINeverAnswersWithAPage is the half that matters more, and the reason
the fallback checks the prefix rather than serving everything it receives.

A client calling `/v1/roomss` has made a typo in an API call. Handing it HTML
would replace a parseable refusal with a page, and the failure it produces —
a JSON parser choking on `<`, somewhere far from the typo — is among the worst
to diagnose.
*/
func TestTheAPINeverAnswersWithAPage(t *testing.T) {
	handler := servedWithAnInterface()

	mistakes := []string{
		api.Prefix,
		api.Prefix + "/",
		api.Prefix + "/roomss",
		api.Prefix + "/me/roomz",
		api.Prefix + "/me/rooms/room_123/messagez",
	}

	for _, target := range mistakes {
		response := fetch(handler, http.MethodGet, target)

		if response.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want %d", target, response.Code, http.StatusNotFound)
		}
		if strings.Contains(response.Body.String(), "<!doctype html>") {
			t.Errorf("%s answered with the page instead of a refusal a client can read: %s",
				target, response.Body)
		}
		assertErrorBody(t, response, api.CodeNotFound)
	}
}

/*
TestOnlyAReadReachesTheInterface keeps a mistyped method from looking like a
success.

A POST to a path nothing claimed is not a navigation. Answering it with 200 and
a page would tell a client its request was accepted, which is the one answer it
must not get.
*/
func TestOnlyAReadReachesTheInterface(t *testing.T) {
	handler := servedWithAnInterface()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := fetch(handler, method, "/rooms/room_123")

		if response.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want %d", method, response.Code, http.StatusNotFound)
		}
	}
}

/*
TestAnAuthenticatedRouteStillRefuses guards the seam rather than the routes.

A registered route is matched before the fallback is ever consulted, so adding
an interface must not turn a 401 into a page. It is the kind of thing that would
only be noticed when somebody signed out and the API appeared to start working.
*/
func TestAnAuthenticatedRouteStillRefuses(t *testing.T) {
	response := fetch(servedWithAnInterface(), http.MethodGet, api.Prefix+"/me")

	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body)
	}
}

/*
TestWithoutAnInterfaceNothingChanges is the deployment that has no page at all.

Convia is a platform first, and an instance serving only integrations should
answer exactly as every Convia did before there was an interface.
*/
func TestWithoutAnInterfaceNothingChanges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New("127.0.0.1:0", logger, testDependencies()).Handler

	response := fetch(handler, http.MethodGet, "/rooms/room_123")

	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertErrorBody(t, response, api.CodeNotFound)
}
