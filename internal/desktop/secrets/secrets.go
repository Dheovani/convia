/*
Package secrets keeps the session where the operating system keeps secrets.

A session presented in a header is a bearer credential: whoever holds a copy is
the person, until it expires or is revoked (docs/adr/0019). So it is never
written beside the executable, never in a configuration file, and never in a
log. It goes where the system already guards secrets for every other
application, and comes back only to the process that put it there.

The first version of Convia's application is for Windows, and the Windows
implementation is the real one. Everywhere else this package refuses rather than
pretending: a session kept in a file nobody guards would be worse than none.
*/
package secrets

import "errors"

/*
ErrNotFound reports that nothing was kept for an installation — nobody has
signed in to it on this machine, or they signed out.
*/
var ErrNotFound = errors.New("no session is kept for this installation")

/*
ErrUnsupported reports a system this build cannot keep a secret on. It is not a
failure of the person or of the installation, and the application says so
rather than signing them in and losing the session at the next start.
*/
var ErrUnsupported = errors.New("this system has no place to keep a session")

/*
Session is what one installation's session is kept as: the person it belongs
to, so the application can say who it is about to sign in, and the credential
itself.
*/
type Session struct {
	Username string
	Token    string
}

/*
Keeper is where sessions are kept.

It is an interface for two reasons, and neither is speculation: the system's
keychain cannot be reached from the tests that run in CI, and the first version
supports one system out of three.
*/
type Keeper interface {
	// Keep replaces whatever was kept for an installation.
	Keep(address string, session Session) error
	// Read answers what is kept for an installation, or ErrNotFound.
	Read(address string) (Session, error)
	// Forget removes it. Forgetting what is not there is not an error.
	Forget(address string) error
}

/*
target names one installation's session in the system's store.

The address is part of the name so that somebody signed in to two installations
holds two sessions rather than one overwriting the other.
*/
func target(address string) string {
	return "Convia:" + address
}
