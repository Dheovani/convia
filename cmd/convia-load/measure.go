package main

import (
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

/*
recorder keeps every latency observed, by operation, and every failure.

It keeps the samples rather than a histogram because a load run is minutes
long and the whole point is the tail: a percentile read from exact samples is
the percentile, where one read from buckets is the bucket it fell in. A run of
the sizes in docs/scaling.md keeps a few hundred thousand durations, which is a
few megabytes.
*/
type recorder struct {
	mutex    sync.Mutex
	samples  map[string][]time.Duration
	failures map[string]map[string]int
}

func newRecorder() *recorder {
	return &recorder{samples: map[string][]time.Duration{}, failures: map[string]map[string]int{}}
}

// took records one successful operation.
func (record *recorder) took(operation string, duration time.Duration) {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	record.samples[operation] = append(record.samples[operation], duration)
}

// failed records one operation that did not succeed, and how.
func (record *recorder) failed(operation, how string) {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	if record.failures[operation] == nil {
		record.failures[operation] = map[string]int{}
	}
	record.failures[operation][how]++
}

// percentile reads the q-th quantile of sorted durations, nearest rank.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(rank, len(sorted)-1))]
}

/*
target is the p95 an operation is held to, from docs/scaling.md, and zero for
one that has none. The report marks each row against it, so a run answers
"was it met" rather than leaving somebody to compare columns.
*/
func target(operation string) time.Duration {
	switch {
	case strings.HasPrefix(operation, "event "):
		return 250 * time.Millisecond
	case strings.HasPrefix(operation, "read "):
		return 200 * time.Millisecond
	case strings.HasPrefix(operation, "write "):
		return 300 * time.Millisecond
	case strings.HasPrefix(operation, "sign in"):
		return time.Second
	default:
		return 0
	}
}

// report writes one row per operation, slowest first, and the failures beneath.
func (record *recorder) report(out io.Writer, elapsed time.Duration) bool {
	record.mutex.Lock()
	defer record.mutex.Unlock()

	operations := make([]string, 0, len(record.samples))
	for operation := range record.samples {
		operations = append(operations, operation)
	}
	sort.Strings(operations)

	met := true
	fmt.Fprintf(out, "\n%-34s %9s %8s %9s %9s %9s %9s  %s\n", "operation", "count", "per s", "p50", "p95", "p99", "target", "")
	for _, operation := range operations {
		sorted := slices.Clone(record.samples[operation])
		slices.Sort(sorted)
		p95 := percentile(sorted, 0.95)

		verdict, goal := "", target(operation)
		if goal > 0 {
			verdict = "met"
			if p95 > goal {
				verdict, met = "MISSED", false
			}
		}
		goalText := "-"
		if goal > 0 {
			goalText = goal.String()
		}
		fmt.Fprintf(out, "%-34s %9d %8.1f %9s %9s %9s %9s  %s\n", operation, len(sorted),
			float64(len(sorted))/elapsed.Seconds(), rounded(percentile(sorted, 0.5)), rounded(p95),
			rounded(percentile(sorted, 0.99)), goalText, verdict)
	}

	if len(record.failures) > 0 {
		fmt.Fprintf(out, "\nfailures\n")
		for operation, kinds := range record.failures {
			for how, count := range kinds {
				fmt.Fprintf(out, "  %-32s %-40s %d\n", operation, how, count)
			}
		}
	}
	return met
}

func rounded(duration time.Duration) string {
	if duration >= 10*time.Millisecond {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(10 * time.Microsecond).String()
}
