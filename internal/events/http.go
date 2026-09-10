package events

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/api"
	"convia/internal/credentials"
)

const (
	/*
		heartbeat is how often Convia proves the connection is still there.

		A control stream can be silent for hours — a tenant with no calls
		running produces nothing — so silence cannot be read as failure. A ping
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
)

/*
statusBehind tells a subscriber its view now has a gap in it.

The close codes RFC 6455 defines describe transport conditions, and this is not
one: the connection was fine and the subscriber was not. It is therefore in the
range the specification reserves for applications, and docs/events.md is where
it is published.
*/
const statusBehind websocket.StatusCode = 4000

// TenantHandler serves an application its own live control events.
type TenantHandler struct {
	logger *slog.Logger
	broker *Broker
}

func NewTenantHandler(logger *slog.Logger, broker *Broker) *TenantHandler {
	return &TenantHandler{logger: logger, broker: broker}
}

/*
Stream upgrades the request and delivers events until one side stops.

Everything that decides what the subscriber may receive happens before the
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

	stream, err := Authorize(handler.broker, principal).Subscribe()
	if err != nil {
		handler.refuse(response, request, principal, err)
		return
	}
	defer stream.Close()

	/*
		Nothing clears the server's read and write deadlines here, and nothing
		needs to: net/http clears them itself when a handler hijacks the
		connection. What bounds a stream instead is the deadline on each
		individual write below, plus the heartbeat — both of which bound an
		operation rather than the connection, which is the difference between a
		stream that can last hours and one the server closes after thirty
		seconds. A test pins that, because it is a property of the standard
		library that this package depends on rather than one it enforces.
	*/
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
		handler.logger.Info("event stream upgrade refused",
			"error", err,
			"application_id", principal.ApplicationID,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		return
	}
	defer connection.CloseNow()

	connection.SetReadLimit(maxClientMessageBytes)

	handler.logger.Info("event stream opened",
		"application_id", principal.ApplicationID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"active_streams", handler.broker.Active(),
	)

	status, reason := handler.deliver(request.Context(), connection, stream)

	handler.logger.Info("event stream closed",
		"application_id", principal.ApplicationID,
		"request_id", api.RequestIDFromContext(request.Context()),
		"close_status", int(status),
		"close_reason", reason,
		"delivered", stream.Delivered(),
	)
}

/*
deliver writes events until one side stops, and reports how it ended.

One goroutine reads and one writes, which is what the protocol requires: pongs
arrive as ordinary frames and are only processed by a reader, so a stream with
nobody reading would answer a heartbeat it had itself sent. The reader is owned
by this function, is bounded by the connection, and is waited for before
returning.
*/
func (handler *TenantHandler) deliver(ctx context.Context, connection *websocket.Conn,
	stream *Stream) (websocket.StatusCode, string) {

	listening, stopListening := context.WithCancel(ctx)
	defer stopListening()

	silent := make(chan struct{})
	go func() {
		defer close(silent)
		defer stopListening()
		refuseClientMessages(listening, connection)
	}()

	status, reason := handler.pump(listening, connection, stream)

	/*
		The close frame goes first and the reader is waited for second. Closing
		is what makes the pending Read return, so waiting first would be
		waiting for something this function is holding up — the same ordering
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
part of the same loop that would have delivered its events.
*/
func (handler *TenantHandler) pump(ctx context.Context, connection *websocket.Conn,
	stream *Stream) (websocket.StatusCode, string) {

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return websocket.StatusNormalClosure, "the connection ended"

		case <-stream.Done():
			return closeFor(stream.Ending())

		case event := <-stream.Events():
			writing, cancel := context.WithTimeout(ctx, writeTimeout)
			err := wsjson.Write(writing, connection, event)
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
func closeFor(ending Ending) (websocket.StatusCode, string) {
	switch ending {
	case EndedBehind:
		return statusBehind, "events were dropped because this stream fell behind"
	case EndedByShutdown:
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

A connection that has already gone needs no status at all — the frame has
nowhere to arrive — so what this really decides is what an operator reads in
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

	case errors.Is(err, ErrTooManyStreams):
		handler.logger.Warn("event stream refused because a ceiling was reached",
			"application_id", principal.ApplicationID,
			"active_streams", handler.broker.Active(),
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		response.Header().Set("Retry-After", retryAfterSeconds)
		handler.fail(response, request, api.NewFailure(http.StatusTooManyRequests, api.CodeRateLimited,
			"Too many event streams are open. Close one or retry later."))

	case errors.Is(err, ErrStopped):
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
