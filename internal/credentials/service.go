package credentials

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

It asks whether an application is being served rather than whether it exists,
because a credential belongs to the application and must stop working the
moment the application stops being served.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
Service applies Convia's rules for application credentials.

It is the only place that turns a presented key into an identity, and the only
place that decides a key no longer works. Everything else in Convia receives
the result of that decision rather than making it again.
*/
type Service struct {
	store   *Store
	tenants tenants
	logger  *slog.Logger
}

func NewService(store *Store, owner tenants, logger *slog.Logger) *Service {
	return &Service{store: store, tenants: owner, logger: logger}
}

// Request is what an operator asks for when issuing a credential.
type Request struct {
	Name      string
	Scopes    []Scope
	ExpiresAt *time.Time
}

// ListOptions selects one page of an application's credentials.
type ListOptions struct {
	Limit  int
	Cursor string
}

// Page is one page of credentials and the token that continues it.
type Page struct {
	Credentials []Credential
	NextCursor  string
}

/*
Principal is who a verified key says it is.

It carries the application the key belongs to, so that every later decision
takes the tenant from the verified identity rather than from anything the
caller sent.
*/
type Principal struct {
	ApplicationID string
	CredentialID  string
	Scopes        []Scope
}

// Allows reports whether the principal carries a scope.
func (principal Principal) Allows(scope Scope) bool {
	for _, held := range principal.Scopes {
		if held == scope {
			return true
		}
	}
	return false
}

/*
Issue creates a credential and returns its secret for the only time.

The secret is generated here, hashed, and stored as a digest. What this returns
is the only copy that will ever exist: Convia cannot show it again, because
Convia does not have it.
*/
func (service *Service) Issue(ctx context.Context, applicationID string, request Request) (Credential, Secret, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Credential{}, "", err
	}

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
		ID:            NewID(),
		ApplicationID: applicationID,
		Name:          name,
		Scopes:        scopes,
		CreatedAt:     created,
		ExpiresAt:     truncate(request.ExpiresAt),
	}

	secret := NewSecret()
	if err := service.store.Create(ctx, credential, Digest(secret)); err != nil {
		return Credential{}, "", fmt.Errorf("issue credential: %w", err)
	}

	service.audit(ctx, "credential.issued", credential)
	return credential, secret, nil
}

/*
Authenticate turns a presented key into the identity it proves.

Every failure returns ErrUnauthenticated, whatever the reason, so that the
answer never distinguishes an unknown identifier from a wrong secret, or a
revoked key from an expired one. A caller learns that the key does not work.

The checks run in order of cost: shape first, so malformed input never reaches
the database, then one lookup by identifier, then the constant-time comparison,
then the lifecycle state, then whether Convia still serves the application.
*/
func (service *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	id, secret, err := ParseToken(token)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}

	credential, digest, err := service.store.ForAuthentication(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate credential: %w", err)
	}

	if !Matches(digest, secret) {
		return Principal{}, ErrUnauthenticated
	}
	if credential.Status(now()) != StatusActive {
		return Principal{}, ErrUnauthenticated
	}

	/*
		A key stops working when its application stops being served, without
		anyone having to revoke every key the application holds. Suspending an
		application therefore withdraws its access immediately.
	*/
	active, err := service.tenants.Active(ctx, credential.ApplicationID)
	if err != nil {
		return Principal{}, fmt.Errorf("check application: %w", err)
	}
	if !active {
		return Principal{}, ErrUnauthenticated
	}

	return Principal{
		ApplicationID: credential.ApplicationID,
		CredentialID:  credential.ID,
		Scopes:        credential.Scopes,
	}, nil
}

// Get returns one credential of an application, never its secret.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Credential, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Credential{}, err
	}
	if !ValidID(id) {
		return Credential{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

// List returns one page of an application's credentials, newest first.
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
		return Page{}, fmt.Errorf("list credentials: %w", err)
	}

	result := Page{Credentials: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Revoke withdraws a credential immediately.

There is no grace period and no denylist to propagate: the next request that
presents the key fails, because verification reads the stored row every time.
Revoking an already-revoked credential succeeds and records nothing further.
*/
func (service *Service) Revoke(ctx context.Context, applicationID, id string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}
	if !ValidID(id) {
		return ErrNotFound
	}

	revoked, err := service.store.Revoke(ctx, applicationID, id, now())
	if err != nil {
		return err
	}
	if !revoked {
		return nil
	}

	service.audit(ctx, "credential.revoked", Credential{ID: id, ApplicationID: applicationID})
	return nil
}

/*
requireApplication refuses work for an application Convia does not serve.

Issuing a key for a suspended application would produce one that cannot
authenticate, so the operator is told the tenant is not being served instead.
*/
func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	active, err := service.tenants.Active(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check application: %w", err)
	}
	if !active {
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
audit records a credential change.

The record names the credential by its public identifier and never carries the
secret, the digest, or the presented token. An audit trail that leaked key
material would be a second copy of the thing it exists to protect.
*/
func (service *Service) audit(ctx context.Context, event string, credential Credential) {
	service.logger.Info("audit event",
		"event", event,
		"credential_id", credential.ID,
		"application_id", credential.ApplicationID,
		"scopes", texts(credential.Scopes),
		"actor", "unauthenticated",
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
