package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

/*
Measuring builds the meter provider, which is a no-op unless one is configured.

**Off is the default and it is a real no-op**, not an exporter pointed at
nowhere: with no endpoint there is no collection goroutine, no accumulation and
no periodic flush, so a laptop and a test run pay nothing at all. That is what
makes it safe for the instruments to be called unconditionally everywhere else.

Shutdown is returned rather than hidden, because the last interval's
measurements are still in memory when a process is asked to stop, and a
deployment that reports nothing about its final minute reports nothing about the
minute that usually matters.
*/
func Measuring(
	ctx context.Context,
	service Service,
	endpoint string,
) (metric.MeterProvider, func(context.Context) error, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return noop.NewMeterProvider(), func(context.Context) error { return nil }, nil
	}

	exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("build the metrics exporter: %w", err)
	}

	/*
		The same four attributes every log line carries, so that a metric and a
		log line from one process describe it identically rather than in two
		vocabularies somebody has to join by hand.
	*/
	described := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service.Name),
		semconv.ServiceVersion(service.Version),
		semconv.ServiceInstanceID(service.Instance),
		semconv.DeploymentEnvironment(service.Environment),
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(described),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	return provider, provider.Shutdown, nil
}

/*
Serving is what Convia records about the requests it answers.

Two instruments, which is fewer than it looks: a histogram carries its own
count, so the request rate and the error rate are both read off the duration
without a counter beside it saying the same thing in a way that can disagree.

`M22-006` asks for request, error, latency and saturation. The first three are
the histogram cut by status. Saturation is the in-flight gauge: the number that
says whether Convia is keeping up, as opposed to whether it is fast.
*/
type Serving struct {
	duration metric.Float64Histogram
	inFlight metric.Int64UpDownCounter
}

// Serve builds the request instruments from a provider.
func Serve(provider metric.MeterProvider) (*Serving, error) {
	meter := provider.Meter(Name)

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithDescription("How long Convia took to answer a request."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("build the request duration histogram: %w", err)
	}

	inFlight, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("How many requests Convia is answering right now."),
		metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("build the active request counter: %w", err)
	}

	return &Serving{duration: duration, inFlight: inFlight}, nil
}

/*
Began records a request arriving, and returns the function that records it
leaving.

The pair is one call at the call site so that the two cannot drift apart: a
middleware that increments and then returns early on some path would leave the
gauge climbing for ever, and a gauge that only climbs is worse than no gauge,
because it looks like load.

**The route is read when the request leaves, not when it arrives.** The router
puts the matched pattern on the request while it serves it, so asking on the way
in would label everything as unmatched and the label would be silently useless
rather than absent.

**The gauge carries no labels at all.** Saturation is a property of the process
— whether Convia is keeping up — and cutting it by route answers a different
question badly: a route nobody is calling contributes a series that is always
zero, and the sum across routes is the only number anybody reads anyway.
*/
func (serving *Serving) Began(ctx context.Context) func(request *http.Request, status int) {
	serving.inFlight.Add(ctx, 1)
	start := time.Now()

	return func(request *http.Request, status int) {
		serving.inFlight.Add(ctx, -1)
		serving.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
			semconv.HTTPRequestMethodKey.String(methodOf(request)),
			semconv.HTTPRoute(routeOf(request)),
			semconv.HTTPResponseStatusCode(status),
		))
	}
}

/*
routeOf is the cardinality guard, and it is the whole reason this is a function.

**The label is the route, never the path.** `/v1/rooms/{room_id}` is one series
and `/v1/rooms/rom_ABC` is one series per room, which is an unbounded number
somebody else chooses — and the one who chooses it is whoever can send a
request. A metrics backend does not refuse that; it accepts it until it falls
over, and the bill arrives either way.

The pattern comes from the router, so the set of values is the route table.
Anything that matched no route, or matched the catch-all the interface is served
from, is collapsed into one value rather than reporting the path it asked for.
*/
func routeOf(request *http.Request) string {
	pattern := request.Pattern
	if pattern == "" {
		return other
	}

	// The router registers patterns as "METHOD /path"; the method is already
	// its own attribute, so carrying it here would say it twice.
	if _, path, found := strings.Cut(pattern, " "); found {
		pattern = path
	}

	// The catch-all serving the interface matches everything nothing else did,
	// so it is not a route in the sense this label means.
	if pattern == "/" || pattern == "/{$}" {
		return other
	}
	return pattern
}

/*
methodOf bounds the other half.

A method is whatever bytes a client put on the request line, so an attacker who
wanted to could offer a different one every time. Only the methods Convia serves
are labels; everything else is one value.
*/
func methodOf(request *http.Request) string {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return request.Method
	default:
		return other
	}
}

/*
other is what every unbounded dimension collapses to.

It is one value rather than the real one on purpose: the point of a label is to
group, and a label with a million values groups nothing while costing more than
everything else put together.
*/
const other = "other"
