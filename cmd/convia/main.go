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

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
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
		case "migrate", "serve":
		default:
			fmt.Print(usage)
			return fmt.Errorf("unknown command %q", arguments[0])
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	if len(arguments) == 0 || arguments[0] == "serve" {
		return serve(ctx, logger, cfg)
	}

	return migrate(ctx, logger, cfg, arguments[1:])
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

	dependencies := server.Dependencies{Database: pool}
	if cfg.AdminAPI {
		applicationService := applications.NewService(applications.NewStore(pool), logger)

		dependencies.Applications = applications.NewHandler(logger, applicationService)
		dependencies.Users = users.NewHandler(logger, users.NewService(users.NewStore(pool), applicationService, logger))

		logger.Warn("the administrative API is enabled and is not authenticated yet",
			"endpoints", api.Prefix+"/applications")
	}

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
