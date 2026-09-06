/*
Package credentials owns how an application proves who it is to Convia.

A credential is an opaque bearer key issued to one application. Convia stores
only a digest of the secret, so a copy of the database does not yield working
keys, and the key itself carries a public identifier so that verification can
find the right row before comparing any secret material.

Convia is the only party that verifies these keys, so they carry no claims and
are not signed. That keeps revocation immediate: a revoked key stops working on
the next request rather than at the end of some validity window.
*/
package credentials

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as a credential identifier.
	idPrefix = "cred_"

	/*
		tokenPrefix marks a string as Convia secret material.

		It is deliberately distinctive so that secret scanners, including the
		one GitHub runs over public repositories, can recognize a leaked Convia
		key by its shape alone.
	*/
	tokenPrefix = "cvk"

	// randomLength is the number of characters crypto/rand.Text emits, which
	// carries roughly 130 bits of entropy.
	randomLength = 26

	maxNameLength = 120

	// digestLength is the stored SHA-256 digest size, in bytes.
	digestLength = 32
)

// ErrNotFound reports that no credential matches the request within its application.
var ErrNotFound = errors.New("credential not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrUnauthenticated reports that a presented key does not authenticate anyone.

Every reason a key can fail — malformed, unknown, wrong secret, revoked,
expired, or belonging to an application that is no longer served — produces
this single error. A caller learns that the key does not work and nothing more,
so the API cannot be used to discover which identifiers exist or which of them
were once valid.
*/
var ErrUnauthenticated = errors.New("credential does not authenticate")

/*
Scope is one permission a credential may carry.

Scopes name Convia domain operations rather than HTTP routes, so that a
permission keeps its meaning when a route is added, renamed, or split.
*/
type Scope string

const (
	// ScopeUsersRead permits reading the application's users.
	ScopeUsersRead Scope = "users:read"
	// ScopeUsersWrite permits resolving, updating, and changing the lifecycle
	// of the application's users.
	ScopeUsersWrite Scope = "users:write"
	// ScopeCredentialsRead permits reading the application's own credentials,
	// never their secrets.
	ScopeCredentialsRead Scope = "credentials:read"
	// ScopeCredentialsWrite permits issuing and revoking the application's own
	// credentials.
	ScopeCredentialsWrite Scope = "credentials:write"
)

/*
Scopes returns every scope Convia recognizes.

The contract test uses it to prove that the API specification and the
implementation describe the same permissions.
*/
func Scopes() []Scope {
	return []Scope{ScopeUsersRead, ScopeUsersWrite, ScopeCredentialsRead, ScopeCredentialsWrite}
}

// Status is the lifecycle state of a credential, derived rather than stored.
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
Credential is an issued key, without its secret.

The secret exists in plaintext exactly once, in the response to the request
that created it. Everything Convia keeps afterwards is in this struct plus the
digest, which is why no operation can ever show a secret again.
*/
type Credential struct {
	ID            string
	ApplicationID string
	Name          string
	Scopes        []Scope
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
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

// NewID generates an opaque public identifier for a credential.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's credential identifier shape.
func ValidID(id string) bool {
	random, found := strings.CutPrefix(id, idPrefix)
	return found && validRandom(random)
}

// validRandom reports whether a string is the base32 alphabet crypto/rand.Text emits.
func validRandom(value string) bool {
	if len(value) != randomLength {
		return false
	}

	for _, character := range value {
		isBase32 := (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
		if !isBase32 {
			return false
		}
	}
	return true
}

/*
Secret is the plaintext half of a key, which Convia holds only in memory.

It is a distinct type so that a secret cannot be passed where an identifier is
expected, and so that every place one is handled is easy to find.
*/
type Secret string

/*
Token renders the string an application presents to Convia.

The identifier travels with the secret so that verification can look up one row
by primary key and then compare, rather than testing the secret against every
credential of every application.
*/
func Token(id string, secret Secret) string {
	return tokenPrefix + "_" + strings.TrimPrefix(id, idPrefix) + "_" + string(secret)
}

/*
ParseToken recovers the credential identifier and secret from a presented key.

A token that does not have the expected shape is rejected here, before any
database work, so that malformed input costs nothing and cannot be used to
probe for identifiers.
*/
func ParseToken(token string) (id string, secret Secret, err error) {
	prefix, rest, found := strings.Cut(token, "_")
	if !found || prefix != tokenPrefix {
		return "", "", ErrUnauthenticated
	}

	random, secretPart, found := strings.Cut(rest, "_")
	if !found || !validRandom(random) || !validRandom(secretPart) {
		return "", "", ErrUnauthenticated
	}
	return idPrefix + random, Secret(secretPart), nil
}

// NewSecret generates the plaintext half of a new key.
func NewSecret() Secret {
	return Secret(rand.Text())
}

/*
Digest reduces a secret to what Convia stores.

The secret is random with roughly 130 bits of entropy rather than chosen by a
person, so it cannot be guessed or found in a dictionary and a deliberately
slow key derivation would add latency to every authenticated request without
making the search any more feasible. A single SHA-256 is the right cost here.
*/
func Digest(secret Secret) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

/*
Matches reports whether a presented secret produced a stored digest.

The comparison takes the same time whichever bytes differ, so that timing
cannot be used to recover a digest one byte at a time.
*/
func Matches(stored []byte, presented Secret) bool {
	return subtle.ConstantTimeCompare(stored, Digest(presented)) == 1
}

/*
NormalizeName validates the label an operator gives a credential.

The name exists so that a person can tell two keys apart when deciding which to
revoke, so it is required: an unnamed key is one nobody dares to withdraw.
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
NormalizeScopes validates the permissions requested for a credential.

An empty set is refused rather than defaulted, because a credential with no
stated permissions would either do nothing or, if defaulted, quietly acquire
more access than anyone asked for. Duplicates are collapsed and the result is
ordered, so that two equivalent requests store the same value.
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
				Message: fmt.Sprintf("%q is not a scope Convia recognizes.", string(scope)),
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
