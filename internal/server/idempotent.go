package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/idempotency"
	"convia/internal/operator"
)

/*
maxReplayableBytes bounds the response body kept for a replay.

Every response Convia stores under a key is a single resource, which is far
below this. A handler that answered with more is not refused, and the client
receives it in full, but the key is released rather than remembered: holding an
unbounded body to serve a retry that may never come is the wrong trade.
*/
const maxReplayableBytes = 64 << 10 // 64 KiB

/*
storeTimeout bounds recording the outcome of an attempt.

It runs after the client has been answered, on a context detached from the
request, so it needs a deadline of its own or a stalled database would hold a
goroutine indefinitely.
*/
const storeTimeout = 5 * time.Second

/*
keyRegistry is the behavior the server needs to honor an idempotency key.

The server depends on this narrow interface rather than on the service, so the
middleware stays testable without PostgreSQL. *idempotency.Service satisfies it.
*/
type keyRegistry interface {
	Begin(ctx context.Context, attempt idempotency.Attempt) (idempotency.Decision, error)
	Complete(ctx context.Context, scope, key string, result idempotency.Result) error
	Release(ctx context.Context, scope, key string) error
}

/*
idempotent honors an Idempotency-Key on a mutation.

The header is optional. A request without one is served exactly as before, so
adding this to a route changes nothing for a client that does not ask for the
guarantee.

A request with one is claimed before the handler runs. When the same key
already answered the same request, that answer is replayed and the handler is
never reached, which is the whole point: the operation must not happen twice.
*/
func idempotent(logger *slog.Logger, keys keyRegistry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		/*
			Presence is read from the header map rather than from the value,
			because net/http strips the optional whitespace around a field
			value before a handler sees it. A client whose key generator
			returned nothing sends a header with an empty value, and asking
			only whether the value is empty would make that indistinguishable
			from not asking for the guarantee at all -- so the request would be
			served unprotected by a client that believed it was protected.
		*/
		if _, asked := request.Header[http.CanonicalHeaderKey(idempotency.Header)]; !asked {
			next.ServeHTTP(response, request)
			return
		}

		/*
			A key was presented and nothing can honor it. Serving the request
			anyway would perform a mutation the caller believes is protected,
			which is the one outcome the header exists to prevent, so it is
			refused as the misconfiguration it is.
		*/
		if keys == nil {
			logger.Error("idempotent route reached without a key registry",
				"method", request.Method,
				"path", request.URL.Path,
				"request_id", api.RequestIDFromContext(request.Context()),
			)
			writeFailure(logger, response, request, api.NewFailure(http.StatusInternalServerError,
				api.CodeInternal, "The server encountered an unexpected condition."))
			return
		}

		key, err := idempotency.NormalizeKey(request.Header.Get(idempotency.Header))
		if err != nil {
			writeFailure(logger, response, request, api.NewFailure(http.StatusBadRequest,
				api.CodeInvalidRequest, err.Error()))
			return
		}

		/*
			The key belongs to whoever proved themselves, not to the
			application named in the path. Scoping it to the caller means an
			operator and a tenant using the same key against the same
			application never meet, which per-application scoping alone would
			not prevent.
		*/
		scope, found := scopeFor(request.Context())
		if !found {
			logger.Error("idempotent route reached without a principal",
				"method", request.Method,
				"path", request.URL.Path,
				"request_id", api.RequestIDFromContext(request.Context()),
			)
			writeFailure(logger, response, request, api.NewFailure(http.StatusUnauthorized,
				api.CodeUnauthenticated, "The request did not carry a usable credential."))
			return
		}

		body, failure := readBody(response, request)
		if failure != nil {
			writeFailure(logger, response, request, failure)
			return
		}

		attempt := idempotency.Attempt{
			Scope:  scope,
			Key:    key,
			Method: request.Method,
			Path:   request.URL.Path,
			Body:   body,
		}

		decision, err := keys.Begin(request.Context(), attempt)
		switch {
		case errors.Is(err, idempotency.ErrConflictingRequest):
			writeFailure(logger, response, request, api.NewFailure(http.StatusConflict, api.CodeConflict,
				"The Idempotency-Key was already used for a different request."))
			return

		case errors.Is(err, idempotency.ErrInProgress):
			writeFailure(logger, response, request, api.NewFailure(http.StatusConflict, api.CodeConflict,
				"A request with this Idempotency-Key is still in progress. Retry to collect its result."))
			return

		case err != nil:
			/*
				The operation is refused rather than performed unguarded.
				Proceeding would risk exactly the duplicate the caller asked to
				be protected from, and a caller that asked for the guarantee
				would rather retry than discover it did not hold.
			*/
			logger.Error("claim idempotency key",
				"error", err,
				"method", request.Method,
				"path", request.URL.Path,
				"request_id", api.RequestIDFromContext(request.Context()),
			)
			writeFailure(logger, response, request, api.NewFailure(http.StatusInternalServerError,
				api.CodeInternal, "The server encountered an unexpected condition."))
			return
		}

		if decision.Replay {
			replay(logger, response, request, decision.Result)
			return
		}

		serveAndRecord(logger, keys, next, response, request, attempt)
	})
}

/*
serveAndRecord runs the handler and stores what it answered.

The response is written through rather than held back until the key is stored,
so honoring the header costs the client nothing. A copy is kept alongside it,
and that copy is what a later retry replays.
*/
func serveAndRecord(logger *slog.Logger, keys keyRegistry, next http.Handler,
	response http.ResponseWriter, request *http.Request, attempt idempotency.Attempt) {
	recorder := &replayRecorder{ResponseWriter: response, status: http.StatusOK}

	served := request.Clone(request.Context())
	served.Body = io.NopCloser(bytes.NewReader(attempt.Body))
	served.ContentLength = int64(len(attempt.Body))

	/*
		A handler that panics answered nothing a caller can use, so the key
		goes back and its retry is a real attempt. The panic itself keeps
		travelling: turning it into a response is recoverPanic's job, not this
		one's.
	*/
	finished := false
	defer func() {
		if !finished {
			release(logger, keys, request, attempt)
		}
	}()

	next.ServeHTTP(recorder, served)
	finished = true

	if recorder.overflowed {
		logger.Warn("idempotent response too large to replay",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		release(logger, keys, request, attempt)
		return
	}

	result := idempotency.Result{
		Status:  recorder.status,
		Headers: replayableHeaders(recorder.Header()),
		Body:    recorder.body.Bytes(),
	}

	/*
		Recording uses a context detached from the request, because a client
		that disconnected after being answered must still leave the key
		recorded. Without that, its retry would perform the operation again.
	*/
	ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), storeTimeout)
	defer cancel()

	if err := keys.Complete(ctx, attempt.Scope, attempt.Key, result); err != nil {
		logger.Error("store idempotent response",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

// release gives an unfinished key back so the next attempt is a real one.
func release(logger *slog.Logger, keys keyRegistry, request *http.Request, attempt idempotency.Attempt) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), storeTimeout)
	defer cancel()

	if err := keys.Release(ctx, attempt.Scope, attempt.Key); err != nil {
		logger.Error("release idempotency key",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

/*
replay answers a repeat with what the first request produced.

The correlation identifier is deliberately not restored. It belongs to the
request being served now, not to the one that produced this body, and returning
the older one would make the response impossible to find in a log.
*/
func replay(logger *slog.Logger, response http.ResponseWriter, request *http.Request,
	result idempotency.Result) {
	for name, value := range result.Headers {
		response.Header().Set(name, value)
	}

	response.WriteHeader(result.Status)
	if _, err := response.Write(result.Body); err != nil {
		logger.Error("write replayed response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

/*
scopeFor names who a key belongs to.

An application's keys are scoped to the application, which is the guarantee
docs/api-compatibility.md states. An operator's are scoped to its credential,
because an operator acts on many applications and its keys are its own.
*/
func scopeFor(ctx context.Context) (string, bool) {
	if principal, found := credentials.PrincipalFromContext(ctx); found {
		return principal.ApplicationID, true
	}
	if principal, found := operator.PrincipalFromContext(ctx); found {
		return "operator:" + principal.CredentialID, true
	}
	return "", false
}

/*
readBody reads the request so it can be fingerprinted and then handed on.

The bound is the shared JSON limit, so a body refused here is one no handler
would have accepted either.
*/
func readBody(response http.ResponseWriter, request *http.Request) ([]byte, *api.Failure) {
	if request.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, api.MaxJSONRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, api.NewFailure(http.StatusRequestEntityTooLarge, api.CodePayloadTooLarge,
				fmt.Sprintf("The request body must not exceed %d bytes.", api.MaxJSONRequestBytes))
		}
		return nil, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"The request body could not be read.")
	}
	return body, nil
}

/*
replayableHeaders selects the headers a stored response keeps.

Headers describing this exchange rather than the resource are dropped: the
correlation identifier and the date belong to the request that produced them,
and the length is recomputed when the body is written again.
*/
func replayableHeaders(header http.Header) map[string]string {
	/*
		The names are canonicalized on both sides. Convia spells its
		correlation header `X-Request-ID`, which is not the canonical form
		net/http stores it under, so comparing the raw spellings would quietly
		fail to drop it.
	*/
	skipped := map[string]bool{
		http.CanonicalHeaderKey(api.RequestIDHeader): true,
		"Date":           true,
		"Content-Length": true,
	}

	kept := make(map[string]string, len(header))
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if skipped[canonical] {
			continue
		}
		kept[canonical] = header.Get(name)
	}
	return kept
}

/*
replayRecorder writes the response through while keeping a copy to store.

A body past the bound stops being copied rather than truncated, so a partial
response is never mistaken for a whole one and replayed as if it were.
*/
type replayRecorder struct {
	http.ResponseWriter
	status     int
	body       bytes.Buffer
	written    bool
	overflowed bool
}

func (recorder *replayRecorder) WriteHeader(status int) {
	if recorder.written {
		return
	}
	recorder.status = status
	recorder.written = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *replayRecorder) Write(body []byte) (int, error) {
	if !recorder.written {
		recorder.WriteHeader(http.StatusOK)
	}

	switch {
	case recorder.overflowed:
	case recorder.body.Len()+len(body) > maxReplayableBytes:
		recorder.overflowed = true
		recorder.body.Reset()
	default:
		recorder.body.Write(body)
	}

	return recorder.ResponseWriter.Write(body)
}

func (recorder *replayRecorder) headerWritten() bool {
	return recorder.written
}

// Unwrap keeps http.ResponseController working for the wrapped handler.
func (recorder *replayRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func writeFailure(logger *slog.Logger, response http.ResponseWriter, request *http.Request,
	failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
