package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/*
installation serves what a Convia serves of the two routes Reach asks about, so
that a test can change one of them and leave the other right.
*/
type installation struct {
	health   func(http.ResponseWriter)
	sessions func(http.ResponseWriter)
}

func serving(t *testing.T, what installation) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/health":
			what.health(response)
		case "/v1/me":
			what.sessions(response)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func healthy(response http.ResponseWriter) {
	_, _ = response.Write([]byte(`{"status":"ok"}`))
}

func unauthenticated(response http.ResponseWriter) {
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated","message":"This request needs a session."}}`))
}

/*
TestAConviaIsRecognizedByWhatItRefuses.

An installed application has no address bar, so this check is what it has
instead: something at this address serves Convia's health and refuses a request
that carries no session in Convia's own vocabulary. It runs before anybody is
asked for a password, which is the whole point â€” a typo must not become a
credential somebody else holds.
*/
func TestAConviaIsRecognizedByWhatItRefuses(t *testing.T) {
	address := serving(t, installation{health: healthy, sessions: unauthenticated})

	client, err := Reach(context.Background(), address, nil)
	if err != nil {
		t.Fatalf("Reach() error = %v", err)
	}
	if client.Address() != address {
		t.Errorf("Reach() returned a client for %q, want %q", client.Address(), address)
	}
	if client.Session() != "" {
		t.Error("reaching an installation held a session")
	}
}

/*
TestSomethingElseAtThatAddressIsSaidToBeSomethingElse.

The ordinary mistake is a typo or a stale bookmark, and what answers is a web
server, a login portal or a proxy. Each of them is happy to take a password.
*/
func TestSomethingElseAtThatAddressIsSaidToBeSomethingElse(t *testing.T) {
	somethingElse := map[string]installation{
		"a session surface that lets anybody in": {
			health:   healthy,
			sessions: func(response http.ResponseWriter) { _, _ = response.Write([]byte(`{"username":"anybody"}`)) },
		},
		"a server that answers everything": {
			health:   func(response http.ResponseWriter) { _, _ = response.Write([]byte(`{"ok":true}`)) },
			sessions: func(response http.ResponseWriter) { _, _ = response.Write([]byte(`{}`)) },
		},
		"a health check that is not Convia's": {
			health:   func(response http.ResponseWriter) { _, _ = response.Write([]byte(`{"status":"UP"}`)) },
			sessions: unauthenticated,
		},
		"a page where the API should be": {
			health:   healthy,
			sessions: func(response http.ResponseWriter) { response.WriteHeader(http.StatusBadGateway) },
		},
		"an installation that is not there": {
			health:   func(response http.ResponseWriter) { response.WriteHeader(http.StatusNotFound) },
			sessions: unauthenticated,
		},
	}

	for what, serves := range somethingElse {
		address := serving(t, serves)

		_, err := Reach(context.Background(), address, nil)

		var wrong *NotConvia
		if !errors.As(err, &wrong) {
			t.Errorf("%s: Reach() error = %v, want it named as not a Convia", what, err)
		}
	}
}

/*
TestAConviaNobodyCanSignInToIsSaidSoInThoseWords.

An installation serves accounts only when it is configured with a first-party
application. One that is not is a real Convia â€” it answers health, it serves
its API â€” and there is still nothing here for a person. Telling them it is "not
a Convia" would send them looking for a typo that is not there.
*/
func TestAConviaNobodyCanSignInToIsSaidSoInThoseWords(t *testing.T) {
	address := serving(t, installation{
		health:   healthy,
		sessions: func(response http.ResponseWriter) { response.WriteHeader(http.StatusNotFound) },
	})

	_, err := Reach(context.Background(), address, nil)

	var wrong *NotConvia
	if !errors.As(err, &wrong) {
		t.Fatalf("Reach() error = %v, want it refused", err)
	}
	if !strings.Contains(wrong.Because, "accounts") {
		t.Errorf("Reach() said %q, which does not say the installation offers no accounts", wrong.Because)
	}
}

/*
TestAnInstallationThatIsNotThereIsUnreachableRatherThanWrong.

One that is starting, or behind a network that is down, is worth trying again.
An address where something else answers is not, and the two must not be given
the same words.
*/
func TestAnInstallationThatIsNotThereIsUnreachableRatherThanWrong(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	_, err := Reach(context.Background(), address, nil)

	var unreachable *Unreachable
	if !errors.As(err, &unreachable) {
		t.Errorf("Reach() error = %v, want it unreachable", err)
	}
}

/*
TestAnAddressIsRefusedBeforeAnythingIsAskedOfIt.

Plain HTTP to anywhere but this machine carries the session in a header across
the network in the clear. It is refused where it is typed, so no request is
ever made to it.
*/
func TestAnAddressIsRefusedBeforeAnythingIsAskedOfIt(t *testing.T) {
	for _, typed := range []string{"", "http://convia.example", "https://"} {
		if _, err := Reach(context.Background(), typed, nil); err == nil {
			t.Errorf("Reach(%q) succeeded", typed)
		}
	}
}
