package idempotency

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

/*
Service decides what a request carrying a key deserves.

The decision is deliberately narrow: replay the original answer, refuse, or
proceed. Nothing here knows what the operation does, which is what lets one
implementation serve every endpoint that needs the guarantee.
*/
type Service struct {
	store  *Store
	logger *slog.Logger
}

func NewService(store *Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// now is the clock, replaced in tests.
var now = func() time.Time { return time.Now().UTC() }

/*
Begin claims a key for an attempt, or reports what the first attempt answered.

A caller that is told to proceed owns the key until it calls Complete or
Release. Leaving it held would make every retry answer ErrInProgress until the
key expires, so a caller that can fail must release.
*/
func (service *Service) Begin(ctx context.Context, attempt Attempt) (Decision, error) {
	reserved, err := service.store.Reserve(ctx, attempt, now())
	if err != nil {
		return Decision{}, err
	}
	if reserved {
		return Decision{}, nil
	}

	/*
		The key is held. Reading it decides between the three ways a repeat can
		end, and the read can find nothing when the holder released it in the
		moment between the two statements. That is a caller whose first attempt
		failed and gave the key back, so the honest answer is to try again.
	*/
	stored, err := service.store.get(ctx, attempt.Scope, attempt.Key)
	if errors.Is(err, errNoRecord) {
		return Decision{}, ErrInProgress
	}
	if err != nil {
		return Decision{}, err
	}

	if stored.RequestDigest != attempt.digest() {
		return Decision{}, ErrConflictingRequest
	}
	if !stored.completed() {
		return Decision{}, ErrInProgress
	}

	service.logger.Info("replayed an idempotent request",
		"scope", attempt.Scope,
		"method", attempt.Method,
		"path", attempt.Path,
		"status", *stored.Status,
	)

	return Decision{
		Replay: true,
		Result: Result{Status: *stored.Status, Headers: stored.Headers, Body: stored.Body},
	}, nil
}

/*
Complete records the response so a repeat of the same request replays it.

Only an answer the server stands behind is recorded. A response it might give
differently on the next attempt -- an internal error, or a refusal to serve
right now -- releases the key instead, because storing it would turn a
transient failure into a permanent one for the whole retention window: every
retry would be answered with the failure rather than allowed to succeed.
*/
func (service *Service) Complete(ctx context.Context, scope, key string, result Result) error {
	if !worthReplaying(result.Status) {
		return service.store.Release(ctx, scope, key)
	}
	return service.store.Complete(ctx, scope, key, result, now())
}

// Release gives an unfinished key back so the next attempt is a real one.
func (service *Service) Release(ctx context.Context, scope, key string) error {
	return service.store.Release(ctx, scope, key)
}

/*
worthReplaying reports whether a status is an answer the server means to keep.

A rejection is kept: the request was understood and refused, and repeating it
unchanged will be refused again, so replaying is both faster and truthful. A
server error or a rate-limited refusal is not, because both invite the client
to come back and find a different answer.
*/
func worthReplaying(status int) bool {
	return status < http.StatusInternalServerError && status != http.StatusTooManyRequests
}
