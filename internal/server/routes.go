package server

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"convia/internal/api"
)

/*
routes builds the request multiplexer.

It records the methods registered for every path so that unmatched methods
and unmatched paths answer with Convia's JSON error schema instead of the
plain-text defaults of net/http.
*/
type routes struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	methods map[string][]string

	/*
		page is Convia's own page, served at every path the API has not
		claimed.

		It is not in the route table and must not be. The table is the API, and
		a test compares it against the OpenAPI document in both directions: a
		page listed there would have to be documented as an operation, which it
		is not. So it hangs off the not-found fallback instead, where it can
		only ever receive what nothing else matched.

		Nil means no interface is served, and every unmatched path answers with
		the API's own not-found.
	*/
	page http.Handler
}

func newRoutes(logger *slog.Logger, own http.Handler) *routes {
	return &routes{
		logger:  logger,
		mux:     http.NewServeMux(),
		methods: make(map[string][]string),
		page:    own,
	}
}

/*
handle registers handler for one method and path.

Registering GET also serves HEAD, matching net/http pattern semantics.
*/
func (rt *routes) handle(method, path string, handler http.Handler) {
	rt.mux.Handle(method+" "+path, handler)

	rt.methods[path] = append(rt.methods[path], method)
	if method == http.MethodGet {
		rt.methods[path] = append(rt.methods[path], http.MethodHead)
	}
}

// handler finalizes the multiplexer by registering the method-not-allowed and
// not-found fallbacks. It must be called after every route is registered.
func (rt *routes) handler() http.Handler {
	for path, methods := range rt.methods {
		allowed := slices.Clone(methods)
		slices.Sort(allowed)
		rt.mux.Handle(path, rt.methodNotAllowedHandler(allowed))
	}

	rt.mux.Handle("/", rt.unmatchedHandler())
	return rt.mux
}

/*
unmatchedHandler decides what a path nothing claimed means.

Two things reach here, and they want opposite answers. A request under the API's
prefix is a call to an operation that does not exist, and has to be told so in
the vocabulary the caller is speaking. Anything else is a browser asking for a
page, and a single-page interface routes in the browser: `/rooms/abc` has to
answer with the page rather than with a 404, or reloading any screen but the
first would break.

The prefix check is what keeps those apart, and it fails towards the API. A
mistyped `/v1/roomss` is answered as a missing operation, not as an interface
route, so a client debugging one never receives HTML where it expected JSON.
*/
func (rt *routes) unmatchedHandler() http.Handler {
	notFound := rt.notFoundHandler()
	if rt.page == nil {
		return notFound
	}

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		underAPI := request.URL.Path == api.Prefix ||
			strings.HasPrefix(request.URL.Path, api.Prefix+"/")

		/*
			Only a read reaches the page. A POST to a path nothing claimed is
			not a navigation, and answering it with a page would turn every
			mistyped method into an apparent success.
		*/
		reading := request.Method == http.MethodGet || request.Method == http.MethodHead

		if underAPI || !reading {
			notFound.ServeHTTP(response, request)
			return
		}
		rt.page.ServeHTTP(response, request)
	})
}

func (rt *routes) methodNotAllowedHandler(allowed []string) http.Handler {
	allowHeader := strings.Join(allowed, ", ")

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Allow", allowHeader)
		rt.writeError(response, request, http.StatusMethodNotAllowed, api.CodeMethodNotAllowed,
			"The requested method is not supported by this resource.")
	})
}

func (rt *routes) notFoundHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		rt.writeError(response, request, http.StatusNotFound, api.CodeNotFound,
			"The requested resource does not exist.")
	})
}

func (rt *routes) writeError(response http.ResponseWriter, request *http.Request, status int, code api.ErrorCode, message string) {
	if err := api.WriteError(response, request, status, code, message); err != nil {
		rt.logger.Error("write error response", "error", err, "request_id", api.RequestIDFromContext(request.Context()))
	}
}
