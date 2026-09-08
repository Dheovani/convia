package rooms

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"convia/internal/api"
	"convia/internal/operator"
)

// Handler exposes an application's rooms to an operator over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// createRequest is the accepted body when creating a room.
type createRequest struct {
	Alias           string            `json:"alias"`
	Name            string            `json:"name"`
	Metadata        map[string]string `json:"metadata"`
	MaxParticipants *int              `json:"max_participants"`
}

/*
updateRequest is the accepted body when changing a room.

Every field is a pointer so that omitting one and clearing it stay
distinguishable. MaxParticipants is a double pointer for the same reason one
level deeper: the outer says whether the caller mentioned capacity at all, and
the inner whether they set it or removed it.
*/
type updateRequest struct {
	Alias           *string            `json:"alias"`
	Name            *string            `json:"name"`
	Metadata        *map[string]string `json:"metadata"`
	MaxParticipants **int              `json:"max_participants"`
}

// roomResponse is the public representation of a room.
type roomResponse struct {
	ID              string            `json:"id"`
	ApplicationID   string            `json:"application_id"`
	Alias           string            `json:"alias,omitempty"`
	Name            string            `json:"name"`
	Metadata        map[string]string `json:"metadata"`
	MaxParticipants *int              `json:"max_participants,omitempty"`
	Status          string            `json:"status"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

// listResponse is the public representation of one page of rooms.
type listResponse struct {
	Data       []roomResponse `json:"data"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func represent(room Room) roomResponse {
	metadata := room.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	return roomResponse{
		ID:              room.ID,
		ApplicationID:   room.ApplicationID,
		Alias:           room.Alias,
		Name:            room.Name,
		Metadata:        metadata,
		MaxParticipants: room.MaxParticipants,
		Status:          string(room.Status),
		CreatedAt:       api.FormatTimestamp(room.CreatedAt),
		UpdatedAt:       api.FormatTimestamp(room.UpdatedAt),
	}
}

/*
expectedVersion reads the entity tag a client is making its update conditional on.

Only a single strong validator is honored. `*` means "any current version",
which is the same as sending no condition for an update to an existing
resource, and a weak validator is not accepted because it does not identify an
exact revision.
*/
func expectedVersion(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("If-Match"))
	if value == "" || value == "*" || strings.HasPrefix(value, "W/") {
		return ""
	}
	return strings.Trim(value, `"`)
}

// listOptions reads the pagination and filtering a client asked for.
func listOptions(request *http.Request) (ListOptions, *api.Failure) {
	query := request.URL.Query()
	options := ListOptions{Cursor: query.Get("cursor"), Status: query.Get("status")}

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

// Create registers a room for the application in the path.
func (handler *Handler) Create(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body createRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the definition carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	room, err := authorized.Create(request.Context(), request.PathValue("application_id"), Definition(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusCreated, room)
}

// Get returns one room of the application in the path.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	room, err := authorized.Get(request.Context(),
		request.PathValue("application_id"), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

/*
List returns one page of the application's rooms.

An `alias` query parameter answers with the single room that holds it, because
looking a room up by the name an application chose is the common case and
should not require paging a listing to find it.
*/
func (handler *Handler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	applicationID := request.PathValue("application_id")

	if alias := request.URL.Query().Get("alias"); alias != "" {
		room, err := authorized.GetByAlias(request.Context(), applicationID, alias)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		handler.write(response, request, http.StatusOK,
			listResponse{Data: []roomResponse{represent(room)}})
		return
	}

	options, failure := listOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.List(request.Context(), applicationID, options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writePage(response, request, page)
}

// Update changes the attributes the application owns.
func (handler *Handler) Update(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body updateRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := authorized.Update(request.Context(),
		request.PathValue("application_id"), request.PathValue("room_id"),
		Change(body), expectedVersion(request))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

// Close stops the room accepting new calls.
func (handler *Handler) Close(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}
	handler.transition(response, request, authorized.Close)
}

// Reopen returns a closed room to service.
func (handler *Handler) Reopen(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}
	handler.transition(response, request, authorized.Reopen)
}

func (handler *Handler) transition(response http.ResponseWriter, request *http.Request,
	apply func(context.Context, string, string) (Room, error)) {
	room, err := apply(request.Context(),
		request.PathValue("application_id"), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

// Delete removes the room from the API surface.
func (handler *Handler) Delete(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	err := authorized.Delete(request.Context(),
		request.PathValue("application_id"), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

/*
writeRoom returns a room with the entity tag a client makes updates conditional
on, so every response carrying a room carries its version.
*/
func (handler *Handler) writeRoom(response http.ResponseWriter, request *http.Request,
	status int, room Room) {
	response.Header().Set("ETag", `"`+room.Version()+`"`)
	handler.write(response, request, status, represent(room))
}

func (handler *Handler) writePage(response http.ResponseWriter, request *http.Request, page Page) {
	body := listResponse{Data: make([]roomResponse, 0, len(page.Rooms)), NextCursor: page.NextCursor}
	for _, room := range page.Rooms {
		body.Data = append(body.Data, represent(room))
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
failureFor maps a rooms error onto the public schema.

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

	case errors.Is(err, ErrNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested room does not exist.")

	case errors.Is(err, ErrAliasTaken):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"Another room of this application already uses that alias.")

	case errors.Is(err, ErrDeleted):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The room is deleted and can no longer be changed.")

	case errors.Is(err, ErrPreconditionFailed):
		return api.NewFailure(http.StatusPreconditionFailed, api.CodePreconditionFailed,
			"The room was modified by another request. Read it again and retry.")

	default:
		logger.Error("room request failed",
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
