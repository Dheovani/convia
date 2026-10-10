package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"convia/internal/api"
	"convia/internal/audit"
)

const (
	/*
		reasonHeader carries why an operator is doing something high-impact.

		It is a header rather than a body field because the operations that
		need one are mostly a DELETE or a POST with no body at all, and a
		reason that had to be smuggled into a body of its own on half of them
		would be two conventions for one rule. No `X-` prefix, per RFC 6648.
	*/
	reasonHeader = "Convia-Reason"

	/*
		confirmHeader names the thing a destructive request is about, again.

		It is `M21-011`'s confirmation, and what it catches is the mistake a
		bearer key cannot: a script with the wrong variable in a path, a shell
		history line re-run against the wrong tenant. The path says what to
		delete and this has to agree with it, so deleting something takes
		saying which thing twice.
	*/
	confirmHeader = "Convia-Confirm"
)

// deliberation reports a route marked to ask for something it cannot ask for.
func deliberation(entry route) error {
	if entry.surface != surfaceOperator {
		return fmt.Errorf("server: %s %s asks for a reason off the operator surface", entry.method, entry.path)
	}

	if entry.confirms != "" && !strings.Contains(entry.path, "{"+entry.confirms+"}") {
		return fmt.Errorf("server: %s %s confirms %q, which is not in its path", entry.method, entry.path, entry.confirms)
	}

	return nil
}

/*
deliberate refuses a high-impact operator request that does not say why, and a
destructive one that does not name its target twice.

It runs inside authentication, so a caller who cannot authenticate learns that
first and learns nothing about which operations would have asked for more. What
it accepts it puts beside the actor, where the trail reads it: no service is
handed a reason, so none can invent one.

The refusals are `invalid_request` rather than a status of their own. Nothing
about them is a precondition the server could change its mind about: the
request is missing something it was always going to need.
*/
func deliberate(logger *slog.Logger, reasoned bool, confirms string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := request.Context()

		if confirms != "" {
			target := request.PathValue(confirms)
			if presented := request.Header.Get(confirmHeader); presented != target {
				refuseUndeliberate(logger, response, request,
					"This operation deletes something for good. Repeat its identifier in the "+confirmHeader+" header.")
				return
			}
		}

		if reasoned {
			reason, err := audit.NormalizeReason(request.Header.Get(reasonHeader))
			if err != nil {
				message := "This operation requires a " + reasonHeader + " header saying why."
				var validation audit.ValidationError
				if errors.As(err, &validation) && request.Header.Get(reasonHeader) != "" {
					message = "The " + reasonHeader + " header cannot be recorded. " + validation.Message
				}
				refuseUndeliberate(logger, response, request, message)
				return
			}
			ctx = audit.ContextWithReason(ctx, reason)
		}

		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func refuseUndeliberate(logger *slog.Logger, response http.ResponseWriter, request *http.Request, message string) {
	if err := api.WriteFailure(response, request,
		api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, message)); err != nil {
		logger.ErrorContext(request.Context(), "write error response", "error", err)
	}
}
