package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// attending registers the presence gauges over a reader a test can drain.
func attending(t *testing.T, held Held) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	if err := Attend(provider, held, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Attend() error = %v", err)
	}
	return reader
}

func presenceGauges(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	read := map[string]int64{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			gauge, ok := recorded.Data.(metricdata.Gauge[int64])
			if !ok || len(gauge.DataPoints) == 0 {
				continue
			}
			read[recorded.Name] = gauge.DataPoints[0].Value
			if labels := gauge.DataPoints[0].Attributes.Len(); labels != 0 {
				t.Errorf("%s carries %d labels, want none", recorded.Name, labels)
			}
		}
	}
	return read
}

/*
TestPresenceIsReportedWithoutNamingAnybody.

`M17-011` asks for this without high-cardinality labels, and presence is the
signal with the most people in it — a series per person here would be the
largest cardinality mistake available in Convia. Neither gauge carries a label
at all, which is the strongest form of that.
*/
func TestPresenceIsReportedWithoutNamingAnybody(t *testing.T) {
	reader := attending(t, func(context.Context) (int64, int64, error) {
		return 120, 3, nil
	})

	read := presenceGauges(t, reader)
	if read["convia.presence.claims"] != 120 || read["convia.presence.overdue"] != 3 {
		t.Errorf("gauges = %v, want 120 claims and 3 overdue", read)
	}
	// The label assertion lives in presenceGauges, so two correct values here
	// means both were unlabelled.
}

/*
TestOverdueIsReadAgainstTheTotal.

Overdue on its own is a number without a scale: a hundred stale claims mean
nothing until somebody knows whether the deployment is holding a hundred and one
or a million. The pair is the reading, so they come from one callback and from
one moment.
*/
func TestOverdueIsReadAgainstTheTotal(t *testing.T) {
	asked := 0
	reader := attending(t, func(context.Context) (int64, int64, error) {
		asked++
		return int64(asked) * 10, int64(asked), nil
	})

	first := presenceGauges(t, reader)
	if first["convia.presence.claims"] != 10 || first["convia.presence.overdue"] != 1 {
		t.Fatalf("the first collection = %v", first)
	}

	second := presenceGauges(t, reader)
	if second["convia.presence.claims"] != 20 || second["convia.presence.overdue"] != 2 {
		t.Fatalf("the second collection = %v", second)
	}

	if asked != 2 {
		t.Errorf("the store was asked %d times for two collections, want 2 — the pair must share a moment", asked)
	}
}

/*
TestPresenceThatCannotBeCountedIsNotZero.

Zero claims is a claim that nobody is anywhere. A store that could not be
reached is not that, and reporting it as one would show every person leaving at
once.
*/
func TestPresenceThatCannotBeCountedIsNotZero(t *testing.T) {
	reader := attending(t, func(context.Context) (int64, int64, error) {
		return 0, 0, errors.New("the store said no")
	})

	if read := presenceGauges(t, reader); len(read) != 0 {
		t.Errorf("a failed count reported %v, want no measurement at all", read)
	}
}
