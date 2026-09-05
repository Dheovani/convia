package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxJSONRequestBytes bounds the size of a decoded JSON request body.
//
// Endpoints that must accept larger payloads have to opt in explicitly rather
// than raising this shared limit.
const MaxJSONRequestBytes = 1 << 20 // 1 MiB

// maxReportedFieldLength bounds how much client-supplied field text is echoed
// back in an error message.
const maxReportedFieldLength = 64

// DecodeJSON reads a single JSON value from the request body into target.
//
// It enforces the shared transport rules for JSON endpoints: the request must
// declare a JSON content type, the body is bounded, unknown fields are
// rejected, and the body must contain exactly one JSON value. Every failure is
// reported as a public Failure, never as a raw decoder error.
//
// DecodeJSON returns nil on success.
func DecodeJSON(response http.ResponseWriter, request *http.Request, target any) *Failure {
	if failure := requireJSONContentType(request); failure != nil {
		return failure
	}

	request.Body = http.MaxBytesReader(response, request.Body, MaxJSONRequestBytes)

	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return decodeFailure(err)
	}

	if decoder.More() {
		return NewFailure(http.StatusBadRequest, CodeInvalidRequest,
			"The request body must contain a single JSON value.")
	}
	return nil
}

func requireJSONContentType(request *http.Request) *Failure {
	header := request.Header.Get("Content-Type")
	if header == "" {
		return NewFailure(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
			"The request must declare a Content-Type of application/json.")
	}

	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || mediaType != ContentTypeJSON {
		return NewFailure(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
			"The request Content-Type must be application/json.")
	}
	return nil
}

func decodeFailure(err error) *Failure {
	var maxBytesError *http.MaxBytesError
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError

	switch {
	case errors.As(err, &maxBytesError):
		return NewFailure(http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			fmt.Sprintf("The request body must not exceed %d bytes.", MaxJSONRequestBytes))

	case errors.Is(err, io.EOF):
		return NewFailure(http.StatusBadRequest, CodeInvalidRequest,
			"The request body must not be empty.")

	case errors.As(err, &syntaxError):
		return NewFailure(http.StatusBadRequest, CodeMalformedJSON,
			fmt.Sprintf("The request body contains malformed JSON at byte %d.", syntaxError.Offset))

	case errors.Is(err, io.ErrUnexpectedEOF):
		return NewFailure(http.StatusBadRequest, CodeMalformedJSON,
			"The request body contains incomplete JSON.")

	case errors.As(err, &typeError):
		if typeError.Field == "" {
			return NewFailure(http.StatusBadRequest, CodeMalformedJSON,
				"The request body has an unexpected JSON type.")
		}
		return NewFailure(http.StatusBadRequest, CodeMalformedJSON,
			fmt.Sprintf("The request field %q has an unexpected JSON type.", truncateField(typeError.Field)))

	default:
		if field, unknown := unknownField(err); unknown {
			return NewFailure(http.StatusBadRequest, CodeInvalidRequest,
				fmt.Sprintf("The request body contains the unknown field %q.", field))
		}
		return NewFailure(http.StatusBadRequest, CodeMalformedJSON,
			"The request body could not be decoded as JSON.")
	}
}

// unknownField reports whether err is the standard library's unknown-field
// error and extracts the offending field name. encoding/json does not expose a
// typed error for this case.
func unknownField(err error) (string, bool) {
	const prefix = "json: unknown field "

	message := err.Error()
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}

	field := strings.Trim(strings.TrimPrefix(message, prefix), `"`)
	return truncateField(field), true
}

func truncateField(field string) string {
	if len(field) > maxReportedFieldLength {
		return field[:maxReportedFieldLength]
	}
	return field
}
