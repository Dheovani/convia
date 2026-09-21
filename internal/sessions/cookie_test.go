package sessions

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// emitted returns the Set-Cookie header a function wrote.
func emitted(t *testing.T, write func(http.ResponseWriter)) string {
	t.Helper()

	response := httptest.NewRecorder()
	write(response)

	header := response.Header().Values("Set-Cookie")
	if len(header) != 1 {
		t.Fatalf("wrote %d Set-Cookie headers, want 1: %v", len(header), header)
	}
	return header[0]
}

/*
TestTheCookieSatisfiesTheHostPrefix is the test the prefix's guarantee depends
on.

`__Host-` is enforced by the browser, not by Go: `http.SetCookie` will happily
emit the prefixed name alongside a `Domain`, and the browser will then discard
the whole cookie — silently, with no error anywhere, and nobody signs in.

So the rules are asserted on the header that actually goes out. There is one
constructor precisely so there is one thing to assert.
*/
func TestTheCookieSatisfiesTheHostPrefix(t *testing.T) {
	header := emitted(t, func(response http.ResponseWriter) { Set(response, "cvs_token") })

	if !strings.HasPrefix(header, "__Host-") {
		t.Errorf("the cookie is not __Host- prefixed: %s", header)
	}
	for _, required := range []string{"Path=/", "Secure", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(header, required) {
			t.Errorf("the cookie lacks %s, which __Host- or CSRF requires: %s", required, header)
		}
	}
	if strings.Contains(header, "Domain=") {
		t.Errorf("the cookie names a Domain, which voids the __Host- prefix entirely: %s", header)
	}
}

/*
TestTheCookieOutlivesNothing keeps a browser from sending a credential that
stopped working weeks ago.

Max-Age carries the idle window rather than the absolute one. A cookie set for
ninety days on a session that lapses after fourteen is seventy-six days of
requests that cost a database read and end in a refusal — and, because the
refusal is a failed attempt, seventy-six days of spending a budget that
protects everybody behind the same address.

It is Max-Age rather than Expires because Expires is absolute and a client with
a wrong clock gets it wrong.
*/
func TestTheCookieOutlivesNothing(t *testing.T) {
	header := emitted(t, func(response http.ResponseWriter) { Set(response, "cvs_token") })

	want := "Max-Age=" + strconv.Itoa(int(IdleLifetime.Seconds()))
	if !strings.Contains(header, want) {
		t.Errorf("the cookie does not carry %s: %s", want, header)
	}
	if strings.Contains(header, "Expires=") {
		t.Errorf("the cookie carries an Expires, which depends on the client's clock: %s", header)
	}
}

/*
TestClearingActuallyClears covers a trap that is silent in Go and invisible by
eye.

`http.Cookie{MaxAge: 0}` means "emit no Max-Age attribute", which does nothing
at all — the browser keeps the cookie. Only -1 makes Go write `Max-Age=0` and
an `Expires` in the past.

Every other attribute has to match the one that set it, too: a browser matches
a replacement by name, path, and domain, so one difference leaves the original
in place and the person still signed in as far as their browser is concerned.
*/
func TestClearingActuallyClears(t *testing.T) {
	set := emitted(t, func(response http.ResponseWriter) { Set(response, "cvs_token") })
	cleared := emitted(t, Clear)

	if !strings.Contains(cleared, "Max-Age=0") {
		t.Errorf("clearing does not expire the cookie; in Go, MaxAge 0 emits nothing: %s", cleared)
	}
	if strings.Contains(cleared, "cvs_token") {
		t.Errorf("clearing carries the old value: %s", cleared)
	}

	for _, attribute := range []string{"__Host-convia_session", "Path=/", "Secure", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(set, attribute) || !strings.Contains(cleared, attribute) {
			t.Errorf("%q differs between setting and clearing, so the browser will not match them.\n"+
				"set:     %s\ncleared: %s", attribute, set, cleared)
		}
	}
}

/*
TestASessionIsReadFromACookieOrItsOwnHeader is half of what keeps the four
credential families apart.

The page Convia serves holds a cookie; Convia's own application holds the token
and presents it here, because it can hold no cookie of Convia's origin. What is
still never read is **another family**: an application's key offered to this
surface is not refused so much as never looked at.
*/
func TestASessionIsReadFromACookieOrItsOwnHeader(t *testing.T) {
	const session = "cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"

	held := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	held.Header.Set("Authorization", "Bearer "+session)
	token, found := Present(held)
	if !found || token != session {
		t.Errorf("Present() = %q, %v, want the header's session", token, found)
	}
	if _, carried := InCookie(held); carried {
		t.Error("a session in a header was reported as a cookie, which decides the origin check")
	}

	sent := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	sent.AddCookie(&http.Cookie{Name: CookieName, Value: "cvs_token"})
	token, found = Present(sent)
	cookie, carried := InCookie(sent)
	if !found || token != "cvs_token" || !carried || cookie != "cvs_token" {
		t.Errorf("Present() = %q, %v and InCookie() = %q, %v, want the cookie's value", token, found, cookie, carried)
	}

	for what, header := range map[string]string{
		"an application's key":   "Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"an operator's key":      "Bearer cvo_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"an invitation":          "Bearer cvi_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"a session, malformed":   "Bearer cvs_not-a-session",
		"another scheme":         "Basic " + session,
		"a token with no scheme": session,
	} {
		other := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		other.Header.Set("Authorization", header)
		if token, found := Present(other); found {
			t.Errorf("%s was read as a session: %q", what, token)
		}
	}
}

func TestAnEmptyCookieIsNoCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: "   "})

	if _, found := Present(request); found {
		t.Error("a blank cookie was read as a session")
	}
}
