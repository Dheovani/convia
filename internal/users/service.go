package users

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
tenants is the behavior this package needs from the tenancy package.

It is declared here, in the consuming package, so that users depends on a
narrow capability rather than on the application service as a whole.
*/
type tenants interface {
	Exists(ctx context.Context, applicationID string) (bool, error)
}

/*
Service applies Convia's rules for the people an application knows about.

Every operation takes the owning application, and every lookup is scoped to it,
so a caller cannot reach another tenant's users even with a valid identifier.
*/
type Service struct {
	store   *Store
	tenants tenants
	logger  *slog.Logger
}

func NewService(store *Store, owner tenants, logger *slog.Logger) *Service {
	return &Service{store: store, tenants: owner, logger: logger}
}

// Identity is the request to resolve an application's person into a Convia user.
type Identity struct {
	ExternalSubject string
	DisplayName     string
	Metadata        map[string]string
}

// ListOptions selects one page of an application's users.
type ListOptions struct {
	Limit  int
	Cursor string
}

// Page is one page of users and the token that continues it.
type Page struct {
	Users      []User
	NextCursor string
}

/*
Resolve maps an application's person to a Convia user, creating it if needed.

The operation is idempotent by the external subject rather than by a client
supplied key: calling it twice with the same subject returns the same user, so
an application can call it before every session without tracking whether it has
called it before. It reports whether the user was created, which lets the
transport layer answer 201 or 200 truthfully.

An existing user is returned unchanged. Updating a display name or metadata is
an explicit operation, so that a routine resolve cannot silently overwrite what
an operator corrected.
*/
func (service *Service) Resolve(ctx context.Context, applicationID string, identity Identity) (User, bool, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return User{}, false, err
	}

	subject, err := NormalizeExternalSubject(identity.ExternalSubject)
	if err != nil {
		return User{}, false, err
	}
	name, err := NormalizeDisplayName(identity.DisplayName)
	if err != nil {
		return User{}, false, err
	}
	metadata, err := NormalizeMetadata(identity.Metadata)
	if err != nil {
		return User{}, false, err
	}

	created := now()
	user, isNew, err := service.store.Resolve(ctx, User{
		ID:              NewID(),
		ApplicationID:   applicationID,
		ExternalSubject: subject,
		DisplayName:     name,
		Metadata:        metadata,
		Status:          StatusActive,
		CreatedAt:       created,
		UpdatedAt:       created,
	})
	if err != nil {
		return User{}, false, fmt.Errorf("resolve user: %w", err)
	}

	if isNew {
		service.audit(ctx, "user.created", user)
	}
	return user, isNew, nil
}

/*
Get returns one user of an application.

An identifier that cannot exist, belongs to another application, or names a
deleted user is reported identically, so that a caller cannot use this endpoint
to discover which identifiers exist elsewhere in Convia.
*/
func (service *Service) Get(ctx context.Context, applicationID, id string) (User, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return User{}, err
	}
	if !ValidID(id) {
		return User{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

// List returns one page of an application's users, newest first.
func (service *Service) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Page{}, err
	}

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

	page, hasMore, err := service.store.List(ctx, applicationID, cursor, limit)
	if err != nil {
		return Page{}, fmt.Errorf("list users: %w", err)
	}

	result := Page{Users: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
requireApplication refuses work for an application Convia does not serve.

Every user operation starts here, so a request naming an unknown or deleted
application is rejected before it can touch the users table at all.
*/
func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	exists, err := service.tenants.Exists(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check application: %w", err)
	}
	if !exists {
		return ErrApplicationNotFound
	}
	return nil
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
audit records a change to an identity.

As with applications, audit entries are structured logs correlated by request
ID rather than a queryable trail, and the actor is a placeholder until M07
introduces authenticated principals. The external subject is deliberately
absent: it is application-owned data that may identify a person, and an audit
record does not need it to be useful.
*/
func (service *Service) audit(ctx context.Context, event string, user User) {
	service.logger.Info("audit event",
		"event", event,
		"user_id", user.ID,
		"application_id", user.ApplicationID,
		"user_status", user.Status,
		"actor", "unauthenticated",
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
