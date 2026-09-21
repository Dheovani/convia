/*
Package client is how Convia's desktop application talks to an installation.

**The application is the API client, and the interface is not.** The window runs
Convia's own interface, and that interface asks this package for what it needs
rather than reaching the network itself. Two things follow, and both are the
reason for the arrangement: the session never enters the webview, and the
person's event stream is opened here, where a header can be set on the
handshake — which a page cannot do. See docs/adr/0019 and `M35`.

Nothing here knows what is on the screen, and nothing here holds a window.
*/
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"convia/internal/api"
)

// prefix is the one version of the API this application speaks.
const prefix = "/v1"

/*
requestTimeout bounds one ordinary request.

It is generous rather than tight: the slow ones are signing in and changing a
password, which hash with argon2id, and a person on a bad connection should be
told the server is slow rather than that something failed.
*/
const requestTimeout = 30 * time.Second

/*
Refusal is something the installation refused, explained.

The code is what to branch on — it is part of Convia's public contract — while
the message is prose the installation may reword. The status is kept beside it
because the two answer different questions.
*/
type Refusal struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (refusal *Refusal) Error() string {
	return fmt.Sprintf("convia refused the request: %s (%d)", refusal.Code, refusal.Status)
}

// Unauthenticated reports a session the installation would not accept: it was
// revoked, it expired, or the account is gone.
func (refusal *Refusal) Unauthenticated() bool { return refusal.Status == http.StatusUnauthorized }

/*
Unreachable is an installation that did not answer, or that answered with
something this application cannot read.

It is a separate kind from a refusal because the two need different words: a
refusal has an explanation worth showing somebody, and an unreachable
installation has none.
*/
type Unreachable struct {
	cause error
}

func (unreachable *Unreachable) Error() string {
	return fmt.Sprintf("convia could not be reached: %v", unreachable.cause)
}

func (unreachable *Unreachable) Unwrap() error { return unreachable.cause }

/*
Client is one installation, held for as long as the application is signed in to
it.

The session lives here and nowhere else in the process. It is set when somebody
signs in and cleared when they sign out, under a mutex because the window and
the stream read it from different goroutines.
*/
type Client struct {
	address string
	http    *http.Client

	mutex   sync.RWMutex
	session string
}

// New returns a client for the installation at address, holding no session yet.
func New(address string, transport *http.Client) (*Client, error) {
	normalized, err := Address(address)
	if err != nil {
		return nil, err
	}

	if transport == nil {
		transport = &http.Client{Timeout: requestTimeout}
	}

	return &Client{address: normalized, http: transport}, nil
}

/*
Address is what somebody typed, read as the address of an installation.

A person types `convia.example`, and what they mean is `https://convia.example`.
Plain HTTP is kept for a development instance on this machine and refused
anywhere else: a session travelling in a header over plain HTTP is a session
anybody on the network holds.
*/
func Address(typed string) (string, error) {
	trimmed := strings.TrimSpace(typed)
	if trimmed == "" {
		return "", errors.New("an installation's address is needed")
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("read the address: %w", err)
	}

	if parsed.Host == "" {
		return "", errors.New("the address names no host")
	}

	if parsed.Scheme != "https" && !local(parsed.Hostname()) {
		return "", errors.New("an installation other than one on this machine must be reached over HTTPS")
	}

	return strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/"), nil
}

// local reports the machine this application is running on.
func local(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Address is the installation this client talks to.
func (client *Client) Address() string { return client.address }

// Session is what the application holds, and what it saves where the operating
// system keeps secrets.
func (client *Client) Session() string {
	client.mutex.RLock()
	defer client.mutex.RUnlock()
	return client.session
}

// Resume makes the client hold a session saved earlier, without signing in again.
func (client *Client) Resume(session string) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.session = session
}

// Forget drops the session this client holds. It makes no request: ending the
// session at the installation is SignOut.
func (client *Client) Forget() {
	client.Resume("")
}

/*
Do performs one request against the installation and decodes what it answered.

The session is attached when there is one, and is attached as a header rather
than as a cookie — see docs/adr/0019. A `204` leaves out untouched.
*/
func (client *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		rendered, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("render the request: %w", err)
		}
		body = bytes.NewReader(rendered)
	}

	request, err := http.NewRequestWithContext(ctx, method, client.address+prefix+path, body)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if session := client.Session(); session != "" {
		request.Header.Set("Authorization", "Bearer "+session)
	}

	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Unreachable{cause: err}
	}
	defer func() { _ = response.Body.Close() }()

	return read(response, out)
}

// read turns what the installation answered into a value or into an error of this package's two kinds.
func read(response *http.Response, out any) error {
	if response.StatusCode == http.StatusNoContent {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return &Unreachable{cause: err}
	}

	if response.StatusCode >= http.StatusBadRequest {
		return refusalFrom(response.StatusCode, body)
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return &Unreachable{cause: fmt.Errorf("read what convia answered: %w", err)}
	}

	return nil
}

/*
refusalFrom reads Convia's error body, and invents nothing.

A body that is not one — a proxy's, a gateway's — is reported with no code, so
that nothing branching on codes ever matches something the installation did not
say.
*/
func refusalFrom(status int, body []byte) error {
	var answered struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(body, &answered); err != nil || answered.Error.Code == "" {
		return &Refusal{Status: status, Message: http.StatusText(status)}
	}
	return &Refusal{
		Status:    status,
		Code:      string(answered.Error.Code),
		Message:   answered.Error.Message,
		RequestID: answered.Error.RequestID,
	}
}
