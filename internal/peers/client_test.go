package peers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/accounts"
	"convia/internal/webhooks"
)

// developmentClient may reach loopback, which is where httptest listens.
func developmentClient() *Client { return NewClient(webhooks.NewDestinations(true)) }

func TestTheClientSignsWhatItSends(t *testing.T) {
	identity, _ := accounts.NewIdentity()

	home := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		verified, err := verifySignature(request, body, time.Now())
		if err != nil || verified.AccountID != identity.ID() {
			http.Error(response, "unsigned", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = response.Write([]byte(`{"room_id":"room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`))
	}))
	defer home.Close()

	answer, err := developmentClient().Do(t.Context(), identity, http.MethodPost, home.URL,
		"/v1/peer/invitations/"+sampleInvitationID+"/accept?x=1", []byte(`{"username":"bia"}`))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if answer.Status != http.StatusOK || !strings.Contains(string(answer.Body), "room_7KQZP4XN2VJH6TBWMDR3YAFC5E") {
		t.Errorf("Do() = %d %s, want the home's verified answer", answer.Status, answer.Body)
	}
}

/*
TestAnythingButConviasAnswerIsUnreachable covers what a home that is not a
Convia, or not behaving as one, can do to the installation calling it.
*/
func TestAnythingButConviasAnswerIsUnreachable(t *testing.T) {
	identity, _ := accounts.NewIdentity()

	answers := map[string]http.HandlerFunc{
		"a page instead of JSON": func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "text/html")
			_, _ = response.Write([]byte("<html>login</html>"))
		},
		"a JSON array": func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`[1,2,3]`))
		},
		"an answer larger than Convia accepts": func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"x":"` + strings.Repeat("a", maxResponseBytes) + `"}`))
		},
	}

	/*
		A redirect to somewhere that answers exactly as a Convia would. Following it
		would succeed, which is why the test is built this way: a redirect to an
		address that fails anyway proves nothing about whether redirects are followed.
	*/
	elsewhere := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"room_id":"room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`))
	}))
	defer elsewhere.Close()
	answers["a redirect to something that answers like Convia"] = func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, elsewhere.URL+"/v1/peer/x", http.StatusFound)
	}

	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			home := httptest.NewServer(answer)
			defer home.Close()

			if _, err := developmentClient().Do(t.Context(), identity, http.MethodGet, home.URL, "/v1/peer/x", nil); !errors.Is(err, ErrUnreachable) {
				t.Errorf("Do() error = %v, want %v", err, ErrUnreachable)
			}
		})
	}
}

/*
TestTheClientRequestsOnlyAPeerPathOnAHome covers the check every request passes,
whatever its caller already did: each of these would have reached somewhere, or
reached the test server at a path that is not the peer surface.
*/
func TestTheClientRequestsOnlyAPeerPathOnAHome(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	reached := false
	home := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer home.Close()

	for name, request := range map[string]struct{ home, target string }{
		"a home with a path":                {home.URL + "/elsewhere", "/v1/peer/x"},
		"a home with credentials":           {strings.Replace(home.URL, "://", "://ana:secret@", 1), "/v1/peer/x"},
		"a home with a trailing slash":      {home.URL + "/", "/v1/peer/x"},
		"a target outside the peer surface": {home.URL, "/v1/users"},
		"a target that names another host":  {home.URL, "@elsewhere.example/v1/peer/x"},
		"a target with a traversal":         {home.URL, "/v1/peer/../users"},
		"a target with a new line":          {home.URL, "/v1/peer/x\r\nHost: elsewhere.example"},
		"a target with a fragment":          {home.URL, "/v1/peer/x#y"},
	} {
		_, err := developmentClient().Do(t.Context(), identity, http.MethodGet, request.home, request.target, nil)
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("%s: Do() error = %v, want %v", name, err, ErrUnreachable)
		}
	}
	if reached {
		t.Error("the client sent a request it should have refused")
	}
}

// TestARefusalIsReportedWithItsCode, so that the caller can tell a missing
// invitation from a home that is down.
func TestARefusalIsReportedWithItsCode(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	home := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
	}))
	defer home.Close()

	answer, err := developmentClient().Do(t.Context(), identity, http.MethodGet, home.URL, "/v1/peer/x", nil)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if answer.Status != http.StatusNotFound || answer.Code != "not_found" {
		t.Errorf("Do() = %+v, want a 404 carrying not_found", answer)
	}
}

/*
TestInDevelopmentThePrivateNetworkIsStillAChoice: development is the default
environment, so an installation left in it must not follow a link into the
network around it unless its operator allowed that.
*/
func TestInDevelopmentThePrivateNetworkIsStillAChoice(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	reached := false
	home := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer home.Close()

	client := NewClient(webhooks.NewDestinations(true).WithPrivateAddresses(false))
	if _, err := client.Do(t.Context(), identity, http.MethodGet, home.URL, "/v1/peer/x", nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("reaching loopback without the setting error = %v, want %v", err, ErrUnreachable)
	}
	if reached {
		t.Error("a development installation followed a link to loopback without being allowed to")
	}
}

/*
TestOutsideDevelopmentAnInstallationStaysOnThePublicInternet is the SSRF guard
applied to links: a production installation will not follow one to loopback,
nor over plain http.
*/
func TestOutsideDevelopmentAnInstallationStaysOnThePublicInternet(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	reached := false
	home := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer home.Close()

	production := NewClient(webhooks.NewDestinations(false))

	if _, err := production.Do(t.Context(), identity, http.MethodGet, home.URL, "/v1/peer/x", nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("reaching loopback over https error = %v, want %v", err, ErrUnreachable)
	}
	if _, err := production.Do(t.Context(), identity, http.MethodGet, "http://convia.example", "/v1/peer/x", nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("reaching a plain http home error = %v, want %v", err, ErrUnreachable)
	}
	if reached {
		t.Error("a production installation connected to a loopback address")
	}
}
