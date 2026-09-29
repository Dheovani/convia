package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"convia/internal/api"
	"convia/internal/sessions"
)

/*
Two sessions of one person, and one of somebody else.

The first pair is the test that matters: a stolen session and the phone it was
stolen from are two sessions of one account, and budgeting by the account would
make the thief's flood refuse the victim.
*/
const (
	onePhone   = "cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"
	oneLaptop  = "cvs_QP7KN2VJH6TBWMDR3YAFC5E4XZ_3TKPQ2MWZC7NVJ6BXRD4FGA5YH"
	otherPhone = "cvs_7KN2VJH6TBWMDR3YAFC5E4XZQP_KPQ2MWZC7NVJ6BXRD4FGA5YH3T"
)

// stubDevices authenticates a fixed set of cookies, so that one test can put
// two of somebody's devices and a stranger behind the same installation.
type stubDevices map[string]sessions.Principal

func (stub stubDevices) Authenticate(_ context.Context, token string) (sessions.Principal, error) {
	principal, known := stub[token]
	if !known {
		return sessions.Principal{}, sessions.ErrUnauthenticated
	}
	return principal, nil
}

func twoDevicesAndAStranger() stubDevices {
	person := samplePerson()
	stranger := samplePerson()
	stranger.AccountID = "acc_7KN2VJH6TBWMDR3YAFC5E4XZQP"
	stranger.UserID = "usr_7KN2VJH6TBWMDR3YAFC5E4XZQP"

	return stubDevices{
		onePhone:   {SessionID: "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E", AccountID: person.AccountID, UserID: person.UserID},
		oneLaptop:  {SessionID: "ses_QP7KN2VJH6TBWMDR3YAFC5E4XZ", AccountID: person.AccountID, UserID: person.UserID},
		otherPhone: {SessionID: "ses_7KN2VJH6TBWMDR3YAFC5E4XZQP", AccountID: stranger.AccountID, UserID: stranger.UserID},
	}
}

// rationing serves the session surface with a budget a test can reach.
func rationing(perMinute int) http.Handler {
	dependencies := testDependencies()
	dependencies.SessionAuthenticator = twoDevicesAndAStranger()
	dependencies.PersonRequestsPerMinute = perMinute

	return New("127.0.0.1:0", discardLogger(), dependencies).Handler
}

// asSession addresses a person-facing route with one device's cookie.
func asSession(token, target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: token})
	request.Header.Set("Origin", "https://convia.test")
	return request
}

func spendSession(handler http.Handler, token, target string, count int) *httptest.ResponseRecorder {
	var response *httptest.ResponseRecorder
	for range count {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, asSession(token, target))
	}
	return response
}

/*
TestAStolenSessionDoesNotThrottleThePhoneItWasStolenFrom.

**This is the property that took two milestones to arrive at**, and it is why
the key is the session rather than the account. A thief holding one of
somebody's sessions floods with it; budgeting by the account would refuse that
person's own phone at the same moment, so the attack would cost the victim
twice — which is exactly the denial of service `docs/threat-model.md` refused to
build.
*/
func TestAStolenSessionDoesNotThrottleThePhoneItWasStolenFrom(t *testing.T) {
	const budget = 20
	handler := rationing(budget)

	spendSession(handler, oneLaptop, api.Prefix+"/me", budget+5)

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, asSession(oneLaptop, api.Prefix+"/me"))
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the flooding session was not refused: status = %d", refused.Code)
	}

	for attempt := 1; attempt <= budget; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, asSession(onePhone, api.Prefix+"/me"))

		if response.Code != http.StatusOK {
			t.Fatalf("request %d from the same person's other device: status = %d, want %d: %s",
				attempt, response.Code, http.StatusOK, response.Body)
		}
	}
}

// TestOneSessionCannotSpendAnother: the same property across people, which is
// the easier half and still worth pinning.
func TestOneSessionCannotSpendAnother(t *testing.T) {
	const budget = 20
	handler := rationing(budget)

	spendSession(handler, onePhone, api.Prefix+"/me", budget+5)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, asSession(otherPhone, api.Prefix+"/me"))

	if response.Code != http.StatusOK {
		t.Errorf("a stranger was refused because somebody else flooded: status = %d", response.Code)
	}
}

/*
TestASignedInPersonIsBoundedAtAll.

What this closes is that a request which succeeds used to cost a person
nothing, however many they made — the tenant budget names an application and a
session names none.
*/
func TestASignedInPersonIsBoundedAtAll(t *testing.T) {
	const budget = 20
	handler := rationing(budget)

	if last := spendSession(handler, onePhone, api.Prefix+"/me", budget); last.Code != http.StatusOK {
		t.Fatalf("request %d status = %d, want %d: %s", budget, last.Code, http.StatusOK, last.Body)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, asSession(onePhone, api.Prefix+"/me"))

	if response.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d after %d requests, want %d", response.Code, budget, http.StatusTooManyRequests)
	}
	if retry := response.Header().Get("Retry-After"); retry == "" {
		t.Error("no Retry-After on a 429")
	}
}

/*
TestATenantIsNotChargedAPersonsBudget.

The two surfaces are separate limits for separate units. A person's requests
must not spend what an application's backend needs, and the reverse.
*/
func TestATenantIsNotChargedAPersonsBudget(t *testing.T) {
	handler := rationing(1)

	spendSession(handler, onePhone, api.Prefix+"/me", 5)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, api.Prefix+"/users", ""))

	if response.Code != http.StatusOK {
		t.Errorf("a tenant got %d while a session was out of budget, want %d: %s",
			response.Code, http.StatusOK, response.Body)
	}
}
