package rooms

import (
	"context"
	"log/slog"
	"net/http"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own rooms over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's rooms
even by mistake: there is no request field that could name one.
*/
type TenantHandler struct {
	logger  *slog.Logger
	service service
}

func NewTenantHandler(logger *slog.Logger, service service) *TenantHandler {
	return &TenantHandler{logger: logger, service: service}
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

// Create registers a room for the caller's own application.
func (handler *TenantHandler) Create(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body createRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := authorized.Create(request.Context(), Definition(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusCreated, room)
}

// Get returns one of the caller's own rooms.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	room, err := authorized.Get(request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

/*
List returns one page of the caller's own rooms.

An `alias` query parameter answers with the single room that holds it, because
looking a room up by the name the application chose is the common case and
should not require paging a listing to find it.
*/
func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if alias := request.URL.Query().Get("alias"); alias != "" {
		room, err := authorized.GetByAlias(request.Context(), alias)
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

	page, err := authorized.List(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := listResponse{Data: make([]roomResponse, 0, len(page.Rooms)), NextCursor: page.NextCursor}
	for _, room := range page.Rooms {
		body.Data = append(body.Data, represent(room))
	}
	handler.write(response, request, http.StatusOK, body)
}

// Update changes the attributes the caller owns.
func (handler *TenantHandler) Update(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body updateRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := authorized.Update(request.Context(), request.PathValue("room_id"),
		Change(body), expectedVersion(request))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

// Close stops one of the caller's own rooms accepting new calls.
func (handler *TenantHandler) Close(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}
	handler.transition(response, request, authorized.Close)
}

// Reopen returns one of the caller's own closed rooms to service.
func (handler *TenantHandler) Reopen(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}
	handler.transition(response, request, authorized.Reopen)
}

func (handler *TenantHandler) transition(response http.ResponseWriter, request *http.Request,
	apply func(context.Context, string) (Room, error)) {
	room, err := apply(request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeRoom(response, request, http.StatusOK, room)
}

// Delete removes one of the caller's own rooms from the API surface.
func (handler *TenantHandler) Delete(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if err := authorized.Delete(request.Context(), request.PathValue("room_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (handler *TenantHandler) writeRoom(response http.ResponseWriter, request *http.Request,
	status int, room Room) {
	response.Header().Set("ETag", `"`+room.Version()+`"`)
	handler.write(response, request, status, represent(room))
}

// writeError translates a domain error into the public error schema, using the
// same mapping as the operator surface so the two cannot diverge.
func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
