package invitations

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/secret"
)

/*
TenantHandler exposes the caller's own invitations over HTTP.

These routes carry no application in the path. The tenant comes from the
verified credential, so a caller cannot address another application's
invitations even by mistake: there is no request field that could name one.
*/
type TenantHandler struct {
	logger  *slog.Logger
	service service
}

func NewTenantHandler(logger *slog.Logger, service service) *TenantHandler {
	return &TenantHandler{logger: logger, service: service}
}

/*
HolderHandler exposes what the holder of an invitation may do with it.

It is a surface of its own, authenticated by the invitation rather than by an
application key, and that separation is the entire point of the milestone: the
party presenting an invitation must not be the party that granted it.
*/
type HolderHandler struct {
	logger  *slog.Logger
	service service
}

func NewHolderHandler(logger *slog.Logger, service service) *HolderHandler {
	return &HolderHandler{logger: logger, service: service}
}

/*
invitationResponse is one invitation as the application sees it.

The secret is absent, because Convia does not have it: only a digest is stored,
and the plaintext exists once, in the response to the request that created it.
*/
type invitationResponse struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	CallID        string `json:"call_id"`
	Guest         bool   `json:"guest"`
	UserID        string `json:"user_id,omitempty"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expires_at"`
	RedeemedAt    string `json:"redeemed_at,omitempty"`
	ParticipantID string `json:"participant_id,omitempty"`
	DeclinedAt    string `json:"declined_at,omitempty"`
	RevokedAt     string `json:"revoked_at,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

/*
issuedInvitationResponse is the one response that carries the secret.

It is a separate type from the ordinary representation rather than the same one
with a field sometimes set. A reader of this file can see exactly which
response can contain a credential, and a handler that returns the ordinary type
has no field to put one in.
*/
type issuedInvitationResponse struct {
	invitationResponse
	Token string `json:"token"`
}

type invitationPageResponse struct {
	Data       []invitationResponse `json:"data"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

/*
redemptionResponse is what the holder of an invitation receives.

It carries the participation the redemption produced and the credential to
connect with, because the holder has no way to obtain either on their own. The
connection fields are the same Convia-owned words the join session uses, and
they name no provider.
*/
type redemptionResponse struct {
	InvitationID  string `json:"invitation_id"`
	ParticipantID string `json:"participant_id"`
	CallID        string `json:"call_id"`
	Role          string `json:"role"`
	MediaURL      string `json:"media_url"`
	MediaToken    string `json:"media_token"`
	ExpiresAt     string `json:"expires_at"`
}

type createInvitationRequest struct {
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Guest     bool   `json:"guest"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

func represent(invitation Invitation) invitationResponse {
	body := invitationResponse{
		ID:            invitation.ID,
		ApplicationID: invitation.ApplicationID,
		CallID:        invitation.CallID,
		Guest:         invitation.Guest(),
		UserID:        invitation.UserID,
		Role:          invitation.Role,
		Status:        string(invitation.Status(time.Now().UTC())),
		ExpiresAt:     api.FormatTimestamp(invitation.ExpiresAt),
		ParticipantID: invitation.ParticipantID,
		CreatedAt:     api.FormatTimestamp(invitation.CreatedAt),
		UpdatedAt:     api.FormatTimestamp(invitation.UpdatedAt),
	}

	if invitation.RedeemedAt != nil {
		body.RedeemedAt = api.FormatTimestamp(*invitation.RedeemedAt)
	}

	if invitation.DeclinedAt != nil {
		body.DeclinedAt = api.FormatTimestamp(*invitation.DeclinedAt)
	}

	if invitation.RevokedAt != nil {
		body.RevokedAt = api.FormatTimestamp(*invitation.RevokedAt)
	}

	return body
}

/*
representIssued renders the invitation together with the key its holder
presents.

Render is called here and nowhere else. The secret exists in memory for the
length of one request and is never written down, so this line is the single
reviewable point where a new invitation becomes something somebody can hold.
*/
func representIssued(invitation Invitation, value secret.Value) issuedInvitationResponse {
	return issuedInvitationResponse{
		invitationResponse: represent(invitation),
		Token:              Render(invitation.ID, value),
	}
}

func representRedemption(invitation Invitation, participant participants.Participant,
	credential media.Credential) redemptionResponse {

	return redemptionResponse{
		InvitationID:  invitation.ID,
		ParticipantID: participant.ID,
		CallID:        participant.CallID,
		Role:          string(participant.Role),
		MediaURL:      credential.URL,
		MediaToken:    credential.Token.Reveal(),
		ExpiresAt:     api.FormatTimestamp(credential.ExpiresAt),
	}
}

func (handler *TenantHandler) authorized(response http.ResponseWriter, request *http.Request) (*Authorized, bool) {
	principal, found := credentials.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("authenticated route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return nil, false
	}
	return Authorize(handler.service, principal), true
}

// Issue creates an invitation to one of the caller's own calls.
func (handler *TenantHandler) Issue(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body createInvitationRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	invitation, value, err := authorized.Issue(request.Context(), request.PathValue("call_id"), Request{
		UserID:    body.UserID,
		Role:      body.Role,
		Guest:     body.Guest,
		ExpiresIn: time.Duration(body.ExpiresIn) * time.Second,
	})

	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, representIssued(invitation, value))
}

func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	invitation, err := authorized.Get(request.Context(), request.PathValue("invitation_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(invitation))
}

func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options := ListOptions{
		Cursor: request.URL.Query().Get("cursor"),
		CallID: request.URL.Query().Get("call_id"),
	}

	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeError(response, request,
				ValidationError{Field: "limit", Message: "The limit must be a number."})
			return
		}
		options.Limit = limit
	}

	page, err := authorized.List(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := invitationPageResponse{Data: make([]invitationResponse, 0, len(page.Invitations)),
		NextCursor: page.NextCursor}
	for _, invitation := range page.Invitations {
		body.Data = append(body.Data, represent(invitation))
	}

	handler.write(response, request, http.StatusOK, body)
}

// Revoke withdraws one of the caller's own invitations.
func (handler *TenantHandler) Revoke(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	invitation, err := authorized.Revoke(request.Context(), request.PathValue("invitation_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(invitation))
}

/*
holder returns the invitation the request was authenticated with.

A request that reaches here without one was routed without the invitation
middleware, which is a wiring mistake rather than a client error.
*/
func (handler *HolderHandler) holder(response http.ResponseWriter, request *http.Request) (Invitation, bool) {
	invitation, found := HolderFromContext(request.Context())
	if !found {
		handler.logger.Error("invitation route reached without a verified invitation",
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable invitation."))
		return Invitation{}, false
	}
	return invitation, true
}

/*
Redeem turns the presented invitation into a presence and the means to connect.

There is no identifier in the path. The invitation names itself, and asking the
holder to repeat it would let them address one they do not hold.
*/
func (handler *HolderHandler) Redeem(response http.ResponseWriter, request *http.Request) {
	invitation, ok := handler.holder(response, request)
	if !ok {
		return
	}

	redeemed, participant, credential, err := handler.service.Redeem(request.Context(), invitation)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated,
		representRedemption(redeemed, participant, credential))
}

// Decline records that the holder of the presented invitation said no.
func (handler *HolderHandler) Decline(response http.ResponseWriter, request *http.Request) {
	invitation, ok := handler.holder(response, request)
	if !ok {
		return
	}

	declined, err := handler.service.Decline(request.Context(), invitation)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	/*
		The holder is shown the invitation they declined, not the application's
		full view of it. They already know everything in it — they were holding
		it — and it names nothing about anybody else.
	*/
	handler.write(response, request, http.StatusOK, represent(declined))
}

func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

func (handler *HolderHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	handler.writeFailure(response, request, failureFor(err, handler.logger, request))
}

/*
failureFor maps an invitations error onto the public schema.

It is shared by both surfaces so that the application and the holder of an
invitation cannot be told different things about the same domain error.
*/
func failureFor(err error, logger *slog.Logger, request *http.Request) *api.Failure {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		return api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message)

	case errors.Is(err, ErrForbidden):
		return api.NewFailure(http.StatusForbidden, api.CodeForbidden,
			"The credential does not carry the scope this operation requires.")

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
			"The requested invitation does not exist.")

	/*
		Expired, revoked, and declined are one answer. The holder is not the
		application that issued the invitation, and distinguishing them would
		let anyone with a dead link learn whether it was withdrawn, whether it
		merely aged out, or whether somebody said no on their behalf.
	*/
	case errors.Is(err, ErrUnusable):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The invitation can no longer be used.")

	case errors.Is(err, ErrAlreadyRedeemed):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The invitation has already been redeemed and cannot be declined.")

	case errors.Is(err, ErrCallEnded):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The call has ended.")

	// Everything about who may actually be in a call is the participants'
	// decision, and its answers are reported in its own words.
	case errors.Is(err, participants.ErrRemoved):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The user was removed from this call and cannot rejoin it.")

	case errors.Is(err, participants.ErrUserSuspended):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The user is not active and cannot join a call.")

	case errors.Is(err, participants.ErrCallFull):
		return api.NewFailure(http.StatusConflict, api.CodeConflict,
			"The call has reached the capacity its room declared.")

	case errors.Is(err, participants.ErrNoMediaPlane):
		return api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
			"This deployment cannot carry media, so there is nothing to connect to.")

	default:
		logger.Error("invitation request failed",
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
	writeBody(handler.logger, response, request, status, body)
}

func (handler *HolderHandler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	writeBody(handler.logger, response, request, status, body)
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	writeFailureBody(handler.logger, response, request, failure)
}

func (handler *HolderHandler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	writeFailureBody(handler.logger, response, request, failure)
}

func writeBody(logger *slog.Logger, response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		logger.Error("write response",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func writeFailureBody(logger *slog.Logger, response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.Error("write failure",
			"error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
