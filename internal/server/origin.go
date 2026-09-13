package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"convia/internal/api"
)

/*
forwardedProtocolHeader is what a reverse proxy uses to say which scheme the
client actually spoke.

It is consulted only to reconstruct this instance's own origin for the check
below, and only where a proxy is in front. Convia never trusts it for anything
a caller could benefit from getting wrong.
*/
const forwardedProtocolHeader = "X-Forwarded-Proto"

/*
sameOrigin refuses a state-changing request that did not come from Convia's own
page.

This is the layer that actually carries CSRF on the surfaces a browser reaches,
and it is worth saying why the others do not carry it alone.

`SameSite=Lax` stops a cross-*site* POST, but a sibling subdomain is same-site:
`docs.convia.example` posting to `app.convia.example` is something Lax does not
see at all. And the JSON content-type requirement, which blocks the
`enctype="text/plain"` form trick, does nothing for a route that takes no body.

So `Origin` is required on every unsafe method and matched **exactly** against
this instance's own origin. Exact, not a suffix: a suffix match is what would
let the sibling subdomain back in, which is the whole attack this is for.

Absence fails closed. Every browser has sent `Origin` on non-GET requests for
years, and a request without one on these surfaces did not come from a page.
`Sec-Fetch-Site` is consulted when present as a second opinion, but never as a
substitute — it shipped in the same browser generation as SameSite, so the
clients that lack one lack the other, and treating them as independent layers
would be counting the same protection twice.

**A WebSocket handshake is checked too**, although it is a GET. It changes
nothing, but it opens a connection that goes on carrying whatever the cookie is
entitled to, and a page on a sibling subdomain that could open one would be
reading somebody's conversations as they happen. That is cross-site WebSocket
hijacking, and SameSite does not see it for the reason it does not see a sibling
POST. Browsers send `Origin` on every handshake, so the same exact match holds.

It is middleware rather than a call inside each handler because that is the
difference between a rule and a habit. Applied here it covers every route
declared on a browser surface, including the ones nobody has written yet; a
route added to the table cannot be served without it.
*/
func sameOrigin(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if safeMethod(request.Method) && !upgrading(request) {
			next.ServeHTTP(response, request)
			return
		}

		if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			refuseOrigin(logger, response, request, "sec-fetch-site "+site)
			return
		}

		origin := request.Header.Get("Origin")
		switch {
		case origin == "" || origin == "null":
			refuseOrigin(logger, response, request, "absent")
		case !strings.EqualFold(origin, ownOrigin(request)):
			refuseOrigin(logger, response, request, "mismatch")
		default:
			next.ServeHTTP(response, request)
		}
	})
}

// safeMethod reports a method that cannot change anything, and therefore needs
// no origin check.
func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

/*
upgrading reports a request asking to become a WebSocket.

It reads the header rather than the route, so a handshake is checked wherever
one is attempted on a browser surface — including a route that never meant to
accept one, where the refusal costs nothing.
*/
func upgrading(request *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket")
}

/*
ownOrigin reconstructs the origin this instance is being reached at.

It is derived from the request rather than configured, so a deployment does not
have to declare its own address twice and cannot get the two out of step. Host
is what the router already matched on, and the scheme comes from the connection
or from the proxy that terminated it.
*/
func ownOrigin(request *http.Request) string {
	scheme := "http"
	switch {
	case request.TLS != nil:
		scheme = "https"
	case strings.EqualFold(request.Header.Get(forwardedProtocolHeader), "https"):
		scheme = "https"
	}

	return (&url.URL{Scheme: scheme, Host: request.Host}).String()
}

func refuseOrigin(logger *slog.Logger, response http.ResponseWriter, request *http.Request, reason string) {
	safeReason := strings.ReplaceAll(reason, "\n", "")
	safeReason = strings.ReplaceAll(safeReason, "\r", "")
	safePath := strings.ReplaceAll(request.URL.Path, "\n", "")
	safePath = strings.ReplaceAll(safePath, "\r", "")

	logger.Warn("a state-changing request was refused on its origin",
		"reason", safeReason,
		"method", request.Method,
		"path", safePath,
		"request_id", api.RequestIDFromContext(request.Context()),
	)

	/*
		A private refusal, like everything else on these surfaces. Whether a
		request was refused depends on the headers it carried, and a cache that
		kept this answer would serve it to a request that should have been let
		through.
	*/
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")

	failure := api.NewFailure(http.StatusForbidden, api.CodeForbidden,
		"This request did not come from Convia's own page.")
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write origin refusal",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
