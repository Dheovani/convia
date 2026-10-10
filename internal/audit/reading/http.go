package reading

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
	"convia/internal/audit"
	"convia/internal/operator"
)

/*
service is the behavior the HTTP layer needs.

It is declared here, in the consuming layer, so that handler tests can exercise
every transport path without PostgreSQL. *Service satisfies it.
*/
type service interface {
	Search(ctx context.Context, options audit.SearchOptions) (audit.Page, error)
}

// Handler exposes the audit trail over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

/*
entryResponse is the public representation of one entry.

The actor is two fields rather than one string, because a consumer filtering
the trail by who acted should not have to split anything: the two halves are
what the query takes.
*/
type entryResponse struct {
	ID            string            `json:"id"`
	Action        string            `json:"action"`
	ActorKind     string            `json:"actor_kind"`
	ActorID       string            `json:"actor_id,omitempty"`
	ApplicationID string            `json:"application_id,omitempty"`
	SubjectKind   string            `json:"subject_kind"`
	SubjectID     string            `json:"subject_id"`
	Reason        string            `json:"reason,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
	RequestID     string            `json:"request_id"`
	RecordedAt    string            `json:"recorded_at"`
}

// searchResponse is the public representation of one page of the trail.
type searchResponse struct {
	Data       []entryResponse `json:"data"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func represent(entry audit.Entry) entryResponse {
	return entryResponse{
		ID:            entry.ID,
		Action:        entry.Action,
		ActorKind:     string(entry.Actor.Kind),
		ActorID:       entry.Actor.ID,
		ApplicationID: entry.ApplicationID,
		SubjectKind:   entry.Subject.Kind,
		SubjectID:     entry.Subject.ID,
		Reason:        entry.Reason,
		Details:       entry.Details,
		RequestID:     entry.RequestID,
		RecordedAt:    api.FormatTimestamp(entry.RecordedAt),
	}
}

/*
authorized binds the request's verified operator to the service.

A request that reaches here without an operator principal was routed without
the authentication middleware, which is a wiring mistake rather than a client
error. It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *Handler) authorized(response http.ResponseWriter, request *http.Request) (*OperatorAuthorized, bool) {
	principal, found := operator.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.ErrorContext(request.Context(), "operator route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
			"The request did not carry a usable credential."))
		return nil, false
	}
	return AuthorizeOperator(handler.service, principal), true
}

// Search returns one page of the audit trail.
func (handler *Handler) Search(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	parameters := request.URL.Query()
	options := audit.SearchOptions{
		Cursor: parameters.Get("cursor"),
		Query: audit.Query{
			ApplicationID: parameters.Get("application_id"),
			ActorKind:     audit.Kind(parameters.Get("actor_kind")),
			ActorID:       parameters.Get("actor_id"),
			SubjectKind:   parameters.Get("subject_kind"),
			SubjectID:     parameters.Get("subject_id"),
			Action:        parameters.Get("action"),
		},
	}

	if raw := parameters.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer."))
			return
		}
		options.Limit = limit
	}

	since, failure := moment(parameters.Get("since"), "since")
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}
	options.Query.Since = since

	until, failure := moment(parameters.Get("until"), "until")
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}
	options.Query.Until = until

	page, err := authorized.Search(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := searchResponse{Data: make([]entryResponse, 0, len(page.Entries)), NextCursor: page.NextCursor}
	for _, entry := range page.Entries {
		body.Data = append(body.Data, represent(entry))
	}

	handler.write(response, request, http.StatusOK, body)
}

/*
moment parses one end of the window a search asks about.

An unparseable instant is refused rather than ignored. A window silently
dropped would answer with the whole trail, and an operator who asked about the
hour before an incident would read a page of unrelated entries as the answer to
the question they asked.
*/
func moment(value, field string) (time.Time, *api.Failure) {
	if value == "" {
		return time.Time{}, nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"The "+field+" parameter must be an RFC 3339 timestamp.")
	}
	return parsed.UTC(), nil
}

/*
writeError translates a domain error into the public error schema.

Only errors the domain declares are described to the client. Anything else is
an unexpected condition: it is logged with its detail and reported as a generic
internal error, so that infrastructure failures never reach a public contract.
*/
func (handler *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var validation audit.ValidationError

	switch {
	case errors.As(err, &validation):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, ErrForbidden):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusForbidden, api.CodeForbidden,
				"The credential does not carry the scope this operation requires."))

	default:
		handler.logger.ErrorContext(request.Context(), "audit request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition."))
	}
}

func (handler *Handler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.ErrorContext(request.Context(), "write audit response",
			"error", err,
		)
	}
}

func (handler *Handler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.ErrorContext(request.Context(), "write error response",
			"error", err,
		)
	}
}
