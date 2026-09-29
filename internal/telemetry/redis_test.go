package telemetry

import (
	"context"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// stubSharing is a Redis client with a pool that says whatever a test wants.
type stubSharing struct {
	stats *goredis.PoolStats
	hooks []goredis.Hook
}

func (stub *stubSharing) PoolStats() *goredis.PoolStats { return stub.stats }
func (stub *stubSharing) AddHook(hook goredis.Hook)     { stub.hooks = append(stub.hooks, hook) }

// run puts one command through whatever hook was attached.
func (stub *stubSharing) run(t *testing.T, name string, err error) {
	t.Helper()
	if len(stub.hooks) == 0 {
		t.Fatal("nothing was attached to this client")
	}

	command := goredis.NewStringCmd(context.Background(), name)
	process := stub.hooks[0].ProcessHook(func(context.Context, goredis.Cmder) error { return err })
	_ = process(context.Background(), command)
}

func connecting(t *testing.T) (*Redis, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	measured, err := Connected(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Connected() error = %v", err)
	}
	return measured, reader
}

// commands returns the duration histogram's points, keyed by command:outcome.
func commands(t *testing.T, reader *sdkmetric.ManualReader) map[string]uint64 {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	counted := map[string]uint64{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "convia.redis.command.duration" {
				continue
			}
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("the command duration is %T, want a float histogram", recorded.Data)
			}
			for _, point := range histogram.DataPoints {
				command, _ := point.Attributes.Value(attribute.Key("command"))
				outcome, _ := point.Attributes.Value(attribute.Key("outcome"))
				counted[command.String()+":"+outcome.String()] = point.Count
			}
		}
	}
	return counted
}

/*
TestAMissingKeyIsAnAnswerRatherThanAFailure.

`redis.Nil` is how Redis says a key is not there. Counting it as an error would
make every read of somebody who is not present look like Redis misbehaving — and
presence is mostly people who are not present, so the error rate would sit near
a hundred percent while nothing at all was wrong.
*/
func TestAMissingKeyIsAnAnswerRatherThanAFailure(t *testing.T) {
	measured, reader := connecting(t)
	client := &stubSharing{stats: &goredis.PoolStats{}}

	if !measured.Measure("presence", client) {
		t.Fatal("Measure() refused a client that can be measured")
	}

	client.run(t, "get", goredis.Nil)
	client.run(t, "get", nil)
	client.run(t, "get", errors.New("connection reset"))

	counted := commands(t, reader)
	if counted["get:ok"] != 2 {
		t.Errorf("get:ok = %d, want 2 — a missing key and a hit are both answers", counted["get:ok"])
	}
	if counted["get:error"] != 1 {
		t.Errorf("get:error = %d, want 1", counted["get:error"])
	}
}

/*
TestEachUseOfRedisIsCountedApart.

Convia connects for two reasons — carrying events between instances, and holding
presence — and they point at the same Redis. Which one is slow is the useful
distinction, and the address cannot say.
*/
func TestEachUseOfRedisIsCountedApart(t *testing.T) {
	measured, reader := connecting(t)

	presence := &stubSharing{stats: &goredis.PoolStats{}}
	events := &stubSharing{stats: &goredis.PoolStats{}}
	measured.Measure("presence", presence)
	measured.Measure("events", events)

	presence.run(t, "zadd", nil)
	events.run(t, "publish", nil)

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pools := map[string]bool{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				pool, _ := point.Attributes.Value(attribute.Key("pool"))
				pools[pool.String()] = true
			}
		}
	}

	if !pools["presence"] || !pools["events"] {
		t.Errorf("the pools measured were %v, want both presence and events", pools)
	}
}

/*
TestAStoreThatIsNotRedisIsNotAFailure.

A single-instance deployment keeps presence in its own memory and has no relay,
so there is no pool to have an opinion about. Measuring reports that it did
nothing rather than refusing to start.
*/
func TestAStoreThatIsNotRedisIsNotAFailure(t *testing.T) {
	measured, _ := connecting(t)

	if measured.Measure("presence", struct{}{}) {
		t.Error("Measure() claimed to have attached to something that is not a Redis client")
	}
}

/*
TestWaitingForAConnectionIsVisible.

This is the saturation signal and the one worth alerting on: a pool that has
handed out every connection makes Convia wait for one, and the symptom is a slow
handler with nothing in its own timings to explain it.
*/
func TestWaitingForAConnectionIsVisible(t *testing.T) {
	measured, reader := connecting(t)

	measured.Measure("presence", &stubSharing{stats: &goredis.PoolStats{
		TotalConns: 10, IdleConns: 3, Misses: 42, Timeouts: 7,
	}})

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	read := map[string]int64{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch data := recorded.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range data.DataPoints {
					state, _ := point.Attributes.Value(attribute.Key("state"))
					read["connections:"+state.String()] = point.Value
				}
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					outcome, _ := point.Attributes.Value(attribute.Key("outcome"))
					read["waits:"+outcome.String()] = point.Value
				}
			}
		}
	}

	if read["connections:idle"] != 3 || read["connections:used"] != 7 {
		t.Errorf("connections = %v, want 3 idle and 7 used of 10", read)
	}
	if read["waits:waited"] != 42 || read["waits:timed_out"] != 7 {
		t.Errorf("waits = %v, want 42 waited and 7 timed out", read)
	}
}
