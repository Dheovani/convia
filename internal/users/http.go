package users

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	Update(ctx context.Context, applicationID, id string, attributes Attributes, expectedVersion string) (User, error)
	Suspend(ctx context.Context, applicationID, id string) (User, error)
	Activate(ctx context.Context, applicationID, id string) (User, error)
	Delete(ctx context.Context, applicationID, id string) error
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

/*
updateRequest is the accepted body when changing a user's attributes.

The fields are pointers so that an absent field is distinguishable from an
empty one: omitting display_name leaves it as stored, while sending an empty
string clears it.
*/
type updateRequest struct {
	DisplayName *string            `json:"display_name"`
	Metadata    *map[string]string `json:"metadata"`
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

/*
Update changes the attributes an application owns.

A client may send the ETag it last read as If-Match to make the change
conditional. The header is optional: a lost attribute update is visible and
easy to repair, so requiring it on every call would cost more than it protects.
*/
func (handler *Handler) Update(response http.ResponseWriter, request *http.Request) {
	var body updateRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	/*
		The request body and the attributes carry the same fields, so the
		conversion keeps them in step: adding a field to one without the other
		fails to compile rather than silently dropping it.
	*/
	user, err := handler.service.Update(request.Context(),
		request.PathValue("application_id"), request.PathValue("user_id"),
		Attributes(body), expectedVersion(request))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
}

// Suspend withdraws a user's access without losing data.
func (handler *Handler) Suspend(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, handler.service.Suspend)
}

// Activate restores a suspended user to normal service.
func (handler *Handler) Activate(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, handler.service.Activate)
}

// Delete removes a user from the API surface.
func (handler *Handler) Delete(response http.ResponseWriter, request *http.Request) {
	err := handler.service.Delete(request.Context(),
		request.PathValue("application_id"), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) transition(response http.ResponseWriter, request *http.Request,
	apply func(context.Context, string, string) (User, error)) {
	user, err := apply(request.Context(),
		request.PathValue("application_id"), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeUser(response, request, http.StatusOK, user)
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

	case errors.Is(err, ErrPreconditionFailed):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusPreconditionFailed, api.CodePreconditionFailed,
				"The user was modified by another request. Read it again and retry."))

	case errors.Is(err, ErrSubjectDeleted):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusConflict, api.CodeConflict,
				"The external subject belongs to a deleted user and stays reserved until erasure."))

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
