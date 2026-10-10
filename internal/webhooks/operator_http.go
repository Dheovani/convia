package webhooks

import (
	"log/slog"
	"net/http"
	"strconv"

	"convia/internal/api"
	"convia/internal/operator"
)

/*
OperatorHandler exposes a tenant's webhooks to an operator.

It answers in exactly the shapes the tenant's own routes do, through the same
representations and the same error translation, so that what an operator and
the tenant see about one delivery cannot disagree. The routes name the tenant in
the path because an operator acts on somebody else's.
*/
type OperatorHandler struct {
	logger  *slog.Logger
	service operatorService

	// replies is the tenant handler's way of answering, borrowed rather than
	// copied: one translation of every webhook error, wherever it is asked.
	replies *TenantHandler
}

func NewOperatorHandler(logger *slog.Logger, service operatorService) *OperatorHandler {
	return &OperatorHandler{logger: logger, service: service, replies: &TenantHandler{logger: logger}}
}

/*
authorized binds the request's verified operator to the service.

A request that reaches here without an operator principal was routed without
the authentication middleware, which is a wiring mistake rather than a client
error. It is refused as unauthenticated, because that is the answer that grants
nothing, and logged so the mistake is visible.
*/
func (handler *OperatorHandler) authorized(response http.ResponseWriter, request *http.Request) (*OperatorAuthorized, bool) {
	principal, found := operator.PrincipalFromContext(request.Context())
	if !found {
		handler.logger.ErrorContext(request.Context(), "operator route reached without a principal",
			"method", request.Method,
			"path", request.URL.Path,
		)
		handler.replies.writeFailure(response, request, api.NewFailure(http.StatusUnauthorized,
			api.CodeUnauthenticated, "The request did not carry a usable credential."))
		return nil, false
	}
	return AuthorizeOperator(handler.service, principal), true
}

// limit reads the page size a request asked for, if it asked.
func limit(request *http.Request) (int, error) {
	raw := request.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, ValidationError{Field: "limit", Message: "The limit must be a number."}
	}

	return value, nil
}

// List returns one page of a tenant's endpoints.
func (handler *OperatorHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	size, err := limit(request)
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}

	page, err := authorized.List(request.Context(), request.PathValue("application_id"),
		ListOptions{Cursor: request.URL.Query().Get("cursor"), Limit: size})
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}

	body := endpointPageResponse{Data: make([]endpointResponse, 0, len(page.Endpoints)), NextCursor: page.NextCursor}
	for _, endpoint := range page.Endpoints {
		body.Data = append(body.Data, represent(endpoint))
	}
	handler.replies.write(response, request, http.StatusOK, body)
}

// Get returns one of a tenant's endpoints.
func (handler *OperatorHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	endpoint, err := authorized.Get(request.Context(), request.PathValue("application_id"), request.PathValue("endpoint_id"))
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}
	handler.replies.write(response, request, http.StatusOK, represent(endpoint))
}

/*
ListDeliveries returns one page of what Convia tried to tell a tenant.

As on the tenant's own routes, the endpoint is named by the path on the nested
listing and by a query on the flat one, and the path wins.
*/
func (handler *OperatorHandler) ListDeliveries(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	endpointID := request.PathValue("endpoint_id")
	if endpointID == "" {
		endpointID = request.URL.Query().Get("endpoint_id")
	}

	size, err := limit(request)
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}

	page, err := authorized.ListDeliveries(request.Context(), request.PathValue("application_id"),
		DeliveryListOptions{Cursor: request.URL.Query().Get("cursor"), EndpointID: endpointID, Limit: size})
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}

	body := deliveryPageResponse{Data: make([]deliveryResponse, 0, len(page.Deliveries)), NextCursor: page.NextCursor}
	for _, delivery := range page.Deliveries {
		body.Data = append(body.Data, representDelivery(delivery))
	}
	handler.replies.write(response, request, http.StatusOK, body)
}

// GetDelivery returns one of a tenant's deliveries.
func (handler *OperatorHandler) GetDelivery(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	delivery, err := authorized.GetDelivery(request.Context(), request.PathValue("application_id"), request.PathValue("delivery_id"))
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}
	handler.replies.write(response, request, http.StatusOK, representDelivery(delivery))
}

/*
Redeliver queues a delivery's event again, and answers with the new delivery.

It answers `202 Accepted` rather than `201`, because what was created is a
promise to try: the new delivery is pending, and whether the destination takes
it is something the deliveries listing will say later.
*/
func (handler *OperatorHandler) Redeliver(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	delivery, err := authorized.Redeliver(request.Context(), request.PathValue("application_id"), request.PathValue("delivery_id"))
	if err != nil {
		handler.replies.writeError(response, request, err)
		return
	}
	handler.replies.write(response, request, http.StatusAccepted, representDelivery(delivery))
}
