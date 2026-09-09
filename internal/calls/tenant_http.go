package calls

import (
	"log/slog"
	"net/http"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own calls over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's calls
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

/*
Start begins a conversation in one of the caller's own rooms.

The room is named in the path because the call belongs to it. A room that is
already hosting a conversation answers `409 conflict` rather than returning the
running call, so a caller always knows whether it started something.
*/
func (handler *TenantHandler) Start(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body startRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the definition carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	call, err := authorized.Start(request.Context(), request.PathValue("room_id"), Definition(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, represent(call))
}

// Get returns one of the caller's own calls.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	call, err := authorized.Get(request.Context(), request.PathValue("call_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(call))
}

/*
List returns one page of the caller's own calls.

The same method serves the application-wide history and one room's, because
they differ only in whether the pattern names a room.
*/
func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
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

	handler.writePage(response, request, page)
}

/*
End stops one of the caller's own conversations.

Ending an already-ended call succeeds and returns it unchanged, so a client
retrying after a timeout is never punished for it.
*/
func (handler *TenantHandler) End(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	body, failure := decodeEnd(response, request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	call, err := authorized.End(request.Context(), request.PathValue("call_id"), body.Reason)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(call))
}

func (handler *TenantHandler) writePage(response http.ResponseWriter, request *http.Request, page Page) {
	body := listResponse{Data: make([]callResponse, 0, len(page.Calls)), NextCursor: page.NextCursor}
	for _, call := range page.Calls {
		body.Data = append(body.Data, represent(call))
	}
	handler.write(response, request, http.StatusOK, body)
}

// writeError translates a domain error into the public error schema.
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
