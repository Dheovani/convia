package webhooks

import (
	"strings"
	"testing"
	"time"
)

/*
FuzzABodyThatChangedNeverVerifies is the guarantee a consumer of Convia's
webhooks actually depends on, and `M24-012` is where it stops being a claim.

An application receiving a delivery checks the signature and then acts on the
body. If a changed body could carry a signature Convia made for a different
one, everything downstream of that check is acting on something Convia did not
say — and the endpoint is reachable by anybody who can send it an HTTP request.

The table tests cover a handful of tampered bodies. This covers every pair of
bodies the fuzzer can reach.
*/
func FuzzABodyThatChangedNeverVerifies(f *testing.F) {
	f.Add([]byte(`{"type":"call.started"}`), []byte(`{"type":"call.ended"}`))
	f.Add([]byte(""), []byte(" "))
	f.Add([]byte("a"), []byte("a "))
	// The shape that would matter if the timestamp and the body could run
	// together: the separator is a full stop and a timestamp is digits.
	f.Add([]byte(".x"), []byte("0.x"))
	f.Add([]byte("00000000.x"), []byte("x"))

	const key = Secret("whsec_4XZQP7KN2VJH6TBWMDR3YAFC5E")
	signedAt := time.Unix(1_700_000_000, 0).UTC()

	f.Fuzz(func(t *testing.T, signed, presented []byte) {
		header := Sign(key, signedAt, signed)

		if !Verify(key, header, signed, signedAt, time.Minute) {
			t.Fatalf("a body did not verify against its own signature: %q", signed)
		}

		if string(signed) == string(presented) {
			return
		}

		if Verify(key, header, presented, signedAt, time.Minute) {
			t.Fatalf("a signature made for %q verified %q", signed, presented)
		}
	})
}

/*
FuzzAnotherEndpointsKeyNeverVerifies.

Every endpoint has its own secret, and rotating one is the remedy when it
leaks. That only means anything while a signature made with one key is useless
with another — otherwise a leaked secret is every endpoint's problem and
rotation fixes nothing.
*/
func FuzzAnotherEndpointsKeyNeverVerifies(f *testing.F) {
	f.Add("4XZQP7KN2VJH6TBWMDR3YAFC5E", "7KQZP4XN2VJH6TBWMDR3YAFC5E", []byte(`{"a":1}`))
	f.Add("A", "B", []byte(""))
	f.Add("", "", []byte("x"))

	signedAt := time.Unix(1_700_000_000, 0).UTC()

	f.Fuzz(func(t *testing.T, mine, theirs string, body []byte) {
		/*
			Secrets are built the way [NewSecret] builds them rather than taken
			from the fuzzer raw, and the reason is a finding this target made on
			its first run: **HMAC cannot tell two keys apart when they differ
			only by trailing NUL bytes**, because it zero-pads a key shorter
			than its block size. `"a"` and `"a\x00"` are one key.

			That is a property of HMAC, not a defect here, and it is unreachable
			in Convia: every secret is `whsec_` and twenty-six characters of one
			base32 alphabet, minted by Convia at creation and at rotation, and
			no caller supplies one. Fuzzing raw strings tested HMAC's padding;
			fuzzing these tests what Convia actually holds.
		*/
		first, second := asSecret(mine), asSecret(theirs)
		header := Sign(first, signedAt, body)

		if !Verify(first, header, body, signedAt, time.Minute) {
			t.Fatalf("a delivery did not verify with the key that signed it")
		}

		if first == second {
			return
		}

		if Verify(second, header, body, signedAt, time.Minute) {
			t.Fatalf("a signature made with one key verified with another: %q and %q", first, second)
		}
	})
}

// asSecret forces bytes into the shape NewSecret produces, so the target
// exercises key separation rather than HMAC's treatment of malformed keys.
func asSecret(raw string) Secret {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

	written := &strings.Builder{}
	written.WriteString("whsec_")
	for i := range 26 {
		if i < len(raw) {
			written.WriteByte(alphabet[int(raw[i])%len(alphabet)])
			continue
		}
		written.WriteByte('A')
	}
	return Secret(written.String())
}

/*
FuzzAHeaderNobodySignedIsRefused.

`parseSignature` reads a header a stranger wrote, which is the one input here
that is entirely under somebody else's control. It must refuse rather than
panic, and it must not accept anything Convia did not sign — a lenient parser
that skipped an element or took the last of a repeated one is the shape that
would.
*/
func FuzzAHeaderNobodySignedIsRefused(f *testing.F) {
	const key = Secret("whsec_4XZQP7KN2VJH6TBWMDR3YAFC5E")
	signedAt := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{"type":"call.started"}`)

	f.Add(Sign(key, signedAt, body))
	f.Add("")
	f.Add("t=1700000000")
	f.Add("v1=deadbeef")
	f.Add("t=1700000000,v1=")
	f.Add("t=0,v1=deadbeef")
	f.Add("t=1700000000,t=1,v1=deadbeef")
	f.Add("t=1700000000,v1=deadbeef,v1=cafe")
	f.Add(",,,,")

	f.Fuzz(func(t *testing.T, header string) {
		if !Verify(key, header, body, signedAt, time.Minute) {
			return
		}

		/*
			It verified, so it must carry the signature Convia would have made
			for this body at this moment. Anything else accepted means the
			parser found a way to agree with something nobody signed.
		*/
		_, presented, ok := parseSignature(header)
		if !ok {
			t.Fatalf("%q verified and then did not parse", header)
		}

		_, expected, _ := parseSignature(Sign(key, signedAt, body))
		if presented != expected {
			t.Fatalf("%q verified carrying %q, which is not what signing this body makes", header, presented)
		}
	})
}
