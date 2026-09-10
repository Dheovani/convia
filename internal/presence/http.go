package presence

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own presence over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot read or assert another application's
people even by mistake: there is no request field that could name one.
*/
type TenantHandler struct {
	logger  *slog.Logger
	service service
}

func NewTenantHandler(logger *slog.Logger, service service) *TenantHandler {
	return &TenantHandler{logger: logger, service: service}
}

/*
assertRequest is what a device says about a person.

There is no expiry and no timestamp, deliberately: a caller states how long its
claim should stand and Convia decides when that is. A field carrying an
absolute time would be a field a client with a wrong clock could get wrong, and
two instances could then disagree about when it lapsed.
*/
type assertRequest struct {
	State           string `json:"state"`
	LifetimeSeconds int    `json:"lifetime_seconds,omitempty"`
}

/*
presenceResponse is what Convia will say about one person.

There is no device list and no device count. The question presence answers is
whether somebody is available; how many screens they have open is a detail
about their day that nothing here needs to publish.
*/
type presenceResponse struct {
	UserID    string  `json:"user_id"`
	State     string  `json:"state"`
	Since     *string `json:"since,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// listResponse is a page of presence, in the order the caller asked for it.
type listResponse struct {
	Data []presenceResponse `json:"data"`
}

/*
authorized binds the request's verified identity to the service.

A request that reaches here without a principal was routed without the
authentication middleware, which is a wiring mistake rather than a client
error. It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *TenantHandler) authorized(response http.ResponseWriter, request *http.Request) (*Authorized, bool) {
	principal, found := credentials.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("authenticated route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
			"The request did not carry a usable credential."))
		return nil, false
	}
	return Authorize(handler.service, principal), true
}

/*
Assert records or refreshes what one device says about one person.

It is a PUT because that is what a heartbeat is: the same request repeated
leaves the same state and moves the deadline. The device is in the path rather
than the body so that the thing being replaced is the thing being addressed.
*/
func (handler *TenantHandler) Assert(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body assertRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	deviceID, err := NormalizeDeviceID(request.PathValue("device_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	state, err := ParseState(body.State)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	lifetime, err := NormalizeLifetime(time.Duration(body.LifetimeSeconds) * time.Second)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	presence, err := authorized.Assert(request.Context(), request.PathValue("user_id"),
		Assertion{DeviceID: deviceID, State: state, Lifetime: lifetime})
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(presence))
}

/*
Withdraw removes what one device said.

The person's presence as it now stands is returned rather than nothing,
because closing one tab does not usually take somebody offline and a client
that assumed it did would be wrong. What it needs to know is what the other
devices still say.
*/
func (handler *TenantHandler) Withdraw(response http.ResponseWriter, request *http.Request) {
	handler.clear(response, request, request.PathValue("device_id"))
}

/*
Forget removes everything said about a person.

It is how an application reports a sign-out, as opposed to one device going
away. It succeeds when nothing was being said, because a client retrying its
own tidying-up must not be told it failed.
*/
func (handler *TenantHandler) Forget(response http.ResponseWriter, request *http.Request) {
	handler.clear(response, request, "")
}

func (handler *TenantHandler) clear(response http.ResponseWriter, request *http.Request, deviceID string) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if deviceID != "" {
		normalized, err := NormalizeDeviceID(deviceID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		deviceID = normalized
	}

	presence, err := authorized.Clear(request.Context(), request.PathValue("user_id"), deviceID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(presence))
}

// Get reports what Convia will say about one person.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	presence, err := authorized.Get(request.Context(), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(presence))
}

/*
List reports what Convia will say about several people.

The users are named in repeated query parameters rather than a request body,
because this is a read and a read should be expressible as a URL: it can be
cached, logged, and retried without a client having to remember that one of its
GETs is unusual.
*/
func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	presences, err := authorized.GetMany(request.Context(), request.URL.Query()["user_id"])
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := listResponse{Data: make([]presenceResponse, 0, len(presences))}
	for _, presence := range presences {
		body.Data = append(body.Data, represent(presence))
	}

	handler.write(response, request, http.StatusOK, body)
}

/*
represent renders presence for the wire.

Both timestamps are omitted when somebody is offline rather than sent as zero
values. There is no moment at which an absence began — nobody is saying
anything, and Convia deliberately does not remember when they stopped — and
nothing is pending, so there is nothing to expire.
*/
func represent(presence Presence) presenceResponse {
	body := presenceResponse{UserID: presence.UserID, State: string(presence.State)}

	if !presence.Since.IsZero() {
		since := api.FormatTimestamp(presence.Since)
		body.Since = &since
	}
	if !presence.ExpiresAt.IsZero() {
		expires := api.FormatTimestamp(presence.ExpiresAt)
		body.ExpiresAt = &expires
	}
	return body
}

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write presence response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write error response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

/*
writeError translates a domain failure into the answer a caller gets.

The one that is unlike the rest of Convia is [ErrUnavailable]. Everywhere else
a store that cannot be reached is an internal error, because there is a durable
answer that should have been available. Presence has no durable answer, so this
is reported as a condition that will pass — with the header that says when to
come back — rather than as a fault the caller can do nothing about.
*/
func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, ErrForbidden):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusForbidden, api.CodeForbidden,
				"The credential does not carry the scope this operation requires."))

	case errors.Is(err, ErrApplicationNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested application does not exist."))

	case errors.Is(err, ErrNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested user does not exist."))

	case errors.Is(err, ErrUserSuspended):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusConflict, api.CodeConflict,
				"Presence is not recorded for a user the application has suspended."))

	case errors.Is(err, ErrTooManyDevices):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusConflict, api.CodeConflict,
				"This user already has as many devices asserting presence as Convia holds."))

	case errors.Is(err, ErrUnavailable):
		response.Header().Set("Retry-After", "5")
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
				"Presence is temporarily unavailable. Nothing else about this application is affected."))

	default:
		handler.logger.Error("presence request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition."))
	}
}
