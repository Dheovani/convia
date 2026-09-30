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
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/config"
)

/*
Open builds the PostgreSQL connection pool and verifies it is usable.

A pool that cannot reach PostgreSQL is an explicit startup failure rather than
a process that starts and fails later on its first query. The connection URL is
never logged or wrapped into an error, because it carries the password.
*/
func Open(ctx context.Context, settings config.Database, logger *slog.Logger,
	tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(settings.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: invalid connection string")
	}

	poolConfig.MaxConns = settings.MaxConnections
	poolConfig.ConnConfig.ConnectTimeout = settings.ConnectTimeout

	/*
		The query timeout, which was configured, validated, documented and not
		applied — a failure-injection test found a query outlasting it by three
		times and finishing.

		It is PostgreSQL's own `statement_timeout` rather than a context at
		each call site, and that is the better half of the fix: a context
		abandons the caller and leaves the server working, while this ends the
		work as well as the wait. A query that hangs otherwise holds a
		connection and a goroutine until something else gives up, which is how
		one slow dependency becomes an installation that is up and answering
		nothing.

		Migrations do not come through here. They open their own connection, so
		a schema change that takes longer than a request may is not cut off by
		a number meant for requests.
	*/
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] =
		strconv.FormatInt(settings.QueryTimeout.Milliseconds(), 10)

	/*
		The query tracer, when there is one. It is a parameter rather than a
		setting read here because whether Convia traces is a decision the
		composition root makes once, and this package should not have to know
		how that decision is expressed.

		Nil is the common case in tests, and pgx treats it as no tracing at
		all rather than as a tracer that does nothing per query.
	*/
	poolConfig.ConnConfig.Tracer = tracer

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
