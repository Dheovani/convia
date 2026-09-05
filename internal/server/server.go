// Package server provides Convia's HTTP server, middleware chain, and routes.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"convia/internal/api"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	// maxHeaderBytes bounds request headers, including the request line and
	// any client-supplied correlation identifier.
	maxHeaderBytes = 1 << 20 // 1 MiB
)

// New constructs the Convia HTTP server.
func New(address string, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler(logger),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

/*
Handler builds the routed handler wrapped by the transport middleware.

The chain is ordered so that every request carries a correlation identifier
before it is logged, and so that a recovered panic is still reported by the
access log with its final status.
*/
func handler(logger *slog.Logger) http.Handler {
	rt := newRoutes(logger)
	for _, entry := range routeTable(logger) {
		rt.handle(entry.method, entry.path, entry.handler)
	}

	return requestID(logRequest(logger, recoverPanic(logger, rt.handler())))
}

// route describes one HTTP route served by Convia.
type route struct {
	method  string
	path    string
	handler http.Handler
}

/*
RouteTable returns every route the service serves.

It is the single source of truth for routing, which lets the contract test
compare the implemented surface with the OpenAPI document in api/.
*/
func routeTable(logger *slog.Logger) []route {
	return []route{
		// Operational endpoints stay outside api.Prefix: they are owned by
		// operators, not by public API consumers, and must not be versioned or
		// authenticated together with the public API.
		{method: http.MethodGet, path: "/health", handler: healthHandler(logger)},
	}
}

// healthResponse is the stable body of the health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

func healthHandler(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := api.Write(response, http.StatusOK, healthResponse{Status: "ok"}); err != nil {
			logger.Error("write health response", "error", err, "request_id", api.RequestIDFromContext(request.Context()))
		}
	})
}
