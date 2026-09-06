package users

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own users over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's users
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

// Resolve maps one of the caller's people to a Convia user.
func (handler *TenantHandler) Resolve(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body resolveRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	user, created, err := authorized.Resolve(request.Context(), Identity(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	handler.writeUser(response, request, status, user)
}

// Get returns one of the caller's users.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	user, err := authorized.Get(request.Context(), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
}

// List returns one page of the caller's users.
func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options := ListOptions{Cursor: request.URL.Query().Get("cursor")}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer."))
			return
		}
		options.Limit = limit
	}

	page, err := authorized.List(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := listResponse{Data: make([]userResponse, 0, len(page.Users)), NextCursor: page.NextCursor}
	for _, user := range page.Users {
		body.Data = append(body.Data, represent(user))
	}

	handler.write(response, request, http.StatusOK, body)
}

// Update changes the attributes of one of the caller's users.
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

	user, err := authorized.Update(request.Context(), request.PathValue("user_id"),
		Attributes(body), expectedVersion(request))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
}

// Suspend withdraws one of the caller's users.
func (handler *TenantHandler) Suspend(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, func(authorized *Authorized, ctx context.Context, id string) (User, error) {
		return authorized.Suspend(ctx, id)
	})
}

// Activate restores one of the caller's suspended users.
func (handler *TenantHandler) Activate(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, func(authorized *Authorized, ctx context.Context, id string) (User, error) {
		return authorized.Activate(ctx, id)
	})
}

// Delete removes one of the caller's users from the API surface.
func (handler *TenantHandler) Delete(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if err := authorized.Delete(request.Context(), request.PathValue("user_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (handler *TenantHandler) transition(response http.ResponseWriter, request *http.Request,
	apply func(*Authorized, context.Context, string) (User, error)) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	user, err := apply(authorized, request.Context(), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
}

// writeUser returns one user together with its entity tag, as the
// operator-facing handler does, so a client sees the same representation
// whichever surface it reached.
func (handler *TenantHandler) writeUser(response http.ResponseWriter, request *http.Request, status int, user User) {
	response.Header().Set("ETag", `"`+user.Version()+`"`)
	handler.write(response, request, status, represent(user))
}

func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	writeDomainError(handler.logger, response, request, err)
}

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write user response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	writeFailure(handler.logger, response, request, failure)
}
