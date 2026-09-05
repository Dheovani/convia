/*
Package users owns the people an application knows about.

A user is one application's view of one of its people. Convia deliberately does
not link users across applications: the same human known to two applications is
two unrelated Convia users. That keeps Convia from becoming a cross-product
identity graph and stops one application from learning where else a person
appears.

Convia is authoritative for the Convia identifier, the owning application, the
lifecycle state, and the timestamps. Everything else — the external subject,
the display name, the metadata — belongs to the application and is stored
without interpretation.
*/
package users

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as a user identifier.
	idPrefix = "usr_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	maxExternalSubjectLength = 255
	maxDisplayNameLength     = 120

	/*
		Metadata is application-owned, so it is bounded in every dimension a
		caller could grow: the number of entries, the length of each key and
		value, and the size of the whole object once stored.
	*/
	maxMetadataEntries   = 16
	maxMetadataKeyLength = 40
	maxMetadataValueSize = 256
	maxMetadataTotalSize = 4096

	// versionLength is how many bytes of the revision digest are published.
	versionLength = 16
)

// ErrNotFound reports that no user matches the request within its application.
var ErrNotFound = errors.New("user not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrPreconditionFailed reports that a conditional update no longer describes the
stored user, because something changed between the read and the write.
*/
var ErrPreconditionFailed = errors.New("user precondition failed")

/*
ErrSubjectDeleted reports that an external subject still belongs to a deleted
user awaiting erasure.

The mapping is unique regardless of status, so the subject stays taken until
erasure frees it. Resolving it again is refused rather than reviving the user,
because a deletion must not be undone by a routine login.
*/
var ErrSubjectDeleted = errors.New("external subject belongs to a deleted user")

/*
Status is the lifecycle state of a user.

It mirrors the application lifecycle so that operators reason about one set of
states across Convia's tenancy model.
*/
type Status string

const (
	// StatusActive means Convia serves the user normally.
	StatusActive Status = "active"
	// StatusSuspended means Convia refuses new work for this user.
	StatusSuspended Status = "suspended"
	// StatusDeleted means the user is gone from the API and awaits erasure.
	StatusDeleted Status = "deleted"
)

/*
User is one application's view of one of its people.

ExternalSubject is the identifier the application already uses for that person.
Convia stores it opaquely and never parses it, so an application may pass its
own primary key. It is scoped to the application, so two applications may use
the same subject without any relationship between the resulting users.
*/
type User struct {
	ID              string
	ApplicationID   string
	ExternalSubject string
	DisplayName     string
	Metadata        map[string]string
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
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

// NewID generates an opaque public identifier for a user.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's user identifier shape.
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
Version returns an opaque token identifying this revision of a user.

A caller that read a user can send its version back with a later update so that
the update applies only while nothing else changed. The token covers every
mutable field, so any change produces a different one.
*/
func (user User) Version() string {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%s\x00%d",
		user.ID, user.ExternalSubject, user.DisplayName, user.Status, user.UpdatedAt.UTC().UnixMicro())

	for _, key := range sortedKeys(user.Metadata) {
		fmt.Fprintf(digest, "\x00%s\x00%s", key, user.Metadata[key])
	}

	return hex.EncodeToString(digest.Sum(nil)[:versionLength])
}

// sortedKeys orders metadata keys so that a version does not depend on map order.
func sortedKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

/*
NormalizeExternalSubject validates the identifier an application supplies.

The value is opaque to Convia, so it is only bounded and checked for characters
that would be unsafe to log or display. Applications are encouraged to pass a
stable internal identifier rather than an email address, because Convia stores
whatever it receives and an email carries personal data it does not need.
*/
func NormalizeExternalSubject(subject string) (string, error) {
	normalized := strings.TrimSpace(subject)

	switch {
	case normalized == "":
		return "", ValidationError{Field: "external_subject", Message: "The external subject must not be empty."}
	case utf8.RuneCountInString(normalized) > maxExternalSubjectLength:
		return "", ValidationError{
			Field:   "external_subject",
			Message: fmt.Sprintf("The external subject must not exceed %d characters.", maxExternalSubjectLength),
		}
	case containsControl(normalized):
		return "", ValidationError{
			Field:   "external_subject",
			Message: "The external subject must not contain control characters.",
		}
	}
	return normalized, nil
}

/*
NormalizeDisplayName validates the display name an application supplies.

The display name is optional: an application that does not want Convia to hold
one may omit it, and Convia will report the user without a name.
*/
func NormalizeDisplayName(name string) (string, error) {
	normalized := strings.TrimSpace(name)

	switch {
	case normalized == "":
		return "", nil
	case utf8.RuneCountInString(normalized) > maxDisplayNameLength:
		return "", ValidationError{
			Field:   "display_name",
			Message: fmt.Sprintf("The display name must not exceed %d characters.", maxDisplayNameLength),
		}
	case containsControl(normalized):
		return "", ValidationError{Field: "display_name", Message: "The display name must not contain control characters."}
	}
	return normalized, nil
}

/*
NormalizeMetadata validates application-provided metadata.

Metadata is a flat map of strings. Nested structures are refused so that the
stored shape stays predictable, and every dimension is bounded so that a caller
cannot use Convia as general-purpose storage. Convia never interprets the
contents: an application that wants an avatar stores its URL here.
*/
func NormalizeMetadata(metadata map[string]string) (map[string]string, error) {
	if len(metadata) == 0 {
		return map[string]string{}, nil
	}
	if len(metadata) > maxMetadataEntries {
		return nil, ValidationError{
			Field:   "metadata",
			Message: fmt.Sprintf("The metadata must not contain more than %d entries.", maxMetadataEntries),
		}
	}

	total := 0
	normalized := make(map[string]string, len(metadata))

	for key, value := range metadata {
		if err := validateMetadataKey(key); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(value) > maxMetadataValueSize {
			return nil, ValidationError{
				Field:   "metadata",
				Message: fmt.Sprintf("The metadata value for %q must not exceed %d characters.", key, maxMetadataValueSize),
			}
		}
		if containsControl(value) {
			return nil, ValidationError{
				Field:   "metadata",
				Message: fmt.Sprintf("The metadata value for %q must not contain control characters.", key),
			}
		}

		total += len(key) + len(value)
		normalized[key] = value
	}

	if total > maxMetadataTotalSize {
		return nil, ValidationError{
			Field:   "metadata",
			Message: fmt.Sprintf("The metadata must not exceed %d bytes in total.", maxMetadataTotalSize),
		}
	}
	return normalized, nil
}

/*
validateMetadataKey restricts keys to a predictable shape.

Keys are lowercase identifiers so that they stay usable as column names, log
fields, and query parameters if metadata is ever indexed or exported.
*/
func validateMetadataKey(key string) error {
	invalid := ValidationError{
		Field: "metadata",
		Message: fmt.Sprintf(
			"Metadata keys must be lowercase letters, digits, and underscores, start with a letter, and be at most %d characters.",
			maxMetadataKeyLength),
	}

	if key == "" || len(key) > maxMetadataKeyLength {
		return invalid
	}
	if key[0] < 'a' || key[0] > 'z' {
		return invalid
	}
	for _, character := range key {
		lowercase := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !lowercase && !digit && character != '_' {
			return invalid
		}
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
