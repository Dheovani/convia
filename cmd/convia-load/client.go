package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
caller is one party talking to Convia: an operator, a tenant's backend, or a
person. Each has the credential it presents and, for people, the address it
appears to come from.

The address is sent as X-Forwarded-For, which Convia believes only from a proxy
it was told to trust. A load run configures this generator as one, because a
thousand people arriving from one address is not what a thousand people look
like: every budget keyed by address would see one caller and refuse it, and the
run would measure the refusals.
*/
type caller struct {
	base    string
	key     string
	address string
	client  *http.Client
	record  *recorder
}

// errStatus is a refusal, kept apart from a failure to reach Convia at all.
type errStatus struct {
	status int
	code   string
}

func (err errStatus) Error() string { return fmt.Sprintf("%d %s", err.status, err.code) }

/*
do sends one request, records how long it took under operation, and decodes
the answer into into. A refusal is recorded by its status and code, so the
report says *what* went wrong at load rather than only how often.
*/
func (who caller) do(
	ctx context.Context,
	operation,
	method,
	path string,
	body any,
	into any,
	headers ...string,
) (*http.Response, error) {
	/*
		Setting up waits when Convia asks it to, as a client that wants to
		succeed would: registering people in bulk runs into the bound on how
		many passwords Convia derives at once, which answers 503 with a
		Retry-After rather than letting a burst decide how much memory it
		allocates. Under load nothing is retried, because there the refusal is
		the measurement.
	*/
	for tries := 0; ; tries++ {
		response, err := who.once(ctx, operation, method, path, body, into, headers...)
		var refused errStatus
		if !strings.HasPrefix(operation, "setup") || tries >= 10 || !errors.As(err, &refused) ||
			(refused.status != http.StatusServiceUnavailable && refused.status != http.StatusTooManyRequests) {
			return response, err
		}

		wait := time.Second
		if seconds, parsed := strconv.Atoi(response.Header.Get("Retry-After")); parsed == nil && seconds > 0 {
			wait = time.Duration(seconds) * time.Second
		}

		sleep(ctx, wait)
	}
}

// once sends one request; see do.
func (who caller) once(
	ctx context.Context,
	operation,
	method,
	path string,
	body any,
	into any,
	headers ...string,
) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, who.base+path, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if who.key != "" {
		request.Header.Set("Authorization", "Bearer "+who.key)
	}
	if who.address != "" {
		request.Header.Set("X-Forwarded-For", who.address)
	}
	for index := 0; index+1 < len(headers); index += 2 {
		request.Header.Set(headers[index], headers[index+1])
	}

	started := time.Now()
	response, err := who.client.Do(request)
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) {
			who.record.failed(operation, "unreachable")
		}
		return nil, err
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	elapsed := time.Since(started)
	if err != nil {
		who.record.failed(operation, "body cut short")
		return nil, err
	}

	if response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(read, &failure)
		who.record.failed(operation, fmt.Sprintf("%d %s", response.StatusCode, failure.Error.Code))
		return response, errStatus{status: response.StatusCode, code: failure.Error.Code}
	}

	who.record.took(operation, elapsed)
	if into != nil && len(read) > 0 {
		if err := json.Unmarshal(read, into); err != nil {
			who.record.failed(operation, "unreadable answer")
			return response, err
		}
	}
	return response, nil
}
