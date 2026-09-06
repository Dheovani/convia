package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/credentials"
	"convia/internal/users"
)

/*
stubProber reports a fixed dependency result.

It keeps every transport test independent from PostgreSQL; the real pool is
exercised by the database integration tests.
*/
type stubProber struct {
	err error
}

func (probe stubProber) Ping(context.Context) error {
	return probe.err
}

/*
stubApplications satisfies the application service that the handler consumes.

Routing and contract tests only need the routes to exist and to answer, so the
behavior lives in the applications package tests instead.
*/
type stubApplications struct {
	application applications.Application
	page        applications.Page
	err         error
}

func (stub stubApplications) Create(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Get(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) List(context.Context, applications.ListOptions) (applications.Page, error) {
	return stub.page, stub.err
}

func (stub stubApplications) Rename(context.Context, string, string, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Suspend(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Activate(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Delete(context.Context, string) error {
	return stub.err
}

/*
stubUsers satisfies the user service the handler consumes.

Routing and contract tests only need the routes to exist and to answer with a
realistic body; the behavior lives in the users package tests.
*/
type stubUsers struct {
	user    users.User
	page    users.Page
	err     error
	created bool
}

func (stub stubUsers) Resolve(context.Context, string, users.Identity) (users.User, bool, error) {
	return stub.user, stub.created, stub.err
}

func (stub stubUsers) Get(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) List(context.Context, string, users.ListOptions) (users.Page, error) {
	return stub.page, stub.err
}

func (stub stubUsers) Update(context.Context, string, string, users.Attributes, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Suspend(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Activate(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Delete(context.Context, string, string) error {
	return stub.err
}

/*
stubCredentials satisfies the credential service the handler consumes.

Routing and contract tests only need the routes to exist and to answer with a
realistic body; the behavior lives in the credentials package tests.
*/
type stubCredentials struct {
	credential credentials.Credential
	secret     credentials.Secret
	page       credentials.Page
	err        error
}

func (stub stubCredentials) Issue(context.Context, string, credentials.Request) (credentials.Credential, credentials.Secret, error) {
	return stub.credential, stub.secret, stub.err
}

func (stub stubCredentials) Get(context.Context, string, string) (credentials.Credential, error) {
	return stub.credential, stub.err
}

func (stub stubCredentials) List(context.Context, string, credentials.ListOptions) (credentials.Page, error) {
	return stub.page, stub.err
}

func (stub stubCredentials) Revoke(context.Context, string, string) error {
	return stub.err
}

// sampleCredential is a realistic credential for transport-level tests.
func sampleCredential() credentials.Credential {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)
	expires := created.Add(720 * time.Hour)

	return credentials.Credential{
		ID:            "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		Name:          "Production backend",
		Scopes:        []credentials.Scope{credentials.ScopeUsersRead, credentials.ScopeUsersWrite},
		CreatedAt:     created,
		ExpiresAt:     &expires,
	}
}

// sampleUser is a realistic user for transport-level tests.
func sampleUser() users.User {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return users.User{
		ID:              "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID:   sampleApplication().ID,
		ExternalSubject: "customer-42",
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
		Status:          users.StatusActive,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
}

// sampleApplication is a realistic application for transport-level tests.
func sampleApplication() applications.Application {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return applications.Application{
		ID:        "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		Name:      "Workspace Town",
		Status:    applications.StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// testDependencies serves every route, including the administrative API.
func testDependencies() Dependencies {
	return newDependencies(stubApplications{
		application: sampleApplication(),
		page: applications.Page{
			Applications: []applications.Application{sampleApplication()},
			NextCursor:   "b3BhcXVl",
		},
	})
}

func newDependencies(stub stubApplications) Dependencies {
	return newFullDependencies(stub, stubUsers{user: sampleUser(), page: users.Page{Users: []users.User{sampleUser()}}})
}

func newFullDependencies(application stubApplications, user stubUsers) Dependencies {
	return newEveryDependency(application, user, stubCredentials{
		credential: sampleCredential(),
		page:       credentials.Page{Credentials: []credentials.Credential{sampleCredential()}},
	})
}

func newEveryDependency(application stubApplications, user stubUsers, credential stubCredentials) Dependencies {
	return newAuthenticatedDependency(application, user, credential,
		stubAuthenticator{principal: samplePrincipal()})
}

func newAuthenticatedDependency(application stubApplications, user stubUsers,
	credential stubCredentials, verifier stubAuthenticator) Dependencies {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return Dependencies{
		Database:          stubProber{},
		Applications:      applications.NewHandler(logger, application),
		Users:             users.NewHandler(logger, user),
		Credentials:       credentials.NewHandler(logger, credential),
		Authenticator:     verifier,
		TenantUsers:       users.NewTenantHandler(logger, user),
		TenantCredentials: credentials.NewTenantHandler(logger, credential),
	}
}

/*
stubAuthenticator stands in for credential verification.

Transport tests need to control whether a request authenticates and what it
authenticates as; whether a real key verifies is settled by the credentials
package tests.
*/
type stubAuthenticator struct {
	principal credentials.Principal
	err       error
}

func (stub stubAuthenticator) Authenticate(context.Context, string) (credentials.Principal, error) {
	return stub.principal, stub.err
}

// samplePrincipal carries every scope, so a transport test fails on routing
// rather than on authorization unless it says otherwise.
func samplePrincipal() credentials.Principal {
	return credentials.Principal{
		ApplicationID: sampleApplication().ID,
		CredentialID:  sampleCredential().ID,
		Scopes:        credentials.Scopes(),
	}
}

// authenticatedRequest addresses a tenant route with a usable key.
func authenticatedRequest(method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = jsonRequest(method, target, body)
	}

	request.Header.Set("Authorization", "Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	return request
}

func newTestHandler() http.Handler {
	return newTestServer(stubProber{}).Handler
}

func newTestServer(database Prober) *http.Server {
	dependencies := testDependencies()
	dependencies.Database = database

	return New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies)
}

/*
TestAdministrativeRoutesAreOptional proves that the administrative endpoints
are absent unless they are wired in, which is what keeps an unauthenticated
tenant API from being reachable by default.
*/
func TestAdministrativeRoutesAreOptional(t *testing.T) {
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dependencies{Database: stubProber{}}).Handler

	for _, target := range []string{api.Prefix + "/applications", api.Prefix + "/applications/app_MXHJAY4MJNX2FO22XWJ3XNCKHT"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", target, response.Code, http.StatusNotFound)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}

func TestHealthEndpointRejectsOtherMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
	assertErrorBody(t, response, api.CodeMethodNotAllowed)
}

func TestUnknownRouteReturnsJSONError(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertErrorBody(t, response, api.CodeNotFound)
}

// Operational endpoints must not be reachable through the versioned public API
// prefix, so that public API policies never apply to them implicitly.
func TestHealthEndpointIsNotVersioned(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, api.Prefix+"/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestResponseGeneratesRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := response.Header().Get(api.RequestIDHeader); requestID == "" {
		t.Errorf("%s header is empty, want a generated identifier", api.RequestIDHeader)
	}
}

func TestResponseEchoesAcceptableRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := response.Header().Get(api.RequestIDHeader); requestID != "REQUEST123" {
		t.Errorf("%s header = %q, want %q", api.RequestIDHeader, requestID, "REQUEST123")
	}
}

func TestResponseReplacesUnacceptableRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "request 123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	requestID := response.Header().Get(api.RequestIDHeader)
	if requestID == "" || requestID == "request 123" {
		t.Errorf("%s header = %q, want a generated identifier", api.RequestIDHeader, requestID)
	}
}

func TestErrorResponseCarriesRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := assertErrorBody(t, response, api.CodeNotFound); requestID != "REQUEST123" {
		t.Errorf("error request_id = %q, want %q", requestID, "REQUEST123")
	}
}

func TestReadinessReportsReadyDependencies(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"ready","checks":{"database":"ok"}}` {
		t.Errorf("body = %q, want the database reported as ok", body)
	}
}

/*
TestReadinessReportsUnreachableDatabase proves that a dependency outage removes
the instance from rotation without exposing why it failed.
*/
func TestReadinessReportsUnreachableDatabase(t *testing.T) {
	failure := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	logs := &bytes.Buffer{}
	handler := New("127.0.0.1:0", slog.New(slog.NewJSONHandler(logs, nil)), Dependencies{Database: stubProber{err: failure}}).Handler

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"unavailable","checks":{"database":"unavailable"}}` {
		t.Errorf("body = %q, want the database reported as unavailable", body)
	}
	if strings.Contains(response.Body.String(), "10.0.0.5") {
		t.Errorf("body = %q, want no infrastructure detail", response.Body.String())
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Error("the readiness failure was not logged")
	}
}

// Liveness must not depend on a dependency, or an outage restarts healthy processes.
func TestHealthIgnoresDependencyFailures(t *testing.T) {
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Database: stubProber{err: errors.New("down")}}).Handler

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestServerAppliesTimeouts(t *testing.T) {
	httpServer := newTestServer(stubProber{})

	if httpServer.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", httpServer.ReadHeaderTimeout, readHeaderTimeout)
	}
	if httpServer.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %s, want %s", httpServer.ReadTimeout, readTimeout)
	}
	if httpServer.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout = %s, want %s", httpServer.WriteTimeout, writeTimeout)
	}
	if httpServer.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", httpServer.IdleTimeout, idleTimeout)
	}
	if httpServer.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", httpServer.MaxHeaderBytes, maxHeaderBytes)
	}
}

// assertErrorBody verifies the public error schema and returns the correlation
// identifier reported in the body.
func assertErrorBody(t *testing.T, response *httptest.ResponseRecorder, code api.ErrorCode) string {
	t.Helper()

	if contentType := response.Header().Get("Content-Type"); contentType != api.ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", contentType, api.ContentTypeJSON)
	}

	var body struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}

	if body.Error.Code != code {
		t.Errorf("error code = %q, want %q", body.Error.Code, code)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty")
	}
	return body.Error.RequestID
}
