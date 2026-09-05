package users

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

/*
Attributes is a partial change to the fields an application owns.

An absent field is nil and keeps its stored value, which lets a client change a
display name without restating metadata, and distinguishes "leave this alone"
from "set this to nothing".
*/
type Attributes struct {
	DisplayName *string
	Metadata    *map[string]string
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

	/*
		The mapping is unique regardless of status, so a deleted user still owns
		its subject until erasure frees it. Resolving it again is refused rather
		than reviving the user, because a routine login must not undo a
		deletion, and returning the deleted user would contradict every other
		endpoint, which reports it as missing.
	*/
	if user.Status == StatusDeleted {
		return User{}, false, ErrSubjectDeleted
	}

	if isNew {
		service.audit(ctx, "user.created", user)
	}
	return user, isNew, nil
}

/*
Update changes the attributes an application owns.

Only the fields present in the request change, so a client can correct a
display name without restating metadata. Metadata is replaced rather than
merged: a client that sends it decides the whole object, which makes removing a
key possible and keeps the stored shape predictable.

When expectedVersion is set, the update applies only while the user still
carries that version, so a client that read a stale copy is refused rather than
silently overwriting a concurrent change. When it is empty, the last write wins.
*/
func (service *Service) Update(ctx context.Context, applicationID, id string,
	attributes Attributes, expectedVersion string) (User, error) {
	if attributes.DisplayName == nil && attributes.Metadata == nil {
		return User{}, ValidationError{Field: "body", Message: "The request must change at least one attribute."}
	}

	/*
		What the caller sent is validated before anything is read, so that a
		malformed request is rejected the same way whether or not the user
		exists.
	*/
	var name *string
	if attributes.DisplayName != nil {
		normalized, err := NormalizeDisplayName(*attributes.DisplayName)
		if err != nil {
			return User{}, err
		}
		name = &normalized
	}

	var metadata map[string]string
	if attributes.Metadata != nil {
		normalized, err := NormalizeMetadata(*attributes.Metadata)
		if err != nil {
			return User{}, err
		}
		metadata = normalized
	}

	current, err := service.Get(ctx, applicationID, id)
	if err != nil {
		return User{}, err
	}

	if name == nil {
		name = &current.DisplayName
	}
	if metadata == nil {
		metadata = current.Metadata
	}

	guard, err := precondition(current, expectedVersion)
	if err != nil {
		return User{}, err
	}

	updated, err := service.store.UpdateAttributes(ctx, applicationID, current.ID, *name, metadata, now(), guard)
	if err != nil {
		return User{}, wrapUpdate(err)
	}

	service.audit(ctx, "user.updated", updated)
	return updated, nil
}

/*
Suspend withdraws a user's access without losing data.

Suspending an already-suspended user changes nothing and reports success, so
that a repeated request is safe.
*/
func (service *Service) Suspend(ctx context.Context, applicationID, id string) (User, error) {
	return service.transition(ctx, applicationID, id, StatusSuspended, "user.suspended")
}

// Activate restores a suspended user to normal service.
func (service *Service) Activate(ctx context.Context, applicationID, id string) (User, error) {
	return service.transition(ctx, applicationID, id, StatusActive, "user.activated")
}

/*
Delete removes a user from the API surface.

The record is retained for the erasure window rather than destroyed, so the
deletion stays recoverable. Deleting an already-deleted user succeeds, and the
external subject stays taken until erasure frees it.
*/
func (service *Service) Delete(ctx context.Context, applicationID, id string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}
	if !ValidID(id) {
		return ErrNotFound
	}

	deleted, err := service.store.Delete(ctx, applicationID, id, now())
	if err != nil {
		return wrapUpdate(err)
	}
	if !deleted {
		return nil
	}

	service.audit(ctx, "user.deleted", User{ID: id, ApplicationID: applicationID, Status: StatusDeleted})
	return nil
}

// transition moves a user to a lifecycle state, or leaves it unchanged.
func (service *Service) transition(ctx context.Context, applicationID, id string,
	status Status, event string) (User, error) {
	current, err := service.Get(ctx, applicationID, id)
	if err != nil {
		return User{}, err
	}
	if current.Status == status {
		return current, nil
	}

	updated, err := service.store.SetStatus(ctx, applicationID, current.ID, status, now(), nil)
	if err != nil {
		return User{}, wrapUpdate(err)
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
func precondition(current User, expectedVersion string) (*time.Time, error) {
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
	return fmt.Errorf("update user: %w", err)
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
