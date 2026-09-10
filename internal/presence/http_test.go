package presence

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/users"
)

/*
surface builds the tenant handler over a real service and an in-process store.

Nothing is stubbed below the handler, because there is nothing here worth
stubbing: the in-process store is a real implementation of the same contract
the shared one satisfies, and these tests are about what a caller sees.
*/
func surface(t *testing.T) (*TenantHandler, func(time.Duration)) {
	t.Helper()

	store, advance := held(t)
	service := NewService(store, servedTenant{active: true},
		knownPeople{user: active()}, &heard{}, quiet())

	return NewTenantHandler(quiet(), service), advance
}

// both is the pair of scopes an application that reports and reads presence
// would hold.
func both() []credentials.Scope {
	return []credentials.Scope{credentials.ScopePresenceRead, credentials.ScopePresenceWrite}
}

// asserted reads the presence out of a response body.
func asserted(t *testing.T, response *httptest.ResponseRecorder) presenceResponse {
	t.Helper()

	var body presenceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read the response %q: %v", response.Body.String(), err)
	}
	return body
}

/*
put sends a heartbeat for one device, with the path values the router would
have set.

The identifier is escaped in the target and set unescaped as the path value,
which is what ServeMux does — so a device identifier this surface has to refuse
can still be expressed as a request.
*/
func put(t *testing.T, handler *TenantHandler, device, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPut,
		"/v1/users/usr_1/presence/"+url.PathEscape(device), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("user_id", "usr_1")
	request.SetPathValue("device_id", device)
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", CredentialID: "cred_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.Assert(response, request)
	return response
}

/*
TestAHeartbeatAnswersWithThePersonRatherThanTheDevice is the shape decision
this surface turns on.

An application asserts per device because a person has several. What it needs
back is the answer it would otherwise have to compute: what Convia will now say
about the person.
*/
func TestAHeartbeatAnswersWithThePersonRatherThanTheDevice(t *testing.T) {
	handler, _ := surface(t)

	response := put(t, handler, "laptop", `{"state":"online"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT presence = %d, body %s", response.Code, response.Body)
	}

	body := asserted(t, response)
	if body.UserID != "usr_1" || body.State != string(StateOnline) {
		t.Errorf("answered %+v, want usr_1 online", body)
	}
	if body.Since == nil || body.ExpiresAt == nil {
		t.Errorf("an online presence omitted its times: %+v", body)
	}

	// The device the claim came from is not part of what Convia reports back.
	if strings.Contains(response.Body.String(), "laptop") {
		t.Errorf("the response names the device: %s", response.Body)
	}
}

/*
TestAnOfflinePersonCarriesNoTimes checks the absence the contract promises.

There is no moment at which an absence began — nobody is saying anything, and
Convia deliberately does not remember when they stopped — and nothing is
pending, so there is nothing to expire.
*/
func TestAnOfflinePersonCarriesNoTimes(t *testing.T) {
	handler, _ := surface(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/users/usr_1/presence", nil)
	request.SetPathValue("user_id", "usr_1")
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.Get(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET presence = %d, body %s", response.Code, response.Body)
	}

	body := asserted(t, response)
	if body.State != string(StateOffline) {
		t.Errorf("state = %q, want %q", body.State, StateOffline)
	}
	if body.Since != nil || body.ExpiresAt != nil {
		t.Errorf("an offline presence carries times: %+v", body)
	}
}

/*
TestALifetimeOutsideTheBoundsIsRefusedWithTheBounds is a message somebody
reads while debugging.

A refusal that only says "invalid" leaves a caller guessing. This one says what
the two numbers are, because the caller cannot find them anywhere in its own
code.
*/
func TestALifetimeOutsideTheBoundsIsRefusedWithTheBounds(t *testing.T) {
	handler, _ := surface(t)

	response := put(t, handler, "laptop", `{"state":"online","lifetime_seconds":3600}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT with an hour = %d, want %d", response.Code, http.StatusBadRequest)
	}

	body := response.Body.String()
	if !strings.Contains(body, "10") || !strings.Contains(body, "300") {
		t.Errorf("the refusal does not say what the bounds are: %s", body)
	}
}

// TestAssertingOfflineIsRefusedWithTheRemedy keeps a caller from guessing at an
// operation Convia already has.
func TestAssertingOfflineIsRefusedWithTheRemedy(t *testing.T) {
	handler, _ := surface(t)

	response := put(t, handler, "laptop", `{"state":"offline"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT offline = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if !strings.Contains(response.Body.String(), "Delete the presence instead") {
		t.Errorf("the refusal does not say what to do instead: %s", response.Body)
	}
}

// TestADeviceIdentifierTheRouterWouldAcceptButConviaWillNot covers the gap
// between a path segment and a value Convia is willing to store.
func TestADeviceIdentifierTheRouterWouldAcceptButConviaWillNot(t *testing.T) {
	handler, _ := surface(t)

	response := put(t, handler, "andré's phone", `{"state":"online"}`)
	if response.Code != http.StatusBadRequest {
		t.Errorf("PUT with an unusable device identifier = %d, want %d",
			response.Code, http.StatusBadRequest)
	}
}

/*
TestWithdrawingOneDeviceReportsWhatTheOthersStillSay is why these operations
answer with a body at all.

Closing a tab does not usually take somebody offline. A client that assumed a
`204` meant it did would show the wrong thing until its next read.
*/
func TestWithdrawingOneDeviceReportsWhatTheOthersStillSay(t *testing.T) {
	handler, _ := surface(t)

	put(t, handler, "laptop", `{"state":"busy"}`)
	put(t, handler, "phone", `{"state":"online"}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/users/usr_1/presence/laptop", nil)
	request.SetPathValue("user_id", "usr_1")
	request.SetPathValue("device_id", "laptop")
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.Withdraw(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("DELETE one device = %d, body %s", response.Code, response.Body)
	}
	if body := asserted(t, response); body.State != string(StateOnline) {
		t.Errorf("after withdrawing the busy device = %q, want %q", body.State, StateOnline)
	}
}

// TestSigningOutTakesEverything covers the other delete, which is the one an
// application sends when somebody leaves rather than when a tab closes.
func TestSigningOutTakesEverything(t *testing.T) {
	handler, _ := surface(t)

	put(t, handler, "laptop", `{"state":"busy"}`)
	put(t, handler, "phone", `{"state":"online"}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/users/usr_1/presence", nil)
	request.SetPathValue("user_id", "usr_1")
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.Forget(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("DELETE all = %d, body %s", response.Code, response.Body)
	}
	if body := asserted(t, response); body.State != string(StateOffline) {
		t.Errorf("after signing out = %q, want %q", body.State, StateOffline)
	}
}

// TestReadingSeveralPeopleAnswersInTheOrderAsked keeps a client from having to
// match rows back to the identifiers it sent.
func TestReadingSeveralPeopleAnswersInTheOrderAsked(t *testing.T) {
	handler, _ := surface(t)
	put(t, handler, "laptop", `{"state":"busy"}`)

	request := httptest.NewRequest(http.MethodGet, "/v1/presence?user_id=usr_1&user_id=usr_1", nil)
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /v1/presence = %d, body %s", response.Code, response.Body)
	}

	var body listResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read the response: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("answered for %d, want 2", len(body.Data))
	}
	for index, row := range body.Data {
		if row.State != string(StateBusy) {
			t.Errorf("row %d = %q, want %q", index, row.State, StateBusy)
		}
	}
}

// TestReadingNobodyIsRefused keeps a request that could never mean anything
// from being answered with an empty list that looks like an answer.
func TestReadingNobodyIsRefused(t *testing.T) {
	handler, _ := surface(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/presence", nil)
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.List(response, request)

	if response.Code != http.StatusBadRequest {
		t.Errorf("GET /v1/presence naming nobody = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

/*
TestAnUnreachableStoreIsServiceUnavailableWithARetryAfter is the answer that
tells a caller what to do.

Everywhere else in Convia a store that cannot be reached is an internal error,
because there is a durable answer that should have been available. Presence has
none, so this is a condition that will pass rather than a fault the caller can
do nothing about — and the header says when to come back.
*/
func TestAnUnreachableStoreIsServiceUnavailableWithARetryAfter(t *testing.T) {
	service := NewService(unreachable{}, servedTenant{active: true},
		knownPeople{user: active()}, &heard{}, quiet())
	handler := NewTenantHandler(quiet(), service)

	request := httptest.NewRequest(http.MethodGet, "/v1/users/usr_1/presence", nil)
	request.SetPathValue("user_id", "usr_1")
	request = request.WithContext(credentials.ContextWithPrincipal(request.Context(),
		credentials.Principal{ApplicationID: "app_1", Scopes: both()}))

	response := httptest.NewRecorder()
	handler.Get(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET with no store = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("Retry-After") == "" {
		t.Error("the refusal does not say when to try again")
	}

	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatalf("read the failure: %v", err)
	}
	if failure.Error.Code != string(api.CodeUnavailable) {
		t.Errorf("code = %q, want %q", failure.Error.Code, api.CodeUnavailable)
	}
}

/*
TestASuspendedUserIsAConflictRatherThanAnAbsence keeps two different answers
apart.

A person the application withdrew exists and is known. Reporting them as
missing would send an integration looking for a bug in its own directory.
*/
func TestASuspendedUserIsAConflictRatherThanAnAbsence(t *testing.T) {
	suspended := active()
	suspended.Status = users.StatusSuspended

	store, _ := held(t)
	service := NewService(store, servedTenant{active: true},
		knownPeople{user: suspended}, &heard{}, quiet())
	handler := NewTenantHandler(quiet(), service)

	response := put(t, handler, "laptop", `{"state":"online"}`)
	if response.Code != http.StatusConflict {
		t.Errorf("PUT for a suspended user = %d, want %d", response.Code, http.StatusConflict)
	}
}

// TestARouteReachedWithoutAPrincipalIsRefused covers the wiring mistake, which
// must never be served as if the caller had proved something.
func TestARouteReachedWithoutAPrincipalIsRefused(t *testing.T) {
	handler, _ := surface(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/users/usr_1/presence", nil)
	request.SetPathValue("user_id", "usr_1")

	response := httptest.NewRecorder()
	handler.Get(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Errorf("a route reached without a principal = %d, want %d",
			response.Code, http.StatusUnauthorized)
	}
}
