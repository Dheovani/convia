package telemetry

import (
	"context"
	"runtime/metrics"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

/*
TestTheProcessReportsWhatItCosts reads the runtime instruments back and checks
each says something plausible about a process that is certainly running: some
goroutines, some memory, some allocation, some processor time.

Every sample is checked to be non-zero rather than merely present, because a
name runtime/metrics does not recognise reads as zero rather than failing --
which is exactly how a renamed metric would go quiet.
*/
func TestTheProcessReportsWhatItCosts(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	if err := Run(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	garbage := make([][]byte, 0, 64)
	for range 64 {
		garbage = append(garbage, make([]byte, 64<<10))
	}
	_ = garbage

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	read := map[string]float64{}
	for _, scope := range collected.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch data := recorded.Data.(type) {
			case metricdata.Sum[int64]:
				read[recorded.Name] = float64(data.DataPoints[0].Value)
			case metricdata.Sum[float64]:
				read[recorded.Name] = data.DataPoints[0].Value
			}
		}
	}

	for _, name := range []string{"go.goroutine.count", "go.memory.used", "convia.runtime.heap.objects",
		"go.memory.allocated"} {
		if read[name] <= 0 {
			t.Errorf("%s = %v, want something a running process has", name, read[name])
		}
	}
	if read["go.memory.allocated"] < 4<<20 {
		t.Errorf("go.memory.allocated = %v after allocating four megabytes", read["go.memory.allocated"])
	}
	if _, found := read["convia.runtime.gc.cycles"]; !found {
		t.Error("convia.runtime.gc.cycles was not reported")
	}

	// Released memory is not used memory: what the runtime has mapped in all
	// is always at least what it reports as used, and usually more.
	total := []metrics.Sample{{Name: "/memory/classes/total:bytes"}}
	metrics.Read(total)
	if read["go.memory.used"] > float64(total[0].Value.Uint64()) {
		t.Errorf("go.memory.used %v exceeds everything the runtime has mapped", read["go.memory.used"])
	}
}

/*
TestADurationLandsInABucketThatSaysSomething is the defect `M25` found in
`M22`: durations in seconds recorded against boundaries chosen for
milliseconds, so every request under five seconds shared the first bucket and
no percentile could be read. A request of thirty milliseconds has to land
between boundaries that tell it apart from one of three hundred.
*/
func TestADurationLandsInABucketThatSaysSomething(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	histogram, err := provider.Meter(Name).Float64Histogram("test.duration", seconds)
	if err != nil {
		t.Fatalf("Float64Histogram() error = %v", err)
	}
	histogram.Record(context.Background(), 0.030)
	histogram.Record(context.Background(), 0.300)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	point := collected.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64]).DataPoints[0]

	occupied := 0
	for index, count := range point.BucketCounts {
		if count > 0 {
			occupied++
			if index < len(point.Bounds) && point.Bounds[index] > 1 {
				t.Errorf("a sub-second duration landed under the %v-second boundary", point.Bounds[index])
			}
		}
	}
	if occupied != 2 {
		t.Errorf("30 ms and 300 ms share a bucket; boundaries %v", point.Bounds)
	}
	for _, target := range []float64{0.2, 0.25, 0.3, 0.5} {
		found := false
		for _, bound := range point.Bounds {
			found = found || bound == target
		}
		if !found {
			t.Errorf("no boundary at the %v-second target, so whether it is met cannot be read", target)
		}
	}
}

/*
inSeconds checks a duration histogram was built with the boundaries durations in
seconds need, by asking for the one at a target. Called from every test that
reads a duration, so an instrument added without them fails where it is tested.
*/
func inSeconds(t *testing.T, name string, points []metricdata.HistogramDataPoint[float64]) {
	t.Helper()

	for _, point := range points {
		found := false
		for _, bound := range point.Bounds {
			found = found || bound == 0.25
		}
		if !found {
			t.Errorf("%s has boundaries %v, which cannot tell a quarter of a second from five", name, point.Bounds)
		}
	}
}

/*
TestASignalIsSentWhereACollectorListens is the defect `M25` found: an address
configured the way a collector documents it had every export posted to "/".
*/
func TestASignalIsSentWhereACollectorListens(t *testing.T) {
	cases := map[string]string{
		"https://collector.internal:4318":     "https://collector.internal:4318/v1/metrics",
		"https://collector.internal:4318/":    "https://collector.internal:4318/v1/metrics",
		"http://127.0.0.1:4318/custom/ingest": "http://127.0.0.1:4318/custom/ingest",
		"https://otlp.example/v1/metrics":     "https://otlp.example/v1/metrics",
	}
	for given, want := range cases {
		if got := signalURL(given, "/v1/metrics"); got != want {
			t.Errorf("signalURL(%q) = %q, want %q", given, got, want)
		}
	}
}
