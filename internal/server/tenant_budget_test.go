package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
The two tenants these tests set against each other.

They are two applications rather than two keys on purpose: the budget is keyed
by the application, and a test written with two keys of the same tenant would
pass for the wrong reason.
*/
const (
	oneTenant   = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	otherTenant = "app_7QJ4ZKRN5XWD2MHF6TVB3YCPAS"

	oneKey      = "cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"
	oneOtherKey = "cvk_QP7KN2VJH6TBWMDR3YAFC5E4XZ_3TKPQ2MWZC7NVJ6BXRD4FGA5YH"
	otherKey    = "cvk_7KN2VJH6TBWMDR3YAFC5E4XZQP_KPQ2MWZC7NVJ6BXRD4FGA5YH3T"
)

// stubTenants authenticates a fixed set of keys, each as the application that
// holds it, so that one test can put two tenants behind the same installation.
type stubTenants map[string]credentials.Principal

func (stub stubTenants) Authenticate(_ context.Context, token string) (credentials.Principal, error) {
	principal, known := stub[token]
	if !known {
		return credentials.Principal{}, credentials.ErrUnauthenticated
	}
	return principal, nil
}

// twoTenants is the fixture: three keys, two applications, one of them holding
// two of the keys.
func twoTenants() stubTenants {
	return stubTenants{
		oneKey:      {ApplicationID: oneTenant, CredentialID: sampleCredential().ID, Scopes: credentials.Scopes()},
		oneOtherKey: {ApplicationID: oneTenant, CredentialID: "cred_QP7KN2VJH6TBWMDR3YAFC5E4XZ", Scopes: credentials.Scopes()},
		otherKey:    {ApplicationID: otherTenant, CredentialID: "cred_7KN2VJH6TBWMDR3YAFC5E4XZQP", Scopes: credentials.Scopes()},
	}
}

// budgeting serves every tenant route with the two tenants above and the
// per-minute budget a test wants to reach without making a million requests.
func budgeting(perMinute int) http.Handler {
	dependencies := testDependencies()
	dependencies.Authenticator = twoTenants()
	dependencies.TenantRequestsPerMinute = perMinute

	return New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).Handler
}

// asking addresses a tenant route with one tenant's key.
func asking(key, target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	return request
}

// spend makes count requests with one key and returns the last answer.
func spend(handler http.Handler, key, target string, count int) *httptest.ResponseRecorder {
	var response *httptest.ResponseRecorder
	for range count {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, asking(key, target))
	}
	return response
}

/*
TestAValidKeyIsNotAReasonToServeWithoutALimit is what `M23-013` adds.

Every budget before it charged a mistake, so an application presenting a working
key met none of them however fast it asked. That is the hole: a tenant does not
have to do anything wrong to take an installation's whole capacity, and the
requests it makes on the way are all successes.
*/
func TestAValidKeyIsNotAReasonToServeWithoutALimit(t *testing.T) {
	const budget = 20
	handler := budgeting(budget)

	if last := spend(handler, oneKey, api.Prefix+"/users", budget); last.Code != http.StatusOK {
		t.Fatalf("request %d status = %d, want %d: %s", budget, last.Code, http.StatusOK, last.Body)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, asking(oneKey, api.Prefix+"/users"))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d after %d requests with a working key, want %d",
			response.Code, budget, http.StatusTooManyRequests)
	}

	retry := response.Header().Get("Retry-After")
	if seconds, err := strconv.Atoi(retry); err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want whole seconds of at least 1", retry)
	}

	var body struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the error body: %v", err)
	}
	if body.Error.Code != api.CodeRateLimited {
		t.Errorf("code = %q, want %q", body.Error.Code, api.CodeRateLimited)
	}
	if body.Error.Message != tooManyTenantRequests {
		t.Errorf("message = %q, want %q", body.Error.Message, tooManyTenantRequests)
	}
}

/*
TestOneTenantCannotSpendAnother is the whole point of keying this by the
application.

A limit that one tenant's flood imposes on every other tenant is not fairness,
it is the outage being shared out. This is the test that fails if the budget is
ever keyed by the address, by the instance, or by anything else two tenants have
in common.
*/
func TestOneTenantCannotSpendAnother(t *testing.T) {
	const budget = 20
	handler := budgeting(budget)

	spend(handler, oneKey, api.Prefix+"/users", budget+5)

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, asking(oneKey, api.Prefix+"/users"))
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the flooding tenant was not refused: status = %d", refused.Code)
	}

	for attempt := 1; attempt <= budget; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, asking(otherKey, api.Prefix+"/users"))

		if response.Code != http.StatusOK {
			t.Fatalf("request %d of the other tenant: status = %d, want %d: %s",
				attempt, response.Code, http.StatusOK, response.Body)
		}
	}
}

/*
TestASecondKeyDoesNotBuyASecondBudget: a tenant issues its own keys.

Keying this by the credential would make the limit advisory — a tenant that met
it would mint another key and carry on, and nothing about doing so looks like
abuse from the inside. The application is the dimension a tenant cannot widen
without an operator, which is why it is the one that is counted.
*/
func TestASecondKeyDoesNotBuyASecondBudget(t *testing.T) {
	const budget = 20
	handler := budgeting(budget)

	spend(handler, oneKey, api.Prefix+"/users", budget)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, asking(oneOtherKey, api.Prefix+"/users"))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("a second key of the same application was served: status = %d, want %d",
			response.Code, http.StatusTooManyRequests)
	}
}

/*
TestTheTenantBudgetIsSharedAcrossRoutes: one limiter for the surface, for the
same reason the failure budget has one.

A budget per route is a budget multiplied by the number of routes, and the route
table grows.
*/
func TestTheTenantBudgetIsSharedAcrossRoutes(t *testing.T) {
	const budget = 20
	handler := budgeting(budget)

	targets := []string{
		api.Prefix + "/users",
		api.Prefix + "/credentials",
		api.Prefix + "/rooms",
		api.Prefix + "/calls",
	}

	limited := false
	for attempt := range budget + 1 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, asking(oneKey, targets[attempt%len(targets)]))

		if response.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}

	if !limited {
		t.Errorf("spending %d over %d endpoints was never refused, so each endpoint has its own budget",
			budget+1, len(targets))
	}
}

/*
TestAnUnsetBudgetIsTheDefaultRatherThanNone.

Dependencies is assembled by hand in a dozen places, and a zero that meant "no
limit" would make every one of them an unlimited installation — including,
eventually, one somebody deployed. Zero means the default, and this is where
that is nailed down.
*/
func TestAnUnsetBudgetIsTheDefaultRatherThanNone(t *testing.T) {
	dependencies := testDependencies()
	dependencies.Authenticator = twoTenants()
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).Handler

	/*
		The exact request the budget runs out on is not asserted, and cannot
		honestly be: the bucket refills while the loop runs, so the number
		depends on how fast the machine is. What is asserted is the pair that
		matters — the default is generous enough not to refuse ordinary use, and
		finite.
	*/
	if last := spend(handler, oneKey, api.Prefix+"/users", tenantBurst/2); last.Code != http.StatusOK {
		t.Fatalf("request %d of an unconfigured installation was refused: %d",
			tenantBurst/2, last.Code)
	}

	for attempt := tenantBurst / 2; attempt <= tenantBurst*2; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, asking(oneKey, api.Prefix+"/users"))

		if response.Code == http.StatusTooManyRequests {
			return
		}
	}

	t.Errorf("%d requests were served with no budget configured, so zero means no limit",
		tenantBurst*2)
}

/*
TestAPersonIsNotATenant.

The session surface carries no application, so nothing here reaches it. That is
deliberate rather than an oversight: a person's client asks on behalf of one
person, and a budget sized for a whole backend would be either useless there or
an outage. Limiting a person is its own question, and this test exists so that
answering it is a decision rather than a side effect of this one.
*/
func TestAPersonIsNotATenant(t *testing.T) {
	handler := budgeting(1)

	spend(handler, oneKey, api.Prefix+"/users", 5)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser(http.MethodGet, api.Prefix+"/me", ""))

	if response.Code != http.StatusOK {
		t.Errorf("a signed-in person got %d while a tenant was out of budget, want %d: %s",
			response.Code, http.StatusOK, response.Body)
	}
}
