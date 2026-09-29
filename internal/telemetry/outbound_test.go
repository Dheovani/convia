package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// replying is a transport that records what reached it and replies.
type replying struct {
	received *http.Request
	status   int
}

func (transport *replying) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.received = request

	status := transport.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
		Request:    request,
	}, nil
}

// sending runs one outbound request through a traced transport.
func sending(t *testing.T, operation string, carry bool, status int) (
	*replying, *tracetest.SpanRecorder, *http.Request) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	inner := &replying{status: status}
	client := &http.Client{Transport: Call(inner, provider, operation, carry)}

	original := httptest.NewRequest(http.MethodPost,
		"https://hooks.example.com/tenant/abc?secret=shh", nil)
	original.RequestURI = ""

	response, err := client.Do(original)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	response.Body.Close()

	return inner, recorder, original
}

/*
TestConviasTraceIsCarriedWhereTheDeploymentChoseTheDestination.

This is the outward half of `M22-004`. An application receiving a webhook can
join its own trace to the operation that caused it, which is the point of
propagating at all — and the destination is one a tenant registered, so it is
that application's own data.
*/
func TestConviasTraceIsCarriedWhereTheDeploymentChoseTheDestination(t *testing.T) {
	inner, _, _ := sending(t, "webhook.deliver", true, http.StatusOK)

	if inner.received.Header.Get("traceparent") == "" {
		t.Error("no traceparent reached the destination, so it cannot join its trace")
	}
}

/*
TestConviasTraceIsNotCarriedToAnInstallationAnybodyChose.

A home on another installation is chosen by whoever sent an invitation, which is
anybody who can register. `docs/threat-model.md` says a valid signature proves
who is asking and nothing about whether they should be served; a trace
identifier handed to one would let it correlate several of Convia's requests as
one operation, which is a small channel Convia gets nothing back for.
*/
func TestConviasTraceIsNotCarriedToAnInstallationAnybodyChose(t *testing.T) {
	inner, recorder, _ := sending(t, "peer.request", false, http.StatusOK)

	if got := inner.received.Header.Get("traceparent"); got != "" {
		t.Errorf("traceparent = %q reached a destination anybody could choose", got)
	}

	// It is still recorded here, because recording and carrying are separate.
	if len(recorder.Ended()) != 1 {
		t.Error("the request was not recorded, so not carrying turned off the span too")
	}
}

/*
TestAnOutboundSpanIsNamedForTheOperationRatherThanTheAddress.

A webhook destination is a tenant's choice and a home is anybody's, so a span
named for the address would be a span name per destination — an unbounded set
decided by somebody outside Convia. The query string would be worse: it is
whatever the tenant put in the URL, and a secret in one is not unheard of.
*/
func TestAnOutboundSpanIsNamedForTheOperationRatherThanTheAddress(t *testing.T) {
	_, recorder, _ := sending(t, "webhook.deliver", true, http.StatusOK)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}

	span := spans[0]
	if span.Name() != "webhook.deliver" {
		t.Errorf("span name = %q, want the operation", span.Name())
	}
	for _, held := range span.Attributes() {
		value := held.Value.String()
		if strings.Contains(value, "hooks.example.com") || strings.Contains(value, "shh") {
			t.Errorf("%s carries the destination: %s", held.Key, value)
		}
	}
}

/*
TestAnOutboundFailureMarksTheSpan, unlike an inbound one.

Inbound, a 404 is Convia replying correctly. Outbound, it is the thing Convia
asked for not happening — a webhook destination that returns one is a delivery
that failed, and that is what somebody is looking for.
*/
func TestAnOutboundFailureMarksTheSpan(t *testing.T) {
	for status, wanted := range map[int]bool{
		http.StatusOK:                  false,
		http.StatusNotFound:            true,
		http.StatusInternalServerError: true,
	} {
		_, recorder, _ := sending(t, "webhook.deliver", true, status)

		spans := recorder.Ended()
		if len(spans) != 1 {
			t.Fatalf("%d: recorded %d spans", status, len(spans))
		}
		if marked := spans[0].Status().Code == 1; /* codes.Error */ marked != wanted {
			t.Errorf("a %d was marked as a fault = %v, want %v", status, marked, wanted)
		}
	}
}

/*
TestTheRequestGivenIsNotTheOneChanged.

A round tripper is not allowed to modify the request it was handed. Convia's
webhook delivery retries, and a retry that reused a header from the attempt
before would report the older trace for the newer attempt.
*/
func TestTheRequestGivenIsNotTheOneChanged(t *testing.T) {
	_, _, original := sending(t, "webhook.deliver", true, http.StatusOK)

	if got := original.Header.Get("traceparent"); got != "" {
		t.Errorf("the original request was modified: traceparent = %q", got)
	}
}
