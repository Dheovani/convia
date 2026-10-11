package redis

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"convia/internal/presence"
)

/*
stalling stands between a client and the test Redis and can stop passing
anything along, connections held open, and start again: a paused primary, or a
partition that heals.
*/
type stalling struct {
	mutex   sync.Mutex
	resumed chan struct{} // closed while traffic flows
}

func (gate *stalling) hang() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	gate.resumed = make(chan struct{})
}

func (gate *stalling) resume() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	close(gate.resumed)
}

func (gate *stalling) wait() {
	gate.mutex.Lock()
	resumed := gate.resumed
	gate.mutex.Unlock()
	<-resumed
}

// pass copies from one side to the other, holding each chunk while the gate is shut.
func (gate *stalling) pass(to io.Writer, from io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		read, err := from.Read(buffer)
		if read > 0 {
			gate.wait()
			if _, err := to.Write(buffer[:read]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// stalled starts a stalling proxy to the test Redis and answers its URL.
func stalled(t *testing.T) (string, *stalling) {
	t.Helper()
	address := strings.TrimSpace(os.Getenv(testRedisURLEnvironment))
	if address == "" {
		t.Skipf("set %s to run the presence store integration tests", testRedisURLEnvironment)
	}
	upstream, err := url.Parse(address)
	if err != nil {
		t.Fatalf("parse %s: %v", testRedisURLEnvironment, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	gate := &stalling{resumed: make(chan struct{})}
	close(gate.resumed)
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", upstream.Host)
			if err != nil {
				client.Close()
				continue
			}
			go func() { defer server.Close(); gate.pass(server, client) }()
			go func() { defer client.Close(); gate.pass(client, server) }()
		}
	}()
	t.Cleanup(func() {
		defer func() { _ = recover() }() // already resumed
		gate.resume()
	})

	proxied := *upstream
	proxied.Host = listener.Addr().String()
	return proxied.String(), gate
}

/*
TestPresenceComesBackWithRedis is the other half of `M25-008`: a store that
stopped asking a hanging Redis must ask again once it answers. The first
version of the breaker never did -- the one caller let through had to open a
connection, and the breaker refused the connection's own handshake -- so
presence stayed unavailable for as long as the process ran.
*/
func TestPresenceComesBackWithRedis(t *testing.T) {
	address, gate := stalled(t)
	store, err := New(Config{URL: address, Timeout: 300 * time.Millisecond}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	application := tenant(t)
	heartbeat := says("desktop", presence.StateOnline, time.Minute)

	if _, err := store.Assert(ctx, application, "usr_1", heartbeat); err != nil {
		t.Fatalf("Assert() before the outage error = %v", err)
	}

	gate.hang()
	if _, err := store.Assert(ctx, application, "usr_1", heartbeat); err == nil {
		t.Fatal("a heartbeat succeeded against a Redis that does not answer")
	}
	if _, err := store.Assert(ctx, application, "usr_1", heartbeat); !errors.Is(err, presence.ErrResting) {
		t.Fatalf("the heartbeat after a failure was %v, want it not asked", err)
	}

	gate.resume()
	deadline := time.Now().Add(5 * cooldown)
	for {
		_, err := store.Assert(ctx, application, "usr_1", heartbeat)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence was still failing %v after Redis came back: %v", 5*cooldown, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
