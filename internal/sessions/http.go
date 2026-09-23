package sessions

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/accounts"
	"convia/internal/api"
)

// Handler exposes the session surface over HTTP.
type Handler struct {
	logger  *slog.Logger
	service service
}

func NewHandler(logger *slog.Logger, service service) *Handler {
	return &Handler{logger: logger, service: service}
}

// signInRequest is what a person sends to sign in.
type signInRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// registrationRequest is what a person sends to create their account.
type registrationRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// passwordRequest is what a person sends to change their password.
type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

/*
meResponse is what Convia says about the person who is signed in.

It names the user identifier as well as the account, because that is the
identifier every other part of Convia addresses a person by — so a client
reading a roster can recognize itself in it without a second request. The
handle is how this person is named to somebody else, and it is rendered here so
that no client has to reimplement the check character to show it.

There is no session identifier, no expiry, and no list of other sessions. A
page does not need to know when its own credential lapses: it finds out by
being refused, which is the only answer that cannot be stale.
*/
type meResponse struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Handle    string `json:"handle"`
	/*
		Token is the session, and it is present only for a client that cannot
		hold Convia's cookie — its own application, which keeps it in the
		operating system's keychain. A browser is answered with the cookie and
		never with this, so a page has nothing to store and nothing to leak.
	*/
	Token string `json:"token,omitempty"`
}

/*
browser reports a caller that is one.

A browser sends `Origin` on every request that changes something, and
`Sec-Fetch-Site` on every request at all; a program sends neither unless it
chooses to. The distinction decides how a new session is handed over, and
nothing else: what a client may do is decided by the session, not by what it
is. See docs/adr/0019.
*/
func browser(request *http.Request) bool {
	return request.Header.Get("Origin") != "" || request.Header.Get("Sec-Fetch-Site") != ""
}

/*
service is the behavior the HTTP layer consumes.

It is declared here rather than taking *Service so the transport can be tested
without a database, and so the handler cannot reach past what the surface is
meant to do.
*/
type service interface {
	Begin(ctx context.Context, username string, password accounts.Password) (Session, string, error)
	Register(ctx context.Context, username string, password accounts.Password) (Session, string, error)
	End(ctx context.Context, sessionID string) error
	EndAll(ctx context.Context, accountID string) (int, error)
	Account(ctx context.Context, accountID string) (accounts.Account, error)
	ChangePassword(ctx context.Context, principal Principal, current, next accounts.Password) (Session, string, error)
}

/*
SignIn exchanges a username and a password for a session.

It is one of two routes on this surface that are not authenticated, which makes
it one that needs every other protection: the origin is checked, the failure
budget is charged by the middleware in front of it, and the account domain
answers every failure identically.
*/
func (handler *Handler) SignIn(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	var body signInRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	session, token, err := handler.service.Begin(request.Context(), body.Username, accounts.Password(body.Password))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.signedIn(response, request, session, token)
}

/*
Register creates an account for the person asking and signs them in.

It is the other unauthenticated route, and it is guarded differently from
signing in: every attempt is charged to the caller's address, not only the ones
that fail, because what it protects against is somebody succeeding too often.
That is the middleware's job; this answers a taken username with `409`, which
necessarily tells the caller the name exists. A registration form cannot avoid
saying so, and the username is half of a handle that is meant to be shared.
*/
func (handler *Handler) Register(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	var body registrationRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	session, token, err := handler.service.Register(request.Context(), body.Username, accounts.Password(body.Password))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.signedIn(response, request, session, token)
}

/*
signedIn hands over a new session and says who it belongs to.

A browser is given the cookie, which it cannot read and cannot be tricked into
sending elsewhere. Anything else is given the token in the answer, because a
cookie of Convia's origin is not something it can hold.
*/
func (handler *Handler) signedIn(response http.ResponseWriter, request *http.Request, session Session, token string) {
	account, err := handler.service.Account(request.Context(), session.AccountID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := represent(account)
	if browser(request) {
		Set(response, token)
	} else {
		body.Token = token
	}

	handler.write(response, request, http.StatusCreated, body)
}

/*
SignOut ends the session the request carried.

It answers the same way whether or not the session was live, so that it cannot
be used to ask whether one is. The cookie is cleared and the row is revoked;
only the second of those is the sign-out.
*/
func (handler *Handler) SignOut(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}
	if err := handler.service.End(request.Context(), principal.SessionID); err != nil {
		handler.writeError(response, request, err)
		return
	}

	Clear(response)
	response.WriteHeader(http.StatusNoContent)
}

/*
SignOutEverywhere ends every session the account holds, including this one.

Somebody reaching for this believes a device was lost. Sparing the browser
asking would mean the operation did not do what it says.
*/
func (handler *Handler) SignOutEverywhere(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}
	if _, err := handler.service.EndAll(request.Context(), principal.AccountID); err != nil {
		handler.writeError(response, request, err)
		return
	}

	Clear(response)
	response.WriteHeader(http.StatusNoContent)
}

/*
Me reports who is signed in.

It is the first request a freshly loaded page makes, every time, to find out
whether it should show a sign-in form. That is why an absent cookie must be an
ordinary 401 rather than anything that costs a budget or writes an error to a
log — see the middleware.
*/
func (handler *Handler) Me(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}

	account, err := handler.service.Account(request.Context(), principal.AccountID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, represent(account))
}

/*
ChangePassword replaces a password and rotates the session it was done from.

The response carries a new cookie. Everything else the account holds is signed
out, because the ordinary reason to change a password is believing somebody
else has it.
*/
func (handler *Handler) ChangePassword(response http.ResponseWriter, request *http.Request) {
	handler.private(response)

	principal, ok := handler.principal(response, request)
	if !ok {
		return
	}
	var body passwordRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	_, rotated, err := handler.service.ChangePassword(request.Context(), principal,
		accounts.Password(body.CurrentPassword), accounts.Password(body.NewPassword))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	/*
		The session is rotated either way, and each client is given the new one
		in the form it holds: a browser its cookie, and an application the
		token in the answer. A client left with the old one would be signed out
		by its own password change.
	*/
	if browser(request) {
		Set(response, rotated)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	handler.write(response, request, http.StatusOK, rotatedSession{Token: rotated})
}

// rotatedSession is the new session a password change produced, for a client
// that holds its own.
type rotatedSession struct {
	Token string `json:"token"`
}

/*
principal reads the verified session a request carries.

A request reaching an authenticated handler without one was routed without the
middleware, which is a wiring mistake rather than a client error. It is refused
as unauthenticated, because that is the answer that grants nothing, and logged
so the mistake is visible.
*/
func (handler *Handler) principal(response http.ResponseWriter, request *http.Request) (Principal, bool) {
	principal, found := PrincipalFromContext(request.Context())
	if !found {
		path := strings.ReplaceAll(request.URL.Path, "\n", "")
		path = strings.ReplaceAll(path, "\r", "")
		handler.logger.Error("authenticated route reached without a session",
			"method", request.Method,
			"path", path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable session."))
		return Principal{}, false
	}
	return principal, true
}

/*
private marks a response as belonging to one person.

`no-store` is the one that matters: Go sends no cache directives of its own, and
HTTP lets a cache keep a 200 that carries none. Without it, signing out on a
shared machine and pressing Back can show the previous person's data from the
browser's own store.

`Vary: Cookie` is redundant while `no-store` is set, and it is set anyway,
because the failure it covers is somebody forgetting `no-store` on one route.
*/
func (handler *Handler) private(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")
}

func represent(account accounts.Account) meResponse {
	return meResponse{
		AccountID: account.ID,
		UserID:    account.UserID,
		Username:  account.Username,
		Handle:    account.Handle(),
	}
}

func (handler *Handler) write(response http.ResponseWriter, request *http.Request, status int, body any) {
	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write session response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

func (handler *Handler) writeFailure(response http.ResponseWriter, request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write error response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}

/*
writeError translates a domain failure into the answer a caller gets.

Signing in refuses every failure the same way, and it says nothing about which
half was wrong. An account that does not exist, a wrong password, a suspended
account, and a digest Convia cannot read are one answer, because any difference
between them is a way to find out more about an account than its name.

Changing a password is different: the caller is already the account, so a wrong
current password is said as what it is, and not as a session that ended.
*/
func (handler *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var validation accounts.ValidationError

	switch {
	case errors.As(err, &validation):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, accounts.ErrUnauthenticated):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
				"The username and password do not match an account."))

	case errors.Is(err, accounts.ErrWrongPassword):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusForbidden, api.CodeWrongPassword,
				"The current password is not right."))

	case errors.Is(err, accounts.ErrUsernameTaken):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusConflict, api.CodeConflict, "That username is already taken."))

	case errors.Is(err, ErrUnauthenticated):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusUnauthorized, api.CodeUnauthenticated,
				"The request did not carry a usable session."))

	case errors.Is(err, accounts.ErrBusy):
		/*
			Not a refusal of the credentials, and it must never read as one.
			Retry-After is short because the condition is contention rather
			than an outage: the passwords ahead of this one finish in
			milliseconds.
		*/
		response.Header().Set("Retry-After", "1")
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusServiceUnavailable, api.CodeUnavailable,
				"Convia is verifying too many passwords at once. Try again in a moment."))

	case errors.Is(err, accounts.ErrNotFound):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusNotFound, api.CodeNotFound, "The account does not exist."))

	default:
		path := strings.ReplaceAll(request.URL.Path, "\n", "")
		path = strings.ReplaceAll(path, "\r", "")
		handler.logger.Error("session request failed",
			"error", err,
			"method", request.Method,
			"path", path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}
