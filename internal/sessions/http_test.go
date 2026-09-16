package sessions

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/accounts"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// stubService answers the handler without a database.
type stubService struct {
	account accounts.Account
	err     error

	endedAll bool
}

func (stub *stubService) Begin(context.Context, string, accounts.Password) (Session, string, error) {
	if stub.err != nil {
		return Session{}, "", stub.err
	}
	return Session{ID: "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E", AccountID: stub.account.ID}, "cvs_token", nil
}

func (stub *stubService) Register(context.Context, string, accounts.Password) (Session, string, error) {
	return stub.Begin(context.Background(), "", "")
}

func (stub *stubService) End(context.Context, string) error { return stub.err }

func (stub *stubService) EndAll(context.Context, string) (int, error) {
	stub.endedAll = true
	return 2, stub.err
}

func (stub *stubService) Account(context.Context, string) (accounts.Account, error) {
	return stub.account, stub.err
}

func (stub *stubService) ChangePassword(context.Context, Principal,
	accounts.Password, accounts.Password) (Session, string, error) {
	if stub.err != nil {
		return Session{}, "", stub.err
	}
	return Session{ID: "ses_2QRSTUVWXYZ234567ABCDEFGH"}, "cvs_rotated", nil
}

// somebody is an account in the state most responses show it in.
func somebody() accounts.Account {
	return accounts.Account{
		ID:       "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		Username: "ana",
		UserID:   "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		Status:   accounts.StatusActive,
	}
}

/*
signedIn builds a request carrying a verified session, as the middleware would
have left it.

It carries no Origin header, because nothing in this package reads one. Whether
a state-changing request came from Convia's own page is decided by the surface
in internal/server, in front of every handler here.
*/
func signedIn(method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}

	return request.WithContext(ContextWithPrincipal(request.Context(), Principal{
		SessionID:     "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		AccountID:     somebody().ID,
		UserID:        somebody().UserID,
		ApplicationID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
	}))
}

/*
TestEveryResponseOnThisSurfaceIsPrivate is what stops one person's data being
served to another from a cache.

Go sends no cache directives of its own, and HTTP permits a cache to keep a 200
that carries none. The concrete failure is signing out on a shared machine and
pressing Back.
*/
func TestEveryResponseOnThisSurfaceIsPrivate(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{account: somebody()})

	response := httptest.NewRecorder()
	handler.Me(response, signedIn(http.MethodGet, "/v1/me", ""))

	if store := response.Header().Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}
	if vary := response.Header().Get("Vary"); !strings.Contains(vary, "Cookie") {
		t.Errorf("Vary = %q, want it to include Cookie", vary)
	}
}

/*
TestSigningInRefusesEveryFailureIdentically is what keeps the form from being a
way to find out who has an account.
*/
func TestSigningInRefusesEveryFailureIdentically(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{err: accounts.ErrUnauthenticated})

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions",
		strings.NewReader(`{"username":"nobody","password":"whatever"}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	handler.SignIn(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	body := response.Body.String()
	for _, leak := range []string{"suspended", "not found", "does not exist", "password is"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("the refusal says which half was wrong: %s", body)
		}
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Error("a refused sign-in set a cookie")
	}
}

/*
TestBusyIsNotARefusal keeps a resource limit from reading as a wrong password.

Hashing is bounded so that a stranger cannot decide how much memory Convia
allocates. Reporting that as a 401 would tell somebody their password is wrong
when it may well be right.
*/
func TestBusyIsNotARefusal(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{err: accounts.ErrBusy})

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions",
		strings.NewReader(`{"username":"ana","password":"whatever"}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	handler.SignIn(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("Retry-After") == "" {
		t.Error("the refusal does not say when to try again")
	}
}

/*
TestRegisteringSignsTheNewAccountIn keeps somebody who has just chosen a
password from being asked to type it again, and says who they now are — handle
included, because that is what they will give other people.
*/
func TestRegisteringSignsTheNewAccountIn(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{account: somebody()})

	request := httptest.NewRequest(http.MethodPost, "/v1/accounts",
		strings.NewReader(`{"username":"ana","password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	handler.Register(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	if !strings.Contains(response.Header().Get("Set-Cookie"), "cvs_token") {
		t.Error("registering did not sign the new account in")
	}
	if handle := somebody().Handle(); !strings.Contains(response.Body.String(), `"handle":"`+handle+`"`) {
		t.Errorf("the response does not carry the handle %q: %s", handle, response.Body)
	}
}

// TestATakenUsernameIsAConflict rather than a refusal of credentials, and sets
// no cookie.
func TestATakenUsernameIsAConflict(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{err: accounts.ErrUsernameTaken})

	request := httptest.NewRequest(http.MethodPost, "/v1/accounts",
		strings.NewReader(`{"username":"ana","password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	handler.Register(response, request)

	if response.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Error("a refused registration set a cookie")
	}
}

/*
TestChangingAPasswordRotatesThisSessionToo is the consequence that makes the
operation worth trusting.

Somebody changing a password usually believes another party has it. Leaving the
current token alive would keep the leak alive in the very browser doing the
fixing.
*/
func TestChangingAPasswordRotatesThisSessionToo(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{account: somebody()})

	request := signedIn(http.MethodPatch, "/v1/me/password",
		`{"current_password":"old one here","new_password":"a new one entirely"}`)

	response := httptest.NewRecorder()
	handler.ChangePassword(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}

	cookie := response.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "cvs_rotated") {
		t.Errorf("the response did not carry a rotated session: %q", cookie)
	}
}

/*
TestAWrongCurrentPasswordIsNotAnEndedSession keeps a typo from signing somebody
out. A page that is told 401 believes its session is gone.
*/
func TestAWrongCurrentPasswordIsNotAnEndedSession(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{account: somebody(), err: accounts.ErrWrongPassword})

	request := signedIn(http.MethodPatch, "/v1/me/password",
		`{"current_password":"not it at all","new_password":"a new one entirely"}`)

	response := httptest.NewRecorder()
	handler.ChangePassword(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusForbidden, response.Body)
	}
	if !strings.Contains(response.Body.String(), `"code":"wrong_password"`) {
		t.Errorf("body = %s, want the wrong_password code", response.Body)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Error("a refused change touched the session cookie")
	}
}

/*
TestSigningOutEverywhereDoesNotSpareThisBrowser is the whole meaning of the
operation.
*/
func TestSigningOutEverywhereDoesNotSpareThisBrowser(t *testing.T) {
	service := &stubService{account: somebody()}
	handler := NewHandler(quiet(), service)

	response := httptest.NewRecorder()
	handler.SignOutEverywhere(response, signedIn(http.MethodDelete, "/v1/sessions", ""))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if !service.endedAll {
		t.Error("the service was not asked to end every session")
	}
	if cookie := response.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Max-Age=0") {
		t.Errorf("this browser kept its cookie: %q", cookie)
	}
}

// TestARouteReachedWithoutASessionIsRefused covers the wiring mistake, which
// must never be served as if somebody had proved who they are.
func TestARouteReachedWithoutASessionIsRefused(t *testing.T) {
	handler := NewHandler(quiet(), &stubService{account: somebody()})

	response := httptest.NewRecorder()
	handler.Me(response, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
