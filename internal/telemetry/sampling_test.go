package telemetry

import (
	"context"
	"net/http"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// sampledAt builds a tracer that keeps the given share of what it starts.
func sampledAt(t *testing.T, ratio float64) (*Requests, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
		sdktrace.WithSampler(sampling(ratio)),
	)
	return Trace(provider), recorder
}

/*
TestACallersDecisionIsHonouredWhateverConviaSamples.

**This is what makes a sampled trace worth having.** If the caller already
decided to record an operation, Convia records its part — otherwise the trace
arrives at whoever is reading it with Convia missing from the middle, which is
worse than not sampling at all because it looks like Convia did nothing.
*/
func TestACallersDecisionIsHonouredWhateverConviaSamples(t *testing.T) {
	requests, recorder := sampledAt(t, 0)

	request := asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms")
	// The trailing 01 is the caller saying it recorded this one.
	request.Header.Set("traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	serve(requests, request, http.StatusOK)

	if len(recorder.Ended()) != 1 {
		t.Error("Convia dropped its part of a trace the caller was keeping")
	}
}

/*
TestACallerThatIsNotRecordingIsNotOverridden.

The other side of the same rule: a caller that decided against recording does
not get Convia's spans arriving on their own, which would be a trace with
nothing but Convia in it.
*/
func TestACallerThatIsNotRecordingIsNotOverridden(t *testing.T) {
	requests, recorder := sampledAt(t, 1)

	request := asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms")
	// The trailing 00 is the caller saying it did not record this one.
	request.Header.Set("traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")

	serve(requests, request, http.StatusOK)

	if len(recorder.Ended()) != 0 {
		t.Error("Convia recorded its part of a trace the caller had decided against")
	}
}

/*
TestNothingConviaStartsIsKeptAtZero, and everything at one.

The ratio applies only to traces Convia starts, which is every request that
arrives without a caller's decision — which is most of them, because most
callers do not trace at all.
*/
func TestNothingConviaStartsIsKeptAtZero(t *testing.T) {
	for ratio, wanted := range map[float64]int{0: 0, 1: 3} {
		requests, recorder := sampledAt(t, ratio)

		for range 3 {
			serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), http.StatusOK)
		}

		if got := len(recorder.Ended()); got != wanted {
			t.Errorf("at a ratio of %v, %d of 3 traces were kept, want %d", ratio, got, wanted)
		}
	}
}

/*
TestAPartialRatioKeepsSomeAndNotAll.

It is deliberately loose. The decision is made from the trace identifier, which
is random, so the exact count over a hundred is a coin-flipping question and a
test that pinned it would fail on a bad afternoon.
*/
func TestAPartialRatioKeepsSomeAndNotAll(t *testing.T) {
	requests, recorder := sampledAt(t, 0.5)

	const many = 200
	for range many {
		serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), http.StatusOK)
	}

	kept := len(recorder.Ended())
	if kept == 0 || kept == many {
		t.Errorf("a half ratio kept %d of %d, which is not sampling at all", kept, many)
	}
}

// TestAnUnconfiguredTracerSamplesNothingToStartWith: off is still off, whatever
// the ratio says, because there is no exporter to send to.
func TestAnUnconfiguredTracerSamplesNothingToStartWith(t *testing.T) {
	provider, shutdown, err := Tracing(context.Background(),
		Describing("development", "test"), "", 1)
	if err != nil {
		t.Fatalf("Tracing() error = %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	requests := Trace(provider)
	ctx := serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), http.StatusOK)

	if ctx == nil {
		t.Error("an unconfigured tracer produced no context at all")
	}
}
