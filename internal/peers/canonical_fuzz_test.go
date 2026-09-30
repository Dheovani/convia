package peers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

/*
FuzzTwoRequestsNeverShareASignatureBase is `M24-012` pointed at the place where
a defect would be a forgery rather than a crash.

A signature covers [canonical], which joins its fields with a newline and
prefixes none of them with a length. **That is unambiguous only while no field
can contain the separator**, and nothing in the primitive says so — the reason
it holds is somewhere else entirely: every field is checked against a strict
alphabet before it gets here. The account is `accounts.ValidID`, the nonce is
`secret.ValidRandom`, the timestamp parses as an integer, the version is one of
a fixed list, the method is one net/http accepted, the target is a
percent-encoded URI, and the authority is this installation's own configured
address rather than anything a caller sent.

So the invariant does not live in the join and does not live in any one check.
It lives in the **combination**, which is exactly the kind of thing that breaks
when somebody loosens one validation for a good reason years later. This is the
guard on that.

Fields are built from the fuzzer's bytes into the shapes those checks accept,
rather than filtered after the fact: a target that generated mostly rejected
input would explore almost nothing.
*/
func FuzzTwoRequestsNeverShareASignatureBase(f *testing.F) {
	f.Add(0, 0, "/v1/peer/rooms/x", "AAAAAAAAAAAAAAAAAAAAAAAAAA", int64(1700000000), "BBBBBBBBBBBBBBBBBBBBBBBBBB",
		0, 1, "/v1/peer/rooms/y", "AAAAAAAAAAAAAAAAAAAAAAAAAB", int64(1700000001), "BBBBBBBBBBBBBBBBBBBBBBBBBC")

	// Neighbouring shapes: a target that ends where another begins, and
	// fields that differ only by where a boundary would fall.
	f.Add(0, 0, "/a", "AAAAAAAAAAAAAAAAAAAAAAAAAA", int64(1), "BBBBBBBBBBBBBBBBBBBBBBBBBB",
		0, 0, "/a/b", "AAAAAAAAAAAAAAAAAAAAAAAAAA", int64(1), "BBBBBBBBBBBBBBBBBBBBBBBBBB")
	f.Add(0, 0, "/", "AAAAAAAAAAAAAAAAAAAAAAAAAA", int64(11), "BBBBBBBBBBBBBBBBBBBBBBBBBB",
		0, 0, "/", "AAAAAAAAAAAAAAAAAAAAAAAAAA", int64(1), "BBBBBBBBBBBBBBBBBBBBBBBBBB")

	f.Fuzz(func(t *testing.T,
		versionA, methodA int, targetA, accountA string, timestampA int64, nonceA string,
		versionB, methodB int, targetB, accountB string, timestampB int64, nonceB string) {
		first := signable(versionA, methodA, targetA, accountA, timestampA, nonceA)
		second := signable(versionB, methodB, targetB, accountB, timestampB, nonceB)

		/*
			The body is the same in both. It reaches the form as a fixed-length
			digest, so it is the one field that cannot move a boundary, and
			varying it would test hashing rather than joining.
		*/
		body := []byte("whatever")

		baseA := canonical(first.version, first.method, authority, first.target, body,
			first.account, first.timestamp, first.nonce)
		baseB := canonical(second.version, second.method, authority, second.target, body,
			second.account, second.timestamp, second.nonce)

		if first.describes() != second.describes() && string(baseA) == string(baseB) {
			t.Fatalf("two different requests share a signature base:\n  A = %s\n  B = %s\n  base = %q",
				first.describes(), second.describes(), baseA)
		}
		if first.describes() == second.describes() && string(baseA) != string(baseB) {
			t.Fatalf("one request produced two signature bases:\n  %q\n  %q", baseA, baseB)
		}
	})
}

// authority is this installation's own, because that is where the verifier
// takes it from — a caller does not choose it.
const authority = "convia.example"

// request is one signable request, in the shapes the verifier accepts.
type request struct {
	version, method, target, account, timestamp, nonce string
}

/*
describes is the tuple the form is meant to distinguish, length-prefixed.

**Length prefixes are the point.** A reference encoding that joined with a
separator would have whatever flaw is being looked for, and the test would
agree with the bug instead of finding it.
*/
func (one request) describes() string {
	written := &strings.Builder{}
	for _, field := range []string{
		one.version,
		strings.ToUpper(one.method),
		strings.ToLower(authority),
		one.target,
		one.account,
		one.timestamp,
		one.nonce,
	} {
		fmt.Fprintf(written, "%d:%s", len(field), field)
	}
	return written.String()
}

/*
signable turns the fuzzer's bytes into a request the verifier would accept.

Each field is forced into the alphabet its check enforces, so every iteration
exercises the join rather than the checks — which have tests of their own.
*/
func signable(version, method int, target, account string, timestamp int64, nonce string) request {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

	return request{
		version:   Spoken[abs(version)%len(Spoken)],
		method:    methods[abs(method)%len(methods)],
		target:    asTarget(target),
		account:   "acc_" + asBase32(account),
		timestamp: strconv.FormatInt(timestamp, 10),
		nonce:     asBase32(nonce),
	}
}

// asTarget makes a request URI the way net/url would present one: a path that
// is percent-encoded, so no separator survives into it.
func asTarget(raw string) string {
	path := &url.URL{Path: "/" + strings.TrimPrefix(raw, "/")}
	return path.RequestURI()
}

// asBase32 forces bytes into the alphabet the identifier and nonce checks
// accept, at the length they require.
func asBase32(raw string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

	written := &strings.Builder{}
	for i := range 26 {
		if i < len(raw) {
			written.WriteByte(alphabet[int(raw[i])%len(alphabet)])
			continue
		}
		written.WriteByte('A')
	}
	return written.String()
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
