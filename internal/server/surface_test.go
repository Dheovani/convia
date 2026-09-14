package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"convia/internal/sessions"
)

// wildcard matches the {name} placeholders in a route pattern.
var wildcard = regexp.MustCompile(`\{[^}]*\}`)

/*
TestEveryAuthenticatedRouteRefusesWithoutACredential is the tripwire that makes
an unwired surface impossible to ship.

The older refusal tests name the routes they cover and filter by surface, which
means a surface added later is silently excluded from them: the routes would be
registered, served with no middleware in front, and every existing test would
still pass. That is exactly the failure the `default: panic` in [handler] also
guards, and a panic only fires if somebody reaches the switch at all — a route
declared on an existing surface and wrapped by nothing would slip past it.

So this walks the whole table, takes every route that claims to demand a
credential, and sends it one with none. Nothing is named here; adding a route
adds a case.
*/
func TestEveryAuthenticatedRouteRefusesWithoutACredential(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	authenticated := 0
	for _, entry := range routeTable(logger, testDependencies()) {
		if !entry.authenticated() {
			continue
		}
		authenticated++

		t.Run(entry.method+" "+entry.path, func(t *testing.T) {
			/*
				The log is watched as well as the status, and that is what
				makes this test mean anything.

				A 401 alone proves almost nothing: every handler on an
				authenticated route defends itself by refusing a request that
				arrives with no principal in its context. So a surface wired to
				nothing still answers 401 — the handler catches what the
				middleware should have. The handler says so, at error level,
				because reaching it that way is a wiring mistake; watching for
				that line is how this tells the two apart.
			*/
			var transcript bytes.Buffer
			watched := slog.New(slog.NewJSONHandler(&transcript, nil))

			/*
				The handlers have to write to the same place, because the line
				this test looks for comes from a handler rather than from the
				chain in front of it.
			*/
			previous := dependencyLogger
			dependencyLogger = watched
			dependencies := testDependencies()
			dependencyLogger = previous

			/*
				A server of its own per route, because the failure budget is
				shared across every authenticated route and deliberately small.
				One handler for all of them would refuse the first sixty and
				rate-limit the rest, which would be this test exhausting the
				budget rather than the routes refusing.
			*/
			handler := New("127.0.0.1:0", watched, dependencies).Handler

			/*
				A wildcard is filled with something that merely matches the
				pattern. What the value is cannot matter: a request that gets
				as far as a handler has already failed this test.
			*/
			target := wildcard.ReplaceAllString(entry.path, "x")

			var body io.Reader
			request := httptest.NewRequest(entry.method, target, body)
			if entry.method != http.MethodGet && entry.method != http.MethodDelete {
				request = httptest.NewRequest(entry.method, target, strings.NewReader("{}"))
				request.Header.Set("Content-Type", "application/json")
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d: this route claims to require a credential "+
					"and served a request carrying none.\nbody: %s",
					response.Code, http.StatusUnauthorized, response.Body)
			}

			if strings.Contains(transcript.String(), "reached without a") {
				t.Errorf("the handler refused this request itself, which means nothing "+
					"authenticated it first. The route is on surface %d; check that handler() "+
					"wraps that surface.\nlog: %s", entry.surface, transcript.String())
			}
		})
	}

	/*
		A guard on the guard. If the table ever stops reporting any route as
		authenticated — a refactor of `authenticated`, a dependency left
		unwired in the fixture — this test would pass by examining nothing,
		which is the one way a tripwire can fail silently.
	*/
	if authenticated == 0 {
		t.Fatal("no route in the table claims to require a credential, so this test proved nothing")
	}
}

/*
TestTheSessionSurfaceOffersNoAuthenticationScheme covers the one deliberate
departure from RFC 9110 on a 401.

There is no registered `WWW-Authenticate` scheme for a cookie. The three bearer
surfaces send `Bearer`, which a client can act on; the session surface sends
nothing, because naming a scheme Convia does not accept would tell a browser to
present something that cannot work.
*/
func TestTheSessionSurfaceOffersNoAuthenticationScheme(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New("127.0.0.1:0", logger, testDependencies()).Handler

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Errorf("the session surface offered %q, and no scheme is registered for a cookie", challenge)
	}
}

// reachedByABrowser reports the surfaces a page calls, which are the ones the
// origin check and the no-state-on-GET rule apply to.
func reachedByABrowser(on surface) bool {
	return on == surfaceSession || on == surfaceSignIn || on == surfaceRegistration
}

/*
TestNoSessionRouteChangesStateOnAGet is what lets SameSite=Lax count as a CSRF
layer at all.

`SameSite=Lax` withholds the cookie from a cross-site POST, which is the whole
reason it is one of the three layers on this surface — but it **sends** the
cookie on a top-level GET navigation. A link, a `window.open`, a redirect. So a
GET that changed anything would be forgeable by any page on the internet, and
none of the other two layers would see it: there is no body to require JSON of,
and the Origin check deliberately exempts safe methods.

The invariant is therefore not a style preference. It is load-bearing, and it
is the kind that erodes the first time somebody adds a convenience route.
*/
func TestNoSessionRouteChangesStateOnAGet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, entry := range routeTable(logger, testDependencies()) {
		if !reachedByABrowser(entry.surface) {
			continue
		}

		if entry.method == http.MethodGet || entry.method == http.MethodHead {
			if strings.Contains(entry.path, "/me") && entry.method == http.MethodGet {
				continue // Reading who is signed in changes nothing.
			}
			t.Errorf("%s %s is a safe method on the session surface. If it changes anything, "+
				"SameSite=Lax will not protect it: the cookie rides along on a top-level "+
				"GET navigation.", entry.method, entry.path)
		}
	}
}

/*
TestEveryStateChangingSessionRouteChecksItsOrigin walks the table rather than
naming routes, because the layer it guards is the one a new route silently
misses.

`SameSite=Lax` is not the whole defence and was never claimed to be: it treats
a sibling subdomain as same-site, so a page on any host under the registrable
domain can make a cookie-carrying POST that Lax sends the session along with.
The exact-match Origin check is the layer that sees that, and a route which
forgets it is protected by nothing a sibling subdomain cannot defeat.

Nothing is named here. Adding a route to this surface adds a case.
*/
func TestEveryStateChangingSessionRouteChecksItsOrigin(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	checked := 0
	for _, entry := range routeTable(logger, testDependencies()) {
		if !reachedByABrowser(entry.surface) {
			continue
		}
		if entry.method == http.MethodGet || entry.method == http.MethodHead {
			continue
		}
		checked++

		t.Run(entry.method+" "+entry.path, func(t *testing.T) {
			target := wildcard.ReplaceAllString(entry.path, "x")

			request := httptest.NewRequest(entry.method, target, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://convia.example.attacker")
			request.AddCookie(&http.Cookie{
				Name:  sessions.CookieName,
				Value: "cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
			})

			response := httptest.NewRecorder()
			New("127.0.0.1:0", logger, testDependencies()).Handler.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d: this route changed state for a request that "+
					"came from another origin.\nbody: %s", response.Code, http.StatusForbidden, response.Body)
			}
		})
	}

	if checked == 0 {
		t.Fatal("no state-changing route on the session surface, so this test proved nothing")
	}
}
