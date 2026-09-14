/*
Package peers lets a room have members who live on another installation.

A room lives where it was created — its **home** — and nowhere else. Somebody
from another installation takes part through their own: their browser talks only
to the Convia it signed in to, exactly as it always has, and that Convia talks to
the home on their behalf. Nothing about cookies, the origin check, or the page's
policy changes, because the browser never meets the other installation at all.

# Who is asking is proved, not asserted

An installation controls its own database and could claim to act for anybody.
So every request between installations is **signed with the key of the person it
is for**: the account identifier is the fingerprint of that key, and the home
checks the signature against the key the identifier names. An installation that
does not hold somebody's key cannot act as them, whatever it writes into its own
tables.

The key is sealed by the person's password, so their installation can only sign
while they are signed in: signing in opens the key, and the session holds it for
as long as the session lasts. See internal/sessions.

# Joining is by invitation, and the invitation is not a secret

Somebody in a room invites a handle — `username#IDENTIFIER` — and gets a link
naming the home and the invitation. The link carries nothing that grants access:
accepting it needs a signature by the key the handle's identifier is the
fingerprint of, so a link sent to the wrong person, or read by anybody on the
way, lets nobody else in. An invitation lasts a day and is used once.

# What the home serves a visitor

The same things it serves a signed-in person in the same room, through the same
handlers: history, saying something, changing or withdrawing it, read state, who
is here, and leaving. A visitor becomes a user of the home's own product, so
membership, authorship and erasure need no second model.
*/
package peers

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
)

// ErrUnauthenticated reports a request between installations that is not signed
// by anybody the home can verify, for any reason.
var ErrUnauthenticated = errors.New("the request is not signed by anybody Convia can verify")

/*
ErrNotFound reports an invitation that cannot be used by whoever asked.

Unknown, expired, revoked, already accepted, and addressed to somebody else are
one answer. Telling them apart would tell a stranger holding a link that an
invitation exists and who it is for.
*/
var ErrNotFound = errors.New("invitation not found")

// ErrRoomNotFound reports a room the person asking is not in, as though it were
// not there.
var ErrRoomNotFound = errors.New("room not found")

// ErrAlreadyMember reports an invitation for somebody who is already in the room.
var ErrAlreadyMember = errors.New("the person is already in the room")

// ErrRemoteRoomNotFound reports a remote room this person holds no pointer to.
var ErrRemoteRoomNotFound = errors.New("remote room not found")

/*
ErrUnreachable reports a home that did not answer as a Convia.

Not answering, refusing the connection, pointing at an address Convia will not
connect to, redirecting, and answering with something that is not Convia's JSON
are one error: the person is told the other installation could not be reached,
and the reason is logged.
*/
var ErrUnreachable = errors.New("the other installation could not be reached")

// ValidationError reports a value that violates a rule of this package.
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string { return fmt.Sprintf("%s: %s", err.Field, err.Message) }

// Signer is who a verified request between installations was signed by.
type Signer struct {
	AccountID string
	PublicKey ed25519.PublicKey
}

// signerKey is the context key a verified signer travels under. Unexported, so
// only the middleware that verified a signature can put one there.
type signerKey struct{}

// ContextWithSigner returns a context carrying a verified signer.
func ContextWithSigner(ctx context.Context, signer Signer) context.Context {
	return context.WithValue(ctx, signerKey{}, signer)
}

// SignerFromContext returns the signer a request was verified with.
func SignerFromContext(ctx context.Context) (Signer, bool) {
	signer, found := ctx.Value(signerKey{}).(Signer)
	return signer, found
}
