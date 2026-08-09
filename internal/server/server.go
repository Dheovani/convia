// Package server provides Convia's HTTP server and routes.
package server

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
)

// New constructs the Convia HTTP server.
func New(address string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler(logger))

	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

func healthHandler(logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(response, "{\"status\":\"ok\"}\n"); err != nil {
			logger.Error("write health response", "error", err)
		}
	}
}
