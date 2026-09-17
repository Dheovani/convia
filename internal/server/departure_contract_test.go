package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/departure"
	"convia/internal/sessions"
)

// stubDeparture answers a deletion with err, and remembers the password it was given.
type stubDeparture struct {
	err      error
	password *accounts.Password
}

func (stub stubDeparture) Delete(_ context.Context, _ sessions.Principal, _ accounts.Identity,
	password accounts.Password) error {
	if stub.password != nil {
		*stub.password = password
	}
	return stub.err
}

func serveDeparture(stub stubDeparture, request *http.Request) *httptest.ResponseRecorder {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.Departures = departure.NewHandler(logger, stub, stubIdentities{})

	response := httptest.NewRecorder()
	New("127.0.0.1:0", logger, dependencies).Handler.ServeHTTP(response, request)
	return response
}

// TestDeletingAnAccountMatchesSpecification covers each answer the contract names.
func TestDeletingAnAccountMatchesSpecification(t *testing.T) {
	document := loadSpecification(t)
	operation := document.Paths.Find(api.Prefix + "/me/delete").Post
	target := api.Prefix + "/me/delete"

	tests := map[string]struct {
		body   string
		stub   stubDeparture
		status int
	}{
		"with the right password": {
			body:   `{"password":"correct horse battery"}`,
			status: http.StatusNoContent,
		},
		"with a wrong password": {
			body:   `{"password":"a guess"}`,
			stub:   stubDeparture{err: accounts.ErrWrongPassword},
			status: http.StatusForbidden,
		},
		"without a password": {
			body:   `{"password":""}`,
			stub:   stubDeparture{err: departure.ErrPasswordMissing},
			status: http.StatusBadRequest,
		},
		"with something else": {
			body:   `{"passphrase":"correct horse battery"}`,
			status: http.StatusBadRequest,
		},
		"while hashing is too busy": {
			body:   `{"password":"correct horse battery"}`,
			stub:   stubDeparture{err: accounts.ErrBusy},
			status: http.StatusServiceUnavailable,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := serveDeparture(test.stub, browser(http.MethodPost, target, test.body))
			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if test.status == http.StatusNoContent {
				return
			}
			assertBodyMatchesSchema(t, responseSchema(t, operation.Responses.Status(test.status)),
				response.Body.Bytes())
		})
	}
}

// TestADeletedAccountSignsTheBrowserOut clears the cookie, and only once the account is gone.
func TestADeletedAccountSignsTheBrowserOut(t *testing.T) {
	var given accounts.Password
	target := api.Prefix + "/me/delete"

	response := serveDeparture(stubDeparture{password: &given},
		browser(http.MethodPost, target, `{"password":"correct horse battery"}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if given != "correct horse battery" {
		t.Errorf("the service was given %q, want the password sent", string(given))
	}
	cleared := response.Header().Get("Set-Cookie")
	if !strings.Contains(cleared, sessions.CookieName+"=") || !strings.Contains(cleared, "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want the session cleared", cleared)
	}

	refused := serveDeparture(stubDeparture{err: accounts.ErrWrongPassword},
		browser(http.MethodPost, target, `{"password":"a guess"}`))
	if refused.Header().Get("Set-Cookie") != "" {
		t.Error("a refused deletion signed the browser out")
	}
}

// TestGuessingAPasswordByDeletingIsBudgeted charges each wrong password as a failed sign-in.
func TestGuessingAPasswordByDeletingIsBudgeted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.Departures = departure.NewHandler(logger, stubDeparture{err: accounts.ErrWrongPassword},
		stubIdentities{})
	handler := New("127.0.0.1:0", logger, dependencies).Handler

	guess := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, browser(http.MethodPost, api.Prefix+"/me/delete", `{"password":"a guess"}`))
		return response
	}

	for attempt := range signInFailureBurst {
		if response := guess(); response.Code != http.StatusForbidden {
			t.Fatalf("guess %d status = %d, want %d: %s", attempt+1, response.Code,
				http.StatusForbidden, response.Body)
		}
	}
	if response := guess(); response.Code != http.StatusTooManyRequests {
		t.Fatalf("guess %d status = %d, want %d", signInFailureBurst+1, response.Code,
			http.StatusTooManyRequests)
	}
}
