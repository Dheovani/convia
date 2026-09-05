/*
Package api defines Convia's shared HTTP transport contract.

It owns the wire behavior every public endpoint depends on: JSON
serialization, the public error schema with stable machine-readable codes,
request correlation identifiers, and strict request decoding. Domain
packages depend on this package instead of restating transport rules.

The conventions implemented here are documented in docs/api-conventions.md.
*/
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Prefix is the path prefix of Convia's versioned public API. Operational
// endpoints such as health checks are served outside this prefix.
const Prefix = "/v1"

// ContentTypeJSON is the media type used by every JSON request and response.
const ContentTypeJSON = "application/json"

/*
Write sends payload as a JSON response body with the given status code.

The payload is serialized before any header is written so that a failing
encoder cannot produce a partial response body.
*/
func Write(response http.ResponseWriter, status int, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode JSON response: %w", err)
	}
	body = append(body, '\n')

	response.Header().Set("Content-Type", ContentTypeJSON)
	response.WriteHeader(status)

	if _, err := response.Write(body); err != nil {
		return fmt.Errorf("write JSON response: %w", err)
	}
	return nil
}
