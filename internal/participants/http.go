package participants

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/operator"
)

// Handler exposes a tenant's call rosters to an operator over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// joinRequest is the accepted body when admitting someone to a call.
type joinRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

/*
removeRequest is the accepted body when putting someone out of a call.

`by` names the participant on whose authority the removal is made. It is
optional: without it the caller acts as itself. With it, Convia checks that
participant is a moderator of the same call.
*/
type removeRequest struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// roleRequest is the accepted body when changing what a participant may do.
type roleRequest struct {
	Role string `json:"role"`
	By   string `json:"by"`
}

/*
participantResponse is the public representation of a participant.

The person is named by their Convia user identifier and nothing else. A display
name would copy into every roster entry something the application already owns
and can change, and would put a person's name in a place nobody asked for it;
a client that wants one reads the user.
*/
type participantResponse struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	CallID        string `json:"call_id"`
	UserID        string `json:"user_id"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	RemovedBy     string `json:"removed_by,omitempty"`
	RemovedByID   string `json:"removed_by_participant_id,omitempty"`
	RemovalReason string `json:"removal_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	LeftAt        string `json:"left_at,omitempty"`
}

// listResponse is the public representation of one page of participants.
type listResponse struct {
	Data       []participantResponse `json:"data"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func represent(participant Participant) participantResponse {
	body := participantResponse{
		ID:            participant.ID,
		ApplicationID: participant.ApplicationID,
		CallID:        participant.CallID,
		UserID:        participant.UserID,
		Role:          string(participant.Role),
		Status:        string(participant.Status),
		RemovedByID:   participant.RemovedByID,
		RemovalReason: participant.RemovalReason,
		CreatedAt:     api.FormatTimestamp(participant.CreatedAt),
		UpdatedAt:     api.FormatTimestamp(participant.UpdatedAt),
	}

	if participant.RemovedBy != nil {
		body.RemovedBy = string(*participant.RemovedBy)
	}
	if participant.LeftAt != nil {
		body.LeftAt = api.FormatTimestamp(*participant.LeftAt)
	}
	return body
}

// listOptions reads the pagination and filtering a client asked for.
func listOptions(request *http.Request) (ListOptions, *api.Failure) {
	query := request.URL.Query()
	options := ListOptions{Cursor: query.Get("cursor"), Status: query.Get("status")}

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return ListOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer.")
		}
		options.Limit = limit
	}
	return options, nil
}

/*
decodeRemoval reads the optional detail recorded with a removal.

A request with no body removes the participant on the caller's own authority.
One that declares a content type is decoded under the ordinary rules, so a
malformed body is still reported rather than silently discarded.
*/
func decodeRemoval(response http.ResponseWriter, request *http.Request) (removeRequest, *api.Failure) {
	if request.Header.Get("Content-Type") == "" {
		return removeRequest{}, nil
	}

	var body removeRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		return removeRequest{}, failure
	}
	return body, nil
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
		handler.logger.Error("operator route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
			"The request did not carry a usable credential."))
		return nil, false
	}
	return AuthorizeOperator(handler.service, principal), true
}

// Get returns one participant of the application in the path.
func (handler *Handler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	participant, err := authorized.Get(request.Context(),
		request.PathValue("application_id"), request.PathValue("participant_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

// List returns one page of a call's roster.
func (handler *Handler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options, failure := listOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.List(request.Context(),
		request.PathValue("application_id"), request.PathValue("call_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writePage(response, request, page)
}

// Remove puts someone out of a tenant's call on the operator's authority.
func (handler *Handler) Remove(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	body, failure := decodeRemoval(response, request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}
	if body.By != "" {
		/*
			An operator acts from outside the conversation, so there is no
			moderator to credit. Accepting the field and ignoring it would let
			a caller believe a check ran that did not.
		*/
		handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
			"An operator removes on its own authority and cannot act as a participant."))
		return
	}

	participant, err := authorized.Remove(request.Context(),
		request.PathValue("application_id"), request.PathValue("participant_id"), body.Reason)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(participant))
}

func (handler *Handler) writePage(response http.ResponseWriter, request *http.Request, page Page) {
	body := listResponse{
		Data:       make([]participantResponse, 0, len(page.Participants)),
		NextCursor: page.NextCursor,
	}
	for _, participant := range page.Participants {
		body.Data = append(body.Data, represent(participant))
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
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

/*
failureFor maps a participants error onto the public schema.

It is shared by both handlers so that the tenant surface and the operator
surface cannot answer the same domain error differently.
*/
func failureFor(err error, logger *slog.Logger, request *http.Request) *api.Failure {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		return api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message)

	case errors.Is(err, ErrForbidden):
		return api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The credential does not carry the scope this operation requires.")

	/*
		A participant acting beyond their role is a forbidden action rather
		than a missing one, and it is deliberately not the same message as a
		refused credential: presenting a different key would not help, and
		neither would being granted a scope. Being made a moderator would.
	*/
	case errors.Is(err, ErrNotAModerator):
		return api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The acting participant is not a moderator of this call.")

	case errors.Is(err, ErrApplicationNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested application does not exist.")

	case errors.Is(err, ErrCallNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested call does not exist.")

	case errors.Is(err, ErrUserNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested user does not exist.")

	case errors.Is(err, ErrNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested participant does not exist.")

	case errors.Is(err, ErrCallEnded):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The call has ended and its participants can no longer change.")

	case errors.Is(err, ErrUserSuspended):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The user is not active and cannot join a call.")

	case errors.Is(err, ErrCallFull):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The call has reached the capacity its room declared.")

	case errors.Is(err, ErrRemoved):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The user was removed from this call and cannot rejoin it.")

	case errors.Is(err, ErrGone):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The participant is no longer in the call.")

	default:
		logger.Error("participant request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		return api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition.")
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
