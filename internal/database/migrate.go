package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver for database/sql
	"github.com/pressly/goose/v3"
)

// migrationDirectory is the embedded location of the migration files.
const migrationDirectory = "migrations"

//go:embed migrations/*.sql
var migrationFiles embed.FS

/*
Migrate applies every migration that has not been applied yet.

Migrations run through a dedicated short-lived connection rather than the
service pool, because migration is an operational task with its own lifetime.
Convia never migrates implicitly at startup: an operator or a deployment step
runs `convia migrate up` so that schema changes stay observable and ordered
relative to a rolling release.
*/
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	return withProvider(ctx, databaseURL, logger, func(database *sql.DB) error {
		return goose.UpContext(ctx, database, migrationDirectory)
	})
}

/*
Rollback reverts the most recently applied migration.

It exists for recovering a failed deployment in an environment where the
previous schema is still correct. Reverting a migration that has already
dropped data cannot restore it, so a rollback is never a substitute for a
backup.
*/
func Rollback(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	return withProvider(ctx, databaseURL, logger, func(database *sql.DB) error {
		return goose.DownContext(ctx, database, migrationDirectory)
	})
}

// Status writes the applied and pending migrations to the migration logger.
func Status(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	return withProvider(ctx, databaseURL, logger, func(database *sql.DB) error {
		return goose.StatusContext(ctx, database, migrationDirectory)
	})
}

/*
withProvider opens a migration connection and runs one migration operation.

Every migration entry point shares this setup so that the dialect, the embedded
file system, and the logger are configured identically.
*/
func withProvider(ctx context.Context, databaseURL string, logger *slog.Logger, operation func(*sql.DB) error) error {
	goose.SetBaseFS(migrationFiles)
	goose.SetLogger(migrationLogger{logger: logger})

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("select migration dialect: %w", err)
	}

	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration connection: invalid connection string")
	}
	defer func() {
		if err := database.Close(); err != nil {
			logger.Error("close migration connection", "error", err)
		}
	}()

	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("reach database for migration: %w", err)
	}
	return operation(database)
}

// migrationLogger adapts goose's logger to Convia's structured logger.
type migrationLogger struct {
	logger *slog.Logger
}

func (adapter migrationLogger) Printf(format string, values ...any) {
	adapter.logger.Info("migration", "message", trimNewline(fmt.Sprintf(format, values...)))
}

func (adapter migrationLogger) Fatalf(format string, values ...any) {
	adapter.logger.Error("migration", "message", trimNewline(fmt.Sprintf(format, values...)))
}

func trimNewline(message string) string {
	for len(message) > 0 && (message[len(message)-1] == '\n' || message[len(message)-1] == '\r') {
		message = message[:len(message)-1]
	}
	return message
}
