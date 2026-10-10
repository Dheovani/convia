package applications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"convia/internal/audit"
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
	store *Store
	trail trail
}

/*
trail is the durable audit record this service writes to.

It is an interface declared here rather than the concrete service so that these
rules can be exercised without a trail's own storage, and it is **not optional**
for the reason `M21-008` exists: a change that records nothing is a change
nobody can be held to.
*/
type trail interface {
	Record(ctx context.Context, written audit.Written) (audit.Entry, error)
}

/*
NewService builds the service.

**There is no logger**, which there was until the trail arrived. The only thing
this service ever logged was an audit line, and the trail writes that now -- as
a row and as a line, in one place, from the actor it read off the request. A
second logger here would be a second account of the same occurrence.
*/
func NewService(store *Store, entries trail) *Service {
	return &Service{store: store, trail: entries}
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

	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		if err := service.store.Create(ctx, application); err != nil {
			return fmt.Errorf("create application: %w", err)
		}
		return service.audit(ctx, "application.created", application)
	})
	if err != nil {
		return Application{}, err
	}

	return application, nil
}

/*
EnsureFirstParty makes the application that is Convia's own product, unless it
already exists.

It runs when Convia starts, so a fresh installation can be signed in to without
anybody administering it first. A row that already exists is never touched: an
operator who suspended Convia's own product meant it, and starting again must
not quietly undo that. Suspension still does what it always did — every session
stops authenticating — which is why nothing here checks the state.
*/
func (service *Service) EnsureFirstParty(ctx context.Context) error {
	created := now()
	application := Application{
		ID:        FirstPartyID,
		Name:      firstPartyName,
		Status:    StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}

	return service.store.Atomically(ctx, func(ctx context.Context) error {
		made, err := service.store.CreateIfAbsent(ctx, application)
		if err != nil {
			return fmt.Errorf("ensure the first-party application: %w", err)
		}
		if !made {
			return nil
		}
		return service.audit(ctx, "application.created", application)
	})
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

/*
Exists reports whether Convia serves an application.

Other packages depend on this narrow question rather than on the application
record itself, so that a tenant-scoped resource can refuse work for an unknown
or deleted application without reading data it has no reason to see.
*/
func (service *Service) Exists(ctx context.Context, id string) (bool, error) {
	_, err := service.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

/*
Active reports whether Convia currently serves an application.

It is stricter than Exists: a suspended application still exists, and an
operator may still administer it, but it is not being served. Anything acting
on the application's own behalf — above all, authenticating its credentials —
asks this question instead, so that suspension actually withdraws access rather
than only recording an intention to.
*/
func (service *Service) Active(ctx context.Context, id string) (bool, error) {
	application, err := service.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return application.Status == StatusActive, nil
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

	var renamed Application
	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		renamed, err = service.store.Rename(ctx, current.ID, normalized, now(), guard)
		if err != nil {
			return wrapUpdate(err)
		}
		return service.audit(ctx, "application.renamed", renamed)
	})
	if err != nil {
		return Application{}, err
	}

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

	return service.store.Atomically(ctx, func(ctx context.Context) error {
		deleted, err := service.store.Delete(ctx, id, now())
		if err != nil {
			return wrapUpdate(err)
		}
		if !deleted {
			return nil
		}
		return service.audit(ctx, "application.deleted", Application{ID: id, Status: StatusDeleted})
	})
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

	var updated Application
	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		updated, err = service.store.SetStatus(ctx, current.ID, status, now(), nil)
		if err != nil {
			return wrapUpdate(err)
		}
		return service.audit(ctx, event, updated)
	})
	if err != nil {
		return Application{}, err
	}

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

**It used to lie about the actor.** Every line it wrote said
`actor=unauthenticated`, beside a comment promising that the actor would become
the authenticated principal "once credentials exist in M07" -- which they have
since `M07` closed, so every entry written since has named the wrong authority
for a change only an operator can make. `M21` is where that is fixed, and the
fix is not a better string: the actor is read from the request by the trail
itself, where the middleware that verified a credential put it, so no service
can name an actor at all.

The status is not recorded beside it, because the action already says it:
`application.suspended` is what suspension is, and a status field would be a
second place for the same fact to be wrong in.
*/
func (service *Service) audit(ctx context.Context, action string, application Application) error {
	_, err := service.trail.Record(ctx, audit.Written{
		Action:        action,
		Subject:       audit.Subject{Kind: "application", ID: application.ID},
		ApplicationID: application.ID,
	})
	return err
}
