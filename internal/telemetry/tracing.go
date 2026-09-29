package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

/*
Tracing builds the tracer provider, which is a no-op unless one is configured.

Off is the default and a real no-op, like [Measuring]: with no endpoint there is
no batching goroutine, no sampling and nothing accumulated, so a laptop and a
test run pay nothing. Spans are started unconditionally everywhere else because
of it.

Shutdown is returned rather than hidden, and matters more here than for metrics:
spans are batched, so a process that exits without flushing loses the trace of
whatever it was doing when it was asked to stop — which is the trace somebody
wanted.
*/
func Tracing(
	ctx context.Context,
	service Service,
	endpoint string,
	sample float64,
) (trace.TracerProvider, func(context.Context) error, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("build the trace exporter: %w", err)
	}

	described := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service.Name),
		semconv.ServiceVersion(service.Version),
		semconv.ServiceInstanceID(service.Instance),
		semconv.DeploymentEnvironment(service.Environment),
	)

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(described),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampling(sample)),
	)
	return provider, provider.Shutdown, nil
}

/*
sampling decides which traces are kept.

**It is parent-based, and that is what makes a trace worth having.** If the
caller already decided to record this operation, Convia records its part —
otherwise a sampled trace would arrive at whoever is reading it with Convia
missing from the middle, which is worse than not sampling at all because it
looks like Convia did nothing.

The consequence is worth stating: **a caller that samples everything makes
Convia trace everything it asks for.** That is a lever somebody outside holds,
and it is bounded by the one that already bounds them — `M23-013`'s per-tenant
request budget caps how much they can ask for in the first place.

A ratio of one is every trace, which is right while an installation is small
and is the default. Zero keeps only what a caller asked to be kept.
*/
func sampling(ratio float64) sdktrace.Sampler {
	switch {
	case ratio >= 1:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case ratio <= 0:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	}
}

/*
Propagation is how a trace crosses a process boundary.

W3C `traceparent`, and **nothing else**. Convia accepts what the standard
defines and does not also read the older vendor headers, because a second format
is a second thing that can disagree about which trace a request belongs to.

Baggage is deliberately absent. It is a way for one service to attach arbitrary
key-value pairs that travel to every other service and, with a careless
exporter, into their telemetry — which is a channel for personal data to leave
the boundary `docs/data-protection.md` draws, opened by default and closed by
nobody.
*/
func Propagation() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

/*
Requests traces what Convia is asked to do.

`M22-003` asks for safe route names, and the reason is the same one the metrics
label has: a span named for the path is a distinct operation per room, which
makes every aggregate across a route empty and every trace search a scan. The
name is the route the router matched.
*/
type Requests struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

func Trace(provider trace.TracerProvider) *Requests {
	return &Requests{tracer: provider.Tracer(Name), propagator: Propagation()}
}

/*
Began starts a span for one request and returns the function that ends it.

**The caller's trace is continued rather than replaced.** `M22-004` is that
sentence: an application that traced its own call into Convia gets one trace
across both, and Convia's work appears underneath the operation that asked for
it rather than as an unrelated trace nobody can join.

The context that comes back carries the span, so everything below — including
the log handler — can name the trace this request belongs to.

**The span is named after the handler runs**, for the reason [routeOf] gives:
the router puts the matched pattern on the request while it serves it, so the
name is not known when the span starts. It begins as the bare method and is
renamed on the way out.
*/
func (requests *Requests) Began(request *http.Request) (context.Context, func(*http.Request, int)) {
	ctx := requests.propagator.Extract(request.Context(),
		propagation.HeaderCarrier(request.Header))

	ctx, span := requests.tracer.Start(ctx, methodOf(request), trace.WithSpanKind(trace.SpanKindServer))

	return ctx, func(served *http.Request, status int) {
		route := routeOf(served)
		span.SetName(methodOf(served) + " " + route)
		span.SetAttributes(
			semconv.HTTPRequestMethodKey.String(methodOf(served)),
			semconv.HTTPRoute(route),
			semconv.HTTPResponseStatusCode(status),
		)

		/*
			Only a server fault marks the span. A 404 or a 401 is Convia
			answering correctly, and marking those would make the error rate of
			every trace search the rate at which people mistype URLs.
		*/
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		span.End()
	}
}
