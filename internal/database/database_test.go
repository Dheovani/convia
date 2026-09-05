package database

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"convia/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpenRejectsInvalidConnectionString(t *testing.T) {
	settings := config.Database{
		URL:            "postgres://convia:secret@localhost:99999/convia",
		MaxConnections: 1,
		ConnectTimeout: time.Second,
		QueryTimeout:   time.Second,
	}

	_, err := Open(context.Background(), settings, discardLogger())
	if err == nil {
		t.Fatal("Open() error = nil, want an invalid connection string error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("Open() error = %q, want it to omit the password", err)
	}
}

/*
TestOpenFailsWhenDatabaseIsUnreachable proves that an unusable database is a
startup failure rather than a process that starts and fails on its first query.
*/
func TestOpenFailsWhenDatabaseIsUnreachable(t *testing.T) {
	settings := config.Database{
		URL:            "postgres://convia:secret@127.0.0.1:1/convia",
		MaxConnections: 1,
		ConnectTimeout: 2 * time.Second,
		QueryTimeout:   time.Second,
	}

	_, err := Open(context.Background(), settings, discardLogger())
	if err == nil {
		t.Fatal("Open() error = nil, want an unreachable database error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("Open() error = %q, want it to name the unreachable address", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("Open() error = %q, want it to omit the password", err)
	}
}
