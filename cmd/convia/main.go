package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"convia/internal/accounts"
	"convia/internal/applications"
	"convia/internal/calls"
	"convia/internal/config"
	"convia/internal/credentials"
	"convia/internal/database"
	"convia/internal/events"
	"convia/internal/events/redis"
	"convia/internal/idempotency"
	"convia/internal/invitations"
	"convia/internal/media"
	"convia/internal/media/livekit"
	"convia/internal/operator"
	"convia/internal/participants"
	"convia/internal/presence"
	presenceredis "convia/internal/presence/redis"
	"convia/internal/rooms"
	"convia/internal/server"
	"convia/internal/sessions"
	"convia/internal/users"
	"convia/internal/webhooks"
)

const shutdownTimeout = 10 * time.Second

const usage = `Convia is a real-time communication service.

Usage:
  convia                 Start the HTTP service
  convia serve           Start the HTTP service
  convia migrate up      Apply every pending migration
  convia migrate down    Revert the most recently applied migration
  convia migrate status  Report applied and pending migrations

Operator credentials administer Convia itself. The first one must be issued
here, because issuing one over the API requires presenting one:

  convia operator issue <name> [scope...]   Issue an operator credential
  convia operator list                      List operator credentials
  convia operator revoke <credential-id>    Withdraw one immediately

Scopes default to every one Convia recognizes when none are named. Available:
  applications:read  applications:write
  tenants:read       tenants:write
  operators:read     operators:write

The secret is printed once and never stored. Running these commands requires
database access, which is the authority the first credential is minted from.

An account is a person who signs in to Convia's own interface. There is no
self-service sign-up: open registration needs email verification, which needs a
mailer Convia does not have, so accounts are created here:

  convia account create <email> <display-name>   Create an account
  convia account suspend <account-id>            Stop somebody signing in
  convia account activate <account-id>           Let them sign in again

The password is generated rather than chosen, printed once, and never stored.
These commands need CONVIA_FIRST_PARTY_APPLICATION set to the application that
owns Convia's own product.
`

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(context.Background(), logger, os.Args[1:]); err != nil {
		logger.Error("convia stopped", "error", err)
		os.Exit(1)
	}
}

/*
run dispatches the requested command.

The composition root loads configuration, builds dependencies, and owns the
process lifecycle. Migrations are a separate command rather than a startup
step, so that schema changes stay an explicit operational decision.
*/
func run(ctx context.Context, logger *slog.Logger, arguments []string) error {
	// Usage is answered before configuration is read, so that describing the
	// commands never requires a configured database.
	if len(arguments) > 0 {
		switch arguments[0] {
		case "help", "-h", "--help":
			fmt.Print(usage)
			return nil
		case "migrate", "serve", "operator", "account":
		default:
			fmt.Print(usage)
			return fmt.Errorf("unknown command %q", arguments[0])
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	switch {
	case len(arguments) == 0 || arguments[0] == "serve":
		return serve(ctx, logger, cfg)
	case arguments[0] == "operator":
		return operatorCommand(ctx, logger, cfg, arguments[1:])
	case arguments[0] == "account":
		return accountCommand(ctx, logger, cfg, arguments[1:])
	default:
		return migrate(ctx, logger, cfg, arguments[1:])
	}
}

func migrate(ctx context.Context, logger *slog.Logger, cfg config.Config, arguments []string) error {
	if len(arguments) != 1 {
		fmt.Print(usage)
		return errors.New("migrate requires exactly one of up, down, or status")
	}

	switch arguments[0] {
	case "up":
		return database.Migrate(ctx, cfg.Database.URL, logger)
	case "down":
		return database.Rollback(ctx, cfg.Database.URL, logger)
	case "status":
		return database.Status(ctx, cfg.Database.URL, logger)
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown migrate command %q", arguments[0])
	}
}

func serve(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	signalContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(signalContext, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	credentialService := credentials.NewService(credentials.NewStore(pool), applicationService, logger)
	operatorService := operator.NewService(operator.NewStore(pool), logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, logger)

	mediaPlane, err := openMediaPlane(cfg.Media, logger)
	if err != nil {
		return err
	}

	/*
		The broker is built before the domains that publish into it, because
		every one of them takes it as a dependency rather than reaching for a
		package-level one. It holds nothing: an event goes to the streams open
		at the moment it is published and is then gone.

		Whether "open" means this instance's streams or the whole deployment's
		is the one decision the relay makes, and it is made here.
	*/
	relay, err := openRelay(cfg.Redis, logger)
	if err != nil {
		return err
	}

	broker := events.NewBroker()
	if relay != nil {
		broker = events.NewSharedBroker(relay)
	}

	/*
		Which addresses this instance is willing to reach is decided here, from
		the environment and from nothing else. A development instance may deliver
		to a receiver on localhost, because that is how anybody tests a webhook;
		production may not, and there is deliberately no setting that changes
		that. See docs/webhooks.md.
	*/
	destinations := webhooks.NewDestinations(cfg.Environment == config.Development)
	webhookStore := webhooks.NewStore(pool)
	webhookService := webhooks.NewService(webhookStore, applicationService, destinations, logger)
	dispatcher := webhooks.NewDispatcher(webhookStore, destinations, logger)

	/*
		One announcer, two promises. The broker reaches whoever is connected now
		and cannot fail; the dispatcher records what is owed to registered
		destinations and can. internal/events/announcer.go says what happens
		when the second one does.
	*/
	announcer := events.NewAnnouncer(broker, dispatcher, logger)

	callService := calls.NewService(calls.NewStore(pool), applicationService, roomService,
		mediaPlane, announcer, logger)
	participantService := participants.NewService(participants.NewStore(pool),
		applicationService, callService, roomService, userService, announcer, logger)
	invitationService := invitations.NewService(invitations.NewStore(pool),
		applicationService, callService, userService, participantService, announcer, logger)
	idempotencyService := idempotency.NewService(idempotency.NewStore(pool), logger)

	/*
		Where presence lives is the same decision the event stream already
		made, and it is made from the same setting. One instance keeps it in
		this process; several keep it where all of them can see it. Nothing
		about the API changes between the two.
	*/
	presenceStore, closePresence, err := openPresence(cfg.Redis, logger)
	if err != nil {
		return err
	}
	defer closePresence()

	presenceService := presence.NewService(presenceStore, applicationService, userService, announcer, logger)

	/*
		Convia's own product, if this deployment serves one.

		Both services are nil when no first-party application is configured,
		which removes the session routes entirely rather than registering
		routes nobody can authenticate to. That is the same shape the media
		plane and webhooks already have, and the contract test proves the
		absence removes rather than opens.
	*/
	var (
		accountService *accounts.Service
		sessionService *sessions.Service
	)
	if cfg.FirstPartyApplication != "" {
		accountService = accounts.NewService(accounts.NewStore(pool), userService,
			cfg.FirstPartyApplication, logger)
		sessionService = sessions.NewService(sessions.NewStore(pool), accountService,
			applicationService, userService, cfg.FirstPartyApplication, logger)
	}

	/*
		Both surfaces are authenticated, so both are always served. The tenant
		surface takes its tenant from an application's key; the operator
		surface names the tenant in the path and proves the authority to reach
		it with an operator key, which no application can hold.
	*/
	dependencies := server.Dependencies{
		Database:       pool,
		TrustedProxies: cfg.TrustedProxies,

		OperatorAuthenticator: operatorService,
		Applications:          applications.NewHandler(logger, applicationService),
		Users:                 users.NewHandler(logger, userService),
		Credentials:           credentials.NewHandler(logger, credentialService),
		Rooms:                 rooms.NewHandler(logger, roomService),
		Calls:                 calls.NewHandler(logger, callService),
		Participants:          participants.NewHandler(logger, participantService),
		OperatorCredentials:   operator.NewHandler(logger, operatorService),

		Authenticator:      credentialService,
		TenantUsers:        users.NewTenantHandler(logger, userService),
		TenantCredentials:  credentials.NewTenantHandler(logger, credentialService),
		TenantRooms:        rooms.NewTenantHandler(logger, roomService),
		TenantCalls:        calls.NewTenantHandler(logger, callService),
		TenantParticipants: participants.NewTenantHandler(logger, participantService),
		TenantInvitations:  invitations.NewTenantHandler(logger, invitationService),
		TenantEvents:       events.NewTenantHandler(logger, broker),
		TenantWebhooks:     webhooks.NewTenantHandler(logger, webhookService),
		TenantPresence:     presence.NewTenantHandler(logger, presenceService),

		InvitationAuthenticator: invitationService,
		Invitations:             invitations.NewHolderHandler(logger, invitationService),

		IdempotencyKeys: idempotencyService,
	}

	/*
		Assigned only when there is a first-party application, so that a nil
		service never reaches the route table as a non-nil interface holding a
		nil pointer — which would register the routes and then panic on the
		first request.
	*/
	if sessionService != nil {
		dependencies.SessionAuthenticator = sessionService
		dependencies.Sessions = sessions.NewHandler(logger, sessionService)
		warnIfNobodyCanSignIn(signalContext, logger, accountService)
	} else {
		logger.Info("no first-party application is configured, so nobody signs in to Convia itself",
			"remedy", "set CONVIA_FIRST_PARTY_APPLICATION to serve Convia's own interface")
	}

	warnIfUnadministered(signalContext, logger, operatorService)

	/*
		The delivery worker. Its owner is this function, its lifetime is the
		process, and cancelling delivering is how it stops. A delivery
		interrupted by that is not lost: its lease expires and the next worker
		to look — this one after a restart, or another instance — takes it
		again.
	*/
	if relay != nil {
		/*
			Events produced on the other instances arrive here and go straight
			to the broker, which decides who is entitled to them exactly as it
			does for an event produced locally. The goroutine's owner is this
			function, and closing the relay below is what ends it.
		*/
		go func() {
			for event := range relay.Events() {
				broker.Receive(event)
			}
		}()

		defer func() {
			if err := relay.Close(); err != nil {
				logger.Warn("closing the shared channel", "error", err)
			}
			if dropped := relay.Dropped(); dropped > 0 {
				logger.Warn("events did not reach the other instances during this run",
					"dropped", dropped)
			}
		}()
	}

	delivering, stopDelivering := context.WithCancel(context.Background())
	defer stopDelivering()

	/*
		The presence sweeper. Expiry is a timer, and a timer tells nobody: this
		is what turns a claim lapsing into an event a subscriber can see.
		Cancelling is how it stops, and a pass that does not happen costs an
		announcement rather than an answer — every read already ignores a claim
		past its deadline.
	*/
	go presence.NewSweeper(presenceService, logger).Run(delivering)

	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		dispatcher.Run(delivering)
	}()

	httpServer := server.New(cfg.Address(), logger, dependencies)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	logger.Info("HTTP server starting", "address", httpServer.Addr, "environment", cfg.Environment)

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	/*
		Event streams are closed before the server is, and deliberately in that
		order. A stream is a hijacked connection, which http.Server.Shutdown
		neither tracks nor waits for, so leaving it to the server would mean
		the process exiting while subscribers were still holding sockets nobody
		had said anything to. Telling them first costs the moment it takes to
		write a close frame and turns a broken connection into a reason.
	*/
	if !broker.Stop(shutdownContext) {
		logger.Warn("some event streams did not close before the shutdown deadline",
			"active_streams", broker.Active())
	}

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}

	/*
		Deliveries stop after the server does, which is the order that loses
		nothing. Stopping first would leave the last requests queuing work for a
		worker that had already gone, and that work would then wait for another
		instance or for this one to restart.
	*/
	stopDelivering()
	select {
	case <-delivered:
	case <-shutdownContext.Done():
		logger.Warn("the webhook worker did not stop before the shutdown deadline")
	}

	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}

	logger.Info("HTTP server stopped")
	return nil
}

/*
openMediaPlane builds whatever will carry this instance's audio and video.

This is the one place in Convia that decides which media plane is in use, and
the only place outside internal/media that names a provider at all. Everything
downstream sees the two operations internal/calls declares and cannot tell the
difference.

Running with no media plane is a supported deployment, not a degraded one, so
it is reported at info rather than as a warning: an operator running the
control plane on its own should not be told every start-up that something is
wrong. What would be wrong is a half-configured one, and internal/config
refuses that before this is reached.
*/
func openMediaPlane(settings config.Media, logger *slog.Logger) (calls.MediaPlane, error) {
	if !settings.Configured() {
		logger.Info("no media plane is configured, so calls carry no audio or video",
			"remedy", "set CONVIA_LIVEKIT_URL, CONVIA_LIVEKIT_API_KEY, and CONVIA_LIVEKIT_API_SECRET")
		return media.Absent{}, nil
	}

	plane, err := livekit.New(livekit.Config{
		URL:       settings.URL,
		ClientURL: settings.ClientURL,
		APIKey:    settings.APIKey,
		APISecret: settings.APISecret,
		Timeout:   settings.Timeout,
	})

	if err != nil {
		return nil, fmt.Errorf("open the media plane: %w", err)
	}

	// The URL is logged and the key is not: one is an address, the other is
	// half of a credential.
	logger.Info("media plane configured", "url", settings.URL, "timeout", settings.Timeout)
	return plane, nil
}

/*
openRelay builds whatever carries events to the other instances, if any.

A nil relay is a supported deployment and the ordinary one: a single instance
needs nothing carried anywhere, and every subscriber is served by the instance
that produced the event. That is reported at info rather than as a warning, for
the same reason a missing media plane is — an operator running one instance
should not be told at every start-up that something is wrong.

What Convia cannot tell from inside one process is whether *several* instances
are running with this unset, which is the configuration that is quietly wrong.
docs/events.md states it as an operational requirement, and the line logged
here is what an operator compares against what they deployed.

Reaching the channel is checked once and does not stop the process. An instance
that refused to start because Redis was unreachable would take a working API
offline over a stream that degrades to what it was before M16 — so the failure
is reported loudly and serving continues.
*/
func openRelay(settings config.Redis, logger *slog.Logger) (*redis.Relay, error) {
	if !settings.Configured() {
		logger.Info("no shared channel is configured, so event streams are served by this instance alone",
			"remedy", "set CONVIA_REDIS_URL when running more than one instance")
		return nil, nil
	}

	/*
		The origin is generated per process rather than configured. Its only job
		is to let this instance recognize its own messages coming back on the
		channel it published to, so it has to be unique per process and means
		nothing beyond that — a value an operator had to set would be one they
		could set the same on two machines.
	*/
	origin := "ins_" + rand.Text()

	relay, err := redis.New(redis.Config{
		URL:     settings.URL,
		Timeout: settings.Timeout,
		Origin:  origin,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("open the shared channel: %w", err)
	}

	// The address is logged and the password is not: one is a location, the
	// other is half of a credential.
	logger.Info("shared channel configured", "address", redis.Redacted(settings.URL),
		"origin", origin, "timeout", settings.Timeout)

	probe, cancel := context.WithTimeout(context.Background(), settings.Timeout)
	defer cancel()

	if err := relay.Ping(probe); err != nil {
		logger.Error("the shared channel could not be reached, so event streams are served by this instance alone until it comes back",
			"error", err, "address", redis.Redacted(settings.URL))
	}
	return relay, nil
}

/*
openPresence builds the store presence lives in, and returns how to close it.

There is no nil case here, unlike the relay. Presence always has somewhere to
live: with one instance that is this process, and the in-process store is the
whole implementation rather than a fallback — there is nowhere else for it to
be, and a network round trip to answer a question this process already knows
would be worse in every respect.

What the shared store adds is convergence between instances, and it is
configured by the same setting that already decides whether the event stream
spans them. An unreachable store is reported and does not stop the process:
presence is the shortest-lived thing Convia holds, and refusing to serve calls,
rooms, and webhooks over it would be the wrong trade by a wide margin.
*/
func openPresence(settings config.Redis, logger *slog.Logger) (presence.Store, func(), error) {
	if !settings.Configured() {
		logger.Info("presence is held by this instance alone",
			"remedy", "set CONVIA_REDIS_URL when running more than one instance")
		return presence.NewMemory(), func() {}, nil
	}

	store, err := presenceredis.New(presenceredis.Config{
		URL:     settings.URL,
		Timeout: settings.Timeout,
	}, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("open the presence store: %w", err)
	}

	// The address is logged and the password is not: one is a location, the
	// other is half of a credential.
	logger.Info("shared presence store configured",
		"address", presenceredis.Redacted(settings.URL), "timeout", settings.Timeout)

	probe, cancel := context.WithTimeout(context.Background(), settings.Timeout)
	defer cancel()

	if err := store.Ping(probe); err != nil {
		logger.Error("the presence store could not be reached, so presence is unavailable until it comes back",
			"error", err, "address", presenceredis.Redacted(settings.URL))
	}

	return store, func() {
		if err := store.Close(); err != nil {
			logger.Warn("closing the presence store", "error", err)
		}
	}, nil
}

/*
warnIfNobodyCanSignIn reports a session surface nobody can reach.

A deployment that configured a first-party application and created no accounts
serves a sign-in form that every password fails against, and nothing about that
looks like a misconfiguration from the outside. It is the same advisory
warnIfUnadministered gives for an instance with no operator, and it names the
command that fixes it for the same reason.
*/
func warnIfNobodyCanSignIn(ctx context.Context, logger *slog.Logger, service *accounts.Service) {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	total, err := service.CountActive(probe)
	if err != nil {
		logger.Warn("could not check whether anybody can sign in", "error", err)
		return
	}

	if total == 0 {
		logger.Warn("a first-party application is configured but no account can sign in",
			"remedy", "create one with: convia account create <email> <display-name>")
	}
}

/*
warnIfUnadministered reports an instance nobody can administer.

With no active operator credential the whole operator surface answers 401,
including the endpoints that create tenants. That is the correct posture for a
fresh instance rather than a fault, but it is also the state an operator is
least likely to have intended and most likely to discover by being refused. It
is a warning, never a failure to start: refusing to serve would take the tenant
surface down with it, and the applications already using it are unaffected.

A failure to count is logged and otherwise ignored. Being unable to run one
advisory query is not a reason to hold up startup, and readiness already covers
a database that is genuinely unreachable.
*/
func warnIfUnadministered(ctx context.Context, logger *slog.Logger, service *operator.Service) {
	active, err := service.CountActive(ctx)
	if err != nil {
		logger.Warn("could not count operator credentials at startup", "error", err)
		return
	}
	if active > 0 {
		return
	}

	logger.Warn("no active operator credential exists, so the operator API refuses every request",
		"remedy", "convia operator issue <name>")
}
