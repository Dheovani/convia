/*
Package database owns Convia's PostgreSQL connectivity and schema migrations.

It builds the connection pool the service uses at runtime and applies the
embedded migrations that define the schema. No domain behavior lives here:
packages that own a resource build their queries on top of the pool.
*/
package database

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/config"
)

/*
Open builds the PostgreSQL connection pool and verifies it is usable.

A pool that cannot reach PostgreSQL is an explicit startup failure rather than
a process that starts and fails later on its first query. The connection URL is
never logged or wrapped into an error, because it carries the password.
*/
func Open(ctx context.Context, settings config.Database, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(settings.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: invalid connection string")
	}

	poolConfig.MaxConns = settings.MaxConnections
	poolConfig.ConnConfig.ConnectTimeout = settings.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	pingContext, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingContext); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reach database at %s: %w", address(poolConfig), err)
	}

	logger.Info("database connected",
		"address", address(poolConfig),
		"database", poolConfig.ConnConfig.Database,
		"max_connections", poolConfig.MaxConns,
	)

	return pool, nil
}

// address returns the host and port of a pool configuration, without credentials.
func address(poolConfig *pgxpool.Config) string {
	return fmt.Sprintf("%s:%d", poolConfig.ConnConfig.Host, poolConfig.ConnConfig.Port)
}
