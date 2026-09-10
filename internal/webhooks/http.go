package webhooks

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/events"
)

// TenantHandler exposes an application's own webhook endpoints over HTTP.
type TenantHandler struct {
	logger  *slog.Logger
	service service
}

func NewTenantHandler(logger *slog.Logger, service service) *TenantHandler {
	return &TenantHandler{logger: logger, service: service}
}

// registrationRequest is the accepted body when registering or changing an
// endpoint.
type registrationRequest struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
}

func (body registrationRequest) registration() Registration {
	types := make([]events.Type, 0, len(body.EventTypes))
	for _, kind := range body.EventTypes {
		types = append(types, events.Type(kind))
	}
	return Registration{Name: body.Name, URL: body.URL, EventTypes: types}
}

/*
endpointResponse is the public representation of an endpoint.

There is no secret field, and there is no code path that could add one: the
value this is built from does not carry the signing key at all.
*/
type endpointResponse struct {
	ID            string   `json:"id"`
	ApplicationID string   `json:"application_id"`
	Name          string   `json:"name"`
	URL           string   `json:"url"`
	EventTypes    []string `json:"event_types"`
	Status        string   `json:"status"`

	ConsecutiveFailures int    `json:"consecutive_failures"`
	DisabledReason      string `json:"disabled_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

/*
registeredEndpointResponse is the one representation that carries a secret.

It is returned by registering and by rotating, and by nothing else. Convia
holds the key because it signs with it, so "shown once" here means that no read
returns it — which is why this type exists separately rather than as a field
that is usually empty.
*/
type registeredEndpointResponse struct {
	endpointResponse
	Secret string `json:"secret"`
}

// deliveryResponse is the public representation of one attempt to tell an
// application something.
type deliveryResponse struct {
	ID            string `json:"id"`
	EndpointID    string `json:"endpoint_id"`
	ApplicationID string `json:"application_id"`
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`

	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LastStatusCode int        `json:"last_status_code,omitempty"`
	LastError      string     `json:"last_error,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

type endpointPageResponse struct {
	Data       []endpointResponse `json:"data"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type deliveryPageResponse struct {
	Data       []deliveryResponse `json:"data"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func represent(endpoint Endpoint) endpointResponse {
	types := make([]string, 0, len(endpoint.EventTypes))
	for _, kind := range endpoint.EventTypes {
		types = append(types, string(kind))
	}

	return endpointResponse{
		ID:                  endpoint.ID,
		ApplicationID:       endpoint.ApplicationID,
		Name:                endpoint.Name,
		URL:                 endpoint.URL,
		EventTypes:          types,
		Status:              string(endpoint.Status),
		ConsecutiveFailures: endpoint.ConsecutiveFailures,
		DisabledReason:      endpoint.DisabledReason,
		CreatedAt:           endpoint.CreatedAt,
		UpdatedAt:           endpoint.UpdatedAt,
	}
}

/*
representDelivery publishes what happened to a delivery, and not what was in it.

The payload is deliberately absent. It is the event, which the application can
read from the stream or from the resource itself, and repeating it in every
delivery record would put the same body in a second place with a different
retention.
*/
func representDelivery(delivery Delivery) deliveryResponse {
	return deliveryResponse{
		ID:             delivery.ID,
		EndpointID:     delivery.EndpointID,
		ApplicationID:  delivery.ApplicationID,
		EventID:        delivery.EventID,
		EventType:      string(delivery.EventType),
		Status:         string(delivery.Status),
		Attempts:       delivery.Attempts,
		NextAttemptAt:  delivery.NextAttemptAt,
		LastStatusCode: delivery.LastStatusCode,
		LastError:      delivery.LastError,
		CreatedAt:      delivery.CreatedAt,
		UpdatedAt:      delivery.UpdatedAt,
		DeliveredAt:    delivery.DeliveredAt,
	}
}

// Register records a destination and returns its signing key once.
func (handler *TenantHandler) Register(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body registrationRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	endpoint, signing, err := authorized.Register(request.Context(), body.registration())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusCreated, registeredEndpointResponse{
		endpointResponse: represent(endpoint),
		Secret:           signing.Reveal(),
	})
}

// List returns one page of the caller's own endpoints.
func (handler *TenantHandler) List(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	options := ListOptions{Cursor: request.URL.Query().Get("cursor")}
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

	body := endpointPageResponse{
		Data:       make([]endpointResponse, 0, len(page.Endpoints)),
		NextCursor: page.NextCursor,
	}
	for _, endpoint := range page.Endpoints {
		body.Data = append(body.Data, represent(endpoint))
	}

	handler.write(response, request, http.StatusOK, body)
}

// Get returns one of the caller's own endpoints.
func (handler *TenantHandler) Get(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	endpoint, err := authorized.Get(request.Context(), request.PathValue("endpoint_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, represent(endpoint))
}

// Update changes one of the caller's own endpoints.
func (handler *TenantHandler) Update(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	var body registrationRequest
	if failure := api.DecodeJSON(response, request, &body); failure != nil {
		handler.writeFailure(response, request, failure)
		return
	}

	endpoint, err := authorized.Update(request.Context(),
		request.PathValue("endpoint_id"), body.registration())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, represent(endpoint))
}

// Rotate issues a new signing key and returns it once.
func (handler *TenantHandler) Rotate(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	endpoint, signing, err := authorized.Rotate(request.Context(), request.PathValue("endpoint_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	handler.write(response, request, http.StatusOK, registeredEndpointResponse{
		endpointResponse: represent(endpoint),
		Secret:           signing.Reveal(),
	})
}

// Enable resumes delivery to one of the caller's own endpoints.
func (handler *TenantHandler) Enable(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, func(authorized *Authorized, id string) (Endpoint, error) {
		return authorized.Enable(request.Context(), id)
	})
}

// Disable stops delivery to one of the caller's own endpoints.
func (handler *TenantHandler) Disable(response http.ResponseWriter, request *http.Request) {
	handler.transition(response, request, func(authorized *Authorized, id string) (Endpoint, error) {
		return authorized.Disable(request.Context(), id)
	})
}

func (handler *TenantHandler) transition(response http.ResponseWriter, request *http.Request,
	change func(*Authorized, string) (Endpoint, error)) {

	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	endpoint, err := change(authorized, request.PathValue("endpoint_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, represent(endpoint))
}

// Delete removes one of the caller's own endpoints.
func (handler *TenantHandler) Delete(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	if err := authorized.Delete(request.Context(), request.PathValue("endpoint_id")); err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// ListDeliveries returns one page of what Convia tried to tell the caller.
func (handler *TenantHandler) ListDeliveries(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	/*
		The endpoint may be named by the path, on the nested listing, or by a
		query on the tenant-wide one. The path wins: a request that addressed
		one endpoint must not be widened by a query naming another.
	*/
	endpointID := request.PathValue("endpoint_id")
	if endpointID == "" {
		endpointID = request.URL.Query().Get("endpoint_id")
	}

	options := DeliveryListOptions{
		Cursor:     request.URL.Query().Get("cursor"),
		EndpointID: endpointID,
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

	page, err := authorized.ListDeliveries(request.Context(), options)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}

	body := deliveryPageResponse{
		Data:       make([]deliveryResponse, 0, len(page.Deliveries)),
		NextCursor: page.NextCursor,
	}
	for _, delivery := range page.Deliveries {
		body.Data = append(body.Data, representDelivery(delivery))
	}

	handler.write(response, request, http.StatusOK, body)
}

// GetDelivery returns one of the caller's own deliveries.
func (handler *TenantHandler) GetDelivery(response http.ResponseWriter, request *http.Request) {
	authorized, ok := handler.authorized(response, request)
	if !ok {
		return
	}

	delivery, err := authorized.GetDelivery(request.Context(), request.PathValue("delivery_id"))
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	handler.write(response, request, http.StatusOK, representDelivery(delivery))
}

func (handler *TenantHandler) authorized(response http.ResponseWriter,
	request *http.Request) (*Authorized, bool) {

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

func (handler *TenantHandler) write(response http.ResponseWriter, request *http.Request,
	status int, body any) {

	if err := api.Write(response, status, body); err != nil {
		handler.logger.Error("write response", "error", err,
			"request_id", api.RequestIDFromContext(request.Context()))
	}
}

func (handler *TenantHandler) writeFailure(response http.ResponseWriter, request *http.Request,
	failure *api.Failure) {

	if err := api.WriteFailure(response, request, failure); err != nil {
		handler.logger.Error("write failure response", "error", err,
			"request_id", api.RequestIDFromContext(request.Context()))
	}
}

/*
writeError translates a domain error into the public error schema.

Only errors the domain declares are described to the client. Anything else is
an unexpected condition: it is logged with its detail and reported as a generic
internal error, so that infrastructure failures never reach a public contract.
*/
func (handler *TenantHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var validation ValidationError

	switch {
	case errors.As(err, &validation):
		handler.writeFailure(response, request,
			api.NewFailure(http.StatusBadRequest, api.CodeInvalidRequest, validation.Message))

	case errors.Is(err, ErrForbidden):
		handler.writeFailure(response, request, api.NewFailure(http.StatusForbidden,
			api.CodeForbidden, "The credential does not carry the scope this operation requires."))

	case errors.Is(err, ErrApplicationNotFound):
		handler.writeFailure(response, request, api.NewFailure(http.StatusNotFound,
			api.CodeNotFound, "The requested application does not exist."))

	case errors.Is(err, ErrNotFound):
		handler.writeFailure(response, request, api.NewFailure(http.StatusNotFound,
			api.CodeNotFound, "The requested webhook endpoint does not exist."))

	case errors.Is(err, ErrDeliveryNotFound):
		handler.writeFailure(response, request, api.NewFailure(http.StatusNotFound,
			api.CodeNotFound, "The requested webhook delivery does not exist."))

	default:
		handler.logger.Error("webhook operation failed", "error", err,
			"method", request.Method,
			"path", request.URL.Path,
			"request_id", api.RequestIDFromContext(request.Context()),
		)
		handler.writeFailure(response, request, api.NewFailure(http.StatusInternalServerError,
			api.CodeInternal, "The server encountered an unexpected condition."))
	}
}
