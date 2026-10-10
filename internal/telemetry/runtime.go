package telemetry

import (
	"context"
	"fmt"
	"runtime/metrics"

	"go.opentelemetry.io/otel/metric"
)

/*
samples are what the runtime is asked for, by the names runtime/metrics gives
them. Each is read once per collection, all together, so the five numbers an
operator compares come from one moment rather than five.
*/
var samples = []string{
	"/sched/goroutines:goroutines",
	"/memory/classes/total:bytes",
	"/memory/classes/heap/released:bytes",
	"/memory/classes/heap/objects:bytes",
	"/gc/heap/allocs:bytes",
	"/gc/cycles/total:gc-cycles",
}

/*
Run registers what this process reports about itself: goroutines, memory, how
much it has allocated, and how often it has collected garbage.

It is `M25-004`. Every other instrument in this package is about what Convia is
carrying -- calls, streams, requests -- and none of them can say whether the
process carrying it is healthy: a leak of goroutines, a heap that only grows,
an allocation rate that makes every request pay for the collector. Those are
the numbers a load test reads to say what a given volume costs, and the ones an
operator reads when an instance slows down with no request to blame.

**They are read from the runtime when somebody collects**, through
runtime/metrics, which is the runtime's supported way to be asked and costs
nothing between collections. Where OpenTelemetry's conventions for the Go
runtime name one of them -- goroutines, memory used, memory allocated -- that is
its name, so a dashboard built for any Go service reads it. The others have no
convention and carry Convia's own prefix rather than one that would look like a
standard.

**Processor time is deliberately not among them.** runtime/metrics offers an
estimate, and the first load run showed what it is worth: the estimate is only
brought up to date when the collector runs, so an instance under steady load
that allocates little reported no processor time at all. A number that is right
only some of the time is worse than its absence. Processor time belongs to the
operating system, which every container platform already reports.
*/
func Run(provider metric.MeterProvider) error {
	meter := provider.Meter(Name)

	goroutines, err := meter.Int64ObservableUpDownCounter("go.goroutine.count",
		metric.WithDescription("Goroutines that currently exist."), metric.WithUnit("{goroutine}"))
	if err != nil {
		return fmt.Errorf("build the goroutine gauge: %w", err)
	}

	/*
		Memory in two readings, because they answer different questions. What
		the runtime holds from the operating system is what a container limit
		is measured against; what live objects use is what the program asked
		for, and the difference between them is the collector's headroom.
	*/
	memory, err := meter.Int64ObservableUpDownCounter("go.memory.used",
		metric.WithDescription("Memory the Go runtime holds from the operating system, less what it has released."),
		metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("build the memory gauge: %w", err)
	}
	heap, err := meter.Int64ObservableUpDownCounter("convia.runtime.heap.objects",
		metric.WithDescription("Memory live heap objects occupy."), metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("build the heap gauge: %w", err)
	}

	allocated, err := meter.Int64ObservableCounter("go.memory.allocated",
		metric.WithDescription("Memory allocated on the heap since the process started."), metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("build the allocation counter: %w", err)
	}
	collections, err := meter.Int64ObservableCounter("convia.runtime.gc.cycles",
		metric.WithDescription("Garbage collections since the process started."), metric.WithUnit("{cycle}"))
	if err != nil {
		return fmt.Errorf("build the collection counter: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		read := make([]metrics.Sample, len(samples))
		for index, name := range samples {
			read[index].Name = name
		}
		metrics.Read(read)

		/*
			Memory used is the total the runtime has mapped less what it has
			given back, which is how OpenTelemetry defines go.memory.used and
			what a container limit actually meets. The total alone counts pages
			the operating system already has again: the first load run read
			627 MB from it while Docker saw 145 MB in the container.
		*/
		observer.ObserveInt64(goroutines, int64(read[0].Value.Uint64()))
		observer.ObserveInt64(memory, int64(read[1].Value.Uint64()-read[2].Value.Uint64()))
		observer.ObserveInt64(heap, int64(read[3].Value.Uint64()))
		observer.ObserveInt64(allocated, int64(read[4].Value.Uint64()))
		observer.ObserveInt64(collections, int64(read[5].Value.Uint64()))
		return nil
	}, goroutines, memory, heap, allocated, collections)
	if err != nil {
		return fmt.Errorf("register the runtime instruments: %w", err)
	}
	return nil
}
