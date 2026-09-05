package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
)

// specificationPath locates the API contract relative to this package.
var specificationPath = filepath.Join("..", "..", "api", "openapi.yaml")

// loadSpecification parses and validates the API specification.
//
// Loading it in the test suite keeps contract validation in `go test`, so it
// runs locally and in CI without a separate toolchain.
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

	for _, entry := range routeTable(logger) {
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
