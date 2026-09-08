package operator

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
Service applies Convia's rules for operator credentials.

It is the only place that turns a presented operator key into an identity. It
asks no question about tenancy, because an operator belongs to no tenant: what
it may do is decided entirely by the scopes on its own key.
*/
type Service struct {
	store  *Store
	logger *slog.Logger
}

func NewService(store *Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// Request is what an operator asks for when issuing an operator credential.
type Request struct {
	Name      string
	Scopes    []Scope
	ExpiresAt *time.Time
}

// ListOptions selects one page of operator credentials.
type ListOptions struct {
	Limit  int
	Cursor string
}

// Page is one page of operator credentials and the token that continues it.
type Page struct {
	Credentials []Credential
	NextCursor  string
}

/*
Issue creates an operator credential and returns its secret for the only time.

The secret is generated here, hashed, and stored as a digest. What this returns
is the only copy that will ever exist.
*/
func (service *Service) Issue(ctx context.Context, request Request) (Credential, Secret, error) {
	name, err := NormalizeName(request.Name)
	if err != nil {
		return Credential{}, "", err
	}
	scopes, err := NormalizeScopes(request.Scopes)
	if err != nil {
		return Credential{}, "", err
	}

	created := now()
	if request.ExpiresAt != nil && !request.ExpiresAt.After(created) {
		return Credential{}, "", ValidationError{
			Field:   "expires_at",
			Message: "The expiry must be in the future.",
		}
	}

	credential := Credential{
		ID:        NewID(),
		Name:      name,
		Scopes:    scopes,
		CreatedAt: created,
		ExpiresAt: truncate(request.ExpiresAt),
	}

	value := NewSecret()
	if err := service.store.Create(ctx, credential, Digest(value)); err != nil {
		return Credential{}, "", fmt.Errorf("issue operator credential: %w", err)
	}

	service.audit(ctx, "operator_credential.issued", credential)
	return credential, value, nil
}

/*
Authenticate turns a presented operator key into the identity it proves.

Every failure returns ErrUnauthenticated, whatever the reason, so the answer
never distinguishes an unknown identifier from a wrong secret, or a revoked key
from an expired one.

The checks run in order of cost: shape first, so malformed input and any
application key never reach the database, then one lookup by identifier, then
the constant-time comparison, then the lifecycle state.

Unlike the tenant equivalent there is no final tenancy check, because an
operator credential is owned by nobody who could be suspended. Withdrawing one
means revoking it.
*/
func (service *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	id, value, err := ParseToken(token)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}

	credential, digest, err := service.store.ForAuthentication(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate operator credential: %w", err)
	}

	if !Matches(digest, value) {
		return Principal{}, ErrUnauthenticated
	}
	if credential.Status(now()) != StatusActive {
		return Principal{}, ErrUnauthenticated
	}

	return Principal{CredentialID: credential.ID, Scopes: credential.Scopes}, nil
}

// Get returns one operator credential, never its secret.
func (service *Service) Get(ctx context.Context, id string) (Credential, error) {
	if !ValidID(id) {
		return Credential{}, ErrNotFound
	}
	return service.store.Get(ctx, id)
}

// List returns one page of operator credentials, newest first.
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
		return Page{}, fmt.Errorf("list operator credentials: %w", err)
	}

	result := Page{Credentials: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Revoke withdraws an operator credential immediately.

There is no grace period and no denylist to propagate: the next request that
presents the key fails, because verification reads the stored row every time.
Revoking an already-revoked credential succeeds and records nothing further.
*/
func (service *Service) Revoke(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}

	revoked, err := service.store.Revoke(ctx, id, now())
	if err != nil {
		return err
	}
	if !revoked {
		return nil
	}

	service.audit(ctx, "operator_credential.revoked", Credential{ID: id})
	return nil
}

/*
CountActive reports how many operator credentials currently authenticate.

The composition root calls it once at startup so that an instance nobody can
administer says so in its logs, rather than letting the operator find out by
being refused.
*/
func (service *Service) CountActive(ctx context.Context) (int, error) {
	return service.store.CountActive(ctx, now())
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
audit records an operator credential change.

The record names the credential by its public identifier and never carries the
secret, the digest, or the presented token.
*/
func (service *Service) audit(ctx context.Context, event string, credential Credential) {
	actor := "bootstrap"
	if principal, found := PrincipalFromContext(ctx); found {
		actor = principal.CredentialID
	}

	service.logger.Info("audit event",
		"event", event,
		"operator_credential_id", credential.ID,
		"scopes", texts(credential.Scopes),
		"actor", actor,
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// truncate normalizes an optional caller-supplied timestamp to stored precision.
func truncate(moment *time.Time) *time.Time {
	if moment == nil {
		return nil
	}

	normalized := moment.UTC().Truncate(time.Microsecond)
	return &normalized
}
