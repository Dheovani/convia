package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
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

/*
authenticator verifies a presented key.

The server depends on this narrow behavior rather than on the credentials
service, so the middleware stays testable without PostgreSQL.
*credentials.Service satisfies it.
*/
type authenticator interface {
	Authenticate(ctx context.Context, token string) (credentials.Principal, error)
}

/*
authenticate refuses a request that does not carry a usable credential.

It wraps only the routes that act on behalf of an application, and puts the
verified identity in the request context. Every reason a key can fail produces
the same answer, so the response never distinguishes an unknown key from a
revoked or expired one.

The `WWW-Authenticate` header is what tells a client which scheme to use, and
RFC 9110 requires it on a 401.
*/
func authenticate(logger *slog.Logger, verifier authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			refuse(logger, response, request)
			return
		}

		principal, err := verifier.Authenticate(request.Context(), token)
		if err != nil {
			if !errors.Is(err, credentials.ErrUnauthenticated) {
				/*
					An infrastructure failure is not a rejected key. It is
					logged with its detail and still answered as a refusal,
					because granting access when verification could not run
					would be the worse mistake.
				*/
				logger.Error("verify credential",
					"error", err,
					"request_id", api.RequestIDFromContext(request.Context()),
				)
			}
			refuse(logger, response, request)
			return
		}

		next.ServeHTTP(response, request.WithContext(
			credentials.ContextWithPrincipal(request.Context(), principal)))
	})
}

/*
bearerToken extracts the key from an Authorization header.

The scheme is matched case-insensitively because RFC 9110 defines it that way,
and nothing else about the value is interpreted here: whether the key is
well-formed is the credential domain's question, not the transport's.
*/
func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(token)
	return token, token != ""
}

// refuse answers a request that did not authenticate, without saying why.
func refuse(logger *slog.Logger, response http.ResponseWriter, request *http.Request) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="convia"`)

	failure := api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
		"The request did not carry a usable credential.")
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write unauthenticated response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
