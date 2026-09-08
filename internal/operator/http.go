package operator

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
)

/*
Handler exposes operator credentials over HTTP.

These routes carry no application in the path, and not because the tenant comes
from the key as it does on the tenant surface: an operator credential belongs
to no tenant at all.
*/
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// issueRequest is the accepted body when issuing an operator credential.
type issueRequest struct {
	Name      string     `json:"name"`
	Scopes    []Scope    `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// credentialResponse is the public representation of an operator credential,
// never its secret.
type credentialResponse struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	RevokedAt string   `json:"revoked_at,omitempty"`
}

/*
issuedResponse is the one operator response that carries secret material.

It exists as a separate type so that the field cannot be added to the ordinary
representation by accident: every other response is built from
credentialResponse, which has nowhere to put a secret.
*/
type issuedResponse struct {
	credentialResponse
	Secret string `json:"secret"`
}

// listResponse is the public representation of one page of operator credentials.
type listResponse struct {
	Data       []credentialResponse `json:"data"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func represent(credential Credential, at time.Time) credentialResponse {
	return credentialResponse{
		ID:        credential.ID,
		Name:      credential.Name,
		Scopes:    texts(credential.Scopes),
		Status:    string(credential.Status(at)),
		CreatedAt: api.FormatTimestamp(credential.CreatedAt),
		ExpiresAt: formatOptional(credential.ExpiresAt),
		RevokedAt: formatOptional(credential.RevokedAt),
	}
}

// formatOptional renders a timestamp that a credential may not carry.
func formatOptional(moment *time.Time) string {
	if moment == nil {
		return ""
	}
	return api.FormatTimestamp(*moment)
}

/*
authorized binds the request's verified operator to the service.

A request that reaches here without a principal was routed without the
authentication middleware, which is a wiring mistake rather than a client
error. It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *Handler) authorized(response http.ResponseWriter, request *http.Request) (*Authorized, bool) {
	principal, found := PrincipalFromContext(request.Context())
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
	return Authorize(handler.service, principal), true
}

/*
Issue creates an operator credential and returns its secret once.

The secret is not stored, so an operator that loses it has to issue another
credential. Saying so is the point of returning it exactly here and nowhere
else.
*/
func (handler *Handler) Issue(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body issueRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the domain request carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	credential, value, err := authorized.Issue(request.Context(), Request(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, issuedResponse{
		credentialResponse: represent(credential, time.Now().UTC()),
		Secret:             Token(credential.ID, value),
	})
}

// Get returns one operator credential.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
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

// List returns one page of operator credentials.
func (handler *Handler) List(response http.ResponseWriter, request *http.Request) {
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

// Revoke withdraws an operator credential.
func (handler *Handler) Revoke(response http.ResponseWriter, request *http.Request) {
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

/*
writeError translates a domain error into the public error schema.

Only errors the domain declares are described to the client. Anything else is
an unexpected condition: it is logged with its detail and reported as a generic
internal error, so that infrastructure failures never reach a public contract.
*/
func (handler *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, ErrForbidden):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusForbidden, api.CodeForbidden,
				"The credential does not carry the scope this operation requires."))

	case errors.Is(err, ErrNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound,
				"The requested operator credential does not exist."))

	default:
		handler.logger.Error("operator credential request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition."))
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
