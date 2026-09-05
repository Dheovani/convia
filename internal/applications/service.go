package applications

import (
	"context"
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

	now := time.Now().UTC().Truncate(time.Microsecond)
	application := Application{
		ID:        NewID(),
		Name:      normalized,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
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
