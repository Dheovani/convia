// Package server provides Convia's HTTP server, middleware chain, and routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/credentials"
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

Applications, Users, and Credentials are the operator-facing handlers. They are
nil when the operator API is disabled, which is the default, and their routes
are then not registered at all, so an unauthenticated endpoint cannot be
reached by accident.
*/
type Dependencies struct {
	Database     Prober
	Applications *applications.Handler
	Users        *users.Handler
	Credentials  *credentials.Handler

	/*
		The tenant-facing surface is authenticated, so it is served whether or
		not the operator gate is open. Authenticator must be present for those
		routes to be registered at all: a route that acts on behalf of an
		application must never exist without the middleware that decides which
		application is asking.
	*/
	Authenticator     authenticator
	TenantUsers       *users.TenantHandler
	TenantCredentials *credentials.TenantHandler
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
	rt := newRoutes(logger)
	for _, entry := range routeTable(logger, dependencies) {
		served := entry.handler
		if entry.authenticated {
			served = authenticate(logger, dependencies.Authenticator, served)
		}
		rt.handle(entry.method, entry.path, served)
	}

	return requestID(logRequest(logger, recoverPanic(logger, rt.handler())))
}

/*
route describes one HTTP route served by Convia.

Whether a route requires a credential is declared here rather than remembered
inside a handler, so the authenticated surface can be read off the table and
compared with the contract by a test.
*/
type route struct {
	method        string
	path          string
	handler       http.Handler
	authenticated bool
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

	if dependencies.Applications != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications",
				handler: http.HandlerFunc(dependencies.Applications.Create)},
			route{method: http.MethodGet, path: api.Prefix + "/applications",
				handler: http.HandlerFunc(dependencies.Applications.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}",
				handler: http.HandlerFunc(dependencies.Applications.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/applications/{application_id}",
				handler: http.HandlerFunc(dependencies.Applications.Rename)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}",
				handler: http.HandlerFunc(dependencies.Applications.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/suspend",
				handler: http.HandlerFunc(dependencies.Applications.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/activate",
				handler: http.HandlerFunc(dependencies.Applications.Activate)},
		)
	}

	if dependencies.Users != nil {
		/*
			These routes name the application in the path because an operator
			acts on a tenant other than itself. An application reaches its own
			users through the authenticated routes below, where the tenant comes
			from the credential instead.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users",
				handler: http.HandlerFunc(dependencies.Users.Resolve)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/users",
				handler: http.HandlerFunc(dependencies.Users.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/users/{user_id}",
				handler: http.HandlerFunc(dependencies.Users.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/applications/{application_id}/users/{user_id}",
				handler: http.HandlerFunc(dependencies.Users.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}/users/{user_id}",
				handler: http.HandlerFunc(dependencies.Users.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users/{user_id}/suspend",
				handler: http.HandlerFunc(dependencies.Users.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/users/{user_id}/activate",
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
			route{method: http.MethodPost, path: api.Prefix + "/users", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Resolve)},
			route{method: http.MethodGet, path: api.Prefix + "/users", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.List)},
			route{method: http.MethodGet, path: api.Prefix + "/users/{user_id}", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/users/{user_id}", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/users/{user_id}", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/users/{user_id}/suspend", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Suspend)},
			route{method: http.MethodPost, path: api.Prefix + "/users/{user_id}/activate", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantUsers.Activate)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantCredentials != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/credentials", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/credentials", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantCredentials.List)},
			route{method: http.MethodGet, path: api.Prefix + "/credentials/{credential_id}", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/credentials/{credential_id}", authenticated: true,
				handler: http.HandlerFunc(dependencies.TenantCredentials.Revoke)},
		)
	}

	if dependencies.Credentials != nil {
		/*
			Issuing a credential is an operator action, so these routes sit
			beside the other administrative endpoints. An application managing
			its own keys through an authenticated route arrives with the
			middleware in the next milestone slice.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/credentials",
				handler: http.HandlerFunc(dependencies.Credentials.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/credentials",
				handler: http.HandlerFunc(dependencies.Credentials.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/credentials/{credential_id}",
				handler: http.HandlerFunc(dependencies.Credentials.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/applications/{application_id}/credentials/{credential_id}",
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
