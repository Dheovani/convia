package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/webhooks"
)

const (
	// clientTimeout bounds one request to another installation, end to end.
	clientTimeout = 10 * time.Second

	// maxResponseBytes bounds what another installation may answer with. It is
	// the same bound Convia applies to what it accepts.
	maxResponseBytes = api.MaxJSONRequestBytes
)

// targetPattern is a path on the peer surface, with an already encoded query.
var targetPattern = regexp.MustCompile(`^/v1/peer/[A-Za-z0-9_/]+(\?[A-Za-z0-9_.~%=&+-]*)?$`)

// Response is what another installation answered.
type Response struct {
	Status int
	// Body is a JSON object on a success, and whatever arrived otherwise.
	Body []byte
	// Code is the error code of a refusal, where the body carried one.
	Code string
}

/*
Client makes signed requests to other installations.

**An invitation link is an address a stranger chose**, and following it makes
this installation connect somewhere from inside whatever network it runs in. So
the client is built on the same guard webhook delivery uses: the address is
checked at the socket on every attempt, private and loopback addresses are
refused outside development, no proxy is consulted, and no redirect is
followed. See internal/webhooks.Destinations.
*/
type Client struct {
	http *http.Client
	/*
		streaming is the same client without a deadline on the whole exchange.

		A request has an end and a stream does not: the timeout that bounds one
		request would cut a connection meant to stay open for hours. Everything
		else is shared, the guard at the socket included, because an address
		somebody else chose is no safer for being connected to for longer.
	*/
	streaming *http.Client
	guard     webhooks.Destinations
	now       func() time.Time
}

func NewClient(guard webhooks.Destinations) *Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: guard.Control}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: clientTimeout,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       time.Minute,
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &Client{
		http: &http.Client{
			Timeout:       clientTimeout,
			Transport:     transport,
			CheckRedirect: noRedirect,
		},
		streaming: &http.Client{
			Transport:     transport,
			CheckRedirect: noRedirect,
		},
		guard: guard,
		now:   time.Now,
	}
}

/*
Do sends one signed request and reads the answer.

Anything that is not an answer from a Convia is [ErrUnreachable]: a connection
refused or refused by the guard, a redirect, a body too large, or a success that
is not a JSON object. The detail is in the wrapped error for the log and never
in what a person is shown.
*/
func (client *Client) Do(
	ctx context.Context,
	identity accounts.Identity,
	method,
	home,
	target string,
	body []byte,
) (Response, error) {
	/*
		Checked again here whatever the caller checked, because every request to
		another installation passes through this one place: the home is a bare scheme
		and host, and the target a path below /v1/peer/ with nothing in it that could
		name another host, another surface, or another header.
	*/
	if !homePattern.MatchString(home) {
		return Response{}, fmt.Errorf("%w: %q is not the address of an installation", ErrUnreachable, home)
	}
	if !targetPattern.MatchString(target) {
		return Response{}, fmt.Errorf("%w: %q is not a path on the peer surface", ErrUnreachable, target)
	}
	if err := client.guard.Permits(home); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	request, err := http.NewRequestWithContext(ctx, method, home+target, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%w: build the request: %v", ErrUnreachable, err)
	}
	request.Header.Set("Accept", api.ContentTypeJSON)
	if body != nil {
		request.Header.Set("Content-Type", api.ContentTypeJSON)
	}
	Sign(request, body, identity, client.now())

	response, err := client.http.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("%w: read the answer: %v", ErrUnreachable, err)
	}
	if len(payload) > maxResponseBytes {
		return Response{}, fmt.Errorf("%w: the answer is larger than %d bytes", ErrUnreachable, maxResponseBytes)
	}

	result := Response{Status: response.StatusCode}
	switch {
	case response.StatusCode == http.StatusNoContent:
		return result, nil
	case response.StatusCode >= 200 && response.StatusCode < 300:
		if !jsonObject(response.Header.Get("Content-Type"), payload) {
			return Response{}, fmt.Errorf("%w: the answer is not a JSON object", ErrUnreachable)
		}
		result.Body = payload
		return result, nil
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return Response{}, fmt.Errorf("%w: redirected, which is not followed", ErrUnreachable)
	}

	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &failure) == nil {
		result.Code = failure.Error.Code
	}
	return result, nil
}

func jsonObject(contentType string, payload []byte) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != api.ContentTypeJSON {
		return false
	}

	var object map[string]json.RawMessage
	return json.Unmarshal(payload, &object) == nil
}

/*
Stream opens a signed WebSocket to another installation and answers it.

The handshake is an ordinary signed request — the same canonical form, the same
headers, the same guard at the socket — so a home checks it exactly as it checks
any other. What differs afterwards is only that nothing is read back here: the
connection is the caller's, and closing it is theirs.

The signature covers the address as https or http names it rather than as ws
names it, because that is the authority the home compares against its own Host.
*/
func (client *Client) Stream(
	ctx context.Context,
	identity accounts.Identity,
	home, target string,
) (*websocket.Conn, error) {
	if !homePattern.MatchString(home) {
		return nil, fmt.Errorf("%w: %q is not the address of an installation", ErrUnreachable, home)
	}
	if !targetPattern.MatchString(target) {
		return nil, fmt.Errorf("%w: %q is not a path on the peer surface", ErrUnreachable, target)
	}
	if err := client.guard.Permits(home); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	/*
		Built as the request it is signed as, and dialled as the websocket URL
		it becomes. Signing the ws form would sign an authority no home ever
		compares against, so the two would never agree.
	*/
	signable, err := http.NewRequestWithContext(ctx, http.MethodGet, home+target, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build the handshake: %v", ErrUnreachable, err)
	}
	Sign(signable, nil, identity, client.now())

	connection, response, err := websocket.Dial(ctx, websocketURL(home)+target, &websocket.DialOptions{
		HTTPClient: client.streaming,
		HTTPHeader: signable.Header,
	})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("%w: the stream was not opened (%d): %v", ErrUnreachable, status, err)
	}
	return connection, nil
}

// websocketURL is a home as a WebSocket address. Only the scheme changes, so
// that the host and port the guard checked are the ones dialled.
func websocketURL(home string) string {
	if rest, found := strings.CutPrefix(home, "https://"); found {
		return "wss://" + rest
	}
	return "ws://" + strings.TrimPrefix(home, "http://")
}
