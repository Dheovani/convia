// Package config loads Convia's runtime configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPHost = "0.0.0.0"
	defaultHTTPPort = 8080

	defaultDatabaseMaxConnections = 10
	defaultDatabaseConnectTimeout = 5 * time.Second
	defaultDatabaseQueryTimeout   = 5 * time.Second

	maxDatabaseConnections = 500

	environmentEnvironment = "CONVIA_ENVIRONMENT"
	adminAPIEnvironment    = "CONVIA_ADMIN_API"

	adminAPIEnabled  = "enabled"
	adminAPIDisabled = "disabled"

	httpHostEnvironment = "CONVIA_HTTP_HOST"
	httpPortEnvironment = "CONVIA_HTTP_PORT"

	databaseURLEnvironment            = "CONVIA_DATABASE_URL"
	databaseMaxConnectionsEnvironment = "CONVIA_DATABASE_MAX_CONNECTIONS"
	databaseConnectTimeoutEnvironment = "CONVIA_DATABASE_CONNECT_TIMEOUT"
	databaseQueryTimeoutEnvironment   = "CONVIA_DATABASE_QUERY_TIMEOUT"
)

/*
Environment names the deployment context of a Convia process.

It decides how strictly configuration is validated. Development trades safety
for convenience; production refuses anything that would be unsafe to operate.
*/
type Environment string

const (
	Development Environment = "development"
	Production  Environment = "production"
)

// Config contains process-level service configuration.
type Config struct {
	Environment Environment
	AdminAPI    bool
	HTTPHost    string
	HTTPPort    int
	Database    Database
}

// Database contains the connection and pool settings of the PostgreSQL client.
type Database struct {
	URL            string
	MaxConnections int32
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

/*
Load reads configuration from the process environment.

Development defaults keep a local process runnable with a single environment
variable. Values that cannot be defaulted safely, such as the database URL,
are mandatory in every environment.
*/
func Load() (Config, error) {
	environment, err := loadEnvironment()
	if err != nil {
		return Config{}, err
	}

	adminAPI, err := loadAdminAPI(environment)
	if err != nil {
		return Config{}, err
	}

	host := environmentOrDefault(httpHostEnvironment, defaultHTTPHost)
	if host == "" {
		return Config{}, fmt.Errorf("%s must not be empty", httpHostEnvironment)
	}

	portValue := environmentOrDefault(httpPortEnvironment, strconv.Itoa(defaultHTTPPort))
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("%s must be an integer between 1 and 65535", httpPortEnvironment)
	}

	database, err := loadDatabase(environment)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Environment: environment,
		AdminAPI:    adminAPI,
		HTTPHost:    host,
		HTTPPort:    port,
		Database:    database,
	}, nil
}

/*
loadAdminAPI decides whether the administrative endpoints are served.

Convia has no authentication yet, so the administrative API would let anyone
who reaches the port create and read tenants. It is therefore disabled by
default and refused outright in production until M07 introduces credentials.
Enabling it is an explicit, local decision made to bootstrap the first
application.
*/
func loadAdminAPI(environment Environment) (bool, error) {
	switch environmentOrDefault(adminAPIEnvironment, adminAPIDisabled) {
	case adminAPIDisabled:
		return false, nil
	case adminAPIEnabled:
		if environment == Production {
			return false, fmt.Errorf(
				"%s must not be %q in production while the administrative API is unauthenticated",
				adminAPIEnvironment, adminAPIEnabled)
		}
		return true, nil
	default:
		return false, fmt.Errorf("%s must be %q or %q", adminAPIEnvironment, adminAPIEnabled, adminAPIDisabled)
	}
}

// Address returns the configured host and port as a network address.
func (config Config) Address() string {
	return net.JoinHostPort(config.HTTPHost, strconv.Itoa(config.HTTPPort))
}

func loadEnvironment() (Environment, error) {
	value := Environment(environmentOrDefault(environmentEnvironment, string(Development)))

	switch value {
	case Development, Production:
		return value, nil
	default:
		return "", fmt.Errorf("%s must be %q or %q", environmentEnvironment, Development, Production)
	}
}

func loadDatabase(environment Environment) (Database, error) {
	databaseURL := os.Getenv(databaseURLEnvironment)
	if databaseURL == "" {
		return Database{}, fmt.Errorf("%s must be set", databaseURLEnvironment)
	}
	if err := validateDatabaseURL(databaseURL, environment); err != nil {
		return Database{}, err
	}

	maxConnections, err := loadInt(databaseMaxConnectionsEnvironment, defaultDatabaseMaxConnections, 1, maxDatabaseConnections)
	if err != nil {
		return Database{}, err
	}

	connectTimeout, err := loadDuration(databaseConnectTimeoutEnvironment, defaultDatabaseConnectTimeout)
	if err != nil {
		return Database{}, err
	}

	queryTimeout, err := loadDuration(databaseQueryTimeoutEnvironment, defaultDatabaseQueryTimeout)
	if err != nil {
		return Database{}, err
	}

	return Database{
		URL:            databaseURL,
		MaxConnections: int32(maxConnections),
		ConnectTimeout: connectTimeout,
		QueryTimeout:   queryTimeout,
	}, nil
}

/*
validateDatabaseURL rejects connection strings that Convia cannot operate.

Production additionally requires a TLS mode that verifies the connection,
because the default PostgreSQL negotiation silently accepts plaintext. The URL
itself is never included in an error, since it carries the password.
*/
func validateDatabaseURL(databaseURL string, environment Environment) error {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL", databaseURLEnvironment)
	}

	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("%s must use the postgres or postgresql scheme", databaseURLEnvironment)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", databaseURLEnvironment)
	}

	if strings.Trim(parsed.Path, "/") == "" {
		return fmt.Errorf("%s must include a database name", databaseURLEnvironment)
	}

	if environment != Production {
		return nil
	}

	switch parsed.Query().Get("sslmode") {
	case "require", "verify-ca", "verify-full":
		return nil
	default:
		return fmt.Errorf("%s must set sslmode to require, verify-ca, or verify-full in production", databaseURLEnvironment)
	}
}

func loadInt(name string, fallback, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(environmentOrDefault(name, strconv.Itoa(fallback)))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func loadDuration(name string, fallback time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(environmentOrDefault(name, fallback.String()))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 5s", name)
	}
	return value, nil
}

func environmentOrDefault(name, fallback string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		return fallback
	}
	return value
}
