package export

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/sessions"
)

// ContentType is newline-delimited JSON, one object per line.
const ContentType = "application/x-ndjson"

// writer is the service the handlers need.
type writer interface {
	Write(ctx context.Context, out io.Writer, applicationID, userID string) error
}

/*
SessionHandler serves a signed-in person asking for their own data.

It names no user. The person is taken from the session, so there is no request
field that could name somebody else -- the same move the tenant surface makes
with the application, and for the same reason.
*/
type SessionHandler struct {
	logger  *slog.Logger
	service writer

	// application is the first-party tenant a signed-in person belongs to.
	// A session names a person, not a tenant, so the tenant comes from here.
	application string
}

func NewSessionHandler(logger *slog.Logger, service writer, application string) *SessionHandler {
	return &SessionHandler{logger: logger, service: service, application: application}
}

// Mine streams everything Convia holds about the signed-in person.
func (handler *SessionHandler) Mine(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Add("Vary", "Cookie")

	principal, found := sessions.PrincipalFromContext(request.Context())
	if !found {
		writeFailure(handler.logger, response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable session."))
		return
	}

	stream(handler.logger, handler.service, response, request, handler.application, principal.UserID)
}

/*
TenantHandler serves an application asking for one of its own people.

Both surfaces exist because the obligation does. A person asks Convia for their
data because Convia holds it; an application asks because **the application is
the controller** for the person it resolved, and answering a request it received
means asking Convia for its part. Serving only one of the two would leave
whichever party asked the other unable to answer.
*/
type TenantHandler struct {
	logger  *slog.Logger
	service writer
}

func NewTenantHandler(logger *slog.Logger, service writer) *TenantHandler {
	return &TenantHandler{logger: logger, service: service}
}

/*
Theirs streams everything Convia holds about one of the caller's users.

The application comes from the verified principal and the user from the path, so
a caller can only ever reach its own people: a valid identifier belonging to
another tenant finds nothing, because the tenant is part of the lookup rather
than a filter applied afterwards.
*/
func (handler *TenantHandler) Theirs(response http.ResponseWriter, request *http.Request) {
	principal, found := credentials.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.ErrorContext(request.Context(), "authenticated route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
		)
		writeFailure(handler.logger, response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return
	}

	/*
		Reading somebody's whole history is a read, and it is the broadest one
		on this surface -- so it is gated on the scope that already means "read
		this tenant's people" rather than on a scope of its own. A new scope
		would be one more thing to grant, and every key that can read a user can
		already read everything this assembles.
	*/
	if !principal.Allows(credentials.ScopeUsersRead) {
		writeFailure(handler.logger, response, request, api.NewFailure(http.StatusForbidden,
			api.CodeForbidden, "This credential may not read users."))
		return
	}

	stream(handler.logger, handler.service, response, request,
		principal.ApplicationID, request.PathValue("user_id"))
}

/*
stream writes the export, or an error if nothing has been written yet.

**The status goes out with the first byte and cannot be taken back.** So a
failure is answered properly only while the body is still empty; after that, all
this can do is stop writing and log, and the missing terminator line is what
tells the reader the export is incomplete. That is why there is a terminator.
*/
func stream(logger *slog.Logger, service writer, response http.ResponseWriter,
	request *http.Request, applicationID, userID string) {
	recorder := &firstByte{ResponseWriter: response}

	err := service.Write(request.Context(), recorder, applicationID, userID)
	if err == nil {
		return
	}

	if recorder.written {
		/*
			Truncated. The reader will not find a terminator, which is how they
			find out; there is nothing useful to send them now, and continuing
			would append valid-looking lines to an export that already has a
			hole in it.
		*/
		logger.ErrorContext(request.Context(), "an export failed after it had started",
			"error", err,
			"user_id", userID,
		)
		return
	}

	if errors.Is(err, ErrNotFound) {
		writeFailure(logger, response, request, api.NewFailure(http.StatusNotFound,
			api.CodeNotFound, "No such user."))
		return
	}

	path := strings.ReplaceAll(request.URL.Path, "\n", "")
	path = strings.ReplaceAll(path, "\r", "")
	logger.ErrorContext(request.Context(), "an export could not be assembled",
		"error", err,
		"method", request.Method,
		"path", path,
	)
	writeFailure(logger, response, request, api.NewFailure(http.StatusInternalServerError,
		api.CodeInternal, "The server encountered an unexpected condition."))
}

/*
firstByte remembers whether anything has reached the client, and sets the
content type on the way past.

The header is written here rather than before the call, because writing it early
would commit the response to being an export before the person has been found --
and then a missing user would arrive as a 200 with a body that is not NDJSON.
*/
type firstByte struct {
	http.ResponseWriter
	written bool
}

func (recorder *firstByte) Write(payload []byte) (int, error) {
	if !recorder.written {
		recorder.Header().Set("Content-Type", ContentType)
		recorder.written = true
	}

	written, err := recorder.ResponseWriter.Write(payload)

	/*
		Flushed per write, so a long export arrives as it is assembled rather
		than sitting in a buffer until the end. A person watching a download of
		their own data should see it moving.
	*/
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return written, err
}

func writeFailure(logger *slog.Logger, response http.ResponseWriter,
	request *http.Request, failure *api.Failure) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		logger.ErrorContext(request.Context(), "write error response",
			"error", err,
		)
	}
}
