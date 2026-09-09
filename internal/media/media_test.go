package media

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

/*
TestRetryableSeparatesAnOutageFromARefusal is the distinction the two errors
exist for.

Treating every provider failure as retryable turns a misconfiguration into an
infinite retry loop. Treating none as retryable turns a one-second outage into
a failed call. The two answers are different, so the two failures have to be.
*/
func TestRetryableSeparatesAnOutageFromARefusal(t *testing.T) {
	retryable := map[string]error{
		"an outage":         ErrUnavailable,
		"a wrapped outage":  fmt.Errorf("dial the media plane: %w", ErrUnavailable),
		"an outage in vain": fmt.Errorf("open session: %w", fmt.Errorf("timeout: %w", ErrUnavailable)),
	}
	for name, err := range retryable {
		if !Retryable(err) {
			t.Errorf("Retryable(%s) = false, want another attempt to be open", name)
		}
	}

	terminal := map[string]error{
		"a refusal":          ErrRejected,
		"a wrapped refusal":  fmt.Errorf("open session: %w", ErrRejected),
		"an unrelated fault": errors.New("something else entirely"),
	}
	for name, err := range terminal {
		if Retryable(err) {
			t.Errorf("Retryable(%s) = true, want retrying to be pointless", name)
		}
	}

	if Retryable(nil) {
		t.Error("Retryable(nil) = true, want no failure to need retrying")
	}
}

/*
TestTheTwoFailuresAreDistinguishable guards the pair from being collapsed.

If one were defined in terms of the other, the vocabulary would still compile
while quietly losing the only property it exists to carry.
*/
func TestTheTwoFailuresAreDistinguishable(t *testing.T) {
	if errors.Is(ErrRejected, ErrUnavailable) || errors.Is(ErrUnavailable, ErrRejected) {
		t.Error("a refusal and an outage are the same error, so the retry decision is meaningless")
	}
}

/*
TestAnAbsentMediaPlaneRealizesNothingAndSaysSo proves what Convia ships with.

A control plane with no media transport configured is a coherent thing to be:
rooms, calls, and participants all work, and nobody can connect because there
is nothing to connect to. Failing instead would make the control plane depend
on infrastructure it does not have.
*/
func TestAnAbsentMediaPlaneRealizesNothingAndSaysSo(t *testing.T) {
	ctx := context.Background()
	var plane Absent

	session, err := plane.OpenSession(ctx, SessionRequest{CallID: "call_7KQZP4XN2VJH6TBWMDR3YAFC5E"})
	if err != nil {
		t.Fatalf("OpenSession() error = %v, want an absent media plane to succeed", err)
	}
	if session.Realized() {
		t.Errorf("session = %+v, want one that was never realized", session)
	}
	if session.Reference != "" {
		t.Errorf("reference = %q, want nothing to reference", session.Reference)
	}

	if err := plane.CloseSession(ctx, session); err != nil {
		t.Errorf("CloseSession() error = %v, want releasing nothing to succeed", err)
	}
}

/*
TestARealizedSessionIsOneWithSomethingToRelease keeps the emptiness meaningful.

The distinction decides whether Convia stores a reference and whether it later
asks anyone to release one, so it must not depend on reading a struct's field
directly at each call site.
*/
func TestARealizedSessionIsOneWithSomethingToRelease(t *testing.T) {
	if (Session{}).Realized() {
		t.Error("an empty session reports itself as realized")
	}
	if !(Session{Reference: "sess-1"}).Realized() {
		t.Error("a session with a reference reports itself as unrealized")
	}
}
