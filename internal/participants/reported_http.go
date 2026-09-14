package participants

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/media"
)

// reportService is what receiving a report needs from this package.
type reportService interface {
	Reported(ctx context.Context, report media.Report) error
}

/*
ReportHandler receives what the media plane observed.

It is reached only through the middleware that verified the report's signature,
which is what puts the report in the request's context. A request that arrives
without one was routed around that middleware, and it is refused as
unauthenticated rather than trusted.
*/
type ReportHandler struct {
	logger  *slog.Logger
	service reportService
}

func NewReportHandler(logger *slog.Logger, service reportService) *ReportHandler {
	return &ReportHandler{logger: logger, service: service}
}

/*
Receive applies one report.

A report that could not be applied because the media plane could not be asked a
follow-up question is answered 503, so that the media plane sends it again
rather than Convia guessing. Anything else that fails is logged and answered
500 for the same reason.
*/
func (handler *ReportHandler) Receive(response http.ResponseWriter, request *http.Request) {
	report, found := media.ReportFromContext(request.Context())
	if !found {
		handler.logger.Error("a media report reached its handler without being verified",
			"request_id", api.RequestIDFromContext(request.Context()))
		handler.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return
	}

	err := handler.service.Reported(request.Context(), report)
	switch {
	case err == nil:
		response.WriteHeader(http.StatusNoContent)
	case errors.Is(err, calls.ErrMediaUnavailable):
		handler.writeFailure(response, request, api.NewFailure(http.StatusServiceUnavailable,
			api.CodeUnavailable, "The report could not be checked against the media plane. Send it again."))
	default:
		handler.logger.Error("a media report could not be applied",
			"error", err,
			"report", string(report.Kind),
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}

func (handler *ReportHandler) writeFailure(
	response http.ResponseWriter,
	request *http.Request,
	failure *api.Failure,
) {
	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response",
			"error", err,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
	}
}
