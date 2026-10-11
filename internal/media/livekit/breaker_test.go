package livekit

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"convia/internal/circuit"
	"convia/internal/media"
)

/*
TestAProviderThatFailsIsNotAskedForACooldown is `M25-009`: once the provider
has failed to answer, the calls after it are told it is unavailable at once,
without asking, until the cooldown lets one through to find out.
*/
func TestAProviderThatFailsIsNotAskedForACooldown(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	plane, fake := newProvider(t, func(recorded) (int, string) {
		if down.Load() {
			return http.StatusServiceUnavailable, `{"code":"unavailable","msg":"restarting"}`
		}
		return http.StatusOK, `{"name":"room"}`
	})
	ctx := context.Background()

	if _, err := plane.OpenSession(ctx, media.SessionRequest{CallID: "call_1"}); !media.Retryable(err) {
		t.Fatalf("a provider answering 503 gave %v, want it unavailable", err)
	}
	asked := len(fake.requests())

	for range 50 {
		if _, err := plane.OpenSession(ctx, media.SessionRequest{CallID: "call_1"}); !media.Retryable(err) {
			t.Fatalf("a call inside the cooldown gave %v, want it unavailable", err)
		}
	}
	if len(fake.requests()) != asked {
		t.Fatalf("the provider was asked %d more times inside the cooldown", len(fake.requests())-asked)
	}

	down.Store(false)
	time.Sleep(cooldown)
	if _, err := plane.OpenSession(ctx, media.SessionRequest{CallID: "call_1"}); err != nil {
		t.Fatalf("a provider that came back was not asked again: %v", err)
	}
}

/*
TestARefusalIsAnAnswer keeps a provider that is working from being treated as
one that is not: a room it does not have, or a request it rejects, is the
provider answering.
*/
func TestARefusalIsAnAnswer(t *testing.T) {
	plane, fake := newProvider(t, func(recorded) (int, string) {
		return http.StatusNotFound, `{"code":"not_found","msg":"no such room"}`
	})
	ctx := context.Background()

	for range 3 {
		if err := plane.CloseSession(ctx, media.Session{Reference: "room"}); err != nil {
			t.Fatalf("CloseSession() of a room that is gone = %v", err)
		}
	}
	if asked := len(fake.requests()); asked != 3 {
		t.Errorf("the provider was asked %d times, want every time: a refusal opened the breaker", asked)
	}
	if plane.breaker.Open() {
		t.Error("the breaker is open after answers")
	}
}

/*
TestAProviderThatHangsCostsOneTimeout: a provider that never answers costs the
first call its timeout and the ones after it nothing.
*/
func TestAProviderThatHangsCostsOneTimeout(t *testing.T) {
	release := make(chan struct{})
	plane, _ := newProvider(t, func(recorded) (int, string) {
		<-release
		return http.StatusOK, "{}"
	})
	// After the server's own cleanup is registered, so it runs first: closing
	// the server waits for the handler this releases.
	t.Cleanup(func() { close(release) })
	plane.client.Timeout = 100 * time.Millisecond
	ctx := context.Background()

	if err := plane.Disconnect(ctx, media.Session{Reference: "room"}, "par_1"); !media.Retryable(err) {
		t.Fatalf("a provider that hangs gave %v, want it unavailable", err)
	}
	started := time.Now()
	for range 20 {
		err := plane.Disconnect(ctx, media.Session{Reference: "room"}, "par_1")
		if !media.Retryable(err) || !errors.Is(err, circuit.ErrOpen) {
			t.Fatalf("a call after the hang gave %v, want it not asked", err)
		}
	}
	if waited := time.Since(started); waited > 50*time.Millisecond {
		t.Errorf("twenty calls after a hang took %v; they waited for the provider", waited)
	}
}
