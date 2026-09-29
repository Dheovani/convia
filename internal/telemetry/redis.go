package telemetry

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

/*
Redis is what Convia records about the store its instances share.

This is `M16-010`, which asked for pool usage, operation latency and failures.
All three are about one question an operator has when Convia feels slow: **is it
Convia, or is it Redis** — and without these the answer is a guess, because
every symptom appears somewhere else. A pool with no free connections looks like
a slow handler; a Redis that times out looks like presence being wrong.

It is one type used by both clients — the relay that carries events between
instances and the store that holds presence — because they share a Redis and an
operator watching one wants the other in the same picture.
*/
type Redis struct {
	duration metric.Float64Histogram
	pools    map[string]func() *goredis.PoolStats

	/*
		tracer is nil when Convia is measuring but not tracing, which is a
		supported configuration rather than half of one: metrics are cheap and
		constant, traces are turned on while somebody is looking at something.
	*/
	tracer trace.Tracer
}

/*
Connected builds the instruments both Redis clients report through.

The pool gauges are registered once here and read from whatever clients are
attached, so adding a third client later is attaching it rather than
registering a third set of instruments that mean the same thing.
*/
func Connected(provider metric.MeterProvider) (*Redis, error) {
	meter := provider.Meter(Name)

	duration, err := meter.Float64Histogram("convia.redis.command.duration",
		metric.WithDescription("How long a Redis command took, and whether it worked."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("build the Redis command histogram: %w", err)
	}

	measured := &Redis{duration: duration, pools: map[string]func() *goredis.PoolStats{}}

	connections, err := meter.Int64ObservableGauge("convia.redis.pool.connections",
		metric.WithDescription("Connections in a Redis pool, idle and in use."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("build the Redis connection gauge: %w", err)
	}

	/*
		Waits are the saturation signal and the one worth alerting on: a pool
		that hands out every connection makes Convia wait for one, and the
		symptom is a slow handler with nothing in its own timings to explain it.
	*/
	waits, err := meter.Int64ObservableCounter("convia.redis.pool.waits",
		metric.WithDescription("How often a caller had to wait for a Redis connection, "+
			"and how often waiting timed out."),
		metric.WithUnit("{wait}"))
	if err != nil {
		return nil, fmt.Errorf("build the Redis wait counter: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		for use, stats := range measured.pools {
			counted := stats()
			if counted == nil {
				continue
			}

			observer.ObserveInt64(connections, int64(counted.IdleConns),
				metric.WithAttributes(poolOf(use), attribute.String("state", "idle")))
			observer.ObserveInt64(connections, int64(counted.TotalConns-counted.IdleConns),
				metric.WithAttributes(poolOf(use), attribute.String("state", "used")))

			observer.ObserveInt64(waits, int64(counted.Misses),
				metric.WithAttributes(poolOf(use), attribute.String("outcome", "waited")))
			observer.ObserveInt64(waits, int64(counted.Timeouts),
				metric.WithAttributes(poolOf(use), attribute.String("outcome", "timed_out")))
		}
		return nil
	}, connections, waits)
	if err != nil {
		return nil, fmt.Errorf("register the Redis pool gauges: %w", err)
	}
	return measured, nil
}

/*
Attach registers a client's pool under a name and returns the hook that times
its commands.

The name says which of Convia's two uses of Redis this is — `events` or
`presence` — and is written by the caller rather than taken from the address,
because both point at the same Redis and the useful distinction is what Convia
is doing with it.

**It is not safe to call once the meter has started collecting.** It is a
composition-root operation, called while the process is being assembled, and
writing to the map afterwards would race the callback reading it.
*/
func (measured *Redis) Tracing(provider trace.TracerProvider) {
	measured.tracer = provider.Tracer(Name)
}

func (measured *Redis) Attach(use string, stats func() *goredis.PoolStats) goredis.Hook {
	measured.pools[use] = stats
	return hook{measured: measured, use: use}
}

/*
Sharing is a Redis client Convia can measure, which both of them are.

It is here rather than in either Redis package so that neither has to know a
meter exists, and it is an interface so that a deployment without Redis — where
presence is in this process and there is no relay — simply has nothing that
satisfies it.
*/
type Sharing interface {
	PoolStats() *goredis.PoolStats
	AddHook(goredis.Hook)
}

/*
Measure attaches a client, if it is one that can be measured.

It reports whether it did, so the composition root can say what is being watched
rather than assume. A store that is not backed by Redis is not a failure: it is
a single-instance deployment, and there is no pool to have an opinion about.
*/
func (measured *Redis) Measure(use string, client any) bool {
	sharing, ok := client.(Sharing)
	if !ok {
		return false
	}

	sharing.AddHook(measured.Attach(use, sharing.PoolStats))
	return true
}

/*
hook times every command a client runs.

go-redis calls it around each command, which is the only place that sees both
how long one took and whether it failed. Doing it at Convia's call sites instead
would mean timing the ones somebody remembered.
*/
type hook struct {
	measured *Redis
	use      string
}

// DialHook is required by the interface and left alone: a dial is not a
// command, and timing it here would put connection setup into the same
// histogram as the work it enables.
func (hook hook) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return next(ctx, network, address)
	}
}

func (hook hook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, command goredis.Cmder) error {
		ctx, done := hook.measured.span(ctx, command.Name())
		start := time.Now()

		err := next(ctx, command)

		hook.record(ctx, command.Name(), start, err)
		done(err)
		return err
	}
}

/*
ProcessPipelineHook times a pipeline as one command called `pipeline`.

Timing each member separately would report a latency none of them had: they were
sent together and waited together, so the only honest duration is the round trip
they shared.
*/
func (hook hook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, commands []goredis.Cmder) error {
		ctx, done := hook.measured.span(ctx, "pipeline")
		start := time.Now()

		err := next(ctx, commands)

		hook.record(ctx, "pipeline", start, err)
		done(err)
		return err
	}
}

/*
span opens one for a command, when Convia is tracing.

**The command name is the whole of it.** A Redis key here is
`convia:presence:v1:<application>:<user>` — it names a person, and putting it in
a span would ship the social graph `docs/data-protection.md` classifies to a
collector, one span at a time. The argument values are worse and are equally
absent.

`redis.Nil` does not mark the span, for the reason [hook.record] gives about the
error rate.
*/
func (measured *Redis) span(ctx context.Context, command string) (context.Context, func(error)) {
	if measured.tracer == nil {
		return ctx, func(error) {}
	}

	ctx, span := measured.tracer.Start(ctx, "redis "+strings.ToLower(command),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemRedis,
			semconv.DBOperationName(strings.ToLower(command)),
		))

	return ctx, func(err error) {
		if err != nil && err != goredis.Nil {
			span.SetStatus(codes.Error, "the command failed")
		}
		span.End()
	}
}

/*
record writes one command's timing.

**A miss is not a failure.** `redis.Nil` is how Redis says a key is not there,
which is an answer rather than an error — counting it as one would make every
read of somebody who is not present look like Redis misbehaving, and presence is
mostly people who are not present.
*/
func (hook hook) record(ctx context.Context, command string, start time.Time, err error) {
	outcome := "ok"
	if err != nil && err != goredis.Nil {
		outcome = "error"
	}

	hook.measured.duration.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(
			poolOf(hook.use),
			attribute.String("command", strings.ToLower(command)),
			attribute.String("outcome", outcome),
		))
}

/*
poolOf names which of Convia's uses of Redis a measurement belongs to.

The set is written by Convia rather than read from anything a caller controls,
so it is as large as the number of places Convia connects — two.
*/
func poolOf(use string) attribute.KeyValue {
	return attribute.String("pool", use)
}
