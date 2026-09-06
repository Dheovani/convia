package credentials

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
)

/*
TenantHandler exposes the caller's own credentials over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so an application can only ever see and withdraw its own
keys.
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
	principal, found := PrincipalFromContext(request.Context())
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
Issue creates a credential for the caller's own application and returns its
secret once.

The scopes it may request are bounded by the ones it already holds, so this
endpoint cannot be used to climb from a narrow key to a broad one.
*/
func (handler *TenantHandler) Issue(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body issueRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	credential, secret, err := authorized.Issue(request.Context(), Request(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, issuedResponse{
		credentialResponse: represent(credential, time.Now().UTC()),
		Secret:             Token(credential.ID, secret),
	})
}

// Get returns one of the caller's own credentials.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	credential, err := authorized.Get(request.Context(), request.PathValue("credential_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(credential, time.Now().UTC()))
}

// List returns one page of the caller's own credentials.
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

	at := time.Now().UTC()
	body := listResponse{Data: make([]credentialResponse, 0, len(page.Credentials)), NextCursor: page.NextCursor}
	for _, credential := range page.Credentials {
		body.Data = append(body.Data, represent(credential, at))
	}

	handler.write(response, request, http.StatusOK, body)
}

// Revoke withdraws one of the caller's own credentials.
func (handler *TenantHandler) Revoke(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if err := authorized.Revoke(request.Context(), request.PathValue("credential_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	writeDomainError(handler.logger, response, request, err)
}

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write credential response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	writeFailure(handler.logger, response, request, failure)
}
