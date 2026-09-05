package users

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
)

/*
service is the behavior the HTTP layer needs.

It is declared here, in the consuming layer, so that handler tests can exercise
every transport path without PostgreSQL. *Service satisfies it.
*/
type service interface {
	Resolve(ctx context.Context, applicationID string, identity Identity) (User, bool, error)
	Get(ctx context.Context, applicationID, id string) (User, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
}

// Handler exposes an application's users over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// resolveRequest is the accepted body when resolving an identity.
type resolveRequest struct {
	ExternalSubject string            `json:"external_subject"`
	DisplayName     string            `json:"display_name"`
	Metadata        map[string]string `json:"metadata"`
}

// userResponse is the public representation of a user.
type userResponse struct {
	ID              string            `json:"id"`
	ApplicationID   string            `json:"application_id"`
	ExternalSubject string            `json:"external_subject"`
	DisplayName     string            `json:"display_name,omitempty"`
	Metadata        map[string]string `json:"metadata"`
	Status          string            `json:"status"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

// listResponse is the public representation of one page of users.
type listResponse struct {
	Data       []userResponse `json:"data"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func represent(user User) userResponse {
	metadata := user.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	return userResponse{
		ID:              user.ID,
		ApplicationID:   user.ApplicationID,
		ExternalSubject: user.ExternalSubject,
		DisplayName:     user.DisplayName,
		Metadata:        metadata,
		Status:          string(user.Status),
		CreatedAt:       api.FormatTimestamp(user.CreatedAt),
		UpdatedAt:       api.FormatTimestamp(user.UpdatedAt),
	}
}

/*
Resolve maps an application's person to a Convia user.

The response is 201 when the mapping was created and 200 when it already
existed, so a client can tell the two apart without the operation ever being
unsafe to repeat.
*/
func (handler *Handler) Resolve(response http.ResponseWriter, request *http.Request) {
	var body resolveRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the identity carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	user, created, err := handler.service.Resolve(request.Context(),
		request.PathValue("application_id"), Identity(body))
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

// Get returns one user of an application.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	user, err := handler.service.Get(request.Context(),
		request.PathValue("application_id"), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
}

// List returns one page of an application's users.
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

	body := listResponse{Data: make([]userResponse, 0, len(page.Users)), NextCursor: page.NextCursor}
	for _, user := range page.Users {
		body.Data = append(body.Data, represent(user))
	}

	handler.write(response, request, http.StatusOK, body)
}

/*
writeUser returns one user together with its entity tag.

The tag is what a client sends back as If-Match to make a later update
conditional, so every response carrying a user carries its version.
*/
func (handler *Handler) writeUser(response http.ResponseWriter, request *http.Request, status int, user User) {
	response.Header().Set("ETag", `"`+user.Version()+`"`)
	handler.write(response, request, status, represent(user))
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

	case errors.Is(err, ErrApplicationNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested application does not exist."))

	case errors.Is(err, ErrNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested user does not exist."))

	default:
		handler.logger.Error("user request failed",
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
		handler.logger.Error("write user response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
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
