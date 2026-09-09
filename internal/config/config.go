// Package config loads Convia's runtime configuration.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"convia/internal/media"
)

const (
	defaultHTTPHost = "0.0.0.0"
	defaultHTTPPort = 8080

	defaultDatabaseMaxConnections = 10
	defaultDatabaseConnectTimeout = 5 * time.Second
	defaultDatabaseQueryTimeout   = 5 * time.Second

	maxDatabaseConnections = 500

	defaultMediaTimeout = 5 * time.Second

	/*
		minimumProductionSecretLength is the shortest media API secret
		production accepts.

		The secret is an HMAC key: it signs every token Convia presents to the
		media plane, and later every token a participant connects with. A short
		one is searchable offline by anyone holding a single signed token, and
		nothing about the deployment would look wrong while that happened.
	*/
	minimumProductionSecretLength = 32

	environmentEnvironment = "CONVIA_ENVIRONMENT"

	httpHostEnvironment = "CONVIA_HTTP_HOST"
	httpPortEnvironment = "CONVIA_HTTP_PORT"

	trustedProxiesEnvironment = "CONVIA_TRUSTED_PROXIES"

	databaseURLEnvironment            = "CONVIA_DATABASE_URL"
	databaseMaxConnectionsEnvironment = "CONVIA_DATABASE_MAX_CONNECTIONS"
	databaseConnectTimeoutEnvironment = "CONVIA_DATABASE_CONNECT_TIMEOUT"
	databaseQueryTimeoutEnvironment   = "CONVIA_DATABASE_QUERY_TIMEOUT"

	/*
		The media variables name their provider, unlike every other name here.

		That is deliberate. They configure a LiveKit server specifically, and a
		provider-neutral name would invite an operator to point them at
		something that does not speak its protocol. The rule Convia holds to is
		that no provider concept reaches a public API, a domain type, or an
		SDK; an operator deploying the media plane is the one person who has to
		know what they are deploying.
	*/
	mediaURLEnvironment       = "CONVIA_LIVEKIT_URL"
	mediaAPIKeyEnvironment    = "CONVIA_LIVEKIT_API_KEY"
	mediaAPISecretEnvironment = "CONVIA_LIVEKIT_API_SECRET"
	mediaTimeoutEnvironment   = "CONVIA_LIVEKIT_TIMEOUT"
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
	HTTPHost    string
	HTTPPort    int
	Database    Database
	Media       Media

	/*
		TrustedProxies are the networks whose forwarded headers Convia believes.

		Empty means trust nothing, which is the default and the only safe one:
		a header nobody told Convia to trust would let any caller claim any
		address. See ClientAddress in internal/server.
	*/
	TrustedProxies []netip.Prefix
}

// Database contains the connection and pool settings of the PostgreSQL client.
type Database struct {
	URL            string
	MaxConnections int32
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

/*
Media contains the settings of the media plane, when there is one.

A Convia with no media plane configured is a supported deployment rather than a
broken one: rooms, calls, and participants all work, and nobody can connect.
The zero value is that deployment, and Configured is how the composition root
tells the two apart.

The secret is an APISecret rather than a string so that logging this struct, or
the Config holding it, cannot print it.
*/
type Media struct {
	URL       string
	APIKey    string
	APISecret media.APISecret
	Timeout   time.Duration
}

// Configured reports whether a media plane was configured at all.
func (settings Media) Configured() bool {
	return settings.URL != ""
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

	trustedProxies, err := loadTrustedProxies()
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

	mediaPlane, err := loadMedia(environment)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Environment:    environment,
		HTTPHost:       host,
		HTTPPort:       port,
		Database:       database,
		Media:          mediaPlane,
		TrustedProxies: trustedProxies,
	}, nil
}

/*
loadMedia reads the media plane settings, if any were given.

The rule is all or nothing. Configuring none of the three values means Convia
runs without a media plane, which is a real deployment. Configuring some of
them is never intentional, and it is the case worth failing on: a deployment
that meant to have a media plane and is missing one value would otherwise start
happily and behave exactly like one that meant to have none, until somebody
started a call and could not hear anybody.
*/
func loadMedia(environment Environment) (Media, error) {
	endpoint := strings.TrimSpace(os.Getenv(mediaURLEnvironment))
	key := strings.TrimSpace(os.Getenv(mediaAPIKeyEnvironment))
	secret := strings.TrimSpace(os.Getenv(mediaAPISecretEnvironment))

	settings := []struct {
		name  string
		value string
	}{
		{mediaURLEnvironment, endpoint},
		{mediaAPIKeyEnvironment, key},
		{mediaAPISecretEnvironment, secret},
	}

	var missing []string
	for _, setting := range settings {
		if setting.value == "" {
			missing = append(missing, setting.name)
		}
	}

	if len(missing) == len(settings) {
		return Media{}, nil
	}

	if len(missing) > 0 {
		return Media{}, fmt.Errorf("a media plane is partially configured: %s must also be set",
			strings.Join(missing, ", "))
	}

	if err := validateMediaURL(endpoint, environment); err != nil {
		return Media{}, err
	}

	if environment == Production && len(secret) < minimumProductionSecretLength {
		// The secret is never included: the error is going to a log.
		return Media{}, fmt.Errorf("%s must be at least %d characters in production",
			mediaAPISecretEnvironment, minimumProductionSecretLength)
	}

	timeout, err := loadDuration(mediaTimeoutEnvironment, defaultMediaTimeout)
	if err != nil {
		return Media{}, err
	}

	return Media{
		URL:       endpoint,
		APIKey:    key,
		APISecret: media.APISecret(secret),
		Timeout:   timeout,
	}, nil
}

/*
validateMediaURL rejects a media endpoint Convia should not use.

Production requires TLS for the same reason the database URL does, and with a
sharper edge: every request to the media plane carries a bearer token signed
with the API secret, so a plaintext hop hands that token to anyone on the path.
*/
func validateMediaURL(mediaURL string, environment Environment) error {
	parsed, err := url.Parse(mediaURL)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL", mediaURLEnvironment)
	}

	switch parsed.Scheme {
	case "http":
		if environment == Production {
			return fmt.Errorf("%s must use https in production", mediaURLEnvironment)
		}
	case "https":
	default:
		return fmt.Errorf("%s must use the http or https scheme", mediaURLEnvironment)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", mediaURLEnvironment)
	}

	return nil
}

/*
loadTrustedProxies reads the networks whose forwarded headers Convia believes.

The value is a comma-separated list of CIDR blocks or bare addresses, for
example "10.0.0.0/8, 192.168.1.7". A bare address is treated as a single-host
network, because writing /32 for one load balancer is a detail nobody should
have to remember.

An unparseable entry is a startup failure rather than a skipped entry. Silently
dropping one would leave Convia trusting fewer proxies than the operator
believes, and the symptom — every client sharing one address for rate-limiting
purposes — would surface much later and look like something else entirely.
*/
func loadTrustedProxies() ([]netip.Prefix, error) {
	raw := strings.TrimSpace(environmentOrDefault(trustedProxiesEnvironment, ""))
	if raw == "" {
		return nil, nil
	}

	var prefixes []netip.Prefix
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		prefix, err := parseProxyEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", trustedProxiesEnvironment, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// parseProxyEntry accepts either a CIDR block or a single address.
func parseProxyEntry(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a valid CIDR block", entry)
		}
		/*
			Masking discards any host bits the operator left set, so that
			"10.1.2.3/8" means the network they clearly intended rather than
			silently matching nothing.
		*/
		return prefix.Masked(), nil
	}

	address, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid address or CIDR block", entry)
	}
	return netip.PrefixFrom(address, address.BitLen()), nil
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
