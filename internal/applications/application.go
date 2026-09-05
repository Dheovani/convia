/*
Package applications owns Convia's tenants.

An application is the unit of isolation in Convia: the standalone product
itself, or an external product such as an online learning platform integrating
through the public API. Every tenant-scoped resource introduced later belongs
to exactly one application.

The package holds the domain rules, their PostgreSQL persistence, the service
that enforces them independently of HTTP, and the handlers that expose them.
*/
package applications

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as an application identifier.
	idPrefix = "app_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	maxNameLength = 120
)

// ErrNotFound reports that no application matches the requested identifier.
var ErrNotFound = errors.New("application not found")

/*
Status is the lifecycle state of an application.

The states are deliberately few: Convia either serves a tenant, refuses to
serve it while keeping its data, or treats it as gone. Anything richer belongs
to the consuming product rather than to Convia's tenancy model.
*/
type Status string

const (
	// StatusActive means Convia serves the application normally.
	StatusActive Status = "active"
	// StatusSuspended means Convia refuses new work while retaining the data.
	StatusSuspended Status = "suspended"
	// StatusDeleted means the application is gone from the API and awaits
	// retention-based erasure.
	StatusDeleted Status = "deleted"
)

/*
Application is a Convia tenant.

Name is display metadata owned by whoever administers the application. It
carries no security meaning: authentication and authorization are attached to
credentials in M07, never to the display name, so renaming an application can
never change what it may do.
*/
type Application struct {
	ID        string
	Name      string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
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

/*
NewID generates an opaque public identifier.

Identifiers are random rather than sequential so that no identifier reveals how
many tenants exist or allows guessing another tenant's identifier. They are
immutable: an application keeps its identifier for its whole lifetime, because
external systems store it as a foreign reference.
*/
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's application identifier shape.
func ValidID(id string) bool {
	random, found := strings.CutPrefix(id, idPrefix)
	if !found || len(random) != idRandomLength {
		return false
	}

	for _, character := range random {
		if !isIdentifierCharacter(character) {
			return false
		}
	}
	return true
}

/*
isIdentifierCharacter reports whether a character belongs to the base32 alphabet
crypto/rand.Text uses, which is the alphabet the database constraint enforces.
*/
func isIdentifierCharacter(character rune) bool {
	return (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
}

/*
NormalizeName validates a display name and returns its canonical form.

Surrounding whitespace is insignificant, so it is removed before the length is
checked and before the value is stored. Control characters are rejected because
a display name is rendered in user interfaces and written to logs.
*/
func NormalizeName(name string) (string, error) {
	normalized := strings.TrimSpace(name)

	if normalized == "" {
		return "", ValidationError{Field: "name", Message: "The name must not be empty."}
	}
	if utf8.RuneCountInString(normalized) > maxNameLength {
		return "", ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("The name must not exceed %d characters.", maxNameLength),
		}
	}
	for _, character := range normalized {
		if unicode.IsControl(character) {
			return "", ValidationError{Field: "name", Message: "The name must not contain control characters."}
		}
	}
	return normalized, nil
}
