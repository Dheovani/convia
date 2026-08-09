package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	unsetEnvironment(t, httpHostEnvironment)
	unsetEnvironment(t, httpPortEnvironment)

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv(httpHostEnvironment, "127.0.0.1")
	t.Setenv(httpPortEnvironment, "9090")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.Address() != "127.0.0.1:9090" {
		t.Errorf("Address() = %q, want %q", config.Address(), "127.0.0.1:9090")
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	unsetEnvironment(t, httpHostEnvironment)

	for _, value := range []string{"not-a-port", "0", "65536"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(httpPortEnvironment, value)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want an invalid port error")
			}
		})
	}
}

func TestLoadRejectsEmptyHost(t *testing.T) {
	t.Setenv(httpHostEnvironment, "")
	unsetEnvironment(t, httpPortEnvironment)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an empty host error")
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
