package calls

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/operator"
)

// Handler exposes an application's calls to an operator over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// startRequest is the accepted body when starting a call.
type startRequest struct {
	Metadata map[string]string `json:"metadata"`
}

/*
endRequest is the accepted body when ending a call.

Every field is optional, and so is the body itself: ending a conversation is
usually all a caller wants to say.
*/
type endRequest struct {
	Reason string `json:"reason"`
}

/*
callResponse is the public representation of a call.

Nothing here names a media provider. When one exists, its session identifier
will live in the calls table and stay out of this struct, and a contract test
asserts the public schema carries only Convia-owned fields.
*/
type callResponse struct {
	ID            string            `json:"id"`
	ApplicationID string            `json:"application_id"`
	RoomID        string            `json:"room_id"`
	Status        string            `json:"status"`
	Metadata      map[string]string `json:"metadata"`
	StartedBy     string            `json:"started_by"`
	EndedBy       string            `json:"ended_by,omitempty"`
	EndReason     string            `json:"end_reason,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
	EndedAt       string            `json:"ended_at,omitempty"`
}

// listResponse is the public representation of one page of calls.
type listResponse struct {
	Data       []callResponse `json:"data"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func represent(call Call) callResponse {
	metadata := call.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	body := callResponse{
		ID:            call.ID,
		ApplicationID: call.ApplicationID,
		RoomID:        call.RoomID,
		Status:        string(call.Status),
		Metadata:      metadata,
		StartedBy:     string(call.StartedBy),
		EndReason:     call.EndReason,
		CreatedAt:     api.FormatTimestamp(call.CreatedAt),
		UpdatedAt:     api.FormatTimestamp(call.UpdatedAt),
	}

	if call.EndedBy != nil {
		body.EndedBy = string(*call.EndedBy)
	}
	if call.EndedAt != nil {
		body.EndedAt = api.FormatTimestamp(*call.EndedAt)
	}
	return body
}

/*
listOptions reads the pagination and filtering a client asked for.

The room is taken from the path rather than from the query, so a room-scoped
history and an application-wide one are different routes rather than the same
route behaving differently. A pattern without a room yields an empty value.
*/
func listOptions(request *http.Request) (ListOptions, *api.Failure) {
	query := request.URL.Query()
	options := ListOptions{
		Cursor: query.Get("cursor"),
		Status: query.Get("status"),
		RoomID: request.PathValue("room_id"),
	}

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return ListOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer.")
		}
		options.Limit = limit
	}
	return options, nil
}

/*
decodeEnd reads the optional explanation recorded with an ending.

A request with no body ends the call without a reason. One that declares a
content type is decoded under the ordinary rules, so a malformed body is still
reported rather than silently discarded.
*/
func decodeEnd(response http.ResponseWriter, request *http.Request) (endRequest, *api.Failure) {
	if request.Header.Get("Content-Type") == "" {
		return endRequest{}, nil
	}

	var body endRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		return endRequest{}, failure
	}
	return body, nil
}

/*
authorized binds the request's verified operator to the service.

A request that reaches here without an operator principal was routed without
the authentication middleware, which is a wiring mistake rather than a client
error. It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *Handler) authorized(response http.ResponseWriter, request *http.Request) (*OperatorAuthorized, bool) {
	principal, found := operator.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("operator route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
			"The request did not carry a usable credential."))
		return nil, false
	}
	return AuthorizeOperator(handler.service, principal), true
}

// Get returns one call of the application in the path.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	call, err := authorized.Get(request.Context(),
		request.PathValue("application_id"), request.PathValue("call_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(call))
}

/*
List returns one page of the application's calls.

The same method serves the application-wide history and one room's, because
they differ only in whether the pattern names a room.
*/
func (handler *Handler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options, failure := listOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.List(request.Context(), request.PathValue("application_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writePage(response, request, page)
}

// End stops a conversation on the operator's authority.
func (handler *Handler) End(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	body, failure := decodeEnd(response, request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	call, err := authorized.End(request.Context(),
		request.PathValue("application_id"), request.PathValue("call_id"), body.Reason)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(call))
}

func (handler *Handler) writePage(response http.ResponseWriter, request *http.Request, page Page) {
	body := listResponse{Data: make([]callResponse, 0, len(page.Calls)), NextCursor: page.NextCursor}
	for _, call := range page.Calls {
		body.Data = append(body.Data, represent(call))
	}
	handler.write(response, request, http.StatusOK, body)
}

/*
writeError translates a domain error into the public error schema.

Only errors the domain declares are described to the client. Anything else is
an unexpected condition: it is logged with its detail and reported as a generic
internal error, so that infrastructure failures never reach a public contract.
*/
func (handler *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

/*
failureFor maps a calls error onto the public schema.

It is shared by both handlers so that the tenant surface and the operator
surface cannot answer the same domain error differently.
*/
func failureFor(err error, logger *slog.Logger, request *http.Request) *api.Failure {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		return api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message)

	case errors.Is(err, ErrForbidden):
		return api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The credential does not carry the scope this operation requires.")

	case errors.Is(err, ErrApplicationNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested application does not exist.")

	case errors.Is(err, ErrRoomNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested room does not exist.")

	case errors.Is(err, ErrNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested call does not exist.")

	case errors.Is(err, ErrRoomClosed):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The room is closed and does not accept new calls.")

	case errors.Is(err, ErrCallInProgress):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The room already has an active call.")

	/*
		The media plane could not realize the call, and the failure was one
		another attempt may get past. The call was not left holding its room,
		so retrying is genuinely open to the caller.
	*/
	case errors.Is(err, ErrMediaUnavailable):
		return api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"The call could not be established because a dependency is unavailable. Retry shortly.")

	default:
		logger.Error("call request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		return api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition.")
	}
}

func (handler *Handler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *Handler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
