package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestIDStoresIdentifierInContext(t *testing.T) {
	var observed string
	handler := requestID(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		observed = api.RequestIDFromContext(request.Context())
	}))

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if observed != "REQUEST123" {
		t.Errorf("context request ID = %q, want %q", observed, "REQUEST123")
	}
	if header := response.Header().Get(api.RequestIDHeader); header != "REQUEST123" {
		t.Errorf("%s header = %q, want %q", api.RequestIDHeader, header, "REQUEST123")
	}
}

func TestRecoverPanicWritesInternalError(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	handler := recoverPanic(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler failure")
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	assertErrorBody(t, response, api.CodeInternal)

	if body := response.Body.String(); strings.Contains(body, "handler failure") {
		t.Errorf("body = %q, want no internal detail", body)
	}
	if !strings.Contains(logs.String(), "handler failure") {
		t.Error("panic value was not logged")
	}
	if !strings.Contains(logs.String(), "stack") {
		t.Error("panic stack was not logged")
	}
}

func TestRecoverPanicPropagatesAbortHandler(t *testing.T) {
	handler := recoverPanic(discardLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		recovered := recover()
		if recovered != http.ErrAbortHandler {
			t.Errorf("recovered = %v, want %v", recovered, http.ErrAbortHandler)
		}
	}()

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	t.Error("ServeHTTP returned normally, want http.ErrAbortHandler to propagate")
}

func TestRecoverPanicKeepsAlreadyWrittenResponse(t *testing.T) {
	handler := logRequest(discardLogger(), recoverPanic(discardLogger(), http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
			panic("late failure")
		})))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusAccepted {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusAccepted)
	}
	if response.Body.Len() != 0 {
		t.Errorf("body = %q, want an empty body", response.Body.String())
	}
}

func TestLogRequestRecordsRequestOutcome(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	handler := requestID(logRequest(logger, http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusTeapot)
			if _, err := io.WriteString(response, "short"); err != nil {
				t.Errorf("write response: %v", err)
			}
		})))

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}

	assertLogField(t, entry, "request_id", "REQUEST123")
	assertLogField(t, entry, "method", http.MethodGet)
	assertLogField(t, entry, "path", "/health")
	assertLogField(t, entry, "status", float64(http.StatusTeapot))
	assertLogField(t, entry, "bytes", float64(len("short")))

	if _, exists := entry["duration_ms"]; !exists {
		t.Error("log entry has no duration_ms field")
	}
}

func TestResponseRecorderKeepsFirstStatus(t *testing.T) {
	logs := &bytes.Buffer{}
	handler := logRequest(slog.New(slog.NewJSONHandler(logs, nil)), http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
			response.WriteHeader(http.StatusInternalServerError)
		}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusCreated {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusCreated)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}
	assertLogField(t, entry, "status", float64(http.StatusCreated))
}

func TestResponseRecorderSupportsResponseController(t *testing.T) {
	handler := logRequest(discardLogger(), http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			if err := http.NewResponseController(response).Flush(); err != nil {
				t.Errorf("flush response: %v", err)
			}
		}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if !response.Flushed {
		t.Error("response was not flushed through the recorder")
	}
}

func assertLogField(t *testing.T, entry map[string]any, name string, want any) {
	t.Helper()

	if value := entry[name]; value != want {
		t.Errorf("log field %s = %v, want %v", name, value, want)
	}
}
