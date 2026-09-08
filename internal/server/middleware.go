package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/operator"
	"convia/internal/ratelimit"
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
func logRequest(logger *slog.Logger, resolve resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: response, status: http.StatusOK}

		next.ServeHTTP(recorder, request)

		/*
			The client address is the resolved one, not the peer, so an
			operator can see at a glance whether trusted-forwarder
			configuration is doing what they intended. Without it in the log,
			a misconfigured proxy list looks exactly like a correct one until
			the rate limiter starts refusing the wrong people.
		*/
		logger.Info("HTTP request",
			"request_id", api.RequestIDFromContext(request.Context()),
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"client", resolve.clientAddress(request),
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
authenticator verifies a presented application key.

The server depends on this narrow behavior rather than on the credentials
service, so the middleware stays testable without PostgreSQL.
credentials.Service satisfies it.
*/
type authenticator interface {
	Authenticate(ctx context.Context, token string) (credentials.Principal, error)
}

/*
operatorAuthenticator verifies a presented operator key.

It is a separate interface from authenticator, returning a separate principal
type, so no wiring mistake can hand an operator route a tenant verifier or the
reverse: the two do not satisfy each other.
operator.Service satisfies it.
*/
type operatorAuthenticator interface {
	Authenticate(ctx context.Context, token string) (operator.Principal, error)
}

/*
verifier turns a presented key into a request context carrying who it proves.

Both surfaces authenticate identically — check the budget, then parse, look up,
compare, and refuse with one indistinguishable answer — and differ only in
which domain verifies and which principal ends up in the context. That
difference lives in the two adapters below so the middleware is written once,
and a change to how refusal works cannot be applied to one surface and missed
on the other.
*/
type verifier interface {
	Verify(ctx context.Context, token string) (context.Context, error)
}

/*
errRefused reports a key that does not authenticate, whatever the reason.

Each adapter maps its own domain's refusal onto this one error, so the
middleware can tell a rejected key from an infrastructure failure without
knowing which domain answered.
*/
var errRefused = errors.New("the presented key does not authenticate")

// tenantVerifier adapts the application credential service to verifier.
type tenantVerifier struct{ service authenticator }

func (verify tenantVerifier) Verify(ctx context.Context, token string) (context.Context, error) {
	principal, err := verify.service.Authenticate(ctx, token)
	switch {
	case errors.Is(err, credentials.ErrUnauthenticated):
		return nil, errRefused
	case err != nil:
		return nil, err
	}
	return credentials.ContextWithPrincipal(ctx, principal), nil
}

// operatorVerifier adapts the operator credential service to verifier.
type operatorVerifier struct{ service operatorAuthenticator }

func (verify operatorVerifier) Verify(ctx context.Context, token string) (context.Context, error) {
	principal, err := verify.service.Authenticate(ctx, token)
	switch {
	case errors.Is(err, operator.ErrUnauthenticated):
		return nil, errRefused
	case err != nil:
		return nil, err
	}
	return operator.ContextWithPrincipal(ctx, principal), nil
}

/*
authenticate refuses a request that does not carry a usable credential.

It wraps every route that acts with someone's authority — an application's or
an operator's — and puts the verified identity in the request context. Every
reason a key can fail produces the same answer, so the response never
distinguishes an unknown key from a revoked or expired one, nor an operator key
offered to a tenant route from one that does not exist.

The `WWW-Authenticate` header is what tells a client which scheme to use, and
RFC 9110 requires it on a 401.
*/
func authenticate(logger *slog.Logger, verify verifier, failures *ratelimit.Limiter,
	resolve resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		/*
			The budget is checked before anything else, so a caller that has
			already spent it costs a map lookup rather than a database read.
			That is the point of limiting here: the work is skipped, not merely
			counted.
		*/
		source := resolve.clientAddress(request)
		if !failures.Allows(source) {
			slowDown(logger, response, request, failures.RetryAfter(source))
			return
		}

		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			failures.Record(source)
			refuse(logger, response, request)
			return
		}

		verified, err := verify.Verify(request.Context(), token)
		if err != nil {
			failures.Record(source)
			if !errors.Is(err, errRefused) {
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

		next.ServeHTTP(response, request.WithContext(verified))
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

/*
slowDown refuses a caller that has spent its budget for failed attempts.

Retry-After is rounded up to a whole second, because RFC 9110 defines it in
seconds and rounding down would invite a client to retry just before it is
welcome.
*/
func slowDown(logger *slog.Logger, response http.ResponseWriter, request *http.Request, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}

	response.Header().Set("Retry-After", strconv.Itoa(seconds))
	response.Header().Set("WWW-Authenticate", `Bearer realm="convia"`)

	failure := api.NewFailure(http.StatusTooManyRequests, api.CodeRateLimited,
		"Too many failed authentication attempts. Retry later.")
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write rate limited response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
