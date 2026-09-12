package messages

import (
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/rooms"
	"convia/internal/sessions"
)

/*
SessionHandler exposes a signed-in person's own conversations over HTTP.

These routes name nobody. The person comes from the session cookie, so there is
no request field that could address somebody else's rooms or write in somebody
else's name — **not because the handler checks, but because there is nowhere to
put it**. That is the same move M18 made with the tenant: a bug of this shape is
unrepresentable rather than merely unlikely.
*/
type SessionHandler struct {
	logger  *slog.Logger
	service service
	rooms   membership
}

func NewSessionHandler(logger *slog.Logger, service service, membership membership) *SessionHandler {
	return &SessionHandler{logger: logger, service: service, rooms: membership}
}

// sessionMessageRequest is the accepted body when saying something or changing it.
type sessionMessageRequest struct {
	Body string `json:"body"`
}

// sessionReadStateRequest is the accepted body when marking a room read.
type sessionReadStateRequest struct {
	Sequence int64 `json:"sequence"`
}

/*
sidebarRoom is one row of somebody's sidebar.

It carries the room a person would recognize and the count that tells them
whether to look at it, because those two together are what a sidebar is. Fetching
the second separately would be a request per row on the screen.
*/
type sidebarRoom struct {
	ID     string `json:"id"`
	Alias  string `json:"alias,omitempty"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Unread int64  `json:"unread"`
}

// sidebarResponse is one page of the rooms somebody is in.
type sidebarResponse struct {
	Data       []sidebarRoom `json:"data"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func representSidebarRoom(room Room) sidebarRoom {
	return sidebarRoom{
		ID:     room.Room.ID,
		Alias:  room.Room.Alias,
		Name:   room.Room.Name,
		Status: string(room.Room.Status),
		Unread: room.Unread,
	}
}

/*
personal binds the request's verified session to the service.

A request that reaches here without a session was routed without the
authentication middleware, which is a wiring mistake rather than a client error.
It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *SessionHandler) personal(response http.ResponseWriter,
	request *http.Request) (*Personal, bool) {
	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("a session route was reached without a session",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return nil, false
	}
	return AsPerson(handler.service, handler.rooms, principal), true
}

/*
Rooms returns the rooms this person is in, with what they have not read.

This is the sidebar. It carries no request field naming whose sidebar it is:
there is one answer per session, and it is the session's.
*/
func (handler *SessionHandler) Rooms(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options := rooms.MembershipOptions{Cursor: request.URL.Query().Get("cursor")}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeFailure(response, request, api.NewFailure(http.StatusBadRequest,
				api.CodeInvalidRequest, "The limit must be an integer."))
			return
		}
		options.Limit = limit
	}

	page, err := personal.Rooms(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := sidebarResponse{
		Data:       make([]sidebarRoom, 0, len(page.Rooms)),
		NextCursor: page.NextCursor,
	}
	for _, room := range page.Rooms {
		body.Data = append(body.Data, representSidebarRoom(room))
	}
	handler.write(response, request, http.StatusOK, body)
}

// History returns one window of a room this person is in.
func (handler *SessionHandler) History(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	options, failure := historyOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := personal.History(request.Context(), request.PathValue("room_id"), options)
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

/*
Post records something this person said.

The body carries what they wrote and nothing else. **There is no author field**,
which is the whole difference between this surface and the tenant one: an
application says which of its people is speaking because it is acting on their
behalf, and a person cannot say it because saying it would mean saying somebody
else.
*/
func (handler *SessionHandler) Post(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body sessionMessageRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	message, err := personal.Post(request.Context(), request.PathValue("room_id"), body.Body)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, represent(message))
}

// Edit replaces what this person said.
func (handler *SessionHandler) Edit(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body sessionMessageRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	message, err := personal.Edit(request.Context(), request.PathValue("message_id"), body.Body)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(message))
}

/*
Delete withdraws something this person said.

A POST to a sub-resource rather than a DELETE, matching the tenant surface. The
reason there was that the request has to name who is withdrawing; here it does
not, but spelling one operation two ways across two surfaces would be a
difference a reader has to explain rather than one that means something.
*/
func (handler *SessionHandler) Delete(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	message, err := personal.Delete(request.Context(), request.PathValue("message_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(message))
}

// MarkRead records how far this person has read in a room they are in.
func (handler *SessionHandler) MarkRead(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	var body sessionReadStateRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	state, err := personal.MarkRead(request.Context(), request.PathValue("room_id"), body.Sequence)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, representReadState(state))
}

// ReadState reports how far this person has read in a room they are in.
func (handler *SessionHandler) ReadState(response http.ResponseWriter, request *http.Request) {
	personal, ok := handler.personal(response, request)
	if !ok {
		return
	}

	state, err := personal.ReadState(request.Context(), request.PathValue("room_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, representReadState(state))
}

/*
writeError translates a domain error for a person rather than an application.

**A private response, always.** These bodies are one person's conversations, and
a cache that served one to somebody else would be the worst failure this surface
could have. `Vary: Cookie` says the answer depends on who asked; `no-store` says
not to keep it at all.
*/
func (handler *SessionHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

func (handler *SessionHandler) write(response http.ResponseWriter, request *http.Request,
	status int, body any) {
	private(response)
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *SessionHandler) writeFailure(response http.ResponseWriter,
	request *http.Request, failure *api.Failure) {
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

It is the same pair the session surface already sets on sign-in and on /v1/me,
and it matters more here: a history is larger, more cacheable-looking, and more
damaging to hand to the wrong person.
*/
func private(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
}
