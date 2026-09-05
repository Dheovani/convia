package applications

import (
	"bytes"
	"context"
	"encoding/base64"
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
)

func testTime() time.Time {
	return time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)
}

func encode(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

/*
fakeService records what the handler asked for and returns what a test needs.

It keeps every transport path testable without PostgreSQL, including the paths
a real database would make hard to reach, such as an unexpected failure.
*/
type fakeService struct {
	application     Application
	page            Page
	err             error
	createdName     string
	requestedID     string
	expectedVersion string
	transitioned    Status
	listOptions     ListOptions
	createCalled    bool
	deleted         bool
}

func (fake *fakeService) Create(_ context.Context, name string) (Application, error) {
	fake.createCalled = true
	fake.createdName = name
	return fake.application, fake.err
}

func (fake *fakeService) Get(_ context.Context, id string) (Application, error) {
	fake.requestedID = id
	return fake.application, fake.err
}

func (fake *fakeService) List(_ context.Context, options ListOptions) (Page, error) {
	fake.listOptions = options
	return fake.page, fake.err
}

func (fake *fakeService) Rename(_ context.Context, id, name, expectedVersion string) (Application, error) {
	fake.requestedID = id
	fake.createdName = name
	fake.expectedVersion = expectedVersion
	return fake.application, fake.err
}

func (fake *fakeService) Suspend(_ context.Context, id string) (Application, error) {
	fake.requestedID = id
	fake.transitioned = StatusSuspended
	return fake.application, fake.err
}

func (fake *fakeService) Activate(_ context.Context, id string) (Application, error) {
	fake.requestedID = id
	fake.transitioned = StatusActive
	return fake.application, fake.err
}

func (fake *fakeService) Delete(_ context.Context, id string) error {
	fake.requestedID = id
	fake.deleted = true
	return fake.err
}

func newTestHandler(fake *fakeService) *Handler {
	return NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), fake)
}

func sampleApplication() Application {
	return Application{
		ID:        "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		Name:      "Workspace Town",
		Status:    StatusActive,
		CreatedAt: testTime(),
		UpdatedAt: testTime(),
	}
}

func postJSON(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/v1/applications", strings.NewReader(body))
	request.Header.Set("Content-Type", api.ContentTypeJSON)
	response := httptest.NewRecorder()

	handler.Create(response, request)
	return response
}

func TestCreateReturnsTheApplication(t *testing.T) {
	fake := &fakeService{application: sampleApplication()}
	response := postJSON(t, newTestHandler(fake), `{"name":"Workspace Town"}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusCreated)
	}
	if fake.createdName != "Workspace Town" {
		t.Errorf("created name = %q, want %q", fake.createdName, "Workspace Town")
	}

	var body applicationResponse
	decodeBody(t, response, &body)

	if body.ID != sampleApplication().ID {
		t.Errorf("id = %q, want %q", body.ID, sampleApplication().ID)
	}
	if body.Status != string(StatusActive) {
		t.Errorf("status = %q, want %q", body.Status, StatusActive)
	}
	if body.CreatedAt != "2026-09-05T14:04:56.154Z" {
		t.Errorf("created_at = %q, want %q", body.CreatedAt, "2026-09-05T14:04:56.154Z")
	}
}

// The transport rules of docs/api-conventions.md apply to this endpoint too.
func TestCreateRejectsMalformedRequests(t *testing.T) {
	tests := map[string]struct {
		body        string
		contentType string
		status      int
		code        api.ErrorCode
	}{
		"unknown field":  {`{"name":"Orbit","status":"active"}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeInvalidRequest},
		"malformed json": {`{"name":`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"empty body":     {``, api.ContentTypeJSON, http.StatusBadRequest, api.CodeInvalidRequest},
		"wrong type":     {`{"name":42}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"wrong media":    {`{"name":"Orbit"}`, "text/plain", http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{application: sampleApplication()}
			request := httptest.NewRequest(http.MethodPost, "/v1/applications", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()

			newTestHandler(fake).Create(response, request)

			assertError(t, response, test.status, test.code)
			if fake.createCalled {
				t.Error("the service was called for an invalid request")
			}
		})
	}
}

func TestCreateReportsValidationFailures(t *testing.T) {
	fake := &fakeService{err: ValidationError{Field: "name", Message: "The name must not be empty."}}
	response := postJSON(t, newTestHandler(fake), `{"name":"   "}`)

	assertError(t, response, http.StatusBadRequest, api.CodeInvalidRequest)

	if !strings.Contains(response.Body.String(), "must not be empty") {
		t.Errorf("body = %q, want the validation message", response.Body.String())
	}
}

/*
TestCreateHidesUnexpectedFailures proves that an infrastructure error never
reaches a client as anything but a generic internal error.
*/
func TestCreateHidesUnexpectedFailures(t *testing.T) {
	fake := &fakeService{err: errors.New("connection refused to 10.0.0.5:5432")}
	response := postJSON(t, newTestHandler(fake), `{"name":"Orbit"}`)

	assertError(t, response, http.StatusInternalServerError, api.CodeInternal)

	if strings.Contains(response.Body.String(), "10.0.0.5") {
		t.Errorf("body = %q, want no infrastructure detail", response.Body.String())
	}
}

func TestGetReturnsTheApplication(t *testing.T) {
	fake := &fakeService{application: sampleApplication()}
	request := httptest.NewRequest(http.MethodGet, "/v1/applications/app_MXHJAY4MJNX2FO22XWJ3XNCKHT", nil)
	request.SetPathValue("application_id", "app_MXHJAY4MJNX2FO22XWJ3XNCKHT")
	response := httptest.NewRecorder()

	newTestHandler(fake).Get(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if fake.requestedID != "app_MXHJAY4MJNX2FO22XWJ3XNCKHT" {
		t.Errorf("requested id = %q, want the path value", fake.requestedID)
	}
}

func TestGetReportsMissingApplications(t *testing.T) {
	fake := &fakeService{err: ErrNotFound}
	request := httptest.NewRequest(http.MethodGet, "/v1/applications/app_MXHJAY4MJNX2FO22XWJ3XNCKHT", nil)
	request.SetPathValue("application_id", "app_MXHJAY4MJNX2FO22XWJ3XNCKHT")
	response := httptest.NewRecorder()

	newTestHandler(fake).Get(response, request)

	assertError(t, response, http.StatusNotFound, api.CodeNotFound)
}

func TestListReturnsAPageAndCursor(t *testing.T) {
	fake := &fakeService{page: Page{
		Applications: []Application{sampleApplication()},
		NextCursor:   "opaque",
	}}

	request := httptest.NewRequest(http.MethodGet, "/v1/applications?limit=10&cursor=opaque", nil)
	response := httptest.NewRecorder()
	newTestHandler(fake).List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if fake.listOptions.Limit != 10 || fake.listOptions.Cursor != "opaque" {
		t.Errorf("options = %+v, want limit 10 and cursor %q", fake.listOptions, "opaque")
	}

	var body listResponse
	decodeBody(t, response, &body)

	if len(body.Data) != 1 {
		t.Fatalf("data has %d items, want 1", len(body.Data))
	}
	if body.NextCursor != "opaque" {
		t.Errorf("next_cursor = %q, want %q", body.NextCursor, "opaque")
	}
}

// An empty page is an empty array rather than a null, so clients can iterate it.
func TestListReturnsAnEmptyArray(t *testing.T) {
	response := httptest.NewRecorder()
	newTestHandler(&fakeService{}).List(response, httptest.NewRequest(http.MethodGet, "/v1/applications", nil))

	if body := strings.TrimSpace(response.Body.String()); body != `{"data":[]}` {
		t.Errorf("body = %q, want %q", body, `{"data":[]}`)
	}
}

func TestListRejectsInvalidQueryParameters(t *testing.T) {
	tests := map[string]struct {
		query string
		err   error
	}{
		"limit is not a number": {"?limit=many", nil},
		"limit is out of range": {"?limit=1000", ValidationError{Field: "limit", Message: "The limit must be between 1 and 100."}},
		"cursor is invalid":     {"?cursor=nonsense", ValidationError{Field: "cursor", Message: "The cursor is not valid."}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newTestHandler(&fakeService{err: test.err}).List(response,
				httptest.NewRequest(http.MethodGet, "/v1/applications"+test.query, nil))

			assertError(t, response, http.StatusBadRequest, api.CodeInvalidRequest)
		})
	}
}

// A response carrying an application also carries the version to send back.
func TestApplicationResponsesCarryAnEntityTag(t *testing.T) {
	fake := &fakeService{application: sampleApplication()}
	response := postJSON(t, newTestHandler(fake), `{"name":"Workspace Town"}`)

	want := `"` + sampleApplication().Version() + `"`
	if tag := response.Header().Get("ETag"); tag != want {
		t.Errorf("ETag = %q, want %q", tag, want)
	}
}

func TestRenameChangesTheName(t *testing.T) {
	fake := &fakeService{application: sampleApplication()}
	request := applicationRequest(http.MethodPatch, "", `{"name":"Workspace Village"}`)
	response := httptest.NewRecorder()

	newTestHandler(fake).Rename(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if fake.createdName != "Workspace Village" {
		t.Errorf("name = %q, want %q", fake.createdName, "Workspace Village")
	}
	if fake.expectedVersion != "" {
		t.Errorf("expected version = %q, want no condition", fake.expectedVersion)
	}
}

/*
TestRenameHonorsIfMatch proves that a conditional rename reaches the service
with the version the client supplied, and that unusable conditions are treated
as no condition rather than as a version.
*/
func TestRenameHonorsIfMatch(t *testing.T) {
	tests := map[string]struct {
		header string
		want   string
	}{
		"strong validator": {`"9f2c1d4b8a3e5f60"`, "9f2c1d4b8a3e5f60"},
		"unquoted":         {"9f2c1d4b8a3e5f60", "9f2c1d4b8a3e5f60"},
		"padded":           {`  "9f2c1d4b8a3e5f60"  `, "9f2c1d4b8a3e5f60"},
		"any version":      {"*", ""},
		"weak validator":   {`W/"9f2c1d4b8a3e5f60"`, ""},
		"absent":           {"", ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{application: sampleApplication()}
			request := applicationRequest(http.MethodPatch, test.header, `{"name":"Orbit"}`)
			newTestHandler(fake).Rename(httptest.NewRecorder(), request)

			if fake.expectedVersion != test.want {
				t.Errorf("expected version = %q, want %q", fake.expectedVersion, test.want)
			}
		})
	}
}

func TestRenameReportsAStaleVersion(t *testing.T) {
	fake := &fakeService{err: ErrPreconditionFailed}
	response := httptest.NewRecorder()

	newTestHandler(fake).Rename(response, applicationRequest(http.MethodPatch, `"stale"`, `{"name":"Orbit"}`))

	assertError(t, response, http.StatusPreconditionFailed, api.CodePreconditionFailed)
}

func TestTransitionsCallTheService(t *testing.T) {
	tests := map[string]struct {
		invoke func(*Handler, http.ResponseWriter, *http.Request)
		want   Status
	}{
		"suspend":  {(*Handler).Suspend, StatusSuspended},
		"activate": {(*Handler).Activate, StatusActive},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{application: sampleApplication()}
			response := httptest.NewRecorder()

			test.invoke(newTestHandler(fake), response, applicationRequest(http.MethodPost, "", ""))

			if response.Code != http.StatusOK {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
			}
			if fake.transitioned != test.want {
				t.Errorf("transitioned to %q, want %q", fake.transitioned, test.want)
			}
			if fake.requestedID != sampleApplication().ID {
				t.Errorf("requested id = %q, want the path value", fake.requestedID)
			}
		})
	}
}

// A deletion returns no content, so it must not write a body.
func TestDeleteReturnsNoContent(t *testing.T) {
	fake := &fakeService{}
	response := httptest.NewRecorder()

	newTestHandler(fake).Delete(response, applicationRequest(http.MethodDelete, "", ""))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Body.Len() != 0 {
		t.Errorf("body = %q, want an empty body", response.Body.String())
	}
	if !fake.deleted {
		t.Error("the service was not asked to delete the application")
	}
}

func TestDeleteReportsMissingApplications(t *testing.T) {
	fake := &fakeService{err: ErrNotFound}
	response := httptest.NewRecorder()

	newTestHandler(fake).Delete(response, applicationRequest(http.MethodDelete, "", ""))

	assertError(t, response, http.StatusNotFound, api.CodeNotFound)
}

// applicationRequest builds a request addressed to the sample application.
func applicationRequest(method, ifMatch, body string) *http.Request {
	target := "/v1/applications/" + sampleApplication().ID

	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", api.ContentTypeJSON)
	}

	request.SetPathValue("application_id", sampleApplication().ID)
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	return request
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()

	if contentType := response.Header().Get("Content-Type"); contentType != api.ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", contentType, api.ContentTypeJSON)
	}

	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code api.ErrorCode) {
	t.Helper()

	if response.Code != status {
		t.Errorf("status code = %d, want %d", response.Code, status)
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
}
