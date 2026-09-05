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
