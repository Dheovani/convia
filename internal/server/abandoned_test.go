package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"convia/internal/api"
)

/*
TestACallerThatWentAwayDoesNotSpendTheBudget keeps a page's own cancelled
requests from locking its person out.

A browser cancels requests routinely — asking again before an answer arrived,
or being left — and one cancelled while its credential was being read used to be
charged as a failed attempt. A real page reached the limit that way during a
call and was refused when it asked to leave.
*/
func TestACallerThatWentAwayDoesNotSpendTheBudget(t *testing.T) {
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
		newAuthenticatedDependency(
			stubApplications{application: sampleApplication()},
			stubUsers{user: sampleUser()},
			stubCredentials{credential: sampleCredential()},
			stubAuthenticator{err: context.Canceled})).Handler

	for attempt := 1; attempt <= authFailureBurst*3; attempt++ {
		gone, cancel := context.WithCancel(context.Background())
		cancel()

		response := httptest.NewRecorder()
		handler.ServeHTTP(response,
			authenticatedRequest(http.MethodGet, api.Prefix+"/users", "").WithContext(gone))

		if response.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d was made to wait for failures nobody made", attempt)
		}
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status code = %d, want the refusal an unverified request gets",
				attempt, response.Code)
		}
	}
}
