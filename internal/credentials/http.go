package credentials

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
)

/*
service is the behavior the HTTP layer needs.

It is declared here, in the consuming layer, so that handler tests can exercise
every transport path without PostgreSQL. *Service satisfies it.
*/
type service interface {
	Issue(ctx context.Context, applicationID string, request Request) (Credential, Secret, error)
	Get(ctx context.Context, applicationID, id string) (Credential, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	Revoke(ctx context.Context, applicationID, id string) error
}

// Handler exposes an application's credentials over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// issueRequest is the accepted body when issuing a credential.
type issueRequest struct {
	Name      string     `json:"name"`
	Scopes    []Scope    `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// credentialResponse is the public representation of a credential, never its secret.
type credentialResponse struct {
	ID            string   `json:"id"`
	ApplicationID string   `json:"application_id"`
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"created_at"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	RevokedAt     string   `json:"revoked_at,omitempty"`
}

/*
issuedResponse is the one response that carries secret material.

It exists as a separate type so that the field cannot be added to the ordinary
representation by accident: every other response is built from
credentialResponse, which has nowhere to put a secret.
*/
type issuedResponse struct {
	credentialResponse
	Secret string `json:"secret"`
}

// listResponse is the public representation of one page of credentials.
type listResponse struct {
	Data       []credentialResponse `json:"data"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func represent(credential Credential, at time.Time) credentialResponse {
	return credentialResponse{
		ID:            credential.ID,
		ApplicationID: credential.ApplicationID,
		Name:          credential.Name,
		Scopes:        texts(credential.Scopes),
		Status:        string(credential.Status(at)),
		CreatedAt:     api.FormatTimestamp(credential.CreatedAt),
		ExpiresAt:     formatOptional(credential.ExpiresAt),
		RevokedAt:     formatOptional(credential.RevokedAt),
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
Issue creates a credential and returns its secret once.

This is the only response in Convia that contains secret material. The secret
is not stored, so a client that loses it has to issue another credential;
saying so is the point of returning it exactly here and nowhere else.
*/
func (handler *Handler) Issue(response http.ResponseWriter, request *http.Request) {
	var body issueRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	credential, secret, err := handler.service.Issue(request.Context(),
		request.PathValue("application_id"), Request(body))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, issuedResponse{
		credentialResponse: represent(credential, time.Now().UTC()),
		Secret:             Token(credential.ID, secret),
	})
}

// Get returns one credential of an application.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	credential, err := handler.service.Get(request.Context(),
		request.PathValue("application_id"), request.PathValue("credential_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(credential, time.Now().UTC()))
}

// List returns one page of an application's credentials.
func (handler *Handler) List(response http.ResponseWriter, request *http.Request) {
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

	page, err := handler.service.List(request.Context(), request.PathValue("application_id"), options)
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

// Revoke withdraws a credential.
func (handler *Handler) Revoke(response http.ResponseWriter, request *http.Request) {
	err := handler.service.Revoke(request.Context(),
		request.PathValue("application_id"), request.PathValue("credential_id"))
	if err != nil {
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
	writeDomainError(handler.logger, response, request, err)
}

/*
writeDomainError is the single translation from a domain error to the public
error schema.

Both the operator-facing and the tenant-facing handlers use it, so the two
surfaces cannot drift into describing the same failure differently.
*/
func writeDomainError(logger *slog.Logger, response http.ResponseWriter, request *http.Request, err error) {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, ErrForbidden):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusForbidden, api.CodeForbidden,
				"The credential does not carry the scope this operation requires."))

	case errors.Is(err, ErrApplicationNotFound):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested application does not exist."))

	case errors.Is(err, ErrNotFound):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested credential does not exist."))

	default:
		logger.Error("credential request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		writeFailure(logger, response, request, api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition."))
	}
}

func writeFailure(logger *slog.Logger, response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write error response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *Handler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write credential response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *Handler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	writeFailure(handler.logger, response, request, failure)
}
