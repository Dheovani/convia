package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// prefixes parses the trusted networks a test configures.
func prefixes(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()

	parsed := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			t.Fatalf("parse %q: %v", entry, err)
		}
		parsed = append(parsed, prefix.Masked())
	}
	return parsed
}

// forwardedRequest builds a request arriving from peer with the given chain.
func forwardedRequest(peer string, forwarded ...string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/users", nil)
	request.RemoteAddr = peer

	for _, value := range forwarded {
		request.Header.Add(forwardedForHeader, value)
	}
	return request
}

/*
TestForwardedHeadersAreIgnoredWithoutConfiguration proves the default is to
trust nothing.

Convia has always used the peer address, and adding trusted-forwarder support
must not change that for anyone who did not ask for it.
*/
func TestForwardedHeadersAreIgnoredWithoutConfiguration(t *testing.T) {
	resolve := newResolver(nil)

	got := resolve.clientAddress(forwardedRequest("203.0.113.9:41000", "9.9.9.9"))
	if got != "203.0.113.9" {
		t.Errorf("clientAddress() = %q, want the peer address", got)
	}
}

/*
TestADirectCallerCannotClaimAnotherAddress is the spoof this feature exists to
prevent.

A caller connecting to Convia from outside the trusted networks may write
anything it likes into X-Forwarded-For. None of it is believed, because the
connection did not arrive from a proxy Convia was told to trust.
*/
func TestADirectCallerCannotClaimAnotherAddress(t *testing.T) {
	resolve := newResolver(prefixes(t, "10.0.0.0/8"))

	claims := []string{
		"9.9.9.9",
		"10.0.0.1",
		"10.0.0.1, 10.0.0.2",
		"not-an-address",
		"",
	}

	for _, claim := range claims {
		t.Run(claim, func(t *testing.T) {
			got := resolve.clientAddress(forwardedRequest("203.0.113.9:41000", claim))
			if got != "203.0.113.9" {
				t.Errorf("clientAddress() = %q, want the peer address regardless of the claim", got)
			}
		})
	}
}

/*
TestTheChainIsWalkedFromTheRight proves the direction that makes the header
safe to read at all.

A proxy appends the address it received the connection from, so anything a
client invented sits further left than anything infrastructure wrote. Reading
left to right — the obvious reading of "the original client comes first" —
would return exactly what the attacker chose.
*/
func TestTheChainIsWalkedFromTheRight(t *testing.T) {
	resolve := newResolver(prefixes(t, "10.0.0.0/8", "192.168.0.0/16"))

	tests := map[string]struct {
		peer      string
		forwarded []string
		want      string
	}{
		"one proxy": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"203.0.113.9"},
			want:      "203.0.113.9",
		},
		"two proxies": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"203.0.113.9, 10.0.0.9"},
			want:      "203.0.113.9",
		},
		"proxies in separate header lines": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"203.0.113.9", "10.0.0.9"},
			want:      "203.0.113.9",
		},
		"mixed trusted networks": {
			peer:      "192.168.1.1:41000",
			forwarded: []string{"203.0.113.9, 10.0.0.9, 192.168.1.9"},
			want:      "203.0.113.9",
		},
		"the client pre-populated the header": {
			/*
				The client sent "9.9.9.9" and the proxy appended what it
				actually saw. Walking from the right finds the real address
				first and never reaches the invented one.
			*/
			peer:      "10.0.0.5:41000",
			forwarded: []string{"9.9.9.9, 203.0.113.9"},
			want:      "203.0.113.9",
		},
		"an entry carrying a port": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"203.0.113.9:52344"},
			want:      "203.0.113.9",
		},
		"an IPv6 client": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"2001:db8::1"},
			want:      "2001:db8::1",
		},
		"an IPv4-mapped client is unmapped": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"::ffff:203.0.113.9"},
			want:      "203.0.113.9",
		},
		"whitespace around entries": {
			peer:      "10.0.0.5:41000",
			forwarded: []string{"  203.0.113.9 ,  10.0.0.9  "},
			want:      "203.0.113.9",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := resolve.clientAddress(forwardedRequest(test.peer, test.forwarded...))
			if got != test.want {
				t.Errorf("clientAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

/*
TestUnusableChainsFallBackToThePeer proves the resolver never invents an
answer.

Every one of these is a case where the header cannot be believed to the end. In
each, the peer is the closest address Convia can actually vouch for, and a
request that would otherwise be charged to nobody stays charged to the
connection it arrived on.
*/
func TestUnusableChainsFallBackToThePeer(t *testing.T) {
	resolve := newResolver(prefixes(t, "10.0.0.0/8"))

	tests := map[string][]string{
		"no header at all":       nil,
		"an empty header":        {""},
		"every entry is trusted": {"10.0.0.9, 10.0.0.8"},
		"a malformed entry":      {"not-an-address, 10.0.0.9"},
		"a bare port":            {":8080"},
		"a zone identifier":      {"fe80::1%eth0"},
	}

	for name, forwarded := range tests {
		t.Run(name, func(t *testing.T) {
			got := resolve.clientAddress(forwardedRequest("10.0.0.5:41000", forwarded...))
			if got != "10.0.0.5" {
				t.Errorf("clientAddress() = %q, want the peer address", got)
			}
		})
	}
}

/*
TestAMalformedEntryStopsTheWalk proves garbage is not stepped over.

Skipping an unparseable entry would mean stepping past whatever wrote it and
believing what lies further left, which is the spoof again by another route. A
proxy emitting garbage is not one whose earlier entries can be trusted.
*/
func TestAMalformedEntryStopsTheWalk(t *testing.T) {
	resolve := newResolver(prefixes(t, "10.0.0.0/8"))

	got := resolve.clientAddress(forwardedRequest("10.0.0.5:41000", "203.0.113.9, nonsense, 10.0.0.9"))
	if got == "203.0.113.9" {
		t.Error("the walk stepped over a malformed entry and believed what was behind it")
	}
	if got != "10.0.0.5" {
		t.Errorf("clientAddress() = %q, want the peer address", got)
	}
}

/*
TestABareAddressIsTrustedAsASingleHost proves the configuration shorthand
works, since writing /32 for one load balancer is a detail nobody should have
to remember.
*/
func TestABareAddressIsTrustedAsASingleHost(t *testing.T) {
	resolve := newResolver([]netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("192.0.2.7"), 32)})

	got := resolve.clientAddress(forwardedRequest("192.0.2.7:41000", "203.0.113.9"))
	if got != "203.0.113.9" {
		t.Errorf("clientAddress() = %q, want the forwarded client", got)
	}

	// One address over is a different host and is not trusted.
	got = resolve.clientAddress(forwardedRequest("192.0.2.8:41000", "203.0.113.9"))
	if got != "192.0.2.8" {
		t.Errorf("clientAddress() = %q, want the peer address", got)
	}
}

// A peer address Convia cannot parse must not become a usable bucket key.
func TestAnUnparseablePeerIsHandled(t *testing.T) {
	resolve := newResolver(prefixes(t, "10.0.0.0/8"))

	request := httptest.NewRequest(http.MethodGet, "/v1/users", nil)
	request.RemoteAddr = "pipe"

	if got := resolve.clientAddress(request); got == "" {
		t.Error("clientAddress() returned an empty key, which would pool unrelated callers together")
	}
}
