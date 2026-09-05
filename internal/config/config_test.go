package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

const testDatabaseURL = "postgres://convia:convia@localhost:5432/convia"

/*
useDefaults clears every Convia variable and sets only what Load requires.

Each test then overrides the single variable it exercises, so a value left in
the developer's environment cannot change the result.
*/
func useDefaults(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		environmentEnvironment,
		httpHostEnvironment,
		httpPortEnvironment,
		databaseMaxConnectionsEnvironment,
		databaseConnectTimeoutEnvironment,
		databaseQueryTimeoutEnvironment,
	} {
		unsetEnvironment(t, name)
	}
	t.Setenv(databaseURLEnvironment, testDatabaseURL)
}

func TestLoadDefaults(t *testing.T) {
	useDefaults(t)

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.Environment != Development {
		t.Errorf("Environment = %q, want %q", config.Environment, Development)
	}
	if config.HTTPHost != defaultHTTPHost {
		t.Errorf("HTTPHost = %q, want %q", config.HTTPHost, defaultHTTPHost)
	}
	if config.HTTPPort != defaultHTTPPort {
		t.Errorf("HTTPPort = %d, want %d", config.HTTPPort, defaultHTTPPort)
	}
	if config.Address() != "0.0.0.0:8080" {
		t.Errorf("Address() = %q, want %q", config.Address(), "0.0.0.0:8080")
	}
	if config.Database.URL != testDatabaseURL {
		t.Errorf("Database.URL = %q, want %q", config.Database.URL, testDatabaseURL)
	}
	if config.Database.MaxConnections != defaultDatabaseMaxConnections {
		t.Errorf("Database.MaxConnections = %d, want %d", config.Database.MaxConnections, defaultDatabaseMaxConnections)
	}
	if config.Database.ConnectTimeout != defaultDatabaseConnectTimeout {
		t.Errorf("Database.ConnectTimeout = %s, want %s", config.Database.ConnectTimeout, defaultDatabaseConnectTimeout)
	}
	if config.Database.QueryTimeout != defaultDatabaseQueryTimeout {
		t.Errorf("Database.QueryTimeout = %s, want %s", config.Database.QueryTimeout, defaultDatabaseQueryTimeout)
	}
}

func TestLoadEnvironment(t *testing.T) {
	useDefaults(t)
	t.Setenv(httpHostEnvironment, "127.0.0.1")
	t.Setenv(httpPortEnvironment, "9090")
	t.Setenv(databaseMaxConnectionsEnvironment, "25")
	t.Setenv(databaseConnectTimeoutEnvironment, "2s")
	t.Setenv(databaseQueryTimeoutEnvironment, "750ms")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.Address() != "127.0.0.1:9090" {
		t.Errorf("Address() = %q, want %q", config.Address(), "127.0.0.1:9090")
	}
	if config.Database.MaxConnections != 25 {
		t.Errorf("Database.MaxConnections = %d, want %d", config.Database.MaxConnections, 25)
	}
	if config.Database.ConnectTimeout != 2*time.Second {
		t.Errorf("Database.ConnectTimeout = %s, want %s", config.Database.ConnectTimeout, 2*time.Second)
	}
	if config.Database.QueryTimeout != 750*time.Millisecond {
		t.Errorf("Database.QueryTimeout = %s, want %s", config.Database.QueryTimeout, 750*time.Millisecond)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	for _, value := range []string{"not-a-port", "0", "65536"} {
		t.Run(value, func(t *testing.T) {
			useDefaults(t)
			t.Setenv(httpPortEnvironment, value)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want an invalid port error")
			}
		})
	}
}

func TestLoadRejectsEmptyHost(t *testing.T) {
	useDefaults(t)
	t.Setenv(httpHostEnvironment, "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an empty host error")
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	useDefaults(t)
	t.Setenv(environmentEnvironment, "staging")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an unknown environment error")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	useDefaults(t)
	unsetEnvironment(t, databaseURLEnvironment)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want a missing database URL error")
	}
}

func TestLoadRejectsInvalidDatabaseURL(t *testing.T) {
	tests := map[string]string{
		"empty":           "",
		"wrong scheme":    "mysql://convia:convia@localhost:3306/convia",
		"no host":         "postgres:///convia",
		"no database":     "postgres://convia:convia@localhost:5432",
		"trailing slash":  "postgres://convia:convia@localhost:5432/",
		"not parseable":   "postgres://convia:convia@localhost:5432/convia\n",
		"missing address": "postgres://",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			useDefaults(t)
			t.Setenv(databaseURLEnvironment, value)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want a database URL error")
			}
		})
	}
}

/*
TestLoadRequiresVerifiedTLSInProduction proves that production refuses a
connection string PostgreSQL would otherwise downgrade to plaintext.
*/
func TestLoadRequiresVerifiedTLSInProduction(t *testing.T) {
	rejected := []string{
		"postgres://convia:convia@db:5432/convia",
		"postgres://convia:convia@db:5432/convia?sslmode=disable",
		"postgres://convia:convia@db:5432/convia?sslmode=prefer",
		"postgres://convia:convia@db:5432/convia?sslmode=allow",
	}

	for _, value := range rejected {
		t.Run(value, func(t *testing.T) {
			useDefaults(t)
			t.Setenv(environmentEnvironment, string(Production))
			t.Setenv(databaseURLEnvironment, value)

			if _, err := Load(); err == nil {
				t.Error("Load() error = nil, want a TLS requirement error")
			}
		})
	}

	accepted := []string{
		"postgres://convia:convia@db:5432/convia?sslmode=require",
		"postgres://convia:convia@db:5432/convia?sslmode=verify-ca",
		"postgres://convia:convia@db:5432/convia?sslmode=verify-full",
	}

	for _, value := range accepted {
		t.Run(value, func(t *testing.T) {
			useDefaults(t)
			t.Setenv(environmentEnvironment, string(Production))
			t.Setenv(databaseURLEnvironment, value)

			if _, err := Load(); err != nil {
				t.Errorf("Load() error = %v, want the URL to be accepted", err)
			}
		})
	}
}

// TestLoadAllowsPlaintextDatabaseInDevelopment keeps local development usable.
func TestLoadAllowsPlaintextDatabaseInDevelopment(t *testing.T) {
	useDefaults(t)
	t.Setenv(databaseURLEnvironment, "postgres://convia:convia@localhost:5432/convia?sslmode=disable")

	if _, err := Load(); err != nil {
		t.Errorf("Load() error = %v, want the URL to be accepted", err)
	}
}

func TestLoadRejectsInvalidPoolSettings(t *testing.T) {
	tests := map[string]struct {
		name  string
		value string
	}{
		"zero connections":    {databaseMaxConnectionsEnvironment, "0"},
		"negative connection": {databaseMaxConnectionsEnvironment, "-1"},
		"too many":            {databaseMaxConnectionsEnvironment, "501"},
		"connections text":    {databaseMaxConnectionsEnvironment, "many"},
		"connect timeout":     {databaseConnectTimeoutEnvironment, "soon"},
		"zero timeout":        {databaseConnectTimeoutEnvironment, "0s"},
		"negative timeout":    {databaseQueryTimeoutEnvironment, "-1s"},
		"query timeout":       {databaseQueryTimeoutEnvironment, "5"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			useDefaults(t)
			t.Setenv(test.name, test.value)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want a %s error", test.name)
			}
		})
	}
}

// TestLoadNeverReportsCredentials keeps the password out of startup failures.
func TestLoadNeverReportsCredentials(t *testing.T) {
	useDefaults(t)
	t.Setenv(environmentEnvironment, string(Production))
	t.Setenv(databaseURLEnvironment, "postgres://convia:super-secret@db:5432/convia?sslmode=disable")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want a TLS requirement error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("Load() error = %q, want it to omit the password", err)
	}
}

func unsetEnvironment(t *testing.T, name string) {
	t.Helper()

	value, exists := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		var err error
		if exists {
			err = os.Setenv(name, value)
		} else {
			err = os.Unsetenv(name)
		}
		if err != nil {
			t.Errorf("restore %s: %v", name, err)
		}
	})
}
