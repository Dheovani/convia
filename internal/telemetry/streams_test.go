package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// stubOpen is however many streams a test says are open.
type stubOpen struct{ open int }

func (stub *stubOpen) Active() int { return stub.open }

func following(t *testing.T, open *stubOpen) (*Streams, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	streams, err := Follow(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), open)
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}
	return streams, reader
}

// endings returns how many streams ended, by reason.
func endings(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	counted := map[string]int64{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "convia.event.streams.ended" {
				continue
			}
			sum, ok := recorded.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("the ending counter is %T, want an int sum", recorded.Data)
			}
			for _, point := range sum.DataPoints {
				reason, _ := point.Attributes.Value(attribute.Key("reason"))
				counted[reason.String()] = point.Value
			}
		}
	}
	return counted
}

// deliveries returns the delivery histogram's points.
func deliveries(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "convia.event.delivery.duration" {
				continue
			}
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("the delivery duration is %T, want a float histogram", recorded.Data)
			}
			return histogram.DataPoints
		}
	}
	return nil
}

/*
TestWhyAStreamEndedIsCountedApart.

`behind` is the reading that matters and it is the one that hides: a subscriber
whose view got a hole in it reconnects, fills the gap from the journal, and
every other signal looks healthy. Counting all three endings together would
average that away into "streams end sometimes", which they do.
*/
func TestWhyAStreamEndedIsCountedApart(t *testing.T) {
	streams, reader := following(t, &stubOpen{})

	streams.Ended("reader")
	streams.Ended("behind")
	streams.Ended("behind")
	streams.Ended("shutdown")

	counted := endings(t, reader)
	if counted["behind"] != 2 || counted["reader"] != 1 || counted["shutdown"] != 1 {
		t.Errorf("endings = %v, want two behind, one reader, one shutdown", counted)
	}
}

/*
TestNothingSaysWhoseStreamEnded.

What is counted is the installation's behaviour, not anybody's use of it. The
reason is the only label, and there are three of it.
*/
func TestNothingSaysWhoseStreamEnded(t *testing.T) {
	streams, reader := following(t, &stubOpen{})
	streams.Ended("behind")

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			sum, ok := recorded.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				if point.Attributes.Len() != 1 {
					t.Errorf("%s carries %d labels, want only the reason",
						recorded.Name, point.Attributes.Len())
				}
			}
		}
	}
}

/*
TestAClockThatWentBackwardsIsNotADelivery.

A negative reading can only come from a clock moving backwards, and recording it
would report an event arriving before the thing it describes happened — worse
than a gap, because somebody would believe it.
*/
func TestAClockThatWentBackwardsIsNotADelivery(t *testing.T) {
	streams, reader := following(t, &stubOpen{})

	streams.Delivered(-2 * time.Second)
	if points := deliveries(t, reader); len(points) != 0 {
		t.Fatalf("a negative latency was recorded: %v", points)
	}

	streams.Delivered(30 * time.Millisecond)
	points := deliveries(t, reader)
	if len(points) != 1 || points[0].Count != 1 {
		t.Fatalf("a real latency was not recorded: %v", points)
	}
}

// TestHowManyStreamsAreOpenIsAsked: the broker already knows, so this reads it
// rather than keeping a second copy that can disagree.
func TestHowManyStreamsAreOpenIsAsked(t *testing.T) {
	open := &stubOpen{open: 4}
	_, reader := following(t, open)

	if got := streamGauge(t, reader); got != 4 {
		t.Errorf("active streams = %d, want 4", got)
	}

	open.open = 1
	if got := streamGauge(t, reader); got != 1 {
		t.Errorf("active streams = %d after one collection later, want 1", got)
	}
}

func streamGauge(t *testing.T, reader *sdkmetric.ManualReader) int64 {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "convia.event.streams.active" {
				continue
			}
			gauge, ok := recorded.Data.(metricdata.Gauge[int64])
			if !ok || len(gauge.DataPoints) == 0 {
				t.Fatalf("the active stream gauge is %T with no points", recorded.Data)
			}
			return gauge.DataPoints[0].Value
		}
	}
	t.Fatal("the active stream gauge was not collected")
	return 0
}
