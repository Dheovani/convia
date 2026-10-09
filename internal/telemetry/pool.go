package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

/*
Pooled is what Convia records about the connections it holds to PostgreSQL.

This is `M04-014`, which asked for pool saturation and query latency once there
was somewhere to put them. `M22` built that, and what it built for the database
was **tracing**: `Queries` puts every statement in a span beneath the request
that ran it, which answers "why was this request slow" and says nothing about
"are requests slow". A trace is one occurrence; these are the distribution.

The gauges mirror the Redis ones deliberately. An operator asking whether Convia
is waiting on a connection should not have to learn two shapes of answer for the
two pools it keeps, and the pool that saturates first is rarely the one anybody
was watching.
*/
type Pooled struct {
	pools map[string]func() PoolStats
}

/*
PoolStats is what this package needs to know about a pool, and nothing else.

It is a shape of its own rather than `*pgxpool.Stat` for two reasons. The
driver's type has unexported fields and no constructor, so **nothing can build
one** -- which would leave this measurable only against a real database, and a
counter nobody can test is a counter nobody can trust. And a telemetry package
that imported the driver would make every future pool a pgx pool.
*/
type PoolStats struct {
	// Idle and Used are connections open and doing nothing, and open and busy.
	Idle int64
	Used int64

	// Limit is the most this pool may open, without which Used means nothing.
	Limit int64

	// Waits is how often a caller found none free and had to wait.
	Waits int64

	// Waiting is how long those waits have taken in total.
	Waiting time.Duration
}

/*
poolAttribute names which pool a measurement is about.

There is one today and the name is still carried, for the reason the Redis
module carries it: a second pool -- a read replica, a separate one for
migrations -- would otherwise arrive as numbers that silently add up with the
first, and the sum of two saturations is not a saturation.
*/
func databasePoolOf(use string) attribute.KeyValue {
	return attribute.String("pool", use)
}

/*
Holding builds the instruments and the callback that reads them.

**The gauges are observed rather than recorded**, which is what the pool
allows: `pgxpool` keeps counters and Convia reads them when the exporter asks,
so nothing is on the path of a query. A counter incremented per acquire would
put telemetry in front of every statement for numbers the pool already has.
*/
func Holding(provider metric.MeterProvider) (*Pooled, error) {
	meter := provider.Meter(Name)
	measured := &Pooled{pools: map[string]func() PoolStats{}}

	connections, err := meter.Int64ObservableGauge("convia.database.pool.connections",
		metric.WithDescription("Connections in a PostgreSQL pool, idle and in use."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("build the database connection gauge: %w", err)
	}

	/*
		The ceiling is reported alongside, because saturation is a ratio and a
		count of used connections means nothing without it. An operator reading
		"twelve in use" cannot tell a healthy pool of fifty from an exhausted
		one of twelve.
	*/
	limit, err := meter.Int64ObservableGauge("convia.database.pool.limit",
		metric.WithDescription("The most connections this pool may open."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("build the database limit gauge: %w", err)
	}

	/*
		Waits are the saturation signal and the one worth alerting on.

		A pool that has handed out every connection makes the next query wait
		for one, and the symptom is a slow handler with nothing in its own
		timings to explain it -- which is exactly the shape of the Redis signal
		beside it, and exactly the confusion both exist to end.
	*/
	waits, err := meter.Int64ObservableCounter("convia.database.pool.waits",
		metric.WithDescription("How often a query found no free connection and had to wait for one."),
		metric.WithUnit("{wait}"))
	if err != nil {
		return nil, fmt.Errorf("build the database wait counter: %w", err)
	}

	/*
		How long those waits took, in total.

		The count says it happened and this says what it cost. A thousand waits
		of a microsecond is a pool running warm; ten waits of a second each is
		ten requests somebody noticed.
	*/
	waiting, err := meter.Float64ObservableCounter("convia.database.pool.wait.duration",
		metric.WithDescription("Time spent waiting for a free PostgreSQL connection."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("build the database wait duration counter: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		for use, stats := range measured.pools {
			counted := stats()
			named := metric.WithAttributes(databasePoolOf(use))

			observer.ObserveInt64(connections, counted.Idle,
				metric.WithAttributes(databasePoolOf(use), attribute.String("state", "idle")))
			observer.ObserveInt64(connections, counted.Used,
				metric.WithAttributes(databasePoolOf(use), attribute.String("state", "used")))

			observer.ObserveInt64(limit, counted.Limit, named)
			observer.ObserveInt64(waits, counted.Waits, named)
			observer.ObserveFloat64(waiting, counted.Waiting.Seconds(), named)
		}
		return nil
	}, connections, limit, waits, waiting)
	if err != nil {
		return nil, fmt.Errorf("register the database pool gauges: %w", err)
	}

	return measured, nil
}

/*
Attach registers a pool under a name, to be read whenever the exporter asks.

It takes a function rather than a value so that a pool closed and replaced is
followed rather than remembered, and so that nothing is read until an exporter
asks.
*/
func (measured *Pooled) Attach(use string, stats func() PoolStats) {
	if measured == nil {
		return
	}
	measured.pools[use] = stats
}
