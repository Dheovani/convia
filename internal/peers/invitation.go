package peers

import (
	"crypto/rand"
	"strings"
	"time"

	"convia/internal/accounts"
	"convia/internal/secret"
)

const (
	invitationPrefix = "rin_"
	remoteRoomPrefix = "rrm_"

	/*
		InvitationLifetime is how long an invitation into a room lasts.

		A day, and once: long enough for a link sent in the evening to be opened
		the next morning, short enough that a forgotten one stops working before
		it is forgotten. Nothing is lost by its expiring, because accepting needs
		the invitee's key anyway and another invitation costs nothing.
	*/
	InvitationLifetime = 24 * time.Hour
)

/*
Invitation is permission for one person, on any installation, to join one room.

The person is named by both halves of their handle. The identifier is what the
signature on the acceptance is checked against; the username is what the person
inviting typed and recognized, and an acceptance that names another one is
refused, so a handle with a mistake in its name is not quietly accepted by
whoever holds the key.
*/
type Invitation struct {
	ID               string
	ApplicationID    string
	RoomID           string
	InviterUserID    string
	InviteeAccountID string
	InviteeUsername  string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	AcceptedAt       *time.Time
	AcceptedUserID   string
	RevokedAt        *time.Time
}

// Pending reports whether an invitation can still be accepted at a moment.
func (invitation Invitation) Pending(at time.Time) bool {
	return invitation.AcceptedAt == nil && invitation.RevokedAt == nil && invitation.ExpiresAt.After(at)
}

// InviteeHandle is the handle the invitation was addressed to.
func (invitation Invitation) InviteeHandle() string {
	return accounts.Handle(invitation.InviteeUsername, invitation.InviteeAccountID)
}

// NewInvitationID returns a fresh invitation identifier.
func NewInvitationID() string { return invitationPrefix + rand.Text() }

// ValidInvitationID reports whether an identifier has an invitation's shape.
func ValidInvitationID(id string) bool {
	random, found := strings.CutPrefix(id, invitationPrefix)
	return found && secret.ValidRandom(random)
}

/*
RemoteRoom is a room somebody here belongs to, which lives on another
installation.

It is a pointer and a label. Nothing that was said in the room is kept here: the
conversation is read from its home each time, so there is no copy to fall out of
step with the original.
*/
type RemoteRoom struct {
	ID        string
	AccountID string
	Home      string
	RoomID    string
	// UserID is who this person is at the home.
	UserID    string
	Name      string
	CreatedAt time.Time
}

// NewRemoteRoomID returns a fresh remote room identifier.
func NewRemoteRoomID() string { return remoteRoomPrefix + rand.Text() }

// ValidRemoteRoomID reports whether an identifier has a remote room's shape.
func ValidRemoteRoomID(id string) bool {
	random, found := strings.CutPrefix(id, remoteRoomPrefix)
	return found && secret.ValidRandom(random)
}
