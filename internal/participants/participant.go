/*
Package participants owns who is in a call, and what happened to them.

M10 keeps three ideas apart, and this package implements the third:

  - **Room membership** — who belongs to a place over time. Convia does not
    model it. It holds no credentials for an application's people, so it could
    not enforce a policy about who belongs; the application already knows.
  - **An invitation** — permission to join that has not been used yet. It
    arrives with the join sessions of M13, where the party presenting it is no
    longer the party that granted it, which is what makes it authorization
    rather than a record of the application's own decision.
  - **Participation** — who actually joined a conversation, in what role, and
    how they left. That is this package.

Convia is authoritative for the identifier, the call, the lifecycle, the role,
and the timestamps. Who the person is belongs to the application, which asserts
it the same way it asserts every other identity: by naming one of its own
Convia users.

Nothing here names a media provider. Whether someone may join is a Convia
decision; handing them a media token is a later and separate one.
*/
package participants

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as a participant identifier.
	idPrefix = "part_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	// maxRemovalReasonLength bounds the explanation a caller may record.
	maxRemovalReasonLength = 200
)

// ErrNotFound reports that no participant matches the request within its application.
var ErrNotFound = errors.New("participant not found")

/*
ErrCallNotFound reports that the call a participant was asked for does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrCallNotFound = errors.New("call not found")

// ErrUserNotFound reports that the person named for a join does not exist.
var ErrUserNotFound = errors.New("user not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

An application Convia has stopped serving is reported the same way as one that
never existed, so a suspended tenant learns nothing about another.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrCallEnded reports a conversation that is over.

Nobody joins a call that has finished, and nobody leaves one either: the
participants of an ended call are its history, and history does not change.
*/
var ErrCallEnded = errors.New("the call has ended")

/*
ErrNoMediaPlane reports a Convia that cannot carry a conversation.

It is not a fault. A control plane with no media transport configured is a
supported deployment, and everything else about calls and participants works in
it. What cannot work is handing a client something to connect to, because there
is nothing to connect to, and saying so plainly is better than returning a
credential that would fail on use.
*/
var ErrNoMediaPlane = errors.New("this deployment has no media plane")

/*
ErrUserSuspended reports a person the application has withdrawn.

Suspension exists to stop a person being served. Letting a suspended user into
a conversation would make the suspension decorative.
*/
var ErrUserSuspended = errors.New("the user is not active")

/*
ErrCallFull reports a call that has reached the capacity its room declared.

Capacity is the room's, because the room is where an application decided how
many people belong. The call is where it is counted.
*/
var ErrCallFull = errors.New("the call is full")

/*
ErrRemoved reports someone a moderator put out of a call trying to come back.

Rejoining after a removal is refused, and it is the whole reason `removed` is a
state rather than a reason attached to `left`. If a removed participant could
simply join again, removing them would mean nothing.
*/
var ErrRemoved = errors.New("the participant was removed from this call")

/*
ErrGone reports an operation on a participant who is no longer in the call.

Leaving twice is not an error — it succeeds and changes nothing. This is for
the operations that need someone present: acting as a moderator, and being
issued a credential to connect with. It does not distinguish having left from
having been removed, because the difference is the application's own record to
consult rather than something an error should narrate.
*/
var ErrGone = errors.New("the participant is no longer in the call")

/*
ErrNotAModerator reports a participant acting beyond their role.

Convia does not decide whether the application may remove someone: it already
may, on its own authority. What Convia decides is whether the participant the
application named was entitled to, because Convia is what holds the roster.
*/
var ErrNotAModerator = errors.New("the acting participant is not a moderator")

/*
Role is what a participant may do in a call.

There are two, and the difference between them is one Convia can enforce: a
moderator may remove someone and change a role, and a member may not.

Roles about what a person may do with **media** — publish, subscribe, share a
screen — are deliberately absent. They describe capabilities of a media plane
that does not exist yet, so Convia could not enforce them, and a role that
promises what nothing enforces is worse than no role at all.
*/
type Role string

const (
	// RoleModerator may remove participants and change roles.
	RoleModerator Role = "moderator"
	// RoleMember takes part in the conversation without moderating it.
	RoleMember Role = "member"
)

// Roles returns every role Convia recognizes, for the contract test.
func Roles() []Role {
	return []Role{RoleModerator, RoleMember}
}

/*
Status is the lifecycle state of a participation.

Three states, all reachable. `removed` is not a reason attached to `left`: it
is terminal with a policy of its own, because someone a moderator put out must
not simply rejoin.
*/
type Status string

const (
	// StatusJoined means the person is in the call.
	StatusJoined Status = "joined"
	// StatusLeft means the person left the call themselves.
	StatusLeft Status = "left"
	// StatusRemoved means the person was put out of the call.
	StatusRemoved Status = "removed"
)

// Statuses returns every state Convia recognizes, for the contract test.
func Statuses() []Status {
	return []Status{StatusJoined, StatusLeft, StatusRemoved}
}

/*
Remover is the authority behind a forced departure.

`participant` names a person acting inside the call, and the participant is
identified alongside it. `application` and `operator` are authorities acting
from outside, which is why neither names a participant.
*/
type Remover string

const (
	// RemoverApplication means the application removed someone on its own authority.
	RemoverApplication Remover = "application"
	// RemoverOperator means an operator removed someone.
	RemoverOperator Remover = "operator"
	// RemoverParticipant means a moderator inside the call removed someone.
	RemoverParticipant Remover = "participant"
)

// Removers returns every removing authority Convia recognizes, for the contract test.
func Removers() []Remover {
	return []Remover{RemoverApplication, RemoverOperator, RemoverParticipant}
}

/*
Participant is one person's presence in one call.

Someone joins when the record is created, which is why there is no separate
joined_at: CreatedAt is when they arrived, and two names for one instant would
only invite them to disagree.
*/
type Participant struct {
	ID            string
	ApplicationID string
	CallID        string

	/*
		UserID names the person, and is empty for a guest.

		Exactly one of UserID and InvitationID identifies a participation. A
		guest has no Convia user by definition, so what identifies them is the
		invitation they redeemed — and the application, which sent it, is the
		only party that knows who that is.
	*/
	UserID string

	// InvitationID identifies a guest, and is empty for a known user.
	InvitationID  string
	Role          Role
	Status        Status
	RemovedBy     *Remover
	RemovedByID   string
	RemovalReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LeftAt        *time.Time
}

/*
Guest reports whether this participation belongs to somebody Convia has no user
for.

Convia deliberately learns nothing else about them: no name, no address, no
identity of any kind. A roster already refuses to carry a display name for
known users, and a guest is not the place to start.
*/
func (participant Participant) Guest() bool {
	return participant.UserID == ""
}

// Present reports whether the person is still in the call.
func (participant Participant) Present() bool {
	return participant.Status == StatusJoined
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

// NewID generates an opaque public identifier for a participant.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's participant identifier shape.
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
ParseRole reads a role supplied by a caller.

An absent role is a member. Defaulting to the lesser of the two is the only
safe direction: a mistyped field must never hand someone the authority to
remove other people.
*/
func ParseRole(value string) (Role, error) {
	if strings.TrimSpace(value) == "" {
		return RoleMember, nil
	}

	for _, role := range Roles() {
		if string(role) == value {
			return role, nil
		}
	}

	return "", ValidationError{
		Field:   "role",
		Message: fmt.Sprintf("%q is not a participant role Convia recognizes.", value),
	}
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
		Message: fmt.Sprintf("%q is not a participant status Convia recognizes.", value),
	}
}

/*
NormalizeRemovalReason validates the explanation recorded with a removal.

It is optional and free text, because the reasons someone is put out of a
conversation are not Convia's to enumerate. It is application-supplied, so it
may say something about the person removed, and nothing audits it.
*/
func NormalizeRemovalReason(reason string) (string, error) {
	normalized := strings.TrimSpace(reason)
	if normalized == "" {
		return "", nil
	}

	switch {
	case utf8.RuneCountInString(normalized) > maxRemovalReasonLength:
		return "", ValidationError{
			Field:   "reason",
			Message: fmt.Sprintf("The reason must not exceed %d characters.", maxRemovalReasonLength),
		}
	case containsControl(normalized):
		return "", ValidationError{
			Field:   "reason",
			Message: "The reason must not contain control characters.",
		}
	}
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
