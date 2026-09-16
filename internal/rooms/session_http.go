package rooms

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/api"
	"convia/internal/sessions"
)

/*
SessionHandler exposes a signed-in person's own rooms over HTTP.

Like every route a session reaches, these name nobody but the person the cookie
belongs to — except adding somebody, which names them in the path and is exactly
as far as that goes: see Personal.Add for who can be named.
*/
type SessionHandler struct {
	logger    *slog.Logger
	service   personalService
	directory directory
}

func NewSessionHandler(logger *slog.Logger, service personalService, directory directory) *SessionHandler {
	return &SessionHandler{logger: logger, service: service, directory: directory}
}

// ownRoomRequest is the accepted body when a person opens a room. It has a name
// and nothing else; see Personal.Create.
type ownRoomRequest struct {
	Name string `json:"name"`
}

// ownRoomResponse is a room as a person in it sees it.
type ownRoomResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Owned     bool   `json:"owned"`
	CreatedAt string `json:"created_at"`
}

func representOwnRoom(room Room, userID string) ownRoomResponse {
	return ownRoomResponse{
		ID:        room.ID,
		Name:      room.Name,
		Status:    string(room.Status),
		Owned:     room.OwnerUserID != "" && room.OwnerUserID == userID,
		CreatedAt: api.FormatTimestamp(room.CreatedAt),
	}
}

// personResponse is somebody a person can see, by the name they go by, and
// their role when they are listed as a member.
type personResponse struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role,omitempty"`
}

type peopleResponse struct {
	Data       []personResponse `json:"data"`
	NextCursor string           `json:"next_cursor,omitempty"`
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
	return AsPerson(handler.service, handler.directory, principal), true
}

// Create opens a room with the signed-in person in it.
func (handler *SessionHandler) Create(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body ownRoomRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := personal.Create(request.Context(), body.Name)
	/*
		The person refused here is the caller, not somebody they named: an
		application can suspend the user behind an account without ending its
		session. Telling them so reveals nothing about anybody else, and the
		answer every other route gives — that a person cannot be added — would
		be about the wrong person.
	*/
	if errors.Is(err, ErrUserUnavailable) || errors.Is(err, ErrUserNotFound) {
		handler.writeFailure(response, request, api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"Your access to rooms is suspended."))
		return
	}
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, representOwnRoom(room, personal.principal.UserID))
}

// Members returns one page of who is in a room the signed-in person is in.
func (handler *SessionHandler) Members(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options, failure := membershipOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := personal.Members(request.Context(), request.PathValue("room_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.writePeople(response, request, page)
}

/*
AddMember gives somebody a place in a room the signed-in person is in.

A `PUT` on the person's address, answering `201` the first time and `200`
afterwards, exactly as the application surface does — the operation is the same
one, reached with a narrower authority.
*/
func (handler *SessionHandler) AddMember(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	member, added, err := personal.Add(request.Context(), request.PathValue("room_id"), request.PathValue("user_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	status := http.StatusOK
	if added {
		status = http.StatusCreated
	}
	handler.write(response, request, status, representMember(member))
}

/*
Leave takes the signed-in person's own place away.

A `POST` to an action rather than a `DELETE` on a member's address. The address
would have to name the person, and on this surface the only person it could
ever name is the caller — so the path says what happens instead of carrying an
identifier that has exactly one legal value.
*/
func (handler *SessionHandler) Leave(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	if err := personal.Leave(request.Context(), request.PathValue("room_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}

	private(response)
	response.WriteHeader(http.StatusNoContent)
}

// People returns one page of the people the signed-in person could add.
func (handler *SessionHandler) People(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options, failure := membershipOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := personal.People(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.writePeople(response, request, page)
}

/*
RemoveMember takes somebody else's place in a room the signed-in person owns.

A `DELETE` on the member's address, which here names somebody else. Leaving is a
separate action for the reason Leave gives.
*/
func (handler *SessionHandler) RemoveMember(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.Remove(request.Context(), request.PathValue("room_id"), request.PathValue("user_id"))
	})
}

// Ban keeps somebody out of a room the signed-in person owns.
func (handler *SessionHandler) Ban(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.Ban(request.Context(), request.PathValue("room_id"), request.PathValue("user_id"))
	})
}

// Unban lifts a ban on a room the signed-in person owns.
func (handler *SessionHandler) Unban(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.Unban(request.Context(), request.PathValue("room_id"), request.PathValue("user_id"))
	})
}

// Bans returns one page of the people kept out of a room the signed-in person owns.
func (handler *SessionHandler) Bans(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options, failure := membershipOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := personal.Bans(request.Context(), request.PathValue("room_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.writePeople(response, request, page)
}

// NameModerator makes a member a moderator of a room the signed-in person owns.
func (handler *SessionHandler) NameModerator(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.NameModerator(request.Context(), request.PathValue("room_id"),
			request.PathValue("user_id"), true)
	})
}

// UnnameModerator stops a member moderating a room the signed-in person owns.
func (handler *SessionHandler) UnnameModerator(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.NameModerator(request.Context(), request.PathValue("room_id"),
			request.PathValue("user_id"), false)
	})
}

// transferRequest names who a room is handed to.
type transferRequest struct {
	UserID string `json:"user_id"`
}

// Transfer hands a room the signed-in person owns to another member.
func (handler *SessionHandler) Transfer(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body transferRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := personal.Transfer(request.Context(), request.PathValue("room_id"), body.UserID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, representOwnRoom(room, personal.principal.UserID))
}

// Rename gives a room the signed-in person owns a new name, and nothing else.
func (handler *SessionHandler) Rename(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body ownRoomRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	room, err := personal.Rename(request.Context(), request.PathValue("room_id"), body.Name)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, representOwnRoom(room, personal.principal.UserID))
}

// Close stops a room the signed-in person owns taking anything new.
func (handler *SessionHandler) Close(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, (*Personal).Close)
}

// Reopen returns a room the signed-in person owns to use.
func (handler *SessionHandler) Reopen(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, (*Personal).Reopen)
}

// DeleteRoom removes a room the signed-in person owns, for everybody in it.
func (handler *SessionHandler) DeleteRoom(response http.ResponseWriter, request *http.Request) {
	handler.act(response, request, func(personal *Personal) error {
		return personal.Delete(request.Context(), request.PathValue("room_id"))
	})
}

// act runs one act whose answer is only that it happened.
func (handler *SessionHandler) act(response http.ResponseWriter, request *http.Request, do func(*Personal) error) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	if err := do(personal); err != nil {
		handler.writeError(response, request, err)
		return
	}
	private(response)
	response.WriteHeader(http.StatusNoContent)
}

// transition moves a room the signed-in person owns and answers with the room.
func (handler *SessionHandler) transition(response http.ResponseWriter, request *http.Request,
	move func(*Personal, context.Context, string) (Room, error)) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	room, err := move(personal, request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, representOwnRoom(room, personal.principal.UserID))
}

func (handler *SessionHandler) writePeople(response http.ResponseWriter, request *http.Request, page People) {
	body := peopleResponse{
		Data:       make([]personResponse, 0, len(page.People)),
		NextCursor: page.NextCursor,
	}
	for _, person := range page.People {
		body.Data = append(body.Data, personResponse(person))
	}
	handler.write(response, request, http.StatusOK, body)
}

/*
writeError translates a domain error for a person rather than an application.

**Somebody who cannot be added is one answer.** The application surface tells a
caller whether the person it named does not exist or is suspended, because an
application is entitled to know that about its own people. A person is not:
the first would reveal which identifiers exist, and the second would reveal
another person's suspension. Personal.Add already folds them together; this
mapping is here so that no future route on this surface can unfold them by
falling through to the application's wording.
*/
func (handler *SessionHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, ErrNotOwner) {
		handler.writeFailure(response, request, api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"Only the room's owner can do that."))
		return
	}
	if errors.Is(err, ErrNotModerator) {
		handler.writeFailure(response, request, api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"Only the room's owner or a moderator can do that."))
		return
	}
	if errors.Is(err, ErrUserNotFound) || errors.Is(err, ErrUserUnavailable) || errors.Is(err, ErrBanned) {
		handler.writeFailure(response, request, api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"That person is not somebody you can add to this room."))
		return
	}
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
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

/*
private marks a response as belonging to whoever asked for it.

Who is in somebody's rooms, and who they know, is as personal as anything on
this surface. It is the same pair the messages and sessions handlers set.
*/
func private(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
}
