/*
Package accounts owns the people who can sign in to Convia's own product.

Convia is two things at once: a platform other products integrate with, and a
standalone product of its own. Everything built until now served the first. An
account is the first thing that serves the second — a person, with a password
they chose, who opens a browser.

# An account is not a user

`internal/users` models one application's view of one of its people, and holds
no credentials for them, deliberately: an application already knows who its
people are and Convia has no business storing their secrets. That decision is
what keeps every tenant's directory free of anything worth stealing.

An account is the exception, and it is confined to one tenant — the first-party
application, which is Convia's own product. Each account names the user row that
represents it there, so rooms, calls, participants, and presence keep working
through the domains that already exist rather than growing a second path for
people who signed in rather than being asserted.

# Accounts are created by an operator

There is no self-service sign-up, and its absence is a decision rather than an
omission. Open registration needs email verification, email verification needs
a mailer, and Convia has no mailer — `AGENTS.md` says not to add infrastructure
before a feature requires it. Signing in works without one; registering safely
does not.

The consequence is stated plainly in docs/sessions.md: somebody with operator
authority creates the account and hands over a password Convia generated once.
*/
package accounts

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as an account identifier.
	idPrefix = "acc_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	maxEmailLength       = 320
	maxDisplayNameLength = 120
)

// ErrNotFound reports that no account matches the request.
var ErrNotFound = errors.New("account not found")

/*
ErrEmailTaken reports an address that already identifies an account.

It stays taken whatever the account's lifecycle state, including deleted, so
that erasing somebody does not hand their address to the next person who asks
for it — and so that a message sent to an old address cannot reach a new owner.
*/
var ErrEmailTaken = errors.New("the email already identifies an account")

/*
ErrNotActive reports an account Convia is not serving.

Suspension exists to stop somebody signing in. It is reported to a caller with
operator authority, and never to the person trying to sign in — there the
answer is the same indistinguishable refusal every other credential family
gives.
*/
var ErrNotActive = errors.New("the account is not active")

/*
Status is the lifecycle state of an account.

It mirrors the vocabulary `users` and `applications` already use, so an operator
reasons about one set of states across everything Convia holds.
*/
type Status string

const (
	// StatusActive means Convia serves the account normally.
	StatusActive Status = "active"
	// StatusSuspended means Convia refuses to sign the person in.
	StatusSuspended Status = "suspended"
	// StatusDeleted means the account is gone from the API and awaits erasure.
	StatusDeleted Status = "deleted"
)

// Statuses returns every state Convia recognizes, for the contract test.
func Statuses() []Status {
	return []Status{StatusActive, StatusSuspended, StatusDeleted}
}

// Known reports whether a status is one Convia recognizes.
func (status Status) Known() bool { return slices.Contains(Statuses(), status) }

/*
Account is a person who can sign in to Convia's own product.

The password digest is not here. It never leaves the store except to be
compared, and keeping it off the struct that handlers and services pass around
is what makes it impossible to render one into a response by forgetting a field.
*/
type Account struct {
	ID    string
	Email string

	/*
		DisplayName is what an interface shows. It is the person's own, unlike
		a user's display name, which the application asserts on their behalf.
	*/
	DisplayName string

	/*
		UserID is the row in the first-party application that represents this
		person, so everything else in Convia can address them the way it
		addresses anybody.
	*/
	UserID string

	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Active reports whether Convia will sign this person in.
func (account Account) Active() bool { return account.Status == StatusActive }

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

// NewID generates an opaque public identifier for an account.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's account identifier shape.
func ValidID(id string) bool {
	random, found := strings.CutPrefix(id, idPrefix)
	if !found || len(random) != idRandomLength {
		return false
	}

	for _, character := range random {
		isBase32 := (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
		if !isBase32 {
			return false
		}
	}
	return true
}

/*
NormalizeEmail checks an address and reduces it to the one form Convia stores.

Lowercased, because a person who signed up as Ana@example.com and types
ana@example.com later is the same person, and treating them as two accounts
would be a support ticket rather than a security property.

Nothing more clever than that. The local part of an address is, by the standard,
case-sensitive and the provider's business — stripping dots or plus-tags because
one popular provider ignores them would silently merge addresses that somebody
else considers distinct.
*/
func NormalizeEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(email)

	switch {
	case trimmed == "":
		return "", ValidationError{Field: "email", Message: "The email must be stated."}
	case utf8.RuneCountInString(trimmed) > maxEmailLength:
		return "", ValidationError{
			Field:   "email",
			Message: fmt.Sprintf("The email must not exceed %d characters.", maxEmailLength),
		}
	}

	address, err := mail.ParseAddress(trimmed)
	if err != nil || address.Name != "" || !strings.Contains(address.Address, "@") {
		return "", ValidationError{
			Field:   "email",
			Message: "The email must be a single address, without a display name.",
		}
	}

	return strings.ToLower(address.Address), nil
}

/*
NormalizeDisplayName checks the name an interface will show.

It is required, unlike a user's, because there is no application standing behind
an account to supply one later: this person is going to appear in somebody's
roster, and an empty name there is a blank space nobody can act on.
*/
func NormalizeDisplayName(name string) (string, error) {
	normalized := strings.TrimSpace(name)

	switch {
	case normalized == "":
		return "", ValidationError{Field: "display_name", Message: "The display name must be stated."}
	case utf8.RuneCountInString(normalized) > maxDisplayNameLength:
		return "", ValidationError{
			Field:   "display_name",
			Message: fmt.Sprintf("The display name must not exceed %d characters.", maxDisplayNameLength),
		}
	case containsControl(normalized):
		return "", ValidationError{
			Field:   "display_name",
			Message: "The display name must not contain control characters.",
		}
	}
	return normalized, nil
}

/*
ParseStatus reads a lifecycle state supplied as a listing filter.

Only states Convia recognizes are accepted, so a misspelled filter is reported
rather than silently returning everything.
*/
func ParseStatus(value string) (Status, error) {
	for _, status := range Statuses() {
		if string(status) == value {
			return status, nil
		}
	}

	return "", ValidationError{
		Field:   "status",
		Message: fmt.Sprintf("%q is not an account status Convia recognizes.", value),
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
