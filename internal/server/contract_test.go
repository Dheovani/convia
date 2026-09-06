package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/credentials"
	"convia/internal/users"
)

// specificationPath locates the API contract relative to this package.
var specificationPath = filepath.Join("..", "..", "api", "openapi.yaml")

/*
loadSpecification parses and validates the API specification.

Loading it in the test suite keeps contract validation in `go test`, so it
runs locally and in CI without a separate toolchain.
*/
func loadSpecification(t *testing.T) *openapi3.T {
	t.Helper()

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	document, err := loader.LoadFromFile(specificationPath)
	if err != nil {
		t.Fatalf("load %s: %v", specificationPath, err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate %s: %v", specificationPath, err)
	}
	return document
}

func TestSpecificationIsValid(t *testing.T) {
	document := loadSpecification(t)

	if document.Info == nil || document.Info.Version == "" {
		t.Error("the specification declares no version")
	}
}

// TestSpecificationMatchesRoutes proves that the documented surface and the
// implemented surface are the same in both directions.
func TestSpecificationMatchesRoutes(t *testing.T) {
	document := loadSpecification(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	documented := make(map[string]bool)
	for path, item := range document.Paths.Map() {
		for method := range item.Operations() {
			documented[method+" "+path] = false
		}
	}

	for _, entry := range routeTable(logger, testDependencies()) {
		key := entry.method + " " + entry.path
		if _, exists := documented[key]; !exists {
			t.Errorf("route %s is implemented but missing from %s", key, specificationPath)
			continue
		}
		documented[key] = true
	}

	for key, implemented := range documented {
		if !implemented {
			t.Errorf("operation %s is documented in %s but not implemented", key, specificationPath)
		}
	}
}

// TestSpecificationCoversEveryErrorCode keeps the public error vocabulary and
// the contract from drifting apart.
func TestSpecificationCoversEveryErrorCode(t *testing.T) {
	document := loadSpecification(t)

	schema, exists := document.Components.Schemas["ErrorCode"]
	if !exists {
		t.Fatalf("%s declares no ErrorCode schema", specificationPath)
	}

	documented := make([]string, 0, len(schema.Value.Enum))
	for _, value := range schema.Value.Enum {
		code, isString := value.(string)
		if !isString {
			t.Fatalf("ErrorCode enum contains a non-string value: %v", value)
		}
		documented = append(documented, code)
	}

	for _, code := range api.ErrorCodes() {
		if !slices.Contains(documented, string(code)) {
			t.Errorf("error code %q is implemented but missing from the ErrorCode enum", code)
		}
	}

	for _, code := range documented {
		if !slices.Contains(api.ErrorCodes(), api.ErrorCode(code)) {
			t.Errorf("error code %q is documented but not implemented", code)
		}
	}
}

func TestHealthResponseMatchesSpecification(t *testing.T) {
	document := loadSpecification(t)

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	operation := document.Paths.Find("/health").Get
	assertBodyMatchesSchema(t, responseSchema(t, operation.Responses.Status(http.StatusOK)), response.Body.Bytes())
}

// TestReadinessResponsesMatchSpecification covers both documented outcomes.
func TestReadinessResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	operation := document.Paths.Find("/ready").Get

	tests := map[string]struct {
		database Prober
		status   int
	}{
		"ready":       {stubProber{}, http.StatusOK},
		"unavailable": {stubProber{err: errors.New("unreachable")}, http.StatusServiceUnavailable},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/ready", nil)
			response := httptest.NewRecorder()
			newTestServer(test.database).Handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d", response.Code, test.status)
			}
			assertBodyMatchesSchema(t, responseSchema(t, operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

// TestErrorResponsesMatchSpecification validates real failure responses against
// the shared Error schema, including the codes produced by routing itself.
func TestErrorResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	errorSchema := responseSchema(t, document.Components.Responses["NotFound"])

	tests := map[string]struct {
		method string
		target string
		status int
	}{
		"not found":          {http.MethodGet, "/does-not-exist", http.StatusNotFound},
		"method not allowed": {http.MethodPost, "/health", http.StatusMethodNotAllowed},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			response := httptest.NewRecorder()
			newTestHandler().ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d", response.Code, test.status)
			}
			assertBodyMatchesSchema(t, errorSchema, response.Body.Bytes())
		})
	}
}

/*
TestApplicationResponsesMatchSpecification validates the administrative
endpoints against the contract, including the failure a client will meet most
often.
*/
func TestApplicationResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	collection := document.Paths.Find(api.Prefix + "/applications")
	item := document.Paths.Find(api.Prefix + "/applications/{application_id}")

	tests := map[string]struct {
		request   *http.Request
		stub      stubApplications
		status    int
		operation *openapi3.Operation
	}{
		"create": {
			request:   jsonRequest(http.MethodPost, api.Prefix+"/applications", `{"name":"Workspace Town"}`),
			stub:      stubApplications{application: sampleApplication()},
			status:    http.StatusCreated,
			operation: collection.Post,
		},
		"list": {
			request: httptest.NewRequest(http.MethodGet, api.Prefix+"/applications?limit=2", nil),
			stub: stubApplications{page: applications.Page{
				Applications: []applications.Application{sampleApplication()},
				NextCursor:   "b3BhcXVl",
			}},
			status:    http.StatusOK,
			operation: collection.Get,
		},
		"get": {
			request:   httptest.NewRequest(http.MethodGet, api.Prefix+"/applications/"+sampleApplication().ID, nil),
			stub:      stubApplications{application: sampleApplication()},
			status:    http.StatusOK,
			operation: item.Get,
		},
		"missing": {
			request:   httptest.NewRequest(http.MethodGet, api.Prefix+"/applications/"+sampleApplication().ID, nil),
			stub:      stubApplications{err: applications.ErrNotFound},
			status:    http.StatusNotFound,
			operation: item.Get,
		},
		"invalid body": {
			request:   jsonRequest(http.MethodPost, api.Prefix+"/applications", `{"name":""}`),
			stub:      stubApplications{err: applications.ValidationError{Field: "name", Message: "The name must not be empty."}},
			status:    http.StatusBadRequest,
			operation: collection.Post,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
				newDependencies(test.stub)).Handler.ServeHTTP(response, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestApplicationMutationsMatchSpecification covers the lifecycle operations,
including the conditional-update failure that clients must handle.
*/
func TestApplicationMutationsMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	item := document.Paths.Find(api.Prefix + "/applications/{application_id}")
	target := api.Prefix + "/applications/" + sampleApplication().ID

	suspend := document.Paths.Find(api.Prefix + "/applications/{application_id}/suspend").Post
	activate := document.Paths.Find(api.Prefix + "/applications/{application_id}/activate").Post

	tests := map[string]struct {
		request   *http.Request
		stub      stubApplications
		status    int
		operation *openapi3.Operation
	}{
		"rename": {
			request:   jsonRequest(http.MethodPatch, target, `{"name":"Workspace Village"}`),
			stub:      stubApplications{application: sampleApplication()},
			status:    http.StatusOK,
			operation: item.Patch,
		},
		"stale rename": {
			request:   jsonRequest(http.MethodPatch, target, `{"name":"Workspace Village"}`),
			stub:      stubApplications{err: applications.ErrPreconditionFailed},
			status:    http.StatusPreconditionFailed,
			operation: item.Patch,
		},
		"suspend": {
			request:   httptest.NewRequest(http.MethodPost, target+"/suspend", nil),
			stub:      stubApplications{application: sampleApplication()},
			status:    http.StatusOK,
			operation: suspend,
		},
		"activate": {
			request:   httptest.NewRequest(http.MethodPost, target+"/activate", nil),
			stub:      stubApplications{application: sampleApplication()},
			status:    http.StatusOK,
			operation: activate,
		},
		"delete missing": {
			request:   httptest.NewRequest(http.MethodDelete, target, nil),
			stub:      stubApplications{err: applications.ErrNotFound},
			status:    http.StatusNotFound,
			operation: item.Delete,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
				newDependencies(test.stub)).Handler.ServeHTTP(response, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

// A deletion documents no body, so it must not send one.
func TestDeleteSendsNoBody(t *testing.T) {
	document := loadSpecification(t)
	deletion := document.Paths.Find(api.Prefix + "/applications/{application_id}").Delete

	if content := deletion.Responses.Status(http.StatusNoContent).Value.Content; len(content) != 0 {
		t.Errorf("the specification documents a body for 204: %v", content)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, api.Prefix+"/applications/"+sampleApplication().ID, nil)
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
		newDependencies(stubApplications{})).Handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Body.Len() != 0 {
		t.Errorf("body = %q, want an empty body", response.Body.String())
	}
}

/*
TestUserResponsesMatchSpecification validates the tenant-scoped endpoints,
including the two outcomes of resolving an identity.
*/
func TestUserResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	collection := document.Paths.Find(api.Prefix + "/applications/{application_id}/users")
	item := document.Paths.Find(api.Prefix + "/applications/{application_id}/users/{user_id}")
	target := api.Prefix + "/applications/" + sampleApplication().ID + "/users"

	tests := map[string]struct {
		request   *http.Request
		stub      stubUsers
		status    int
		operation *openapi3.Operation
	}{
		"created": {
			request:   jsonRequest(http.MethodPost, target, `{"external_subject":"customer-42"}`),
			stub:      stubUsers{user: sampleUser(), created: true},
			status:    http.StatusCreated,
			operation: collection.Post,
		},
		"already existed": {
			request:   jsonRequest(http.MethodPost, target, `{"external_subject":"customer-42"}`),
			stub:      stubUsers{user: sampleUser()},
			status:    http.StatusOK,
			operation: collection.Post,
		},
		"list": {
			request:   httptest.NewRequest(http.MethodGet, target+"?limit=2", nil),
			stub:      stubUsers{page: users.Page{Users: []users.User{sampleUser()}, NextCursor: "b3BhcXVl"}},
			status:    http.StatusOK,
			operation: collection.Get,
		},
		"get": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleUser().ID, nil),
			stub:      stubUsers{user: sampleUser()},
			status:    http.StatusOK,
			operation: item.Get,
		},
		"missing user": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleUser().ID, nil),
			stub:      stubUsers{err: users.ErrNotFound},
			status:    http.StatusNotFound,
			operation: item.Get,
		},
		"missing application": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleUser().ID, nil),
			stub:      stubUsers{err: users.ErrApplicationNotFound},
			status:    http.StatusNotFound,
			operation: item.Get,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
				newFullDependencies(stubApplications{application: sampleApplication()}, test.stub)).
				Handler.ServeHTTP(response, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

func jsonRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", api.ContentTypeJSON)
	return request
}

// responseSchema returns the JSON schema of a documented response.
func responseSchema(t *testing.T, reference *openapi3.ResponseRef) *openapi3.Schema {
	t.Helper()

	if reference == nil || reference.Value == nil {
		t.Fatal("the specification declares no such response")
	}

	media := reference.Value.Content.Get(api.ContentTypeJSON)
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatalf("the response declares no %s schema", api.ContentTypeJSON)
	}
	return media.Schema.Value
}

func assertBodyMatchesSchema(t *testing.T, schema *openapi3.Schema, body []byte) {
	t.Helper()

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}

	if err := schema.VisitJSON(decoded); err != nil {
		t.Errorf("body %q does not match the specification: %v", body, err)
	}
}

/*
TestSpecificationCoversEveryScope keeps the permission vocabulary and the
contract from drifting apart.

A scope that exists in code but not in the document would be undiscoverable,
and one documented but unimplemented would be granted and never enforced.
*/
func TestSpecificationCoversEveryScope(t *testing.T) {
	document := loadSpecification(t)

	schema, exists := document.Components.Schemas["Scope"]
	if !exists {
		t.Fatalf("%s declares no Scope schema", specificationPath)
	}

	documented := make([]string, 0, len(schema.Value.Enum))
	for _, value := range schema.Value.Enum {
		scope, isString := value.(string)
		if !isString {
			t.Fatalf("scope %v is not a string", value)
		}
		documented = append(documented, scope)
	}

	implemented := make([]string, 0, len(credentials.Scopes()))
	for _, scope := range credentials.Scopes() {
		implemented = append(implemented, string(scope))
	}

	slices.Sort(documented)
	slices.Sort(implemented)

	if !slices.Equal(documented, implemented) {
		t.Errorf("documented scopes %v, implemented %v", documented, implemented)
	}
}

/*
TestCredentialResponsesMatchSpecification proves the credential endpoints
answer with exactly what the contract promises.
*/
func TestCredentialResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)
	collection := document.Paths.Find(api.Prefix + "/applications/{application_id}/credentials")
	item := document.Paths.Find(api.Prefix + "/applications/{application_id}/credentials/{credential_id}")
	target := api.Prefix + "/applications/" + sampleApplication().ID + "/credentials"

	issued := stubCredentials{
		credential: sampleCredential(),
		secret:     "YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
	}

	tests := map[string]struct {
		request   *http.Request
		stub      stubCredentials
		status    int
		operation *openapi3.Operation
	}{
		"issued": {
			request:   jsonRequest(http.MethodPost, target, `{"name":"Production backend","scopes":["users:read"]}`),
			stub:      issued,
			status:    http.StatusCreated,
			operation: collection.Post,
		},
		"list": {
			request: httptest.NewRequest(http.MethodGet, target+"?limit=2", nil),
			stub: stubCredentials{page: credentials.Page{
				Credentials: []credentials.Credential{sampleCredential()},
				NextCursor:  "b3BhcXVl",
			}},
			status:    http.StatusOK,
			operation: collection.Get,
		},
		"get": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleCredential().ID, nil),
			stub:      stubCredentials{credential: sampleCredential()},
			status:    http.StatusOK,
			operation: item.Get,
		},
		"missing credential": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleCredential().ID, nil),
			stub:      stubCredentials{err: credentials.ErrNotFound},
			status:    http.StatusNotFound,
			operation: item.Get,
		},
		"missing application": {
			request:   httptest.NewRequest(http.MethodGet, target+"/"+sampleCredential().ID, nil),
			stub:      stubCredentials{err: credentials.ErrApplicationNotFound},
			status:    http.StatusNotFound,
			operation: item.Get,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
				newEveryDependency(stubApplications{application: sampleApplication()},
					stubUsers{user: sampleUser()}, test.stub)).
				Handler.ServeHTTP(response, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)), response.Body.Bytes())
		})
	}
}

/*
TestOnlyIssuingReturnsASecret is the guarantee the whole credential design
rests on: secret material leaves Convia exactly once.

The reading endpoints are driven with a stub that holds a secret, so a handler
that started returning one would be caught here rather than in review.
*/
func TestOnlyIssuingReturnsASecret(t *testing.T) {
	target := api.Prefix + "/applications/" + sampleApplication().ID + "/credentials"
	const secret = "YH3TKPQ2MWZC7NVJ6BXRD4FGA5"

	stub := stubCredentials{
		credential: sampleCredential(),
		secret:     secret,
		page:       credentials.Page{Credentials: []credentials.Credential{sampleCredential()}},
	}

	reads := map[string]*http.Request{
		"list": httptest.NewRequest(http.MethodGet, target, nil),
		"get":  httptest.NewRequest(http.MethodGet, target+"/"+sampleCredential().ID, nil),
	}

	for name, request := range reads {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
				newEveryDependency(stubApplications{application: sampleApplication()},
					stubUsers{user: sampleUser()}, stub)).
				Handler.ServeHTTP(response, request)

			if strings.Contains(response.Body.String(), secret) {
				t.Errorf("%s returned secret material: %s", name, response.Body)
			}
		})
	}

	t.Run("issue", func(t *testing.T) {
		response := httptest.NewRecorder()
		New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
			newEveryDependency(stubApplications{application: sampleApplication()},
				stubUsers{user: sampleUser()}, stub)).
			Handler.ServeHTTP(response, jsonRequest(http.MethodPost, target,
			`{"name":"Production backend","scopes":["users:read"]}`))

		if !strings.Contains(response.Body.String(), secret) {
			t.Errorf("issuing did not return the secret, which is the only chance to: %s", response.Body)
		}
	})
}

/*
TestAuthenticationParityWithTheContract proves the routes that demand a
credential are exactly the ones documented as demanding one.

A route authenticated in code but not in the contract would surprise a client;
one documented as authenticated but served openly would be a hole. Reading both
from their sources rather than from a list keeps neither able to drift.
*/
func TestAuthenticationParityWithTheContract(t *testing.T) {
	document := loadSpecification(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, entry := range routeTable(logger, testDependencies()) {
		item := document.Paths.Find(entry.path)
		if item == nil {
			t.Errorf("route %s %s is not documented at all", entry.method, entry.path)
			continue
		}

		operation := item.GetOperation(entry.method)
		if operation == nil {
			t.Errorf("route %s %s is not documented at all", entry.method, entry.path)
			continue
		}

		documented := operation.Security != nil && len(*operation.Security) > 0
		if documented != entry.authenticated {
			t.Errorf("%s %s: authenticated in code = %t, in the contract = %t",
				entry.method, entry.path, entry.authenticated, documented)
		}
	}
}

/*
TestTenantRoutesRefuseWithoutAUsableCredential proves the authenticated surface
is closed by default.

Every reason a request can fail to authenticate produces the same status, code,
and challenge, so the response cannot be used to learn which keys exist.
*/
func TestTenantRoutesRefuseWithoutAUsableCredential(t *testing.T) {
	targets := map[string]*http.Request{
		"resolve":           jsonRequest(http.MethodPost, api.Prefix+"/users", `{"external_subject":"customer-42"}`),
		"list users":        httptest.NewRequest(http.MethodGet, api.Prefix+"/users", nil),
		"get user":          httptest.NewRequest(http.MethodGet, api.Prefix+"/users/"+sampleUser().ID, nil),
		"delete user":       httptest.NewRequest(http.MethodDelete, api.Prefix+"/users/"+sampleUser().ID, nil),
		"suspend user":      httptest.NewRequest(http.MethodPost, api.Prefix+"/users/"+sampleUser().ID+"/suspend", nil),
		"list credentials":  httptest.NewRequest(http.MethodGet, api.Prefix+"/credentials", nil),
		"issue credential":  jsonRequest(http.MethodPost, api.Prefix+"/credentials", `{"name":"K","scopes":["users:read"]}`),
		"revoke credential": httptest.NewRequest(http.MethodDelete, api.Prefix+"/credentials/"+sampleCredential().ID, nil),
	}

	headers := map[string]string{
		"absent":       "",
		"empty bearer": "Bearer ",
		"wrong scheme": "Basic Y29udmlhOmNvbnZpYQ==",
		"no scheme":    "cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"rejected key": "Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"nonsense":     "Bearer not-a-key",
	}

	document := loadSpecification(t)
	errorSchema := responseSchema(t, document.Components.Responses["Unauthorized"])

	for routeName, template := range targets {
		for headerName, header := range headers {
			t.Run(routeName+"/"+headerName, func(t *testing.T) {
				/*
					The verifier refuses everything, so even a well-formed key
					fails. That covers the revoked and expired cases at this
					layer without restating what the credentials tests prove.
				*/
				dependencies := newAuthenticatedDependency(
					stubApplications{application: sampleApplication()},
					stubUsers{user: sampleUser()},
					stubCredentials{credential: sampleCredential()},
					stubAuthenticator{err: credentials.ErrUnauthenticated})

				request := template.Clone(template.Context())
				if header != "" {
					request.Header.Set("Authorization", header)
				}

				response := httptest.NewRecorder()
				New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).
					Handler.ServeHTTP(response, request)

				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body)
				}
				if challenge := response.Header().Get("WWW-Authenticate"); challenge == "" {
					t.Error("no WWW-Authenticate challenge, which RFC 9110 requires on a 401")
				}
				assertBodyMatchesSchema(t, errorSchema, response.Body.Bytes())

				var body struct {
					Error api.ErrorBody `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode the error body: %v", err)
				}
				if body.Error.Code != api.CodeUnauthenticated {
					t.Errorf("code = %q, want %q", body.Error.Code, api.CodeUnauthenticated)
				}
			})
		}
	}
}

/*
TestVerificationFailureIsNotAGrant proves an unreachable verifier refuses the
request rather than letting it through.

A dependency failure during authentication must never resolve in the caller's
favour, and it must not be reported differently either, because the difference
would tell an attacker when Convia is degraded.
*/
func TestVerificationFailureIsNotAGrant(t *testing.T) {
	dependencies := newAuthenticatedDependency(
		stubApplications{application: sampleApplication()},
		stubUsers{user: sampleUser()},
		stubCredentials{credential: sampleCredential()},
		stubAuthenticator{err: errors.New("the database is unreachable")})

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).
		Handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, api.Prefix+"/users", ""))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if strings.Contains(response.Body.String(), "unreachable") {
		t.Errorf("the response leaked the infrastructure failure: %s", response.Body)
	}
}

/*
TestTenantRoutesAreAbsentWithoutAnAuthenticator proves a route that acts on
behalf of an application cannot exist without the middleware that decides which
application is asking.
*/
func TestTenantRoutesAreAbsentWithoutAnAuthenticator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.Authenticator = nil

	for _, entry := range routeTable(logger, dependencies) {
		if entry.authenticated {
			t.Errorf("route %s %s is served without an authenticator", entry.method, entry.path)
		}
	}
}

/*
TestVerifiedRequestReachesTheHandler proves the middleware passes the identity
through rather than only refusing.
*/
func TestVerifiedRequestReachesTheHandler(t *testing.T) {
	document := loadSpecification(t)
	response := httptest.NewRecorder()

	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), testDependencies()).
		Handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, api.Prefix+"/users", ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	operation := document.Paths.Find(api.Prefix + "/users").Get
	assertBodyMatchesSchema(t, responseSchema(t, operation.Responses.Status(http.StatusOK)), response.Body.Bytes())
}

/*
TestScopeRefusalIsForbiddenNotUnauthenticated proves the two failures stay
distinct at the transport layer, because the remedies differ: one needs a
different key, the other needs a broader grant.
*/
func TestScopeRefusalIsForbiddenNotUnauthenticated(t *testing.T) {
	document := loadSpecification(t)
	narrow := credentials.Principal{
		ApplicationID: sampleApplication().ID,
		CredentialID:  sampleCredential().ID,
		Scopes:        []credentials.Scope{credentials.ScopeCredentialsRead},
	}

	dependencies := newAuthenticatedDependency(
		stubApplications{application: sampleApplication()},
		stubUsers{user: sampleUser()},
		stubCredentials{credential: sampleCredential()},
		stubAuthenticator{principal: narrow})

	response := httptest.NewRecorder()
	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).
		Handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, api.Prefix+"/users", ""))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusForbidden, response.Body)
	}
	assertBodyMatchesSchema(t, responseSchema(t, document.Components.Responses["Forbidden"]), response.Body.Bytes())
}
