package api

import (
	"net/http"
)

// ErrorCode is a stable, machine-readable identifier for a public failure.
//
// Codes are part of Convia's public contract: clients may branch on them, so
// existing codes must not change meaning. Adding a code is an additive change.
type ErrorCode string

const (
	// CodeInvalidRequest reports a syntactically valid request that violates
	// the endpoint contract, such as an unknown or missing field.
	CodeInvalidRequest ErrorCode = "invalid_request"
	// CodeMalformedJSON reports a body that is not valid JSON or whose values
	// do not match the expected JSON types.
	CodeMalformedJSON ErrorCode = "malformed_json"
	// CodeUnsupportedMediaType reports a request whose Content-Type is not
	// accepted by the endpoint.
	CodeUnsupportedMediaType ErrorCode = "unsupported_media_type"
	// CodePayloadTooLarge reports a request body above the accepted limit.
	CodePayloadTooLarge ErrorCode = "payload_too_large"
	// CodeNotFound reports an unknown route or a missing resource.
	CodeNotFound ErrorCode = "not_found"
	// CodeMethodNotAllowed reports a known route addressed with an
	// unsupported HTTP method.
	CodeMethodNotAllowed ErrorCode = "method_not_allowed"
	// CodeInternal reports an unexpected server-side condition.
	CodeInternal ErrorCode = "internal_error"
)

// ErrorBody is the public representation of a single failure.
type ErrorBody struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id,omitempty"`
}

// errorResponse wraps ErrorBody so that error responses never collide with
// resource representations.
type errorResponse struct {
	Error ErrorBody `json:"error"`
}

// Failure describes a public API failure together with its HTTP status.
//
// It is returned by helpers that detect transport-level problems so that
// handlers can decide when to respond without restating status codes.
type Failure struct {
	Status  int
	Code    ErrorCode
	Message string
}

// NewFailure builds a Failure with the given status, code, and public message.
func NewFailure(status int, code ErrorCode, message string) *Failure {
	return &Failure{Status: status, Code: code, Message: message}
}

// Error implements the error interface using the public failure message.
func (failure *Failure) Error() string {
	return string(failure.Code) + ": " + failure.Message
}

// WriteFailure sends failure as a JSON error response.
func WriteFailure(response http.ResponseWriter, request *http.Request, failure *Failure) error {
	return WriteError(response, request, failure.Status, failure.Code, failure.Message)
}

// WriteError sends a JSON error response and correlates it with the request ID
// when one is present in the request context.
func WriteError(response http.ResponseWriter, request *http.Request, status int, code ErrorCode, message string) error {
	body := errorResponse{Error: ErrorBody{
		Code:    code,
		Message: message,
	}}
	if request != nil {
		body.Error.RequestID = RequestIDFromContext(request.Context())
	}

	return Write(response, status, body)
}
