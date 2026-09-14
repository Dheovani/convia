/*
Package rooms owns the places an application's people meet.

A room is **durable**. It outlives any single conversation held in it, which is
what separates it from the call that will occupy it in M09: the room is the
place, the call is the occasion. An application creates a room once and returns
to it, or creates one per occasion and deletes it — both are supported, and the
alias is what distinguishes them.

Convia is authoritative for the identifier, the owning application, the
lifecycle state, and the timestamps. The alias, the name, the metadata, and the
capacity belong to the application and are stored without interpretation.

Nothing here names a media provider. A room is a Convia concept that the media
plane will later be asked to realize, never the other way round.
*/
package rooms

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
	// idPrefix marks a public identifier as a room identifier.
	idPrefix = "room_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	maxAliasLength = 120
	maxNameLength  = 120

	/*
		maxParticipants bounds what an application may ask for. It is a domain
		rule rather than a capability claim: Convia records the limit now, and
		the media plane enforces it when calls exist.
	*/
	minParticipants = 1
	maxParticipants = 1000

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

// ErrNotFound reports that no room matches the request within its application.
var ErrNotFound = errors.New("room not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrPreconditionFailed reports that a conditional update no longer describes the
stored room, because something changed between the read and the write.
*/
var ErrPreconditionFailed = errors.New("room precondition failed")

/*
ErrAliasTaken reports that another room of the same application already uses
the requested alias.

It stays taken while that room is deleted and awaiting erasure. Reusing it is
refused rather than rebinding it, because a client that cached an alias must
never find it pointing at a different room.
*/
var ErrAliasTaken = errors.New("room alias is already in use")

/*
ErrDeleted reports an operation on a room that is gone from the API.

A deleted room is refused rather than silently revived: deletion is an
application's decision and must not be undone by a routine update.
*/
var ErrDeleted = errors.New("room is deleted")

/*
Status is the lifecycle state of a room.

Rooms use `open` and `closed` rather than the `active` and `suspended` of
applications and users, because the words describe what an operator is actually
doing: a closed room is not being punished, it is finished.
*/
type Status string

const (
	// StatusOpen means the room accepts new calls.
	StatusOpen Status = "open"
	/*
		StatusClosed means the room refuses new calls but remains readable.

		Closing is reversible and lossless. It is also what "archive" means
		here: a fourth retained-but-invisible state would duplicate
		StatusDeleted without adding a distinction anyone could act on.
	*/
	StatusClosed Status = "closed"
	// StatusDeleted means the room is gone from the API and awaits erasure.
	StatusDeleted Status = "deleted"
)

// Statuses returns every state Convia recognizes, for the contract test.
func Statuses() []Status {
	return []Status{StatusOpen, StatusClosed, StatusDeleted}
}

/*
Room is a place an application's people meet.

Alias is optional, and its presence is the difference between the two ways an
application uses rooms. A room **with** an alias is durable and addressable: the
weekly standup, the support queue, a classroom that exists all term. A room
**without** one is ephemeral: created for an occasion, referred to by its
identifier, and deleted afterwards.
*/
type Room struct {
	ID              string
	ApplicationID   string
	Alias           string
	Name            string
	Metadata        map[string]string
	MaxParticipants *int
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// Personal is whether a person opened the room. Only such a room has an
	// owner; an application's rooms are the application's to manage.
	Personal bool
	// OwnerUserID is who owns a room a person opened, and is empty while
	// nobody who could hold it is in the room. See docs/rooms.md.
	OwnerUserID string
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

// NewID generates an opaque public identifier for a room.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's room identifier shape.
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
Version renders an opaque revision of the room for conditional updates.

Every field a client can change contributes, so a version cannot stay equal
across a change anyone could observe. It is a digest rather than a counter so
that clients cannot order versions or predict the next one, which would invite
them to construct one.
*/
func (room Room) Version() string {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%s\x00%d",
		room.ID, room.Alias, room.Name, room.Status, room.UpdatedAt.UTC().UnixMicro())

	if room.MaxParticipants != nil {
		fmt.Fprintf(digest, "\x00%d", *room.MaxParticipants)
	}

	for _, key := range sortedKeys(room.Metadata) {
		fmt.Fprintf(digest, "\x00%s\x00%s", key, room.Metadata[key])
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
NormalizeAlias validates the stable name an application gives a room.

An alias is how an application addresses a room it will come back to, so it is
constrained more tightly than a display name: it may not carry whitespace or
control characters, because a value that renders identically to another would
make two rooms indistinguishable in a listing while remaining distinct keys.

An empty alias is not an error. It means the room is anonymous, addressed only
by its identifier, which is the ephemeral case.
*/
func NormalizeAlias(alias string) (string, error) {
	normalized := strings.TrimSpace(alias)
	if normalized == "" {
		return "", nil
	}

	switch {
	case utf8.RuneCountInString(normalized) > maxAliasLength:
		return "", ValidationError{
			Field:   "alias",
			Message: fmt.Sprintf("The alias must not exceed %d characters.", maxAliasLength),
		}
	case strings.ContainsFunc(normalized, unicode.IsSpace):
		return "", ValidationError{
			Field:   "alias",
			Message: "The alias must not contain whitespace.",
		}
	case containsControl(normalized):
		return "", ValidationError{
			Field:   "alias",
			Message: "The alias must not contain control characters.",
		}
	}
	return normalized, nil
}

/*
NormalizeName validates the label a person reads.

Unlike the alias it is free text, because it exists to be shown rather than to
be looked up. It is required: an unnamed room is one nobody can identify in a
listing.
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
		return "", ValidationError{
			Field:   "name",
			Message: "The name must not contain control characters.",
		}
	}
	return normalized, nil
}

/*
NormalizeMaxParticipants validates a requested capacity.

Absent means the room states no limit of its own. Zero is refused rather than
treated as absent, because a room nobody may enter is far more likely to be a
serialization mistake than an intention.
*/
func NormalizeMaxParticipants(limit *int) (*int, error) {
	if limit == nil {
		return nil, nil
	}

	if *limit < minParticipants || *limit > maxParticipants {
		return nil, ValidationError{
			Field: "max_participants",
			Message: fmt.Sprintf("The capacity must be between %d and %d, or absent for no room-level limit.",
				minParticipants, maxParticipants),
		}
	}

	value := *limit
	return &value, nil
}

/*
NormalizeMetadata validates the application-owned annotations on a room.

The rules are deliberately identical to the users domain, down to the key
pattern. Metadata means the same thing in both places, and validating one
concept two different ways is exactly the drift that makes an API surprising:
a client that learned the rules for a user should not have to learn them again
for a room.
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

// validateMetadataKey applies the same key rule as the users domain.
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
		Message: fmt.Sprintf("%q is not a room status Convia recognizes.", value),
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
