package presence

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"convia/internal/api"
	"convia/internal/sessions"
)

/*
What a person signed in to Convia's own product says about themselves, and
learns about the people they share a room with.

The product owner decided the shape: **each open page asserts the person's
presence** as a device of its own, with the same service and the same expiry an
application's devices have. The page says available, busy or away — away
because the person chose it or because the page went idle — and a page that
closes lapses on the timer, or says so on the way out.

A person learns the presence of the people they share a room with here, and of
nobody else. Visitors from other installations are left out: their pages assert
presence to their own installation, so this one would only ever call them
offline.
*/

// neighbours is how the session surface learns whom a person may see.
type neighbours interface {
	/*
		LocalNeighbours returns those of the candidates who share a room that
		still exists with the person and have an account on this installation.
		The person is included when they are among the candidates.
	*/
	LocalNeighbours(ctx context.Context, applicationID, userID string, candidates []string) ([]string, error)
}

// PersonalHandler serves presence on the session surface.
type PersonalHandler struct {
	logger  *slog.Logger
	service service
	rooms   neighbours
	tenant  *TenantHandler
}

func NewPersonalHandler(logger *slog.Logger, service service, rooms neighbours) *PersonalHandler {
	return &PersonalHandler{logger: logger, service: service, rooms: rooms, tenant: NewTenantHandler(logger, service)}
}

func (handler *PersonalHandler) principal(response http.ResponseWriter, request *http.Request) (sessions.Principal, bool) {
	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.Error("presence reached without a session",
			"request_id", api.RequestIDFromContext(request.Context()))
		handler.tenant.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable session."))
		return sessions.Principal{}, false
	}
	return principal, true
}

/*
Assert records what one of this person's pages says about them. The page names
itself in the path, as an application's device does.
*/
func (handler *PersonalHandler) Assert(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	var body assertRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.tenant.writeFailure(response, request, failure)
		return
	}

	deviceID, err := NormalizeDeviceID(request.PathValue("device_id"))
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}

	state, err := ParseState(body.State)
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}

	lifetime, err := NormalizeLifetime(time.Duration(body.LifetimeSeconds) * time.Second)
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}

	presence, err := handler.service.Assert(request.Context(), principal.ApplicationID, principal.UserID,
		Assertion{DeviceID: deviceID, State: state, Lifetime: lifetime})
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}
	handler.write(response, request, represent(presence))
}

// Withdraw removes what one of this person's pages said, as it closes.
func (handler *PersonalHandler) Withdraw(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	deviceID, err := NormalizeDeviceID(request.PathValue("device_id"))
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}

	presence, err := handler.service.Clear(request.Context(), principal.ApplicationID, principal.UserID, deviceID)
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}
	handler.write(response, request, represent(presence))
}

/*
People reports the presence of the named people this person may see, in the
order asked. Anybody else named is left out rather than refused, so that a list
of names cannot be used to find out which of them exist.
*/
func (handler *PersonalHandler) People(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	named := request.URL.Query()["user_id"]
	if len(named) == 0 || len(named) > MaxUsersPerRead {
		handler.tenant.writeError(response, request, ValidationError{Field: "user_id",
			Message: fmt.Sprintf("Name between one and %d people to read presence for.", MaxUsersPerRead)})
		return
	}

	visible, err := handler.rooms.LocalNeighbours(request.Context(), principal.ApplicationID, principal.UserID, named)
	if err != nil {
		handler.tenant.writeError(response, request, err)
		return
	}

	allowed := make(map[string]bool, len(visible))
	for _, userID := range visible {
		allowed[userID] = true
	}
	var asked []string
	for _, userID := range named {
		if allowed[userID] {
			asked = append(asked, userID)
		}
	}

	body := listResponse{Data: []presenceResponse{}}
	if len(asked) > 0 {
		presences, err := handler.service.GetMany(request.Context(), principal.ApplicationID, asked)
		if err != nil {
			handler.tenant.writeError(response, request, err)
			return
		}
		for _, presence := range presences {
			body.Data = append(body.Data, represent(presence))
		}
	}
	handler.write(response, request, body)
}

// write answers for one person, so no cache may keep it for another.
func (handler *PersonalHandler) write(response http.ResponseWriter, request *http.Request, body any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
	handler.tenant.write(response, request, http.StatusOK, body)
}
