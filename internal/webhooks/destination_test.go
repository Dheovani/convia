package webhooks

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/*
TestConviaRefusesToBeUsedAsAProxy is M15-011, and it is the test this package
most needs.

A destination is chosen by a tenant and fetched by Convia's own process, from
inside whatever network Convia runs in. Every address below is somewhere a
webhook receiver does not live and something interesting does: the cloud
metadata service, the private network, Convia itself.
*/
func TestConviaRefusesToBeUsedAsAProxy(t *testing.T) {
	guard := NewDestinations(false)

	refused := map[string]string{
		"loopback":                  "127.0.0.1:8080",
		"loopback by another name":  "127.0.0.53:80",
		"IPv6 loopback":             "[::1]:443",
		"the private ten":           "10.0.0.7:80",
		"the private one-nine-two":  "192.168.1.10:443",
		"the private one-seven-two": "172.16.4.4:8080",
		"cloud metadata":            "169.254.169.254:80",
		"link-local IPv6":           "[fe80::1]:80",
		"unique local IPv6":         "[fd00::1]:80",
		"carrier-grade NAT":         "100.64.0.1:80",
		"the unspecified address":   "0.0.0.0:80",
		"multicast":                 "224.0.0.1:80",
		"documentation":             "192.0.2.1:80",
		"reserved for the future":   "240.0.0.1:80",
		"IETF protocol assignments": "192.0.0.1:80",
		"benchmarking":              "198.18.0.1:80",

		/*
			The classic way past a naive check: an IPv4 address wearing an IPv6
			costume. Anything that parses the string rather than the address
			lets this through.
		*/
		"private IPv4 mapped into IPv6": "[::ffff:10.0.0.7]:80",
		"metadata mapped into IPv6":     "[::ffff:169.254.169.254]:80",
	}

	for name, address := range refused {
		t.Run(name, func(t *testing.T) {
			err := guard.Control("tcp", address, nil)
			if err == nil {
				t.Fatalf("Convia would have connected to %s", address)
			}
			if !errors.Is(err, ErrUnsafeDestination) {
				t.Errorf("the refusal is %v, not an unsafe destination", err)
			}
		})
	}
}

// TestAPublicAddressIsAllowed keeps the guard from refusing the thing it exists
// to permit.
func TestAPublicAddressIsAllowed(t *testing.T) {
	guard := NewDestinations(false)

	for name, address := range map[string]string{
		"a public IPv4": "93.184.216.34:443",
		"a public IPv6": "[2606:2800:220:1:248:1893:25c8:1946]:443",
	} {
		t.Run(name, func(t *testing.T) {
			if err := guard.Control("tcp", address, nil); err != nil {
				t.Errorf("a public address was refused: %v", err)
			}
		})
	}
}

/*
TestDevelopmentMayReachALocalReceiver is why the guard is not simply always on.

A webhook nobody can test locally is a webhook nobody adopts. Development
reaches localhost; production has no setting that would let it.
*/
func TestDevelopmentMayReachALocalReceiver(t *testing.T) {
	guard := NewDestinations(true)

	if err := guard.Control("tcp", "127.0.0.1:8080", nil); err != nil {
		t.Errorf("a development instance could not reach a local receiver: %v", err)
	}
	if err := guard.Permits("http://localhost:8080/hooks"); err != nil {
		t.Errorf("a development instance refused a plain http receiver: %v", err)
	}
}

/*
TestPrivateAddressesAreAChoiceApartFromPlainHTTP covers the guard links between
installations use. In development, refusing the private network leaves plain
http allowed; in production, allowing the private network does not bring plain
http back.
*/
func TestPrivateAddressesAreAChoiceApartFromPlainHTTP(t *testing.T) {
	closed := NewDestinations(true).WithPrivateAddresses(false)
	if err := closed.Control("tcp", "127.0.0.1:8080", nil); !errors.Is(err, ErrUnsafeDestination) {
		t.Errorf("a guard refusing private addresses reached loopback: %v", err)
	}
	if err := closed.Permits("http://convia.example"); err != nil {
		t.Errorf("development refused plain http once private addresses were refused: %v", err)
	}

	opened := NewDestinations(false).WithPrivateAddresses(true)
	if err := opened.Control("tcp", "192.168.1.10:443", nil); err != nil {
		t.Errorf("a guard allowing private addresses refused one: %v", err)
	}
	if err := opened.Permits("http://convia.example"); err == nil {
		t.Error("allowing private addresses in production also allowed plain http")
	}
}

/*
TestProductionRequiresHTTPS is the half of the check that does not depend on
what a name resolves to.

A webhook body describes a conversation between real people. The signature
proves who sent it; it hides nothing.
*/
func TestProductionRequiresHTTPS(t *testing.T) {
	guard := NewDestinations(false)

	if err := guard.Permits("http://hooks.example.com/convia"); err == nil {
		t.Error("a production instance would have delivered over plain http")
	}
	if err := guard.Permits("https://hooks.example.com/convia"); err != nil {
		t.Errorf("a production instance refused an https destination: %v", err)
	}
}

/*
TestTheCheckRunsAtTheSocketRatherThanAtRegistration is the property that makes
the guard worth having.

A name an attacker controls can resolve to a public address while the
registration is being checked and to a private one an hour later. The only
check that cannot be raced is the one on the address actually being connected
to, which is why it lives in the dialer.

This proves it end to end: the client is given a URL whose host resolves to
loopback, and the connection never happens.
*/
func TestTheCheckRunsAtTheSocketRatherThanAtRegistration(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Convia connected to a loopback receiver in production")
	}))
	defer receiver.Close()

	guard := NewDestinations(false)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Control: guard.Control}).DialContext,
		},
	}

	response, err := client.Get(receiver.URL)
	if err == nil {
		response.Body.Close()
		t.Fatal("the request reached a loopback address")
	}
	if !strings.Contains(err.Error(), ErrUnsafeDestination.Error()) {
		t.Errorf("the request failed for the wrong reason: %v", err)
	}
}

/*
TestAMalformedDestinationIsRefusedBeforeAnythingElse covers the shapes a
request can get wrong.

None of these is a security question — they are a caller mistake, and each is
answered by naming the field rather than by a failed delivery an hour later.
*/
func TestAMalformedDestinationIsRefusedBeforeAnythingElse(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":                    "",
		"not a URL":                "not a url at all",
		"an unsupported scheme":    "ftp://hooks.example.com/convia",
		"a scheme Convia will not": "file:///etc/passwd",
		"no host":                  "https:///convia",
		"credentials in the URL":   "https://user:password@hooks.example.com/convia",
		"a fragment":               "https://hooks.example.com/convia#section",
		"too long":                 "https://hooks.example.com/" + strings.Repeat("a", maxURLLength),
	} {
		t.Run(name, func(t *testing.T) {
			var validation ValidationError

			_, err := NormalizeURL(raw)
			if !errors.As(err, &validation) {
				t.Fatalf("NormalizeURL(%q) error = %v, want a validation error", raw, err)
			}
			if validation.Field != "url" {
				t.Errorf("the refusal names %q rather than the destination", validation.Field)
			}
		})
	}
}

/*
TestAnAddressInAnIPv6CostumeIsStillWhatItIs is the classic way past a naive
guard, and the one worth a test of its own.

`::ffff:169.254.169.254` is the cloud metadata service written as IPv6. A check
that asks `IsLinkLocalUnicast` of the address as it arrived gets `false`, because
what arrived is an IPv6 address that merely contains an IPv4 one — so it has to
be unwrapped before anything is asked about it.

The IPv6 ranges are here for the same reason the IPv4 ones are: they exist,
Convia refuses them, and nothing proved it.
*/
func TestAnAddressInAnIPv6CostumeIsStillWhatItIs(t *testing.T) {
	guard := NewDestinations(false)

	for address, why := range map[string]string{
		/*
			The one that actually depends on unwrapping.

			The ranges with no predicate of their own are matched with
			`Prefix.Contains`, and an IPv4-mapped IPv6 address is not considered
			IPv4 by it — so without `Unmap` this one is matched by nothing at
			all and is allowed. The addresses below it are refused by netip's own
			predicates either way, which is why they are not enough on their own.
		*/
		"[::ffff:100.64.0.1]:80":      "carrier-grade NAT, written as IPv6",
		"[::ffff:198.18.0.1]:80":      "a benchmarking range, written as IPv6",
		"[::ffff:240.0.0.1]:80":       "a reserved range, written as IPv6",
		"[::ffff:169.254.169.254]:80": "cloud metadata, written as IPv6",
		"[::ffff:127.0.0.1]:80":       "loopback, written as IPv6",
		"[::ffff:10.0.0.7]:80":        "a private address, written as IPv6",
		"[::1]:80":                    "IPv6 loopback",
		"[fc00::1]:80":                "an IPv6 unique-local address",
		"[fe80::1]:80":                "an IPv6 link-local address",
		"[::]:80":                     "the unspecified IPv6 address",
		"[ff02::1]:80":                "IPv6 multicast",
	} {
		if err := guard.Control("tcp6", address, nil); !errors.Is(err, ErrUnsafeDestination) {
			t.Errorf("%s (%s) error = %v, want it refused", address, why, err)
		}
	}
}

// TestAPublicIPv6AddressIsAllowed: the guard refuses what is not the public
// internet, and an IPv6 address on it is the public internet.
func TestAPublicIPv6AddressIsAllowed(t *testing.T) {
	guard := NewDestinations(false)

	if err := guard.Control("tcp6", "[2606:2800:220:1:248:1893:25c8:1946]:443", nil); err != nil {
		t.Errorf("a public IPv6 address error = %v, want it allowed", err)
	}
}
