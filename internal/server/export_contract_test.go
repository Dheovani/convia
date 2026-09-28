package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
	"convia/internal/export"
)

/*
stubExport writes one plausible export without a database behind it.

What these tests are about is the transport: which surface a route is on, what
it answers with, and that the two routes reach two different people. What the
export contains is internal/export's own business and is tested there.
*/
type stubExport struct {
	err error
}

func (stub stubExport) Write(_ context.Context, out io.Writer, applicationID, userID string) error {
	if stub.err != nil {
		return stub.err
	}

	_, err := io.WriteString(out,
		`{"type":"export","user_id":"`+userID+`","application_id":"`+applicationID+`"}`+"\n"+
			`{"type":"end","counts":{"rooms":0,"messages":0,"calls":0}}`+"\n")
	return err
}

/*
TestAnExportIsNewlineDelimitedJSON.

The content type is what tells a client not to try parsing the whole thing as
one document, and it is set on the first byte rather than before the work, so
that a person who is not here is answered with a status instead of with a
valid-looking export containing nothing.
*/
func TestAnExportIsNewlineDelimitedJSON(t *testing.T) {
	handler := newTestHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser(http.MethodGet, api.Prefix+"/me/data", ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != export.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, export.ContentType)
	}

	lines := strings.Split(strings.TrimRight(response.Body.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("the export has %d lines, want at least two", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], `"type":"end"`) {
		t.Errorf("the last line is not a terminator: %q", lines[len(lines)-1])
	}
}

/*
TestAnExportIsNeverCached.

It is the most sensitive response Convia produces — one person's whole history
in one body — and a shared cache holding it would serve it to whoever asked
next. `Vary: Cookie` alone is not enough, because the store is free to keep it.
*/
func TestAnExportIsNeverCached(t *testing.T) {
	handler := newTestHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser(http.MethodGet, api.Prefix+"/me/data", ""))

	if got := response.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain no-store", got)
	}
	if got := response.Header().Values("Vary"); len(got) == 0 {
		t.Error("no Vary header, so a cache keyed on the URL alone would share one person's export")
	}
}

/*
TestAPersonsExportNamesNobodyButThem.

The session route takes no user identifier, which is the property: there is no
request field that could name somebody else, so asking for another person's
data is unrepresentable rather than refused.
*/
func TestAPersonsExportNamesNobodyButThem(t *testing.T) {
	handler := newTestHandler()

	// Somebody the fixtures never mention, so finding this identifier in the
	// answer can only mean the request was believed.
	const somebodyElse = "usr_ZK4XQP7N2VJH6TBWMDR3YAFC5E"

	for _, target := range []string{
		api.Prefix + "/me/data/" + somebodyElse,
		api.Prefix + "/me/data?user_id=" + somebodyElse,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, browser(http.MethodGet, target, ""))

		if strings.Contains(response.Body.String(), somebodyElse) {
			t.Errorf("%s was served an export naming somebody from the request", target)
		}
	}
}

/*
TestATenantExportRequiresTheScopeThatReadsUsers.

It assembles everything a `users:read` key can already read, so it is gated on
that rather than on a scope of its own — and a key without it is refused here
as it is everywhere else.
*/
func TestATenantExportRequiresTheScopeThatReadsUsers(t *testing.T) {
	unscoped := samplePrincipal()
	unscoped.Scopes = nil

	dependencies := testDependencies()
	dependencies.Authenticator = stubAuthenticator{principal: unscoped}
	handler := New("127.0.0.1:0", discardLogger(), dependencies).Handler

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet,
		api.Prefix+"/users/"+sampleUser().ID+"/data", ""))

	if response.Code != http.StatusForbidden {
		t.Errorf("status = %d without users:read, want %d: %s",
			response.Code, http.StatusForbidden, response.Body)
	}
}

/*
TestAnExportRouteIsAbsentWithoutItsHandler: the same rule every other optional
route follows. Forgetting to wire one removes the endpoint rather than serving
it unauthenticated.
*/
func TestAnExportRouteIsAbsentWithoutItsHandler(t *testing.T) {
	dependencies := testDependencies()
	dependencies.PersonalExport = nil
	dependencies.TenantExport = nil
	handler := New("127.0.0.1:0", discardLogger(), dependencies).Handler

	for _, request := range []*http.Request{
		browser(http.MethodGet, api.Prefix+"/me/data", ""),
		authenticatedRequest(http.MethodGet, api.Prefix+"/users/"+sampleUser().ID+"/data", ""),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Errorf("%s answered %d with no handler wired, want %d",
				request.URL.Path, response.Code, http.StatusNotFound)
		}
	}
}
