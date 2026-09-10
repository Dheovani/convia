/*
Package webhooks delivers Convia's events to destinations an application chose,
and keeps a record of every attempt.

It is the durable half of what M14 began, and the two halves are deliberately
different things:

  - **An event stream** ([convia/internal/events]) delivers to whoever is
    connected right now and holds nothing. It is for a picture that must stay
    current and that a client can rebuild by re-reading.
  - **A webhook** is for something a consumer must not miss. Every delivery is
    a row that exists before the first attempt and outlives the last, so it can
    be retried, counted, and explained afterwards.

A consumer that needs neither of those should poll. That is not a fallback: for
anything that changes slowly, a read is simpler than a destination Convia has
to reach, sign for, and give up on.

Nothing here decides what an event *is*. The envelope, the vocabulary, and the
rule about what Convia announces all live in [convia/internal/events], and a
webhook body is that same envelope. A consumer reading one and a client reading
the stream are looking at the same object, which is the point of `M15-003`.
*/
package webhooks

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"convia/internal/events"
	"convia/internal/secret"
)

const (
	// endpointPrefix marks a public identifier as a webhook endpoint.
	endpointPrefix = "whk_"

	// deliveryPrefix marks a public identifier as one attempt to tell somebody
	// something.
	deliveryPrefix = "whd_"

	// maxNameLength bounds the label an application gives an endpoint.
	maxNameLength = 120

	// maxURLLength bounds a destination. It is generous for a real endpoint and
	// far below anything that would make a column awkward.
	maxURLLength = 2000
)

var (
	// ErrNotFound reports that no endpoint matches the request within its application.
	ErrNotFound = errors.New("webhook endpoint not found")

	// ErrDeliveryNotFound reports that no delivery matches the request within
	// its application.
	ErrDeliveryNotFound = errors.New("webhook delivery not found")

	// ErrApplicationNotFound reports work asked for a tenant Convia does not serve.
	ErrApplicationNotFound = errors.New("application not found")
)

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

/*
Secret is the key Convia signs a destination's deliveries with.

It is a distinct type for the same reason [convia/internal/media.APISecret] is:
every way Go has of rendering a value incidentally is overridden, so reaching
the real bytes takes the deliberate act of calling Reveal. A secret does not
leak because somebody printed it on purpose — it leaks because a struct was
logged while somebody was debugging.

Unlike Convia's three families of bearer key, this one is stored rather than
digested, and the migration explains why: nobody presents it back, Convia signs
with it, and a digest cannot produce a signature.
*/
type Secret string

// String hides the secret from fmt and from anything that stringifies a value.
func (Secret) String() string { return redacted }

// GoString hides the secret from the %#v verb, which does not consult String.
func (Secret) GoString() string { return redacted }

// LogValue hides the secret from slog, which resolves this before formatting.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

/*
Reveal returns the secret itself.

Every call is a place where a credential escapes its wrapper, so there should be
very few, and each should be signing something rather than storing or reporting
it.
*/
func (value Secret) Reveal() string { return string(value) }

// redacted is what a signing secret renders as everywhere except where it signs.
const redacted = "[redacted]"

/*
The compile-time assertions below are the point of the type.

If a future change drops one of these methods, the secret silently starts
rendering itself and nothing else fails. Naming the interfaces here turns that
into a build error.
*/
var (
	_ fmt.Stringer   = Secret("")
	_ fmt.GoStringer = Secret("")
	_ slog.LogValuer = Secret("")
)

// NewSecret generates a signing key for a destination.
func NewSecret() Secret {
	return Secret("whsec_" + string(secret.New()))
}

/*
Status is what Convia will do with an endpoint.

It is stored rather than derived, unlike a call's or an invitation's, because it
changes for a reason Convia discovered rather than for a moment that passed: an
endpoint is disabled because it stopped working, and that is a fact about the
past that nothing recomputes.
*/
type Status string

const (
	// StatusEnabled means Convia delivers to this endpoint.
	StatusEnabled Status = "enabled"
	// StatusDisabled means Convia has stopped, and says why.
	StatusDisabled Status = "disabled"
)

/*
Endpoint is one place an application asked to be told things, and which things.

The secret is deliberately absent from every read. It is set when the endpoint
is created and when it is rotated, returned exactly once in that response, and
never carried on a value a handler could accidentally represent.
*/
type Endpoint struct {
	ID            string
	ApplicationID string
	Name          string
	URL           string
	EventTypes    []events.Type
	Status        Status

	ConsecutiveFailures int
	DisabledReason      string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Enabled reports whether Convia will deliver to this endpoint.
func (endpoint Endpoint) Enabled() bool { return endpoint.Status == StatusEnabled }

// Subscribes reports whether an endpoint asked about a type of event.
func (endpoint Endpoint) Subscribes(kind events.Type) bool {
	return slices.Contains(endpoint.EventTypes, kind)
}

/*
DeliveryStatus is where one delivery got to.

Pending is the only state with work left in it, which is why the schema ties
being due to being pending: a delivery that is neither finished nor scheduled
would be one nothing ever picks up.
*/
type DeliveryStatus string

const (
	// DeliveryPending means Convia has not finished with it.
	DeliveryPending DeliveryStatus = "pending"
	// DeliveryDelivered means a destination accepted it.
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryFailed means Convia gave up.
	DeliveryFailed DeliveryStatus = "failed"
)

/*
Delivery is one event owed to one endpoint.

The payload is the exact bytes that were signed. It is kept rather than
regenerated because a signature is over bytes, and an envelope rebuilt later —
with a field added by a newer Convia, or with map keys in another order — would
no longer match the signature a consumer was given.
*/
type Delivery struct {
	ID            string
	EndpointID    string
	ApplicationID string

	EventID   string
	EventType events.Type
	Payload   []byte

	Status        DeliveryStatus
	Attempts      int
	NextAttemptAt *time.Time

	LastStatusCode int
	LastError      string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeliveredAt *time.Time
}

// Done reports whether Convia has finished with a delivery, either way.
func (delivery Delivery) Done() bool { return delivery.Status != DeliveryPending }

// NewID returns a fresh public endpoint identifier.
func NewID() string { return endpointPrefix + rand.Text() }

// ValidID reports whether an identifier has an endpoint's shape.
func ValidID(id string) bool {
	random, found := strings.CutPrefix(id, endpointPrefix)
	return found && secret.ValidRandom(random)
}

// NewDeliveryID returns a fresh public delivery identifier.
func NewDeliveryID() string { return deliveryPrefix + rand.Text() }

// ValidDeliveryID reports whether an identifier has a delivery's shape.
func ValidDeliveryID(id string) bool {
	random, found := strings.CutPrefix(id, deliveryPrefix)
	return found && secret.ValidRandom(random)
}

/*
NormalizeName checks the label an application gave an endpoint.

It is for people reading a list of destinations, so it is required: an unnamed
endpoint in a list of six is one nobody can safely delete.
*/
func NormalizeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)

	switch {
	case trimmed == "":
		return "", ValidationError{Field: "name", Message: "The endpoint must be named."}
	case utf8.RuneCountInString(trimmed) > maxNameLength:
		return "", ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("The name must be at most %d characters.", maxNameLength),
		}
	}
	return trimmed, nil
}

/*
NormalizeEventTypes checks what an endpoint asked to be told about.

At least one, and every one of them a type Convia actually delivers. Accepting
an unknown type would register a subscription that can never fire, and the
application would have no way of telling that from an event that simply has not
happened yet.
*/
func NormalizeEventTypes(requested []events.Type) ([]events.Type, error) {
	if len(requested) == 0 {
		return nil, ValidationError{
			Field:   "event_types",
			Message: "Name at least one event type, because an endpoint that asked about nothing is never sent anything.",
		}
	}

	normalized := make([]events.Type, 0, len(requested))
	for _, kind := range requested {
		if !kind.Known() {
			return nil, ValidationError{
				Field:   "event_types",
				Message: fmt.Sprintf("%q is not an event type Convia delivers.", string(kind)),
			}
		}
		if !slices.Contains(normalized, kind) {
			normalized = append(normalized, kind)
		}
	}

	slices.Sort(normalized)
	return normalized, nil
}

/*
NormalizeURL checks the shape of a destination, and nothing about where it
leads.

Whether an address is one Convia will actually connect to is a separate
question, asked by [Destinations] against the addresses it resolves to, and
asked again before every attempt. Keeping the two apart matters: this one is
about a malformed request, and that one is about a request that is trying to
reach somewhere it should not.
*/
func NormalizeURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)

	if trimmed == "" {
		return "", ValidationError{Field: "url", Message: "The destination must be stated."}
	}

	if len(trimmed) > maxURLLength {
		return "", ValidationError{
			Field:   "url",
			Message: fmt.Sprintf("The destination must be at most %d characters.", maxURLLength),
		}
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ValidationError{Field: "url", Message: "The destination is not a valid URL."}
	}

	/*
		Both schemes parse here, and only one of them is acceptable in
		production. That decision needs to know which instance this is, so it
		belongs with the destination guard rather than with the shape — and it
		has to be made there anyway, because an address that was fine when it
		was registered can resolve somewhere else later.
	*/
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", ValidationError{
			Field:   "url",
			Message: "The destination must be an http or https URL.",
		}
	}

	if parsed.Host == "" {
		return "", ValidationError{Field: "url", Message: "The destination names no host."}
	}

	if parsed.User != nil {
		return "", ValidationError{
			Field:   "url",
			Message: "The destination must not carry credentials in its URL.",
		}
	}

	if parsed.Fragment != "" {
		return "", ValidationError{
			Field:   "url",
			Message: "The destination must not carry a fragment, which is never sent.",
		}
	}

	return parsed.String(), nil
}
