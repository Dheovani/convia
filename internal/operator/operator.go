/*
Package operator owns how a person or system running Convia proves who it is.

An operator credential authenticates the party that operates Convia itself:
the one that creates tenants, suspends them, and issues their first keys. It is
deliberately a different thing from an application credential, in a different
table, with a different token prefix and its own scopes.

# Why a separate table

An application credential is always scoped to one application, and every query
in the credentials package carries that application in its WHERE clause. Making
an operator credential a row in the same table with no application would mean
one forgotten predicate could return it, turning an omission into a privilege
escalation. A separate table makes that unrepresentable: the credentials store
cannot return an operator credential, because it does not read this table.

# Why a different token prefix

A presented key names its own family. Convia routes it to one verifier without
querying for it, an operator key offered to a tenant route is refused on its
shape before any database work, and a leaked key can be recognized as the
higher-privilege kind by sight.
*/
package operator

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"convia/internal/secret"
)

/*
format is how an operator credential is rendered and parsed.

"cvo" is one letter from the "cvk" an application uses, which is deliberate:
the two are equally recognizable to a secret scanner, and distinct enough that
neither parses as the other.
*/
var format = secret.Format{Token: "cvo", ID: "oper_"}

const maxNameLength = 120

// ErrNotFound reports that no operator credential matches the identifier.
var ErrNotFound = errors.New("operator credential not found")

/*
ErrUnauthenticated reports that a presented key does not authenticate anyone.

As on the tenant surface, every reason a key can fail produces this one error,
so the answer cannot be used to learn which identifiers exist or which were
once valid.
*/
var ErrUnauthenticated = errors.New("operator credential does not authenticate")

/*
Scope is one permission an operator credential may carry.

Operator scopes are named separately from application scopes on purpose. On a
tenant key, "users:write" means "my users". An operator acts on someone else's,
so reusing the name would make one string mean two different amounts of
authority depending on which key carried it.
*/
type Scope string

const (
	// ScopeApplicationsRead permits listing and reading tenants.
	ScopeApplicationsRead Scope = "applications:read"
	/*
		ScopeApplicationsWrite permits creating, renaming, suspending,
		activating, and deleting tenants.

		Suspending an application withdraws every key it holds at once, so this
		scope is the one that can take a tenant offline.
	*/
	ScopeApplicationsWrite Scope = "applications:write"
	// ScopeTenantsRead permits reading any application's users and credentials.
	ScopeTenantsRead Scope = "tenants:read"
	/*
		ScopeTenantsWrite permits managing any application's users and
		credentials, including issuing a key on a tenant's behalf.

		This is the scope that bootstraps a tenant, and the most dangerous one
		Convia grants: a holder can mint an application key carrying any
		application scope.
	*/
	ScopeTenantsWrite Scope = "tenants:write"
	// ScopeOperatorsRead permits reading operator credentials, never their secrets.
	ScopeOperatorsRead Scope = "operators:read"
	/*
		ScopeOperatorsWrite permits issuing and revoking operator credentials.

		This is the scope that grants authority over Convia itself, so issuing
		is bounded by a subset rule: an operator can only mint a key carrying
		scopes it already holds. Without that, one key with only this scope
		could mint another carrying everything.
	*/
	ScopeOperatorsWrite Scope = "operators:write"
)

/*
Scopes returns every operator scope Convia recognizes.

The contract test uses it to prove that the API specification and the
implementation describe the same permissions.
*/
func Scopes() []Scope {
	return []Scope{
		ScopeApplicationsRead, ScopeApplicationsWrite,
		ScopeTenantsRead, ScopeTenantsWrite,
		ScopeOperatorsRead, ScopeOperatorsWrite,
	}
}

// Status is the lifecycle state of an operator credential, derived rather than stored.
type Status string

const (
	// StatusActive means the credential authenticates.
	StatusActive Status = "active"
	// StatusExpired means the credential passed its expiry.
	StatusExpired Status = "expired"
	// StatusRevoked means the credential was withdrawn deliberately.
	StatusRevoked Status = "revoked"
)

/*
Credential is an issued operator key, without its secret.

It carries no application, which is the point: an operator is not a tenant, and
nothing about this record scopes it to one.
*/
type Credential struct {
	ID        string
	Name      string
	Scopes    []Scope
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

/*
Status reports the lifecycle state at the given moment.

Revocation outranks expiry: a credential that was withdrawn and then also
passed its expiry is reported as revoked, because that is the fact an operator
acted on.
*/
func (credential Credential) Status(at time.Time) Status {
	switch {
	case credential.RevokedAt != nil:
		return StatusRevoked
	case credential.ExpiresAt != nil && !credential.ExpiresAt.After(at):
		return StatusExpired
	default:
		return StatusActive
	}
}

// Allows reports whether the credential carries a scope.
func (credential Credential) Allows(scope Scope) bool {
	return slices.Contains(credential.Scopes, scope)
}

/*
Principal is who a verified operator key says it is.

Unlike the tenant equivalent it names no application, because an operator acts
across all of them. The tenant it acts on comes from the request path, and the
scope it needed to get there was already checked.
*/
type Principal struct {
	CredentialID string
	Scopes       []Scope
}

// Allows reports whether the principal carries a scope.
func (principal Principal) Allows(scope Scope) bool {
	return slices.Contains(principal.Scopes, scope)
}

/*
ValidationError reports a value that violates a domain rule.

It names the offending field so that the transport layer can report which part
of the request was rejected without the domain knowing about HTTP.
*/
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

// Secret is the plaintext half of an operator key, held only in memory.
type Secret = secret.Value

// NewID generates an opaque public identifier for an operator credential.
func NewID() string {
	return format.NewID()
}

// ValidID reports whether an identifier has the operator credential shape.
func ValidID(id string) bool {
	return format.ValidID(id)
}

// NewSecret generates the plaintext half of a new operator key.
func NewSecret() Secret {
	return secret.New()
}

// Token renders the string an operator presents to Convia.
func Token(id string, value Secret) string {
	return format.Render(id, value)
}

/*
ParseToken recovers the identifier and secret from a presented operator key.

An application key fails here on its token prefix. That is what keeps the two
surfaces from ever being confused: neither family can be parsed by the other's
verifier, so no lookup can cross between them.
*/
func ParseToken(token string) (id string, value Secret, err error) {
	id, value, ok := format.Parse(token)
	if !ok {
		return "", "", ErrUnauthenticated
	}
	return id, value, nil
}

// Digest reduces a secret to what Convia stores. See [secret.Digest].
func Digest(value Secret) []byte {
	return secret.Digest(value)
}

// Matches reports whether a presented secret produced a stored digest, in
// constant time. See [secret.Matches].
func Matches(stored []byte, presented Secret) bool {
	return secret.Matches(stored, presented)
}

/*
NormalizeName validates the label given to an operator credential.

The name is how a person tells two keys apart when deciding which to revoke, so
it is required: an unnamed key is one nobody dares to withdraw.
*/
func NormalizeName(name string) (string, error) {
	normalized := strings.TrimSpace(name)

	switch {
	case normalized == "":
		return "", ValidationError{Field: "name", Message: "The name must not be empty."}
	case utf8.RuneCountInString(normalized) > maxNameLength:
		return "", ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("The name must not exceed %d characters.", maxNameLength),
		}
	case containsControl(normalized):
		return "", ValidationError{Field: "name", Message: "The name must not contain control characters."}
	}
	return normalized, nil
}

/*
NormalizeScopes validates the permissions requested for an operator credential.

An empty set is refused rather than defaulted, because a credential with no
stated permissions would either do nothing or, if defaulted, quietly acquire
more authority than anyone asked for. Duplicates are collapsed and the result
is ordered, so two equivalent requests store the same value.
*/
func NormalizeScopes(requested []Scope) ([]Scope, error) {
	if len(requested) == 0 {
		return nil, ValidationError{
			Field:   "scopes",
			Message: "At least one scope is required, because a credential never carries implicit access.",
		}
	}

	known := Scopes()
	normalized := make([]Scope, 0, len(requested))

	for _, scope := range requested {
		if !slices.Contains(known, scope) {
			return nil, ValidationError{
				Field:   "scopes",
				Message: fmt.Sprintf("%q is not an operator scope Convia recognizes.", string(scope)),
			}
		}
		if !slices.Contains(normalized, scope) {
			normalized = append(normalized, scope)
		}
	}

	slices.Sort(normalized)
	return normalized, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
