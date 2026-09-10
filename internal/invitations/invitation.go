/*
Package invitations owns permission to join a call that has not been used yet.

It is the last of the three ideas M10 kept apart, and it waited for a reason
worth restating: **an invitation is only authorization when the party
presenting it is not the party that granted it.** Before M13 the application
created one with its key and joined someone with the same key, so Convia would
have been keeping a ledger of a decision the application made and could have
made without telling Convia.

M13 changed the shape. A client now presents something Convia issued, so an
invitation becomes a rule Convia enforces against a party that cannot choose to
ignore it, and expiry and revocation start to mean something.

An invitation is a **credential**, in the same sense an application key is: a
random secret Convia stores only as a digest and cannot show again. It differs
in what it authorizes — one person, one call, one role — and in how briefly it
lasts.

Redeeming one produces a participation and the credential to connect with, in a
single request, because the client holding an invitation has no other way to
reach either.
*/
package invitations

import (
	"errors"
	"fmt"
	"time"

	"convia/internal/secret"
)

const (
	// idPrefix marks a public identifier as an invitation identifier.
	idPrefix = "inv_"

	/*
		DefaultLifetime is how long an invitation lasts when no expiry is asked
		for.

		A day is long enough for somebody to open a link at a reasonable hour
		and short enough that a forwarded message stops working before it has
		been forgotten about.
	*/
	DefaultLifetime = 24 * time.Hour

	/*
		MaxLifetime bounds what a caller may ask for.

		The limit exists because an invitation is a way into a conversation and
		a caller can always issue another. A month is generous for a scheduled
		meeting and far short of permanent.
	*/
	MaxLifetime = 30 * 24 * time.Hour
)

/*
format is the invitation family of Convia keys.

A third prefix alongside cvk_ and cvo_ is what lets a presented key be routed
to the right verifier without a lookup, and what lets a person or a secret
scanner tell one kind from another by sight. See internal/secret.
*/
var format = secret.Format{Token: "cvi", ID: idPrefix}

// ErrNotFound reports that no invitation matches the request within its application.
var ErrNotFound = errors.New("invitation not found")

/*
ErrCallNotFound reports that the call an invitation was asked for does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrCallNotFound = errors.New("call not found")

// ErrUserNotFound reports that the person an invitation names does not exist.
var ErrUserNotFound = errors.New("user not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

An application Convia has stopped serving is reported the same way as one that
never existed, so a suspended tenant learns nothing about another.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrUnusable reports an invitation that cannot be redeemed.

Expired, revoked, and declined are one error rather than three. The holder of
an invitation is not the application that issued it, and telling them which of
those happened would let anyone with a dead link learn whether it was
withdrawn, whether it merely aged out, or whether somebody else already said
no on their behalf. The application can see all of that on its own invitation.
*/
var ErrUnusable = errors.New("the invitation can no longer be used")

/*
ErrCallEnded reports a conversation that is over.

It is distinct from ErrUnusable because the invitation is fine: what ended is
the call. A holder learning that is learning about the conversation they were
invited to, which they were told about anyway.
*/
var ErrCallEnded = errors.New("the call has ended")

/*
Status is the public state of an invitation.

It is **derived rather than stored**, which is the point. An expiry that had to
be written into a row would need something to write it, and a sweeper that fell
behind would leave invitations reading as usable after they had stopped being
usable. Here the row records what happened — when it was redeemed, declined, or
revoked, and when it runs out — and the state is read from those facts.
*/
type Status string

const (
	// StatusPending means the invitation is waiting to be used.
	StatusPending Status = "pending"
	// StatusRedeemed means it has been used at least once and still works.
	StatusRedeemed Status = "redeemed"
	// StatusDeclined means the invitee said no. It is terminal.
	StatusDeclined Status = "declined"
	// StatusRevoked means the application withdrew it. It is terminal.
	StatusRevoked Status = "revoked"
	// StatusExpired means it ran out of time.
	StatusExpired Status = "expired"
)

// Statuses lists every state an invitation is published in.
func Statuses() []Status {
	return []Status{StatusPending, StatusRedeemed, StatusDeclined, StatusRevoked, StatusExpired}
}

/*
Invitation is permission for one person to join one call.

It names a user rather than describing a person, for the same reason a
participant does: the application already told Convia who its people are, and a
second copy of anyone's name would be a second thing to keep correct.
*/
type Invitation struct {
	ID            string
	ApplicationID string
	CallID        string
	UserID        string
	Role          string
	ExpiresAt     time.Time
	RedeemedAt    *time.Time
	ParticipantID string
	DeclinedAt    *time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

/*
Status reports the state of the invitation at a moment.

The order of the checks is the order of finality. Revocation wins over
everything, including a redemption that already happened, because an
application withdrawing an invitation means the holder must not be able to use
it again. Declining wins over expiry because it says something the clock does
not.
*/
func (invitation Invitation) Status(at time.Time) Status {
	switch {
	case invitation.RevokedAt != nil:
		return StatusRevoked
	case invitation.DeclinedAt != nil:
		return StatusDeclined
	case !at.Before(invitation.ExpiresAt):
		return StatusExpired
	case invitation.RedeemedAt != nil:
		return StatusRedeemed
	default:
		return StatusPending
	}
}

/*
Usable reports whether the invitation may still be redeemed.

**A redeemed invitation is still usable**, which is deliberate and is the same
rule that makes joining idempotent by the person. Somebody whose laptop died
after joining clicks the same link again; refusing them would make a single
dropped connection unrecoverable, and they would arrive at the same
participation anyway. What stops an invitation is time, a revocation, or the
holder declining it.
*/
func (invitation Invitation) Usable(at time.Time) bool {
	switch invitation.Status(at) {
	case StatusPending, StatusRedeemed:
		return true
	default:
		return false
	}
}

// NewID generates an opaque public identifier for an invitation.
func NewID() string {
	return format.NewID()
}

// ValidID reports whether a string has the shape of an invitation identifier.
func ValidID(id string) bool {
	return format.ValidID(id)
}

/*
ParseToken recovers the identifier and secret from a presented invitation.

A key belonging to another family is rejected on its shape, before any lookup,
so an application key offered here fails without touching the database.
*/
func ParseToken(token string) (id string, value secret.Value, ok bool) {
	return format.Parse(token)
}

// Render produces the string a holder presents to Convia.
func Render(id string, value secret.Value) string {
	return format.Render(id, value)
}

/*
ValidationError reports a request Convia will not act on.

It carries the field so that a caller is told what to change rather than that
something was wrong.
*/
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

/*
NormalizeLifetime settles how long a new invitation lasts.

An absent lifetime takes the default rather than being refused, because the
common case is an application that has no opinion. A lifetime beyond the
maximum is refused rather than quietly clamped: an application that asked for a
year and silently received a month would believe something untrue about its
own links.
*/
func NormalizeLifetime(requested time.Duration) (time.Duration, error) {
	switch {
	case requested == 0:
		return DefaultLifetime, nil
	case requested < 0:
		return 0, ValidationError{Field: "expires_in", Message: "The lifetime must be positive."}
	case requested > MaxLifetime:
		return 0, ValidationError{
			Field:   "expires_in",
			Message: fmt.Sprintf("The lifetime must not exceed %d hours.", int(MaxLifetime.Hours())),
		}
	default:
		return requested, nil
	}
}
