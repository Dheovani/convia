package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	/*
		SignatureHeader carries the proof that a delivery came from Convia.

		The scheme is versioned in the value rather than in the header name, so
		a second algorithm can be sent alongside the first during a migration
		and a consumer can accept either without the header changing shape.
	*/
	SignatureHeader = "Convia-Signature"

	// DeliveryHeader names this delivery, which is what a consumer stores to
	// recognize a retry of something it has already handled.
	DeliveryHeader = "Convia-Delivery"

	// EventHeader names the type, so a consumer can route without parsing the
	// body first.
	EventHeader = "Convia-Event"

	// AttemptHeader says which attempt this is, starting at one. A consumer
	// seeing a number above one knows Convia did not hear back, which is
	// usually the more useful half of a duplicate.
	AttemptHeader = "Convia-Attempt"

	/*
		signatureVersion is the scheme the v1 element uses: HMAC-SHA256 over the
		timestamp, a dot, and the exact request body.

		The timestamp is inside the signed material on purpose. Signing only the
		body would let anyone who once saw a valid delivery replay it forever,
		because the signature would stay valid for as long as the secret did.
	*/
	signatureVersion = "v1"
)

/*
Sign produces the value of the signature header for one attempt.

The signed material is `timestamp.body`, and the timestamp travels beside the
signature in the clear. A consumer recomputes the same string and compares in
constant time, which is what docs/webhooks.md shows.
*/
func Sign(key Secret, signedAt time.Time, body []byte) string {
	seconds := strconv.FormatInt(signedAt.Unix(), 10)

	mac := hmac.New(sha256.New, []byte(key.Reveal()))
	mac.Write([]byte(seconds))
	mac.Write([]byte("."))
	mac.Write(body)

	return fmt.Sprintf("t=%s,%s=%s", seconds, signatureVersion, hex.EncodeToString(mac.Sum(nil)))
}

/*
Verify reports whether a header proves a body was signed with a key, within a
tolerance.

Convia does not receive its own webhooks, so nothing in the service calls this.
It exists because a signature scheme that is only ever produced is a scheme
nobody has checked: the tests use it to verify what Convia actually sent, and
the same twenty lines are what a consumer writes. A scheme that is awkward to
verify is a scheme consumers will skip.
*/
func Verify(key Secret, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	signedAt, presented, ok := parseSignature(header)
	if !ok {
		return false
	}

	/*
		Both directions of the window are checked. A timestamp far in the past
		is a replay; one far in the future is a signature made to outlive the
		window, which is the same attack wearing a different hat.
	*/
	age := now.Sub(signedAt)
	if age > tolerance || age < -tolerance {
		return false
	}

	expected := Sign(key, signedAt, body)
	_, computed, ok := parseSignature(expected)
	if !ok {
		return false
	}

	return hmac.Equal([]byte(computed), []byte(presented))
}

/*
parseSignature pulls the timestamp and the v1 element out of a header.

Anything malformed is one indistinguishable failure. A caller learning *which*
part of a signature was wrong learns something about the secret it did not
have.
*/
func parseSignature(header string) (signedAt time.Time, signature string, ok bool) {
	var seconds int64
	var found bool

	for element := range strings.SplitSeq(header, ",") {
		name, value, hasValue := strings.Cut(strings.TrimSpace(element), "=")
		if !hasValue {
			continue
		}

		switch name {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return time.Time{}, "", false
			}
			seconds = parsed
		case signatureVersion:
			signature = value
			found = true
		}
	}

	if !found || seconds == 0 || signature == "" {
		return time.Time{}, "", false
	}
	return time.Unix(seconds, 0).UTC(), signature, true
}
