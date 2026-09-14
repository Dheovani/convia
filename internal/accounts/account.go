/*
Package accounts owns the people who can sign in to Convia's own product.

Convia is two things at once: a platform other products integrate with, and a
standalone product of its own. Everything built until now served the first. An
account is the first thing that serves the second — a person, with a password
they chose, who opens a browser.

# An account belongs to its installation and to the person who made it

Somebody who installs Convia creates their own account from the sign-in page,
with a username and a password, and nothing else: no email, no operator, no
mailer. An installation holds as many accounts as people create on it, the way a
password manager's file holds as many entries as somebody adds.

The password does two jobs. It signs somebody in, verified against an argon2id
digest, and it **seals the account's private key**, which is stored only in a
form the password opens. Nobody who can read the database, including whoever
runs the machine, can use that key without the password. The consequence is the
same one a password manager has, and it is stated rather than softened: there
is no password reset, because nothing can reopen the key without the password.

# The identifier is a key's fingerprint

An account's identifier is not drawn at random. It is the fingerprint of the
account's Ed25519 public key, so it names exactly one key, and whoever claims it
can be asked to prove they hold the matching private key. That matters once
people on different installations invite each other: an installation controls
its own database and could write any identifier and username it liked into it,
so an identifier that proved nothing would be copied rather than guessed. See
[IDFor] and [Handle].

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
*/
package accounts

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	// idPrefix marks a public identifier as an account identifier.
	idPrefix = "acc_"

	// idFingerprintLength is the number of base32 characters after the prefix.
	idFingerprintLength = 26

	minUsernameLength = 3
	maxUsernameLength = 32
)

// ErrNotFound reports that no account matches the request.
var ErrNotFound = errors.New("account not found")

/*
ErrUsernameTaken reports a username that already names an account on this
installation.

It stays taken whatever the account's lifecycle state, including deleted, so
that erasing somebody does not hand their name to the next person who asks for
it — and so that an invitation addressed to an old name cannot reach a new
owner.
*/
var ErrUsernameTaken = errors.New("the username already names an account")

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

The password digest and the sealed private key are not here. They never leave
the store except to be compared or opened, and keeping them off the struct that
handlers and services pass around is what makes it impossible to render one
into a response by forgetting a field.
*/
type Account struct {
	// ID is the fingerprint of PublicKey. See [IDFor].
	ID string

	// Username is what the person chose to be called, unique on this
	// installation and fixed once chosen.
	Username string

	// PublicKey is the half of the account's identity anybody may hold.
	PublicKey ed25519.PublicKey

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

// Handle returns how this person is named to somebody else. See [Handle].
func (account Account) Handle() string { return Handle(account.Username, account.ID) }

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

// ValidID reports whether an identifier has Convia's account identifier shape.
func ValidID(id string) bool {
	fingerprint, found := strings.CutPrefix(id, idPrefix)
	return found && validFingerprint(fingerprint)
}

// validFingerprint reports whether a value is an identifier without its prefix.
func validFingerprint(fingerprint string) bool {
	if len(fingerprint) != idFingerprintLength {
		return false
	}

	for _, character := range fingerprint {
		if !strings.ContainsRune(base32Alphabet, character) {
			return false
		}
	}
	return true
}

/*
NormalizeUsername checks a username and reduces it to the one form Convia stores.

Lowercased, because somebody who registered as Ana and types ana later is the
same person, and a handle read aloud does not carry case.

**ASCII letters, digits, dots, dashes and underscores, and nothing else.** A
wider alphabet is friendlier and would undo the point of a handle: Cyrillic "а"
and Latin "a" render identically, so two people could hold names nobody can tell
apart by looking, and an invitation meant for one would be sent to the other by
somebody who checked carefully. The first character is a letter or a digit, so a
name cannot begin with punctuation that disappears at the edge of a sentence.
*/
func NormalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))

	switch {
	case normalized == "":
		return "", ValidationError{Field: "username", Message: "The username must be stated."}
	case len(normalized) < minUsernameLength || len(normalized) > maxUsernameLength:
		return "", ValidationError{
			Field: "username",
			Message: fmt.Sprintf("The username must be between %d and %d characters.",
				minUsernameLength, maxUsernameLength),
		}
	case !usernameStart(rune(normalized[0])):
		return "", ValidationError{
			Field:   "username",
			Message: "The username must begin with a letter or a digit.",
		}
	}

	for _, character := range normalized {
		if !usernameStart(character) && !strings.ContainsRune("._-", character) {
			return "", ValidationError{
				Field:   "username",
				Message: "The username may contain only letters, digits, dots, dashes and underscores.",
			}
		}
	}
	return normalized, nil
}

// usernameStart reports whether a character may begin a username.
func usernameStart(character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
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
