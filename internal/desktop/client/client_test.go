package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// asking is one request an installation received, with the body it carried.
type asking struct {
	*http.Request
	body string
}

// answering starts an installation that replies with one answer and records
// what it was asked.
func answering(t *testing.T, status int, body string) (*Client, *[]asking) {
	t.Helper()

	var asked []asking
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sent, _ := io.ReadAll(request.Body)
		asked = append(asked, asking{Request: request.Clone(context.Background()), body: string(sent)})
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		if body != "" {
			_, _ = response.Write([]byte(body))
		}
	}))
	t.Cleanup(server.Close)

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, &asked
}

/*
TestAnAddressIsReadTheWaySomebodyTypesIt is the first screen of the
application: somebody types where their Convia is, and gets it wrong in the
ordinary ways.
*/
func TestAnAddressIsReadTheWaySomebodyTypesIt(t *testing.T) {
	for typed, want := range map[string]string{
		"convia.example":            "https://convia.example",
		"  convia.example/  ":       "https://convia.example",
		"https://convia.example":    "https://convia.example",
		"https://convia.example:84": "https://convia.example:84",
		"http://localhost:8080":     "http://localhost:8080",
		"http://127.0.0.1:8080":     "http://127.0.0.1:8080",
	} {
		read, err := Address(typed)
		if err != nil || read != want {
			t.Errorf("Address(%q) = %q, %v, want %q", typed, read, err, want)
		}
	}

	for _, typed := range []string{"", "   ", "https://", "http://convia.example"} {
		if read, err := Address(typed); err == nil {
			t.Errorf("Address(%q) = %q, want a refusal", typed, read)
		}
	}
}

/*
TestSigningInHoldsTheSessionAndPresentsItInAHeader is what the whole
arrangement exists for: the application holds the session, and the interface
never sees it. See docs/adr/0019.
*/
func TestSigningInHoldsTheSessionAndPresentsItInAHeader(t *testing.T) {
	client, asked := answering(t, http.StatusCreated, `{"account_id":"acc_1","user_id":"usr_1",
		"username":"ana","handle":"ana#X","token":"cvs_session"}`)

	person, err := client.SignIn(context.Background(), "ana", "correct horse battery staple")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if person.Username != "ana" || client.Session() != "cvs_session" {
		t.Errorf("SignIn() = %+v holding %q, want ana's session", person, client.Session())
	}

	signingIn := (*asked)[0]
	if signingIn.URL.Path != "/v1/sessions" || signingIn.Method != http.MethodPost {
		t.Errorf("signed in with %s %s", signingIn.Method, signingIn.URL.Path)
	}
	if signingIn.Header.Get("Authorization") != "" {
		t.Error("signing in presented a session")
	}
	if signingIn.Header.Get("Origin") != "" {
		t.Error("the application claimed to be a page, which is what tells the two apart")
	}

	if _, err := client.Me(context.Background()); err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if held := (*asked)[1].Header.Get("Authorization"); held != "Bearer cvs_session" {
		t.Errorf("the session was presented as %q", held)
	}
	if cookie := (*asked)[1].Header.Get("Cookie"); cookie != "" {
		t.Errorf("the application sent a cookie: %q", cookie)
	}
}

// TestAnInstallationThatSignsInWithoutASessionIsRefused: the application would
// otherwise believe it was signed in and fail on every request afterwards.
func TestAnInstallationThatSignsInWithoutASessionIsRefused(t *testing.T) {
	client, _ := answering(t, http.StatusCreated, `{"account_id":"acc_1","user_id":"usr_1","username":"ana","handle":"ana#X"}`)

	if _, err := client.SignIn(context.Background(), "ana", "correct horse battery staple"); !errors.Is(err, ErrNoSession) {
		t.Errorf("SignIn() error = %v, want %v", err, ErrNoSession)
	}
	if client.Session() != "" {
		t.Error("a session was held although none was given")
	}
}

/*
TestSigningInAgainDropsTheSessionHeldBefore keeps one person from signing in
while the application still presents another's session, which is how somebody
ends up looking at rooms that are not theirs.
*/
func TestSigningInAgainDropsTheSessionHeldBefore(t *testing.T) {
	client, asked := answering(t, http.StatusUnauthorized,
		`{"error":{"code":"unauthenticated","message":"The username and password do not match an account."}}`)
	client.Resume("cvs_somebody_else")

	if _, err := client.SignIn(context.Background(), "bruno", "a wrong password"); err == nil {
		t.Fatal("SignIn() succeeded")
	}
	if held := (*asked)[0].Header.Get("Authorization"); held != "" {
		t.Errorf("signing in presented %q", held)
	}
	if client.Session() != "" {
		t.Errorf("the previous session survived a failed sign-in: %q", client.Session())
	}
}

// TestSigningOutForgetsTheSessionWhateverHappened: one the installation already
// ended is one this application must stop presenting.
func TestSigningOutForgetsTheSessionWhateverHappened(t *testing.T) {
	client, _ := answering(t, http.StatusInternalServerError, `{"error":{"code":"internal","message":"x"}}`)
	client.Resume("cvs_session")

	if err := client.SignOut(context.Background()); err == nil {
		t.Fatal("SignOut() succeeded")
	}
	if client.Session() != "" {
		t.Errorf("the session survived signing out: %q", client.Session())
	}
}

// TestChangingAPasswordKeepsThisApplicationSignedIn holds the rotated session,
// which the installation answers with rather than setting a cookie.
func TestChangingAPasswordKeepsThisApplicationSignedIn(t *testing.T) {
	client, _ := answering(t, http.StatusOK, `{"token":"cvs_rotated"}`)
	client.Resume("cvs_session")

	if err := client.ChangePassword(context.Background(), "old one here", "a new one entirely"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if client.Session() != "cvs_rotated" {
		t.Errorf("the application holds %q, want the rotated session", client.Session())
	}
}

/*
TestARefusalCarriesWhatConviaSaidAndNothingElse keeps the application from
inventing an explanation: a code it can branch on when Convia gave one, and no
code at all when the answer came from something else.
*/
func TestARefusalCarriesWhatConviaSaidAndNothingElse(t *testing.T) {
	client, _ := answering(t, http.StatusForbidden,
		`{"error":{"code":"wrong_password","message":"The password is not right.","request_id":"req_1"}}`)
	client.Resume("cvs_session")

	err := client.Do(context.Background(), http.MethodPost, "/me/delete", map[string]string{"password": "x"}, nil)
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Do() error = %v, want a refusal", err)
	}
	if refusal.Code != "wrong_password" || refusal.Status != http.StatusForbidden || refusal.RequestID != "req_1" {
		t.Errorf("the refusal is %+v", refusal)
	}
	if refusal.Unauthenticated() {
		t.Error("a wrong password was read as a session that ended")
	}

	gateway, _ := answering(t, http.StatusBadGateway, "<html>502</html>")
	err = gateway.Do(context.Background(), http.MethodGet, "/me", nil, nil)
	if !errors.As(err, &refusal) || refusal.Code != "" {
		t.Errorf("a gateway's answer became %v, want a refusal with no code", err)
	}
}

// TestAnInstallationThatDoesNotAnswerIsUnreachable rather than a refusal: there
// is nothing to show somebody about a request that never arrived.
func TestAnInstallationThatDoesNotAnswerIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server.Close()

	err = client.Do(context.Background(), http.MethodGet, "/me", nil, nil)
	var unreachable *Unreachable
	if !errors.As(err, &unreachable) {
		t.Errorf("Do() error = %v, want it unreachable", err)
	}
}

// TestACancelledRequestIsCancelledRatherThanUnreachable, so that a window
// closing is not reported to somebody as an installation being down.
func TestACancelledRequestIsCancelledRatherThanUnreachable(t *testing.T) {
	client, _ := answering(t, http.StatusOK, `{}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Do(ctx, http.MethodGet, "/me", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do() error = %v, want it cancelled", err)
	}
}

// TestTheBodyIsSentAsJSON covers the one piece of serialization this package does.
func TestTheBodyIsSentAsJSON(t *testing.T) {
	client, asked := answering(t, http.StatusCreated, `{"id":"room_1"}`)
	client.Resume("cvs_session")

	var room struct {
		ID string `json:"id"`
	}
	if err := client.Do(context.Background(), http.MethodPost, "/me/rooms",
		map[string]string{"name": "Standup"}, &room); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if room.ID != "room_1" {
		t.Errorf("the answer decoded to %+v", room)
	}

	sent := (*asked)[0]
	if sent.body != `{"name":"Standup"}` {
		t.Errorf("the body sent was %s", sent.body)
	}
	if sent.Header.Get("Content-Type") != "application/json" {
		t.Errorf("the body was sent as %q", sent.Header.Get("Content-Type"))
	}
}
