package webhooks

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// tolerance is the window docs/webhooks.md tells consumers to accept.
const tolerance = 5 * time.Minute

func signedAt() time.Time {
	return time.Date(2026, time.September, 10, 14, 4, 56, 0, time.UTC)
}

/*
TestAConsumerCanVerifyWhatConviaSent is the whole point of the scheme.

Convia never receives its own webhooks, so Verify exists to be the twenty lines
a consumer writes — and to prove those twenty lines work against what Convia
actually produces. A signature that is only ever generated is a signature
nobody has checked.
*/
func TestAConsumerCanVerifyWhatConviaSent(t *testing.T) {
	key := Secret("whsec_test")
	body := []byte(`{"id":"evt_1","type":"call.started"}`)

	header := Sign(key, signedAt(), body)

	if !Verify(key, header, body, signedAt(), tolerance) {
		t.Error("a consumer following the documented steps could not verify a real signature")
	}
}

/*
TestChangingAnythingBreaksTheSignature is what the header is for.

Each case below is a way a delivery could arrive altered, and each must fail
for the scheme to be worth sending.
*/
func TestChangingAnythingBreaksTheSignature(t *testing.T) {
	key := Secret("whsec_test")
	body := []byte(`{"id":"evt_1","type":"call.started"}`)
	header := Sign(key, signedAt(), body)

	cases := map[string]struct {
		key    Secret
		header string
		body   []byte
		now    time.Time
	}{
		"a changed body": {
			key: key, header: header, now: signedAt(),
			body: []byte(`{"id":"evt_1","type":"call.ended"}`),
		},
		"a body with one byte added": {
			key: key, header: header, now: signedAt(),
			body: append(append([]byte{}, body...), ' '),
		},
		"another endpoint's key": {
			key: Secret("whsec_somebody_else"), header: header, body: body, now: signedAt(),
		},
		/*
			The timestamp is inside the signed material, so moving it invalidates
			the signature rather than merely shifting the window. Without that,
			anyone who saw one valid delivery could replay it forever.
		*/
		"a timestamp moved to widen the window": {
			key: key, body: body, now: signedAt(),
			header: strings.Replace(header, "t=", "t=9", 1),
		},
		"a signature from a replay, hours later": {
			key: key, header: header, body: body, now: signedAt().Add(6 * time.Hour),
		},
		"a signature dated in the future": {
			key: key, body: body, now: signedAt(),
			header: Sign(key, signedAt().Add(time.Hour), body),
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if Verify(test.key, test.header, test.body, test.now, tolerance) {
				t.Error("a delivery that should not verify did")
			}
		})
	}
}

/*
TestAMalformedHeaderIsOneIndistinguishableFailure keeps the parser from
answering questions.

A caller learning *which* part of a signature was wrong learns something about
a secret it does not have, so every malformed shape produces the same answer.
*/
func TestAMalformedHeaderIsOneIndistinguishableFailure(t *testing.T) {
	key := Secret("whsec_test")
	body := []byte(`{}`)

	for name, header := range map[string]string{
		"empty":                "",
		"no elements":          "nonsense",
		"no timestamp":         "v1=abcdef",
		"no signature":         "t=1789000000",
		"an unknown version":   "t=1789000000,v9=abcdef",
		"a timestamp in words": "t=yesterday,v1=abcdef",
		"an empty signature":   "t=1789000000,v1=",
	} {
		t.Run(name, func(t *testing.T) {
			if Verify(key, header, body, signedAt(), tolerance) {
				t.Errorf("the header %q verified", header)
			}
		})
	}
}

/*
TestASignatureIsTiedToItsOwnMoment covers what makes retries workable.

The timestamp is per attempt, so a retry of the same delivery carries a fresh
signature. That is what lets a consumer refuse anything older than its
tolerance without also refusing Convia's own second try.
*/
func TestASignatureIsTiedToItsOwnMoment(t *testing.T) {
	key := Secret("whsec_test")
	body := []byte(`{"id":"evt_1"}`)

	first := Sign(key, signedAt(), body)
	retried := Sign(key, signedAt().Add(30*time.Second), body)

	if first == retried {
		t.Fatal("two attempts at different moments produced the same signature")
	}
	if !Verify(key, retried, body, signedAt().Add(30*time.Second), tolerance) {
		t.Error("a retry's own signature did not verify at its own moment")
	}
}

/*
TestTheHeaderIsTheShapeTheContractPublishes pins what a consumer parses.

Consumers split on commas and equals signs, so the layout is part of the
public contract in the same way a field name is.
*/
func TestTheHeaderIsTheShapeTheContractPublishes(t *testing.T) {
	header := Sign(Secret("whsec_test"), signedAt(), []byte(`{}`))

	timestamp, signature, found := strings.Cut(header, ",")
	if !found {
		t.Fatalf("the header carries one element: %q", header)
	}
	if !strings.HasPrefix(timestamp, "t=") {
		t.Errorf("the first element is %q, not a timestamp", timestamp)
	}
	if !strings.HasPrefix(signature, "v1=") {
		t.Errorf("the second element is %q, not a v1 signature", signature)
	}

	/*
		Hex, lower case, and the full width of SHA-256. A consumer comparing
		strings has to produce exactly this.
	*/
	digest := strings.TrimPrefix(signature, "v1=")
	if len(digest) != 64 {
		t.Errorf("the signature is %d characters, want 64", len(digest))
	}
	if digest != strings.ToLower(digest) {
		t.Errorf("the signature is not lower-case hex: %q", digest)
	}
}

/*
TestASigningKeyCannotRenderItself is the same guarantee the media secret has.

A key does not leak because somebody printed it deliberately. It leaks because
a struct was logged while somebody was debugging.
*/
func TestASigningKeyCannotRenderItself(t *testing.T) {
	key := NewSecret()

	rendered := map[string]string{
		"%v":  sprint("%v", key),
		"%s":  sprint("%s", key),
		"%q":  sprint("%q", key),
		"%#v": sprint("%#v", key),
	}

	for verb, output := range rendered {
		if strings.Contains(output, key.Reveal()) {
			t.Errorf("%s rendered the key: %s", verb, output)
		}
		if !strings.Contains(output, redacted) {
			t.Errorf("%s produced %q, which does not read as a withheld value", verb, output)
		}
	}

	// The real value is still reachable, deliberately, because signing needs it.
	if key.Reveal() == redacted {
		t.Error("Reveal returned the placeholder rather than the key")
	}
}

// sprint renders a value with a verb, which is how a secret escapes by accident.
func sprint(verb string, value any) string {
	return fmt.Sprintf(verb, value)
}
