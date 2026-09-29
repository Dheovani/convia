package telemetry

import (
	"net/http"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

/*
Calling wraps an HTTP client so that what Convia sends is in the picture too.

This is the outbound half of `M22-005`, and it is a round tripper rather than
work at each call site for the reason pgx's tracer is: it sees every request the
client makes, including the ones somebody adds next year.

**Whether the trace is carried is a separate decision from whether it is
recorded**, and the two are separate parameters. Recording is always safe: the
span stays here. Carrying puts Convia's trace identifier in a header somebody
else receives, which is a decision about who that somebody is.
*/
type Calling struct {
	inner      http.RoundTripper
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator

	/*
		operation names the span, and is fixed by whoever built this rather
		than taken from the URL.

		A webhook destination is a tenant's choice and a home is chosen by
		anybody who can send an invitation, so a span named for the address
		would be a span name per destination — an unbounded set, decided by
		somebody outside. The operation is one of Convia's own words.
	*/
	operation string

	// carry says whether to put Convia's trace on the wire. See [Call].
	carry bool
}

/*
Call wraps a transport.

`carry` is the question worth pausing on. **Say yes where the destination was
chosen by the deployment or by a tenant it admitted** — an application receiving
a webhook can then join its own trace to the request that caused it, which is
the whole point of propagating outward, and it is that application's own data.

**Say no where the destination is chosen by anybody who can register.** A home
on another installation is exactly that: `docs/threat-model.md` says a valid
signature proves who is asking and nothing about whether they should be served,
and a trace identifier handed to one would let it correlate several of Convia's
requests as one operation. That is a small channel and it is one Convia gets
nothing back for.

An inner transport of nil means the default one, which is what an http.Client
with no Transport already uses.
*/
func Call(inner http.RoundTripper, provider trace.TracerProvider, operation string, carry bool) *Calling {
	if inner == nil {
		inner = http.DefaultTransport
	}

	return &Calling{
		inner:      inner,
		tracer:     provider.Tracer(Name),
		propagator: Propagation(),
		operation:  operation,
		carry:      carry,
	}
}

/*
RoundTrip records one outbound request.

The request is cloned before a header is added, because a round tripper is not
allowed to modify the one it was given — a retry would otherwise send a header
from the attempt before it.
*/
func (calling *Calling) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, span := calling.tracer.Start(request.Context(), calling.operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.HTTPRequestMethodKey.String(methodOf(request))))
	defer span.End()

	sent := request.Clone(ctx)
	if calling.carry {
		calling.propagator.Inject(ctx, propagation.HeaderCarrier(sent.Header))
	}

	response, err := calling.inner.RoundTrip(sent)
	if err != nil {
		span.SetStatus(codes.Error, "the request could not be made")
		return response, err
	}

	span.SetAttributes(semconv.HTTPResponseStatusCode(response.StatusCode))

	/*
		Any answer at or above 400 marks this one, unlike an inbound span.
		Inbound, a 404 is Convia answering correctly; outbound, it is the thing
		Convia asked not happening — a webhook destination that returns one is
		a delivery that failed, and that is what somebody is looking for.
	*/
	if response.StatusCode >= http.StatusBadRequest {
		span.SetStatus(codes.Error, http.StatusText(response.StatusCode))
	}
	return response, nil
}
