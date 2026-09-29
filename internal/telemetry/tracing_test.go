package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// tracing builds a tracer over a recorder a test can read back.
func tracing(t *testing.T) (*Requests, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	return Trace(provider), recorder
}

// serve runs one request through the tracer as the middleware would.
func serve(requests *Requests, request *http.Request, status int) context.Context {
	ctx, done := requests.Began(request)
	served := request.WithContext(ctx)
	done(served, status)
	return ctx
}

func asking(method, target, pattern string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Pattern = pattern
	return request
}

/*
TestASpanIsNamedForItsRouteRatherThanItsPath.

`M22-003` asks for safe route names, and the reason is the metrics label's: a
span named for the path is a distinct operation per room, which makes every
aggregate across a route empty and every trace search a scan.
*/
func TestASpanIsNamedForItsRouteRatherThanItsPath(t *testing.T) {
	requests, recorder := tracing(t)

	serve(requests, asking(http.MethodGet, "/v1/rooms/rom_ABC", "GET /v1/rooms/{room_id}"), http.StatusOK)
	serve(requests, asking(http.MethodGet, "/v1/rooms/rom_XYZ", "GET /v1/rooms/{room_id}"), http.StatusOK)

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(spans))
	}
	for _, span := range spans {
		if span.Name() != "GET /v1/rooms/{room_id}" {
			t.Errorf("span name = %q, want the route", span.Name())
		}
		if strings.Contains(span.Name(), "rom_") {
			t.Errorf("span name carries an identifier: %q", span.Name())
		}
	}
}

/*
TestTheCallersTraceIsContinuedRatherThanReplaced.

This is `M22-004` in one sentence: an application that traced its own call into
Convia gets one trace across both, and Convia's work appears underneath the
operation that asked for it rather than as an unrelated trace nobody can join.
*/
func TestTheCallersTraceIsContinuedRatherThanReplaced(t *testing.T) {
	requests, recorder := tracing(t)

	const caller = "4bf92f3577b34da6a3ce929d0e0e4736"
	request := asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms")
	request.Header.Set("traceparent", "00-"+caller+"-00f067aa0ba902b7-01")

	serve(requests, request, http.StatusOK)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != caller {
		t.Errorf("trace = %q, want the caller's %q — Convia started a trace of its own", got, caller)
	}
	if !spans[0].Parent().IsValid() {
		t.Error("the span has no parent, so it is not underneath the operation that asked for it")
	}
}

/*
TestOnlyAServerFaultMarksTheSpan.

A 404 or a 401 is Convia answering correctly. Marking those would make the error
rate of every trace search the rate at which people mistype URLs and let sessions
expire.
*/
func TestOnlyAServerFaultMarksTheSpan(t *testing.T) {
	requests, recorder := tracing(t)

	for _, status := range []int{http.StatusOK, http.StatusNotFound,
		http.StatusUnauthorized, http.StatusInternalServerError} {
		serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), status)
	}

	faulted := 0
	for _, span := range recorder.Ended() {
		if span.Status().Code == 1 /* codes.Error */ {
			faulted++
		}
	}
	if faulted != 1 {
		t.Errorf("%d spans were marked as faults, want only the 500", faulted)
	}
}

/*
TestALineWrittenInsideASpanNamesItsTrace.

This is the other half of `M22-008`. The two identifiers are not redundant: the
request identifier is Convia's own and is in the answer a client received, so
somebody holding a failed response can find its lines; the trace identifier is
the caller's and spans every service, so somebody holding a slow trace can find
what Convia was doing inside it.
*/
func TestALineWrittenInsideASpanNamesItsTrace(t *testing.T) {
	requests, _ := tracing(t)

	written := &bytes.Buffer{}
	logger := slog.New(Correlate(slog.NewJSONHandler(written, nil)))

	ctx := serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), http.StatusOK)
	logger.ErrorContext(ctx, "something went wrong")

	fields := line(t, written)
	wanted := trace.SpanContextFromContext(ctx).TraceID().String()
	if got := fields["trace_id"]; got != wanted {
		t.Errorf("trace_id = %v, want %q", got, wanted)
	}
}

/*
TestNothingIsTracedWhenNothingIsConfigured.

Off is the default and has to be a real no-op: spans are started on every
request, so anything they cost is paid by every deployment that never asked for
tracing.
*/
func TestNothingIsTracedWhenNothingIsConfigured(t *testing.T) {
	provider, shutdown, err := Tracing(context.Background(),
		Describing("development", "test"), "")
	if err != nil {
		t.Fatalf("Tracing() with no endpoint error = %v", err)
	}

	requests := Trace(provider)
	ctx := serve(requests, asking(http.MethodGet, "/v1/rooms", "GET /v1/rooms"), http.StatusOK)

	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("an unconfigured tracer produced a recording span")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutting down an unconfigured tracer error = %v", err)
	}
}

/*
TestBaggageIsNotCarried.

Baggage travels to every service a request touches and, with a careless
exporter, into their telemetry — a channel for personal data to leave the
boundary docs/data-protection.md draws, opened by default and closed by nobody.
Convia propagates trace context and nothing else.
*/
func TestBaggageIsNotCarried(t *testing.T) {
	carried := Propagation().Fields()

	for _, field := range carried {
		if strings.EqualFold(field, "baggage") {
			t.Errorf("baggage is propagated: %v", carried)
		}
	}
	if len(carried) == 0 {
		t.Error("nothing is propagated at all, so a caller's trace cannot be continued")
	}
}
