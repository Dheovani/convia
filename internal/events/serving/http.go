package serving

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/sessions"

	"convia/internal/events"
)

const (
	/*
		heartbeat is how often Convia proves the connection is still there.

		A control stream can be silent for hours â€” a tenant with no calls
		running produces nothing â€” so silence cannot be read as failure. A ping
		is what separates the two, and it also keeps intermediaries from
		reclaiming a connection they believe is idle.
	*/
	heartbeat = 30 * time.Second

	/*
		heartbeatTimeout is how long a client has to answer a ping.

		Ten seconds is generous for a round trip and short enough that a
		machine that went away is noticed within one heartbeat rather than
		holding a place against the per-application ceiling.
	*/
	heartbeatTimeout = 10 * time.Second

	// writeTimeout bounds one event write, so a reader that stopped consuming
	// TCP cannot hold the serving goroutine indefinitely.
	writeTimeout = 10 * time.Second

	/*
		maxClientMessageBytes bounds what Convia will read from a client before
		closing the connection.

		The stream carries nothing upstream, so this is not a message size
		limit in any useful sense: it is the point at which Convia stops
		reading rather than buffering something it is going to refuse anyway.
	*/
	maxClientMessageBytes = 1024

	/*
		retryAfterSeconds is what a client is told when a ceiling is reached.

		A place frees when one of the application's own streams ends, which
		Convia cannot predict, so this is a polite floor rather than a promise
		about when capacity returns.
	*/
	retryAfterSeconds = "5"

	/*
		recheckInterval is how often a person's stream asks again whether it
		should still be open, and which rooms it covers.

		A session is verified when a stream opens and a socket outlives any
		request, so without asking again a person who signed out everywhere â€”
		or was suspended â€” would keep receiving events on a connection opened
		before. Their rooms are read again at the same moment, which bounds
		what a membership event lost between instances can cost.

		A minute is the window both of those stay open for, and it is stated
		rather than hidden. Shorter multiplies a few indexed reads by every
		open tab; the events carry identifiers rather than anything said, and
		reading what they point at asks the database again, where membership
		is checked on every request.
	*/
	recheckInterval = time.Minute

	// recheckTimeout bounds one recheck, which runs on the goroutine that
	// writes events and would otherwise hold them back for as long as the
	// database took to answer.
	recheckTimeout = 10 * time.Second
)

/*
statusBehind tells a subscriber its view now has a gap in it.

The close codes RFC 6455 defines describe transport conditions, and this is not
one: the connection was fine and the subscriber was not. It is therefore in the
range the specification reserves for applications, and docs/events.md is where
it is published.
*/
const statusBehind websocket.StatusCode = 4000

/*
statusSessionEnded tells a person their stream closed because the session that
opened it no longer authenticates anybody.

It is its own code so that an interface can go straight to its sign-in form. A
reconnect would be refused before the upgrade, and a refused WebSocket handshake
reaches a script without its status, so this is the one moment the reason can
still be said.
*/
const statusSessionEnded websocket.StatusCode = 4001

/*
statusTooOld tells a subscriber the cursor it resumed from is further back than
Convia keeps events, so what it missed cannot be replayed. It re-reads over REST
and connects again without a cursor, as after falling behind.
*/
const statusTooOld websocket.StatusCode = 4002

// resumeParameter names the cursor a reconnecting client hands back.
const resumeParameter = "after"

/*
resumption reads the cursor a request resumes from, if it names one. A cursor
Convia did not write is refused before anything else, as a malformed request.
*/
func resumption(request *http.Request) (events.Cursor, bool, error) {
	text := request.URL.Query().Get(resumeParameter)
	if text == "" {
		return events.Cursor{}, false, nil
	}
	cursor, err := events.ParseCursor(text)
	if err != nil {
		return events.Cursor{}, false, err
	}
	return cursor, true, nil
}

/*
resume is how a stream catches up before it goes live.

The stream has already subscribed, so asking for the position afterwards leaves
no gap: everything up to the position is replayed from the journal, everything
after it arrives live, and anything that arrives both ways is written once.
*/
type resume struct {
	after    events.Cursor
	replayer events.Replayer
}

// run replays what the stream missed and returns the cursor live delivery continues from.
func (from *resume) run(ctx context.Context, connection *websocket.Conn, stream *events.Stream) (events.Cursor, error) {
	if from.replayer == nil {
		return events.Cursor{}, events.ErrCursorTooOld
	}

	until := from.replayer.Position()
	if !from.after.Before(until) {
		return from.after, nil
	}

	err := from.replayer.Replay(ctx, stream.ApplicationID(), from.after, until, func(event events.Event) error {
		if !stream.Replays(event) {
			return nil
		}
		writing, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		if err := wsjson.Write(writing, connection, stream.Outgoing(event)); err != nil {
			return err
		}
		stream.Replayed()
		return nil
	})
	return until, err
}

// TenantHandler serves an application its own live control events.
type TenantHandler struct {
	logger   *slog.Logger
	broker   *events.Broker
	replayer events.Replayer
}

// NewTenantHandler serves streams that can resume from replayer, which may be nil when nothing is recorded.
func NewTenantHandler(logger *slog.Logger, broker *events.Broker, replayer events.Replayer) *TenantHandler {
	return &TenantHandler{logger: logger, broker: broker, replayer: replayer}
}

/*
Stream upgrades the request and delivers events until one side stops.

Everything that decides what the subscriber may events.receive happens before the
upgrade, while the exchange is still an ordinary HTTP request that can be
refused with an ordinary JSON error. Once the connection is a socket there is
no way left to refuse anything, so nothing is left to decide there.
*/
func (handler *TenantHandler) Stream(response http.ResponseWriter, request *http.Request) {
	principal, found := credentials.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("authenticated route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.fail(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return
	}

	after, resuming, err := resumption(request)
	if err != nil {
		handler.fail(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"The after parameter is not a cursor Convia gave out."))
		return
	}

	stream, err := Authorize(handler.broker, principal).Subscribe()
	if err != nil {
		handler.refuse(response, request, principal, err)
		return
	}
	defer stream.Close()

	var from *resume
	if resuming {
		from = &resume{after: after, replayer: handler.replayer}
	}

	connection, ok := accept(handler.logger, response, request, "application_id", principal.ApplicationID)
	if !ok {
		return
	}
	defer connection.CloseNow()

	handler.logger.Info("event stream opened",
		"application_id", principal.ApplicationID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"active_streams", handler.broker.Active(),
	)

	status, reason := deliver(request.Context(), connection, stream, nil, from)

	handler.logger.Info("event stream closed",
		"application_id", principal.ApplicationID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"close_status", int(status),
		"close_reason", reason,
		"delivered", stream.Delivered(),
	)
}

/*
accept upgrades a request whose subscription has already been decided.

Nothing clears the server's read and write deadlines here, and nothing needs
to: net/http clears them itself when a handler hijacks the connection. What
bounds a stream instead is the deadline on each individual write, plus the
heartbeat â€” both of which bound an operation rather than the connection, which
is the difference between a stream that can last hours and one the server
closes after thirty seconds. A test pins that, because it is a property of the
standard library that this package depends on rather than one it enforces.
*/
func accept(
	logger *slog.Logger,
	response http.ResponseWriter,
	request *http.Request,
	who ...any,
) (*websocket.Conn, bool) {
	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		/*
			Compression buys little on a stream of identifiers and enumerations,
			and a compressor shared between attacker-influenced and secret
			content is a hazard nobody needs to take on for that.
		*/
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		/*
			Accept has already answered the client. What is left is to say why
			for an operator, because a refused upgrade is otherwise invisible:
			a wrong Origin and a missing Upgrade header look identical from
			outside.
		*/
		logger.Info("event stream upgrade refused", append([]any{
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		}, who...)...)
		return nil, false
	}

	connection.SetReadLimit(maxClientMessageBytes)
	return connection, true
}

/*
recheck is work a stream repeats while it is open.

It returns a close status and true when the stream must end, and false to carry
on. A stream without one runs for as long as its subscriber reads.
*/
type recheck struct {
	every time.Duration
	run   func(ctx context.Context) (websocket.StatusCode, string, bool)
}

/*
deliver writes events until one side stops, and reports how it ended.

One goroutine reads and one writes, which is what the protocol requires: pongs
arrive as ordinary frames and are only processed by a reader, so a stream with
nobody reading would answer a heartbeat it had itself sent. The reader is owned
by this function, is bounded by the connection, and is waited for before
returning.
*/
func deliver(
	ctx context.Context,
	connection *websocket.Conn,
	stream *events.Stream,
	again *recheck,
	from *resume,
) (websocket.StatusCode, string) {
	listening, stopListening := context.WithCancel(ctx)
	defer stopListening()

	silent := make(chan struct{})
	go func() {
		defer close(silent)
		defer stopListening()
		refuseClientMessages(listening, connection)
	}()

	status, reason := pump(listening, connection, stream, again, from)

	/*
		The close frame goes first and the reader is waited for second. Closing
		is what makes the pending Read return, so waiting first would be
		waiting for something this function is holding up â€” the same ordering
		mistake as releasing a resource before the thing using it has stopped.
	*/
	_ = connection.Close(status, reason)
	<-silent

	return status, reason
}

/*
pump is the writing half: events out, heartbeats out, nothing in.

The heartbeat is sent from here rather than from its own goroutine so that
writing stays single-threaded, and a client that stops answering is noticed as
part of the same loop that would have delivered its events. A recheck runs here
for the same reason.
*/
func pump(
	ctx context.Context,
	connection *websocket.Conn,
	stream *events.Stream,
	again *recheck,
	from *resume,
) (websocket.StatusCode, string) {
	var last events.Cursor
	if from != nil {
		var err error
		last, err = from.run(ctx, connection, stream)
		if errors.Is(err, events.ErrCursorTooOld) {
			return statusTooOld, "the cursor is older than the events Convia keeps"
		}
		if err != nil {
			return closeStatusFor(err), "the missed events could not be delivered"
		}
	}

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	// A nil channel is never ready, which is how a stream with nothing to
	// recheck leaves that case out of the select below.
	var rechecks <-chan time.Time
	if again != nil {
		rechecking := time.NewTicker(again.every)
		defer rechecking.Stop()
		rechecks = rechecking.C
	}

	for {
		select {
		case <-ctx.Done():
			return websocket.StatusNormalClosure, "the connection ended"

		case <-stream.Done():
			return closeFor(stream.Ending())

		case event := <-stream.Events():
			// Already written by the replay.
			if cursor, recorded := events.CursorOf(event); recorded && !last.Before(cursor) {
				continue
			}

			writing, cancel := context.WithTimeout(ctx, writeTimeout)
			err := wsjson.Write(writing, connection, stream.Outgoing(event))
			cancel()

			if err != nil {
				return closeStatusFor(err), "the event could not be delivered"
			}

		case <-ticker.C:
			pinging, cancel := context.WithTimeout(ctx, heartbeatTimeout)
			err := connection.Ping(pinging)
			cancel()

			if err != nil {
				return closeStatusFor(err), "the connection stopped answering"
			}

		case <-rechecks:
			if status, reason, ended := again.run(ctx); ended {
				return status, reason
			}
		}
	}
}

/*
closeFor says what a subscriber is told when its stream ends.

Each ending means something different to whoever is reading, and the close
frame is the only place left to say it: one is orderly, one says to reconnect
somewhere else, and one says the subscriber's picture now has a gap that
reconnecting will not fill.
*/
func closeFor(ending events.Ending) (websocket.StatusCode, string) {
	switch ending {
	case events.EndedBehind:
		return statusBehind, "events were dropped because this stream fell behind"
	case events.EndedByShutdown:
		return websocket.StatusGoingAway, "this instance is shutting down"
	default:
		return websocket.StatusNormalClosure, "the stream was closed"
	}
}

/*
refuseClientMessages reads so that pongs are processed, and closes the
connection if a client sends anything at all.

This is M14-010 made structural rather than advisory. The stream is one
direction: Convia never interprets a client message, so there is no message a
client could send that would carry media, and none that could widen what the
stream delivers. Refusing outright rather than discarding silently is the
difference between a client learning its protocol is wrong and a client
believing Convia received something.
*/
func refuseClientMessages(ctx context.Context, connection *websocket.Conn) {
	/*
		One read is enough, and not because one message is tolerated. Pings,
		pongs, and close frames are handled inside Read without returning, so
		this call blocks until either a client sends something it should not or
		the connection ends. Either way there is nothing left to read for.
	*/
	if _, _, err := connection.Read(ctx); err != nil {
		return
	}

	_ = connection.Close(websocket.StatusUnsupportedData,
		"this stream carries events to the client and accepts nothing from it")
}

/*
closeStatusFor maps a write or heartbeat failure onto a close status.

A connection that has already gone needs no status at all â€” the frame has
nowhere to arrive â€” so what this really decides is what an operator reads in
the summary, and what a still-present client is told when the fault was
Convia's.
*/
func closeStatusFor(err error) websocket.StatusCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return websocket.StatusPolicyViolation
	}
	return websocket.StatusInternalError
}

/*
refuse answers a subscription that was not opened, before any upgrade.

A ceiling is the one refusal an operator needs to see, because it is the one
that is about this instance rather than about the caller's key.
*/
func (handler *TenantHandler) refuse(response http.ResponseWriter, request *http.Request,
	principal credentials.Principal, err error) {

	switch {
	case errors.Is(err, ErrForbidden):
		handler.fail(response, request, api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The credential does not carry the scopes an event stream requires."))

	case errors.Is(err, events.ErrTooManyStreams):
		handler.logger.Warn("event stream refused because a ceiling was reached",
			"application_id", principal.ApplicationID,
			"active_streams", handler.broker.Active(),
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		response.Header().Set("Retry-After", retryAfterSeconds)
		handler.fail(response, request, api.NewFailure(http.StatusTooManyRequests, api.CodeRateLimited,
			"Too many event streams are open. Close one or retry later."))

	case errors.Is(err, events.ErrStopped):
		handler.fail(response, request, api.NewFailure(http.StatusServiceUnavailable,
			api.CodeUnavailable, "This instance is shutting down and is not accepting event streams."))

	default:
		handler.logger.Error("open event stream",
			"error", err,
			"application_id", principal.ApplicationID,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.fail(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}

func (handler *TenantHandler) fail(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write event stream failure",
			"error", err, "request_id", api.RequestIDFromContext(request.Context()))
	}
}

/*
sessionAuthenticator is how a person's stream asks whether its session still
authenticates anybody. sessions.Service satisfies it.
*/
type sessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (sessions.Principal, error)
}

/*
memberships is how a person's stream learns which rooms it covers.
rooms.Service satisfies it.

It is declared here and satisfied from outside, which is what keeps this
package a leaf: the rooms domain imports this one to announce, so this one can
never import it.
*/
type memberships interface {
	RoomIDsOf(ctx context.Context, applicationID, userID string) ([]string, error)
}

/*
PersonHandler serves a signed-in person the events about the rooms they are in.

It is not the tenant's handler with a different verifier, and the difference is
the design. An application's stream is authorized once, for a whole tenant, by
scopes that do not change while it is open. A person's is authorized **per
room**, and which rooms is exactly the thing that changes while it is open:
somebody adds them, or they leave in another tab. So the rooms are read before
the upgrade, kept current by the membership events the stream itself carries,
and read again on an interval along with the session.
*/
type PersonHandler struct {
	logger   *slog.Logger
	broker   *events.Broker
	replayer events.Replayer
	sessions sessionAuthenticator
	rooms    memberships

	// every is how often the stream rechecks, which is recheckInterval outside
	// the tests that need to watch it happen.
	every time.Duration
}

func NewPersonHandler(
	logger *slog.Logger,
	broker *events.Broker,
	replayer events.Replayer,
	sessions sessionAuthenticator,
	rooms memberships,
) *PersonHandler {
	return &PersonHandler{logger: logger, broker: broker, replayer: replayer, sessions: sessions, rooms: rooms,
		every: recheckInterval}
}

/*
Stream upgrades the request and delivers the person's events until one side
stops.

What the stream covers is read before the upgrade, for the reason the tenant's
handler gives: a person whose rooms cannot be read is told so with a status
code, rather than handed a socket that would stay silent forever.
*/
func (handler *PersonHandler) Stream(response http.ResponseWriter, request *http.Request) {
	principal, found := sessions.PrincipalFromContext(request.Context())
	token, presented := sessions.Present(request)
	if !found || !presented {
		path := strings.ReplaceAll(strings.ReplaceAll(request.URL.Path, "\n", ""), "\r", "")
		handler.logger.Error("a session route was reached without a session",
			"method", request.Method,
			"path", path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.fail(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return
	}

	after, resuming, err := resumption(request)
	if err != nil {
		handler.fail(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"The after parameter is not a cursor Convia gave out."))
		return
	}

	stream, err := AsPerson(handler.broker, principal).Subscribe()
	if err != nil {
		handler.refuse(response, request, principal, err)
		return
	}
	defer stream.Close()

	if err := stream.Reconcile(handler.roomsOf(request.Context(), principal)); err != nil {
		handler.logger.Error("read the rooms a person's event stream covers",
			"error", err,
			"session_id", principal.SessionID,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.fail(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
		return
	}

	var from *resume
	if resuming {
		from = &resume{after: after, replayer: handler.replayer}
	}

	connection, ok := accept(handler.logger, response, request, "session_id", principal.SessionID)
	if !ok {
		return
	}
	defer connection.CloseNow()

	handler.logger.Info("person event stream opened",
		"session_id", principal.SessionID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"active_streams", handler.broker.Active(),
	)

	status, reason := deliver(request.Context(), connection, stream, &recheck{
		every: handler.every,
		run: func(ctx context.Context) (websocket.StatusCode, string, bool) {
			return handler.recheck(ctx, token, principal, stream)
		},
	}, from)

	handler.logger.Info("person event stream closed",
		"session_id", principal.SessionID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"close_status", int(status),
		"close_reason", reason,
		"delivered", stream.Delivered(),
	)
}

/*
recheck asks whether the stream should stay open, and which rooms it covers.

A session that no longer authenticates ends the stream. A check that could not
be made does not: the database being briefly unreachable says nothing about the
person, and closing every stream on an instance because of it would send every
open tab to reconnect at once, into the same outage.

Authenticating again also counts as using the session, exactly as a request
does. An open tab keeps its session from going idle, which is what the tab
polling every few seconds already did.
*/
func (handler *PersonHandler) recheck(
	ctx context.Context,
	token string,
	principal sessions.Principal,
	stream *events.Stream,
) (websocket.StatusCode, string, bool) {
	checking, cancel := context.WithTimeout(ctx, recheckTimeout)
	defer cancel()

	_, err := handler.sessions.Authenticate(checking, token)
	if errors.Is(err, sessions.ErrUnauthenticated) {
		return statusSessionEnded, "the session this stream was opened with has ended", true
	}
	if err != nil {
		handler.logger.Warn("a person's event stream could not recheck its session",
			"error", err, "session_id", principal.SessionID)
		return 0, "", false
	}

	if err := stream.Reconcile(handler.roomsOf(checking, principal)); err != nil {
		handler.logger.Warn("a person's event stream could not read their rooms again",
			"error", err, "session_id", principal.SessionID)
	}
	return 0, "", false
}

func (handler *PersonHandler) roomsOf(ctx context.Context, principal sessions.Principal) func() ([]string, error) {
	return func() ([]string, error) {
		return handler.rooms.RoomIDsOf(ctx, principal.ApplicationID, principal.UserID)
	}
}

// refuse answers a person's subscription that was not opened, before any
// upgrade. See TenantHandler.refuse.
func (handler *PersonHandler) refuse(
	response http.ResponseWriter,
	request *http.Request,
	principal sessions.Principal,
	err error,
) {
	switch {
	case errors.Is(err, events.ErrTooManyStreams):
		handler.logger.Warn("person event stream refused because a ceiling was reached",
			"session_id", principal.SessionID,
			"active_streams", handler.broker.Active(),
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		response.Header().Set("Retry-After", retryAfterSeconds)
		handler.fail(response, request, api.NewFailure(http.StatusTooManyRequests, api.CodeRateLimited,
			"Too many event streams are open. Close one or retry later."))

	case errors.Is(err, events.ErrStopped):
		handler.fail(response, request, api.NewFailure(http.StatusServiceUnavailable,
			api.CodeUnavailable, "This instance is shutting down and is not accepting event streams."))

	default:
		handler.logger.Error("open person event stream",
			"error", err,
			"session_id", principal.SessionID,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.fail(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}

/*
fail answers privately, like every refusal on the session surface: whether a
stream opened depends on the cookie, and a cache that kept the answer would
serve it to somebody else.
*/
func (handler *PersonHandler) fail(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write person event stream failure",
			"error", err, "request_id", api.RequestIDFromContext(request.Context()))
	}
}
