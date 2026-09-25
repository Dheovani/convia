package peers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/messages"
	"convia/internal/sessions"
)

// The bodies exchanged between installations. Both sides use these, so the two
// halves of the protocol cannot describe it differently.
type (
	previewBody struct {
		RoomName  string `json:"room_name"`
		Inviter   string `json:"inviter"`
		Invitee   string `json:"invitee"`
		ExpiresAt string `json:"expires_at"`
	}

	acceptRequest struct {
		Username string `json:"username"`
	}

	acceptedBody struct {
		RoomID   string `json:"room_id"`
		RoomName string `json:"room_name"`
		UserID   string `json:"user_id"`
		Local    bool   `json:"local"`
	}
)

// hostService is what the surface other installations call needs.
type hostService interface {
	Preview(ctx context.Context, signer Signer, id string) (Preview, error)
	Accept(ctx context.Context, signer Signer, username, id string) (Accepted, error)
}

// PeerHandler serves the invitation routes other installations call.
type PeerHandler struct {
	logger  *slog.Logger
	service hostService
}

func NewPeerHandler(logger *slog.Logger, service hostService) *PeerHandler {
	return &PeerHandler{logger: logger, service: service}
}

// Invitation tells the person an invitation is for what it is for.
func (handler *PeerHandler) Invitation(response http.ResponseWriter, request *http.Request) {
	signer, ok := signerOf(handler.logger, response, request)
	if !ok {
		return
	}

	preview, err := handler.service.Preview(request.Context(), signer, request.PathValue("invitation_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	write(handler.logger, response, request, http.StatusOK, previewBody{
		RoomName:  preview.RoomName,
		Inviter:   preview.Inviter,
		Invitee:   preview.Invitee,
		ExpiresAt: api.FormatTimestamp(preview.ExpiresAt),
	})
}

// Accept admits the person an invitation is for.
func (handler *PeerHandler) Accept(response http.ResponseWriter, request *http.Request) {
	signer, ok := signerOf(handler.logger, response, request)
	if !ok {
		return
	}

	var body acceptRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}

	accepted, err := handler.service.Accept(request.Context(), signer, body.Username, request.PathValue("invitation_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	write(handler.logger, response, request, http.StatusOK, acceptedBody(accepted))
}

// personalService is what the routes a signed-in person uses need.
type personalService interface {
	Invite(ctx context.Context, principal sessions.Principal, roomID, handle string) (Invitation, error)
	Revoke(ctx context.Context, principal sessions.Principal, id string) error
	Pending(ctx context.Context, principal sessions.Principal, roomID string) ([]Invitation, error)
	Look(ctx context.Context, ours []string, principal sessions.Principal, identity accounts.Identity, link string) (Link, Preview, error)
	Join(ctx context.Context, ours []string, account accounts.Account, identity accounts.Identity, link string) (Joined, error)
	RemoteRooms(ctx context.Context, accountID string) ([]RemoteRoom, error)
	RemoteRoom(ctx context.Context, accountID, id string) (RemoteRoom, error)
	Relay(ctx context.Context, identity accounts.Identity, remote RemoteRoom, method, target string, body []byte) (Response, error)
	Leave(ctx context.Context, identity accounts.Identity, remote RemoteRoom) error
	Forget(ctx context.Context, remote RemoteRoom) error
}

/*
identities is how a request's session becomes the key to sign with.

The key is opened from the session cookie on each request that needs it and
dropped when the request ends. Nothing keeps it longer.
*/
type identities interface {
	Identity(ctx context.Context, token string) (accounts.Identity, error)
	Account(ctx context.Context, accountID string) (accounts.Account, error)
}

/*
SessionHandler serves what a signed-in person does with rooms that cross
installations: inviting somebody into a room here, and joining and using a room
somewhere else.
*/
type SessionHandler struct {
	logger     *slog.Logger
	service    personalService
	identities identities
	// public is the address an operator says other installations reach this one
	// at, already checked, or "" when nobody said. See [SessionHandler.naming].
	public string
}

func NewSessionHandler(
	logger *slog.Logger,
	service personalService,
	identities identities,
	public string,
) *SessionHandler {
	return &SessionHandler{logger: logger, service: service, identities: identities, public: public}
}

/*
naming is the address this installation's links should name.

A configured address wins over anything a request can say, because it is the
only one that is true where the two differ: behind a reverse proxy, or when the
person using Convia opened it at an address nobody else can reach. Without one,
the address the request arrived at is the best guess available, and for one
machine on one network it is right.
*/
func (handler *SessionHandler) naming(request *http.Request) (string, error) {
	if handler.public != "" {
		return handler.public, nil
	}
	return homeReached(request)
}

/*
ours is every address that is this installation, for deciding whether a link
somebody pasted leads back here.

There can be two, and both are ordinary: the configured one that other people
use, and the one this request arrived at, which is how somebody reaches their
own Convia from the machine it runs on. A link naming either is answered here
rather than fetched over the network from ourselves. Anything else travels —
working out which *other* names might resolve to this same machine is how a
guard against reaching the private network gets talked out of its job.
*/
func (handler *SessionHandler) ours(request *http.Request) []string {
	addresses := make([]string, 0, 2)
	if handler.public != "" {
		addresses = append(addresses, handler.public)
	}
	if reached, err := homeReached(request); err == nil && reached != handler.public {
		addresses = append(addresses, reached)
	}
	return addresses
}

type (
	inviteRequest struct {
		Handle string `json:"handle"`
	}

	invitationResponse struct {
		ID        string `json:"id"`
		Link      string `json:"link"`
		Invitee   string `json:"invitee"`
		ExpiresAt string `json:"expires_at"`
	}

	linkRequest struct {
		Link string `json:"link"`
	}

	lookResponse struct {
		Home      string `json:"home"`
		RoomName  string `json:"room_name"`
		Inviter   string `json:"inviter"`
		Invitee   string `json:"invitee"`
		ExpiresAt string `json:"expires_at"`
	}

	remoteRoomResponse struct {
		ID     string `json:"id"`
		Home   string `json:"home"`
		RoomID string `json:"room_id"`
		UserID string `json:"user_id"`
		Name   string `json:"name"`
	}

	joinedResponse struct {
		RoomID     string              `json:"room_id"`
		RoomName   string              `json:"room_name"`
		RemoteRoom *remoteRoomResponse `json:"remote_room,omitempty"`
	}

	invitationsResponse struct {
		Data []invitationResponse `json:"data"`
	}

	remoteRoomsResponse struct {
		Data []remoteRoomResponse `json:"data"`
	}

	relayedMessage struct {
		Body string `json:"body"`
	}

	relayedReadState struct {
		Sequence int64 `json:"sequence"`
	}
)

func representRemoteRoom(room RemoteRoom) remoteRoomResponse {
	return remoteRoomResponse{ID: room.ID, Home: room.Home, RoomID: room.RoomID, UserID: room.UserID, Name: room.Name}
}

/*
Invite makes an invitation into a room here, and the link to send.

The link names this installation by the origin the request came from, which the
surface has already checked is exactly the address Convia was reached at.
*/
func (handler *SessionHandler) Invite(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	var body inviteRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}

	home, err := handler.naming(request)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	invitation, err := handler.service.Invite(request.Context(), principal, request.PathValue("room_id"), body.Handle)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	write(handler.logger, response, request, http.StatusCreated, representInvitation(home, invitation))
}

/*
Pending lists the invitations this person made into a room that still work.

The links name this installation by the address the request reached, the way
the surface's origin check reconstructs it: a page reading a list sends no
Origin, and the address it reached is the one it would have sent.
*/
func (handler *SessionHandler) Pending(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	home, err := handler.naming(request)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	pending, err := handler.service.Pending(request.Context(), principal, request.PathValue("room_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	body := invitationsResponse{Data: make([]invitationResponse, 0, len(pending))}
	for _, invitation := range pending {
		body.Data = append(body.Data, representInvitation(home, invitation))
	}
	write(handler.logger, response, request, http.StatusOK, body)
}

// homeReached prefers the browser's public origin, falling back to the address
// reached by non-browser clients such as the desktop application.
func homeReached(request *http.Request) (string, error) {
	if origin := request.Header.Get("Origin"); origin != "" {
		return ParseHome(origin)
	}
	return ParseHome(requestOrigin(request))
}

// requestOrigin is the origin a request reached, from its Host and its scheme.
func requestOrigin(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + request.Host
}

func representInvitation(home string, invitation Invitation) invitationResponse {
	return invitationResponse{
		ID:        invitation.ID,
		Link:      Link{Home: home, InvitationID: invitation.ID}.String(),
		Invitee:   invitation.InviteeHandle(),
		ExpiresAt: api.FormatTimestamp(invitation.ExpiresAt),
	}
}

// Revoke withdraws an invitation this person made.
func (handler *SessionHandler) Revoke(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	if err := handler.service.Revoke(request.Context(), principal, request.PathValue("invitation_id")); err != nil {
		writeError(handler.logger, response, request, err)
		return
	}
	private(response)
	response.WriteHeader(http.StatusNoContent)
}

// Look asks the home of a link what it is an invitation to.
func (handler *SessionHandler) Look(response http.ResponseWriter, request *http.Request) {
	principal, identity, ok := handler.signingAs(response, request)
	if !ok {
		return
	}

	var body linkRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}

	link, preview, err := handler.service.Look(request.Context(), handler.ours(request), principal, identity, body.Link)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	write(handler.logger, response, request, http.StatusOK, lookResponse{
		Home:      link.Home,
		RoomName:  preview.RoomName,
		Inviter:   preview.Inviter,
		Invitee:   preview.Invitee,
		ExpiresAt: api.FormatTimestamp(preview.ExpiresAt),
	})
}

// Join accepts an invitation link and remembers the room.
func (handler *SessionHandler) Join(response http.ResponseWriter, request *http.Request) {
	principal, identity, ok := handler.signingAs(response, request)
	if !ok {
		return
	}

	var body linkRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}

	account, err := handler.identities.Account(request.Context(), principal.AccountID)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	joined, err := handler.service.Join(request.Context(), handler.ours(request), account, identity, body.Link)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	result := joinedResponse{RoomID: joined.RoomID, RoomName: joined.RoomName}
	if joined.Remote != nil {
		represented := representRemoteRoom(*joined.Remote)
		result.RemoteRoom = &represented
	}
	write(handler.logger, response, request, http.StatusCreated, result)
}

// RemoteRooms lists the rooms elsewhere this person belongs to.
func (handler *SessionHandler) RemoteRooms(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	found, err := handler.service.RemoteRooms(request.Context(), principal.AccountID)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	body := remoteRoomsResponse{Data: make([]remoteRoomResponse, 0, len(found))}
	for _, room := range found {
		body.Data = append(body.Data, representRemoteRoom(room))
	}
	write(handler.logger, response, request, http.StatusOK, body)
}

// History reads a window of a remote room's conversation.
func (handler *SessionHandler) History(response http.ResponseWriter, request *http.Request) {
	handler.relay(response, request, http.MethodGet, func(remote RemoteRoom) (string, bool) {
		return roomTarget(remote, "/messages") + forwardQuery(request, "limit", "cursor", "direction"), true
	}, nil)
}

// Post says something in a remote room.
func (handler *SessionHandler) Post(response http.ResponseWriter, request *http.Request) {
	var body relayedMessage
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}
	handler.relay(response, request, http.MethodPost, func(remote RemoteRoom) (string, bool) {
		return roomTarget(remote, "/messages"), true
	}, body)
}

// Edit changes something this person said in a remote room.
func (handler *SessionHandler) Edit(response http.ResponseWriter, request *http.Request) {
	var body relayedMessage
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}
	handler.relay(response, request, http.MethodPatch, func(RemoteRoom) (string, bool) {
		return messageTarget(request, "")
	}, body)
}

// Withdraw takes back something this person said in a remote room.
func (handler *SessionHandler) Withdraw(response http.ResponseWriter, request *http.Request) {
	handler.relay(response, request, http.MethodPost, func(RemoteRoom) (string, bool) {
		return messageTarget(request, "/delete")
	}, nil)
}

// ReadState reads how far this person has read in a remote room.
func (handler *SessionHandler) ReadState(response http.ResponseWriter, request *http.Request) {
	handler.relay(response, request, http.MethodGet, func(remote RemoteRoom) (string, bool) {
		return roomTarget(remote, "/read_state"), true
	}, nil)
}

// MarkRead records how far this person has read in a remote room.
func (handler *SessionHandler) MarkRead(response http.ResponseWriter, request *http.Request) {
	var body relayedReadState
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		writeFailure(handler.logger, response, request, failure)
		return
	}
	handler.relay(response, request, http.MethodPut, func(remote RemoteRoom) (string, bool) {
		return roomTarget(remote, "/read_state"), true
	}, body)
}

// Members reads who is in a remote room.
func (handler *SessionHandler) Members(response http.ResponseWriter, request *http.Request) {
	handler.relay(response, request, http.MethodGet, func(remote RemoteRoom) (string, bool) {
		return roomTarget(remote, "/members") + forwardQuery(request, "limit", "cursor"), true
	}, nil)
}

// Leave takes this person out of a remote room.
func (handler *SessionHandler) Leave(response http.ResponseWriter, request *http.Request) {
	principal, identity, ok := handler.signingAs(response, request)
	if !ok {
		return
	}

	remote, err := handler.service.RemoteRoom(request.Context(), principal.AccountID, request.PathValue("remote_room_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	if err := handler.service.Leave(request.Context(), identity, remote); err != nil {
		writeError(handler.logger, response, request, err)
		return
	}
	private(response)
	response.WriteHeader(http.StatusNoContent)
}

/*
Forget drops a remote room from this installation without asking its home.

It needs no key, because nothing is signed: it is for a home that will not
answer. The person stays a member there, which the interface says before it
sends this, and which is why leaving never does it on its own.
*/
func (handler *SessionHandler) Forget(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	remote, err := handler.service.RemoteRoom(request.Context(), principal.AccountID, request.PathValue("remote_room_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	if err := handler.service.Forget(request.Context(), remote); err != nil {
		writeError(handler.logger, response, request, err)
		return
	}
	private(response)
	response.WriteHeader(http.StatusNoContent)
}

/*
relay sends one request about a remote room to its home and hands back what it said.

The body is re-encoded from what this installation decoded, never forwarded as
it arrived, so nothing a browser sends reaches another installation unless this
one understood it. A success is passed back as the JSON object it was; a refusal
is translated — see writeRelayed.
*/
func (handler *SessionHandler) relay(
	response http.ResponseWriter,
	request *http.Request,
	method string,
	target func(RemoteRoom) (string, bool),
	body any,
) {
	principal, identity, ok := handler.signingAs(response, request)
	if !ok {
		return
	}

	remote, err := handler.service.RemoteRoom(request.Context(), principal.AccountID, request.PathValue("remote_room_id"))
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}

	path, valid := target(remote)
	if !valid {
		writeError(handler.logger, response, request, ErrRemoteRoomNotFound)
		return
	}

	var payload []byte
	if body != nil {
		if payload, err = json.Marshal(body); err != nil {
			writeError(handler.logger, response, request, err)
			return
		}
	}

	answer, err := handler.service.Relay(request.Context(), identity, remote, method, path, payload)
	if err != nil {
		writeError(handler.logger, response, request, err)
		return
	}
	handler.writeRelayed(response, request, answer)
}

/*
writeRelayed answers with what a home said, translated for this installation.

**A home's 401 never becomes this installation's 401.** The page treats a 401 as
its own session ending and returns to the sign-in form; a home that stopped
recognizing somebody has said nothing about their session here. It is a 403.
*/
func (handler *SessionHandler) writeRelayed(response http.ResponseWriter, request *http.Request, answer Response) {
	private(response)

	switch {
	case answer.Status == http.StatusNoContent:
		response.WriteHeader(http.StatusNoContent)
		return
	case answer.Status >= 200 && answer.Status < 300:
		response.Header().Set("Content-Type", api.ContentTypeJSON)
		response.WriteHeader(answer.Status)
		if _, err := response.Write(answer.Body); err != nil {
			handler.logger.Error("write relayed response", "error", err,
				"request_id", api.RequestIDFromContext(request.Context()))
		}
		return
	}

	var failure *api.Failure
	switch answer.Status {
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge:
		code := api.CodeInvalidRequest
		if answer.Status == http.StatusConflict {
			code = api.CodeConflict
		}
		if answer.Status == http.StatusRequestEntityTooLarge {
			code = api.CodePayloadTooLarge
		}
		failure = api.NewFailure(answer.Status, code, "The other Convia did not accept that.")
	case http.StatusNotFound:
		failure = api.NewFailure(http.StatusNotFound, api.CodeNotFound, "That is not there on the other Convia.")
	case http.StatusUnauthorized, http.StatusForbidden:
		failure = api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The other Convia no longer lets you into that room.")
	default:
		failure = api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"The other Convia could not be reached. Try again in a moment.")
	}
	writeFailure(handler.logger, response, request, failure)
}

func (handler *SessionHandler) principal(response http.ResponseWriter, request *http.Request) (sessions.Principal, bool) {
	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("a session route was reached without a session",
			"method", request.Method, "request_id", api.RequestIDFromContext(request.Context()))
		writeFailure(handler.logger, response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return sessions.Principal{}, false
	}
	return principal, true
}

// signingAs returns the session's principal and the key it holds.
func (handler *SessionHandler) signingAs(
	response http.ResponseWriter,
	request *http.Request,
) (sessions.Principal, accounts.Identity, bool) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return sessions.Principal{}, accounts.Identity{}, false
	}

	token, _ := sessions.Present(request)
	identity, err := handler.identities.Identity(request.Context(), token)
	if errors.Is(err, sessions.ErrUnauthenticated) {
		writeFailure(handler.logger, response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return sessions.Principal{}, accounts.Identity{}, false
	}
	if err != nil {
		writeError(handler.logger, response, request, err)
		return sessions.Principal{}, accounts.Identity{}, false
	}
	return principal, identity, true
}

func roomTarget(remote RemoteRoom, suffix string) string {
	return "/v1/peer/rooms/" + remote.RoomID + suffix
}

// messageTarget addresses one message at the home, if its identifier has the
// shape one can have. Nothing else from the path reaches the home's URL.
func messageTarget(request *http.Request, suffix string) (string, bool) {
	id := request.PathValue("message_id")
	if !messages.ValidID(id) {
		return "", false
	}
	return "/v1/peer/messages/" + id + suffix, true
}

// forwardQuery copies the named query parameters, and only those.
func forwardQuery(request *http.Request, names ...string) string {
	forwarded := url.Values{}
	for _, name := range names {
		if value := request.URL.Query().Get(name); value != "" {
			forwarded.Set(name, value)
		}
	}
	if len(forwarded) == 0 {
		return ""
	}
	return "?" + forwarded.Encode()
}

func signerOf(logger *slog.Logger, response http.ResponseWriter, request *http.Request) (Signer, bool) {
	signer, found := SignerFromContext(request.Context())
	if !found {
		logger.Error("a route between installations was reached without a signature",
			"method", request.Method, "request_id", api.RequestIDFromContext(request.Context()))
		writeFailure(logger, response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return Signer{}, false
	}
	return signer, true
}

// writeError translates this package's errors into what a caller is told.
func writeError(logger *slog.Logger, response http.ResponseWriter, request *http.Request, err error) {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))
	case errors.Is(err, ErrNotFound):
		writeFailure(logger, response, request, api.NewFailure(http.StatusNotFound, api.CodeNotFound,
			"That invitation cannot be used. It may have expired, been used, or been meant for somebody else."))
	case errors.Is(err, ErrRoomNotFound):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The room does not exist."))
	case errors.Is(err, ErrRemoteRoomNotFound):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "You are not in that room."))
	case errors.Is(err, ErrAlreadyMember):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusConflict, api.CodeConflict, "That person is already in the room."))
	case errors.Is(err, ErrBanned):
		writeFailure(logger, response, request,
			api.NewFailure(http.StatusForbidden, api.CodeForbidden, "That person cannot be invited to this room."))
	case errors.Is(err, ErrUnreachable):
		logger.Warn("another installation could not be reached", "error", err,
			"request_id", api.RequestIDFromContext(request.Context()))
		writeFailure(logger, response, request, api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"The other Convia could not be reached. Try again in a moment."))
	default:
		logger.Error("request between installations failed", "error", err, "method", request.Method,
			"request_id", api.RequestIDFromContext(request.Context()))
		writeFailure(logger, response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}

func write(logger *slog.Logger, response http.ResponseWriter, request *http.Request, status int, body any) {
	private(response)
	if err := api.Write(response, status, body); err != nil {
		logger.Error("write response", "error", err, "request_id", api.RequestIDFromContext(request.Context()))
	}
}

func writeFailure(logger *slog.Logger, response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	private(response)
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write failure response", "error", err,
			"request_id", api.RequestIDFromContext(request.Context()))
	}
}

// private marks a response as one person's, as every session route's is.
func private(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
}
