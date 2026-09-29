package telemetry

import (
	"strings"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// tracingRedis builds Redis instruments that both measure and trace.
func tracingRedis(t *testing.T) (*Redis, *tracetest.SpanRecorder) {
	t.Helper()

	measured, err := Connected(sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewManualReader())))
	if err != nil {
		t.Fatalf("Connected() error = %v", err)
	}

	recorder := tracetest.NewSpanRecorder()
	measured.Tracing(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	return measured, recorder
}

/*
TestARedisSpanNamesTheCommandAndNothingElse.

A Redis key in Convia is `convia:presence:v1:<application>:<user>` — it names a
person. Putting one in a span would ship the social graph
`docs/data-protection.md` classifies to a collector, one span at a time, and the
argument values would be worse.
*/
func TestARedisSpanNamesTheCommandAndNothingElse(t *testing.T) {
	measured, recorder := tracingRedis(t)
	client := &stubSharing{stats: &goredis.PoolStats{}}
	measured.Measure("presence", client)

	client.run(t, "hset", nil)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}

	span := spans[0]
	if span.Name() != "redis hset" {
		t.Errorf("span name = %q, want the command", span.Name())
	}
	for _, held := range span.Attributes() {
		value := held.Value.String()
		if strings.Contains(value, "convia:") || strings.Contains(value, "usr_") {
			t.Errorf("%s carries a key: %s", held.Key, value)
		}
	}
}

/*
TestATracelessRedisStillMeasures.

Measuring without tracing is a supported configuration rather than half of one:
metrics are cheap and constant, and traces are turned on while somebody is
looking at something. A client attached before any tracer exists must not
panic on its first command.
*/
func TestATracelessRedisStillMeasures(t *testing.T) {
	measured, _ := connecting(t)
	client := &stubSharing{stats: &goredis.PoolStats{}}
	measured.Measure("presence", client)

	client.run(t, "get", nil)
}
