package participants

import (
	"log/slog"
	"net/http"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own call rosters over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's
participants even by mistake: there is no request field that could name one.
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
Join admits someone to one of the caller's own calls.

It answers `201` when this request admitted them and `200` when they were
already there, which is what makes a reconnection safe to retry: a client that
dropped and came back is the same person and gets the participant that already
exists, rather than a second entry in the roster.
*/
func (handler *TenantHandler) Join(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body joinRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the admission carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	participant, admitted, err := authorized.Join(request.Context(),
		request.PathValue("call_id"), Admission(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	status := http.StatusOK
	if admitted {
		status = http.StatusCreated
	}
	handler.write(response, request, status, represent(participant))
}

// Get returns one participant of the caller's own call.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	participant, err := authorized.Get(request.Context(), request.PathValue("participant_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

// List returns one page of a call's roster.
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

	page, err := authorized.List(request.Context(), request.PathValue("call_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writePage(response, request, page)
}

/*
Leave records that someone left of their own accord.

Leaving twice succeeds and returns the participant unchanged, so a client
retrying after a timeout is never punished for it.
*/
func (handler *TenantHandler) Leave(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	participant, err := authorized.Leave(request.Context(), request.PathValue("participant_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

/*
Remove puts someone out of one of the caller's own calls.

Naming an acting participant in `by` makes Convia check that participant is a
moderator of the same call. Without one, the application removes on its own
authority, which it holds over its own calls.
*/
func (handler *TenantHandler) Remove(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	body, failure := decodeRemoval(response, request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	participant, err := authorized.Remove(request.Context(),
		request.PathValue("participant_id"), body.By, body.Reason)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

/*
SetRole changes what a participant of the caller's own call may do.

As with removal, naming an acting participant makes Convia check that person is
a moderator: promoting someone is exactly the authority the role exists for.
*/
func (handler *TenantHandler) SetRole(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body roleRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	participant, err := authorized.SetRole(request.Context(),
		request.PathValue("participant_id"), body.Role, body.By)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

func (handler *TenantHandler) writePage(response http.ResponseWriter, request *http.Request, page Page) {
	body := listResponse{
		Data:       make([]participantResponse, 0, len(page.Participants)),
		NextCursor: page.NextCursor,
	}
	for _, participant := range page.Participants {
		body.Data = append(body.Data, represent(participant))
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
