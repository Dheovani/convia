package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/api"
)

// browser builds the request a signed-in person's browser would make, carrying
// a session cookie and the origin of the page that made it.
func browser(method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	return asPerson(request)
}

func serveBrowser(request *http.Request) *httptest.ResponseRecorder {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	response := httptest.NewRecorder()
	New("127.0.0.1:0", logger, testDependencies()).Handler.ServeHTTP(response, request)
	return response
}

/*
TestAStateChangingRequestMustComeFromConviasOwnPage is the CSRF layer that
actually carries the browser surfaces.

SameSite=Lax does not see a sibling subdomain, because a sibling is *same-site*.
The JSON content-type requirement does nothing for a route with no body. So the
Origin check is what is left, and it has to refuse an absent header rather than
wave it through — a request without one did not come from a page.

The cases are the near misses on purpose. Each is a way an exact comparison
could be weakened into a prefix, a suffix, or a host-only match, and each of
those weakenings lets exactly one of these through.
*/
func TestAStateChangingRequestMustComeFromConviasOwnPage(t *testing.T) {
	refused := map[string]string{
		"absent":              "",
		"null":                "null",
		"a sibling subdomain": "http://docs.example.com",
		"another scheme":      "https://example.com",
		"a suffix of ours":    "http://evil-example.com",
		"a prefix of ours":    "http://example.com.evil.test",
	}

	for name, origin := range refused {
		t.Run(name, func(t *testing.T) {
			request := browser(http.MethodDelete, api.Prefix+"/sessions/current", "")
			if origin == "" {
				request.Header.Del("Origin")
			} else {
				request.Header.Set("Origin", origin)
			}

			response := serveBrowser(request)

			if response.Code != http.StatusForbidden {
				t.Errorf("Origin %q was accepted: status = %d", origin, response.Code)
			}
		})
	}
}

/*
TestOurOwnOriginIsAccepted is the other half: the check must not be so strict
that the product cannot use its own API.
*/
func TestOurOwnOriginIsAccepted(t *testing.T) {
	response := serveBrowser(browser(http.MethodDelete, api.Prefix+"/sessions/current", ""))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if cookie := response.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Max-Age=0") {
		t.Errorf("signing out did not clear the cookie: %q", cookie)
	}
}

/*
TestAForeignSecFetchSiteIsRefusedEvenWithAMatchingOrigin covers the belt to the
Origin check's braces.

It is not an independent layer — `Sec-Fetch-Site` shipped with SameSite and is
absent on exactly the clients that ignore SameSite — but where a browser sends
it, a value of anything but same-origin is a request that did not come from
this page, whatever the Origin header says.
*/
func TestAForeignSecFetchSiteIsRefusedEvenWithAMatchingOrigin(t *testing.T) {
	request := browser(http.MethodDelete, api.Prefix+"/sessions/current", "")
	request.Header.Set("Sec-Fetch-Site", "cross-site")

	if response := serveBrowser(request); response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

/*
TestReadingNeedsNoOrigin keeps the check from breaking an ordinary read.

A safe method changes nothing, so forging one achieves nothing, and requiring
an Origin on it would refuse a plain browser navigation for no benefit. This is
the same exemption [TestNoSessionRouteChangesStateOnAGet] pays for: it is only
safe while no GET on this surface changes anything.
*/
func TestReadingNeedsNoOrigin(t *testing.T) {
	for _, target := range []string{api.Prefix + "/me", api.Prefix + "/me/rooms"} {
		request := browser(http.MethodGet, target, "")
		request.Header.Del("Origin")

		if response := serveBrowser(request); response.Code != http.StatusOK {
			t.Errorf("%s answered %d without an Origin, want %d: %s",
				target, response.Code, http.StatusOK, response.Body)
		}
	}
}

/*
TestARefusedOriginIsNotCached keeps an intermediary from turning one refusal
into everybody's.

Whether a request is refused here depends on headers, so a cache that kept the
403 would serve it to a request that carried the right ones.
*/
func TestARefusedOriginIsNotCached(t *testing.T) {
	request := browser(http.MethodDelete, api.Prefix+"/sessions/current", "")
	request.Header.Set("Origin", "http://docs.example.com")

	response := serveBrowser(request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if store := response.Header().Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}
}

/*
TestEveryStateChangingSessionRouteDocumentsItsOriginRefusal keeps the contract
honest about the check the middleware makes.

When the Origin check moved from inside a handler to the surface, every unsafe
route on it began answering 403 to another page — including four that M31 had
added without documenting one, because at the time they did not refuse. A
client generated from the specification would not have known the answer
existed. This walks the table, like the tripwire that enforces the check, so a
route added later cannot be refused in practice and silent in the contract.
*/
func TestEveryStateChangingSessionRouteDocumentsItsOriginRefusal(t *testing.T) {
	document := loadSpecification(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	checked := 0
	for _, entry := range routeTable(logger, testDependencies()) {
		if entry.surface != surfaceSession && entry.surface != surfaceSignIn {
			continue
		}
		if entry.method == http.MethodGet || entry.method == http.MethodHead {
			continue
		}
		checked++

		item := document.Paths.Find(entry.path)
		if item == nil {
			t.Errorf("%s %s is not in the specification", entry.method, entry.path)
			continue
		}
		operation := item.GetOperation(entry.method)
		if operation == nil || operation.Responses.Status(http.StatusForbidden) == nil {
			t.Errorf("%s %s is refused with 403 when it comes from another page, and the "+
				"specification does not say so", entry.method, entry.path)
		}
	}

	if checked == 0 {
		t.Fatal("no state-changing route on the session surface, so this test proved nothing")
	}
}

/*
TestSigningInIsAlsoGuarded covers the surface that is not surfaceSession.

Signing in authenticates nobody, so the walk over authenticated routes does not
reach it — but it is reached by a browser, changes state, and sets a cookie. A
page elsewhere that could post credentials here would be able to sign somebody
in as an account the attacker controls, which is a real attack with a boring
name.
*/
func TestSigningInIsAlsoGuarded(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, api.Prefix+"/sessions",
		strings.NewReader(`{"email":"ana@example.com","password":"correct horse battery"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://docs.example.com")

	response := serveBrowser(request)

	if response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d: %s", response.Code, http.StatusForbidden, response.Body)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Error("a request from another page was given a session")
	}
}
