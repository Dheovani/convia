package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"convia/internal/api"
)

/*
requestID assigns every request a correlation identifier.

A client-supplied identifier is reused when it is safe to echo; otherwise a
new one is generated. The identifier is stored in the request context,
returned in the response header, and included in structured logs and error
responses.
*/
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identifier := api.NormalizeRequestID(request.Header.Get(api.RequestIDHeader))
		response.Header().Set(api.RequestIDHeader, identifier)

		next.ServeHTTP(response, request.WithContext(api.WithRequestID(request.Context(), identifier)))
	})
}

// logRequest emits one structured access log entry per request and records the
// response status and size for the rest of the chain.
func logRequest(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: response, status: http.StatusOK}

		next.ServeHTTP(recorder, request)

		logger.Info("HTTP request",
			"request_id", api.RequestIDFromContext(request.Context()),
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

/*
recoverPanic converts an unexpected panic into a generic JSON error.

The panic value and stack are logged for operators; clients never receive
internal details. http.ErrAbortHandler keeps its documented meaning and is
propagated to net/http.
*/
func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if err, isError := recovered.(error); isError && errors.Is(err, http.ErrAbortHandler) {
				panic(recovered)
			}

			identifier := api.RequestIDFromContext(request.Context())
			logger.Error("recovered from panic in HTTP handler",
				"request_id", identifier,
				"method", request.Method,
				"path", request.URL.Path,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)

			if reporter, ok := response.(headerWriteReporter); ok && reporter.headerWritten() {
				return
			}
			if err := api.WriteError(response, request, http.StatusInternalServerError, api.CodeInternal,
				"The server encountered an unexpected condition."); err != nil {
				logger.Error("write error response", "error", err, "request_id", identifier)
			}
		}()

		next.ServeHTTP(response, request)
	})
}

// headerWriteReporter is implemented by response writers that know whether the
// response header has already been sent.
type headerWriteReporter interface {
	headerWritten() bool
}

// responseRecorder captures the response status and size without altering the
// behavior of the underlying writer.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	bytes   int64
	written bool
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.written {
		return
	}
	recorder.status = status
	recorder.written = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if !recorder.written {
		recorder.WriteHeader(http.StatusOK)
	}

	written, err := recorder.ResponseWriter.Write(body)
	recorder.bytes += int64(written)
	return written, err
}

func (recorder *responseRecorder) headerWritten() bool {
	return recorder.written
}

// Unwrap exposes the underlying writer so that http.ResponseController keeps
// working for handlers that need flushing or connection control.
func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}
