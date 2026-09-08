package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/credentials"
	"convia/internal/database"
	"convia/internal/operator"
	"convia/internal/server"
	"convia/internal/users"
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
		case "migrate", "serve", "operator":
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
		OperatorCredentials:   operator.NewHandler(logger, operatorService),

		Authenticator:     credentialService,
		TenantUsers:       users.NewTenantHandler(logger, userService),
		TenantCredentials: credentials.NewTenantHandler(logger, credentialService),
	}

	warnIfUnadministered(signalContext, logger, operatorService)

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

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}

	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}

	logger.Info("HTTP server stopped")
	return nil
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
