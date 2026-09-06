package users

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
)

const (
	testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	testUserID        = "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"
)

/*
fakeService records what the handler asked for and returns what a test needs.

It keeps every transport path testable without PostgreSQL, including the paths
a real database would make hard to reach.
*/
type fakeService struct {
	user            User
	page            Page
	err             error
	created         bool
	identity        Identity
	application     string
	requestedID     string
	options         ListOptions
	attributes      Attributes
	expectedVersion string
	transitionedTo  Status
	called          bool
}

func (fake *fakeService) Resolve(_ context.Context, applicationID string, identity Identity) (User, bool, error) {
	fake.called = true
	fake.application = applicationID
	fake.identity = identity
	return fake.user, fake.created, fake.err
}

func (fake *fakeService) Get(_ context.Context, applicationID, id string) (User, error) {
	fake.called = true
	fake.application = applicationID
	fake.requestedID = id
	return fake.user, fake.err
}

func (fake *fakeService) List(_ context.Context, applicationID string, options ListOptions) (Page, error) {
	fake.called = true
	fake.application = applicationID
	fake.options = options
	return fake.page, fake.err
}

func (fake *fakeService) Update(_ context.Context, applicationID, id string,
	attributes Attributes, expectedVersion string) (User, error) {
	fake.called = true
	fake.application = applicationID
	fake.requestedID = id
	fake.attributes = attributes
	fake.expectedVersion = expectedVersion
	return fake.user, fake.err
}

func (fake *fakeService) Suspend(_ context.Context, applicationID, id string) (User, error) {
	return fake.record(applicationID, id, StatusSuspended)
}

func (fake *fakeService) Activate(_ context.Context, applicationID, id string) (User, error) {
	return fake.record(applicationID, id, StatusActive)
}

func (fake *fakeService) Delete(_ context.Context, applicationID, id string) error {
	fake.called = true
	fake.application = applicationID
	fake.requestedID = id
	return fake.err
}

// record captures a lifecycle transition so a test can assert which one ran.
func (fake *fakeService) record(applicationID, id string, status Status) (User, error) {
	fake.called = true
	fake.application = applicationID
	fake.requestedID = id
	fake.transitionedTo = status
	return fake.user, fake.err
}

func newTestHandler(fake *fakeService) *Handler {
	return NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), fake)
}

func sampleUser() User {
	return User{
		ID:              testUserID,
		ApplicationID:   testApplicationID,
		ExternalSubject: "customer-42",
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
		Status:          StatusActive,
		CreatedAt:       testTime(),
		UpdatedAt:       testTime(),
	}
}

// userRequest builds a request addressed to the sample application.
func userRequest(method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", api.ContentTypeJSON)
	}

	request.SetPathValue("application_id", testApplicationID)
	request.SetPathValue("user_id", testUserID)
	return request
}

/*
TestResolveDistinguishesCreationFromResolution proves the status code tells a
client whether the mapping is new, which is the only observable difference
between the two outcomes.
*/
func TestResolveDistinguishesCreationFromResolution(t *testing.T) {
	tests := map[string]struct {
		created bool
		status  int
	}{
		"created":  {true, http.StatusCreated},
		"existing": {false, http.StatusOK},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser(), created: test.created}
			response := httptest.NewRecorder()

			newTestHandler(fake).Resolve(response,
				userRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/users", `{"external_subject":"customer-42"}`))

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d", response.Code, test.status)
			}
			if fake.application != testApplicationID {
				t.Errorf("application = %q, want the path value", fake.application)
			}
		})
	}
}

func TestResolvePassesTheIdentityThrough(t *testing.T) {
	fake := &fakeService{user: sampleUser(), created: true}
	body := `{"external_subject":"customer-42","display_name":"Ada Lovelace","metadata":{"plan":"pro"}}`

	newTestHandler(fake).Resolve(httptest.NewRecorder(),
		userRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/users", body))

	if fake.identity.ExternalSubject != "customer-42" {
		t.Errorf("external subject = %q, want %q", fake.identity.ExternalSubject, "customer-42")
	}
	if fake.identity.DisplayName != "Ada Lovelace" {
		t.Errorf("display name = %q, want %q", fake.identity.DisplayName, "Ada Lovelace")
	}
	if fake.identity.Metadata["plan"] != "pro" {
		t.Errorf("metadata = %v, want the plan entry", fake.identity.Metadata)
	}
}

// Nested metadata is rejected by decoding, before the service is reached.
func TestResolveRejectsMalformedRequests(t *testing.T) {
	tests := map[string]struct {
		body        string
		contentType string
		status      int
		code        api.ErrorCode
	}{
		"unknown field":    {`{"external_subject":"a","role":"admin"}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeInvalidRequest},
		"nested metadata":  {`{"external_subject":"a","metadata":{"plan":{"tier":"pro"}}}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"numeric metadata": {`{"external_subject":"a","metadata":{"seats":3}}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"array metadata":   {`{"external_subject":"a","metadata":["pro"]}`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"malformed json":   {`{"external_subject":`, api.ContentTypeJSON, http.StatusBadRequest, api.CodeMalformedJSON},
		"empty body":       {``, api.ContentTypeJSON, http.StatusBadRequest, api.CodeInvalidRequest},
		"wrong media":      {`{"external_subject":"a"}`, "text/plain", http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser()}
			request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/users",
				strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.SetPathValue("application_id", testApplicationID)
			response := httptest.NewRecorder()

			newTestHandler(fake).Resolve(response, request)

			assertError(t, response, test.status, test.code)
			if fake.called {
				t.Error("the service was called for an invalid request")
			}
		})
	}
}

func TestResolveReportsValidationFailures(t *testing.T) {
	fake := &fakeService{err: ValidationError{Field: "external_subject", Message: "The external subject must not be empty."}}
	response := httptest.NewRecorder()

	newTestHandler(fake).Resolve(response,
		userRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/users", `{"external_subject":"  "}`))

	assertError(t, response, http.StatusBadRequest, api.CodeInvalidRequest)
	if !strings.Contains(response.Body.String(), "must not be empty") {
		t.Errorf("body = %q, want the validation message", response.Body.String())
	}
}

/*
TestMissingApplicationAndUserAreDistinguished keeps the two 404s useful to a
caller without either revealing anything about another tenant.
*/
func TestMissingApplicationAndUserAreDistinguished(t *testing.T) {
	tests := map[string]struct {
		err  error
		want string
	}{
		"missing application": {ErrApplicationNotFound, "application"},
		"missing user":        {ErrNotFound, "user"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newTestHandler(&fakeService{err: test.err}).Get(response,
				userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users/"+testUserID, ""))

			assertError(t, response, http.StatusNotFound, api.CodeNotFound)
			if !strings.Contains(strings.ToLower(response.Body.String()), test.want) {
				t.Errorf("body = %q, want it to mention the %s", response.Body.String(), test.want)
			}
		})
	}
}

func TestGetReturnsTheUserAndItsVersion(t *testing.T) {
	fake := &fakeService{user: sampleUser()}
	response := httptest.NewRecorder()

	newTestHandler(fake).Get(response, userRequest(http.MethodGet,
		"/v1/applications/"+testApplicationID+"/users/"+testUserID, ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if fake.requestedID != testUserID {
		t.Errorf("requested id = %q, want the path value", fake.requestedID)
	}
	if want := `"` + sampleUser().Version() + `"`; response.Header().Get("ETag") != want {
		t.Errorf("ETag = %q, want %q", response.Header().Get("ETag"), want)
	}

	var body userResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}
	if body.ExternalSubject != "customer-42" || body.Metadata["plan"] != "pro" {
		t.Errorf("body = %+v, want the stored identity", body)
	}
}

// A user without a display name omits the field rather than sending an empty one.
func TestUsersWithoutADisplayNameOmitIt(t *testing.T) {
	user := sampleUser()
	user.DisplayName = ""
	user.Metadata = nil

	response := httptest.NewRecorder()
	newTestHandler(&fakeService{user: user}).Get(response,
		userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users/"+testUserID, ""))

	body := response.Body.String()
	if strings.Contains(body, "display_name") {
		t.Errorf("body = %q, want no display_name field", body)
	}
	if !strings.Contains(body, `"metadata":{}`) {
		t.Errorf("body = %q, want metadata as an empty object", body)
	}
}

func TestListReturnsAPageAndCursor(t *testing.T) {
	fake := &fakeService{page: Page{Users: []User{sampleUser()}, NextCursor: "opaque"}}
	request := userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users?limit=10&cursor=opaque", "")
	response := httptest.NewRecorder()

	newTestHandler(fake).List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if fake.options.Limit != 10 || fake.options.Cursor != "opaque" {
		t.Errorf("options = %+v, want limit 10 and the cursor", fake.options)
	}

	var body listResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}
	if len(body.Data) != 1 || body.NextCursor != "opaque" {
		t.Errorf("body = %+v, want one user and the cursor", body)
	}
}

func TestListReturnsAnEmptyArray(t *testing.T) {
	response := httptest.NewRecorder()
	newTestHandler(&fakeService{}).List(response,
		userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users", ""))

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
				userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users"+test.query, ""))

			assertError(t, response, http.StatusBadRequest, api.CodeInvalidRequest)
		})
	}
}

// An infrastructure failure never reaches a client as anything but a generic error.
func TestUnexpectedFailuresAreHidden(t *testing.T) {
	fake := &fakeService{err: errors.New("connection refused to 10.0.0.5:5432")}
	response := httptest.NewRecorder()

	newTestHandler(fake).Get(response,
		userRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/users/"+testUserID, ""))

	assertError(t, response, http.StatusInternalServerError, api.CodeInternal)
	if strings.Contains(response.Body.String(), "10.0.0.5") {
		t.Errorf("body = %q, want no infrastructure detail", response.Body.String())
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

/*
TestUpdateSeparatesAbsentFromEmpty proves the transport preserves the
difference a partial update depends on.

An omitted field must reach the service as nil so it keeps its stored value,
while an explicitly empty one must arrive as a present, empty value. Collapsing
the two would silently erase attributes a client never mentioned.
*/
func TestUpdateSeparatesAbsentFromEmpty(t *testing.T) {
	tests := map[string]struct {
		body        string
		wantName    *string
		hasMetadata bool
	}{
		"only display name": {`{"display_name":"Ada King"}`, pointerTo("Ada King"), false},
		"only metadata":     {`{"metadata":{"plan":"pro"}}`, nil, true},
		"cleared name":      {`{"display_name":""}`, pointerTo(""), false},
		"cleared metadata":  {`{"metadata":{}}`, nil, true},
		"both together":     {`{"display_name":"Ada","metadata":{}}`, pointerTo("Ada"), true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser()}
			response := httptest.NewRecorder()

			newTestHandler(fake).Update(response,
				userRequest(http.MethodPatch, "/v1/applications/"+testApplicationID+"/users/"+testUserID, test.body))

			if response.Code != http.StatusOK {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
			}

			switch {
			case test.wantName == nil && fake.attributes.DisplayName != nil:
				t.Errorf("DisplayName = %q, want it absent", *fake.attributes.DisplayName)
			case test.wantName != nil && fake.attributes.DisplayName == nil:
				t.Error("DisplayName is absent, want it present")
			case test.wantName != nil && *fake.attributes.DisplayName != *test.wantName:
				t.Errorf("DisplayName = %q, want %q", *fake.attributes.DisplayName, *test.wantName)
			}

			if (fake.attributes.Metadata != nil) != test.hasMetadata {
				t.Errorf("metadata present = %t, want %t", fake.attributes.Metadata != nil, test.hasMetadata)
			}
		})
	}
}

func pointerTo[T any](value T) *T {
	return &value
}

/*
TestUpdateForwardsTheEntityTag proves a conditional update reaches the service
as a condition rather than being dropped at the transport layer.
*/
func TestUpdateForwardsTheEntityTag(t *testing.T) {
	tests := map[string]struct {
		header string
		want   string
	}{
		"strong validator": {`"abc123"`, "abc123"},
		"unquoted":         {"abc123", "abc123"},
		"any version":      {"*", ""},
		"weak validator":   {`W/"abc123"`, ""},
		"absent":           {"", ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser()}
			request := userRequest(http.MethodPatch,
				"/v1/applications/"+testApplicationID+"/users/"+testUserID, `{"display_name":"Ada"}`)
			if test.header != "" {
				request.Header.Set("If-Match", test.header)
			}

			newTestHandler(fake).Update(httptest.NewRecorder(), request)

			if fake.expectedVersion != test.want {
				t.Errorf("expectedVersion = %q, want %q", fake.expectedVersion, test.want)
			}
		})
	}
}

// TestLifecycleHandlersApplyTheirOwnTransition guards against the two endpoints
// being wired to the same service method.
func TestLifecycleHandlersApplyTheirOwnTransition(t *testing.T) {
	tests := map[string]struct {
		invoke func(*Handler, http.ResponseWriter, *http.Request)
		want   Status
	}{
		"suspend":  {(*Handler).Suspend, StatusSuspended},
		"activate": {(*Handler).Activate, StatusActive},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser()}
			response := httptest.NewRecorder()

			test.invoke(newTestHandler(fake), response,
				userRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/users/"+testUserID+"/"+name, ""))

			if response.Code != http.StatusOK {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
			}
			if fake.transitionedTo != test.want {
				t.Errorf("transitioned to %q, want %q", fake.transitionedTo, test.want)
			}
			if response.Header().Get("ETag") == "" {
				t.Error("no ETag, want the new version so a client can make its next update conditional")
			}
		})
	}
}

// TestDeleteAnswersWithoutABody proves deletion reports success by status alone.
func TestDeleteAnswersWithoutABody(t *testing.T) {
	fake := &fakeService{}
	response := httptest.NewRecorder()

	newTestHandler(fake).Delete(response,
		userRequest(http.MethodDelete, "/v1/applications/"+testApplicationID+"/users/"+testUserID, ""))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Body.Len() != 0 {
		t.Errorf("body = %q, want it empty", response.Body.String())
	}
	if fake.requestedID != testUserID {
		t.Errorf("requested %q, want the path value %q", fake.requestedID, testUserID)
	}
}

/*
TestDomainErrorsBecomeTheirPublicStatus proves each declared domain error keeps
its own public meaning instead of collapsing into a generic failure.
*/
func TestDomainErrorsBecomeTheirPublicStatus(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
		code   api.ErrorCode
	}{
		"stale version":   {ErrPreconditionFailed, http.StatusPreconditionFailed, api.CodePreconditionFailed},
		"deleted subject": {ErrSubjectDeleted, http.StatusConflict, api.CodeConflict},
		"missing user":    {ErrNotFound, http.StatusNotFound, api.CodeNotFound},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{err: test.err}
			response := httptest.NewRecorder()

			newTestHandler(fake).Update(response,
				userRequest(http.MethodPatch,
					"/v1/applications/"+testApplicationID+"/users/"+testUserID, `{"display_name":"Ada"}`))

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d", response.Code, test.status)
			}

			var body struct {
				Error api.ErrorBody `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode the error body: %v", err)
			}
			if body.Error.Code != test.code {
				t.Errorf("code = %q, want %q", body.Error.Code, test.code)
			}
		})
	}
}
