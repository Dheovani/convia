package media

import (
	"fmt"
	"log/slog"
)

/*
redacted is what a media secret renders as everywhere except where it is used.

It is a visible placeholder rather than an empty string so that a log line
reads as a withheld value rather than as a missing one, which is the difference
between a reader concluding "this is not printed" and "this was never set".
*/
const redacted = "[redacted]"

/*
APISecret is the shared secret Convia signs media provider requests with.

It is a distinct type for one reason: the plain string form is a credential
that must never be written down. Every way Go has of rendering a value
incidentally — printing it, formatting it, logging it — is overridden here to
produce a placeholder, so that reaching the real bytes takes the deliberate act
of calling Reveal.

This matters more than the usual argument for a wrapper type. A secret does not
leak because someone printed it on purpose; it leaks because a configuration
struct was logged at startup, or because an error carried it into a message. A
type that cannot render itself makes both of those harmless.
*/
type APISecret string

// String hides the secret from fmt and from anything that stringifies a value.
func (APISecret) String() string { return redacted }

// GoString hides the secret from the %#v verb, which does not consult String.
func (APISecret) GoString() string { return redacted }

// LogValue hides the secret from slog, which resolves this before formatting.
func (APISecret) LogValue() slog.Value { return slog.StringValue(redacted) }

/*
Reveal returns the secret itself.

Every call is a place where a credential escapes its wrapper, so there should
be very few, and each should be signing something rather than storing or
reporting it.
*/
func (secret APISecret) Reveal() string { return string(secret) }

// Empty reports whether no secret was configured.
func (secret APISecret) Empty() bool { return secret == "" }

/*
The compile-time assertions below are the point of the type.

If a future change drops one of these methods, the secret silently starts
rendering itself and nothing else fails. Naming the interfaces here turns that
into a build error.
*/
var (
	_ fmt.Stringer   = APISecret("")
	_ fmt.GoStringer = APISecret("")
	_ slog.LogValuer = APISecret("")

	_ fmt.Stringer   = Token("")
	_ fmt.GoStringer = Token("")
	_ slog.LogValuer = Token("")
)
