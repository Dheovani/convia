package sessions

import (
	"net/http"
	"strings"
)

/*
CookieName is where a session travels, and the name is doing real work.

The `__Host-` prefix is enforced by the browser rather than by Convia: a cookie
carrying it is accepted only when it is `Secure`, has `Path=/`, and has **no
`Domain`**. That last one is the reason it is here.

Without the prefix, a host-only cookie is only host-only because Convia said
so. Cookies are scoped by domain rather than by origin, so anything that can
write a cookie for a parent domain — an XSS on a sibling subdomain, a status
page, a preview environment, a network attacker serving plain http on any
sibling — can set `Domain=.convia.example` with this same name. The browser
then sends **both**, Convia reads whichever comes first, and the attacker has
either pinned a value or produced a permanently signed-out person that signing
in again does not repair.

The prefix makes that impossible: only the exact host can set the cookie at all.

It is the same name in every environment, deliberately. `http://localhost` is a
secure context in every browser Convia supports, so `Secure` — and therefore
the prefix — works over plain http there. A name that differed by environment
would mean the production cookie path was never exercised until production,
which is the class of bug this whole file exists to prevent.
*/
const CookieName = "__Host-convia_session"

/*
Set writes the cookie that carries a session.

Every attribute is fixed here and nowhere else. Go does not enforce the
`__Host-` rules — it will happily emit the prefixed name alongside a `Domain`
and let the browser silently drop the whole cookie — so having exactly one
constructor, with the rules hard-coded, is what keeps the prefix's guarantee
real. A test asserts the emitted header.

MaxAge carries the **idle** window rather than the absolute one. A cookie that
outlives the session it names is a cookie the browser keeps sending for weeks
after it stopped working, and every one of those is a wasted read that ends in
a refusal.
*/
func Set(response http.ResponseWriter, token string) {
	http.SetCookie(response, &http.Cookie{
		Name:  CookieName,
		Value: token,

		// Required by the __Host- prefix, all three.
		Path:   "/",
		Secure: true,
		// Domain is deliberately empty: naming one voids the prefix.

		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(IdleLifetime.Seconds()),
	})
}

/*
Clear removes the cookie.

Every attribute matches [Set] exactly. A browser matches a replacement by name,
path, and domain, so a single difference would leave the original in place and
the person still signed in as far as their browser is concerned.

MaxAge is -1 rather than 0, and the difference is a real trap: in Go, `MaxAge:
0` means "emit no Max-Age attribute at all", which does nothing. Only -1 makes
Go write `Max-Age=0` and an `Expires` in the past.

Clearing the cookie is the lesser half of signing out. The greater half is
revoking the row, because a bearer token is only as gone as the server says it
is.
*/
func Clear(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

/*
Present extracts a session token from a request's cookies.

It is the only way a session is ever read. A token in an `Authorization` header
is not looked for and would not be found, which is what makes a session cookie
and an API key refuse each other's surfaces by shape rather than by a check
somebody has to remember.
*/
func Present(request *http.Request) (string, bool) {
	cookie, err := request.Cookie(CookieName)
	if err != nil {
		return "", false
	}

	token := strings.TrimSpace(cookie.Value)
	return token, token != ""
}
