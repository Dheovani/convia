package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// forwardedForHeader carries the chain of addresses a request passed through.
const forwardedForHeader = "X-Forwarded-For"

/*
resolver decides which address a request is charged to.

The address matters because failed authentication attempts are budgeted per
caller. Getting it wrong is not cosmetic in either direction: believing a
header nobody vouched for lets a caller spend someone else's budget, and
ignoring a real proxy makes every client behind it share one budget, so a
single misconfigured client can lock out everyone.
*/
type resolver struct {
	// trusted are the networks whose forwarded headers Convia believes. Empty
	// means trust nothing, which is the default.
	trusted []netip.Prefix
}

func newResolver(trusted []netip.Prefix) resolver {
	return resolver{trusted: trusted}
}

/*
clientAddress identifies who to charge for a request.

With no trusted proxies configured, or a request arriving directly from
somewhere untrusted, this is the peer address of the connection and nothing
else is consulted. **A caller that connects to Convia directly can never claim
another address**, however it decorates its headers.

When the peer *is* a trusted proxy, X-Forwarded-For is walked from the right,
skipping entries that are themselves trusted proxies. The first untrusted
address is the client.

Right to left is the only direction that is safe. A proxy appends the address
it received the connection from, so the rightmost entries were written by
infrastructure the operator controls, and anything a client invented arrives
further left. Reading left to right — the obvious reading of "the original
client is first" — would take whatever the client wrote, which is exactly the
spoof this exists to prevent.

The port is dropped so that a caller opening a new connection per attempt is
still recognized as the same one.
*/
func (resolve resolver) clientAddress(request *http.Request) string {
	peer := peerAddress(request)
	if len(resolve.trusted) == 0 || !resolve.trusts(peer) {
		return peer.String()
	}

	forwarded := request.Header.Values(forwardedForHeader)
	for index := len(forwarded) - 1; index >= 0; index-- {
		entries := strings.Split(forwarded[index], ",")

		for position := len(entries) - 1; position >= 0; position-- {
			address, ok := parseForwarded(entries[position])
			if !ok {
				/*
					An unparseable entry ends the walk rather than being
					skipped. Stepping over it would mean stepping past whatever
					wrote it, and a proxy that emits garbage is not one whose
					later entries can be believed.
				*/
				return peer.String()
			}
			if !resolve.trusts(address) {
				return address.String()
			}
		}
	}

	/*
		Every entry was a trusted proxy, or there was no header at all. The
		closest thing to a client is the peer, which is what a proxy chain with
		no external hop actually describes: a request that originated inside
		the trusted network.
	*/
	return peer.String()
}

// trusts reports whether an address belongs to a configured proxy network.
func (resolve resolver) trusts(address netip.Addr) bool {
	unmapped := address.Unmap()
	for _, prefix := range resolve.trusted {
		if prefix.Contains(unmapped) {
			return true
		}
	}
	return false
}

/*
peerAddress is the address the connection actually came from.

It is the one value in this file that cannot be influenced by the caller, so
every decision above is anchored to it.
*/
func peerAddress(request *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}

	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return address.Unmap()
}

/*
parseForwarded reads one entry of an X-Forwarded-For header.

An entry is normally a bare address, but a port is tolerated because some
proxies append one. A zone identifier is not: it is meaningful only on the host
that owns the interface, so carrying one across a network boundary is a sign
the value cannot be trusted to mean what it says.
*/
func parseForwarded(entry string) (netip.Addr, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return netip.Addr{}, false
	}

	if address, err := netip.ParseAddr(entry); err == nil {
		return address.Unmap(), address.Zone() == ""
	}

	host, _, err := net.SplitHostPort(entry)
	if err != nil {
		return netip.Addr{}, false
	}

	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}
