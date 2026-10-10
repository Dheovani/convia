package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"

	"convia/internal/applications"
	"convia/internal/audit"
	"convia/internal/config"
	"convia/internal/credentials"
	"convia/internal/database"
)

// testDatabaseURLEnvironment points these tests at a PostgreSQL instance; unset, they skip.
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// freshDatabase creates a database of the test's own and answers with its address.
func freshDatabase(t *testing.T) string {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the shutdown tests", testDatabaseURLEnvironment)
	}

	name := "convia_test_" + strings.ToLower(rand.Text()[:16])
	maintain(t, maintenanceURL, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		maintain(t, maintenanceURL, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	parsed, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnvironment, err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func maintain(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to the maintenance database: %v", err)
	}
	defer func() { _ = connection.Close(ctx) }()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

// freePort answers with a port nothing is listening on at the moment of asking.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// tenantKey makes an application and a key for it, as an operator would.
func tenantKey(t *testing.T, cfg config.Config, logger *slog.Logger) string {
	t.Helper()

	ctx := context.Background()
	pool, err := database.Open(ctx, cfg.Database, logger, nil)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer pool.Close()

	trail := audit.NewService(audit.NewStore(pool), logger)
	tenants := applications.NewService(applications.NewStore(pool), trail)
	application, err := tenants.Create(ctx, "Shutdown")
	if err != nil {
		t.Fatalf("create an application: %v", err)
	}

	keys := credentials.NewService(credentials.NewStore(pool), tenants, trail)
	credential, secret, err := keys.Issue(ctx, application.ID, credentials.Request{
		Name: "backend",
		Scopes: []credentials.Scope{credentials.ScopeRoomsWrite, credentials.ScopeCallsRead,
			credentials.ScopeCallsWrite, credentials.ScopeEventsRead},
	})
	if err != nil {
		t.Fatalf("issue a key: %v", err)
	}
	return credentials.Token(credential.ID, secret)
}

// running is one Convia serving in this test, and how to stop it.
type running struct {
	address string
	stop    context.CancelFunc
	done    chan error
}

func start(t *testing.T, cfg config.Config, logger *slog.Logger) running {
	t.Helper()

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, logger, cfg) }()

	address := "http://" + cfg.Address()
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, err := http.Get(address + "/health")
		if err == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("Convia did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return running{address: address, stop: stop, done: done}
}

func ask(t *testing.T, key, method, address, body string) map[string]any {
	t.Helper()

	request, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, address, err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, address, err)
	}
	defer response.Body.Close()

	read, _ := io.ReadAll(response.Body)
	if response.StatusCode >= 300 {
		t.Fatalf("%s %s answered %d: %s", method, address, response.StatusCode, read)
	}
	var decoded map[string]any
	if err := json.Unmarshal(read, &decoded); err != nil {
		t.Fatalf("decode %s: %v", read, err)
	}
	return decoded
}

/*
TestStoppingSaysSoAndLosesNothing is `M25-010`: an instance stopped while a call
is running and a subscriber is listening.

What a restart must not do is the whole test. A subscriber is told, with a code
and a reason, rather than finding a dead socket -- the reason is the difference
between "reconnect elsewhere" and "something is broken". The process ends
within its deadline rather than being killed at the end of it. And the call
survives: media flows between a client and the media plane, not through
Convia, so nothing about stopping this process may end a conversation that is
happening, and starting again must find it exactly as it was.
*/
func TestStoppingSaysSoAndLosesNothing(t *testing.T) {
	databaseURL := freshDatabase(t)
	t.Setenv("CONVIA_ENVIRONMENT", "development")
	t.Setenv("CONVIA_HTTP_HOST", "127.0.0.1")
	t.Setenv("CONVIA_HTTP_PORT", strconv.Itoa(freePort(t)))
	t.Setenv("CONVIA_DATABASE_URL", databaseURL)
	t.Setenv("CONVIA_LOG_LEVEL", "error")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(context.Background(), cfg.Database.URL, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	key := tenantKey(t, cfg, logger)

	first := start(t, cfg, logger)
	room := ask(t, key, http.MethodPost, first.address+"/v1/rooms", `{"name":"Standup"}`)
	call := ask(t, key, http.MethodPost, first.address+"/v1/rooms/"+room["id"].(string)+"/calls", `{}`)
	callID := call["id"].(string)

	dialing, cancelDial := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDial()
	stream, _, err := websocket.Dial(dialing, strings.Replace(first.address, "http", "ws", 1)+"/v1/events",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}}})
	if err != nil {
		t.Fatalf("open the event stream: %v", err)
	}
	defer stream.CloseNow()

	stopped := time.Now()
	first.stop()

	reading, cancelRead := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelRead()
	var closed websocket.CloseError
	for {
		if _, _, err := stream.Read(reading); err != nil {
			if !errors.As(err, &closed) {
				t.Fatalf("the stream ended without saying why: %v", err)
			}
			break
		}
	}
	if closed.Code != websocket.StatusGoingAway || !strings.Contains(closed.Reason, "shutting down") {
		t.Errorf("the stream was closed with %d %q, want going away because the instance is shutting down",
			closed.Code, closed.Reason)
	}

	select {
	case err := <-first.done:
		if err != nil {
			t.Errorf("serve() = %v after a requested stop, want nil", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("Convia did not stop within its shutdown deadline")
	}
	if took := time.Since(stopped); took > shutdownTimeout {
		t.Errorf("stopping took %v, longer than the %v deadline", took, shutdownTimeout)
	}

	if _, err := http.Get(first.address + "/health"); err == nil {
		t.Error("a stopped instance still answered")
	}

	second := start(t, cfg, logger)
	defer func() {
		second.stop()
		<-second.done
	}()
	again := ask(t, key, http.MethodGet, second.address+"/v1/calls/"+callID, "")
	if again["status"] != "active" {
		t.Errorf("after a restart the call is %v, want it still active", again["status"])
	}
}
