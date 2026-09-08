/*
Package idempotency lets a caller retry a mutation without repeating its effect.

A client that does not receive a response cannot tell whether the request was
lost on the way out or on the way back. Retrying is the only thing it can do,
and without help that retry creates a second resource. An `Idempotency-Key`
turns the retry into a question Convia can answer from what it already did.

The public behavior is specified in docs/api-compatibility.md. This package
implements it for any endpoint, because the same guarantee is owed by room
creation, call creation, and everything after them; nothing here knows what a
room is.
*/
package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	// Header is the request header carrying a client-generated key.
	Header = "Idempotency-Key"

	// MaxKeyLength bounds a key, as docs/api-compatibility.md states.
	MaxKeyLength = 255

	/*
		Retention is how long a key is remembered.

		The contract promises at least 24 hours, which is the window in which a
		client that failed and backed off will realistically come back. Past
		it, the same key is treated as a new request rather than replayed,
		because a caller reusing a day-old key almost certainly means a new
		operation.
	*/
	Retention = 24 * time.Hour
)

var (
	/*
		ErrConflictingRequest reports a key already used for a different request.

		Answering it with the stored response would be a lie about what the
		caller asked for, and performing it would defeat the key. Refusing is
		the only honest answer.
	*/
	ErrConflictingRequest = errors.New("the idempotency key was used for a different request")

	/*
		ErrInProgress reports a key whose first request has not finished.

		Two requests carrying one key arrived close enough together that the
		first is still running. The second is refused rather than queued: the
		caller learns the operation is under way and can retry to collect its
		result, which is the answer that neither duplicates the work nor holds
		a connection open waiting for it.
	*/
	ErrInProgress = errors.New("a request with this idempotency key is still in progress")
)

/*
NormalizeKey validates a client-supplied key.

Surrounding whitespace is trimmed because it is invisible and would otherwise
make two keys a client believes are the same into two different reservations.
Nothing else about the value is interpreted: it is opaque to Convia, and
constraining its shape would reject generators that are perfectly sound.
*/
func NormalizeKey(value string) (string, error) {
	key := strings.TrimSpace(value)

	switch {
	case key == "":
		return "", ValidationError{Message: "The Idempotency-Key header must not be empty."}
	case len(key) > MaxKeyLength:
		return "", ValidationError{Message: "The Idempotency-Key header must not exceed 255 characters."}
	}
	return key, nil
}

// ValidationError reports a key that violates the contract.
type ValidationError struct {
	Message string
}

func (err ValidationError) Error() string {
	return err.Message
}

/*
Attempt is one request offered under a key.

Scope is who the key belongs to, so that two callers using the same key never
meet. Method, Path, and Body are what the request asked for, and together they
decide whether a repeat is the same request or a different one.
*/
type Attempt struct {
	Scope  string
	Key    string
	Method string
	Path   string
	Body   []byte
}

/*
digest fingerprints what a request asked for.

The request itself is not stored. A digest answers the only question the
contract asks of it -- is this the same request as before -- while keeping a
body an application composed out of a second table. The parts are separated by
a byte that cannot appear in a method or a path, so no two different requests
can produce one input.
*/
func (attempt Attempt) digest() string {
	sum := sha256.New()
	sum.Write([]byte(attempt.Method))
	sum.Write([]byte{0})
	sum.Write([]byte(attempt.Path))
	sum.Write([]byte{0})
	sum.Write(attempt.Body)

	return hex.EncodeToString(sum.Sum(nil))
}

/*
Result is the response a completed attempt produced.

Headers are kept because a response is not only its body: a created room
carries the entity tag its updates will be conditional on, and a replay that
dropped it would answer a retry with less than the original.
*/
type Result struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

/*
Decision is what a caller should do with an attempt.

Replay means the answer is already known and the operation must not run again.
Otherwise the caller owns the key and is expected to report what happened with
Complete or Release.
*/
type Decision struct {
	Replay bool
	Result Result
}
