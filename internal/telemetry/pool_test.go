package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func holding(t *testing.T) (*Pooled, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	measured, err := Holding(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Holding() error = %v", err)
	}
	return measured, reader
}

/* gathered reads every point back, keyed by what distinguishes it. */
func gathered(t *testing.T, reader *sdkmetric.ManualReader) map[string]float64 {
	t.Helper()

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	read := map[string]float64{}
	for _, scope := range collected.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch data := recorded.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range data.DataPoints {
					state, _ := point.Attributes.Value(attribute.Key("state"))
					pool, _ := point.Attributes.Value(attribute.Key("pool"))
					read[recorded.Name+"|"+pool.String()+"|"+state.String()] = float64(point.Value)
				}
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					pool, _ := point.Attributes.Value(attribute.Key("pool"))
					read[recorded.Name+"|"+pool.String()+"|"] = float64(point.Value)
				}
			case metricdata.Sum[float64]:
				for _, point := range data.DataPoints {
					pool, _ := point.Attributes.Value(attribute.Key("pool"))
					read[recorded.Name+"|"+pool.String()+"|"] = point.Value
				}
			}
		}
	}
	return read
}

/*
TestSaturationIsVisibleWithoutGuessingAtIt is why this exists at all.

`Queries` already traces every statement, which answers "why was this request
slow" and nothing about "are requests slow". **A pool that has handed out every
connection makes the next query wait**, and the symptom is a slow handler with
nothing in its own timings to explain it -- the one failure the traces beside it
cannot show.
*/
func TestSaturationIsVisibleWithoutGuessingAtIt(t *testing.T) {
	measured, reader := holding(t)

	measured.Attach("primary", func() PoolStats {
		return PoolStats{Idle: 2, Used: 10, Limit: 12, Waits: 97, Waiting: 3 * time.Second}
	})

	read := gathered(t, reader)

	if read["convia.database.pool.connections|primary|idle"] != 2 {
		t.Errorf("idle = %v, want 2", read["convia.database.pool.connections|primary|idle"])
	}
	if read["convia.database.pool.connections|primary|used"] != 10 {
		t.Errorf("used = %v, want 10", read["convia.database.pool.connections|primary|used"])
	}

	/*
		The ceiling, without which the count above means nothing: ten in use is
		a healthy pool of fifty and an exhausted one of twelve, and an operator
		reading only the first cannot tell them apart.
	*/
	if read["convia.database.pool.limit|primary|"] != 12 {
		t.Errorf("limit = %v, want 12", read["convia.database.pool.limit|primary|"])
	}

	if read["convia.database.pool.waits|primary|"] != 97 {
		t.Errorf("waits = %v, want 97", read["convia.database.pool.waits|primary|"])
	}

	// How often it happened and what it cost are different questions: a
	// thousand waits of a microsecond is a pool running warm.
	if read["convia.database.pool.wait.duration|primary|"] != 3 {
		t.Errorf("waiting = %v seconds, want 3", read["convia.database.pool.wait.duration|primary|"])
	}
}

/*
TestEachPoolIsCountedApart keeps a second pool from hiding the first.

There is one today, and the name is carried anyway: a read replica or a pool
kept for migrations would otherwise arrive as numbers that silently add up, and
**the sum of two saturations is not a saturation** -- one pool exhausted beside
one idle reads as neither.
*/
func TestEachPoolIsCountedApart(t *testing.T) {
	measured, reader := holding(t)

	measured.Attach("primary", func() PoolStats {
		return PoolStats{Idle: 0, Used: 12, Limit: 12, Waits: 500}
	})
	measured.Attach("replica", func() PoolStats {
		return PoolStats{Idle: 9, Used: 1, Limit: 10, Waits: 0}
	})

	read := gathered(t, reader)

	if read["convia.database.pool.waits|primary|"] != 500 {
		t.Errorf("the exhausted pool reported %v waits, want 500",
			read["convia.database.pool.waits|primary|"])
	}
	if read["convia.database.pool.waits|replica|"] != 0 {
		t.Errorf("the idle pool reported %v waits, want 0",
			read["convia.database.pool.waits|replica|"])
	}
	if read["convia.database.pool.connections|primary|idle"] != 0 {
		t.Error("the exhausted pool reported free connections")
	}
	if read["convia.database.pool.connections|replica|idle"] != 9 {
		t.Error("the idle pool's free connections were not reported as its own")
	}
}

/*
TestNothingIsReadUntilSomebodyCollects keeps telemetry off the path of a query.

The pool keeps these counters itself, so Convia reads them when an exporter
asks. Counting per acquire instead would put this in front of every statement
for numbers that already exist.
*/
func TestNothingIsReadUntilSomebodyCollects(t *testing.T) {
	measured, reader := holding(t)

	reads := 0
	measured.Attach("primary", func() PoolStats {
		reads++
		return PoolStats{Idle: 1, Used: 1, Limit: 2}
	})

	if reads != 0 {
		t.Errorf("the pool was read %d times before anybody collected", reads)
	}

	gathered(t, reader)
	if reads != 1 {
		t.Errorf("the pool was read %d times for one collection, want 1", reads)
	}
}

/*
TestMeasuringNothingIsNotAFailure is the configuration a single instance runs.

A `Pooled` nobody attached to reports nothing and does not fail collecting,
because an exporter asking a Convia that has not opened its pool yet is a
startup ordering question rather than an error.
*/
func TestMeasuringNothingIsNotAFailure(t *testing.T) {
	_, reader := holding(t)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() with nothing attached error = %v", err)
	}
}
