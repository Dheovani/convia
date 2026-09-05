package server

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"convia/internal/api"
)

// routes builds the request multiplexer.
//
// It records the methods registered for every path so that unmatched methods
// and unmatched paths answer with Convia's JSON error schema instead of the
// plain-text defaults of net/http.
type routes struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	methods map[string][]string
}

func newRoutes(logger *slog.Logger) *routes {
	return &routes{
		logger:  logger,
		mux:     http.NewServeMux(),
		methods: make(map[string][]string),
	}
}

// handle registers handler for one method and path.
//
// Registering GET also serves HEAD, matching net/http pattern semantics.
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

	rt.mux.Handle("/", rt.notFoundHandler())
	return rt.mux
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
