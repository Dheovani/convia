package departure

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/sessions"
)

// deleter is the service the handler needs.
type deleter interface {
	Delete(ctx context.Context, principal sessions.Principal, identity accounts.Identity, password accounts.Password) error
}

// identities opens the key a session holds, which leaving rooms elsewhere signs with.
type identities interface {
	Identity(ctx context.Context, token string) (accounts.Identity, error)
}

// Handler serves a person deleting their own account.
type Handler struct {
	logger     *slog.Logger
	service    deleter
	identities identities
}

func NewHandler(logger *slog.Logger, service deleter, identities identities) *Handler {
	return &Handler{logger: logger, service: service, identities: identities}
}

// deleteRequest is the password that confirms the deletion.
type deleteRequest struct {
	Password string `json:"password"`
}

/*
Delete removes the signed-in person's account and signs the browser out.

It is a POST to a verb rather than a DELETE on `/v1/me`, because it carries a
body, which a DELETE is not promised to.
*/
func (handler *Handler) Delete(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")

	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		handler.writeError(response, request, sessions.ErrUnauthenticated)
		return
	}

	var body deleteRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	token, _ := sessions.Present(request)
	identity, err := handler.identities.Identity(request.Context(), token)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	if err := handler.service.Delete(request.Context(), principal, identity,
		accounts.Password(body.Password)); err != nil {
		handler.writeError(response, request, err)
		return
	}

	sessions.Clear(response)
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, ErrPasswordMissing):
		handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"Say the account's password to delete it."))

	case errors.Is(err, accounts.ErrWrongPassword):
		handler.writeFailure(response, request, api.NewFailure(http.StatusForbidden, api.CodeWrongPassword,
			"The password is not right."))

	case errors.Is(err, sessions.ErrUnauthenticated), errors.Is(err, accounts.ErrNotFound):
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
			"The request did not carry a usable session."))

	case errors.Is(err, accounts.ErrBusy):
		response.Header().Set("Retry-After", "1")
		handler.writeFailure(response, request, api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"Convia is verifying too many passwords at once. Try again in a moment."))

	default:
		path := strings.ReplaceAll(request.URL.Path, "\n", "")
		path = strings.ReplaceAll(path, "\r", "")
		handler.logger.Error("deleting an account failed",
			"error", err,
			"method", request.Method,
			"path", path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}

func (handler *Handler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write error response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
