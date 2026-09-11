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
		defaultRedisTimeout bounds one operation against the shared channel.

		Short, because nothing waits on it: publishing to the other instances is
		queued rather than awaited, so this only decides how quickly a broken
		connection is noticed and retried.
	*/
	defaultRedisTimeout = 3 * time.Second

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

	/*
	   mediaClientURLEnvironment is where a browser reaches the media plane.

	   It is separate from the URL Convia uses because the two are routinely
	   different: a deployment commonly reaches its media server on a private
	   address that no client can resolve. Unset means the two are the same.
	*/
	mediaClientURLEnvironment = "CONVIA_LIVEKIT_CLIENT_URL"

	/*
	   redisURLEnvironment is where this instance reaches the channel it shares
	   with the other instances of the same deployment.

	   Unset means there are no other instances, which is a supported
	   deployment and the one every local process runs. See internal/events.
	*/
	redisURLEnvironment     = "CONVIA_REDIS_URL"
	redisTimeoutEnvironment = "CONVIA_REDIS_TIMEOUT"

	/*
	   firstPartyApplicationEnvironment names the tenant that owns Convia's own
	   product.

	   It is an application identifier rather than a name, because names are
	   not unique and there is no lookup by one. The application is created
	   once by an operator and its identifier is configured here.

	   Unset means Convia serves the platform and not its own interface, which
	   is a supported deployment: the session surface is simply not registered.
	*/
	firstPartyApplicationEnvironment = "CONVIA_FIRST_PARTY_APPLICATION"
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
	Redis       Redis

	/*
		FirstPartyApplication is the tenant that owns Convia's own product, and
		the only one a browser session can ever act within.

		Empty means nobody signs in to Convia itself. That is a deployment
		choice rather than a fault — Convia is a platform first — and the
		session routes are absent rather than refusing.
	*/
	FirstPartyApplication string

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
	ClientURL string
	APIKey    string
	APISecret media.APISecret
	Timeout   time.Duration
}

// Configured reports whether a media plane was configured at all.
func (settings Media) Configured() bool {
	return settings.URL != ""
}

/*
Redis contains the settings of the channel instances share, when there is one.

A Convia with no Redis configured is a supported deployment and the ordinary
one: a single instance needs nothing carried anywhere, and everything works.
What it does not support is *several* instances with this unset, because each
would then serve only the event-stream subscribers connected to it. That is an
operational requirement rather than something Convia can check, and
docs/events.md states it.

Nothing durable is kept here. The only use is publish/subscribe, which stores
nothing at all — which is also what makes it impossible for this to become an
accidental source of truth.
*/
type Redis struct {
	URL     string
	Timeout time.Duration
}

// Configured reports whether instances were given a way to reach each other.
func (settings Redis) Configured() bool {
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

	shared, err := loadRedis(environment)
	if err != nil {
		return Config{}, err
	}

	firstParty, err := loadFirstPartyApplication()
	if err != nil {
		return Config{}, err
	}

	return Config{
		Environment:           environment,
		HTTPHost:              host,
		HTTPPort:              port,
		Database:              database,
		Media:                 mediaPlane,
		Redis:                 shared,
		FirstPartyApplication: firstParty,
		TrustedProxies:        trustedProxies,
	}, nil
}

/*
loadFirstPartyApplication reads which tenant owns Convia's own product.

The shape is checked here and the existence is not: whether the application is
there is a question for the database, and asking it during configuration would
make a process that cannot start when PostgreSQL is briefly unavailable. What
this catches is the mistake somebody actually makes — configuring a name, or a
credential, where an application identifier belongs.
*/
func loadFirstPartyApplication() (string, error) {
	identifier := strings.TrimSpace(os.Getenv(firstPartyApplicationEnvironment))
	if identifier == "" {
		return "", nil
	}

	/*
		The shape is checked here rather than by calling into the applications
		domain, because configuration is a leaf: everything imports it, and
		importing a domain back would be the first edge of a cycle. What it
		costs is one literal describing a format that is already fixed by a
		database constraint and a test.
	*/
	const prefix = "app_"
	random, found := strings.CutPrefix(identifier, prefix)
	if !found || len(random) != 26 {
		return "", fmt.Errorf("%s must be an application identifier beginning with %s",
			firstPartyApplicationEnvironment, prefix)
	}

	for _, character := range random {
		base32 := (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
		if !base32 {
			return "", fmt.Errorf("%s must be an application identifier beginning with %s",
				firstPartyApplicationEnvironment, prefix)
		}
	}
	return identifier, nil
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

	clientURL := strings.TrimSpace(os.Getenv(mediaClientURLEnvironment))
	if err := validateMediaClientURL(clientURL, environment); err != nil {
		return Media{}, err
	}

	return Media{
		URL:       endpoint,
		ClientURL: clientURL,
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

/*
validateMediaClientURL rejects an address clients could not use.

Empty is valid and means the client address is derived from Convia's own by
swapping the scheme, which is right whenever one server answers both. When it
is set it must already be a WebSocket address, because that is what a client
connects with and silently rewriting an operator's http:// into ws:// would
hide the case where they meant a different host entirely.

Production requires wss for the same reason it requires https: the address is
published to clients along with a bearer token, and a plaintext one hands that
token to anyone on the path.
*/
func validateMediaClientURL(clientURL string, environment Environment) error {
	if clientURL == "" {
		return nil
	}

	parsed, err := url.Parse(clientURL)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL", mediaClientURLEnvironment)
	}

	switch parsed.Scheme {
	case "ws":
		if environment == Production {
			return fmt.Errorf("%s must use wss in production", mediaClientURLEnvironment)
		}
	case "wss":
	default:
		return fmt.Errorf("%s must use the ws or wss scheme", mediaClientURLEnvironment)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", mediaClientURLEnvironment)
	}

	return nil
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

/*
loadRedis reads the settings of the channel instances share.

Unset is not a mistake and is not treated as one: a single instance needs
nothing carried anywhere, and that is what a local process and a small
deployment both are. What Convia cannot tell from here is whether *several*
instances are running with it unset, which is the one configuration that is
quietly wrong — docs/events.md states it as an operational requirement, and
the composition root says at startup which of the two this process is.

The URL is validated for shape rather than reachability. Refusing to start
because Redis is down would take a working API offline over a stream that
degrades to what it was before M16.
*/
func loadRedis(environment Environment) (Redis, error) {
	endpoint := strings.TrimSpace(os.Getenv(redisURLEnvironment))
	if endpoint == "" {
		return Redis{}, nil
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Redis{}, fmt.Errorf("%s must be a valid URL", redisURLEnvironment)
	}

	switch parsed.Scheme {
	case "rediss":
	case "redis":
		/*
			Plain redis carries the events of every tenant on this deployment
			between machines. In development that is a container on the same
			host; in production it is a network Convia does not own.
		*/
		if environment == Production {
			return Redis{}, fmt.Errorf("%s must use rediss in production", redisURLEnvironment)
		}
	default:
		return Redis{}, fmt.Errorf("%s must be a redis or rediss URL", redisURLEnvironment)
	}

	if parsed.Host == "" {
		return Redis{}, fmt.Errorf("%s must name a host", redisURLEnvironment)
	}

	timeout, err := loadDuration(redisTimeoutEnvironment, defaultRedisTimeout)
	if err != nil {
		return Redis{}, err
	}

	return Redis{URL: endpoint, Timeout: timeout}, nil
}
