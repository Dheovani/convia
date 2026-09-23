package app

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"convia/internal/api"
)

/*
Carry serves a request the interface made by making it against the
installation.

The window's webview asks for `/v1/...` exactly as the page in a browser does,
and this is what is on the other end of that. Nothing is listening on a port:
the webview's request never leaves this process until it leaves it as a request
to Convia, so nothing else on the machine can reach what is served here.

**The session is added here and read nowhere else.** The interface cannot set
the header, cannot see it, and does not know it exists — which is the same
position the page is in with the cookie, and the reason docs/adr/0019 allows
both.
*/
func (application *App) Carry(response http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(request.URL.Path, api.Prefix+"/") {
		response.WriteHeader(http.StatusNotFound)
		return
	}

	if owned(request.Method, request.URL.Path) {
		/*
			These are the routes that hand a session over or take one away, and
			they are the application's rather than the interface's. Carrying
			them would put the token in an answer the webview reads, which is
			the one thing this arrangement exists to prevent. The interface
			reaches them through the methods below instead.
		*/
		refuse(response, http.StatusForbidden, api.CodeForbidden,
			"This route belongs to the application rather than to its interface.")
		return
	}

	reached := application.held()
	if reached == nil || reached.Session() == "" {
		/*
			Nothing to carry it to. The interface should be on the screen that
			asks which installation to connect to, so this is a mistake in the
			interface rather than something a person did.
		*/
		refuse(response, http.StatusServiceUnavailable, api.CodeUnavailable,
			"This application is not connected to an installation.")
		return
	}

	target, err := url.Parse(reached.Address())
	if err != nil {
		refuse(response, http.StatusServiceUnavailable, api.CodeUnavailable,
			"This application is not connected to an installation.")
		return
	}

	carrying := request.WithContext(context.WithValue(request.Context(), carriedTo{},
		carriage{target: target, session: reached.Session()}))
	application.carrier.ServeHTTP(response, carrying)
}

// carriedTo is the key under which one request's destination travels, so that
// rewriting it reads what was resolved rather than the application's state a
// moment later.
type carriedTo struct{}

type carriage struct {
	target  *url.URL
	session string
}

/*
carrier forwards what the interface asked for, with the session attached and
the browser taken off.

A webview is a browser, and it sends what a browser sends: an origin, a
site-context, and cookies for the address it thinks it is at. None of that
belongs on a request to Convia — and worse, Convia reads exactly those to
decide whether it is talking to a browser, and would answer by setting a cookie
this process cannot keep instead of by handing over a session it can.
*/
func carrier() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			to, _ := request.In.Context().Value(carriedTo{}).(carriage)

			request.Out.URL.Scheme = to.target.Scheme
			request.Out.URL.Host = to.target.Host
			request.Out.URL.Path = strings.TrimSuffix(to.target.Path, "/") + request.In.URL.Path
			request.Out.Host = to.target.Host

			for _, browserish := range []string{
				"Origin", "Cookie", "Referer",
				"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User",
			} {
				request.Out.Header.Del(browserish)
			}

			// Whatever the interface put here, the session is the application's.
			request.Out.Header.Set("Authorization", "Bearer "+to.session)
		},

		ModifyResponse: func(response *http.Response) error {
			// Convia has no reason to set one now that it knows this is not a
			// browser. If it ever does, the webview does not get it.
			response.Header.Del("Set-Cookie")
			return nil
		},

		/*
			An installation that did not answer is 503 in Convia's own error
			shape, because that is the only shape the interface reads. It is
			the truthful status: the request was fine and the thing it was for
			is not there.
		*/
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			refuse(response, http.StatusServiceUnavailable, api.CodeUnavailable,
				"Convia could not be reached.")
		},
	}
}

/*
owned names the routes that mint or end a session.

They are matched by method and path together, because the path alone does not
say: `/v1/sessions` hands one over when posted to and takes them away when
deleted, and only one of those is a thing the interface may ask for directly.
*/
func owned(method, path string) bool {
	switch path {
	case api.Prefix + "/sessions":
		return method == http.MethodPost || method == http.MethodDelete
	case api.Prefix + "/sessions/current":
		return method == http.MethodDelete
	case api.Prefix + "/accounts":
		return method == http.MethodPost
	case api.Prefix + "/me/password":
		return method == http.MethodPatch
	case api.Prefix + "/me/delete":
		return method == http.MethodPost
	default:
		return false
	}
}

/*
refuse answers in the one error shape Convia publishes.

The interface reads a code and branches on it, and it must not have to learn a
second vocabulary for the few refusals that come from the application rather
than from an installation. Every code used here is one Convia already defines.
*/
func refuse(response http.ResponseWriter, status int, code api.ErrorCode, message string) {
	response.Header().Set("Cache-Control", "no-store")
	_ = api.WriteError(response, nil, status, code, message)
}
