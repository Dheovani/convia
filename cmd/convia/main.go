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
	"convia/internal/audit"
	"convia/internal/audit/reading"
	"convia/internal/calls"
	"convia/internal/config"
	"convia/internal/credentials"
	"convia/internal/database"
	"convia/internal/departure"
	"convia/internal/erasure"
	"convia/internal/events"
	"convia/internal/events/journal"
	"convia/internal/events/redis"
	"convia/internal/events/serving"
	"convia/internal/export"
	"convia/internal/idempotency"
	"convia/internal/invitations"
	"convia/internal/media"
	"convia/internal/media/livekit"
	"convia/internal/messages"
	"convia/internal/operator"
	"convia/internal/participants"
	"convia/internal/peers"
	"convia/internal/presence"
	presenceredis "convia/internal/presence/redis"
	"convia/internal/rooms"
	"convia/internal/server"
	"convia/internal/sessions"
	"convia/internal/telemetry"
	"convia/internal/users"
	"convia/internal/web"
	"convia/internal/webhooks"
)

const shutdownTimeout = 10 * time.Second

/*
measurementFlush bounds reporting the last interval on the way out.

It is short because it happens after everything else has stopped, with a person
or an orchestrator waiting: a collector that is unreachable should delay the
exit by a couple of seconds and then be given up on, not hold the process open
until something kills it.
*/
const measurementFlush = 3 * time.Second

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
  audit:read

An operator credential works for 90 days. The secret is printed once and never stored. Running these commands requires
database access, which is the authority the first credential is minted from.

An account is a person who signs in to Convia's own interface. People create
their own from the sign-in page, with a username and a password nobody else
sees. Whoever runs Convia can stop somebody signing in, and let them back:

  convia account suspend <account-id>    Stop somebody signing in
  convia account activate <account-id>   Let them sign in again
`

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		/*
			The bootstrap logger, and the only thing it is for.

			It carries no service attributes because the one failure that
			reaches here without any is the configuration being unreadable —
			and at that point there is nothing to describe the process with.
		*/
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("convia stopped", "error", err)
		os.Exit(1)
	}
}

/*
describing builds the logger every line goes through once configuration is read.

**Four attributes on every line**, and each answers a question somebody asks
during an incident: what is this, which build, which deployment, and which
instance. The last is the one that is useless until a deployment is replicated
and impossible to add afterwards — which is why it is here now rather than when
it is needed.

They are attached to the logger rather than passed at call sites, because an
attribute somebody has to remember is one a new call site will forget, and there
are already several hundred of those.
*/
func describing(cfg config.Config) *slog.Logger {
	service := telemetry.Describing(string(cfg.Environment), cfg.ServiceInstance)

	/*
		Correlation wraps the writer and the service attributes are attached
		outside it, in that order and not the other way round: `With` returns a
		handler from the one it is called on, so attaching first and wrapping
		second would put the wrapper above attributes it can no longer see — and
		wrapping the wrapper's result is what `Correlating.WithAttrs` exists to
		survive.
	*/
	handler := telemetry.Correlate(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	return slog.New(handler).With(attributes(service.Describe())...)
}

// attributes widens a slice of attributes into the arguments With takes.
func attributes(described []slog.Attr) []any {
	widened := make([]any, 0, len(described))
	for _, attribute := range described {
		widened = append(widened, attribute)
	}
	return widened
}

/*
run dispatches the requested command.

The composition root loads configuration, builds dependencies, and owns the
process lifecycle. Migrations are a separate command rather than a startup
step, so that schema changes stay an explicit operational decision.
*/
func run(ctx context.Context, arguments []string) error {
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

	/*
		The logger is built here rather than handed in, because until
		configuration is read there is nothing to describe the process with and
		no level to obey.

		Nothing above this line logs. What can fail up there is reading the
		configuration, and that is returned to main — which has its own plain
		logger for exactly this, since a line saying the configuration could not
		be read is more use than the same line in a deployment that could not be
		identified anyway.
	*/
	logger := describing(cfg)

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

	/*
		Measurement is built before anything it measures, and its shutdown is
		deferred first so that it runs last.

		The final interval's measurements are still in memory when a process is
		asked to stop, and a deployment that reports nothing about its last
		minute reports nothing about the minute that usually matters. The
		shutdown context is deliberately not the signal one: it has already been
		cancelled by the time this runs.
	*/
	service := telemetry.Describing(string(cfg.Environment), cfg.ServiceInstance)

	meters, stopMeasuring, err := telemetry.Measuring(signalContext, service, cfg.MetricsEndpoint)
	if err != nil {
		return fmt.Errorf("start measuring: %w", err)
	}
	defer func() {
		flushing, cancel := context.WithTimeout(context.WithoutCancel(ctx), measurementFlush)
		defer cancel()

		if err := stopMeasuring(flushing); err != nil {
			logger.WarnContext(flushing, "the last measurements could not be reported", "error", err)
		}
	}()

	httpMetrics, err := telemetry.Serve(meters)
	if err != nil {
		return fmt.Errorf("build the request instruments: %w", err)
	}

	/*
		Tracing, with the same shape and the same shutdown discipline. Spans
		are batched, so a process that exits without flushing loses the trace
		of whatever it was doing when it was asked to stop — which is the trace
		somebody wanted.
	*/
	tracers, stopTracing, err := telemetry.Tracing(signalContext, service, cfg.TracesEndpoint, cfg.TraceSample)
	if err != nil {
		return fmt.Errorf("start tracing: %w", err)
	}
	defer func() {
		flushing, cancel := context.WithTimeout(context.WithoutCancel(ctx), measurementFlush)
		defer cancel()

		if err := stopTracing(flushing); err != nil {
			logger.WarnContext(flushing, "the last spans could not be reported", "error", err)
		}
	}()

	requests := telemetry.Trace(tracers)

	pool, err := database.Open(signalContext, cfg.Database, logger, telemetry.Query(tracers))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	/*
		What the pool is doing, beside what each query does.

		The tracer above answers "why was this request slow" and says
		nothing about "are requests slow": a trace is one occurrence. The
		one that matters here is waiting -- a pool that has handed out every
		connection makes the next query queue, and the symptom is a slow
		handler with nothing in its own timings to explain it.
	*/
	pools, err := telemetry.Holding(meters)
	if err != nil {
		return fmt.Errorf("measure the database pool: %w", err)
	}
	/*
		Read when the exporter asks, and translated here rather than in the
		telemetry package: what that package needs is five numbers, and
		having it import the driver would make every future pool a pgx one.
	*/
	pools.Attach("primary", func() telemetry.PoolStats {
		counted := pool.Stat()
		return telemetry.PoolStats{
			Idle:    int64(counted.IdleConns()),
			Used:    int64(counted.AcquiredConns()),
			Limit:   int64(counted.MaxConns()),
			Waits:   counted.EmptyAcquireCount(),
			Waiting: counted.EmptyAcquireWaitTime(),
		}
	})

	/*
		What Convia is carrying right now, asked of the database when somebody
		collects rather than counted as calls start and end — a number kept in
		memory would start at zero on every restart while the calls it counts
		are still going on.
	*/
	if err := telemetry.Watch(meters, calls.NewStore(pool), participants.NewStore(pool), logger); err != nil {
		return fmt.Errorf("watch what Convia is carrying: %w", err)
	}

	/*
		The trail comes before the services that write to it, because they take
		it rather than reach for it: an entry is written by the change that
		caused it, in the transaction the change opened, and a service that
		found the trail for itself could be given one that writes somewhere
		else.
	*/
	auditService := audit.NewService(audit.NewStore(pool), logger)

	applicationService := applications.NewService(applications.NewStore(pool),
		auditService)
	userService := users.NewService(users.NewStore(pool), applicationService, auditService)
	credentialService := credentials.NewService(credentials.NewStore(pool), applicationService,
		auditService)
	operatorService := operator.NewService(operator.NewStore(pool), auditService)
	mediaPlane, mediaReports, err := openMediaPlane(cfg.Media, logger)
	if err != nil {
		return err
	}

	/*
		And the media plane, whose address is operator configuration — so
		carrying Convia's trace there is carrying it somewhere the deployment
		chose. A Convia with no media plane has nothing to wrap.
	*/
	if mediaReports != nil {
		mediaReports.Trace(func(inner http.RoundTripper) http.RoundTripper {
			return telemetry.Call(inner, tracers, "media.request", true)
		})
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
		What the streams are doing. The ending counter is the one worth
		watching: `behind` means somebody's view of a conversation had a hole
		in it, which is invisible everywhere else — the subscriber reconnects
		and everything looks healthy again.
	*/
	streams, err := telemetry.Follow(meters, broker)
	if err != nil {
		return fmt.Errorf("follow the event streams: %w", err)
	}
	broker.Watch(streams)

	/*
		Which addresses this instance is willing to reach is decided here, from
		the environment and from nothing else. A development instance may deliver
		to a receiver on localhost, because that is how anybody tests a webhook;
		production may not, and there is deliberately no setting that changes
		that. See docs/webhooks.md.
	*/
	destinations := webhooks.NewDestinations(cfg.Environment == config.Development)
	webhookStore := webhooks.NewStore(pool)
	webhookService := webhooks.NewService(webhookStore, applicationService, destinations, auditService)
	dispatcher := webhooks.NewDispatcher(webhookStore, destinations, logger)

	/*
		Webhook deliveries appear in the trace of the request that caused them,
		and **carry Convia's trace outward**: an application receiving one can
		join its own trace to the operation behind it, which is the point of
		propagating at all and is that application's own data.

		It is not done for requests to another installation. A home is chosen
		by anybody who can send an invitation, and handing one a trace
		identifier would let it correlate several of Convia's requests as one
		operation — a small channel Convia gets nothing back for. See
		docs/threat-model.md on what a signature does and does not prove.
	*/
	dispatcher.Trace(func(inner http.RoundTripper) http.RoundTripper {
		return telemetry.Call(inner, tracers, "webhook.deliver", true)
	})

	/*
		Events that must not be lost are recorded in the journal and queued for
		webhooks by the transaction that made them happen. This instance follows
		the journal to feed its own streams, and replays it to a stream that
		resumes. See docs/adr/0017.
	*/
	eventJournal := journal.New(pool)
	follower, err := journal.NewFollower(signalContext, eventJournal, broker, logger)
	if err != nil {
		return fmt.Errorf("start following the event journal: %w", err)
	}
	announcer := serving.NewJournaledAnnouncer(broker, eventJournal, dispatcher, follower.Wake, logger)

	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, userService, announcer,
		auditService, logger)

	callService := calls.NewService(calls.NewStore(pool), applicationService, roomService,
		mediaPlane, announcer,
		auditService, logger)
	participantService := participants.NewService(participants.NewStore(pool),
		applicationService, callService, roomService, userService, announcer,
		auditService, logger)

	/*
		A room tells the call it is holding when it changes under it: deleted,
		or somebody losing their place. The participants service is built from
		the rooms service, so it is attached afterwards rather than passed in,
		and before anything is served.
	*/
	roomService.InformCalls(participantService)
	invitationService := invitations.NewService(invitations.NewStore(pool),
		applicationService, callService, userService, participantService, announcer, auditService, logger)
	messageService := messages.NewService(messages.NewStore(pool),
		applicationService, roomService, userService, invitationService, announcer, logger)
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

	/*
		Both uses of Redis, measured through one set of instruments.

		The question these answer is the one an operator has when Convia feels
		slow: is it Convia, or is it Redis. Without them the answer is a guess,
		because every symptom shows up somewhere else — a pool with no free
		connections looks like a slow handler, and a Redis that times out looks
		like presence being wrong.

		A deployment with no Redis has nothing to attach, which is a single
		instance rather than a failure.
	*/
	shared, err := telemetry.Connected(meters)
	if err != nil {
		return fmt.Errorf("measure the shared store: %w", err)
	}
	shared.Tracing(tracers)
	shared.Measure("presence", presenceStore)
	if relay != nil {
		shared.Measure("events", relay)
	}

	presenceService := presence.NewService(presenceStore, applicationService, userService, announcer, logger)

	/*
		How much presence there is, and how much of it has gone stale. The
		second is the one worth watching: a claim past its deadline changes no
		answer, because every read already ignores it — what it says is whether
		the sweeper is keeping up with telling subscribers somebody left.
	*/
	if err := telemetry.Attend(meters, func(ctx context.Context) (int64, int64, error) {
		standing, err := presenceStore.Standing(ctx)
		return standing.Claims, standing.Overdue, err
	}, logger); err != nil {
		return fmt.Errorf("attend to presence: %w", err)
	}

	/*
		Convia's own product, which every installation serves.

		Its application is made here on the first start and left alone on every
		later one, so a person who installed Convia can register and sign in
		without anybody administering a tenant first. Nothing is configured: an
		instance that needed a setting before anybody could sign in used to
		serve a sign-in form that refused everyone and said nothing useful.
	*/
	if err := applicationService.EnsureFirstParty(signalContext); err != nil {
		return err
	}

	accountService := accounts.NewService(accounts.NewStore(pool), userService,
		applications.FirstPartyID,
		auditService, logger)
	sessionService := sessions.NewService(sessions.NewStore(pool), accountService,
		applicationService, userService, applications.FirstPartyID, auditService, logger)

	/*
		Rooms shared with other installations. The client that reaches them is
		built on the same destination guard as webhook delivery, because an
		invitation link is an address somebody else chose, with one difference:
		whether it may reach the private network is a setting of its own rather
		than the environment's, because anybody who registers can make it follow
		a link. See docs/peers.md.
	*/
	/*
		The address other installations reach this one at, if an operator said.

		It is checked here, once, rather than on the request that needs it: a
		value nobody can make a link out of is a mistake in the configuration,
		and a mistake in the configuration stops the process rather than
		producing links that quietly name the wrong place.
	*/
	publicAddress := ""
	if cfg.PublicAddress != "" {
		publicAddress, err = peers.ParseHome(cfg.PublicAddress)
		if err != nil {
			return fmt.Errorf("CONVIA_PUBLIC_ADDRESS must be an http or https address with no path, such as https://convia.example: %w", err)
		}
		logger.Info("invitation links will name this installation by its configured address", "address", publicAddress)
	}

	peerDestinations := destinations.WithPrivateAddresses(cfg.PeersAllowPrivateAddresses)
	if cfg.PeersAllowPrivateAddresses {
		logger.Warn("links between installations may reach this server's private network, and anybody who registers can follow one",
			"remedy", "unset CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES unless the installations you share rooms with are on that network")
	}
	peerClient := peers.NewClient(peerDestinations)
	peerService := peers.NewService(peers.NewStore(pool), roomService, userService, applicationService,
		accountService, peerClient, applications.FirstPartyID, publicAddress, auditService, logger)

	/*
		Both surfaces are authenticated, so both are always served. The tenant
		surface takes its tenant from an application's key; the operator
		surface names the tenant in the path and proves the authority to reach
		it with an operator key, which no application can hold.
	*/
	// Deleting an account reaches every domain a person acts in. See docs/adr/0016.
	departures := departure.NewHandler(logger,
		departure.NewService(accountService, peerService, roomService, messageService, userService, logger),
		sessionService)

	/*
		Handing somebody their own data reaches four domains and owns none of
		them, so it is assembled here like departure above — and it is
		departure's opposite: what this writes out is what erasure takes away.
	*/
	exports := export.NewService(userService, rooms.NewStore(pool),
		messages.NewStore(pool), participants.NewStore(pool))

	dependencies := server.Dependencies{
		Database:                pool,
		TrustedProxies:          cfg.TrustedProxies,
		TenantRequestsPerMinute: cfg.TenantRequestsPerMinute,
		PersonRequestsPerMinute: cfg.PersonRequestsPerMinute,
		Serving:                 httpMetrics,
		Requests:                requests,

		OperatorAuthenticator: operatorService,
		Applications:          applications.NewHandler(logger, applicationService),
		Users:                 users.NewHandler(logger, userService),
		Credentials:           credentials.NewHandler(logger, credentialService),
		Rooms:                 rooms.NewHandler(logger, roomService),
		Calls:                 calls.NewHandler(logger, callService),
		Participants:          participants.NewHandler(logger, participantService),
		OperatorCredentials:   operator.NewHandler(logger, operatorService),
		Audit:                 reading.NewHandler(logger, auditService),

		Authenticator:      credentialService,
		TenantUsers:        users.NewTenantHandler(logger, userService),
		TenantCredentials:  credentials.NewTenantHandler(logger, credentialService),
		TenantRooms:        rooms.NewTenantHandler(logger, roomService),
		TenantCalls:        calls.NewTenantHandler(logger, callService),
		TenantParticipants: participants.NewTenantHandler(logger, participantService),
		TenantInvitations:  invitations.NewTenantHandler(logger, invitationService),
		TenantMessages:     messages.NewTenantHandler(logger, messageService),
		PersonalMessages:   messages.NewSessionHandler(logger, messageService, roomService),
		PersonalRooms:      rooms.NewSessionHandler(logger, roomService, userService),
		TenantEvents:       serving.NewTenantHandler(logger, broker, follower),
		TenantWebhooks:     webhooks.NewTenantHandler(logger, webhookService),
		Webhooks:           webhooks.NewOperatorHandler(logger, webhookService),
		TenantPresence:     presence.NewTenantHandler(logger, presenceService),
		PersonalPresence:   presence.NewPersonalHandler(logger, presenceService, roomService),

		InvitationAuthenticator: invitationService,
		Invitations:             invitations.NewHolderHandler(logger, invitationService),

		IdempotencyKeys: idempotencyService,

		SessionAuthenticator: sessionService,
		Sessions:             sessions.NewHandler(logger, sessionService),
		Departures:           departures,
		PersonalExport:       export.NewSessionHandler(logger, exports, applications.FirstPartyID),
		TenantExport:         export.NewTenantHandler(logger, exports),
		PersonalEvents: serving.NewPersonHandler(logger, broker, follower, sessionService, peerService,
			peers.NewFollowing(peerService, peerService, sessionService, peerClient, logger), roomService),

		PeerAuthenticator: peerService,
		Peers:             peers.NewPeerHandler(logger, peerService),
		RoomInvitations:   peers.NewSessionHandler(logger, peerService, sessionService, publicAddress),

		PersonalCalls: participants.NewSessionHandler(logger, participantService, roomService, userService),
	}

	/*
		Only a media plane that exists can report anything, so the route a
		report is sent to is served only when one is configured.
	*/
	mediaAddress := ""
	if mediaReports != nil {
		dependencies.MediaReporter = mediaReports
		dependencies.MediaReports = participants.NewReportHandler(logger, participantService)
		mediaAddress = mediaReports.ClientURL()
	}

	/*
		Convia's own page, served from this same origin at every path the API
		has not claimed.

		A binary built without the frontend bundle says so at startup and in
		the page itself, rather than being silently absent.
	*/
	site := web.New(logger, mediaAddress)
	dependencies.Interface = site
	if !site.Built() {
		logger.Warn("this binary carries no interface, so only the API is served",
			"remedy", "build it with: cd web && npm install && npm run build")
	}

	warnIfUnadministered(signalContext, logger, operatorService)

	/*
		The delivery worker. Its owner is this function, its lifetime is the
		process, and cancelling delivering is how it stops. A delivery
		interrupted by that is not lost: its lease expires and the next worker
		to look â€” this one after a restart, or another instance â€” takes it
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
		announcement rather than an answer â€” every read already ignores a claim
		past its deadline.
	*/
	go presence.NewSweeper(presenceService, logger).Run(delivering)

	/*
		The nonce janitor. Every signed request between installations writes one
		down, and a nonce stops refusing anything once the timestamp it came
		with would be too old to accept — from then on it is a row nobody reads.
	*/
	go peers.NewJanitor(peers.NewStore(pool), logger).Run(delivering)

	/*
		The erasure janitor. Deleting a user keeps the row, so that the deletion
		stays recoverable and the external subject stays taken — and until this
		existed, nothing ever gave either of them back. It is the one retention
		rule Convia has, and the only one it needs: everything else is kept
		until somebody asks for it to go, and a deleted person asked.
	*/
	go erasure.NewJanitor(users.NewStore(pool), messageService, cfg.ErasureWindow, logger).
		Run(delivering)

	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		dispatcher.Run(delivering)
	}()

	// The journal is followed for as long as deliveries run.
	go follower.Run(delivering)

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

The adapter is returned a second time, as itself, because it is also what
verifies the reports the media plane sends. It is nil when there is no media
plane, and the caller serves no report route then.
*/
func openMediaPlane(settings config.Media, logger *slog.Logger) (calls.MediaPlane, *livekit.Plane, error) {
	if !settings.Configured() {
		logger.Info("no media plane is configured, so calls carry no audio or video",
			"remedy", "set CONVIA_LIVEKIT_URL, CONVIA_LIVEKIT_API_KEY, and CONVIA_LIVEKIT_API_SECRET")
		return media.Absent{}, nil, nil
	}

	plane, err := livekit.New(livekit.Config{
		URL:       settings.URL,
		ClientURL: settings.ClientURL,
		APIKey:    settings.APIKey,
		APISecret: settings.APISecret,
		Timeout:   settings.Timeout,

		PreviousAPIKey:    settings.PreviousAPIKey,
		PreviousAPISecret: settings.PreviousAPISecret,
	})

	if err != nil {
		return nil, nil, fmt.Errorf("open the media plane: %w", err)
	}

	// The URL is logged and the key is not: one is an address, the other is
	// half of a credential.
	logger.Info("media plane configured", "url", settings.URL, "timeout", settings.Timeout,
		"reports", "/media/reports")
	return plane, plane, nil
}

/*
openRelay builds whatever carries presence to the other instances, if any.
Recorded events need no relay: every instance follows the journal.

A nil relay is a supported deployment and the ordinary one: a single instance
needs nothing carried anywhere, and every subscriber is served by the instance
that produced the change. That is reported at info rather than as a warning, for
the same reason a missing media plane is â€” an operator running one instance
should not be told at every start-up that something is wrong.

What Convia cannot tell from inside one process is whether *several* instances
are running with this unset, which is the configuration that is quietly wrong.
docs/events.md states it as an operational requirement, and the line logged
here is what an operator compares against what they deployed.

Reaching the channel is checked once and does not stop the process. An instance
that refused to start because Redis was unreachable would take a working API
offline over presence that degrades to what it was before M16 â€” so the failure
is reported loudly and serving continues.
*/
func openRelay(settings config.Redis, logger *slog.Logger) (*redis.Relay, error) {
	if !settings.Configured() {
		logger.Info("no shared channel is configured, so presence changes reach this instance's streams alone",
			"remedy", "set CONVIA_REDIS_URL when running more than one instance")
		return nil, nil
	}

	/*
		The origin is generated per process rather than configured. Its only job
		is to let this instance recognize its own messages coming back on the
		channel it published to, so it has to be unique per process and means
		nothing beyond that â€” a value an operator had to set would be one they
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
		logger.Error("the shared channel could not be reached, so presence changes reach this instance's streams alone until it comes back",
			"error", err, "address", redis.Redacted(settings.URL))
	}
	return relay, nil
}

/*
openPresence builds the store presence lives in, and returns how to close it.

There is no nil case here, unlike the relay. Presence always has somewhere to
live: with one instance that is this process, and the in-process store is the
whole implementation rather than a fallback â€” there is nowhere else for it to
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
	standing, err := service.Standing(ctx)
	if err != nil {
		logger.Warn("could not count operator credentials at startup", "error", err)
		return
	}

	for _, warning := range administrationWarnings(standing, time.Now()) {
		logger.Warn(warning.message, "remedy", warning.remedy)
	}
}

// warning is one thing an operator should hear about at startup, and what to do.
type warning struct {
	message string
	remedy  string
}

// expiringSoon is how far ahead the last operator key's expiry is announced.
const expiringSoon = 14 * 24 * time.Hour

/*
administrationWarnings says what is wrong with who can administer Convia.

Three states are worth a line at startup, because each is otherwise discovered
by being refused: nobody can administer it; somebody can, with a key from before
every key expired; and the last key that works is about to stop. The last is
what `M21-002` made possible -- a bounded key is a key that runs out -- and a
warning two weeks ahead is what keeps that from being an outage.
*/
func administrationWarnings(standing operator.Standing, at time.Time) []warning {
	if standing.Active == 0 {
		return []warning{{
			message: "no active operator credential exists, so the operator API refuses every request",
			remedy:  "convia operator issue <name>",
		}}
	}

	var warnings []warning
	if standing.Unbounded > 0 {
		warnings = append(warnings, warning{
			message: fmt.Sprintf("%d operator credentials never expire; they were issued before every key had a lifetime", standing.Unbounded),
			remedy:  "issue replacements, then revoke the old keys; see docs/authentication.md",
		})
	}
	if standing.Unbounded == 0 && standing.LastExpiry != nil && standing.LastExpiry.Sub(at) < expiringSoon {
		warnings = append(warnings, warning{
			message: "the last working operator credential expires at " + standing.LastExpiry.Format(time.RFC3339) +
				", after which the operator API refuses every request",
			remedy: "issue a new operator credential before then",
		})
	}
	return warnings
}
