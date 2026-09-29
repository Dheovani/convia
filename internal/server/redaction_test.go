package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"convia/internal/api"
	"convia/internal/telemetry"
)

/*
The things that must never appear in telemetry, planted where a careless
instrument would pick them up.

Each is a different class from `docs/data-protection.md`: a credential, a
conversation, and an identifier an application chose for one of its people.
*/
const (
	plantedKey    = "cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"
	plantedSaid   = "the merger closes on tuesday"
	plantedPerson = "ana@example.com"
)

// watching drives requests through the whole telemetry stack and gives back
// everything the three signals produced.
type watching struct {
	handler http.Handler
	logs    *bytes.Buffer
	metrics *sdkmetric.ManualReader
	spans   *tracetest.SpanRecorder
}

func watched(t *testing.T) *watching {
	t.Helper()

	logs := &bytes.Buffer{}
	logger := slog.New(telemetry.Correlate(slog.NewJSONHandler(logs, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	reader := sdkmetric.NewManualReader()
	serving, err := telemetry.Serve(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	recorder := tracetest.NewSpanRecorder()
	requests := telemetry.Trace(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	/*
		The handlers write to the same buffer, so this covers what a domain
		logs as well as what the middleware does — the two fail differently and
		a test that saw only one would miss the other.
	*/
	previous := dependencyLogger
	dependencyLogger = logger
	t.Cleanup(func() { dependencyLogger = previous })

	dependencies := testDependencies()
	dependencies.Serving = serving
	dependencies.Requests = requests

	return &watching{
		handler: New("127.0.0.1:0", logger, dependencies).Handler,
		logs:    logs,
		metrics: reader,
		spans:   recorder,
	}
}

// everything is the whole of what the three signals would ship, as text.
func (seen *watching) everything(t *testing.T) string {
	t.Helper()

	written := &strings.Builder{}
	written.WriteString(seen.logs.String())

	var gathered metricdata.ResourceMetrics
	if err := seen.metrics.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			written.WriteString(recorded.Name + " " + recorded.Description + "\n")
			written.WriteString(attributesOf(recorded) + "\n")
		}
	}

	for _, span := range seen.spans.Ended() {
		written.WriteString(span.Name() + "\n")
		for _, held := range span.Attributes() {
			written.WriteString(string(held.Key) + "=" + held.Value.String() + "\n")
		}
		for _, event := range span.Events() {
			written.WriteString(event.Name + "\n")
		}
		if status := span.Status(); status.Description != "" {
			written.WriteString(status.Description + "\n")
		}
	}

	return written.String()
}

// attributesOf flattens whatever shape of data point an instrument produced.
func attributesOf(recorded metricdata.Metrics) string {
	written := &strings.Builder{}

	switch data := recorded.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, point := range data.DataPoints {
			written.WriteString(point.Attributes.Encoded(nil) + "\n")
		}
	case metricdata.Sum[int64]:
		for _, point := range data.DataPoints {
			written.WriteString(point.Attributes.Encoded(nil) + "\n")
		}
	case metricdata.Gauge[int64]:
		for _, point := range data.DataPoints {
			written.WriteString(point.Attributes.Encoded(nil) + "\n")
		}
	}
	return written.String()
}

/*
TestNothingSensitiveReachesTelemetry is `M22-009` as one test rather than as a
list of promises.

Most of what it asserts was already true — secrets redact themselves, an event
carries no body, a database span carries no arguments — but each of those is
enforced somewhere different, and **there was nothing that would notice a new
instrument or a new span attribute carrying one of them.** This is that
something. It fails when somebody adds a label they should not, which is the
only moment the mistake is cheap.

It drives a real request through the real chain with all three signals
attached, so what it inspects is what a collector would receive.
*/
func TestNothingSensitiveReachesTelemetry(t *testing.T) {
	seen := watched(t)

	body, err := json.Marshal(map[string]any{
		"external_subject": plantedPerson,
		"display_name":     plantedSaid,
	})
	if err != nil {
		t.Fatalf("build the request body: %v", err)
	}

	request := jsonRequest(http.MethodPost, api.Prefix+"/users", string(body))
	request.Header.Set("Authorization", "Bearer "+plantedKey)

	response := httptest.NewRecorder()
	seen.handler.ServeHTTP(response, request)

	shipped := seen.everything(t)
	for what, planted := range map[string]string{
		"a credential":        plantedKey,
		"the secret half":     strings.Split(plantedKey, "_")[2],
		"what somebody wrote": plantedSaid,
		"who somebody is":     plantedPerson,
	} {
		if strings.Contains(shipped, planted) {
			t.Errorf("%s reached telemetry: %q appears in\n%s", what, planted, shipped)
		}
	}
}

/*
TestNothingSensitiveReachesTelemetryWhenThingsGoWrong.

The failure paths are the ones that log the most and are written in the most
hurry, so they are where a careless `"error", err` carrying a token would land.
A refused credential is the case that has one to carry.
*/
func TestNothingSensitiveReachesTelemetryWhenThingsGoWrong(t *testing.T) {
	seen := watched(t)

	for range 3 {
		request := authenticatedRequest(http.MethodGet, api.Prefix+"/users", "")
		request.Header.Set("Authorization", "Bearer "+plantedKey)

		response := httptest.NewRecorder()
		seen.handler.ServeHTTP(response, request)
	}

	shipped := seen.everything(t)
	if strings.Contains(shipped, plantedKey) {
		t.Errorf("a refused credential reached telemetry:\n%s", shipped)
	}
	if strings.Contains(shipped, strings.Split(plantedKey, "_")[2]) {
		t.Errorf("the secret half of a refused credential reached telemetry:\n%s", shipped)
	}
}

/*
TestTheAuthorizationHeaderIsNeverAnAttribute.

It is the single most dangerous field to instrument, because a tracer that
recorded request headers would be a reasonable-looking tracer that shipped every
application key in the deployment.
*/
func TestTheAuthorizationHeaderIsNeverAnAttribute(t *testing.T) {
	seen := watched(t)

	request := authenticatedRequest(http.MethodGet, api.Prefix+"/users", "")
	response := httptest.NewRecorder()
	seen.handler.ServeHTTP(response, request)

	shipped := strings.ToLower(seen.everything(t))
	for _, forbidden := range []string{"authorization", "bearer ", "cookie", "set-cookie"} {
		if strings.Contains(shipped, forbidden) {
			t.Errorf("%q appears in telemetry:\n%s", forbidden, shipped)
		}
	}
}
