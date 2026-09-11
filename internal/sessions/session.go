/*
Package sessions owns what a browser holds after somebody signs in.

This is Convia's fourth credential family, and the first that a person rather
than a program presents. It is `cvs_`, it lives **only in a cookie**, and it is
never read from an `Authorization` header — which is what makes an API key
useless here and a session cookie useless on the tenant surface, refused by
shape before any lookup, the same way the other three families already refuse
each other.

# A session is not an application key, and cannot become one

This is the most important sentence in the package.

Every `Authorize` in Convia takes a [credentials.Principal], which carries an
application and its scopes — authority over **the whole tenant**. The tempting
shortcut, when the standalone interface needs to list rooms, is to hand a signed
in person one of those for the first-party application. It would work
immediately and it would be catastrophic: every person who signed in would hold
`credentials:write`, and could mint a permanent API key that outlives their
session, their password, and their account. The entire lifecycle in this package
would become decorative.

So [Principal] is its own type, it carries no scopes, and **there is no function
anywhere in Convia that converts one into a credentials.Principal.** Routes that
act for a person are their own surface, authorized against the person, and they
are added deliberately rather than inherited by accident. A test asserts the
conversion does not exist.

# What a session is worth

An opaque secret Convia generated, stored as a digest, revocable immediately
because Convia is the only party that verifies it. It expires two ways — an
idle window measured from last use, and an absolute deadline that no amount of
activity extends — and both are derived from timestamps rather than a status
column, so expiry needs no scheduled job to take effect.
*/
package sessions

import (
	"errors"
	"time"

	"convia/internal/secret"
)

/*
format is how a session token is rendered and parsed.

The "cvs" prefix is distinctive so that a secret scanner can recognize a leaked
Convia session by its shape alone, and so that a token presented to the wrong
surface is refused before any lookup. It is the same machinery the other three
families use; only the letters differ.
*/
var format = secret.Format{Token: "cvs", ID: "ses_"}

const (
	/*
		IdleLifetime is how long a session survives without being used.

		Measured from last use, and refreshed lazily — see [RefreshInterval] —
		so the effective window is this plus at most one refresh interval. That
		is stated rather than hidden: a session dies somewhere between fourteen
		and fourteen days and an hour after its owner stops.

		What it actually bounds is how long a stolen cookie stays useful after
		the victim stops working, which is worth having and is not the same as
		"logged out when idle". A tab left open on a product that polls will
		keep a session alive indefinitely, and no server-side timer can tell
		that from a person at the keyboard.
	*/
	IdleLifetime = 14 * 24 * time.Hour

	/*
		AbsoluteLifetime is how long a session survives at all.

		Nothing extends it. It is the backstop under every other control here:
		whatever happens to the idle window, a credential minted today stops
		working in ninety days.
	*/
	AbsoluteLifetime = 90 * 24 * time.Hour

	/*
		RefreshInterval is how stale the last-use timestamp may get.

		Writing it on every request would put a database write in front of
		every authenticated read — which is exactly the cost that made
		`last_used_at` on an application credential "not yet implemented" in
		docs/authentication.md. The difference here is the caller: one browser
		rather than a fleet, so one write an hour per signed-in person is a
		rounding error.

		Staleness only ever shortens the idle window, never lengthens it, so
		this trades a little promptness for a lot of writes and gives up no
		safety.
	*/
	RefreshInterval = time.Hour

	/*
		MaxPerAccount bounds how many sessions one person may hold at once.

		Without it, anybody who learns a password can open sessions without
		limit and revoking them becomes the owner's problem, one page at a time.
		Ten covers a phone, a laptop, a tablet, and a few browsers with room to
		spare; the eleventh evicts the least recently used, because a person
		signing in on a new device means it rather than having made a mistake.
	*/
	MaxPerAccount = 10
)

/*
ErrUnauthenticated reports a presented session that does not authenticate
anybody.

Every reason produces it: malformed, unknown, wrong secret, revoked, idle out,
past its absolute deadline, or belonging to an account, a person, or an
application Convia is no longer serving. The caller learns the session does not
work and nothing more.
*/
var ErrUnauthenticated = errors.New("session does not authenticate")

// ErrNotFound reports that no session matches the request.
var ErrNotFound = errors.New("session not found")

/*
Session is one browser holding one account's authority.

The digest is not here, for the reason it is not on an account: it is read by
one statement for one purpose, and keeping it off the struct that travels
through the service and the handler makes rendering one into a response
impossible rather than merely unlikely.
*/
type Session struct {
	ID        string
	AccountID string

	CreatedAt time.Time

	// LastSeenAt is what the idle window is measured from, refreshed at most
	// once per [RefreshInterval].
	LastSeenAt time.Time

	// AbsoluteExpiresAt is when this session ends whatever its owner does.
	AbsoluteExpiresAt time.Time

	RevokedAt *time.Time
}

/*
Live reports whether this session still authenticates at a given moment.

All three conditions are checked here rather than in the query that loads the
row, so that a session is judged by the same rule wherever it came from — and
so that adding a fourth condition later is one edit rather than a search.
*/
func (session Session) Live(at time.Time) bool {
	switch {
	case session.RevokedAt != nil:
		return false
	case !session.AbsoluteExpiresAt.After(at):
		return false
	case !session.LastSeenAt.Add(IdleLifetime).After(at):
		return false
	default:
		return true
	}
}

/*
Stale reports whether the last-use timestamp is far enough behind to be worth
writing.

It is the throttle that keeps authentication from writing on every request.
*/
func (session Session) Stale(at time.Time) bool {
	return at.Sub(session.LastSeenAt) >= RefreshInterval
}

/*
Principal is what a verified session proves: one person, and nothing else.

It carries **no scopes**, deliberately. Scopes describe what an integration may
do to a tenant's data; a person is not an integration, and giving them a scope
vocabulary would be the first step toward the shortcut the package
documentation refuses. What a person may do is decided per operation, against
the person, by the domain that owns the operation.

There is no method on this type that produces a [credentials.Principal], and
there must never be one.
*/
type Principal struct {
	// SessionID identifies the session itself, so that revoking the one in
	// front of you is addressable without naming it in a request.
	SessionID string

	// AccountID is who signed in.
	AccountID string

	/*
		UserID is the same person as the row every other domain in Convia
		addresses, in the first-party application. It is what lets a
		person-facing route ask an existing domain a question without inventing
		a second notion of who somebody is.
	*/
	UserID string

	// ApplicationID is the first-party application, present so that a
	// person-facing route never has to look it up or guess.
	ApplicationID string
}

// NewID returns a fresh public session identifier.
func NewID() string { return format.NewID() }

// ValidID reports whether an identifier has Convia's session identifier shape.
func ValidID(id string) bool { return format.ValidID(id) }

// NewSecret returns the secret half of a session token.
func NewSecret() secret.Value { return secret.New() }

// Token renders the value a browser holds.
func Token(id string, value secret.Value) string { return format.Render(id, value) }

/*
ParseToken splits a presented token into its identifier and secret.

A token that does not have this family's shape is refused here, before any
lookup — which is how an application key presented in a session cookie costs a
string comparison rather than a database read.
*/
func ParseToken(token string) (string, secret.Value, error) {
	id, value, ok := format.Parse(token)
	if !ok {
		return "", "", ErrUnauthenticated
	}
	return id, value, nil
}

// Digest returns the stored form of a session secret.
func Digest(value secret.Value) []byte { return secret.Digest(value) }

// Matches compares a presented secret with a stored digest, in constant time.
func Matches(stored []byte, presented secret.Value) bool {
	return secret.Matches(stored, presented)
}
