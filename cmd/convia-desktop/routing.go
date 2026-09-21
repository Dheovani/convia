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
