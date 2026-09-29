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

type stubLive struct {
	active int64
	err    error
	asked  int
}

func (stub *stubLive) CountActive(context.Context) (int64, error) {
	stub.asked++
	return stub.active, stub.err
}

type stubPresent struct {
	present int64
	err     error
}

func (stub *stubPresent) CountPresent(context.Context) (int64, error) {
	return stub.present, stub.err
}

// watching registers the call gauges over a reader a test can drain.
func watching(t *testing.T, live *stubLive, people *stubPresent) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	if err := Watch(provider, live, people, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	return reader
}

// gauges returns the value of every gauge collected, by name.
func gauges(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
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
			if attributes := gauge.DataPoints[0].Attributes.Len(); attributes != 0 {
				t.Errorf("%s carries %d labels, want none", recorded.Name, attributes)
			}
		}
	}
	return read
}

/*
TestWhatIsHappeningIsAskedRatherThanRemembered.

A counter kept in memory starts at zero when a process starts, so an instance
that restarts while ten calls are running reports zero and the graph shows an
outage that did not happen. These are observed instead, so the answer is true
whoever asks and however long they have been up — and the proof is that a
collection *after* the numbers changed reports the new ones without anything
having been told.
*/
func TestWhatIsHappeningIsAskedRatherThanRemembered(t *testing.T) {
	live := &stubLive{active: 3}
	people := &stubPresent{present: 11}
	reader := watching(t, live, people)

	if read := gauges(t, reader); read["convia.calls.active"] != 3 ||
		read["convia.participants.active"] != 11 {
		t.Fatalf("first collection = %v, want 3 calls and 11 people", read)
	}

	live.active, people.present = 0, 0

	if read := gauges(t, reader); read["convia.calls.active"] != 0 ||
		read["convia.participants.active"] != 0 {
		t.Errorf("second collection = %v, want both at zero", gauges(t, reader))
	}
	if live.asked < 2 {
		t.Errorf("the store was asked %d times for two collections", live.asked)
	}
}

/*
TestNeitherGaugeCarriesALabel is `M22-007` stated as a test.

The item asks for call metrics without user or room identifiers. No labels at
all is the strongest form of that: there is no dimension to get wrong later, and
anybody asking "which room" is asking something the API answers rather than
something a time series should.
*/
func TestNeitherGaugeCarriesALabel(t *testing.T) {
	reader := watching(t, &stubLive{active: 2}, &stubPresent{present: 5})

	read := gauges(t, reader)
	if len(read) != 2 {
		t.Fatalf("collected %d gauges, want 2: %v", len(read), read)
	}
	// The label assertion itself is inside gauges, so reaching here with two
	// gauges means both were unlabelled.
}

/*
TestNotKnowingIsNotZero.

Zero is a claim that nothing is happening. A database that could not be reached
is not that claim, and reporting it as one would show an outage in the calls
rather than in the counting.
*/
func TestNotKnowingIsNotZero(t *testing.T) {
	reader := watching(t,
		&stubLive{err: errors.New("the database said no")},
		&stubPresent{present: 5})

	if read := gauges(t, reader); len(read) != 0 {
		t.Errorf("a failed count reported %v, want no measurement at all", read)
	}
}

/*
TestOneFailureWithholdsBoth.

The two are read together because an operator compares them — people per call is
what says whether an installation is carrying a few large calls or many small
ones. Reporting one from this moment and the other from the last would make that
ratio a number that was never true.
*/
func TestOneFailureWithholdsBoth(t *testing.T) {
	reader := watching(t,
		&stubLive{active: 4},
		&stubPresent{err: errors.New("the database said no")})

	if read := gauges(t, reader); len(read) != 0 {
		t.Errorf("one half failed and %v was still reported", read)
	}
}
