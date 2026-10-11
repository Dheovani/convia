package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
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

	/*
		windows is the same, cut into stretches of [windowLength], which is what
		a run that breaks something on purpose is read by: a percentile over the
		whole run says that something went wrong, and the windows say when, for
		how long, and whether it came back.
	*/
	windows map[string]map[int64]*window
	now     func() time.Time

	// held is what a run is held to beyond the targets, when it is; see ceilings.
	held *ceilings
}

/*
ceilings are what a run may not exceed for it to pass as no worse than before:
a p95 per operation, named by the longest prefix that matches it, and how many
requests may fail outside setting up. It is `M25-012`, read from
ceilings.json, and it is what the nightly run fails on.
*/
type ceilings struct {
	P95      map[string]string `json:"p95"`
	Failures *int              `json:"failures"`

	parsed map[string]time.Duration
}

// readCeilings reads and checks a ceilings file.
func readCeilings(path string) (*ceilings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var held ceilings
	if err := json.Unmarshal(raw, &held); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	held.parsed = map[string]time.Duration{}
	for prefix, text := range held.P95 {
		ceiling, err := time.ParseDuration(text)
		if err != nil || ceiling <= 0 {
			return nil, fmt.Errorf("%s: the ceiling for %q is %q, which is not a duration", path, prefix, text)
		}
		held.parsed[prefix] = ceiling
	}

	return &held, nil
}

// ceiling is the p95 an operation may not exceed, and zero when nothing names it.
func (held *ceilings) ceiling(operation string) time.Duration {
	if held == nil {
		return 0
	}

	longest, ceiling := -1, time.Duration(0)
	for prefix, limit := range held.parsed {
		if strings.HasPrefix(operation, prefix) && len(prefix) > longest {
			longest, ceiling = len(prefix), limit
		}
	}
	return ceiling
}

// windowLength is how long one stretch of a timeline is.
const windowLength = 10 * time.Second

// window is what one operation did in one stretch of the run.
type window struct {
	samples []time.Duration
	failed  int
}

func newRecorder() *recorder {
	return &recorder{samples: map[string][]time.Duration{}, failures: map[string]map[string]int{},
		windows: map[string]map[int64]*window{}, now: time.Now}
}

// took records one successful operation.
func (record *recorder) took(operation string, duration time.Duration) {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	record.samples[operation] = append(record.samples[operation], duration)
	stretch := record.stretch(operation)
	stretch.samples = append(stretch.samples, duration)
}

// failed records one operation that did not succeed, and how.
func (record *recorder) failed(operation, how string) {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	if record.failures[operation] == nil {
		record.failures[operation] = map[string]int{}
	}
	record.failures[operation][how]++
	record.stretch(operation).failed++
}

// stretch is the window an operation is in now. The lock is held.
func (record *recorder) stretch(operation string) *window {
	at := record.now().UnixNano() / int64(windowLength)
	if record.windows[operation] == nil {
		record.windows[operation] = map[int64]*window{}
	}
	if record.windows[operation][at] == nil {
		record.windows[operation][at] = &window{}
	}
	return record.windows[operation][at]
}

/*
timeline writes, for every operation that failed or missed its target in some
stretch of [from, to], one line per stretch: what succeeded, what failed, and
the p95 of what succeeded. An operation that was fine throughout is left out,
which is what keeps the lines that matter on one screen.
*/
func (record *recorder) timeline(out io.Writer, from, to time.Time) {
	record.mutex.Lock()
	defer record.mutex.Unlock()

	first, last := from.UnixNano()/int64(windowLength), to.UnixNano()/int64(windowLength)
	operations := make([]string, 0, len(record.windows))
	for operation := range record.windows {
		operations = append(operations, operation)
	}
	sort.Strings(operations)

	fmt.Fprintf(out, "\nover time, every %v from the start of the load, for whatever failed or was slow\n", windowLength)
	quiet := true
	for _, operation := range operations {
		troubled := false
		for at := first; at <= last; at++ {
			if stretch := record.windows[operation][at]; stretch != nil && stretch.troubled(target(operation)) {
				troubled = true
				break
			}
		}
		if !troubled {
			continue
		}
		quiet = false
		fmt.Fprintf(out, "  %s\n", operation)
		for at := first; at <= last; at++ {
			stretch := record.windows[operation][at]
			if stretch == nil {
				stretch = &window{}
			}
			sorted := slices.Clone(stretch.samples)
			slices.Sort(sorted)
			fmt.Fprintf(out, "    %+5ds  %6d ok  %6d failed  p95 %9s\n",
				(at-first)*int64(windowLength/time.Second), len(sorted), stretch.failed, rounded(percentile(sorted, 0.95)))
		}
	}
	if quiet {
		fmt.Fprintf(out, "  nothing failed, and nothing missed its target\n")
	}
}

// troubled reports a stretch with a failure, or a p95 over the target.
func (stretch *window) troubled(goal time.Duration) bool {
	if stretch.failed > 0 {
		return true
	}
	if goal == 0 {
		return false
	}
	sorted := slices.Clone(stretch.samples)
	slices.Sort(sorted)
	return percentile(sorted, 0.95) > goal
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
	fmt.Fprintf(out, "\n%-46s %9s %8s %9s %9s %9s %9s  %s\n", "operation", "count", "per s", "p50", "p95", "p99", "target", "")
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
		if limit := record.held.ceiling(operation); limit > 0 && p95 > limit && verdict != "MISSED" {
			verdict, met = fmt.Sprintf("REGRESSED, over %v", limit), false
		}
		goalText := "-"
		if goal > 0 {
			goalText = goal.String()
		}
		fmt.Fprintf(out, "%-46s %9d %8.1f %9s %9s %9s %9s  %s\n", operation, len(sorted),
			float64(len(sorted))/elapsed.Seconds(), rounded(percentile(sorted, 0.5)), rounded(p95),
			rounded(percentile(sorted, 0.99)), goalText, verdict)
	}

	failed := 0
	if len(record.failures) > 0 {
		fmt.Fprintf(out, "\nfailures\n")
		for operation, kinds := range record.failures {
			for how, count := range kinds {
				fmt.Fprintf(out, "  %-32s %-40s %d\n", operation, how, count)
				if !strings.HasPrefix(operation, "setup") {
					failed += count
				}
			}
		}
	}

	if record.held != nil && record.held.Failures != nil && failed > *record.held.Failures {
		fmt.Fprintf(out, "\nREGRESSED: %d requests failed under load, and at most %d may\n", failed, *record.held.Failures)
		met = false
	}

	return met
}

func rounded(duration time.Duration) string {
	if duration >= 10*time.Millisecond {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(10 * time.Microsecond).String()
}
