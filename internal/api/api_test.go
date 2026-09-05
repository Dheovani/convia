package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteEncodesPayload(t *testing.T) {
	response := httptest.NewRecorder()

	if err := Write(response, http.StatusCreated, map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if response.Code != http.StatusCreated {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusCreated)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", contentType, ContentTypeJSON)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}

func TestWriteReportsUnserializablePayload(t *testing.T) {
	response := httptest.NewRecorder()

	if err := Write(response, http.StatusOK, make(chan int)); err == nil {
		t.Fatal("Write() error = nil, want an encoding error")
	}
	if response.Body.Len() != 0 {
		t.Errorf("body = %q, want an empty body", response.Body.String())
	}
}

func TestWriteErrorIncludesRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/rooms", nil)
	request = request.WithContext(WithRequestID(request.Context(), "REQUEST123"))
	response := httptest.NewRecorder()

	if err := WriteError(response, request, http.StatusNotFound, CodeNotFound, "The requested resource does not exist."); err != nil {
		t.Fatalf("WriteError() error = %v", err)
	}

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}

	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != CodeNotFound {
		t.Errorf("error code = %q, want %q", body.Error.Code, CodeNotFound)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty")
	}
	if body.Error.RequestID != "REQUEST123" {
		t.Errorf("request ID = %q, want %q", body.Error.RequestID, "REQUEST123")
	}
}

func TestWriteErrorOmitsMissingRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/rooms", nil)
	response := httptest.NewRecorder()

	if err := WriteError(response, request, http.StatusInternalServerError, CodeInternal, "unexpected"); err != nil {
		t.Fatalf("WriteError() error = %v", err)
	}

	if strings.Contains(response.Body.String(), "request_id") {
		t.Errorf("body = %q, want no request_id field", response.Body.String())
	}
}

func TestWriteFailureUsesFailureStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/rooms", nil)
	response := httptest.NewRecorder()
	failure := NewFailure(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "unsupported")

	if err := WriteFailure(response, request, failure); err != nil {
		t.Fatalf("WriteFailure() error = %v", err)
	}

	if response.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
	}
	if !strings.Contains(failure.Error(), string(CodeUnsupportedMediaType)) {
		t.Errorf("Error() = %q, want it to contain %q", failure.Error(), CodeUnsupportedMediaType)
	}
}
