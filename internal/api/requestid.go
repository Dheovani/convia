package api

import (
	"context"
	"crypto/rand"
)

// RequestIDHeader carries the correlation identifier of a request and of its
// response. Clients may supply their own value; Convia replaces unacceptable
// values with a generated one.
const RequestIDHeader = "X-Request-ID"

// maxRequestIDLength bounds client-supplied correlation identifiers so that
// they stay safe to log and cheap to store.
const maxRequestIDLength = 128

type contextKey struct{}

var requestIDContextKey contextKey

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

// RequestIDFromContext returns the request ID stored in ctx, or an empty
// string when the context carries none.
func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

// NewRequestID generates a random correlation identifier.
func NewRequestID() string {
	return rand.Text()
}

// NormalizeRequestID returns a correlation identifier safe to echo and log.
//
// A client-supplied value is preserved only when it is non-empty, within the
// accepted length, and limited to unreserved identifier characters. Any other
// value is replaced by a freshly generated identifier so that logs and
// response headers cannot be poisoned by untrusted input.
func NormalizeRequestID(value string) string {
	if value == "" || len(value) > maxRequestIDLength {
		return NewRequestID()
	}

	for _, character := range []byte(value) {
		if !isRequestIDCharacter(character) {
			return NewRequestID()
		}
	}
	return value
}

func isRequestIDCharacter(character byte) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	case character >= '0' && character <= '9':
		return true
	case character == '-', character == '_', character == '.', character == ':':
		return true
	default:
		return false
	}
}
