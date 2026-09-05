package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
)

func newTestHandler() http.Handler {
	return New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler
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

func TestServerAppliesTimeouts(t *testing.T) {
	httpServer := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))

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
