/*
Package livekit realizes Convia's media sessions on a LiveKit server.

It is the first implementation behind the boundary M11 established, and it is
the only package in Convia that knows a provider exists. Everything it deals in
— rooms, grants, signed tokens, the provider's wire format — stops here. What
crosses back out is media.Session, media.ErrUnavailable, and media.ErrRejected,
which are Convia's own.

**It speaks the provider's HTTP API directly rather than through its Go SDK.**
That is a deliberate decision with a cost and a reason; see
docs/adr/0002-livekit-over-http-rather-than-its-go-sdk.md. In short: Convia
needs two calls and a signed token, and the SDK would link a complete WebRTC
implementation into a control plane that must never transport media.

See docs/adr/0001-control-plane-media-plane-boundary.md for the boundary this
sits behind, and docs/media.md for running one locally.
*/
package livekit

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
	"time"

	"convia/internal/media"
)

const (
	/*
		roomEmptyTimeout is how long the provider keeps a room nobody is in.

		Convia deletes a room when its call ends, so this is not the ordinary
		path — it is the provider's own garbage collection, and it exists to
		bound the one leak ADR 0001 named as a known cost: a session Convia
		failed to release stays until something else reclaims it.

		It has to be comfortably longer than the gap between a call starting
		and the first participant arriving, because the room is created before
		anyone joins. Ten minutes is far more than that gap ever is, and a room
		reclaimed early is not a failure anyway: the provider recreates it
		under the same name when someone connects.
	*/
	roomEmptyTimeout = 10 * time.Minute

	/*
		maxErrorDetail bounds how much of a provider's error message is kept.

		The message is written by the provider and ends up in Convia's logs, so
		it is bounded rather than trusted to be short.
	*/
	maxErrorDetail = 200

	// twirpPrefix is the path LiveKit serves its room service on.
	twirpPrefix = "/twirp/livekit.RoomService/"
)

/*
Config is what an operator has to supply for Convia to reach a LiveKit server.

It is validated by internal/config before it gets here. New validates it again,
because a constructor that trusts its arguments is one refactor away from
building an adapter that cannot work and only says so at the first call.
*/
type Config struct {
	URL       string
	APIKey    string
	APISecret media.APISecret
	Timeout   time.Duration

	/*
		ClientURL is where a browser reaches the media plane, when that is not
		where Convia reaches it.

		The two are routinely different and the difference is not cosmetic. A
		deployment commonly reaches its media server on a private address that
		no client can resolve, so publishing the address Convia uses would hand
		every client something that cannot work. Empty means the two are the
		same, which is the local case.
	*/
	ClientURL string
}

/*
Plane realizes media sessions on a LiveKit server.

It satisfies the interface internal/calls declares, and holds no state beyond
its configuration and an HTTP client: a session is identified entirely by the
room name Convia derives from the call, so nothing has to be remembered between
calls.
*/
type Plane struct {
	endpoint  string
	clientURL string
	apiKey    string
	apiSecret media.APISecret
	client    *http.Client
	now       func() time.Time
}

/*
New builds an adapter for one LiveKit server.

A configuration error is returned rather than tolerated. Convia can run with no
media plane at all — that is what media.Absent is for — so a half-configured
one is never the intended state, and starting with it would turn a typo into a
failure that first appears when somebody tries to start a call.
*/
func New(config Config) (*Plane, error) {
	endpoint, err := normalizeEndpoint(config.URL)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("the media API key must not be empty")
	}

	if config.APISecret.Empty() {
		return nil, errors.New("the media API secret must not be empty")
	}

	if config.Timeout <= 0 {
		return nil, errors.New("the media timeout must be positive")
	}

	clientURL, err := clientEndpoint(config.ClientURL, endpoint)
	if err != nil {
		return nil, err
	}

	return &Plane{
		endpoint:  endpoint,
		clientURL: clientURL,
		apiKey:    config.APIKey,
		apiSecret: config.APISecret,
		client:    &http.Client{Timeout: config.Timeout},
		now:       time.Now,
	}, nil
}

/*
clientEndpoint decides the address a client is told to connect to.

Clients speak WebSocket where Convia speaks HTTP, against the same server, so
an unset value is derived by swapping the scheme rather than requiring an
operator to write the same host twice and keep the two in step. A configured
value is taken as given, because the case it exists for is precisely the one
where the client's address is not a transformation of Convia's.
*/
func clientEndpoint(configured, apiEndpoint string) (string, error) {
	if strings.TrimSpace(configured) == "" {
		return strings.Replace(apiEndpoint, "http", "ws", 1), nil
	}

	parsed, err := url.Parse(strings.TrimSpace(configured))
	if err != nil {
		return "", errors.New("the media client URL is not a valid URL")
	}

	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return "", errors.New("the media client URL must use the ws or wss scheme")
	}

	if parsed.Host == "" {
		return "", errors.New("the media client URL must include a host")
	}

	return strings.TrimSuffix(parsed.String(), "/"), nil
}

// normalizeEndpoint validates the server URL and strips any trailing slash.
func normalizeEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		// The URL is not echoed: an operator pasted it, and it may carry more
		// than a host.
		return "", errors.New("the media URL is not a valid URL")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("the media URL must use the http or https scheme")
	}

	if parsed.Host == "" {
		return "", errors.New("the media URL must include a host")
	}

	return strings.TrimSuffix(parsed.String(), "/"), nil
}

/*
roomName is the provider room a Convia call happens in.

The call identifier is used as it stands. It is already unique and already
carries its own "call_" prefix, so it needs no decoration to be unambiguous on
the provider side, and a name derived by a rule rather than stored is one a
future reconciliation can recompute without a lookup.

The mapping is one call to one room. Convia never reuses a room across calls,
because a room that outlived its call would let a stale credential reach a
conversation it was never issued for.
*/
func roomName(callID string) string {
	return callID
}

/*
OpenSession creates the room a call happens in.

The room is created eagerly, before anyone joins, even though LiveKit would
create it on the first connection anyway. That is the whole point: M11 decided
that a call whose session cannot be realized is ended rather than left holding
its room, and that decision only means anything if Convia actually talks to the
provider while the caller is still waiting. Creating lazily would move the
first sign of a broken media plane to a participant failing to connect, long
after Convia answered that the call had started.

Room capacity is deliberately not sent. ADR 0001 left open whether the media
plane should enforce it too; it should not. Convia's capacity can be changed
while a call is running, and a number copied to the provider at creation would
then be stale in the restrictive direction — a participant Convia admitted
would be refused by the provider, and Convia would have no way to explain it.
*/
func (plane *Plane) OpenSession(ctx context.Context, request media.SessionRequest) (media.Session, error) {
	name := roomName(request.CallID)

	var room struct {
		Name string `json:"name"`
	}

	body := map[string]any{
		"name": name,
		/*
			The canonical protobuf JSON spelling is lowerCamelCase. Twirp
			accepts the underscore form too, but a field it did not understand
			would be dropped silently, and the room would quietly take the
			provider's default lifetime instead of this one.
		*/
		"emptyTimeout": int(roomEmptyTimeout.Seconds()),
	}

	if err := plane.call(ctx, "CreateRoom", roomLifecycle, body, &room); err != nil {
		return media.Session{}, err
	}

	/*
		The provider's own name for the room is what gets stored, falling back
		to the requested one. Convia never interprets the reference, so the
		only thing that matters is that it names the room that now exists.
	*/
	if room.Name != "" {
		name = room.Name
	}

	return media.Session{Reference: name}, nil
}

/*
CloseSession deletes the room a call was held in.

A room the provider no longer has is a success, not a failure. Convia asks it
to reclaim empty rooms on its own, so "already gone" is an outcome this adapter
arranged, and reporting it as an error would put a misleading line in the log
every time that worked as intended.
*/
func (plane *Plane) CloseSession(ctx context.Context, session media.Session) error {
	if !session.Realized() {
		return nil
	}

	body := map[string]any{"room": session.Reference}

	err := plane.call(ctx, "DeleteRoom", roomLifecycle, body, nil)
	if errors.Is(err, errRoomGone) {
		return nil
	}

	return err
}

/*
IssueCredential mints the credential one admitted person connects with.

**No request is made to the provider.** A LiveKit access token is signed
locally and verified by the server when the holder connects, so this operation
cannot time out, cannot find the provider unreachable, and never returns
ErrUnavailable. That is worth knowing at the boundary: admitting somebody stays
possible during an outage that would prevent starting a call, and it fails only
for reasons an operator has to fix.

The credential is bound to one room and one identity. It carries no
administrative permission, so a client holding it can take part in exactly the
conversation Convia admitted it to and can do nothing else — see admissionTo.
*/
func (plane *Plane) IssueCredential(_ context.Context, admission media.Admission) (media.Credential, error) {
	switch {
	case !admission.Session.Realized():
		/*
			There is no room to admit anyone to. It happens when a call was
			started while this deployment had no media plane and one was
			configured afterwards, and it is terminal: the conversation this
			call refers to does not exist on the provider, and no retry
			creates it.
		*/
		return media.Credential{}, fmt.Errorf("the call has no media session: %w", media.ErrRejected)
	case admission.ParticipantID == "":
		return media.Credential{}, fmt.Errorf("an admission needs an identity: %w", media.ErrRejected)
	case admission.Lifetime <= 0:
		return media.Credential{}, fmt.Errorf("an admission needs a positive lifetime: %w", media.ErrRejected)
	}

	token, expiresAt, err := plane.mint(
		admission.ParticipantID,
		admissionTo(admission.Session.Reference),
		admission.Lifetime,
	)

	if err != nil {
		return media.Credential{}, err
	}

	return media.Credential{
		URL:       plane.clientURL,
		Token:     media.Token(token),
		ExpiresAt: expiresAt,
	}, nil
}

/*
errRoomGone reports a room the provider does not have.

It is internal, because the only caller that can meaningfully encounter it
treats it as success. It still wraps a terminal failure so that escaping is
safe: were a create ever answered this way, it would be reported as the
misconfiguration it would have to be rather than retried forever.
*/
var errRoomGone = fmt.Errorf("the room no longer exists: %w", media.ErrRejected)

/*
call performs one room service request.

Every request is a POST carrying JSON and a token minted for that request
alone. The token is a bearer credential and appears nowhere but the header.
*/
func (plane *Plane) call(ctx context.Context, method string, permission grant, body, into any) error {
	token, _, err := plane.mint("", permission, apiTokenLifetime)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s: encode the request: %w", method, media.ErrRejected)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		plane.endpoint+twirpPrefix+method, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%s: build the request: %w", method, media.ErrRejected)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := plane.client.Do(request)
	if err != nil {
		/*
			A transport failure is retryable by definition: the request never
			got an answer, so nothing is known about whether the provider would
			have refused it. The underlying error is wrapped for the operator
			log, and carries a URL but never the token, which is a header.
		*/
		return fmt.Errorf("%s: %v: %w", method, err, media.ErrUnavailable)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %w", method, failure(response))
	}

	if into == nil {
		return nil
	}

	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		/*
			An answer that cannot be read is not a refusal. The provider
			accepted the request, so the room may well exist; treating it as
			retryable is the reading that does not lose one.
		*/
		return fmt.Errorf("%s: decode the response: %w", method, media.ErrUnavailable)
	}

	return nil
}

/*
failure turns a non-200 answer into the Convia error it means.

The split is the one ADR 0001 defined, and the HTTP status is what decides it.
Twirp maps its own codes onto statuses, so retryability never has to be read
out of the code string: statuses the server produced itself — it is overloaded,
restarting, behind a proxy that could not reach it — are retryable, and
statuses that describe the request are not. A rejected token or a malformed
call will be rejected exactly the same way next time, and an operator has to
act.

One outcome does need the code, and it is not about retrying. A missing room
and an unknown method both answer 404, and only the code tells them apart.
Reading the second as the first would turn a wrong endpoint path — a real
misconfiguration — into a silent success.
*/
func failure(response *http.Response) error {
	code, message := twirpError(response.Body)

	switch {
	case code == "not_found":
		return errRoomGone
	case response.StatusCode >= 500,
		response.StatusCode == http.StatusTooManyRequests,
		response.StatusCode == http.StatusRequestTimeout:
		return fmt.Errorf("livekit answered %d (%s): %w", response.StatusCode, message, media.ErrUnavailable)
	default:
		return fmt.Errorf("livekit answered %d (%s): %w", response.StatusCode, message, media.ErrRejected)
	}
}

/*
twirpError reads the code and message out of an error body.

A body that is missing, truncated, or not the shape Twirp documents is not
itself an error worth reporting: the status already decided the outcome, and
this only enriches the log line.
*/
func twirpError(body io.Reader) (code, detail string) {
	var reported struct {
		Code    string `json:"code"`
		Message string `json:"msg"`
	}

	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || json.Unmarshal(raw, &reported) != nil {
		return "", "no readable detail"
	}

	detail = strings.TrimSpace(reported.Code + " " + strings.TrimSpace(reported.Message))
	if length := len([]rune(detail)); length > maxErrorDetail {
		detail = string([]rune(detail)[:maxErrorDetail]) + "…"
	}

	if detail == "" {
		detail = "no readable detail"
	}

	return reported.Code, detail
}
