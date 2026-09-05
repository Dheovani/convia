package applications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
)

const (
	// defaultPageSize and maxPageSize implement the pagination bounds defined
	// in docs/api-conventions.md.
	defaultPageSize = 25
	maxPageSize     = 100
)

/*
Service applies Convia's application rules.

Every invariant is enforced here rather than in a handler, so the same rules
hold for any future caller: an administrative endpoint, a command, or another
service inside Convia.
*/
type Service struct {
	store  *Store
	logger *slog.Logger
}

func NewService(store *Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// ListOptions selects one page of applications.
type ListOptions struct {
	Limit  int
	Cursor string
}

// Page is one page of applications and the token that continues it.
type Page struct {
	Applications []Application
	NextCursor   string
}

/*
Create registers a new application.

The identifier and timestamps are assigned by Convia rather than by the caller,
so a client can neither choose an identifier nor backdate a tenant. Timestamps
are truncated to microseconds because that is the precision PostgreSQL keeps,
which makes a stored application compare equal to the one returned here.
*/
func (service *Service) Create(ctx context.Context, name string) (Application, error) {
	normalized, err := NormalizeName(name)
	if err != nil {
		return Application{}, err
	}

	created := now()
	application := Application{
		ID:        NewID(),
		Name:      normalized,
		Status:    StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}

	if err := service.store.Create(ctx, application); err != nil {
		return Application{}, fmt.Errorf("create application: %w", err)
	}

	service.audit(ctx, "application.created", application)
	return application, nil
}

// Get returns one application, or ErrNotFound.
func (service *Service) Get(ctx context.Context, id string) (Application, error) {
	if !ValidID(id) {
		/*
			An identifier that cannot exist is reported as missing rather than
			as invalid, so that probing identifier shapes reveals nothing about
			which applications exist.
		*/
		return Application{}, ErrNotFound
	}

	application, err := service.store.Get(ctx, id)
	if err != nil {
		return Application{}, err
	}
	return application, nil
}

// List returns one page of applications, newest first.
func (service *Service) List(ctx context.Context, options ListOptions) (Page, error) {
	limit, err := pageSize(options.Limit)
	if err != nil {
		return Page{}, err
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}

	page, hasMore, err := service.store.List(ctx, cursor, limit)
	if err != nil {
		return Page{}, fmt.Errorf("list applications: %w", err)
	}

	result := Page{Applications: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Rename changes the display name of an application.

When expectedVersion is set, the rename applies only while the application
still carries that version, so a client that read a stale copy is refused
rather than silently overwriting a concurrent change. When it is empty, the
rename is unconditional and the last write wins.
*/
func (service *Service) Rename(ctx context.Context, id, name, expectedVersion string) (Application, error) {
	normalized, err := NormalizeName(name)
	if err != nil {
		return Application{}, err
	}

	current, err := service.Get(ctx, id)
	if err != nil {
		return Application{}, err
	}

	guard, err := precondition(current, expectedVersion)
	if err != nil {
		return Application{}, err
	}

	renamed, err := service.store.Rename(ctx, current.ID, normalized, now(), guard)
	if err != nil {
		return Application{}, wrapUpdate(err)
	}

	service.audit(ctx, "application.renamed", renamed)
	return renamed, nil
}

/*
Suspend withdraws access without losing data.

Suspending an already-suspended application changes nothing and reports
success, so that a repeated request is safe.
*/
func (service *Service) Suspend(ctx context.Context, id string) (Application, error) {
	return service.transition(ctx, id, StatusSuspended, "application.suspended")
}

// Activate restores a suspended application to normal service.
func (service *Service) Activate(ctx context.Context, id string) (Application, error) {
	return service.transition(ctx, id, StatusActive, "application.activated")
}

/*
Delete removes an application from the API surface.

The record is retained for the erasure window rather than destroyed, so the
deletion stays recoverable. Deleting an already-deleted application succeeds.
*/
func (service *Service) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}

	deleted, err := service.store.Delete(ctx, id, now())
	if err != nil {
		return wrapUpdate(err)
	}
	if !deleted {
		return nil
	}

	service.audit(ctx, "application.deleted", Application{ID: id, Status: StatusDeleted})
	return nil
}

// transition moves an application to a lifecycle state, or leaves it unchanged.
func (service *Service) transition(ctx context.Context, id string, status Status, event string) (Application, error) {
	current, err := service.Get(ctx, id)
	if err != nil {
		return Application{}, err
	}
	if current.Status == status {
		return current, nil
	}

	updated, err := service.store.SetStatus(ctx, current.ID, status, now(), nil)
	if err != nil {
		return Application{}, wrapUpdate(err)
	}

	service.audit(ctx, event, updated)
	return updated, nil
}

/*
precondition converts a client-supplied version into a storage guard.

The comparison happens here so that a stale version is refused before any write
is attempted, and the guard repeats the check inside the update itself so that
a change arriving in between is refused too.
*/
func precondition(current Application, expectedVersion string) (*time.Time, error) {
	if expectedVersion == "" {
		return nil, nil
	}
	if expectedVersion != current.Version() {
		return nil, ErrPreconditionFailed
	}

	guard := current.UpdatedAt
	return &guard, nil
}

/*
wrapUpdate preserves the domain errors a caller must distinguish.

Anything else is an infrastructure failure and keeps its context for the logs.
*/
func wrapUpdate(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPreconditionFailed) {
		return err
	}
	return fmt.Errorf("update application: %w", err)
}

/*
now returns the timestamp Convia stores for a change.

It is truncated to microseconds because that is the precision PostgreSQL keeps,
which makes a stored application compare equal to the one returned to a caller.
*/
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

/*
pageSize validates a requested page size.

An oversized limit is rejected rather than silently clamped, so that a client
never believes it received every result it asked for.
*/
func pageSize(requested int) (int, error) {
	switch {
	case requested == 0:
		return defaultPageSize, nil
	case requested < 0 || requested > maxPageSize:
		return 0, ValidationError{
			Field:   "limit",
			Message: fmt.Sprintf("The limit must be between 1 and %d.", maxPageSize),
		}
	default:
		return requested, nil
	}
}

/*
audit records a security-relevant change to an application.

Audit entries are structured logs correlated by request ID. They are an
operational record, not yet a queryable audit trail; M21 introduces durable
audit storage. Once credentials exist in M07, the actor becomes the
authenticated principal instead of a placeholder.
*/
func (service *Service) audit(ctx context.Context, event string, application Application) {
	service.logger.Info("audit event",
		"event", event,
		"application_id", application.ID,
		"application_status", application.Status,
		"actor", "unauthenticated",
		"request_id", api.RequestIDFromContext(ctx),
	)
}
