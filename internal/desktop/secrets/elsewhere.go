//go:build !windows

package secrets

/*
Elsewhere is every system this build has no keychain for.

It refuses rather than keeping a session somewhere nothing guards it. `M35`
ships for Windows first; macOS and Linux get an implementation when they get a
build, and until then this is the honest answer.
*/
type Elsewhere struct{}

func NewKeeper() Keeper { return Elsewhere{} }

func (Elsewhere) Keep(string, Session) error { return ErrUnsupported }

func (Elsewhere) Read(string) (Session, error) { return Session{}, ErrUnsupported }

func (Elsewhere) Forget(string) error { return ErrUnsupported }
