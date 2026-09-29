package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

/*
recording builds instruments over a reader a test can drain.

This is `M22-010`: measurements are read back in memory rather than sent
anywhere, so what is asserted is what a collector would receive and no test
needs a collector.
*/
func recording(t *testing.T) (*Serving, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	serving, err := Serve(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	return serving, reader
}

// collected drains the reader and returns one instrument's data points.
func collected(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != name {
				continue
			}
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want a float histogram", name, recorded.Data)
			}
			return histogram.DataPoints
		}
	}
	t.Fatalf("no instrument named %q was collected", name)
	return nil
}

// answering records one request as the middleware would.
func answering(serving *Serving, method, target, pattern string, status int) {
	request := httptest.NewRequest(method, target, nil)
	request.Pattern = pattern

	done := serving.Began(request.Context())
	done(request, status)
}

func label(point metricdata.HistogramDataPoint[float64], key string) string {
	value, _ := point.Attributes.Value(attribute.Key(key))
	return value.String()
}

/*
TestThePathIsNeverALabel is the one that matters, and it is about the bill as
much as about the graph.

`/v1/rooms/{room_id}` is one series. `/v1/rooms/rom_ABC` is one series per room,
which is an unbounded number chosen by whoever can send a request — and a
metrics backend does not refuse that. It accepts it until it falls over.
*/
func TestThePathIsNeverALabel(t *testing.T) {
	serving, reader := recording(t)

	for _, room := range []string{"rom_AAA", "rom_BBB", "rom_CCC", "rom_DDD"} {
		answering(serving, http.MethodGet, "/v1/rooms/"+room,
			"GET /v1/rooms/{room_id}", http.StatusOK)
	}

	points := collected(t, reader, "http.server.request.duration")
	if len(points) != 1 {
		t.Fatalf("four rooms produced %d series, want 1 — the path is being labelled", len(points))
	}

	if got := label(points[0], "http.route"); got != "/v1/rooms/{room_id}" {
		t.Errorf("http.route = %q, want the pattern", got)
	}
	if points[0].Count != 4 {
		t.Errorf("count = %d, want 4", points[0].Count)
	}
}

/*
TestAnUnroutedRequestIsOneSeries.

A request that matched nothing carries no pattern, and labelling it with what it
asked for would hand the same unbounded dimension to anybody who can send a 404
— which is anybody at all, without a credential.
*/
func TestAnUnroutedRequestIsOneSeries(t *testing.T) {
	serving, reader := recording(t)

	for _, target := range []string{"/wp-admin", "/.env", "/../../etc/passwd", "/nope"} {
		answering(serving, http.MethodGet, target, "", http.StatusNotFound)
	}

	points := collected(t, reader, "http.server.request.duration")
	if len(points) != 1 {
		t.Fatalf("four unrouted paths produced %d series, want 1", len(points))
	}
	if got := label(points[0], "http.route"); got != other {
		t.Errorf("http.route = %q, want %q", got, other)
	}
}

/*
TestTheCatchAllIsNotARoute.

Convia serves its own interface from a pattern that matches whatever nothing
else did. That is not a route in the sense this label means, and letting it
through would put every asset and every unknown page under one heading that
means "something".
*/
func TestTheCatchAllIsNotARoute(t *testing.T) {
	serving, reader := recording(t)

	answering(serving, http.MethodGet, "/rooms/abc", "GET /", http.StatusOK)
	answering(serving, http.MethodGet, "/settings", "GET /", http.StatusOK)

	points := collected(t, reader, "http.server.request.duration")
	if got := label(points[0], "http.route"); got != other {
		t.Errorf("http.route = %q, want %q — the catch-all was treated as a route", got, other)
	}
}

/*
TestAnInventedMethodIsOneSeries.

The method is whatever bytes a client put on the request line, so somebody who
wanted to could offer a different one every time. Only what Convia serves is a
label.
*/
func TestAnInventedMethodIsOneSeries(t *testing.T) {
	serving, reader := recording(t)

	for _, method := range []string{"PROPFIND", "TRACE", "BREW", "WHATEVER"} {
		answering(serving, method, "/v1/rooms", "PROPFIND /v1/rooms", http.StatusMethodNotAllowed)
	}

	points := collected(t, reader, "http.server.request.duration")
	if len(points) != 1 {
		t.Fatalf("four invented methods produced %d series, want 1", len(points))
	}
	if got := label(points[0], "http.request.method"); got != other {
		t.Errorf("http.request.method = %q, want %q", got, other)
	}
}

/*
TestFailuresAreDistinguishableFromSuccesses.

`M22-006` asks for a request rate, an error rate and a latency. This is why it
is one instrument rather than three: the histogram carries its own count, so
cutting it by status gives all of them, and there is no counter beside it that
can disagree about the total.
*/
func TestFailuresAreDistinguishableFromSuccesses(t *testing.T) {
	serving, reader := recording(t)

	answering(serving, http.MethodGet, "/v1/rooms", "GET /v1/rooms", http.StatusOK)
	answering(serving, http.MethodGet, "/v1/rooms", "GET /v1/rooms", http.StatusOK)
	answering(serving, http.MethodGet, "/v1/rooms", "GET /v1/rooms", http.StatusInternalServerError)

	points := collected(t, reader, "http.server.request.duration")
	if len(points) != 2 {
		t.Fatalf("two statuses produced %d series, want 2", len(points))
	}

	counts := map[string]uint64{}
	for _, point := range points {
		counts[label(point, "http.response.status_code")] = point.Count
	}
	if counts["200"] != 2 || counts["500"] != 1 {
		t.Errorf("counts = %v, want two 200s and one 500", counts)
	}
}

/*
TestNothingIsMeasuredWhenNothingIsConfigured.

Off is the default, and it has to be a real no-op rather than an exporter
pointed at nowhere: the instruments are called on every request, so anything
they cost is paid by every deployment that never asked for telemetry.
*/
func TestNothingIsMeasuredWhenNothingIsConfigured(t *testing.T) {
	provider, shutdown, err := Measuring(context.Background(),
		Describing("development", "test"), "")
	if err != nil {
		t.Fatalf("Measuring() with no endpoint error = %v", err)
	}

	serving, err := Serve(provider)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	// It still has to be callable, because the middleware does not check.
	answering(serving, http.MethodGet, "/v1/rooms", "GET /v1/rooms", http.StatusOK)

	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutting down an unconfigured provider error = %v", err)
	}
}

/*
TestSaturationRisesAndFalls.

A gauge that only climbs is worse than no gauge, because it looks like load. The
increment and the decrement are one call at the call site for that reason, and
this is what says they stay paired.
*/
func TestSaturationRisesAndFalls(t *testing.T) {
	serving, reader := recording(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/rooms", nil)
	request.Pattern = "GET /v1/rooms"

	first := serving.Began(request.Context())
	second := serving.Began(request.Context())

	if got := saturation(t, reader); got != 2 {
		t.Errorf("active requests = %d while two are in flight, want 2", got)
	}

	first(request, http.StatusOK)
	second(request, http.StatusOK)

	if got := saturation(t, reader); got != 0 {
		t.Errorf("active requests = %d after both finished, want 0", got)
	}
}

func saturation(t *testing.T, reader *sdkmetric.ManualReader) int64 {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "http.server.active_requests" {
				continue
			}
			sum, ok := recorded.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) == 0 {
				t.Fatalf("active_requests is %T with no points", recorded.Data)
			}
			return sum.DataPoints[0].Value
		}
	}
	t.Fatal("active_requests was not collected")
	return 0
}
