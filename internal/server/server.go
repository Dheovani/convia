// Package server provides Convia's HTTP server, middleware chain, and routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/credentials"
	"convia/internal/operator"
	"convia/internal/ratelimit"
	"convia/internal/rooms"
	"convia/internal/users"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	// maxHeaderBytes bounds request headers, including the request line and
	// any client-supplied correlation identifier.
	maxHeaderBytes = 1 << 20 // 1 MiB

	// readinessTimeout bounds a dependency check so that readiness answers
	// even while a dependency is unresponsive.
	readinessTimeout = 2 * time.Second

	/*
		Failed authentication attempts are budgeted per caller address.

		The budget is checked before the key is verified, so an exhausted
		caller costs a map lookup instead of a database read. That is the point
		of limiting here, and it has a consequence worth stating plainly: while
		an address is out of budget, even a valid key from that address is
		refused. Verifying it would be the very work being declined.

		The numbers are chosen around that consequence. What is being protected
		is one indexed read of a few hundred microseconds, which PostgreSQL
		serves tens of thousands of times a second, so a tight budget would buy
		almost nothing and would make the collateral refusal likely. Sixty
		attempts, refilled over a minute, leaves a misconfigured client
		retrying every few seconds far from the limit while still cutting a
		flood down to one attempt a second.
	*/
	authFailureBurst  = 60
	authFailurePeriod = time.Minute

	/*
		authFailureKeys bounds the addresses remembered at once. A bucket
		exists only for an address that has failed recently, so this is far
		above any plausible number of simultaneously misconfigured clients.
	*/
	authFailureKeys = 10_000
)

/*
Prober reports whether a dependency is reachable.

The server depends on this narrow behavior rather than on a database type, so
readiness stays testable without infrastructure. *pgxpool.Pool satisfies it.
*/
type Prober interface {
	Ping(ctx context.Context) error
}

/*
Dependencies are the collaborators the HTTP layer serves.

Every route that acts with someone's authority requires the verifier for its
surface to be present, or it is not registered at all. A route that acts on
behalf of an application cannot exist without the middleware that decides which
application is asking, and an operator route cannot exist without the one that
decides whether the caller may administer Convia. Forgetting to wire a verifier
removes the endpoints rather than opening them.
*/
type Dependencies struct {
	Database Prober

	/*
		TrustedProxies are the networks whose forwarded headers Convia
		believes when deciding which address a request is charged to. Empty
		means trust nothing, which is the default and the only safe one.
	*/
	TrustedProxies []netip.Prefix

	/*
		The operator surface administers tenants: creating them, suspending
		them, and issuing their first keys. It is authenticated by an operator
		credential, which no application can hold.
	*/
	OperatorAuthenticator operatorAuthenticator
	Applications          *applications.Handler
	Users                 *users.Handler
	Credentials           *credentials.Handler
	Rooms                 *rooms.Handler
	OperatorCredentials   *operator.Handler

	/*
		The tenant-facing surface is authenticated by an application's own key,
		which is also where the tenant comes from.
	*/
	Authenticator     authenticator
	TenantUsers       *users.TenantHandler
	TenantCredentials *credentials.TenantHandler
	TenantRooms       *rooms.TenantHandler
}

// New constructs the Convia HTTP server.
func New(address string, logger *slog.Logger, dependencies Dependencies) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler(logger, dependencies),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

/*
handler builds the routed handler wrapped by the transport middleware.

The chain is ordered so that every request carries a correlation identifier
before it is logged, and so that a recovered panic is still reported by the
access log with its final status.
*/
func handler(logger *slog.Logger, dependencies Dependencies) http.Handler {
	/*
		One limiter is shared by every authenticated route, so a caller cannot
		spread its failed attempts across endpoints to buy more of them.
	*/
	failures := ratelimit.New(authFailureBurst, authFailurePeriod, authFailureKeys)

	resolve := newResolver(dependencies.TrustedProxies)

	rt := newRoutes(logger)
	for _, entry := range routeTable(logger, dependencies) {
		served := entry.handler
		switch entry.surface {
		case surfaceTenant:
			served = authenticate(logger, tenantVerifier{dependencies.Authenticator}, failures, resolve, served)
		case surfaceOperator:
			served = authenticate(logger, operatorVerifier{dependencies.OperatorAuthenticator}, failures, resolve, served)
		}
		rt.handle(entry.method, entry.path, served)
	}

	return requestID(logRequest(logger, resolve, recoverPanic(logger, rt.handler())))
}

/*
surface names which authority a route acts with.

It is an enumeration rather than a pair of booleans so that "authenticated by
nobody in particular" cannot be expressed: a route is public, acts for an
application, or acts for an operator, and there is no fourth state to get
wrong.
*/
type surface int

const (
	// surfacePublic is served without a credential. Operational endpoints only.
	surfacePublic surface = iota
	// surfaceTenant acts with an application's authority, taken from its key.
	surfaceTenant
	// surfaceOperator acts with an operator's authority over Convia itself.
	surfaceOperator
)

/*
route describes one HTTP route served by Convia.

Which authority a route acts with is declared here rather than remembered
inside a handler, so the authenticated surface can be read off the table and
compared with the contract by a test.
*/
type route struct {
	method  string
	path    string
	handler http.Handler
	surface surface
}

// authenticated reports whether the route demands a credential of any kind.
func (entry route) authenticated() bool {
	return entry.surface != surfacePublic
}

/*
routeTable returns every route the service serves.

It is the single source of truth for routing, which lets the contract test
compare the implemented surface with the OpenAPI document in api/.
*/
func routeTable(logger *slog.Logger, dependencies Dependencies) []route {
	table := []route{
		/*
			Operational endpoints stay outside api.Prefix: they are owned by
			operators, not by public API consumers, and must not be versioned or
			authenticated together with the public API.
		*/
		{method: http.MethodGet, path: "/health", handler: healthHandler(logger)},
		{method: http.MethodGet, path: "/ready", handler: readinessHandler(logger, dependencies.Database)},
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Applications != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Create)},
			route{method: http.MethodGet, path: api.Prefix + "/applications", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/applications/{application_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Rename)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/suspend", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/activate", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Applications.Activate)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.OperatorCredentials != nil {
		/*
			An operator administering operator credentials. This is the most
			privileged surface Convia has, which is why issuing here carries a
			subset rule: a key cannot mint one that outranks it.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/operator/credentials", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.OperatorCredentials.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/operator/credentials", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.OperatorCredentials.List)},
			route{method: http.MethodGet, path: api.Prefix + "/operator/credentials/{credential_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.OperatorCredentials.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/operator/credentials/{credential_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.OperatorCredentials.Revoke)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Users != nil {
		/*
			These routes name the application in the path because an operator
			acts on a tenant other than itself. An application reaches its own
			users through the tenant routes below, where the tenant comes from
			the credential instead and no request field could name another.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Resolve)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/users", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/users/{user_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/applications/{application_id}/users/{user_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}/users/{user_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users/{user_id}/suspend", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users/{user_id}/activate", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Users.Activate)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantUsers != nil {
		/*
			The authenticated surface names no application in its paths. The
			tenant comes from the verified credential, which is what M06 said
			would arrive here: an application addresses its own users without
			naming itself.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/users", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Resolve)},
			route{method: http.MethodGet, path: api.Prefix + "/users", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.List)},
			route{method: http.MethodGet, path: api.Prefix + "/users/{user_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/users/{user_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/users/{user_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/users/{user_id}/suspend", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/users/{user_id}/activate", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantUsers.Activate)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantCredentials != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/credentials", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/credentials", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCredentials.List)},
			route{method: http.MethodGet, path: api.Prefix + "/credentials/{credential_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/credentials/{credential_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Revoke)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantRooms != nil {
		/*
			An application addressing its own rooms. As with users, the tenant
			comes from the credential rather than the path, so no request field
			could name another application's room.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/rooms", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Create)},
			route{method: http.MethodGet, path: api.Prefix + "/rooms", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.List)},
			route{method: http.MethodGet, path: api.Prefix + "/rooms/{room_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/rooms/{room_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/rooms/{room_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/rooms/{room_id}/close", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Close)},
			route{method: http.MethodPost, path: api.Prefix + "/rooms/{room_id}/reopen", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantRooms.Reopen)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Rooms != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/rooms", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Create)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/rooms", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}/close", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Close)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}/reopen", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Rooms.Reopen)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Credentials != nil {
		/*
			An operator issuing a key on a tenant's behalf. This is the
			bootstrap the operator surface exists for: an application cannot
			issue its own first credential, because issuing requires presenting
			one.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/credentials", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Credentials.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/credentials", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Credentials.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/credentials/{credential_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Credentials.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}/credentials/{credential_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Credentials.Revoke)},
		)
	}
	return table
}

// healthResponse is the stable body of the health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

/*
healthHandler reports process liveness.

It deliberately checks nothing beyond the process itself. A dependency outage
must not make an orchestrator restart a healthy process, so dependency state
belongs to readiness instead.
*/
func healthHandler(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := api.Write(response, http.StatusOK, healthResponse{Status: "ok"}); err != nil {
			logger.Error("write health response", "error", err, "request_id", api.RequestIDFromContext(request.Context()))
		}
	})
}

// readinessResponse reports whether Convia can currently serve traffic.
type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

const (
	statusReady       = "ready"
	statusUnavailable = "unavailable"
	checkOK           = "ok"
)

/*
readinessHandler reports whether Convia's dependencies are usable.

An unreachable database answers 503 so that a load balancer stops sending
traffic to this instance while the process keeps running. The reason for the
failure is logged rather than returned, because dependency errors expose
infrastructure detail.
*/
func readinessHandler(logger *slog.Logger, database Prober) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		probeContext, cancel := context.WithTimeout(request.Context(), readinessTimeout)
		defer cancel()

		body := readinessResponse{Status: statusReady, Checks: map[string]string{"database": checkOK}}
		status := http.StatusOK

		if err := database.Ping(probeContext); err != nil {
			logger.Error("readiness probe failed",
				"error", err,
				"dependency", "database",
				"request_id", api.RequestIDFromContext(request.Context()),
			)
			body = readinessResponse{Status: statusUnavailable, Checks: map[string]string{"database": statusUnavailable}}
			status = http.StatusServiceUnavailable
		}

		if err := api.Write(response, status, body); err != nil {
			logger.Error("write readiness response", "error", err, "request_id", api.RequestIDFromContext(request.Context()))
		}
	})
}
