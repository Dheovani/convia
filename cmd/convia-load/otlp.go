package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

/*
observatory receives what the Convia under load says about itself, over the
same OTLP/HTTP it would send to any collector.

It is here rather than a collector beside the run because what a run needs
from Convia's metrics is small and specific: the server's own view of how long
requests took, which is how the targets in docs/scaling.md are stated, and what
the process cost -- goroutines, memory, allocation, processor. Pointing
CONVIA_METRICS_ENDPOINT at this is the whole of the wiring.

Every sample is kept with its time, because a run has two phases that cost
very different things: building the world registers hundreds of people, and a
password hash is the most expensive thing Convia does, so a report that mixed
the two would put the cost of setting up under the load's name. The report is
made over the load's window alone: peaks within it, and a counter's rate from
the first sample inside it to the last.

Histograms are cumulative, so the window is read as the difference between the
last point before it opened and the last inside it.
*/
type observatory struct {
	mutex      sync.Mutex
	histograms map[string][]histogramAt
	series     map[string][]sample
}

type histogramAt struct {
	point *metrics.HistogramDataPoint
	at    time.Time
}

type sample struct {
	value float64
	at    time.Time
}

func newObservatory() *observatory {
	return &observatory{
		histograms: map[string][]histogramAt{},
		series:     map[string][]sample{},
	}
}

// ServeHTTP accepts one export.
func (seen *observatory) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	var body io.Reader = request.Body
	if request.Header.Get("Content-Encoding") == "gzip" {
		unzipped, err := gzip.NewReader(request.Body)
		if err != nil {
			http.Error(response, "unreadable", http.StatusBadRequest)
			return
		}
		defer unzipped.Close()
		body = unzipped
	}
	read, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		http.Error(response, "unreadable", http.StatusBadRequest)
		return
	}

	var export colmetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(read, &export); err != nil {
		http.Error(response, "unreadable", http.StatusBadRequest)
		return
	}
	seen.take(&export)

	answer, _ := proto.Marshal(&colmetrics.ExportMetricsServiceResponse{})
	response.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = response.Write(answer)
}

func (seen *observatory) take(export *colmetrics.ExportMetricsServiceRequest) {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()

	for _, resource := range export.ResourceMetrics {
		instance := attributeOf(resource.GetResource().GetAttributes(), "service.instance.id")
		for _, scope := range resource.ScopeMetrics {
			for _, metric := range scope.Metrics {
				switch data := metric.Data.(type) {
				case *metrics.Metric_Histogram:
					for _, point := range data.Histogram.DataPoints {
						key := metric.Name + "|" + instance + "|" + attributeOf(point.Attributes, "http.request.method") +
							" " + attributeOf(point.Attributes, "http.route") + "|" +
							attributeOf(point.Attributes, "http.response.status_code")
						seen.histograms[key] = append(seen.histograms[key],
							histogramAt{point: point, at: time.Unix(0, int64(point.TimeUnixNano))})
					}
				case *metrics.Metric_Sum:
					for _, point := range data.Sum.DataPoints {
						seen.note(metric.Name+"|"+instance, numberOf(point), point.TimeUnixNano)
					}
				case *metrics.Metric_Gauge:
					for _, point := range data.Gauge.DataPoints {
						seen.note(metric.Name+"|"+instance, numberOf(point), point.TimeUnixNano)
					}
				}
			}
		}
	}
}

func (seen *observatory) note(key string, value float64, at uint64) {
	seen.series[key] = append(seen.series[key], sample{value, time.Unix(0, int64(at))})
}

// within answers the samples of one series taken between from and to.
func within(samples []sample, from, to time.Time) []sample {
	var inside []sample
	for _, each := range samples {
		if !each.at.Before(from) && !each.at.After(to) {
			inside = append(inside, each)
		}
	}
	return inside
}

/*
during reads what a cumulative histogram gained between from and to: the last
point at or before to, less the last point before from. A series that only
began inside the window has nothing to subtract.
*/
func during(points []histogramAt, from, to time.Time) (counts []uint64, bounds []float64, total uint64, ok bool) {
	var before, last *metrics.HistogramDataPoint
	for _, each := range points {
		if each.at.Before(from) {
			before = each.point
		}
		if !each.at.After(to) {
			last = each.point
		}
	}
	if last == nil {
		return nil, nil, 0, false
	}
	counts = append([]uint64(nil), last.BucketCounts...)
	total = last.Count
	if before != nil && len(before.BucketCounts) == len(counts) {
		for index := range counts {
			counts[index] -= before.BucketCounts[index]
		}
		total -= before.Count
	}
	return counts, last.ExplicitBounds, total, total > 0
}

func numberOf(point *metrics.NumberDataPoint) float64 {
	if value, ok := point.Value.(*metrics.NumberDataPoint_AsDouble); ok {
		return value.AsDouble
	}
	return float64(point.GetAsInt())
}

func attributeOf(attributes []*common.KeyValue, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			if text := attribute.Value.GetStringValue(); text != "" {
				return text
			}
			if number := attribute.Value.GetIntValue(); number != 0 {
				return fmt.Sprint(number)
			}
		}
	}
	return ""
}

/*
upperBound reads the q-th quantile of a cumulative histogram as the boundary of
the bucket it falls in. It is an upper bound, not an estimate, and the report
says so: "under 250ms" is what a bucket can honestly claim.
*/
func upperBound(counts []uint64, bounds []float64, q float64) (float64, bool) {
	var total uint64
	for _, count := range counts {
		total += count
	}
	if total == 0 {
		return 0, false
	}
	needed := uint64(float64(total)*q + 0.999999)
	var running uint64
	for index, count := range counts {
		running += count
		if running >= needed {
			if index < len(bounds) {
				return bounds[index], true
			}
			return 0, false
		}
	}
	return 0, false
}

// report writes the server's view and what the process cost, between from and to.
func (seen *observatory) report(out io.Writer, from, to time.Time) {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()

	if len(seen.histograms) == 0 && len(seen.series) == 0 {
		fmt.Fprintf(out, "\nConvia sent no metrics: is CONVIA_METRICS_ENDPOINT pointed here, and OTEL_METRIC_EXPORT_INTERVAL short?\n")
		return
	}

	type row struct {
		name   string
		counts []uint64
		bounds []float64
		total  uint64
	}
	merged := map[string]*row{}
	for key, points := range seen.histograms {
		counts, bounds, total, ok := during(points, from, to)
		if !ok {
			continue
		}
		parts := strings.Split(key, "|")
		// A stream is a request that lasts as long as the connection does, so
		// its "duration" is how long somebody listened, not how long Convia took.
		if strings.HasSuffix(parts[2], "/events") {
			continue
		}
		name := parts[0] + " " + parts[2]
		if parts[0] == "convia.event.delivery.duration" {
			name = parts[0]
		}
		if strings.HasPrefix(parts[3], "4") || strings.HasPrefix(parts[3], "5") {
			name += " (" + parts[3] + ")"
		}
		merging := merged[name]
		if merging == nil {
			merging = &row{name: name, counts: make([]uint64, len(counts)), bounds: bounds}
			merged[name] = merging
		}
		for index, count := range counts {
			if index < len(merging.counts) {
				merging.counts[index] += count
			}
		}
		merging.total += total
	}

	rows := make([]*row, 0, len(merged))
	for _, each := range merged {
		rows = append(rows, each)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })

	fmt.Fprintf(out, "\nas Convia measured it during the load (p95 is the bucket boundary it falls under)\n")
	for _, each := range rows {
		bound, ok := upperBound(each.counts, each.bounds, 0.95)
		text := "over the last boundary"
		if ok {
			text = "under " + (time.Duration(bound * float64(time.Second))).String()
		}
		fmt.Fprintf(out, "  %-70s %8d  p95 %s\n", each.name, each.total, text)
	}

	fmt.Fprintf(out, "\nwhat the load cost, per instance\n")
	instances := map[string]bool{}
	for key := range seen.series {
		instances[strings.SplitN(key, "|", 2)[1]] = true
	}
	for instance := range instances {
		at := func(name string) string { return name + "|" + instance }
		inside := func(name string) []sample { return within(seen.series[at(name)], from, to) }
		peak := func(name string) float64 {
			highest := 0.0
			for _, each := range inside(name) {
				highest = max(highest, each.value)
			}
			return highest
		}
		rate := func(name string) float64 {
			samples := inside(name)
			if len(samples) < 2 {
				return 0
			}
			first, last := samples[0], samples[len(samples)-1]
			span := last.at.Sub(first.at).Seconds()
			if span <= 0 {
				return 0
			}
			return (last.value - first.value) / span
		}
		fmt.Fprintf(out, "  instance %s\n", instance)
		fmt.Fprintf(out, "    goroutines          peak %.0f\n", peak("go.goroutine.count"))
		fmt.Fprintf(out, "    memory held         peak %.0f MB\n", peak("go.memory.used")/1e6)
		fmt.Fprintf(out, "    live heap           peak %.0f MB\n", peak("convia.runtime.heap.objects")/1e6)
		fmt.Fprintf(out, "    allocation rate     %.1f MB/s\n", rate("go.memory.allocated")/1e6)
		fmt.Fprintf(out, "    collections         %.2f per second\n", rate("convia.runtime.gc.cycles"))
		fmt.Fprintf(out, "    database waits      %.1f per second\n", rate("convia.database.pool.waits"))
		fmt.Fprintf(out, "    event streams       peak %.0f\n", peak("convia.event.streams.active"))
	}
}
