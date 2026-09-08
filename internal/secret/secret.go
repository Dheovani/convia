/*
Package secret owns the primitives shared by every family of Convia bearer key.

Convia issues more than one kind of opaque key: an application credential and
an operator credential. They differ in who they authenticate and what they
permit, but not in how a secret is generated, rendered, parsed, stored, or
compared. That part lives here so it exists once.

The duplication this avoids is not incidental. A weakness fixed in one copy of
a constant-time comparison and missed in the other would be exactly the kind of
defect that survives review, so there is one copy.
*/
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

const (
	/*
		RandomLength is the number of characters crypto/rand.Text emits.

		It is base32, so each character carries five bits: twenty-six of them
		is roughly 130 bits of entropy, which cannot be searched at any rate.
	*/
	RandomLength = 26

	// DigestLength is the stored SHA-256 digest size, in bytes.
	DigestLength = 32
)

/*
Value is the plaintext half of a key, which Convia holds only in memory.

It is a distinct type so that a secret cannot be passed where an identifier is
expected, and so that every place one is handled is easy to find.
*/
type Value string

// New generates the plaintext half of a new key.
func New() Value {
	return Value(rand.Text())
}

/*
Digest reduces a secret to what Convia stores.

The secret is random with roughly 130 bits of entropy rather than chosen by a
person, so it cannot be guessed or found in a dictionary, and a deliberately
slow key derivation would add latency to every authenticated request without
making the search any more feasible. A single SHA-256 is the right cost here.
*/
func Digest(value Value) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

/*
Matches reports whether a presented secret produced a stored digest.

The comparison takes the same time whichever bytes differ, so that timing
cannot be used to recover a digest one byte at a time.
*/
func Matches(stored []byte, presented Value) bool {
	return subtle.ConstantTimeCompare(stored, Digest(presented)) == 1
}

/*
ValidRandom reports whether a string is the base32 alphabet crypto/rand.Text
emits, at the length it emits.

Checking the shape before touching the database is what makes malformed input
free: it cannot be used to probe for identifiers, because no lookup happens.
*/
func ValidRandom(value string) bool {
	if len(value) != RandomLength {
		return false
	}

	for _, character := range value {
		isBase32 := (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
		if !isBase32 {
			return false
		}
	}
	return true
}

/*
Format renders and parses one family of keys.

A family is identified by two prefixes: one on the public identifier, and one
on the presented token. Distinct token prefixes are what let Convia route a
presented key to the right verifier without querying for it, and what let a
person or a secret scanner tell an application key from an operator key by
sight.
*/
type Format struct {
	// Token prefixes the presented key, for example "cvk".
	Token string
	// ID prefixes the public identifier, for example "cred_".
	ID string
}

// NewID generates an opaque public identifier in this family.
func (format Format) NewID() string {
	return format.ID + rand.Text()
}

// ValidID reports whether an identifier has this family's shape.
func (format Format) ValidID(id string) bool {
	random, found := strings.CutPrefix(id, format.ID)
	return found && ValidRandom(random)
}

/*
Render produces the string a caller presents to Convia.

The identifier travels with the secret so that verification can look up one row
by primary key and then compare, rather than testing the presented secret
against every stored digest.
*/
func (format Format) Render(id string, value Value) string {
	return format.Token + "_" + strings.TrimPrefix(id, format.ID) + "_" + string(value)
}

/*
Parse recovers the identifier and secret from a presented key.

A token belonging to a different family is rejected here, so an operator key
presented to a tenant route, or the reverse, fails on its shape without
reaching a database. The boolean is false for every malformed input; the caller
turns that into its own domain's single indistinguishable failure.
*/
func (format Format) Parse(token string) (id string, value Value, ok bool) {
	prefix, rest, found := strings.Cut(token, "_")
	if !found || prefix != format.Token {
		return "", "", false
	}

	random, secretPart, found := strings.Cut(rest, "_")
	if !found || !ValidRandom(random) || !ValidRandom(secretPart) {
		return "", "", false
	}
	return format.ID + random, Value(secretPart), true
}
