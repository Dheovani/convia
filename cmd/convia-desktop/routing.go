package main

import (
	"io/fs"
	"net/http"
	"strings"

	"convia/internal/api"
	"convia/internal/desktop/app"
)

/*
routed answers what the window asks for and the bundle does not contain.

Two things reach here. Convia's API, which the application carries to the
installation with the session attached — see internal/desktop/app. And every
other address, which is a place within the interface rather than a file: a
single-page interface routes in itself, so `/rooms/abc` has to answer with the
page or reloading any screen but the first would be a dead end.

Nothing is served over a port. This handler is called in process by the
window's own asset server, so what it answers is reachable by the window and by
nothing else on the machine.
*/
func routed(ui fs.FS, application *app.App) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, api.Prefix+"/") {
			application.Carry(response, request)
			return
		}

		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		page, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			// The application refuses to open a window with no interface in
			// it, so this is unreachable rather than a case to handle.
			response.WriteHeader(http.StatusInternalServerError)
			return
		}

		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		_, _ = response.Write(page)
	})
}

/*
policy is what the interface is allowed to do inside the window.

The service serves the same interface under the same policy, and for the same
reason: it is what keeps a flaw in this bundle from becoming a way to load
somebody else's script or send what is on the screen somewhere else. Wails
serves the page itself, so this is applied as middleware around its asset
server rather than by the handler above, which only ever sees what the bundle
does not contain.

**`connect-src` is the one directive that cannot be exact here.** The service
knows which media server its deployment has and names it. An application
connects to whichever installation somebody typed, each with a media server of
its own, and the page is loaded before any of that is known — so what it can
say is the scheme: encrypted, or this machine. That is weaker than the page's
policy and stronger than what an application has by default, which is none.

`'self'` is the window's own asset server, which is where every request for
Convia's API goes before this process carries it.
*/
const policy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self'; " +
	"font-src 'self'; " +
	"connect-src 'self' https: wss: http://localhost:* ws://localhost:* http://127.0.0.1:* ws://127.0.0.1:*; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

/*
hardened puts that policy on everything the window loads.

It wraps the whole asset server, including the page, because the page is the
thing the policy is about and Wails serves that one itself.
*/
func hardened(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := response.Header()
		header.Set("Content-Security-Policy", policy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")

		// The camera and the microphone are a call's, and this page's alone.
		header.Set("Permissions-Policy", "camera=(self), microphone=(self), geolocation=(), payment=(), usb=()")

		next.ServeHTTP(response, request)
	})
}
