// Package server provides Convia's HTTP server, middleware chain, and routes.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/calls"
	"convia/internal/credentials"
	"convia/internal/events"
	"convia/internal/invitations"
	"convia/internal/operator"
	"convia/internal/participants"
	"convia/internal/presence"
	"convia/internal/ratelimit"
	"convia/internal/rooms"
	"convia/internal/sessions"
	"convia/internal/users"
	"convia/internal/webhooks"
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

	/*
		signInFailureBurst and signInFailurePeriod budget failed sign-ins.

		Ten a minute per address. A person who has mistyped their password ten
		times in a minute is not typing, and ten attempts a minute is far below
		what guessing needs to be worth attempting — while still leaving a
		household or an office behind one address able to sign in normally.
	*/
	signInFailureBurst  = 10
	signInFailurePeriod = time.Minute
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
	Calls                 *calls.Handler
	Participants          *participants.Handler
	OperatorCredentials   *operator.Handler

	/*
		The tenant-facing surface is authenticated by an application's own key,
		which is also where the tenant comes from.
	*/
	Authenticator      authenticator
	TenantUsers        *users.TenantHandler
	TenantCredentials  *credentials.TenantHandler
	TenantRooms        *rooms.TenantHandler
	TenantCalls        *calls.TenantHandler
	TenantParticipants *participants.TenantHandler
	TenantInvitations  *invitations.TenantHandler

	/*
		TenantEvents is the one route that is not a request and a response. It
		is left out of the write timeouts the rest of the surface is served
		under, because a stream is supposed to outlive them, and the handler
		clears them for its own connection rather than the server relaxing them
		for every route.
	*/
	TenantEvents *events.TenantHandler

	/*
		TenantWebhooks is the durable counterpart of the stream above: where an
		application asks to be reached rather than to listen. Leaving it out
		removes the routes and leaves Convia delivering nothing, which is a
		supported deployment.
	*/
	TenantWebhooks *webhooks.TenantHandler

	/*
		TenantPresence is the advisory surface: what an application says about
		who is available, held for as long as it keeps saying it. Leaving it
		out removes the routes, and everything else about the application works
		unchanged — which is the same thing that happens when the ephemeral
		store behind it is unreachable.
	*/
	TenantPresence *presence.TenantHandler

	/*
		The invitation surface is authenticated by an invitation itself, which
		is what makes it authorization rather than a record of the
		application's own decision. Leaving it out removes those routes, and an
		application can still admit people directly.
	*/
	InvitationAuthenticator invitationAuthenticator
	Invitations             *invitations.HolderHandler

	/*
		The browser surface. A session is the fourth credential family and the
		first that a person rather than a program presents. It is wired like
		the others: leaving either of these out removes the routes, rather than
		serving them to nobody in particular.
	*/
	SessionAuthenticator sessionAuthenticator
	Sessions             *sessions.Handler

	/*
		IdempotencyKeys lets a caller retry a creation without risking a second
		resource. Leaving it out does not remove the routes it guards, because
		the header is optional and every request that omits it is served
		normally. A request that presents one is refused instead of being served
		without the guarantee, so a client is never told a promise held when it
		did not.
	*/
	IdempotencyKeys keyRegistry
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

	/*
		A budget of its own for the browser surface, and a much smaller one.

		The sixty-a-minute figure above is justified by a secret nobody can
		search: a failed application key tells an attacker nothing, so the limit
		is there to stop a flood rather than to stop guessing. **A password can
		be guessed**, so reusing that number here would import a rationale that
		does not hold.

		It is a separate limiter as well as a smaller one, so that a browser
		with a stale cookie cannot spend the budget that protects the API for
		every other caller behind the same address.
	*/
	signingIn := ratelimit.New(signInFailureBurst, signInFailurePeriod, authFailureKeys)

	resolve := newResolver(dependencies.TrustedProxies)

	rt := newRoutes(logger)
	for _, entry := range routeTable(logger, dependencies) {
		served := entry.handler

		/*
			Idempotency wraps the handler and is itself wrapped by
			authentication, so a key is only ever claimed for a caller who has
			proved who they are. The reverse order would let an unauthenticated
			request reserve keys.
		*/
		if entry.idempotent {
			served = idempotent(logger, dependencies.IdempotencyKeys, served)
		}

		/*
			Every surface is named here, and the default panics. `handler` runs
			at startup, so a surface somebody adds and forgets to wire brings
			the process down instead of serving its routes to anybody who
			asks — which is what the omission used to do, silently.
		*/
		switch entry.surface {
		case surfacePublic:
			// Served as it is. Operational endpoints only.
		case surfaceSignIn:
			served = budgeted(logger, signingIn, resolve, served)
		case surfaceTenant:
			served = authenticate(logger, tenantVerifier{service: dependencies.Authenticator},
				failures, resolve, served)
		case surfaceOperator:
			served = authenticate(logger, operatorVerifier{service: dependencies.OperatorAuthenticator},
				failures, resolve, served)
		case surfaceInvitation:
			served = authenticate(logger, invitationVerifier{service: dependencies.InvitationAuthenticator},
				failures, resolve, served)
		case surfaceSession:
			served = authenticate(logger, sessionVerifier{service: dependencies.SessionAuthenticator},
				signingIn, resolve, served)
		default:
			panic(fmt.Sprintf("server: route %s %s is on surface %d, which nothing authenticates",
				entry.method, entry.path, entry.surface))
		}
		rt.handle(entry.method, entry.path, served)
	}

	return requestID(logRequest(logger, resolve, recoverPanic(logger, rt.handler())))
}

/*
surface names which authority a route acts with.

It is an enumeration rather than a set of booleans so that "authenticated by
nobody in particular" cannot be expressed. Every value is named in the switch
that wraps handlers, and the default there panics at startup, so adding one
without deciding what verifies it is a process that does not start rather than
an API that is open.
*/
type surface int

const (
	// surfacePublic is served without a credential. Operational endpoints only.
	surfacePublic surface = iota
	// surfaceTenant acts with an application's authority, taken from its key.
	surfaceTenant
	// surfaceOperator acts with an operator's authority over Convia itself.
	surfaceOperator
	/*
		surfaceInvitation acts with the authority of one invitation, presented
		by whoever holds it.

		It is a surface of its own rather than a variation of the tenant one,
		because the party presenting an invitation is deliberately not the
		party that granted it. An application key is never accepted here and an
		invitation is never accepted anywhere else: each is refused on its
		shape, before any lookup.
	*/
	surfaceInvitation
	/*
		surfaceSession acts for one person signed in to Convia's own product.

		It is the only surface whose credential is a cookie rather than a
		header, and the only one whose principal carries no scopes. A person is
		not an integration: what they may do is decided per operation, against
		them, rather than by an authority over a whole tenant.
	*/
	surfaceSession
	/*
		surfaceSignIn is how somebody gets a session, and it authenticates
		nobody.

		It is not surfacePublic, because it is not free: it is the one
		unauthenticated route in Convia where guessing pays, so it carries a
		budget of its own. Separating it from the public operational endpoints
		is what makes that visible in the route table rather than hidden in a
		handler.
	*/
	surfaceSignIn
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

	/*
		idempotent marks a route that honors an Idempotency-Key.

		It is declared here for the same reason the surface is: a guarantee a
		client can rely on should be readable off the table rather than
		remembered inside a handler. Only operations that create something need
		it. A transition that is already repeatable -- closing a room, deleting
		one -- does not, because repeating it is already harmless.
	*/
	idempotent bool
}

/*
authenticated reports whether the route demands a credential of any kind.

The surfaces are listed rather than compared against the public one, because
signing in is neither: it demands no credential and is not free. A list means a
surface added later has to be classified deliberately.
*/
func (entry route) authenticated() bool {
	switch entry.surface {
	case surfaceTenant, surfaceOperator, surfaceInvitation, surfaceSession:
		return true
	default:
		return false
	}
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
			route{method: http.MethodPost, path: api.Prefix + "/rooms", surface: surfaceTenant, idempotent: true,
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

	if dependencies.Authenticator != nil && dependencies.TenantCalls != nil {
		/*
			An application starting and ending its own conversations. Starting
			names the room in the path because the call belongs to it, and
			ending does not, because a call is addressable on its own once it
			exists.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/rooms/{room_id}/calls", surface: surfaceTenant, idempotent: true,
				handler: http.HandlerFunc(dependencies.TenantCalls.Start)},
			route{method: http.MethodGet, path: api.Prefix + "/rooms/{room_id}/calls", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCalls.List)},
			route{method: http.MethodGet, path: api.Prefix + "/calls", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCalls.List)},
			route{method: http.MethodGet, path: api.Prefix + "/calls/{call_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCalls.Get)},
			/*
				Ending is not marked idempotent. Repeating it already succeeds
				and returns the call unchanged, so a key would add a refusal to
				an operation that cannot go wrong twice.
			*/
			route{method: http.MethodPost, path: api.Prefix + "/calls/{call_id}/end", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantCalls.End)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantParticipants != nil {
		/*
			Who is in one of the application's own calls. Admitting someone
			names the call in the path because the participation belongs to it,
			and every later operation addresses the participant directly.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/calls/{call_id}/participants", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.Join)},
			route{method: http.MethodGet, path: api.Prefix + "/calls/{call_id}/participants", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.List)},
			route{method: http.MethodGet, path: api.Prefix + "/participants/{participant_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/participants/{participant_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.SetRole)},
			/*
				Joining is not marked idempotent, and needs no key: it is
				already idempotent by the person, which is what makes a
				reconnection safe. Leaving and removing are repeatable for the
				same reason ending a call is.
			*/
			route{method: http.MethodPost, path: api.Prefix + "/participants/{participant_id}/leave", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.Leave)},
			route{method: http.MethodPost, path: api.Prefix + "/participants/{participant_id}/remove", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantParticipants.Remove)},
			/*
				Issuing a connection credential is marked idempotent, unlike
				the operations above it. Those are idempotent by nature; this
				one mints something new every time it is called, so a client
				that retried after a timeout would otherwise leave a usable
				credential behind that nobody ever received.
			*/
			route{method: http.MethodPost, path: api.Prefix + "/participants/{participant_id}/session", surface: surfaceTenant, idempotent: true,
				handler: http.HandlerFunc(dependencies.TenantParticipants.Session)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantInvitations != nil {
		/*
			Invitations the application issues to its own calls. Creating one
			names the call in the path because the invitation belongs to it,
			and every later operation addresses the invitation directly.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/calls/{call_id}/invitations", surface: surfaceTenant, idempotent: true,
				handler: http.HandlerFunc(dependencies.TenantInvitations.Issue)},
			route{method: http.MethodGet, path: api.Prefix + "/invitations", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantInvitations.List)},
			route{method: http.MethodGet, path: api.Prefix + "/invitations/{invitation_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantInvitations.Get)},
			route{method: http.MethodPost, path: api.Prefix + "/invitations/{invitation_id}/revoke", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantInvitations.Revoke)},
		)
	}

	if dependencies.InvitationAuthenticator != nil && dependencies.Invitations != nil {
		/*
			What the holder of an invitation may do with it. Neither route
			names an invitation in its path: the presented key names itself,
			and asking the holder to repeat it would let them address one they
			do not hold.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/invitation/redeem", surface: surfaceInvitation, idempotent: true,
				handler: http.HandlerFunc(dependencies.Invitations.Redeem)},
			route{method: http.MethodPost, path: api.Prefix + "/invitation/decline", surface: surfaceInvitation,
				handler: http.HandlerFunc(dependencies.Invitations.Decline)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantEvents != nil {
		/*
			The live control stream. It names nothing in its path: an
			application receives its own events, and the set it receives was
			settled from its credential's scopes before the connection existed.

			It is not marked idempotent. An Idempotency-Key exists so that a
			retried creation produces no second resource, and a stream creates
			nothing — opening a second one is a second subscriber, which is
			exactly what a client that reconnected wants.
		*/
		table = append(table,
			route{method: http.MethodGet, path: api.Prefix + "/events", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantEvents.Stream)},
		)
	}

	if dependencies.SessionAuthenticator != nil && dependencies.Sessions != nil {
		/*
			Convia's own product, signed in to from a browser.

			Signing in is on its own surface: it authenticates nobody and is
			therefore not wrapped by the credential middleware, but it is the
			one unauthenticated route where guessing pays, so it carries a
			budget of its own.

			It is deliberately **not** marked idempotent. An Idempotency-Key is
			claimed inside the authentication wrapper precisely so an
			unauthenticated caller cannot reserve keys, and this route is
			unauthenticated by definition — marking it would hand a stranger
			exactly what that ordering exists to prevent.

			Nothing here changes state on a GET. That is what lets SameSite=Lax
			count as a CSRF layer at all, since Lax still sends the cookie on a
			top-level GET navigation, and a test asserts it.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/sessions", surface: surfaceSignIn,
				handler: http.HandlerFunc(dependencies.Sessions.SignIn)},
			route{method: http.MethodDelete, path: api.Prefix + "/sessions/current", surface: surfaceSession,
				handler: http.HandlerFunc(dependencies.Sessions.SignOut)},
			route{method: http.MethodDelete, path: api.Prefix + "/sessions", surface: surfaceSession,
				handler: http.HandlerFunc(dependencies.Sessions.SignOutEverywhere)},
			route{method: http.MethodGet, path: api.Prefix + "/me", surface: surfaceSession,
				handler: http.HandlerFunc(dependencies.Sessions.Me)},
			route{method: http.MethodPatch, path: api.Prefix + "/me/password", surface: surfaceSession,
				handler: http.HandlerFunc(dependencies.Sessions.ChangePassword)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantPresence != nil {
		/*
			Who is available. The device is in the path rather than the body
			because it is the thing being replaced: a heartbeat is a PUT of one
			device's claim, and repeating it leaves the same state with a later
			deadline.

			None of it is marked idempotent. An Idempotency-Key exists so that
			a retried creation produces no second resource, and nothing here
			creates one — every operation is already safe to repeat, which is
			what a heartbeat has to be.
		*/
		table = append(table,
			route{method: http.MethodPut, path: api.Prefix + "/users/{user_id}/presence/{device_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantPresence.Assert)},
			route{method: http.MethodDelete, path: api.Prefix + "/users/{user_id}/presence/{device_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantPresence.Withdraw)},
			route{method: http.MethodGet, path: api.Prefix + "/users/{user_id}/presence", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantPresence.Get)},
			route{method: http.MethodDelete, path: api.Prefix + "/users/{user_id}/presence", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantPresence.Forget)},
			route{method: http.MethodGet, path: api.Prefix + "/presence", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantPresence.List)},
		)
	}

	if dependencies.Authenticator != nil && dependencies.TenantWebhooks != nil {
		/*
			Where an application asks to be told things it must not miss.

			Registering is marked idempotent, and rotating is not. Registering
			mints a secret that is shown exactly once, so a retry after a
			timeout would otherwise leave a destination behind that nobody
			received the key for. Rotating also mints one, but repeating it is
			how a caller recovers from losing the answer: a second rotation
			supersedes the first, which is the behaviour somebody who lost a
			key actually wants.
		*/
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/webhooks", surface: surfaceTenant, idempotent: true,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Register)},
			route{method: http.MethodGet, path: api.Prefix + "/webhooks", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.List)},
			route{method: http.MethodGet, path: api.Prefix + "/webhooks/{endpoint_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Get)},
			route{method: http.MethodPatch, path: api.Prefix + "/webhooks/{endpoint_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Update)},
			route{method: http.MethodDelete, path: api.Prefix + "/webhooks/{endpoint_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Delete)},
			route{method: http.MethodPost, path: api.Prefix + "/webhooks/{endpoint_id}/rotate", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Rotate)},
			route{method: http.MethodPost, path: api.Prefix + "/webhooks/{endpoint_id}/enable", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Enable)},
			route{method: http.MethodPost, path: api.Prefix + "/webhooks/{endpoint_id}/disable", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.Disable)},

			/*
				What Convia tried to send. Nested under an endpoint when the
				question is about one destination, and flat when it is about the
				tenant — the same shape rooms and calls already use.
			*/
			route{method: http.MethodGet, path: api.Prefix + "/webhooks/{endpoint_id}/deliveries", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.ListDeliveries)},
			route{method: http.MethodGet, path: api.Prefix + "/deliveries", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.ListDeliveries)},
			route{method: http.MethodGet, path: api.Prefix + "/deliveries/{delivery_id}", surface: surfaceTenant,
				handler: http.HandlerFunc(dependencies.TenantWebhooks.GetDelivery)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Rooms != nil {
		table = append(table,
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/rooms", surface: surfaceOperator, idempotent: true,
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

	if dependencies.OperatorAuthenticator != nil && dependencies.Calls != nil {
		/*
			An operator reads a tenant's calls and can end one, but cannot
			start one. Starting a conversation between an application's people
			is not administration; ending one is the lever an operator needs
			when a conversation must stop and the application cannot stop it.
		*/
		table = append(table,
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/rooms/{room_id}/calls", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Calls.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/calls", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Calls.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/calls/{call_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Calls.Get)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/calls/{call_id}/end", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Calls.End)},
		)
	}

	if dependencies.OperatorAuthenticator != nil && dependencies.Participants != nil {
		/*
			An operator reads a tenant's roster and can remove someone from it,
			and nothing else. That is the same line the call domain draws:
			removing is the lever an operator needs when someone must be put
			out of a conversation, while admitting someone or promoting them
			would be arranging a conversation nobody asked Convia to arrange.
		*/
		table = append(table,
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/calls/{call_id}/participants", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Participants.List)},
			route{method: http.MethodGet, path: api.Prefix + "/applications/{application_id}/participants/{participant_id}", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Participants.Get)},
			route{method: http.MethodPost, path: api.Prefix + "/applications/{application_id}/participants/{participant_id}/remove", surface: surfaceOperator,
				handler: http.HandlerFunc(dependencies.Participants.Remove)},
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
