package participants

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/sessions"
)

/*
SessionHandler exposes the calls in a signed-in person's rooms over HTTP.

Every route names a room, and the person comes from the cookie. Removing
somebody names them too, and reaches only somebody who is in the call.
*/
type SessionHandler struct {
	logger    *slog.Logger
	service   personalService
	rooms     membership
	directory directory
}

func NewSessionHandler(logger *slog.Logger, service personalService, places membership,
	people directory) *SessionHandler {
	return &SessionHandler{logger: logger, service: service, rooms: places, directory: people}
}

// roomCallResponse is a call as the people in its room see it.
type roomCallResponse struct {
	ID        string `json:"id"`
	RoomID    string `json:"room_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func representRoomCall(call calls.Call) roomCallResponse {
	return roomCallResponse{
		ID:        call.ID,
		RoomID:    call.RoomID,
		Status:    string(call.Status),
		CreatedAt: api.FormatTimestamp(call.CreatedAt),
	}
}

// presentResponse is somebody in a call, by the name they go by.
type presentResponse struct {
	ParticipantID string `json:"participant_id"`
	UserID        string `json:"user_id"`
	DisplayName   string `json:"display_name"`
	Role          string `json:"role"`
	JoinedAt      string `json:"joined_at"`
}

type rosterResponse struct {
	Data       []presentResponse `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func (handler *SessionHandler) personal(response http.ResponseWriter, request *http.Request) (*Personal, bool) {
	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		path := strings.ReplaceAll(strings.ReplaceAll(request.URL.Path, "\n", ""), "\r", "")
		handler.logger.Error("a session route was reached without a session",
			"method", request.Method,
			"path", path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return nil, false
	}
	return AsPerson(handler.service, handler.rooms, handler.directory, principal), true
}

type roomCallsResponse struct {
	Data []roomCallResponse `json:"data"`
}

// Calls returns the calls running in the rooms the signed-in person is in.
func (handler *SessionHandler) Calls(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	running, err := personal.Calls(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := roomCallsResponse{Data: make([]roomCallResponse, 0, len(running))}
	for _, call := range running {
		body.Data = append(body.Data, representRoomCall(call))
	}
	handler.write(response, request, http.StatusOK, body)
}

// Call returns the call a room the signed-in person is in is holding.
func (handler *SessionHandler) Call(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	call, err := personal.Call(request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, representRoomCall(call))
}

// Roster returns one page of who is in that call now.
func (handler *SessionHandler) Roster(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options, failure := listOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	roster, err := personal.Roster(request.Context(), request.PathValue("room_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := rosterResponse{Data: make([]presentResponse, 0, len(roster.People)), NextCursor: roster.NextCursor}
	for _, present := range roster.People {
		body.Data = append(body.Data, presentResponse{
			ParticipantID: present.Participant.ID,
			UserID:        present.Participant.UserID,
			DisplayName:   present.DisplayName,
			Role:          string(present.Participant.Role),
			JoinedAt:      api.FormatTimestamp(present.Participant.CreatedAt),
		})
	}
	handler.write(response, request, http.StatusOK, body)
}

/*
Join seats the signed-in person in the room's call, starting one if there is
none, and answers with what to connect with.

201 means this request started the call and 200 that it joined one already
running, so a client can tell the two apart without a second read. Joining
again while already in the call answers the same participation with a fresh
credential, which is how a client that lost its connection gets back in.
*/
func (handler *SessionHandler) Join(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	seat, started, err := personal.Join(request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	status := http.StatusOK
	if started {
		status = http.StatusCreated
	}
	handler.write(response, request, status, representSession(seat.Participant, seat.Credential))
}

// Leave takes the signed-in person out of the room's call.
func (handler *SessionHandler) Leave(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	if err := personal.Leave(request.Context(), request.PathValue("room_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.noContent(response)
}

// Remove puts somebody out of the room's call, on the authority of its moderator.
func (handler *SessionHandler) Remove(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	err := personal.Remove(request.Context(), request.PathValue("room_id"), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.noContent(response)
}

/*
writeError answers a person in their own terms.

The tenant surface's messages talk about participants and credentials, which is
right for an integration and wrong for somebody who pressed a button. Anything
this does not name is answered as the tenant surface would, so no failure goes
unanswered.
*/
func (handler *SessionHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var failure *api.Failure

	switch {
	case errors.Is(err, ErrRoomNotFound), errors.Is(err, calls.ErrRoomNotFound):
		failure = api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The requested room does not exist.")
	case errors.Is(err, ErrCallNotFound):
		failure = api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The room has no call right now.")
	case errors.Is(err, ErrNotFound):
		failure = api.NewFailure(http.StatusNotFound, api.CodeNotFound, "That person is not in the call.")
	case errors.Is(err, calls.ErrRoomClosed):
		failure = api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The room is closed, so a call cannot be started in it.")
	case errors.Is(err, ErrCallEnded):
		failure = api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The call ended as you joined it. Join again to start a new one.")
	case errors.Is(err, ErrRemoved):
		failure = api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"You were removed from this call and cannot rejoin it.")
	case errors.Is(err, ErrNotAModerator):
		failure = api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"Only the room's owner moderates its calls.")
	case errors.Is(err, ErrGone):
		failure = api.NewFailure(http.StatusConflict, api.CodeConflict, "Join the call to moderate it.")
	case errors.Is(err, ErrUserSuspended), errors.Is(err, ErrUserNotFound):
		failure = api.NewFailure(http.StatusForbidden, api.CodeForbidden, "Your access to calls is suspended.")
	case errors.Is(err, ErrNoMediaPlane):
		failure = api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"This installation has no media server, so calls cannot be held here.")
	default:
		failure = failureFor(err, handler.logger, request)
	}

	handler.writeFailure(response, request, failure)
}

func (handler *SessionHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	private(response)
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *SessionHandler) writeFailure(
	response http.ResponseWriter,
	request *http.Request,
	failure *api.Failure,
) {
	private(response)
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *SessionHandler) noContent(response http.ResponseWriter) {
	private(response)
	response.WriteHeader(http.StatusNoContent)
}

/*
private marks a response as belonging to whoever asked for it.

Who is in somebody's calls is as personal as who is in their rooms. It is the
same pair every other session handler sets.
*/
func private(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
}
