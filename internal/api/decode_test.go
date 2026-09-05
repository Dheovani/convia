package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type decodeTarget struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

func decodeRequest(t *testing.T, contentType, body string) (*decodeTarget, *Failure) {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/v1/rooms", strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}

	target := &decodeTarget{}
	return target, DecodeJSON(httptest.NewRecorder(), request, target)
}

func TestDecodeJSONAcceptsValidBody(t *testing.T) {
	target, failure := decodeRequest(t, ContentTypeJSON, `{"name":"standup","size":4}`)
	if failure != nil {
		t.Fatalf("DecodeJSON() failure = %v", failure)
	}

	if target.Name != "standup" || target.Size != 4 {
		t.Errorf("target = %+v, want {Name:standup Size:4}", *target)
	}
}

func TestDecodeJSONAcceptsContentTypeParameters(t *testing.T) {
	if _, failure := decodeRequest(t, "application/json; charset=utf-8", `{"name":"standup"}`); failure != nil {
		t.Fatalf("DecodeJSON() failure = %v", failure)
	}
}

func TestDecodeJSONRejectsContentTypes(t *testing.T) {
	tests := map[string]string{
		"missing":  "",
		"text":     "text/plain",
		"form":     "application/x-www-form-urlencoded",
		"invalid":  "application/json;;",
		"subtype":  "application/vnd.convia+json",
		"wildcard": "*/*",
	}

	for name, contentType := range tests {
		t.Run(name, func(t *testing.T) {
			_, failure := decodeRequest(t, contentType, `{"name":"standup"}`)
			assertFailure(t, failure, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType)
		})
	}
}

func TestDecodeJSONRejectsEmptyBody(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, "")
	assertFailure(t, failure, http.StatusBadRequest, CodeInvalidRequest)
}

func TestDecodeJSONRejectsMalformedJSON(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `{"name":`)
	assertFailure(t, failure, http.StatusBadRequest, CodeMalformedJSON)
}

func TestDecodeJSONRejectsSyntaxErrors(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `{"name" "standup"}`)
	assertFailure(t, failure, http.StatusBadRequest, CodeMalformedJSON)
}

func TestDecodeJSONRejectsWrongFieldType(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `{"size":"large"}`)
	assertFailure(t, failure, http.StatusBadRequest, CodeMalformedJSON)

	if !strings.Contains(failure.Message, "size") {
		t.Errorf("message = %q, want it to name the offending field", failure.Message)
	}
}

func TestDecodeJSONRejectsWrongBodyType(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `["standup"]`)
	assertFailure(t, failure, http.StatusBadRequest, CodeMalformedJSON)
}

func TestDecodeJSONRejectsUnknownField(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `{"name":"standup","unexpected":true}`)
	assertFailure(t, failure, http.StatusBadRequest, CodeInvalidRequest)

	if !strings.Contains(failure.Message, "unexpected") {
		t.Errorf("message = %q, want it to name the unknown field", failure.Message)
	}
}

func TestDecodeJSONRejectsTrailingValues(t *testing.T) {
	_, failure := decodeRequest(t, ContentTypeJSON, `{"name":"standup"}{"name":"retro"}`)
	assertFailure(t, failure, http.StatusBadRequest, CodeInvalidRequest)
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", MaxJSONRequestBytes) + `"}`

	_, failure := decodeRequest(t, ContentTypeJSON, body)
	assertFailure(t, failure, http.StatusRequestEntityTooLarge, CodePayloadTooLarge)
}

func TestDecodeJSONTruncatesReportedFieldNames(t *testing.T) {
	field := strings.Repeat("f", maxReportedFieldLength*2)

	_, failure := decodeRequest(t, ContentTypeJSON, `{"`+field+`":1}`)
	assertFailure(t, failure, http.StatusBadRequest, CodeInvalidRequest)

	if strings.Contains(failure.Message, field) {
		t.Errorf("message = %q, want the field name to be truncated", failure.Message)
	}
}

func assertFailure(t *testing.T, failure *Failure, status int, code ErrorCode) {
	t.Helper()

	if failure == nil {
		t.Fatalf("DecodeJSON() failure = nil, want %q", code)
	}
	if failure.Status != status {
		t.Errorf("status = %d, want %d", failure.Status, status)
	}
	if failure.Code != code {
		t.Errorf("code = %q, want %q", failure.Code, code)
	}
	if failure.Message == "" {
		t.Error("message is empty")
	}
}
