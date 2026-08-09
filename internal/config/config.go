// Package config loads Convia's runtime configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const (
	defaultHTTPHost = "0.0.0.0"
	defaultHTTPPort = 8080

	httpHostEnvironment = "CONVIA_HTTP_HOST"
	httpPortEnvironment = "CONVIA_HTTP_PORT"
)

// Config contains process-level service configuration.
type Config struct {
	HTTPHost string
	HTTPPort int
}

// Load reads configuration from the process environment and applies development defaults.
func Load() (Config, error) {
	host := environmentOrDefault(httpHostEnvironment, defaultHTTPHost)
	if host == "" {
		return Config{}, fmt.Errorf("%s must not be empty", httpHostEnvironment)
	}

	portValue := environmentOrDefault(httpPortEnvironment, strconv.Itoa(defaultHTTPPort))
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("%s must be an integer between 1 and 65535", httpPortEnvironment)
	}

	return Config{
		HTTPHost: host,
		HTTPPort: port,
	}, nil
}

// Address returns the configured host and port as a network address.
func (config Config) Address() string {
	return net.JoinHostPort(config.HTTPHost, strconv.Itoa(config.HTTPPort))
}

func environmentOrDefault(name, fallback string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		return fallback
	}
	return value
}
