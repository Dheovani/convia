package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"syscall"
)

/*
ErrUnsafeDestination reports an address Convia will not connect to.

It is one error for every reason, and the message a caller receives says which
category was refused without listing what resolved to what. Telling an
application that its hostname resolved to 10.0.0.7 would answer, precisely, the
question an attacker was asking.
*/
var ErrUnsafeDestination = errors.New("the destination is not an address Convia will connect to")

/*
Destinations decides which addresses Convia is willing to reach.

This is the whole of `M15-011`, and it is worth being explicit about what the
attack is. A webhook URL is chosen by a tenant and fetched by Convia's own
process, from inside whatever network Convia runs in. Without this, an
application could register `http://169.254.169.254/latest/meta-data/` and have
Convia read a cloud instance's credentials and post them nowhere — but the
delivery record would hold the response status, and a `200` is already an
oracle. The same trick reaches an unauthenticated database, an internal admin
panel, or another tenant's Convia.

Two things follow from that, and both are implemented here:

  - **Names are not addresses.** Checking the hostname is useless: a name an
    attacker controls can resolve to anything, including a public address at
    registration and a private one an hour later. What is checked is every
    address the name resolves to, at the moment of connecting.
  - **The check belongs at the socket.** It runs in the dialer, so it applies
    to the address actually being connected to, not to one resolved earlier and
    assumed to still be the same. That closes the window between a check and a
    connection, which is otherwise a race an attacker chooses the timing of.

For webhook delivery, a development instance is allowed to reach private
addresses, because a local receiver is how anybody tests this. Production never
is, and it is not a setting: there is no environment variable that turns this
off, because the only reason to want one is the reason not to have one. Links
between installations are the exception, and it runs the other way: see
[Destinations.WithPrivateAddresses].
*/
type Destinations struct {
	// private is whether this instance may reach addresses that are not on the
	// public internet.
	private bool
	// plainHTTP is whether this instance may reach a destination over plain http.
	plainHTTP bool
}

// NewDestinations returns the guard for an instance. Development may reach
// private addresses and plain http; production may do neither.
func NewDestinations(development bool) Destinations {
	return Destinations{private: development, plainHTTP: development}
}

/*
WithPrivateAddresses returns the same guard with private addresses allowed or
refused, leaving the rule about plain http to the environment.

Webhook delivery never calls it. It exists for links between installations,
where following a link is something anybody who registers can cause, so reaching
the private network is a choice an operator makes rather than a side effect of
running in development, which is the default. See docs/peers.md.
*/
func (guard Destinations) WithPrivateAddresses(allow bool) Destinations {
	guard.private = allow
	return guard
}

// AllowsPrivate reports whether this instance may reach private addresses.
func (guard Destinations) AllowsPrivate() bool { return guard.private }

/*
Permits reports whether a destination is one Convia will attempt at all.

It checks the scheme, which is the part that does not depend on what a name
resolves to. Plain http reaches a production destination over a network Convia
does not control, carrying a description of a conversation between real people;
the signature proves who sent it and hides nothing.
*/
func (guard Destinations) Permits(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ValidationError{Field: "url", Message: "The destination is not a valid URL."}
	}

	if parsed.Scheme == "http" && !guard.plainHTTP {
		return ValidationError{
			Field:   "url",
			Message: "The destination must be an https URL.",
		}
	}

	return nil
}

/*
Control is the hook net.Dialer calls with the address it is about to connect to.

Refusing here rather than before the dial is what makes the check apply to the
address actually used. A resolver that returns a public address on the first
lookup and a private one on the second cannot get past this, because there is
no earlier answer being trusted.
*/
func (guard Destinations) Control(_, address string, _ syscall.RawConn) error {
	if guard.private {
		return nil
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, "the address could not be read")
	}

	parsed, err := netip.ParseAddr(host)
	if err != nil {
		/*
			The dialer hands this an address, never a name. Reaching this means
			something upstream changed in a way that would silently disable the
			check, so it refuses rather than assuming the best.
		*/
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, "the address could not be read")
	}

	if reason, unsafe := unsafeAddress(parsed); unsafe {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, reason)
	}

	return nil
}

/*
unsafeAddress reports whether an address is one Convia must not reach, and why.

The list is of what is *refused* rather than what is allowed, which is the
weaker of the two shapes and is chosen deliberately: an allowlist of the public
internet is the entire address space minus these, and writing it that way would
mean a new reserved range silently becoming reachable rather than silently
becoming unreachable. Every entry below is a range that is not the public
internet by definition, so the list only grows when the definition does.
*/
func unsafeAddress(address netip.Addr) (string, bool) {
	// An IPv4 address wearing an IPv6 costume is the classic way past a naive
	// check, so it is unwrapped before anything is asked about it.
	address = address.Unmap()

	switch {
	case !address.IsValid():
		return "the address is not valid", true
	case address.IsLoopback():
		return "loopback addresses are Convia itself", true
	case address.IsPrivate():
		return "private addresses are inside the network Convia runs in", true
	case address.IsLinkLocalUnicast(), address.IsLinkLocalMulticast():
		return "link-local addresses include cloud metadata services", true
	case address.IsUnspecified():
		return "the unspecified address is not a destination", true
	case address.IsMulticast(), address.IsInterfaceLocalMulticast():
		return "multicast is not a destination for a delivery", true
	}

	/*
		Ranges that netip has no predicate for. Each is reserved by an RFC and
		none of them is somewhere a tenant's webhook receiver lives.
	*/
	for _, reserved := range reservedRanges {
		if reserved.prefix.Contains(address) {
			return reserved.reason, true
		}
	}
	return "", false
}

// reservedRange is one block that is not the public internet.
type reservedRange struct {
	prefix netip.Prefix
	reason string
}

var reservedRanges = []reservedRange{
	{netip.MustParsePrefix("100.64.0.0/10"), "carrier-grade NAT is not the public internet"},
	{netip.MustParsePrefix("192.0.0.0/24"), "IETF protocol assignments are reserved"},
	{netip.MustParsePrefix("192.0.2.0/24"), "documentation ranges are not routable"},
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking ranges are not routable"},
	{netip.MustParsePrefix("198.51.100.0/24"), "documentation ranges are not routable"},
	{netip.MustParsePrefix("203.0.113.0/24"), "documentation ranges are not routable"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved for future use"},
	{netip.MustParsePrefix("::/128"), "the unspecified address is not a destination"},
	{netip.MustParsePrefix("64:ff9b::/96"), "NAT64 translates to addresses this cannot see"},
	{netip.MustParsePrefix("100::/64"), "the discard prefix goes nowhere"},
	{netip.MustParsePrefix("2001:db8::/32"), "documentation ranges are not routable"},
	{netip.MustParsePrefix("fc00::/7"), "unique local addresses are inside the network Convia runs in"},
}

/*
Resolves reports whether a destination resolves to anything Convia would
connect to, for the sake of telling an application at registration.

It is **not** the enforcement. Enforcement is [Destinations.Control], which runs
at every connection, because what a name resolves to now says nothing about
what it will resolve to at delivery time. This exists so that registering an
obviously unusable destination is refused while somebody is still looking at the
response, rather than becoming a delivery that quietly fails an hour later.
*/
func (guard Destinations) Resolves(ctx context.Context, raw string) error {
	if guard.private {
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ValidationError{Field: "url", Message: "The destination is not a valid URL."}
	}

	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil {
		return ValidationError{
			Field:   "url",
			Message: "The destination's host could not be resolved.",
		}
	}

	for _, address := range addresses {
		if _, unsafe := unsafeAddress(address); unsafe {
			return ValidationError{
				Field:   "url",
				Message: "The destination resolves to an address Convia will not connect to.",
			}
		}
	}

	return nil
}
