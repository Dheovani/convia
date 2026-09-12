package rooms

import (
	"net/http"
	"strconv"

	"convia/internal/api"
)

// memberResponse is the public representation of one person's place in a room.
type memberResponse struct {
	ApplicationID string `json:"application_id"`
	RoomID        string `json:"room_id"`
	UserID        string `json:"user_id"`
	CreatedAt     string `json:"created_at"`
}

// membershipResponse is the public representation of one page of places.
type membershipResponse struct {
	Data       []memberResponse `json:"data"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func representMember(member Member) memberResponse {
	return memberResponse{
		ApplicationID: member.ApplicationID,
		RoomID:        member.RoomID,
		UserID:        member.UserID,
		CreatedAt:     api.FormatTimestamp(member.CreatedAt),
	}
}

// membershipOptions reads the page a client asked for.
func membershipOptions(request *http.Request) (MembershipOptions, *api.Failure) {
	query := request.URL.Query()
	options := MembershipOptions{Cursor: query.Get("cursor")}

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return MembershipOptions{}, api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest,
				"The limit must be an integer.")
		}
		options.Limit = limit
	}
	return options, nil
}

/*
AddMember gives one of the caller's own people a place in one of its rooms.

A `PUT` on the person's own address rather than a `POST` to a collection,
because the operation is idempotent by the person: adding somebody twice is the
same outcome as adding them once, and a collection `POST` would promise a new
resource each time.

It answers `201` the first time and `200` afterwards, so a caller that needs to
know whether it changed anything can tell without comparing timestamps.
*/
func (handler *TenantHandler) AddMember(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	member, added, err := authorized.AddMember(request.Context(),
		request.PathValue("room_id"), request.PathValue("user_id"))
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
RemoveMember takes somebody's place away.

Removing somebody who was not there answers `204` like any other removal. The
caller asked for a state the room is already in, and a retried request must not
look like a mistake.

**Nothing happens to what they said.** The messages are the room's record of a
conversation that did happen, and withdrawing them would rewrite it for
everybody still there.
*/
func (handler *TenantHandler) RemoveMember(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if _, err := authorized.RemoveMember(request.Context(),
		request.PathValue("room_id"), request.PathValue("user_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

// Members returns one page of who belongs to one of the caller's own rooms.
func (handler *TenantHandler) Members(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options, failure := membershipOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.Members(request.Context(), request.PathValue("room_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeMembership(response, request, page)
}

/*
RoomsOf returns one page of the rooms one of the caller's own people belongs to.

It hangs off the person rather than off rooms because that is what it is about,
and because the rooms listing filters a room's own attributes — membership is
not one of them.
*/
func (handler *TenantHandler) RoomsOf(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options, failure := membershipOptions(request)
	if failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	page, err := authorized.RoomsOf(request.Context(), request.PathValue("user_id"), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.writeMembership(response, request, page)
}

func (handler *TenantHandler) writeMembership(response http.ResponseWriter,
	request *http.Request, page Membership) {
	body := membershipResponse{
		Data:       make([]memberResponse, 0, len(page.Members)),
		NextCursor: page.NextCursor,
	}
	for _, member := range page.Members {
		body.Data = append(body.Data, representMember(member))
	}
	handler.write(response, request, http.StatusOK, body)
}
