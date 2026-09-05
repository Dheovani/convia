package applications

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
	Create(ctx context.Context, name string) (Application, error)
	Get(ctx context.Context, id string) (Application, error)
	List(ctx context.Context, options ListOptions) (Page, error)
}

// Handler exposes applications over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// createRequest is the accepted body of a creation request.
type createRequest struct {
	Name string `json:"name"`
}

// applicationResponse is the public representation of an application.
type applicationResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// listResponse is the public representation of one page of applications.
type listResponse struct {
	Data       []applicationResponse `json:"data"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func represent(application Application) applicationResponse {
	return applicationResponse{
		ID:        application.ID,
		Name:      application.Name,
		Status:    string(application.Status),
		CreatedAt: api.FormatTimestamp(application.CreatedAt),
		UpdatedAt: api.FormatTimestamp(application.UpdatedAt),
	}
}

// Create registers an application and returns its representation.
func (handler *Handler) Create(response http.ResponseWriter, request *http.Request) {
	var body createRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	application, err := handler.service.Create(request.Context(), body.Name)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, represent(application))
}

// Get returns one application.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	application, err := handler.service.Get(request.Context(), request.PathValue("application_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(application))
}

// List returns one page of applications.
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

	page, err := handler.service.List(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := listResponse{Data: make([]applicationResponse, 0, len(page.Applications)), NextCursor: page.NextCursor}
	for _, application := range page.Applications {
		body.Data = append(body.Data, represent(application))
	}

	handler.write(response, request, http.StatusOK, body)
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

	case errors.Is(err, ErrNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested application does not exist."))

	default:
		handler.logger.Error("application request failed",
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
		handler.logger.Error("write application response",
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
