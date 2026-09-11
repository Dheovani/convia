package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/invitations"
	"convia/internal/operator"
	"convia/internal/ratelimit"
	"convia/internal/sessions"
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
Hijack lets a handler take the connection over, and records that it did.

Without this the access log would report a protocol upgrade as an ordinary 200
that returned no bytes, because nothing after the hijack goes through the
writer any more. Recording the status here is what keeps one line per request
true for the one route that stops being a request.
*/
func (recorder *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffered, err := http.NewResponseController(recorder.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}

	recorder.status = http.StatusSwitchingProtocols
	recorder.written = true
	return connection, buffered, nil
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

Every surface authenticates identically — check the budget, then parse, look
up, compare, and refuse with one indistinguishable answer — and they differ in
three things: where the credential travels, which domain verifies it, and which
principal ends up in the context. All three live in the adapters below so the
middleware is written once, and a change to how refusal works cannot be applied
to one surface and missed on another.

`present` is what keeps the families from crossing. A bearer surface never
looks at a cookie and the session surface never looks at a header, so a
credential offered to the wrong surface is not merely rejected — it is not read
at all.
*/
type verifier interface {
	/*
		present extracts the credential this surface accepts, and nothing else.
		The second result is false when the request carried none.
	*/
	present(request *http.Request) (string, bool)

	/*
		challenge is the WWW-Authenticate value for a refusal, or empty where
		none applies. RFC 9110 asks for one on a 401, and there is no
		registered scheme for a cookie — so the session surface sends none
		rather than inventing one.
	*/
	challenge() string

	/*
		chargeAbsence reports whether presenting nothing counts as a failed
		attempt.

		It does for an integration, which should always send a key. It does not
		for a browser: asking "am I signed in?" with no cookie is the first
		request a freshly loaded page makes, every time, and charging it would
		let ordinary use exhaust a budget that protects everybody sharing an
		address.
	*/
	chargeAbsence() bool

	Verify(ctx context.Context, token string) (context.Context, error)
}

/*
bearer is the presentation half of every surface that reads an Authorization
header.

The three key families embed it, so they cannot drift apart in how a key is
found or how a refusal is phrased.
*/
type bearer struct{}

func (bearer) present(request *http.Request) (string, bool) {
	return bearerToken(request.Header.Get("Authorization"))
}

func (bearer) challenge() string { return `Bearer realm="convia"` }

func (bearer) chargeAbsence() bool { return true }

/*
errRefused reports a key that does not authenticate, whatever the reason.

Each adapter maps its own domain's refusal onto this one error, so the
middleware can tell a rejected key from an infrastructure failure without
knowing which domain answered.
*/
var errRefused = errors.New("the presented key does not authenticate")

// tenantVerifier adapts the application credential service to verifier.
type tenantVerifier struct {
	bearer
	service authenticator
}

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

/*
invitationAuthenticator verifies a presented invitation.

The holder of an invitation is not an application and names no tenant, so what
comes back is the invitation itself rather than a principal carrying scopes:
what an invitation authorizes is fixed when it is issued.
*/
type invitationAuthenticator interface {
	Authenticate(ctx context.Context, token string) (invitations.Invitation, error)
}

// invitationVerifier adapts the invitations service to verifier.
type invitationVerifier struct {
	bearer
	service invitationAuthenticator
}

func (verify invitationVerifier) Verify(ctx context.Context, token string) (context.Context, error) {
	invitation, err := verify.service.Authenticate(ctx, token)
	switch {
	case errors.Is(err, invitations.ErrUnauthenticated):
		return nil, errRefused
	case err != nil:
		return nil, err
	}

	return invitations.ContextWithHolder(ctx, invitation), nil
}

// operatorVerifier adapts the operator credential service to verifier.
type operatorVerifier struct {
	bearer
	service operatorAuthenticator
}

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
sessionAuthenticator verifies a presented browser session.

It is a separate interface returning a separate principal type, like the three
before it — but this one matters more than the pattern. A [sessions.Principal]
carries no scopes and no application authority, and **nothing anywhere converts
one into a credentials.Principal**. That is what stops a signed-in person from
holding the whole tenant's authority, which is the failure this surface would
otherwise walk into.
sessions.Service satisfies it.
*/
type sessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (sessions.Principal, error)
}

/*
sessionVerifier adapts the session service to verifier.

It does not embed [bearer]: the whole point is that this surface reads a cookie
and nothing else, so an application key presented here is never even looked at.
*/
type sessionVerifier struct{ service sessionAuthenticator }

func (sessionVerifier) present(request *http.Request) (string, bool) {
	return sessions.Present(request)
}

/*
challenge is empty because there is no registered WWW-Authenticate scheme for a
cookie, and naming one that does not exist would tell a client to do something
impossible.
*/
func (sessionVerifier) challenge() string { return "" }

// chargeAbsence is false: a page asking whether anybody is signed in carries no
// cookie by definition, and that is not a failed attempt.
func (sessionVerifier) chargeAbsence() bool { return false }

func (verify sessionVerifier) Verify(ctx context.Context, token string) (context.Context, error) {
	principal, err := verify.service.Authenticate(ctx, token)
	switch {
	case errors.Is(err, sessions.ErrUnauthenticated):
		return nil, errRefused
	case err != nil:
		return nil, err
	}
	return sessions.ContextWithPrincipal(ctx, principal), nil
}

/*
budgeted limits a route that authenticates nobody.

Signing in is the one place in Convia where guessing a secret pays, and it is
unauthenticated by definition — so the budget that protects every other route
cannot reach it, because that one lives inside [authenticate]. This wraps the
handler instead, charging a failure for any answer at or above 400.

Charging on the response rather than before it is what makes this honest: a
person who signs in correctly spends nothing, and only attempts that failed
count against the address that made them.
*/
func budgeted(logger *slog.Logger, failures *ratelimit.Limiter, resolve resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		source := resolve.clientAddress(request)
		if !failures.Allows(source) {
			slowDown(logger, response, request, "", failures.RetryAfter(source))
			return
		}

		recorder := &responseRecorder{ResponseWriter: response}
		next.ServeHTTP(recorder, request)

		if recorder.status >= http.StatusBadRequest {
			failures.Record(source)
		}
	})
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
			slowDown(logger, response, request, verify.challenge(), failures.RetryAfter(source))
			return
		}

		token, ok := verify.present(request)
		if !ok {
			if verify.chargeAbsence() {
				failures.Record(source)
			}
			refuse(logger, response, request, verify.challenge())
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
			refuse(logger, response, request, verify.challenge())
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

/*
refuse answers a request that did not authenticate, without saying why.

The challenge comes from the surface rather than being fixed here, because a
cookie has none to offer and claiming otherwise would be a header that tells a
client to present something Convia does not accept.
*/
func refuse(logger *slog.Logger, response http.ResponseWriter, request *http.Request, challenge string) {
	if challenge != "" {
		response.Header().Set("WWW-Authenticate", challenge)
	}

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
func slowDown(logger *slog.Logger, response http.ResponseWriter, request *http.Request,
	challenge string, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}

	response.Header().Set("Retry-After", strconv.Itoa(seconds))
	if challenge != "" {
		response.Header().Set("WWW-Authenticate", challenge)
	}

	failure := api.NewFailure(http.StatusTooManyRequests, api.CodeRateLimited,
		"Too many failed authentication attempts. Retry later.")
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write rate limited response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
