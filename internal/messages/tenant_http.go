package messages

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/credentials"
)

/*
TenantHandler exposes the caller's own rooms' histories over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's messages
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
authorRequest is how a caller names which of its people is acting.

Both fields are optional in the schema and exactly one is required in practice,
which the domain enforces. Expressing it as two nullable fields rather than a
tagged union keeps the wire shape the same as the roster's, where a participant
is already a user or an invitation.
*/
type authorRequest struct {
	UserID       string `json:"user_id"`
	InvitationID string `json:"invitation_id"`
}

// author reads the identity the request named. The conversion is the same one
// the rooms handler makes from its request body to a domain value.
func (request authorRequest) author() Author {
	return Author(request)
}

/*
contentRequest is the accepted body when saying something or changing it.

Posting and editing take the same shape because they are the same statement:
this person, these words. Declaring two identical types so that the two routes
could drift apart later would be inviting exactly the drift.
*/
type contentRequest struct {
	authorRequest
	Body string `json:"body"`
}

/*
messageResponse is the public representation of a message.

`body` is omitted rather than sent empty once a message is withdrawn. A
tombstone that carried `"body": ""` would be indistinguishable from a bug, and
a client would have to consult `deleted_at` to know which it was looking at.
*/
type messageResponse struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	RoomID        string `json:"room_id"`
	Sequence      int64  `json:"sequence"`
	UserID        string `json:"user_id,omitempty"`
	InvitationID  string `json:"invitation_id,omitempty"`
	Body          string `json:"body,omitempty"`
	Deleted       bool   `json:"deleted"`
	CreatedAt     string `json:"created_at"`
	EditedAt      string `json:"edited_at,omitempty"`
	DeletedAt     string `json:"deleted_at,omitempty"`
	DeletedBy     string `json:"deleted_by,omitempty"`
}

// historyResponse is the public representation of one window of a history.
type historyResponse struct {
	Data       []messageResponse `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func represent(message Message) messageResponse {
	response := messageResponse{
		ID:            message.ID,
		ApplicationID: message.ApplicationID,
		RoomID:        message.RoomID,
		Sequence:      message.Sequence,
		UserID:        message.Author.UserID,
		InvitationID:  message.Author.InvitationID,
		Body:          message.Body,
		Deleted:       message.Deleted(),
		CreatedAt:     api.FormatTimestamp(message.CreatedAt),
	}

	if message.EditedAt != nil {
		response.EditedAt = api.FormatTimestamp(*message.EditedAt)
	}

	if message.DeletedAt != nil {
		response.DeletedAt = api.FormatTimestamp(*message.DeletedAt)
		response.DeletedBy = string(message.DeletedBy)
	}

	return response
}

/*
historyOptions reads the window a client asked for.

The cursor is parsed as an integer rather than accepted as an opaque string,
because it is one: a message's sequence is published in the message itself and
is what read state is expressed in. A cursor that is not a number is a client
mistake worth reporting rather than a value to pass through and ignore.
*/
func historyOptions(request *http.Request) (HistoryOptions, *api.Failure) {
	query := request.URL.Query()
	options := HistoryOptions{Direction: Older}

	if raw := query.Get("direction"); raw != "" {
		direction, err := ParseDirection(raw)
		if err != nil {
			return HistoryOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The direction must be either older or newer.")
		}
		options.Direction = direction
	}

	if raw := query.Get("cursor"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 {
			return HistoryOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The cursor must be the sequence of a message to read from.")
		}
		options.After = &cursor
	}

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return HistoryOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer.")
		}
		options.Limit = limit
	}

	return options, nil
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

// Post records something one of the caller's own people said in a room.
func (handler *TenantHandler) Post(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body contentRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	message, err := authorized.Post(request.Context(), request.PathValue("room_id"),
		body.author(), body.Body)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, represent(message))
}

// History returns one window of a room's history.
func (handler *TenantHandler) History(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options, failure := historyOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.History(request.Context(), request.PathValue("room_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := historyResponse{
		Data:       make([]messageResponse, 0, len(page.Messages)),
		NextCursor: page.NextCursor,
	}
	for _, message := range page.Messages {
		body.Data = append(body.Data, represent(message))
	}
	handler.write(response, request, http.StatusOK, body)
}

// Get returns one message from the caller's own application.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	message, err := authorized.Get(request.Context(), request.PathValue("message_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(message))
}

// Edit replaces what one of the caller's own people said.
func (handler *TenantHandler) Edit(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body contentRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	message, err := authorized.Edit(request.Context(), request.PathValue("message_id"),
		body.author(), body.Body)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(message))
}

/*
Delete withdraws a message, leaving a tombstone where it was.

It is a POST to a sub-resource rather than a DELETE on the message, because the
request has to name **which person** is withdrawing it, and a DELETE carrying a
body is a shape many clients cannot send. This is the same reason a participant
is removed by `POST /participants/{id}/remove`, and the answer is the same for
the same reason.

The reply is the tombstone rather than 204, because a client needs the
`deleted_at` it now carries to render it.
*/
func (handler *TenantHandler) Delete(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body authorRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	message, err := authorized.Delete(request.Context(), request.PathValue("message_id"), body.author())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(message))
}

// writeError translates a domain error into the public error schema.
func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

/*
failureFor maps a domain error onto the public error schema.

A room that does not exist and a room belonging to another tenant produce the
same answer, because they are the same answer: the caller may not learn that an
identifier names something elsewhere.
*/
func failureFor(err error, logger *slog.Logger, request *http.Request) *api.Failure {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		return api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message)

	case errors.Is(err, ErrForbidden):
		return api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The credential does not carry the scope this operation requires.")

	case errors.Is(err, ErrRoomNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested room does not exist.")

	case errors.Is(err, ErrNotFound):
		return api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"The requested message does not exist.")

	case errors.Is(err, ErrRoomClosed):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The room is closed and accepts no new messages.")

	case errors.Is(err, ErrDeleted):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The message was withdrawn and can no longer be changed.")

	/*
		Not 403. A credential carrying messages:write was granted everything
		this surface can grant, so telling it to ask for a scope would send it
		after something that would not help. It named the wrong person.
	*/
	case errors.Is(err, ErrNotAuthor):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The message was written by somebody else, and only its author may change it.")

	default:
		logger.Error("message request failed",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		return api.NewFailure(http.StatusInternalServerError, api.CodeInternal,
			"The server encountered an unexpected condition.")
	}
}

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

// readStateRequest is the accepted body when marking a room read.
type readStateRequest struct {
	UserID   string `json:"user_id"`
	Sequence int64  `json:"sequence"`
}

/*
readStateResponse is the public representation of how far somebody has read.

`unread` is derived from `sequence` on every read rather than stored, so the two
cannot drift apart. It excludes the person's own messages and withdrawn ones —
nobody has an unread message from themselves, and a tombstone is the absence of
something to read.
*/
type readStateResponse struct {
	RoomID    string `json:"room_id"`
	UserID    string `json:"user_id"`
	Sequence  int64  `json:"sequence"`
	Unread    int64  `json:"unread"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func representReadState(state ReadState) readStateResponse {
	response := readStateResponse{
		RoomID:   state.RoomID,
		UserID:   state.UserID,
		Sequence: state.Sequence,
		Unread:   state.Unread,
	}

	// Somebody who has read nothing has no timestamp to report, and sending
	// the zero time would read as "they read this in 1970".
	if state.Sequence != Unseen {
		response.UpdatedAt = api.FormatTimestamp(state.UpdatedAt)
	}
	return response
}

/*
MarkRead records that one of the caller's own people has read a room.

It is a PUT because it sets a position rather than appending to anything, and
sending the same position twice is the same outcome as sending it once. The
reply is the position that now holds, which is not always the one that was sent:
read state only moves forward, so a mark behind the stored one leaves it alone.
*/
func (handler *TenantHandler) MarkRead(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body readStateRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	state, err := authorized.MarkRead(request.Context(), request.PathValue("room_id"),
		body.UserID, body.Sequence)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, representReadState(state))
}

/*
ReadState reports how far one of the caller's own people has read in a room.

The person is named in the query rather than in a body, because this is a read:
a GET that carried a body would be a request many caches and proxies would
mishandle, and the tenant already comes from the credential.
*/
func (handler *TenantHandler) ReadState(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	userID := request.URL.Query().Get("user_id")
	if userID == "" {
		handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest,
			api.CodeInvalidRequest, "Naming the person whose read state to report is required."))
		return
	}

	state, err := authorized.ReadState(request.Context(), request.PathValue("room_id"), userID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, representReadState(state))
}
